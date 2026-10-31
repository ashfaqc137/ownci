//go:build linux

package supervisor

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/ashfaqc137/ownci/internal/config"
	"github.com/ashfaqc137/ownci/internal/testutil"
)

const testName = "t07"

// TestMain doubles as the detached-supervisor entry point: tests that need a
// supervisor in a separate process re-exec the test binary with
// OWNCI_TEST_SUPERVISE_ROOT set (so no ownci binary has to be built).
func TestMain(m *testing.M) {
	if root := os.Getenv("OWNCI_TEST_SUPERVISE_ROOT"); root != "" {
		grace, _ := time.ParseDuration(os.Getenv("OWNCI_TEST_GRACE"))
		st, err := Run(context.Background(), Options{
			Paths: config.NewPaths(root), Name: testName, Mode: ModeDetached,
			Grace: grace,
		})
		if err != nil || st.Phase != PhaseStopped {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

type rig struct {
	t     *testing.T
	paths config.Paths
	dir   string
}

func newRig(t *testing.T) *rig {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not on PATH")
	}
	paths := config.NewPaths(t.TempDir())
	if err := paths.Ensure(); err != nil {
		t.Fatal(err)
	}
	dir := paths.RunnerDir(testName)
	testutil.WriteFakeRunner(t, dir, testutil.FakeRunnerOptions{Version: "9.9.9-fake"})
	if err := os.WriteFile(filepath.Join(dir, ".runner"), []byte(`{"agentName":"`+testName+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return &rig{t: t, paths: paths, dir: dir}
}

func (r *rig) opts() Options {
	return Options{
		Paths: r.paths, Name: testName, Mode: ModeForeground,
		Grace:   5 * time.Second,
		Backoff: []time.Duration{20 * time.Millisecond, 20 * time.Millisecond, 20 * time.Millisecond},
	}
}

type running struct {
	sigs     chan os.Signal
	done     chan result
	finished bool
}

type result struct {
	st  State
	err error
}

func (r *rig) start(o Options) *running {
	r.t.Helper()
	sigs := make(chan os.Signal, 2)
	o.Signals = sigs
	run := &running{sigs: sigs, done: make(chan result, 1)}
	go func() {
		st, err := Run(context.Background(), o)
		run.done <- result{st, err}
	}()
	r.t.Cleanup(func() {
		if run.finished {
			return
		}
		select {
		case <-run.done:
		default:
			sigs <- syscall.SIGTERM
			sigs <- syscall.SIGTERM
			select {
			case <-run.done:
			case <-time.After(10 * time.Second):
			}
		}
	})
	return run
}

func (r *running) wait(t *testing.T) result {
	t.Helper()
	select {
	case res := <-r.done:
		r.finished = true
		return res
	case <-time.After(30 * time.Second):
		t.Fatal("supervisor did not finish")
		return result{}
	}
}

func (r *rig) waitFor(cond func(*State) bool, what string) *State {
	r.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if st, err := ReadState(r.paths.StateFile(testName)); err == nil && st != nil && cond(st) {
			return st
		}
		time.Sleep(25 * time.Millisecond)
	}
	st, _ := ReadState(r.paths.StateFile(testName))
	r.t.Fatalf("timed out waiting for %s; state = %+v", what, st)
	return nil
}

func (r *rig) waitPhase(p Phase) *State {
	r.t.Helper()
	return r.waitFor(func(s *State) bool { return s.Phase == p }, "phase "+string(p))
}

func groupGone(t *testing.T, pgid int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(-pgid, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("processes remain in group %d", pgid)
}

func TestRunListensAndStopsCleanly(t *testing.T) {
	r := newRig(t)
	var echo bytes.Buffer
	o := r.opts()
	o.Echo = &echo
	o.OwnciVersion = "v-test"
	run := r.start(o)

	st := r.waitPhase(PhaseListening)
	if st.RunnerVersion != "9.9.9-fake" || st.RunnerPID == 0 || st.SupervisorPID != os.Getpid() {
		t.Errorf("state = %+v", st)
	}
	if held, info, err := IsRunning(r.paths, testName); err != nil || !held || info.PID != os.Getpid() || info.Mode != ModeForeground || info.OwnciVersion != "v-test" {
		t.Errorf("IsRunning = %v %+v %v", held, info, err)
	}
	pgid := st.RunnerPID

	run.sigs <- syscall.SIGTERM
	res := run.wait(t)
	if res.err != nil || res.st.Phase != PhaseStopped {
		t.Fatalf("result = %+v", res)
	}
	if res.st.ExitCode == nil || *res.st.ExitCode != 0 {
		t.Errorf("exit code = %v", res.st.ExitCode)
	}
	if held, _, _ := IsRunning(r.paths, testName); held {
		t.Error("lock still held after stop")
	}
	if _, err := os.Stat(r.paths.PidFile(testName)); !os.IsNotExist(err) {
		t.Errorf("pidfile still there: %v", err)
	}
	groupGone(t, pgid)

	logBytes, err := os.ReadFile(r.paths.RunnerLog(testName))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Connected to GitHub", "Listening for Jobs", "Exiting..."} {
		if !strings.Contains(string(logBytes), want) || !strings.Contains(echo.String(), want) {
			t.Errorf("%q missing from log or echo:\n%s", want, logBytes)
		}
	}
	if fi, _ := os.Stat(r.paths.RunnerLog(testName)); fi.Mode().Perm() != 0o600 {
		t.Errorf("log mode = %v", fi.Mode().Perm())
	}
}

func TestRunSimulatedJob(t *testing.T) {
	r := newRig(t)
	run := r.start(r.opts())
	r.waitPhase(PhaseListening)

	if err := os.WriteFile(filepath.Join(r.dir, ".fake-job-request"), []byte("build"), 0o600); err != nil {
		t.Fatal(err)
	}
	st := r.waitFor(func(s *State) bool { return s.LastJob != nil }, "a finished job")
	if st.LastJob.Name != "build" || st.LastJob.Result != "Succeeded" {
		t.Errorf("LastJob = %+v", st.LastJob)
	}
	if st.Phase != PhaseListening || st.CurrentJob != "" {
		t.Errorf("after the job: phase %s, current %q", st.Phase, st.CurrentJob)
	}
	run.sigs <- syscall.SIGTERM
	run.wait(t)
}

func TestRunRedactsSecretsInLog(t *testing.T) {
	r := newRig(t)
	script := "#!/usr/bin/env bash\necho 'token ghp_abcdefghijklmnopqrstuvwxyz0123456789'\necho '2026-10-15 10:00:00Z: Listening for Jobs'\nsleep 30\n"
	if err := os.WriteFile(filepath.Join(r.dir, "run.sh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	var echo bytes.Buffer
	o := r.opts()
	o.Echo = &echo
	run := r.start(o)
	r.waitPhase(PhaseListening)
	run.sigs <- syscall.SIGTERM
	run.wait(t)
	b, _ := os.ReadFile(r.paths.RunnerLog(testName))
	if strings.Contains(string(b), "ghp_abc") || strings.Contains(echo.String(), "ghp_abc") {
		t.Errorf("token leaked:\nlog: %s\necho: %s", b, echo.String())
	}
}

func TestRunCrashRestartsThenCrashed(t *testing.T) {
	r := newRig(t)
	o := r.opts()
	o.Env = []string{"FAKE_RUNNER_EXIT_CODE=1"}
	res := r.start(o).wait(t)

	if res.st.Phase != PhaseCrashed {
		t.Fatalf("phase = %s", res.st.Phase)
	}
	if res.st.Restarts != 3 {
		t.Errorf("restarts = %d, want 3", res.st.Restarts)
	}
	if res.st.ExitCode == nil || *res.st.ExitCode != 1 {
		t.Errorf("exit code = %v", res.st.ExitCode)
	}
	if !strings.Contains(res.st.LastError, "Current runner version") {
		t.Errorf("LastError = %q, want the last runner output lines", res.st.LastError)
	}
	if n := len(strings.Split(res.st.LastError, "\n")); n > 20 {
		t.Errorf("LastError has %d lines", n)
	}
	// The final state on disk matches.
	disk, _ := ReadState(r.paths.StateFile(testName))
	if disk == nil || disk.Phase != PhaseCrashed {
		t.Errorf("disk state = %+v", disk)
	}
}

func TestRunVersionDeprecatedDoesNotRestart(t *testing.T) {
	r := newRig(t)
	o := r.opts()
	o.Env = []string{"FAKE_RUNNER_EXIT_CODE=7"}
	res := r.start(o).wait(t)
	if res.st.Phase != PhaseVersionDeprecated || res.st.Restarts != 0 {
		t.Errorf("state = %+v", res.st)
	}
	if res.st.ExitCode == nil || *res.st.ExitCode != 7 {
		t.Errorf("exit code = %v", res.st.ExitCode)
	}
}

func TestRunRegistrationLostAndDuplicateSession(t *testing.T) {
	for _, tc := range []struct {
		line  string
		phase Phase
	}{
		{"The runner no longer exists on the server. Please reconfigure the runner.", PhaseRegistrationLost},
		{"A session for this runner already exists.", PhaseDuplicateSession},
	} {
		t.Run(string(tc.phase), func(t *testing.T) {
			r := newRig(t)
			script := "#!/usr/bin/env bash\necho '" + tc.line + "'\nexit 1\n"
			if err := os.WriteFile(filepath.Join(r.dir, "run.sh"), []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			res := r.start(r.opts()).wait(t)
			if res.st.Phase != tc.phase || res.st.Restarts != 0 {
				t.Errorf("state = %+v", res.st)
			}
			if !strings.Contains(res.st.LastError, tc.line) {
				t.Errorf("LastError = %q", res.st.LastError)
			}
		})
	}
}

func TestRunSecondSupervisorIsRejected(t *testing.T) {
	r := newRig(t)
	run := r.start(r.opts())
	r.waitPhase(PhaseListening)

	_, err := Run(context.Background(), r.opts())
	if !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("err = %v, want ErrAlreadyRunning", err)
	}
	// The failed attempt must not have damaged the first one's files.
	if _, err := os.Stat(r.paths.PidFile(testName)); err != nil {
		t.Errorf("pidfile gone: %v", err)
	}
	if st, _ := ReadState(r.paths.StateFile(testName)); st == nil || st.Phase != PhaseListening {
		t.Errorf("state = %+v", st)
	}
	run.sigs <- syscall.SIGTERM
	run.wait(t)
}

func TestStalePidfileWithFreeLock(t *testing.T) {
	r := newRig(t)
	if err := writePidFile(r.paths.PidFile(testName), PidInfo{PID: 999999, Mode: ModeDetached}); err != nil {
		t.Fatal(err)
	}
	if running, _, err := IsRunning(r.paths, testName); err != nil || running {
		t.Fatalf("IsRunning = %v, %v; want false", running, err)
	}
	run := r.start(r.opts())
	r.waitPhase(PhaseListening)
	running, info, _ := IsRunning(r.paths, testName)
	if !running || info.PID != os.Getpid() {
		t.Errorf("IsRunning = %v %+v", running, info)
	}
	run.sigs <- syscall.SIGTERM
	run.wait(t)
}

func TestRunBeforeLaunchErrorEndsRun(t *testing.T) {
	r := newRig(t)
	o := r.opts()
	o.BeforeLaunch = func(context.Context) error { return errors.New("boom") }
	res := r.start(o).wait(t)
	if res.err == nil || res.st.Phase != PhaseCrashed || !strings.Contains(res.st.LastError, "boom") {
		t.Errorf("result = %+v", res)
	}
}

func TestRunStopDuringBeforeLaunch(t *testing.T) {
	r := newRig(t)
	o := r.opts()
	entered := make(chan struct{})
	o.BeforeLaunch = func(ctx context.Context) error {
		close(entered)
		<-ctx.Done() // a long image pull, cancelled by the stop
		return ctx.Err()
	}
	run := r.start(o)
	<-entered
	run.sigs <- syscall.SIGINT
	if res := run.wait(t); res.err != nil || res.st.Phase != PhaseStopped {
		t.Errorf("result = %+v, want a clean stop", res)
	}
}

func TestRunStopDuringBackoff(t *testing.T) {
	r := newRig(t)
	o := r.opts()
	o.Backoff = []time.Duration{time.Hour}
	o.Env = []string{"FAKE_RUNNER_EXIT_CODE=1"}
	run := r.start(o)
	r.waitFor(func(s *State) bool { return s.ExitCode != nil }, "the first exit")
	run.sigs <- syscall.SIGTERM
	if res := run.wait(t); res.st.Phase != PhaseStopped {
		t.Errorf("phase = %s", res.st.Phase)
	}
}

func TestRunEscalatesToSIGKILL(t *testing.T) {
	r := newRig(t)
	// A runner that ignores SIGINT.
	script := "#!/usr/bin/env bash\ntrap '' INT TERM\necho 'Listening for Jobs'\nwhile true; do sleep 1; done\n"
	if err := os.WriteFile(filepath.Join(r.dir, "run.sh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	o := r.opts()
	o.Grace = 300 * time.Millisecond
	run := r.start(o)
	st := r.waitPhase(PhaseListening)
	run.sigs <- syscall.SIGTERM
	res := run.wait(t)
	if res.st.Phase != PhaseStopped {
		t.Errorf("phase = %s", res.st.Phase)
	}
	groupGone(t, st.RunnerPID)
}

// spawnSupervisor starts a supervisor in its own process (the test binary).
func (r *rig) spawnSupervisor(grace string) *exec.Cmd {
	r.t.Helper()
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), "OWNCI_TEST_SUPERVISE_ROOT="+r.paths.Root, "OWNCI_TEST_GRACE="+grace)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		r.t.Fatal(err)
	}
	exited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(exited) }()
	r.t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-exited
	})
	return cmd
}

func anyProcess(int) bool { return true }

func TestStopGraceful(t *testing.T) {
	r := newRig(t)
	cmd := r.spawnSupervisor("5s")
	st := r.waitPhase(PhaseListening)
	if st.SupervisorPID != cmd.Process.Pid {
		t.Fatalf("supervisor pid %d, state says %d", cmd.Process.Pid, st.SupervisorPID)
	}
	if err := Stop(context.Background(), r.paths, testName, StopOptions{Timeout: 10 * time.Second, IsOwnci: anyProcess}); err != nil {
		t.Fatal(err)
	}
	if running, _, _ := IsRunning(r.paths, testName); running {
		t.Error("still running")
	}
	if _, err := os.Stat(r.paths.PidFile(testName)); !os.IsNotExist(err) {
		t.Errorf("pidfile: %v", err)
	}
	if got, _ := ReadState(r.paths.StateFile(testName)); got == nil || got.Phase != PhaseStopped {
		t.Errorf("state = %+v", got)
	}
	groupGone(t, st.RunnerPID)

	if err := Stop(context.Background(), r.paths, testName, StopOptions{}); !errors.Is(err, ErrNotRunning) {
		t.Errorf("second Stop = %v, want ErrNotRunning", err)
	}
}

func TestStopForce(t *testing.T) {
	r := newRig(t)
	script := "#!/usr/bin/env bash\ntrap '' INT TERM\necho 'Listening for Jobs'\nwhile true; do sleep 1; done\n"
	if err := os.WriteFile(filepath.Join(r.dir, "run.sh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	// A supervisor with a long grace period and a runner that ignores SIGINT:
	// the graceful stop cannot finish within the test's timeout.
	r.spawnSupervisor("1h")
	st := r.waitPhase(PhaseListening)

	err := Stop(context.Background(), r.paths, testName, StopOptions{Timeout: 300 * time.Millisecond, IsOwnci: anyProcess})
	if !errors.Is(err, ErrStopTimeout) {
		t.Fatalf("Stop without Force = %v, want ErrStopTimeout", err)
	}
	if err := Stop(context.Background(), r.paths, testName, StopOptions{Timeout: 300 * time.Millisecond, Force: true, IsOwnci: anyProcess}); err != nil {
		t.Fatal(err)
	}
	if running, _, _ := IsRunning(r.paths, testName); running {
		t.Error("still running after force")
	}
	groupGone(t, st.RunnerPID)
	if got, _ := ReadState(r.paths.StateFile(testName)); got == nil || got.Phase != PhaseStopped {
		t.Errorf("state = %+v", got)
	}
	if _, err := os.Stat(r.paths.PidFile(testName)); !os.IsNotExist(err) {
		t.Errorf("pidfile: %v", err)
	}
}

func TestStopRefusesNonOwnciPid(t *testing.T) {
	r := newRig(t)
	run := r.start(r.opts()) // the pid in the pidfile is the test binary
	r.waitPhase(PhaseListening)
	err := Stop(context.Background(), r.paths, testName, StopOptions{Timeout: time.Second})
	if err == nil || errors.Is(err, ErrNotRunning) || errors.Is(err, ErrStopTimeout) {
		t.Fatalf("err = %v, want a refusal to signal a non-ownci process", err)
	}
	if !strings.Contains(err.Error(), strconv.Itoa(os.Getpid())) && !strings.Contains(err.Error(), "ownci process") {
		t.Errorf("err = %v", err)
	}
	run.sigs <- syscall.SIGTERM
	run.wait(t)
}

func TestLastLines(t *testing.T) {
	p := filepath.Join(t.TempDir(), "log")
	if got := LastLines(p, 3); got != nil {
		t.Errorf("missing file: %v", got)
	}
	var b strings.Builder
	for i := 1; i <= 10; i++ {
		b.WriteString("line " + strconv.Itoa(i) + "\n")
	}
	if err := os.WriteFile(p, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	got := LastLines(p, 3)
	if strings.Join(got, "|") != "line 8|line 9|line 10" {
		t.Errorf("got %v", got)
	}
	if got := LastLines(p, 50); len(got) != 10 {
		t.Errorf("len = %d", len(got))
	}
}

// A runner left behind by a SIGKILLed supervisor must be cleaned up before the
// next launch; processes outside the runner directory must be left alone.
func TestRunKillsLeftoverRunnerProcesses(t *testing.T) {
	r := newRig(t)
	spawn := func(script string) *exec.Cmd {
		t.Helper()
		if err := os.WriteFile(script, []byte("#!/bin/bash\nwhile :; do sleep 0.2; done\n"), 0o700); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(script)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); _ = cmd.Wait() })
		return cmd
	}
	orphan := spawn(filepath.Join(r.dir, "orphan-listener.sh"))
	bystander := spawn(filepath.Join(t.TempDir(), "bystander.sh"))
	orphanDone := make(chan struct{})
	go func() { _ = orphan.Wait(); close(orphanDone) }()

	run := r.start(r.opts())
	r.waitPhase(PhaseListening)

	select {
	case <-orphanDone:
	case <-time.After(5 * time.Second):
		t.Fatal("leftover runner process was not killed")
	}
	if err := syscall.Kill(bystander.Process.Pid, 0); err != nil {
		t.Errorf("process outside the runner directory was signalled: %v", err)
	}
	log, _ := os.ReadFile(r.paths.RunnerLog(testName))
	if !strings.Contains(string(log), "leftover runner process") {
		t.Errorf("runner log has no notice about the leftover process:\n%s", log)
	}
	run.sigs <- syscall.SIGTERM
	run.wait(t)
}

// TestRunRecyclesAfterEachJob covers container-runner mode: the runner runs
// with --once (through a custom Command), exits after its job, and is launched
// again with BeforeLaunch and AfterExit around every launch, without counting
// restarts.
func TestRunRecyclesAfterEachJob(t *testing.T) {
	r := newRig(t)
	var launches, exits atomic.Int32
	o := r.opts()
	o.Command = []string{"bash", filepath.Join(r.dir, "run.sh"), "--once"}
	o.RecycleAfterJob = true
	o.BeforeLaunch = func(context.Context) error { launches.Add(1); return nil }
	o.AfterExit = func(context.Context) { exits.Add(1) }
	run := r.start(o)
	r.waitPhase(PhaseListening)

	for i, job := range []string{"one", "two"} {
		if err := os.WriteFile(filepath.Join(r.dir, ".fake-job-request"), []byte(job), 0o600); err != nil {
			t.Fatal(err)
		}
		r.waitFor(func(s *State) bool { return s.LastJob != nil && s.LastJob.Name == job }, "job "+job)
		want := int32(i + 2)
		r.waitFor(func(s *State) bool { return launches.Load() == want && s.Phase == PhaseListening }, "a fresh runner after job "+job)
	}
	run.sigs <- syscall.SIGTERM
	res := run.wait(t)
	if res.err != nil || res.st.Phase != PhaseStopped || res.st.Restarts != 0 {
		t.Fatalf("result = %+v", res)
	}
	if launches.Load() != 3 || exits.Load() != 3 {
		t.Errorf("launches = %d, exits = %d, want 3 and 3", launches.Load(), exits.Load())
	}
	b, _ := os.ReadFile(r.paths.RunnerLog(testName))
	if n := strings.Count(string(b), "[ownci] job finished; starting a fresh runner"); n != 2 {
		t.Errorf("%d recycle notices in the log:\n%s", n, b)
	}
}

// Without RecycleAfterJob an exit after a job is a crash like any other.
func TestRunExitAfterJobWithoutRecycleIsACrash(t *testing.T) {
	r := newRig(t)
	o := r.opts()
	o.Command = []string{"bash", filepath.Join(r.dir, "run.sh"), "--once"}
	run := r.start(o)
	r.waitPhase(PhaseListening)
	if err := os.WriteFile(filepath.Join(r.dir, ".fake-job-request"), []byte("one"), 0o600); err != nil {
		t.Fatal(err)
	}
	r.waitFor(func(s *State) bool { return s.Restarts == 1 }, "a counted restart")
	run.sigs <- syscall.SIGTERM
	run.wait(t)
}

func TestRunLaunchFailureIsNotRetried(t *testing.T) {
	r := newRig(t)
	script := filepath.Join(r.t.TempDir(), "docker")
	if err := os.WriteFile(script, []byte("#!/usr/bin/env bash\necho 'docker: Error response from daemon: No such image: img' >&2\nexit 125\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	var exits atomic.Int32
	o := r.opts()
	o.Command = []string{script, "run", "img"}
	o.LaunchFailed = func(code int) bool { return code == 125 }
	o.AfterExit = func(context.Context) { exits.Add(1) }
	res := r.start(o).wait(t)
	if res.err != nil || res.st.Phase != PhaseCrashed || res.st.Restarts != 0 || res.st.ExitCode == nil || *res.st.ExitCode != 125 {
		t.Fatalf("result = %+v", res)
	}
	if !strings.Contains(res.st.LastError, "could not be launched (exit code 125)") || !strings.Contains(res.st.LastError, "No such image") {
		t.Errorf("LastError = %q", res.st.LastError)
	}
	if exits.Load() != 1 {
		t.Errorf("AfterExit ran %d times", exits.Load())
	}
}

// A supervisor that cannot react (here: stopped) is SIGKILLed, and AfterKill
// cleans up what it would have (container mode: the runner container).
func TestStopForceRunsAfterKill(t *testing.T) {
	r := newRig(t)
	cmd := r.spawnSupervisor("1h")
	r.waitPhase(PhaseListening)
	if err := cmd.Process.Signal(syscall.SIGSTOP); err != nil {
		t.Fatal(err)
	}
	afterKill := 0
	err := Stop(context.Background(), r.paths, testName, StopOptions{Timeout: 300 * time.Millisecond, Force: true, IsOwnci: anyProcess,
		AfterKill: func(context.Context) { afterKill++ }})
	if err != nil {
		t.Fatal(err)
	}
	if afterKill != 1 {
		t.Errorf("AfterKill ran %d times", afterKill)
	}
	if running, _, _ := IsRunning(r.paths, testName); running {
		t.Error("still running after force")
	}
}
