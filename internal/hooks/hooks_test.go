package hooks

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ashfaqc137/ownci/internal/testutil"
)

type hookRig struct {
	t    *testing.T
	root string // OWNCI_RUNNER_ROOT
	work string
	fd   testutil.FakeDocker
}

func newHookRig(t *testing.T) *hookRig {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not on PATH")
	}
	fd := testutil.UseFakeDocker(t)
	root := t.TempDir()
	work := filepath.Join(root, "_work")
	for _, d := range []string{"_tool/x", "_actions/y", "repo/repo", "_temp/keep", "_PipelineMapping/m"} {
		if err := os.MkdirAll(filepath.Join(work, d), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(work, d, "marker"), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(Dir(root), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(JobCompletedPath(root), JobCompletedScript, 0o700); err != nil {
		t.Fatal(err)
	}
	return &hookRig{t: t, root: root, work: work, fd: fd}
}

func (r *hookRig) run(env ...string) (string, int) {
	r.t.Helper()
	cmd := exec.Command(JobCompletedPath(r.root))
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		r.t.Fatal(err)
	}
	return string(out), code
}

func (r *hookRig) defaultEnv() []string {
	return []string{"OWNCI_RUNNER_ROOT=" + r.root, "OWNCI_CLEANUP_IMAGE=" + CleanupImage}
}

func (r *hookRig) lastCleanup() map[string]string {
	r.t.Helper()
	b, err := os.ReadFile(LastCleanupPath(r.root))
	if err != nil {
		r.t.Fatalf("no last-cleanup.json: %v", err)
	}
	var m map[string]string
	if err := json.Unmarshal(b, &m); err != nil {
		r.t.Fatalf("last-cleanup.json is not valid JSON: %v\n%s", err, b)
	}
	return m
}

func entries(t *testing.T, dir string) []string {
	t.Helper()
	es, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range es {
		names = append(names, e.Name())
	}
	return names
}

func TestCleanupRemovesSymlinkedTemp(t *testing.T) {
	r := newHookRig(t)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "precious"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{"_temp", "_PipelineMapping"} {
		if err := os.RemoveAll(filepath.Join(r.work, d)); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(r.work, d)); err != nil {
			t.Fatal(err)
		}
	}
	if out, code := r.run(r.defaultEnv()...); code != 0 {
		t.Fatalf("exit %d, output %q", code, out)
	}
	if got := entries(t, r.work); len(got) != 0 {
		t.Errorf("_work = %v, want the symlinks removed", got)
	}
	if _, err := os.Stat(filepath.Join(outside, "precious")); err != nil {
		t.Error("a file behind a symlink was deleted")
	}
}

func TestCleanupKeepsOnlyTempAndPipelineMapping(t *testing.T) {
	r := newHookRig(t)
	out, code := r.run(r.defaultEnv()...)
	if code != 0 || strings.TrimSpace(out) != "ownci: workspace cleaned" {
		t.Fatalf("exit %d, output %q", code, out)
	}
	got := strings.Join(entries(t, r.work), ",")
	if got != "_PipelineMapping,_temp" {
		t.Errorf("_work contains %q, want only _PipelineMapping and _temp", got)
	}
	if _, err := os.Stat(filepath.Join(r.work, "_temp", "keep", "marker")); err != nil {
		t.Errorf("_temp content was removed: %v", err)
	}
	if lc := r.lastCleanup(); lc["result"] != "ok" || lc["time"] == "" {
		t.Errorf("last cleanup = %v", lc)
	}
}

func TestCleanupContainerArguments(t *testing.T) {
	r := newHookRig(t)
	if _, code := r.run(r.defaultEnv()...); code != 0 {
		t.Fatal("hook failed")
	}
	var run string
	for _, c := range r.fd.Calls() {
		if strings.HasPrefix(c, "run ") {
			run = c
		}
	}
	if run == "" {
		t.Fatalf("no docker run recorded: %v", r.fd.Calls())
	}
	for _, want := range []string{"--rm", "--pull missing", "--network none", "--label ownci.managed=true", "-v " + r.work + ":/w", CleanupImage} {
		if !strings.Contains(run, want) {
			t.Errorf("docker run lacks %q:\n%s", want, run)
		}
	}
	for _, bad := range []string{"--privileged", "docker.sock", "--cap-add"} {
		if strings.Contains(run, bad) {
			t.Errorf("docker run contains %q", bad)
		}
	}
}

func TestCleanupFailureFailsTheHook(t *testing.T) {
	r := newHookRig(t)
	out, code := r.run(append(r.defaultEnv(), "FAKE_DOCKER_RUN_FAIL=1")...)
	if code != 1 || !strings.Contains(out, "ownci: cleanup FAILED (") {
		t.Fatalf("exit %d, output %q", code, out)
	}
	lc := r.lastCleanup()
	if lc["result"] != "failed" || lc["detail"] == "" {
		t.Errorf("last cleanup = %v", lc)
	}
	// Nothing was deleted: the fake docker failed before running anything.
	if _, err := os.Stat(filepath.Join(r.work, "_tool", "x", "marker")); err != nil {
		t.Errorf("files were removed although the container failed: %v", err)
	}
}

func TestCleanupRefusesUnexpectedWorkDir(t *testing.T) {
	t.Run("OWNCI_RUNNER_ROOT unset", func(t *testing.T) {
		r := newHookRig(t)
		out, code := r.run("OWNCI_CLEANUP_IMAGE=" + CleanupImage)
		if code != 1 || !strings.Contains(out, "cleanup skipped: unexpected work dir") {
			t.Errorf("exit %d, output %q", code, out)
		}
		if _, err := os.Stat(filepath.Join(r.work, "_tool", "x", "marker")); err != nil {
			t.Error("files were deleted")
		}
		if len(r.fd.Calls()) != 0 {
			t.Errorf("docker was called: %v", r.fd.Calls())
		}
	})
	t.Run("relative root", func(t *testing.T) {
		r := newHookRig(t)
		out, code := r.run("OWNCI_RUNNER_ROOT=relative/path", "OWNCI_CLEANUP_IMAGE="+CleanupImage)
		if code != 1 || !strings.Contains(out, "unexpected work dir") {
			t.Errorf("exit %d, output %q", code, out)
		}
	})
	t.Run("_work is a symlink", func(t *testing.T) {
		r := newHookRig(t)
		outside := t.TempDir()
		if err := os.WriteFile(filepath.Join(outside, "precious"), nil, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(r.work, r.work+".real"); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, r.work); err != nil {
			t.Fatal(err)
		}
		out, code := r.run(r.defaultEnv()...)
		if code != 1 || !strings.Contains(out, "unexpected work dir") {
			t.Errorf("exit %d, output %q", code, out)
		}
		if _, err := os.Stat(filepath.Join(outside, "precious")); err != nil {
			t.Error("a file behind the symlink was deleted")
		}
		if got := r.lastCleanup()["result"]; got != "failed" {
			t.Errorf("last cleanup result = %q, want failed (status must not keep showing ok)", got)
		}
		if len(r.fd.Calls()) != 0 {
			t.Errorf("docker was called: %v", r.fd.Calls())
		}
	})
	t.Run("_work missing", func(t *testing.T) {
		r := newHookRig(t)
		if err := os.RemoveAll(r.work); err != nil {
			t.Fatal(err)
		}
		if _, code := r.run(r.defaultEnv()...); code != 1 {
			t.Errorf("exit %d, want 1", code)
		}
	})
}

func TestCleanupRequiresAnImage(t *testing.T) {
	r := newHookRig(t)
	out, code := r.run("OWNCI_RUNNER_ROOT=" + r.root)
	if code != 1 || !strings.Contains(out, "cleanup FAILED") {
		t.Errorf("exit %d, output %q", code, out)
	}
}

func TestPinnedImageIsADigestReference(t *testing.T) {
	if !strings.Contains(CleanupImage, "@sha256:") || len(CleanupImage[strings.Index(CleanupImage, "@sha256:")+8:]) != 64 {
		t.Errorf("CleanupImage %q is not pinned by digest", CleanupImage)
	}
}

func TestScriptDoesNotPrintEnvironment(t *testing.T) {
	// The hook's output lands in the job log: it must not dump variables.
	for _, bad := range []string{"printenv", "env |", "set -x", "declare -p", "export -p"} {
		if strings.Contains(string(JobCompletedScript), bad) {
			t.Errorf("script contains %q", bad)
		}
	}
}
