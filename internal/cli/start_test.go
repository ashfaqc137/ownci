package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/ashfaqc137/ownci/internal/config"
	"github.com/ashfaqc137/ownci/internal/execx"
	"github.com/ashfaqc137/ownci/internal/supervisor"
	"github.com/ashfaqc137/ownci/internal/testutil"
	"github.com/ashfaqc137/ownci/internal/testutil/fakegh"
)

const testRunner = "t07"

// TestMain doubles as the `ownci` binary for detached starts: with
// OWNCI_TEST_SUPERVISE=1 the test binary runs the CLI instead of the tests, so
// `start --detach` can re-execute it without building ownci.
func TestMain(m *testing.M) {
	if os.Getenv("OWNCI_TEST_SUPERVISE") == "1" {
		os.Args = append([]string{"ownci"}, os.Args[1:]...)
		os.Exit(Execute())
	}
	os.Exit(m.Run())
}

// installRunner lays out a set-up runner (config + fake runner files + .runner)
// in a fresh data dir and returns the paths.
func installRunner(t *testing.T) config.Paths {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not on PATH")
	}
	// A fake docker on PATH, so a supervisor re-executed by a test (which
	// uses the real exec runner) never touches the real Docker or network.
	testutil.UseFakeDocker(t)
	paths := config.NewPaths(filepath.Join(t.TempDir(), "data"))
	if err := paths.Ensure(); err != nil {
		t.Fatal(err)
	}
	dir := paths.RunnerDir(testRunner)
	testutil.WriteFakeRunner(t, dir, testutil.FakeRunnerOptions{Version: "2.1.0"})
	if err := os.WriteFile(filepath.Join(dir, ".runner"),
		[]byte("\xef\xbb\xbf"+`{"agentId":7,"agentName":"`+testRunner+`","gitHubUrl":"https://github.com/me/demo"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	err := config.Save(paths, &config.Config{Runner: &config.RunnerConfig{
		Name: testRunner, Provider: "github", Repo: "me/demo", URL: "https://github.com/me/demo",
		Labels: []string{"ownci"}, InstalledVersion: "2.1.0", Isolation: "docker-job-container", CreatedAt: time.Now(),
	}})
	if err != nil {
		t.Fatal(err)
	}
	return paths
}

// useChildBinary makes `start --detach` re-execute the test binary.
func useChildBinary(t *testing.T) {
	t.Helper()
	t.Setenv("OWNCI_TEST_SUPERVISE", "1")
	old := executablePath
	executablePath = func() (string, error) { return os.Args[0], nil }
	t.Cleanup(func() { executablePath = old })
}

// stopRunner is registered with t.Cleanup. t.Context() is already cancelled by
// then, so it must not be passed to Stop: Stop would give up waiting for the
// supervisor to exit, and the TempDir cleanup would race its last writes.
func stopRunner(t *testing.T, p config.Paths) {
	t.Helper()
	ctx := context.WithoutCancel(t.Context())
	err := supervisor.Stop(ctx, p, testRunner, supervisor.StopOptions{Timeout: 10 * time.Second, Force: true})
	if err != nil && !errors.Is(err, supervisor.ErrNotRunning) {
		t.Errorf("stopping the runner: %v", err)
	}
}

func TestStartPreconditions(t *testing.T) {
	t.Run("not configured", func(t *testing.T) {
		useFake(t, healthyFake())
		_, stderr, code := runCLI(t, "--data-dir", filepath.Join(t.TempDir(), "d"), "start")
		if code != 1 || !strings.Contains(stderr, "not set up") || !strings.Contains(stderr, "ownci setup --repo") {
			t.Errorf("exit %d, stderr %q", code, stderr)
		}
	})
	t.Run("no registration", func(t *testing.T) {
		useFake(t, healthyFake())
		p := installRunner(t)
		if err := os.Remove(filepath.Join(p.RunnerDir(testRunner), ".runner")); err != nil {
			t.Fatal(err)
		}
		_, stderr, code := runCLI(t, "--data-dir", p.Root, "start")
		if code != 1 || !strings.Contains(stderr, "no registration") || !strings.Contains(stderr, "--force") {
			t.Errorf("exit %d, stderr %q", code, stderr)
		}
	})
	t.Run("docker down", func(t *testing.T) {
		f := execx.NewFake()
		f.SetPath("docker", "/usr/bin/docker")
		f.On("docker", "version").Stderr("Cannot connect to the Docker daemon").Exit(1)
		useFake(t, f)
		p := installRunner(t)
		_, stderr, code := runCLI(t, "--data-dir", p.Root, "start")
		if code != 1 || !strings.Contains(stderr, "Docker daemon") || !strings.Contains(stderr, "Hint:") {
			t.Errorf("exit %d, stderr %q", code, stderr)
		}
		if running, _, _ := supervisor.IsRunning(p, testRunner); running {
			t.Error("runner started despite the failed Docker check")
		}
	})
}

func TestDetachedLifecycle(t *testing.T) {
	useFake(t, healthyFake())
	useChildBinary(t)
	p := installRunner(t)
	t.Cleanup(func() { stopRunner(t, p) })

	// Before start: nothing is running, and status says so.
	stdout, _, code := runCLI(t, "--data-dir", p.Root, "status")
	if code != 0 || !strings.Contains(stdout, "not running") || strings.Contains(stdout, "listening") {
		t.Fatalf("pre-start status (exit %d):\n%s", code, stdout)
	}

	stdout, stderr, code := runCLI(t, "--data-dir", p.Root, "start", "--detach", "--wait", "30s")
	if code != 0 || !strings.Contains(stdout, "is listening for jobs on me/demo") || !strings.Contains(stdout, "ownci stop") {
		t.Fatalf("start --detach (exit %d):\n%s\n%s", code, stdout, stderr)
	}

	stdout, _, _ = runCLI(t, "--data-dir", p.Root, "status")
	for _, want := range []string{"running", "detached", "listening", "2.999.0-fake", "ok (27.0.3, linux)", "me/demo"} {
		if want == "2.999.0-fake" {
			want = "runner 2.1.0" // the fake runner built by installRunner reports 2.1.0
		}
		if !strings.Contains(stdout, want) {
			t.Errorf("status lacks %q:\n%s", want, stdout)
		}
	}

	// A second start is refused.
	_, stderr, code = runCLI(t, "--data-dir", p.Root, "start")
	if code != 1 || !strings.Contains(stderr, "already running") {
		t.Errorf("second start: exit %d, stderr %q", code, stderr)
	}
	if code := mustExit(t, "--data-dir", p.Root, "setup", "--repo", "me/demo", "--force"); code != 1 {
		t.Errorf("setup --force while running: exit %d, want 1", code)
	}

	// A simulated job shows up as the last job.
	if err := os.WriteFile(filepath.Join(p.RunnerDir(testRunner), ".fake-job-request"), []byte("build"), 0o600); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool {
		st, _ := supervisor.ReadState(p.StateFile(testRunner))
		return st != nil && st.LastJob != nil
	})
	stdout, _, _ = runCLI(t, "--data-dir", p.Root, "status", "--json")
	var rep map[string]any
	if err := json.Unmarshal([]byte(stdout), &rep); err != nil {
		t.Fatalf("status --json: %v\n%s", err, stdout)
	}
	proc := rep["process"].(map[string]any)
	if proc["running"] != true || proc["mode"] != "detached" {
		t.Errorf("process = %v", proc)
	}
	state := rep["state"].(map[string]any)
	if state["phase"] != "listening" || state["lastJob"].(map[string]any)["result"] != "Succeeded" {
		t.Errorf("state = %v", state)
	}
	stdout, _, _ = runCLI(t, "--data-dir", p.Root, "status")
	if !strings.Contains(stdout, "Last job     build → Succeeded") {
		t.Errorf("status lacks the last job:\n%s", stdout)
	}

	// Stop keeps the registration and leaves no process behind.
	st, _ := supervisor.ReadState(p.StateFile(testRunner))
	runnerPID := st.RunnerPID
	stdout, stderr, code = runCLI(t, "--data-dir", p.Root, "stop")
	if code != 0 || !strings.Contains(stdout, "stopped (registration kept") {
		t.Fatalf("stop (exit %d):\n%s\n%s", code, stdout, stderr)
	}
	if _, err := os.Stat(filepath.Join(p.RunnerDir(testRunner), ".runner")); err != nil {
		t.Errorf("registration lost: %v", err)
	}
	waitUntil(t, func() bool { return syscall.Kill(-runnerPID, 0) == syscall.ESRCH })

	stdout, _, _ = runCLI(t, "--data-dir", p.Root, "status")
	if !strings.Contains(stdout, "not running") || !strings.Contains(stdout, "stopped") || strings.Contains(stdout, "listening") {
		t.Errorf("status after stop:\n%s", stdout)
	}
	stdout, _, code = runCLI(t, "--data-dir", p.Root, "stop")
	if code != 0 || !strings.Contains(stdout, "is not running") {
		t.Errorf("second stop (exit %d): %s", code, stdout)
	}
}

// mustExit runs the CLI and returns just the exit code.
func mustExit(t *testing.T, args ...string) int {
	t.Helper()
	_, _, code := runCLI(t, args...)
	return code
}

func waitUntil(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("condition not reached in time")
}

func TestDetachedFailureReportsPhaseAndLog(t *testing.T) {
	useFake(t, healthyFake())
	useChildBinary(t)
	p := installRunner(t)
	t.Cleanup(func() { stopRunner(t, p) })
	if err := os.WriteFile(filepath.Join(p.RunnerDir(testRunner), ".env"), []byte("FAKE_RUNNER_EXIT_CODE=7\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, stderr, code := runCLI(t, "--data-dir", p.Root, "start", "--detach", "--wait", "30s")
	if code != 1 {
		t.Fatalf("exit %d, stderr:\n%s", code, stderr)
	}
	for _, want := range []string{"version is deprecated", "ownci setup --force", "Current runner version", "Last lines of"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, stderr)
		}
	}
	stdout, _, _ := runCLI(t, "--data-dir", p.Root, "status")
	if !strings.Contains(stdout, "not running") || !strings.Contains(stdout, "version_deprecated") {
		t.Errorf("status:\n%s", stdout)
	}
}

func TestForegroundStartStopsOnSIGINT(t *testing.T) {
	useFake(t, healthyFake())
	p := installRunner(t)
	t.Cleanup(func() { stopRunner(t, p) })
	old := tuneSupervisor
	tuneSupervisor = func(o *supervisor.Options) { o.Grace = 5 * time.Second }
	t.Cleanup(func() { tuneSupervisor = old })

	type res struct {
		out, errOut string
		code        int
	}
	done := make(chan res, 1)
	go func() {
		o, e, c := runCLI(t, "--data-dir", p.Root, "start")
		done <- res{o, e, c}
	}()
	waitUntil(t, func() bool {
		st, _ := supervisor.ReadState(p.StateFile(testRunner))
		return st != nil && st.Phase == supervisor.PhaseListening
	})
	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-done:
		if r.code != 0 {
			t.Fatalf("exit %d\n%s\n%s", r.code, r.out, r.errOut)
		}
		for _, want := range []string{"Listening for Jobs", "Exiting...", "stopped (registration kept"} {
			if !strings.Contains(r.out, want) {
				t.Errorf("stdout lacks %q:\n%s", want, r.out)
			}
		}
	case <-time.After(20 * time.Second):
		t.Fatal("foreground start did not exit after SIGINT")
	}
	if running, _, _ := supervisor.IsRunning(p, testRunner); running {
		t.Error("still running")
	}
}

// writeLiveState fakes a running supervisor: it holds the lock and writes the
// pidfile and state the way supervisor.Run would.
func writeLiveState(t *testing.T, p config.Paths, st supervisor.State, pidfilePID int) {
	t.Helper()
	lock, err := os.OpenFile(p.LockFile(testRunner), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { lock.Close() })
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	writeJSON(t, p.PidFile(testRunner), supervisor.PidInfo{PID: pidfilePID, Mode: "foreground", StartedAt: time.Now().Add(-time.Minute)})
	writeJSON(t, p.StateFile(testRunner), st)
}

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, _ := json.Marshal(v)
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestStatusTruthfulness(t *testing.T) {
	now := time.Now()
	listening := supervisor.State{Name: testRunner, Phase: supervisor.PhaseListening, Since: now, RunnerVersion: "2.5.0", SupervisorPID: 4242}

	tests := []struct {
		name  string
		setup func(t *testing.T, p config.Paths)
		want  []string
		deny  []string
	}{
		{
			name:  "files only: installed, never started",
			setup: func(*testing.T, config.Paths) {},
			want:  []string{"Installed    yes", "Configured   yes", "Process      not running", "State        —"},
			deny:  []string{"running  (", "listening"},
		},
		{
			name: "lock free but state claims listening",
			setup: func(t *testing.T, p config.Paths) {
				writeJSON(t, p.StateFile(testRunner), listening)
			},
			want: []string{"stopped (unexpectedly; last state: listening", "State        —"},
			deny: []string{"Process      running"},
		},
		{
			name: "lock held and state matches",
			setup: func(t *testing.T, p config.Paths) {
				writeLiveState(t, p, listening, 4242)
			},
			want: []string{"Process      running  (supervisor pid 4242, foreground", "State        listening", "runner 2.5.0"},
		},
		{
			name: "lock held, state from another supervisor",
			setup: func(t *testing.T, p config.Paths) {
				writeLiveState(t, p, listening, 9999)
			},
			want: []string{"Process      running", "State        unknown"},
			deny: []string{"State        listening"},
		},
		{
			name: "busy",
			setup: func(t *testing.T, p config.Paths) {
				st := listening
				st.Phase, st.CurrentJob = supervisor.PhaseBusy, "build"
				writeLiveState(t, p, st, 4242)
			},
			want: []string{`busy: running job "build"`},
		},
		{
			name: "crashed",
			setup: func(t *testing.T, p config.Paths) {
				st := listening
				st.Phase, st.LastError = supervisor.PhaseCrashed, "boom\nfinal words"
				writeJSON(t, p.StateFile(testRunner), st)
			},
			want: []string{"Process      not running", "State        crashed", "Last error   final words", "Hint:"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			useFake(t, healthyFake())
			p := installRunner(t)
			tc.setup(t, p)
			stdout, _, code := runCLI(t, "--data-dir", p.Root, "status")
			if code != 0 {
				t.Fatalf("exit %d", code)
			}
			for _, w := range tc.want {
				if !strings.Contains(stdout, w) {
					t.Errorf("missing %q in:\n%s", w, stdout)
				}
			}
			for _, d := range tc.deny {
				if strings.Contains(stdout, d) {
					t.Errorf("unexpected %q in:\n%s", d, stdout)
				}
			}
		})
	}
}

func TestStatusNotConfigured(t *testing.T) {
	useFake(t, healthyFake())
	dir := filepath.Join(t.TempDir(), "d")
	stdout, _, code := runCLI(t, "--data-dir", dir, "status")
	if code != 0 || !strings.Contains(stdout, "Configured   no") || !strings.Contains(stdout, "ownci setup --repo") || !strings.Contains(stdout, dir) {
		t.Errorf("exit %d:\n%s", code, stdout)
	}
	stdout, _, code = runCLI(t, "--data-dir", dir, "status", "--json")
	var rep map[string]any
	if err := json.Unmarshal([]byte(stdout), &rep); code != 0 || err != nil || rep["configured"] != false {
		t.Errorf("exit %d, err %v:\n%s", code, err, stdout)
	}
}

func TestStatusDockerProblemIsReported(t *testing.T) {
	f := execx.NewFake()
	f.SetPath("docker", "/usr/bin/docker")
	f.On("docker", "version").Stderr("Cannot connect to the Docker daemon").Exit(1)
	useFake(t, f)
	p := installRunner(t)
	stdout, _, code := runCLI(t, "--data-dir", p.Root, "status")
	if code != 0 || !strings.Contains(stdout, "Docker       NOT OK") {
		t.Errorf("exit %d:\n%s", code, stdout)
	}
}

func useGitHub(t *testing.T, h http.Handler) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	old := apiBaseURL
	apiBaseURL = func() string { return srv.URL }
	t.Cleanup(func() { apiBaseURL = old })
}

func TestStatusRemote(t *testing.T) {
	live := supervisor.State{Name: testRunner, Phase: supervisor.PhaseListening, Since: time.Now(), SupervisorPID: 4242}

	t.Run("online", func(t *testing.T) {
		useFake(t, healthyFake())
		fg := fakegh.New(fakegh.Options{})
		fg.AddRunner("me/demo", testRunner, "ownci")
		useGitHub(t, fg)
		t.Setenv("GITHUB_TOKEN", "fake-token")
		p := installRunner(t)
		writeLiveState(t, p, live, 4242)
		stdout, _, _ := runCLI(t, "--data-dir", p.Root, "status", "--remote")
		if !strings.Contains(stdout, "GitHub       online, idle") || strings.Contains(stdout, "Warning") {
			t.Errorf("status:\n%s", stdout)
		}
	})
	t.Run("local listening but remote offline warns", func(t *testing.T) {
		useFake(t, healthyFake())
		useGitHub(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, `{"total_count":1,"runners":[{"id":1,"name":"`+testRunner+`","os":"Linux","status":"offline","busy":false,"labels":[]}]}`)
		}))
		t.Setenv("GITHUB_TOKEN", "fake-token")
		p := installRunner(t)
		writeLiveState(t, p, live, 4242)
		stdout, _, _ := runCLI(t, "--data-dir", p.Root, "status", "--remote")
		if !strings.Contains(stdout, "GitHub       offline") || !strings.Contains(stdout, "Warning: locally listening, but GitHub shows the runner offline") {
			t.Errorf("status:\n%s", stdout)
		}
	})
	t.Run("no token", func(t *testing.T) {
		useFake(t, healthyFake()) // the fake has no gh on PATH
		t.Setenv("GITHUB_TOKEN", "")
		t.Setenv("GH_TOKEN", "")
		p := installRunner(t)
		stdout, _, code := runCLI(t, "--data-dir", p.Root, "status", "--remote")
		if code != 0 || !strings.Contains(stdout, "GitHub       skipped (no token)") {
			t.Errorf("exit %d:\n%s", code, stdout)
		}
	})
	t.Run("without --remote GitHub is not queried", func(t *testing.T) {
		useFake(t, healthyFake())
		p := installRunner(t)
		stdout, _, _ := runCLI(t, "--data-dir", p.Root, "status")
		if strings.Contains(stdout, "GitHub       ") {
			t.Errorf("status:\n%s", stdout)
		}
	})
}

func TestVersionShowsRunnerWhenConfigured(t *testing.T) {
	p := installRunner(t)
	stdout, _, code := runCLI(t, "--data-dir", p.Root, "version")
	if code != 0 || !strings.Contains(stdout, "runner: 2.1.0") {
		t.Errorf("exit %d:\n%s", code, stdout)
	}
	stdout, _, _ = runCLI(t, "--data-dir", p.Root, "version", "--json")
	var m map[string]any
	if err := json.Unmarshal([]byte(stdout), &m); err != nil || m["runner"] != "2.1.0" {
		t.Errorf("version --json: %v\n%s", err, stdout)
	}
	// Unconfigured: no runner line, and the data dir is not created.
	dir := filepath.Join(t.TempDir(), "none")
	stdout, _, _ = runCLI(t, "--data-dir", dir, "version")
	if strings.Contains(stdout, "runner:") {
		t.Errorf("unexpected runner line: %s", stdout)
	}
	if _, err := os.Stat(dir); err == nil {
		t.Error("version created the data dir")
	}
}
