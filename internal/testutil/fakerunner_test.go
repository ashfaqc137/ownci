package testutil

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestTarballContents(t *testing.T) {
	data, sha := FakeRunnerTarball(FakeRunnerOptions{Version: "2.5.0"})
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != sha {
		t.Fatal("returned sha does not match data")
	}
	if again, sha2 := FakeRunnerTarball(FakeRunnerOptions{Version: "2.5.0"}); sha2 != sha || !bytes.Equal(again, data) {
		t.Error("tarball must be deterministic")
	}
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	modes := map[string]int64{}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		modes[h.Name] = h.Mode
		if h.Name == "./bin/Runner.Listener" {
			b, _ := io.ReadAll(tr)
			if !strings.Contains(string(b), `VERSION="2.5.0"`) {
				t.Errorf("version not substituted: %s", b)
			}
		}
	}
	for _, name := range []string{"./config.sh", "./run.sh", "./bin/Runner.Listener", "./bin/installdependencies.sh"} {
		if modes[name] != 0o755 {
			t.Errorf("%s mode = %o", name, modes[name])
		}
	}
}

// --- behavior of the scripts ---

func runnerDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "runner")
	WriteFakeRunner(t, dir)
	return dir
}

func script(t *testing.T, dir, name string, env []string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(filepath.Join(dir, name), args...)
	cmd.Env = append(os.Environ(), append([]string{"RUNNER_ALLOW_RUNASROOT=1"}, env...)...)
	out, err := cmd.CombinedOutput()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return string(out), ee.ExitCode()
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(out), 0
}

func configure(t *testing.T, dir string, extra ...string) (string, int) {
	args := append([]string{"--unattended", "--url", "https://github.com/o/r", "--token", "SECRETTOKEN123", "--name", "n1", "--labels", "ownci,x", "--no-default-labels", "--work", "_work"}, extra...)
	return script(t, dir, "config.sh", nil, args...)
}

func TestListenerAndDependencies(t *testing.T) {
	dir := runnerDir(t)
	if out, code := script(t, dir, "bin/Runner.Listener", nil, "--version"); code != 0 || strings.TrimSpace(out) != "2.999.0-fake" {
		t.Errorf("--version: %q %d", out, code)
	}
	if _, code := script(t, dir, "bin/Runner.Listener", nil, "run"); code != 1 {
		t.Errorf("other args exit = %d", code)
	}
	if out, code := script(t, dir, "bin/installdependencies.sh", nil); code != 0 || !strings.Contains(out, "fake: dependencies ok") {
		t.Errorf("%q %d", out, code)
	}
	if out, code := script(t, dir, "config.sh", nil, "--version"); code != 0 || !strings.Contains(out, "2.999.0-fake") {
		t.Errorf("config --version: %q %d", out, code)
	}
}

func TestConfigureAndRemove(t *testing.T) {
	dir := runnerDir(t)
	if out, code := configure(t, dir); code != 0 || !strings.Contains(out, "Runner successfully added") || !strings.Contains(out, "Settings Saved") {
		t.Fatalf("configure: %q %d", out, code)
	}
	runner, _ := os.ReadFile(filepath.Join(dir, ".runner"))
	if !bytes.HasPrefix(runner, []byte("\xef\xbb\xbf{")) {
		t.Errorf(".runner must start with a BOM: %q", runner)
	}
	if !strings.Contains(string(runner), `"agentName":"n1"`) || !strings.Contains(string(runner), `"gitHubUrl":"https://github.com/o/r"`) {
		t.Errorf(".runner = %q", runner)
	}
	if fi, err := os.Stat(filepath.Join(dir, ".credentials")); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf(".credentials: %v %v", fi, err)
	}
	args, _ := os.ReadFile(filepath.Join(dir, ".fake-config-args"))
	if strings.Contains(string(args), "SECRETTOKEN123") || !strings.Contains(string(args), "--token ***") || !strings.Contains(string(args), "--no-default-labels") {
		t.Errorf(".fake-config-args = %q", args)
	}

	if out, code := configure(t, dir); code != 1 || !strings.Contains(out, "already configured") {
		t.Errorf("second configure: %q %d", out, code)
	}
	// Like the real config.sh: --replace does not override a local .runner.
	if out, code := configure(t, dir, "--replace"); code != 1 || !strings.Contains(out, "already configured") {
		t.Errorf("--replace over .runner: %q %d", out, code)
	}

	if out, code := script(t, dir, "config.sh", nil, "remove", "--token", "T"); code != 0 || !strings.Contains(out, "Runner removed successfully") {
		t.Errorf("remove: %q %d", out, code)
	}
	for _, f := range []string{".runner", ".credentials"} {
		if _, err := os.Stat(filepath.Join(dir, f)); !os.IsNotExist(err) {
			t.Errorf("%s should be gone", f)
		}
	}
	if out, code := script(t, dir, "config.sh", nil, "remove", "--token", "T"); code != 1 || !strings.Contains(out, "not configured") {
		t.Errorf("remove again: %q %d", out, code)
	}
}

func TestConfigureMissingArgsAndRoot(t *testing.T) {
	dir := runnerDir(t)
	if out, code := script(t, dir, "config.sh", nil, "--unattended", "--url", "u"); code != 1 || !strings.Contains(out, "required") {
		t.Errorf("missing args: %q %d", out, code)
	}
	if os.Geteuid() == 0 {
		cmd := exec.Command(filepath.Join(dir, "config.sh"), "--version")
		cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
		out, err := cmd.CombinedOutput()
		if err == nil || !strings.Contains(string(out), "Must not run with sudo") {
			t.Errorf("root must be refused: %q %v", out, err)
		}
	}
}

// --- run.sh ---

type lockedBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}
func (l *lockedBuf) String() string { l.mu.Lock(); defer l.mu.Unlock(); return l.b.String() }

type running struct {
	cmd  *exec.Cmd
	out  *lockedBuf
	done chan error
}

func startRun(t *testing.T, dir string, env ...string) *running {
	t.Helper()
	r := &running{out: &lockedBuf{}, done: make(chan error, 1)}
	r.cmd = exec.Command(filepath.Join(dir, "run.sh"))
	r.cmd.Env = append(os.Environ(), env...)
	r.cmd.Stdout, r.cmd.Stderr = r.out, r.out
	if err := r.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { r.done <- r.cmd.Wait() }()
	t.Cleanup(func() { r.cmd.Process.Kill() })
	return r
}

func (r *running) waitFor(t *testing.T, substr string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(r.out.String(), substr) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %q; output:\n%s", substr, r.out.String())
}

func (r *running) wait(t *testing.T) int {
	t.Helper()
	select {
	case err := <-r.done:
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return ee.ExitCode()
		}
		if err != nil {
			t.Fatal(err)
		}
		return 0
	case <-time.After(3 * time.Second):
		t.Fatalf("run.sh did not exit; output:\n%s", r.out.String())
		return -1
	}
}

func configured(t *testing.T) string {
	t.Helper()
	dir := runnerDir(t)
	if out, code := configure(t, dir); code != 0 {
		t.Fatalf("configure: %q", out)
	}
	return dir
}

func TestRunNotConfigured(t *testing.T) {
	r := startRun(t, runnerDir(t))
	if code := r.wait(t); code != 1 || !strings.Contains(r.out.String(), "Runner is not configured.") {
		t.Errorf("code %d, out %q", code, r.out.String())
	}
}

func TestRunListensAndStopsOnSIGINT(t *testing.T) {
	dir := configured(t)
	r := startRun(t, dir)
	r.waitFor(t, "Listening for Jobs")
	out := r.out.String()
	for _, want := range []string{"√ Connected to GitHub", "Current runner version: '2.999.0-fake'"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %q", want, out)
		}
	}
	r.cmd.Process.Signal(syscall.SIGINT)
	if code := r.wait(t); code != 0 || !strings.Contains(r.out.String(), "Exiting...") {
		t.Errorf("code %d, out %q", code, r.out.String())
	}
	logs, _ := filepath.Glob(filepath.Join(dir, "_diag", "Runner_*.log"))
	if len(logs) != 1 {
		t.Fatalf("diag logs = %v", logs)
	}
	if b, _ := os.ReadFile(logs[0]); !strings.Contains(string(b), "Listening for Jobs") {
		t.Errorf("diag log = %q", b)
	}
}

func TestRunStopsOnSIGTERM(t *testing.T) {
	r := startRun(t, configured(t))
	r.waitFor(t, "Listening for Jobs")
	r.cmd.Process.Signal(syscall.SIGTERM)
	if code := r.wait(t); code != 0 {
		t.Errorf("code = %d", code)
	}
}

func TestRunExitCodeFromEnvAndDotEnv(t *testing.T) {
	dir := configured(t)
	r := startRun(t, dir, "FAKE_RUNNER_EXIT_CODE=7")
	if code := r.wait(t); code != 7 || !strings.Contains(r.out.String(), "Current runner version") || strings.Contains(r.out.String(), "Listening") {
		t.Errorf("env: code %d, out %q", code, r.out.String())
	}

	os.WriteFile(filepath.Join(dir, ".env"), []byte("LANG=C\r\nFAKE_RUNNER_EXIT_CODE=3\n# comment=x\nnoequals\n"), 0o600)
	r = startRun(t, dir)
	if code := r.wait(t); code != 3 {
		t.Errorf(".env: code %d, out %q", code, r.out.String())
	}
}

func sendJob(t *testing.T, dir, req string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, ".fake-job-request"), []byte(req+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func exists(dir string, parts ...string) bool {
	_, err := os.Stat(filepath.Join(append([]string{dir}, parts...)...))
	return err == nil
}

func TestRunSimulatedJobWithoutHook(t *testing.T) {
	dir := configured(t)
	r := startRun(t, dir)
	r.waitFor(t, "Listening for Jobs")
	sendJob(t, dir, "build")
	r.waitFor(t, "Job build completed with result: Succeeded")
	if !strings.Contains(r.out.String(), "Running job: build") {
		t.Errorf("out = %q", r.out.String())
	}
	for _, m := range []string{"_work/_tool", "_work/_actions", "_work/fakerepo/fakerepo", "_work/_temp"} {
		if !exists(dir, m, "fake-marker") {
			t.Errorf("missing marker in %s", m)
		}
	}
	if exists(dir, ".fake-job-request") {
		t.Error("request file should be consumed")
	}
}

func TestRunJobHook(t *testing.T) {
	dir := configured(t)
	hook := filepath.Join(t.TempDir(), "hook.sh")
	os.WriteFile(hook, []byte("#!/bin/bash\necho \"hook root=$RUNNER_ROOT ws=$GITHUB_WORKSPACE\"\nrm -f \"$GITHUB_WORKSPACE/fake-marker\"\n[ -z \"$FAIL_HOOK\" ]\n"), 0o755)

	r := startRun(t, dir, "ACTIONS_RUNNER_HOOK_JOB_COMPLETED="+hook)
	r.waitFor(t, "Listening for Jobs")
	sendJob(t, dir, "j1")
	r.waitFor(t, "Job j1 completed with result: Succeeded")
	if exists(dir, "_work/fakerepo/fakerepo/fake-marker") {
		t.Error("hook should have removed the workspace marker")
	}
	log, _ := os.ReadFile(filepath.Join(dir, "_diag", "fake-job-1.log"))
	if !strings.Contains(string(log), "hook root="+dir) || !strings.Contains(string(log), "ws="+dir+"/_work/fakerepo/fakerepo") {
		t.Errorf("job log = %q", log)
	}
	r.cmd.Process.Signal(syscall.SIGINT)
	r.wait(t)

	r = startRun(t, dir, "ACTIONS_RUNNER_HOOK_JOB_COMPLETED="+hook, "FAIL_HOOK=1")
	r.waitFor(t, "Listening for Jobs")
	sendJob(t, dir, "j2")
	r.waitFor(t, "Job j2 completed with result: Failed")
}

func TestRunRequireJobContainer(t *testing.T) {
	dir := configured(t)
	r := startRun(t, dir, "ACTIONS_RUNNER_REQUIRE_JOB_CONTAINER=true")
	r.waitFor(t, "Listening for Jobs")

	sendJob(t, dir, "plain nocontainer")
	r.waitFor(t, "Job plain completed with result: Failed")
	log, _ := os.ReadFile(filepath.Join(dir, "_diag", "fake-job-1.log"))
	if !strings.Contains(string(log), "Jobs without a job container are forbidden on this runner") {
		t.Errorf("job log = %q", log)
	}
	if exists(dir, "_work") {
		t.Error("rejected job must not touch _work")
	}

	sendJob(t, dir, "boxed")
	r.waitFor(t, "Job boxed completed with result: Succeeded")
}

func TestNoContainerAllowedWhenNotRequired(t *testing.T) {
	dir := configured(t)
	r := startRun(t, dir)
	r.waitFor(t, "Listening for Jobs")
	sendJob(t, dir, "plain nocontainer")
	r.waitFor(t, "Job plain completed with result: Succeeded")
}
