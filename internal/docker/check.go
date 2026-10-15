// Package docker talks to the docker CLI (through execx) and diagnoses
// common setup problems.
package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/ashfaqc137/ownci/internal/execx"
)

// Error is a Docker problem with an actionable hint. The cli layer converts
// it to a UserError (this package must not import cli).
type Error struct{ Msg, Hint string }

func (e *Error) Error() string    { return e.Msg }
func (e *Error) UserHint() string { return e.Hint }

// Status is the result of Check.
type Status struct {
	CLIPath         string
	CLIFound        bool
	DesktopShim     bool // the "WSL integration is off" shim was detected
	DaemonReachable bool
	ServerVersion   string
	ServerOS        string // must be "linux"
	Err             error  // first failure; an *Error when it carries a hint
}

// OK reports whether Docker is usable for OwnCI.
func (s Status) OK() bool { return s.Err == nil }

// Client groups docker operations behind an execx.Runner. t08 adds the
// cleanup container run here.
type Client struct{ Exec execx.Runner }

// Check diagnoses the local Docker installation.
func (c Client) Check(ctx context.Context) Status { return Check(ctx, c.Exec) }

const (
	checkTimeout   = 10 * time.Second
	excerptLimit   = 300
	shimMarker     = "could not be found in this WSL 2 distro"
	installHint    = "Install Docker: on WSL use Docker Desktop with WSL integration; on Linux install Docker Engine (https://docs.docker.com/engine/install/)."
	daemonHint     = "Start Docker (Docker Desktop: launch the app; Linux: sudo systemctl start docker), then retry."
	permissionHint = "Add your user to the docker group: sudo usermod -aG docker $USER, then log out and back in."
	windowsHint    = "Docker is in Windows-containers mode; switch to Linux containers (Docker Desktop tray icon → Switch to Linux containers)."
)

// Check runs `docker version` and classifies the outcome.
func Check(ctx context.Context, r execx.Runner) Status {
	return check(ctx, r, os.Getenv("WSL_DISTRO_NAME"))
}

func check(ctx context.Context, r execx.Runner, distro string) Status {
	var st Status
	path, err := r.LookPath("docker")
	if err != nil {
		st.Err = &Error{Msg: "docker command not found on PATH", Hint: installHint}
		return st
	}
	st.CLIPath, st.CLIFound = path, true

	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	res, err := r.Run(ctx, execx.Cmd{Name: "docker", Args: []string{"version", "--format", "{{json .}}"}})
	if err != nil {
		st.Err = &Error{
			Msg:  fmt.Sprintf("running docker version: %v", err),
			Hint: "Check that Docker responds: run 'docker version' yourself.",
		}
		return st
	}

	combined := string(res.Stdout) + "\n" + string(res.Stderr)
	lower := strings.ToLower(combined)
	switch {
	case strings.Contains(combined, shimMarker):
		st.DesktopShim = true
		name := "this distro"
		if distro != "" {
			name = "'" + distro + "'"
		}
		st.Err = &Error{
			Msg:  "docker is not available in this WSL 2 distro (Docker Desktop WSL integration is off)",
			Hint: "Enable WSL integration: Docker Desktop → Settings → Resources → WSL integration → enable " + name + " → Apply & restart.",
		}
	case strings.Contains(lower, "permission denied") && strings.Contains(lower, "docker.sock"):
		st.Err = &Error{Msg: "permission denied connecting to the Docker socket", Hint: permissionHint}
	case strings.Contains(lower, "cannot connect to the docker daemon") || strings.Contains(lower, "error during connect"):
		st.Err = &Error{Msg: "cannot connect to the Docker daemon", Hint: daemonHint}
	case res.ExitCode != 0:
		st.Err = &Error{
			Msg:  fmt.Sprintf("docker version failed (exit %d): %s", res.ExitCode, excerpt(combined)),
			Hint: "Run 'docker version' yourself to see the full error.",
		}
	default:
		parseServer(&st, res.Stdout)
	}
	return st
}

func parseServer(st *Status, stdout []byte) {
	var v struct {
		Server *struct {
			Version string
			Os      string
		}
	}
	if err := json.Unmarshal(stdout, &v); err != nil {
		st.Err = &Error{
			Msg:  "unexpected output from docker version: " + excerpt(string(stdout)),
			Hint: "Run 'docker version' yourself and check that the docker CLI is working.",
		}
		return
	}
	if v.Server == nil || v.Server.Version == "" {
		st.Err = &Error{Msg: "docker reported no server (daemon not running?)", Hint: daemonHint}
		return
	}
	st.DaemonReachable = true
	st.ServerVersion, st.ServerOS = v.Server.Version, v.Server.Os
	if st.ServerOS != "linux" {
		st.Err = &Error{Msg: fmt.Sprintf("Docker server OS is %q, need linux", st.ServerOS), Hint: windowsHint}
	}
}

func excerpt(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > excerptLimit {
		return s[:excerptLimit] + "…"
	}
	return s
}
