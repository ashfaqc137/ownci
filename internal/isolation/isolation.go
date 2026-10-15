// Package isolation installs, repairs and checks what makes the runner's
// isolation policy real (D1, D10): the job-container requirement in the
// runner's .env and the per-job cleanup hook with its pinned image.
package isolation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ashfaqc137/ownci/internal/execx"
	"github.com/ashfaqc137/ownci/internal/hooks"
	"github.com/ashfaqc137/ownci/internal/runner"
)

// Env keys OwnCI manages in the runner's .env.
const (
	EnvRequireContainer = "ACTIONS_RUNNER_REQUIRE_JOB_CONTAINER"
	EnvHookCompleted    = "ACTIONS_RUNNER_HOOK_JOB_COMPLETED"
	EnvRunnerRoot       = "OWNCI_RUNNER_ROOT"
	EnvCleanupImage     = "OWNCI_CLEANUP_IMAGE"
)

const (
	dockerTimeout  = 10 * time.Minute // a first pull can be slow
	inspectTimeout = 15 * time.Second
	cleanupTimeout = 2 * time.Minute
)

// Error is an isolation failure with an actionable hint.
type Error struct{ Msg, Hint string }

func (e *Error) Error() string    { return e.Msg }
func (e *Error) UserHint() string { return e.Hint }

// ManagedEnv is the .env content OwnCI enforces for runnerDir.
func ManagedEnv(runnerDir, image string) map[string]string {
	return map[string]string{
		EnvRequireContainer: "true",
		EnvHookCompleted:    hooks.JobCompletedPath(runnerDir),
		EnvRunnerRoot:       runnerDir,
		EnvCleanupImage:     image,
	}
}

// Install writes the hook and the .env entries and pulls the cleanup image.
// It is called by setup right after the runner is configured.
func Install(ctx context.Context, ex execx.Runner, runnerDir, image string) error {
	if _, err := writeHook(runnerDir); err != nil {
		return err
	}
	if _, err := runner.EnsureEnv(runnerDir, ManagedEnv(runnerDir, image)); err != nil {
		return &Error{Msg: "writing the runner's .env: " + err.Error(), Hint: "Check that " + runnerDir + " is writable."}
	}
	return pullImage(ctx, ex, image)
}

// Repair re-asserts everything Install did, without needing the network when
// nothing is wrong. It returns a description of each thing it had to fix
// (tampering, or a .env rewritten by the runner).
func Repair(ctx context.Context, ex execx.Runner, runnerDir, image string) (fixed []string, err error) {
	wrote, err := writeHook(runnerDir)
	if err != nil {
		return nil, err
	}
	if wrote {
		fixed = append(fixed, "rewrote the cleanup hook")
	}
	changed, err := runner.EnsureEnv(runnerDir, ManagedEnv(runnerDir, image))
	if err != nil {
		return fixed, &Error{Msg: "restoring the runner's .env: " + err.Error(), Hint: "Check that " + runnerDir + " is writable."}
	}
	if changed {
		fixed = append(fixed, "restored the isolation settings in .env")
	}
	if !imagePresent(ctx, ex, image) {
		if err := pullImage(ctx, ex, image); err != nil {
			return fixed, err
		}
		fixed = append(fixed, "pulled the cleanup image")
	}
	return fixed, nil
}

// writeHook installs the embedded hook (dir 0700, file 0700) if the file is
// missing, different or has the wrong mode, and reports whether it wrote.
func writeHook(runnerDir string) (bool, error) {
	dir, path := hooks.Dir(runnerDir), hooks.JobCompletedPath(runnerDir)
	if hookOK(path) {
		return false, nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return false, &Error{Msg: "creating " + dir + ": " + err.Error(), Hint: "Check that " + runnerDir + " is writable."}
	}
	_ = os.Chmod(dir, 0o700)
	tmp, err := os.CreateTemp(dir, ".hook-*")
	if err == nil {
		_, err = tmp.Write(hooks.JobCompletedScript)
		if cerr := tmp.Chmod(0o700); err == nil {
			err = cerr
		}
		if cerr := tmp.Close(); err == nil {
			err = cerr
		}
		if err == nil {
			err = os.Rename(tmp.Name(), path)
		}
		if err != nil {
			os.Remove(tmp.Name())
		}
	}
	if err != nil {
		return false, &Error{Msg: "installing the cleanup hook: " + err.Error(), Hint: "Check that " + runnerDir + " is writable."}
	}
	return true, nil
}

func hookOK(path string) bool {
	fi, err := os.Lstat(path)
	if err != nil || !fi.Mode().IsRegular() || fi.Mode().Perm() != 0o700 {
		return false
	}
	b, err := os.ReadFile(path)
	return err == nil && bytes.Equal(b, hooks.JobCompletedScript)
}

func pullImage(ctx context.Context, ex execx.Runner, image string) error {
	ctx, cancel := context.WithTimeout(ctx, dockerTimeout)
	defer cancel()
	res, err := ex.Run(ctx, execx.Cmd{Name: "docker", Args: []string{"pull", "--quiet", image}})
	if err != nil || res.ExitCode != 0 {
		detail := fmt.Sprint(err)
		if err == nil {
			detail = excerpt(string(res.Stderr))
		}
		return &Error{
			Msg:  fmt.Sprintf("pulling the cleanup image %s failed: %s", image, detail),
			Hint: "Check your network and Docker (run 'ownci doctor'), then retry.",
		}
	}
	return nil
}

func imagePresent(ctx context.Context, ex execx.Runner, image string) bool {
	ctx, cancel := context.WithTimeout(ctx, inspectTimeout)
	defer cancel()
	res, err := ex.Run(ctx, execx.Cmd{Name: "docker", Args: []string{"image", "inspect", "--format", "{{.Id}}", image}})
	return err == nil && res.ExitCode == 0
}

// RunCleanup runs the installed hook once, as the runner would after a job.
// start uses it to clear leftovers of a crash or kill that happened mid-job.
func RunCleanup(ctx context.Context, ex execx.Runner, runnerDir, image string) error {
	// A runner that never ran a job has no _work yet: nothing to clean. (The
	// hook itself is strict about a missing or symlinked _work.)
	if _, err := os.Lstat(filepath.Join(runnerDir, "_work")); os.IsNotExist(err) {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, cleanupTimeout)
	defer cancel()
	res, err := ex.Run(ctx, execx.Cmd{
		Name: hooks.JobCompletedPath(runnerDir),
		Env:  []string{EnvRunnerRoot + "=" + runnerDir, EnvCleanupImage + "=" + image},
	})
	if err != nil {
		return &Error{Msg: "running the cleanup hook: " + err.Error(), Hint: "Run 'ownci doctor'."}
	}
	if res.ExitCode != 0 {
		return &Error{
			Msg:  "cleaning the workspace failed: " + excerpt(string(res.Stdout)+string(res.Stderr)),
			Hint: "Run 'ownci doctor'; check that Docker works and the image is present.",
		}
	}
	return nil
}

// Item is one line of a Report.
type Item struct {
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
}

// LastCleanup is the hook's record of its latest run.
type LastCleanup struct {
	Result string    `json:"result"` // ok | failed | never
	Time   time.Time `json:"time,omitzero"`
	Detail string    `json:"detail,omitempty"`
}

// Report is the verified isolation state of one runner directory.
type Report struct {
	JobContainerRequired Item        `json:"jobContainerRequired"`
	CleanupHook          Item        `json:"cleanupHook"`
	CleanupImage         Item        `json:"cleanupImage"`
	LastCleanup          LastCleanup `json:"lastCleanup"`
}

// EnforcementOK is true when jobs are required to use a container and the
// cleanup hook is in place. The image and the last cleanup are informational
// (a missing image fails the cleanup, and so the job, visibly).
func (r Report) EnforcementOK() bool { return r.JobContainerRequired.OK && r.CleanupHook.OK }

// Verify inspects runnerDir without changing anything.
func Verify(ctx context.Context, ex execx.Runner, runnerDir, image string) Report {
	var r Report

	env, err := runner.ReadEnv(runnerDir)
	switch {
	case err != nil:
		r.JobContainerRequired = Item{Detail: err.Error()}
	case env[EnvRequireContainer] != "true":
		r.JobContainerRequired = Item{Detail: EnvRequireContainer + " is not set to true in .env"}
	default:
		r.JobContainerRequired = Item{OK: true}
	}

	switch want := hooks.JobCompletedPath(runnerDir); {
	case env[EnvHookCompleted] != want:
		r.CleanupHook = Item{Detail: EnvHookCompleted + " in .env does not point at the ownci hook"}
	case !hookOK(want):
		r.CleanupHook = Item{Detail: "the hook file is missing, modified or not mode 0700"}
	default:
		r.CleanupHook = Item{OK: true}
	}

	if imagePresent(ctx, ex, image) {
		r.CleanupImage = Item{OK: true}
	} else {
		r.CleanupImage = Item{Detail: "image " + image + " is not present locally"}
	}

	r.LastCleanup = readLastCleanup(runnerDir)
	return r
}

func readLastCleanup(runnerDir string) LastCleanup {
	b, err := os.ReadFile(hooks.LastCleanupPath(runnerDir))
	if err != nil {
		return LastCleanup{Result: "never"}
	}
	var lc LastCleanup
	if err := json.Unmarshal(b, &lc); err != nil || (lc.Result != "ok" && lc.Result != "failed") {
		return LastCleanup{Result: "failed", Detail: "unreadable last-cleanup.json"}
	}
	return lc
}

// Problems lists what is wrong with the enforcement items, for error text.
func (r Report) Problems() []string {
	var p []string
	if !r.JobContainerRequired.OK {
		p = append(p, "job container requirement: "+r.JobContainerRequired.Detail)
	}
	if !r.CleanupHook.OK {
		p = append(p, "cleanup hook: "+r.CleanupHook.Detail)
	}
	return p
}

func excerpt(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	return s
}
