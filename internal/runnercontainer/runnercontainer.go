// Package runnercontainer runs the official runner inside a Docker container
// (container-runner mode, D13): the `docker run` command line, the entrypoint
// that maps the container user to the host user, and the checks the mode
// depends on.
//
// The runner directory is mounted at the identical path inside the container.
// The runner hands its own paths (_work, externals, …) to the host Docker
// daemon as bind-mount sources for job containers, so they must mean the same
// thing on both sides (tasks/future.md F2).
package runnercontainer

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ashfaqc137/ownci/internal/execx"
	"github.com/ashfaqc137/ownci/internal/hooks"
)

// DefaultImage is the official runner image (Ubuntu 24.04 with the runner's
// dependencies, git and the docker CLI), pinned by its multi-arch index
// digest (looked up with `docker buildx imagetools inspect
// ghcr.io/actions/actions-runner:2.338.0`, 2026-10-22). Only the OS and
// tools of the image are used: the runner itself runs from the mounted
// runner directory, so it keeps self-updating there.
const DefaultImage = "ghcr.io/actions/actions-runner:2.338.0@sha256:4ffadc0002b2581327e06101fc8c06cd189232baf79fe561fac9caeb76f5e807"

// SocketPath is the Docker socket as the daemon sees it, mounted into the
// runner container (job containers get it from the runner anyway).
const SocketPath = "/var/run/docker.sock"

// EntrypointArg0 is $0 of the inline entrypoint; the fake docker in tests
// recognizes the runner container by it.
const EntrypointArg0 = "ownci-entrypoint"

//go:embed entrypoint.sh
var entrypointScript string

// Error is a container-mode problem with an actionable hint.
type Error struct{ Msg, Hint string }

func (e *Error) Error() string    { return e.Msg }
func (e *Error) UserHint() string { return e.Hint }

// ContainerName is the fixed name of a runner's container, so a leftover can
// always be found and removed. Container names are global to the Docker
// daemon, so two data directories with the same runner name share it; the
// DataDirLabel tells them apart.
func ContainerName(runner string) string { return "ownci-" + runner }

// DataDirLabel records which OwnCI data directory started a runner container.
const DataDirLabel = "ownci.datadir"

// ErrForeignContainer means the runner's container belongs to another OwnCI
// data directory (a runner of the same name set up for another repository).
var ErrForeignContainer = errors.New("container belongs to another OwnCI data directory")

// ForeignHint is the hint for ErrForeignContainer.
const ForeignHint = "Each OwnCI data directory needs its own runner name in container-runner mode. " +
	"Give this one another name: 'ownci uninstall --unregister', then 'ownci setup --repo owner/name --name <unique name>'."

// Spec describes one runner container.
type Spec struct {
	Runner    string // runner name
	RunnerDir string // absolute and symlink-free (see CheckPath)
	DataDir   string // the OwnCI data directory, recorded as DataDirLabel
	Image     string
	UID, GID  int // the host user the runner runs as inside the container
}

// Args returns the arguments of `docker run` (without "docker") for s.
//
// The container stays attached (no -d), so the supervisor reads the runner's
// output and exit code exactly as in host mode; --init forwards SIGINT to
// run.sh. `--once` makes the runner take a single job and exit, so every job
// without `container:` starts in a fresh container. The cleanup hook is
// mounted read-only over itself so a job cannot disable it.
func Args(s Spec) []string {
	dir, hook := s.RunnerDir, hooks.JobCompletedPath(s.RunnerDir)
	return []string{
		"run", "--rm", "--init", "--pull", "never",
		"--name", ContainerName(s.Runner),
		"--label", "ownci.managed=true", "--label", "ownci.runner=" + s.Runner,
		"--label", DataDirLabel + "=" + s.DataDir,
		"--user", "0:0",
		"-v", dir + ":" + dir, "-w", dir,
		"-v", hook + ":" + hook + ":ro",
		"-v", SocketPath + ":" + SocketPath,
		"-e", "OWNCI_UID=" + strconv.Itoa(s.UID), "-e", "OWNCI_GID=" + strconv.Itoa(s.GID),
		"-e", "RUNNER_MANUALLY_TRAP_SIG=1", "-e", "ACTIONS_RUNNER_RETURN_VERSION_DEPRECATED_EXIT_CODE=1",
		"--entrypoint", "bash", s.Image,
		"-c", entrypointScript, EntrypointArg0, "./run.sh", "--once",
	}
}

// LaunchFailed reports whether a `docker run` exit code means Docker could not
// start the container (125: daemon error such as a missing image or a refused
// mount; 126/127: the entrypoint cannot be run). Retrying does not help.
func LaunchFailed(code int) bool { return code == 125 || code == 126 || code == 127 }

var windowsMount = regexp.MustCompile(`^/mnt/[a-zA-Z](/|$)`)

// CheckPath refuses a runner directory the Docker daemon cannot bind-mount at
// the identical path: relative or unclean paths, ':' or ',' (mount syntax),
// symlinked components (the daemon would mount the target under another
// name), and WSL's Windows drives.
func CheckPath(dir string) error {
	hint := "Use a plain Linux path for the data directory, e.g. --data-dir ~/.local/share/ownci (resolved, without symlinks)."
	switch {
	case !filepath.IsAbs(dir) || filepath.Clean(dir) != dir:
		return &Error{Msg: fmt.Sprintf("runner directory %q is not a clean absolute path", dir), Hint: hint}
	case strings.ContainsAny(dir, ":,"):
		return &Error{Msg: fmt.Sprintf("runner directory %q contains ':' or ',', which Docker mounts cannot express", dir), Hint: hint}
	case windowsMount.MatchString(dir):
		return &Error{Msg: fmt.Sprintf("runner directory %q is on a Windows drive, which Docker cannot mount reliably", dir), Hint: hint}
	}
	existing := dir
	for {
		if _, err := os.Lstat(existing); err == nil {
			break
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			break
		}
		existing = parent
	}
	real, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return &Error{Msg: fmt.Sprintf("resolving %s: %v", existing, err), Hint: hint}
	}
	if real != existing {
		return &Error{
			Msg:  fmt.Sprintf("runner directory %s goes through a symlink (%s is really %s); the runner container needs the real path", dir, existing, real),
			Hint: "Use --data-dir with the resolved path (" + real + "/…), or set OWNCI_DATA_DIR to it.",
		}
	}
	return nil
}

const (
	dockerTimeout = 30 * time.Second
	stopGrace     = 30 // seconds for the runner to shut down after SIGINT
)

// Remove stops the runner's container gracefully, then force-removes it. A
// missing container is not an error.
//
// The graceful stop matters: SIGINT lets the listener close its GitHub
// session. A container that is only killed leaves the session behind, and the
// next runner then waits about a minute on "A session for this runner already
// exists … Conflict" (seen 2026-10-22 after `kill -9` of the supervisor).
//
// A container another data directory started (see Owned) is left alone and
// reported as ErrForeignContainer: it is another repository's runner.
func Remove(ctx context.Context, ex execx.Runner, runner, dataDir string) error {
	name := ContainerName(runner)
	info, err := Inspect(ctx, ex, runner)
	switch {
	case errors.Is(err, ErrNoContainer):
		return nil
	case err == nil && !info.Owned(dataDir):
		return fmt.Errorf("%s: %w (%s)", name, ErrForeignContainer, info.DataDir)
	}
	// Other inspect failures (daemon down, …) surface from stop/rm below.
	stopCtx, cancel := context.WithTimeout(ctx, dockerTimeout+stopGrace*time.Second)
	_, _ = ex.Run(stopCtx, execx.Cmd{Name: "docker", Args: []string{"stop", "--signal", "SIGINT", "--time", strconv.Itoa(stopGrace), name}})
	cancel()

	ctx, cancel = context.WithTimeout(ctx, dockerTimeout)
	defer cancel()
	res, err := ex.Run(ctx, execx.Cmd{Name: "docker", Args: []string{"rm", "--force", name}})
	if err != nil {
		return fmt.Errorf("removing container %s: %w", name, err)
	}
	if res.ExitCode != 0 && !strings.Contains(string(res.Stderr), "No such container") {
		return fmt.Errorf("removing container %s: %s", name, strings.TrimSpace(string(res.Stderr)))
	}
	return nil
}

// ErrNoContainer means the runner's container does not exist.
var ErrNoContainer = errors.New("no such container")

// Info is what Docker reports about a runner container.
type Info struct {
	State   string `json:"state"` // running, exited, …
	Image   string `json:"image"`
	DataDir string `json:"dataDir,omitempty"` // DataDirLabel; empty for containers of older versions
}

// Owned reports whether the container belongs to dataDir. A container without
// the label (started by an older OwnCI) counts as owned, as before the label.
func (i Info) Owned(dataDir string) bool { return i.DataDir == "" || i.DataDir == dataDir }

// Inspect returns Docker's view of the runner's container, or ErrNoContainer.
func Inspect(ctx context.Context, ex execx.Runner, runner string) (Info, error) {
	ctx, cancel := context.WithTimeout(ctx, dockerTimeout)
	defer cancel()
	format := `{{.State.Status}} {{.Config.Image}} {{index .Config.Labels "` + DataDirLabel + `"}}`
	res, err := ex.Run(ctx, execx.Cmd{Name: "docker", Args: []string{
		"inspect", "--type", "container", "--format", format, ContainerName(runner),
	}})
	if err != nil {
		return Info{}, err
	}
	if res.ExitCode != 0 {
		if strings.Contains(strings.ToLower(string(res.Stderr)), "no such") {
			return Info{}, ErrNoContainer
		}
		return Info{}, fmt.Errorf("docker inspect: %s", strings.TrimSpace(string(res.Stderr)))
	}
	// The data dir comes last: it is the only field that may contain spaces.
	state, rest, _ := strings.Cut(strings.TrimSpace(string(res.Stdout)), " ")
	image, dataDir, _ := strings.Cut(rest, " ")
	return Info{State: state, Image: image, DataDir: dataDir}, nil
}

// probeScript lists what the runner container needs from an image: the
// entrypoint's tools, ICU for the runner, and the docker CLI (the cleanup hook
// and `container:` jobs use it). git and sudo are only recommended:
// actions/checkout falls back to the REST API without git, and only workflows
// that call sudo need it.
const probeScript = `missing="" optional=""
for c in setpriv useradd groupadd usermod getent stat docker; do
  command -v "$c" >/dev/null 2>&1 || missing="$missing $c"
done
ldconfig -p 2>/dev/null | grep -q libicu || missing="$missing libicu"
for c in git sudo; do
  command -v "$c" >/dev/null 2>&1 || optional="$optional $c"
done
[ -z "$optional" ] || echo "optional:$optional"
if [ -n "$missing" ]; then echo "missing:$missing"; exit 3; fi
echo ok`

// optionalWhy explains what a job loses without a recommended tool.
var optionalWhy = map[string]string{
	"git":  "git is missing: actions/checkout downloads a tarball instead of cloning (no history, no submodules)",
	"sudo": "sudo is missing: workflow steps that call sudo (e.g. sudo apt-get install) will fail",
}

// Probe checks that image can host the runner: it runs a throwaway,
// network-less container from it and looks for the required tools (an
// *Error naming what is missing) and the recommended ones (warnings). The
// image must be present locally.
func Probe(ctx context.Context, ex execx.Runner, image string) (warnings []string, err error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	res, err := ex.Run(ctx, execx.Cmd{Name: "docker", Args: []string{
		"run", "--rm", "--pull", "never", "--network", "none", "--label", "ownci.managed=true",
		"--entrypoint", "bash", image, "-c", probeScript, "ownci-probe",
	}})
	if err != nil {
		return nil, fmt.Errorf("probing image %s: %w", image, err)
	}
	var missing string
	for _, line := range strings.Split(string(res.Stdout), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "optional:"):
			for _, tool := range strings.Fields(strings.TrimPrefix(line, "optional:")) {
				if why, ok := optionalWhy[tool]; ok {
					warnings = append(warnings, why)
				}
			}
		case strings.HasPrefix(line, "missing:"):
			missing = strings.TrimPrefix(line, "missing:")
		}
	}
	switch {
	case res.ExitCode == 0:
		return warnings, nil
	case missing != "":
		return warnings, &Error{
			Msg:  fmt.Sprintf("image %s cannot host the runner; it lacks:%s", image, missing),
			Hint: "Use an image with the runner's dependencies (bash, setpriv, useradd/groupadd/usermod, getent, stat, the docker CLI and ICU), e.g. the default " + DefaultImage + " or one derived from it.",
		}
	default:
		detail := strings.TrimSpace(string(res.Stderr))
		if detail == "" {
			detail = strings.TrimSpace(string(res.Stdout))
		}
		return warnings, &Error{
			Msg:  fmt.Sprintf("image %s could not be probed (exit %d): %s", image, res.ExitCode, excerpt(detail)),
			Hint: "The image needs bash. Pull it ('docker pull " + image + "') and check that it is a Linux image.",
		}
	}
}

func excerpt(s string) string {
	if len(s) > 300 {
		return s[:300] + "…"
	}
	return s
}
