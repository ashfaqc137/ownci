package isolation

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ashfaqc137/ownci/internal/config"
	"github.com/ashfaqc137/ownci/internal/execx"
	"github.com/ashfaqc137/ownci/internal/hooks"
	"github.com/ashfaqc137/ownci/internal/runner"
)

const image = hooks.CleanupImage

var hostPol = HostPolicy()

func dockerFake(imagePresent bool) *execx.Fake {
	f := execx.NewFake()
	f.On("docker", "pull")
	if imagePresent {
		f.On("docker", "image", "inspect").Stdout("sha256:x")
	} else {
		f.On("docker", "image", "inspect").Exit(1).Stderr("No such image")
	}
	return f
}

func pulls(f *execx.Fake) int {
	n := 0
	for _, c := range f.Calls() {
		if c.Name == "docker" && len(c.Args) > 0 && c.Args[0] == "pull" {
			n++
		}
	}
	return n
}

func TestInstall(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(runner.EnvPath(dir), []byte("LANG=C.UTF-8\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f := dockerFake(false)
	if err := Install(context.Background(), f, dir, hostPol); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(hooks.JobCompletedPath(dir)); err != nil || fi.Mode().Perm() != 0o700 {
		t.Errorf("hook: %v %v", fi, err)
	}
	if fi, _ := os.Stat(hooks.Dir(dir)); fi.Mode().Perm() != 0o700 {
		t.Errorf("hook dir mode = %v", fi.Mode().Perm())
	}
	env, _ := runner.ReadEnv(dir)
	want := map[string]string{
		"LANG":                                 "C.UTF-8",
		"ACTIONS_RUNNER_REQUIRE_JOB_CONTAINER": "true",
		"ACTIONS_RUNNER_HOOK_JOB_COMPLETED":    filepath.Join(dir, "ownci", "job-completed.sh"),
		"OWNCI_RUNNER_ROOT":                    dir,
		"OWNCI_CLEANUP_IMAGE":                  image,
	}
	for k, v := range want {
		if env[k] != v {
			t.Errorf("env[%s] = %q, want %q", k, env[k], v)
		}
	}
	if pulls(f) != 1 {
		t.Errorf("pulls = %d", pulls(f))
	}
	rep := Verify(context.Background(), dockerFake(true), dir, hostPol)
	if !rep.EnforcementOK() || rep.LastCleanup.Result != "never" {
		t.Errorf("report after install = %+v", rep)
	}
}

func TestInstallPullFailureHasHint(t *testing.T) {
	f := execx.NewFake()
	f.On("docker", "pull").Exit(1).Stderr("pull access denied")
	err := Install(context.Background(), f, t.TempDir(), hostPol)
	var ie interface{ UserHint() string }
	if !errors.As(err, &ie) || !strings.Contains(err.Error(), "pull access denied") || ie.UserHint() == "" {
		t.Fatalf("err = %v", err)
	}
}

func TestRepair(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	// Nothing installed yet: everything is repaired, the image is pulled.
	f := dockerFake(false)
	fixed, err := Repair(ctx, f, dir, hostPol)
	if err != nil {
		t.Fatal(err)
	}
	if len(fixed) != 3 || pulls(f) != 1 {
		t.Errorf("fixed = %v, pulls = %d", fixed, pulls(f))
	}

	// Healthy: no changes, no pull.
	f = dockerFake(true)
	if fixed, err := Repair(ctx, f, dir, hostPol); err != nil || len(fixed) != 0 || pulls(f) != 0 {
		t.Errorf("healthy repair: %v %v pulls=%d", fixed, err, pulls(f))
	}

	// Tampered hook and .env (the REQUIRE key deleted, as in the spec's test).
	if err := os.WriteFile(hooks.JobCompletedPath(dir), []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(runner.EnvPath(dir))
	kept := ""
	for _, l := range strings.Split(string(raw), "\n") {
		if !strings.Contains(l, "REQUIRE_JOB_CONTAINER") {
			kept += l + "\n"
		}
	}
	if err := os.WriteFile(runner.EnvPath(dir), []byte(kept), 0o600); err != nil {
		t.Fatal(err)
	}
	if rep := Verify(ctx, dockerFake(true), dir, hostPol); rep.EnforcementOK() || rep.JobContainerRequired.OK || rep.CleanupHook.OK {
		t.Fatalf("tampering not detected: %+v", rep)
	}
	fixed, err = Repair(ctx, dockerFake(true), dir, hostPol)
	if err != nil || len(fixed) != 2 {
		t.Fatalf("fixed = %v, err = %v", fixed, err)
	}
	if rep := Verify(ctx, dockerFake(true), dir, hostPol); !rep.EnforcementOK() {
		t.Errorf("not repaired: %+v", rep)
	}
	got, _ := os.ReadFile(hooks.JobCompletedPath(dir))
	if string(got) != string(hooks.JobCompletedScript) {
		t.Error("hook content not restored")
	}

	// A hook with the wrong mode is also replaced.
	if err := os.Chmod(hooks.JobCompletedPath(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	if fixed, _ := Repair(ctx, dockerFake(true), dir, hostPol); len(fixed) != 1 {
		t.Errorf("mode repair: %v", fixed)
	}
}

func TestRepairFailsWhenRunnerDirIsReadOnly(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o700)
	_, err := Repair(context.Background(), dockerFake(true), dir, hostPol)
	var ie *Error
	if !errors.As(err, &ie) || ie.Hint == "" {
		t.Fatalf("err = %v, want an *Error with a hint", err)
	}
}

func TestVerifyCombinations(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	if err := Install(ctx, dockerFake(true), dir, hostPol); err != nil {
		t.Fatal(err)
	}

	t.Run("image missing is informational", func(t *testing.T) {
		rep := Verify(ctx, dockerFake(false), dir, hostPol)
		if !rep.EnforcementOK() || rep.CleanupImage.OK || rep.CleanupImage.Detail == "" {
			t.Errorf("%+v", rep)
		}
	})
	t.Run("hook path in .env points elsewhere", func(t *testing.T) {
		d := t.TempDir()
		if err := Install(ctx, dockerFake(true), d, hostPol); err != nil {
			t.Fatal(err)
		}
		if _, err := runner.EnsureEnv(d, map[string]string{EnvHookCompleted: "/evil/hook.sh"}); err != nil {
			t.Fatal(err)
		}
		if rep := Verify(ctx, dockerFake(true), d, hostPol); rep.CleanupHook.OK {
			t.Errorf("%+v", rep)
		}
	})
	t.Run("REQUIRE set to false", func(t *testing.T) {
		d := t.TempDir()
		if err := Install(ctx, dockerFake(true), d, hostPol); err != nil {
			t.Fatal(err)
		}
		if _, err := runner.EnsureEnv(d, map[string]string{EnvRequireContainer: "false"}); err != nil {
			t.Fatal(err)
		}
		rep := Verify(ctx, dockerFake(true), d, hostPol)
		if rep.JobContainerRequired.OK || len(rep.Problems()) != 1 {
			t.Errorf("%+v", rep)
		}
	})
	t.Run("last cleanup", func(t *testing.T) {
		for _, tc := range []struct{ content, result string }{
			{`{"time":"2026-10-15T10:05:41Z","result":"ok","detail":"workspace cleaned"}`, "ok"},
			{`{"time":"2026-10-15T10:05:41Z","result":"failed","detail":"boom"}`, "failed"},
			{`not json`, "failed"},
			{`{"result":"weird"}`, "failed"},
		} {
			if err := os.WriteFile(hooks.LastCleanupPath(dir), []byte(tc.content), 0o600); err != nil {
				t.Fatal(err)
			}
			if got := Verify(ctx, dockerFake(true), dir, hostPol).LastCleanup; got.Result != tc.result {
				t.Errorf("%q → %+v, want %s", tc.content, got, tc.result)
			}
		}
		os.Remove(hooks.LastCleanupPath(dir))
		if got := Verify(ctx, dockerFake(true), dir, hostPol).LastCleanup.Result; got != "never" {
			t.Errorf("no file → %s", got)
		}
	})
	t.Run("nothing installed", func(t *testing.T) {
		rep := Verify(ctx, dockerFake(true), t.TempDir(), hostPol)
		if rep.EnforcementOK() || rep.JobContainerRequired.OK || rep.CleanupHook.OK {
			t.Errorf("%+v", rep)
		}
	})
}

func TestRunCleanup(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	hook := hooks.JobCompletedPath(dir)

	t.Run("no _work: nothing to do", func(t *testing.T) {
		f := execx.NewFake()
		if err := RunCleanup(ctx, f, dir, image); err != nil || len(f.Calls()) != 0 {
			t.Errorf("err=%v calls=%v", err, f.Calls())
		}
	})
	if err := os.MkdirAll(filepath.Join(dir, "_work"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Run("runs the hook with its environment", func(t *testing.T) {
		f := execx.NewFake()
		f.On(hook)
		if err := RunCleanup(ctx, f, dir, image); err != nil {
			t.Fatal(err)
		}
		c := f.Calls()[0]
		if c.Name != hook || !contains(c.Env, EnvRunnerRoot+"="+dir) || !contains(c.Env, EnvCleanupImage+"="+image) {
			t.Errorf("call = %+v", c)
		}
	})
	t.Run("a failing hook is an error with its output", func(t *testing.T) {
		f := execx.NewFake()
		f.On(hook).Exit(1).Stdout("ownci: cleanup FAILED (boom)\n")
		err := RunCleanup(ctx, f, dir, image)
		var ie *Error
		if !errors.As(err, &ie) || !strings.Contains(ie.Msg, "cleanup FAILED (boom)") || ie.Hint == "" {
			t.Errorf("err = %v", err)
		}
	})
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

const runnerImage = "ghcr.io/actions/actions-runner:9.9.9@sha256:abc"

var containerPol = Policy{Mode: config.ModeContainerRunner, CleanupImage: image, RunnerImage: runnerImage}

func pulled(f *execx.Fake) []string {
	var out []string
	for _, c := range f.Calls() {
		if c.Name == "docker" && len(c.Args) > 2 && c.Args[0] == "pull" {
			out = append(out, c.Args[2])
		}
	}
	return out
}

func TestContainerPolicy(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	// The runner dir was in host mode before (setup --force --mode container-runner).
	if err := Install(ctx, dockerFake(true), dir, hostPol); err != nil {
		t.Fatal(err)
	}
	f := dockerFake(false)
	if err := Install(ctx, f, dir, containerPol); err != nil {
		t.Fatal(err)
	}
	if got := pulled(f); len(got) != 2 || got[0] != image || got[1] != runnerImage {
		t.Errorf("pulled %v, want the cleanup and the runner image", got)
	}
	env, _ := runner.ReadEnv(dir)
	if _, ok := env[EnvRequireContainer]; ok {
		t.Errorf("%s is still in .env: jobs without container: would be rejected", EnvRequireContainer)
	}
	if env[EnvHookCompleted] != hooks.JobCompletedPath(dir) || env[EnvRunnerRoot] != dir || env[EnvCleanupImage] != image {
		t.Errorf("managed env = %v", env)
	}

	rep := Verify(ctx, dockerFake(true), dir, containerPol)
	if !rep.EnforcementOK() || rep.Mode != config.ModeContainerRunner || rep.JobContainerRequired != nil ||
		rep.RunnerImage == nil || !rep.RunnerImage.OK || rep.Image != runnerImage {
		t.Errorf("report = %+v", rep)
	}
	rep = Verify(ctx, dockerFake(false), dir, containerPol)
	if !rep.EnforcementOK() || rep.RunnerImage.OK || !strings.Contains(rep.RunnerImage.Detail, runnerImage) {
		t.Errorf("missing runner image should be informational: %+v", rep)
	}

	// Repair puts the requirement back for host mode and removes it again.
	if fixed, err := Repair(ctx, dockerFake(true), dir, hostPol); err != nil || len(fixed) != 1 {
		t.Fatalf("to host: %v %v", fixed, err)
	}
	if rep := Verify(ctx, dockerFake(true), dir, hostPol); !rep.EnforcementOK() || rep.RunnerImage != nil {
		t.Errorf("host report = %+v", rep)
	}
	f = dockerFake(false)
	fixed, err := Repair(ctx, f, dir, containerPol)
	if err != nil || len(fixed) != 3 || len(pulled(f)) != 2 {
		t.Fatalf("to container: fixed=%v err=%v pulled=%v", fixed, err, pulled(f))
	}
	if fixed, err := Repair(ctx, dockerFake(true), dir, containerPol); err != nil || len(fixed) != 0 {
		t.Errorf("healthy container repair: %v %v", fixed, err)
	}
}

func TestPolicyFor(t *testing.T) {
	host := PolicyFor(&config.RunnerConfig{}, "ignored")
	if host.Mode != config.ModeHost || host.RunnerImage != "" || host.CleanupImage != hooks.CleanupImage {
		t.Errorf("host = %+v", host)
	}
	rc := &config.RunnerConfig{Mode: config.ModeContainerRunner, Image: "configured"}
	if p := PolicyFor(rc, ""); p.RunnerImage != "configured" {
		t.Errorf("configured = %+v", p)
	}
	if p := PolicyFor(rc, "override"); p.RunnerImage != "override" {
		t.Errorf("override = %+v", p)
	}
}
