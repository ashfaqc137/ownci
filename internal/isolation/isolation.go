// Package isolation installs, repairs and checks what makes the runner's
// isolation policy real: the job-container requirement (host
// mode) in the runner's .env, the per-job cleanup hook with its pinned image,
// and the runner image (container-runner mode).
package isolation

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ashfaqc137/ownci/internal/config"
	"github.com/ashfaqc137/ownci/internal/docker"
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

const cleanupTimeout = 2 * time.Minute

// Error is an isolation failure with an actionable hint.
type Error struct{ Msg, Hint string }

func (e *Error) Error() string    { return e.Msg }
func (e *Error) UserHint() string { return e.Hint }

// Policy is what a runner directory must enforce: the execution mode and the
// images it depends on.
type Policy struct {
	Mode         string // config.ModeHost or config.ModeContainerRunner
	CleanupImage string
	RunnerImage  string // container-runner mode only
}

// HostPolicy is the host-mode policy.
func HostPolicy() Policy { return Policy{Mode: config.ModeHost, CleanupImage: hooks.CleanupImage} }

// PolicyFor is the policy of a configured runner; image overrides the
// configured runner image when not empty (`start --image`).
func PolicyFor(rc *config.RunnerConfig, image string) Policy {
	p := Policy{Mode: rc.EffectiveMode(), CleanupImage: hooks.CleanupImage}
	if p.Mode == config.ModeContainerRunner {
		p.RunnerImage = rc.Image
		if image != "" {
			p.RunnerImage = image
		}
	}
	return p
}

func (p Policy) container() bool { return p.Mode == config.ModeContainerRunner }

// ManagedEnv is the .env content OwnCI enforces for runnerDir. The runner
// container mounts runnerDir at the same path, so the values hold in both
// modes.
func ManagedEnv(runnerDir string, p Policy) map[string]string {
	m := map[string]string{
		EnvHookCompleted: hooks.JobCompletedPath(runnerDir),
		EnvRunnerRoot:    runnerDir,
		EnvCleanupImage:  p.CleanupImage,
	}
	if !p.container() {
		m[EnvRequireContainer] = "true"
	}
	return m
}

// absentEnv lists keys that must not be set: in container-runner mode jobs
// without `container:` are the point, so the requirement is removed.
func absentEnv(p Policy) []string {
	if p.container() {
		return []string{EnvRequireContainer}
	}
	return nil
}

// Install writes the hook and the .env entries and pulls the images that are
// not present yet (a runner image may be built locally and exist in no
// registry). It is called by setup right after the runner is configured.
func Install(ctx context.Context, ex execx.Runner, runnerDir string, p Policy) error {
	if _, err := writeHook(runnerDir); err != nil {
		return err
	}
	if _, err := runner.EnsureEnv(runnerDir, ManagedEnv(runnerDir, p), absentEnv(p)...); err != nil {
		return &Error{Msg: "writing the runner's .env: " + err.Error(), Hint: "Check that " + runnerDir + " is writable."}
	}
	for _, image := range p.images() {
		if _, err := docker.EnsureImage(ctx, ex, image); err != nil {
			return err
		}
	}
	return nil
}

func (p Policy) images() []string {
	if p.container() {
		return []string{p.CleanupImage, p.RunnerImage}
	}
	return []string{p.CleanupImage}
}

// Repair re-asserts everything Install did, without needing the network when
// nothing is wrong. It returns a description of each thing it had to fix
// (tampering, or a .env rewritten by the runner).
func Repair(ctx context.Context, ex execx.Runner, runnerDir string, p Policy) (fixed []string, err error) {
	wrote, err := writeHook(runnerDir)
	if err != nil {
		return nil, err
	}
	if wrote {
		fixed = append(fixed, "rewrote the cleanup hook")
	}
	changed, err := runner.EnsureEnv(runnerDir, ManagedEnv(runnerDir, p), absentEnv(p)...)
	if err != nil {
		return fixed, &Error{Msg: "restoring the runner's .env: " + err.Error(), Hint: "Check that " + runnerDir + " is writable."}
	}
	if changed {
		fixed = append(fixed, "restored the isolation settings in .env")
	}
	for _, image := range p.images() {
		pulled, err := docker.EnsureImage(ctx, ex, image)
		if err != nil {
			return fixed, err
		}
		if pulled {
			fixed = append(fixed, "pulled "+image)
		}
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
	Mode string `json:"mode"`
	// JobContainerRequired is set in host mode only.
	JobContainerRequired *Item `json:"jobContainerRequired,omitempty"`
	// RunnerImage is set in container-runner mode only: the image is
	// present locally.
	RunnerImage  *Item       `json:"runnerImage,omitempty"`
	Image        string      `json:"image,omitempty"`
	CleanupHook  Item        `json:"cleanupHook"`
	CleanupImage Item        `json:"cleanupImage"`
	LastCleanup  LastCleanup `json:"lastCleanup"`
}

// EnforcementOK is true when the cleanup hook is in place and, in host mode,
// jobs are required to use a container. In container-runner mode jobs
// without `container:` run inside the runner container by construction (the
// supervisor only launches it there). The images and the last cleanup are
// informational: a missing cleanup image fails the cleanup, and so the job,
// visibly; a missing runner image is pulled before launch.
func (r Report) EnforcementOK() bool {
	if r.JobContainerRequired != nil && !r.JobContainerRequired.OK {
		return false
	}
	return r.CleanupHook.OK
}

// Verify inspects runnerDir without changing anything.
func Verify(ctx context.Context, ex execx.Runner, runnerDir string, p Policy) Report {
	r := Report{Mode: p.Mode}

	env, err := runner.ReadEnv(runnerDir)
	if !p.container() {
		var it Item
		switch {
		case err != nil:
			it = Item{Detail: err.Error()}
		case env[EnvRequireContainer] != "true":
			it = Item{Detail: EnvRequireContainer + " is not set to true in .env"}
		default:
			it = Item{OK: true}
		}
		r.JobContainerRequired = &it
	} else {
		r.Image = p.RunnerImage
		it := Item{OK: true}
		if !docker.ImagePresent(ctx, ex, p.RunnerImage) {
			it = Item{Detail: "runner image " + p.RunnerImage + " is not present locally"}
		}
		r.RunnerImage = &it
	}

	switch want := hooks.JobCompletedPath(runnerDir); {
	case env[EnvHookCompleted] != want:
		r.CleanupHook = Item{Detail: EnvHookCompleted + " in .env does not point at the ownci hook"}
	case !hookOK(want):
		r.CleanupHook = Item{Detail: "the hook file is missing, modified or not mode 0700"}
	default:
		r.CleanupHook = Item{OK: true}
	}

	if docker.ImagePresent(ctx, ex, p.CleanupImage) {
		r.CleanupImage = Item{OK: true}
	} else {
		r.CleanupImage = Item{Detail: "image " + p.CleanupImage + " is not present locally"}
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
	if r.JobContainerRequired != nil && !r.JobContainerRequired.OK {
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
