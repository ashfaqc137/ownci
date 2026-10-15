package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ashfaqc137/ownci/internal/config"
	"github.com/ashfaqc137/ownci/internal/hooks"
	"github.com/ashfaqc137/ownci/internal/isolation"
	"github.com/ashfaqc137/ownci/internal/supervisor"
)

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func listDir(t *testing.T, dir string) string {
	t.Helper()
	es, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range es {
		names = append(names, e.Name())
	}
	return strings.Join(names, ",")
}

func waitForJobs(t *testing.T, p config.Paths, n int, name string) *supervisor.State {
	t.Helper()
	var st *supervisor.State
	waitUntil(t, func() bool {
		st, _ = supervisor.ReadState(p.StateFile(testRunner))
		return st != nil && st.LastJob != nil && st.LastJob.Name == name
	})
	return st
}

func TestStartEnforcesIsolationEndToEnd(t *testing.T) {
	useFake(t, healthyFake())
	useChildBinary(t)
	p := installRunner(t)
	t.Cleanup(func() { stopRunner(t, p) })
	dir := p.RunnerDir(testRunner)

	if _, _, code := runCLI(t, "--data-dir", p.Root, "start", "--detach", "--wait", "30s"); code != 0 {
		t.Fatal("start --detach failed")
	}
	env := readFile(t, filepath.Join(dir, ".env"))
	for _, want := range []string{"ACTIONS_RUNNER_REQUIRE_JOB_CONTAINER=true", "ACTIONS_RUNNER_HOOK_JOB_COMPLETED=" + filepath.Join(dir, "ownci", "job-completed.sh"), "OWNCI_RUNNER_ROOT=" + dir} {
		if !strings.Contains(env, want) {
			t.Errorf(".env lacks %q:\n%s", want, env)
		}
	}

	// A container job succeeds and its workspace is wiped afterwards.
	if err := os.WriteFile(filepath.Join(dir, ".fake-job-request"), []byte("build"), 0o600); err != nil {
		t.Fatal(err)
	}
	st := waitForJobs(t, p, 1, "build")
	if st.LastJob.Result != "Succeeded" {
		t.Errorf("LastJob = %+v", st.LastJob)
	}
	if got := listDir(t, filepath.Join(dir, "_work")); got != "_temp" {
		t.Errorf("_work contains %q after the job, want only _temp", got)
	}
	var lc map[string]any
	if err := jsonUnmarshal(readFile(t, hooks.LastCleanupPath(dir)), &lc); err != nil || lc["result"] != "ok" {
		t.Errorf("last-cleanup.json = %v (%v)", lc, err)
	}

	// A job without a container is rejected by the runner.
	if err := os.WriteFile(filepath.Join(dir, ".fake-job-request"), []byte("build2 nocontainer"), 0o600); err != nil {
		t.Fatal(err)
	}
	st = waitForJobs(t, p, 2, "build2")
	if st.LastJob.Result != "Failed" {
		t.Errorf("LastJob = %+v", st.LastJob)
	}
	logs, _ := filepath.Glob(filepath.Join(dir, "_diag", "fake-job-*.log"))
	var all string
	for _, l := range logs {
		all += readFile(t, l)
	}
	if !strings.Contains(all, "Jobs without a job container are forbidden on this runner") {
		t.Errorf("no rejection message in the job logs:\n%s", all)
	}

	stdout, _, _ := runCLI(t, "--data-dir", p.Root, "status")
	if !strings.Contains(stdout, "Isolation    container jobs required ✓   cleanup hook ✓   cleanup image ✓   last cleanup: ok at") {
		t.Errorf("status:\n%s", stdout)
	}
}

func TestStartRepairsTamperedIsolation(t *testing.T) {
	useFake(t, healthyFake())
	useChildBinary(t)
	p := installRunner(t)
	t.Cleanup(func() { stopRunner(t, p) })
	dir := p.RunnerDir(testRunner)

	if _, _, code := runCLI(t, "--data-dir", p.Root, "start", "--detach", "--wait", "30s"); code != 0 {
		t.Fatal("first start failed")
	}
	if _, _, code := runCLI(t, "--data-dir", p.Root, "stop"); code != 0 {
		t.Fatal("stop failed")
	}

	// A runner-rewritten .env without the managed keys, and a modified hook.
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("LANG=C.UTF-8\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hooks.JobCompletedPath(dir), []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	stdout, _, _ := runCLI(t, "--data-dir", p.Root, "status")
	if !strings.Contains(stdout, "container jobs required ✗   cleanup hook ✗") {
		t.Errorf("status does not show the damage:\n%s", stdout)
	}

	if _, stderr, code := runCLI(t, "--data-dir", p.Root, "start", "--detach", "--wait", "30s"); code != 0 {
		t.Fatalf("second start failed:\n%s", stderr)
	}
	env := readFile(t, filepath.Join(dir, ".env"))
	if !strings.Contains(env, "LANG=C.UTF-8") || !strings.Contains(env, "ACTIONS_RUNNER_REQUIRE_JOB_CONTAINER=true") {
		t.Errorf(".env not restored:\n%s", env)
	}
	if readFile(t, hooks.JobCompletedPath(dir)) != string(hooks.JobCompletedScript) {
		t.Error("hook not restored")
	}
	// The supervisor's log says what it repaired.
	if sup := readFile(t, logPathSupervisor(&App{Paths: p}, testRunner)); !strings.Contains(sup, "restored the isolation settings") || !strings.Contains(sup, "rewrote the cleanup hook") {
		t.Errorf("supervisor log does not mention the repair:\n%s", sup)
	}
}

func TestStartRefusesWhenIsolationCannotBeEnforced(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	useFake(t, healthyFake())
	p := installRunner(t)
	dir := p.RunnerDir(testRunner)
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })

	stdout, stderr, code := runCLI(t, "--data-dir", p.Root, "start")
	if code != 1 || !strings.Contains(stderr, "Error:") || !strings.Contains(stderr, "Hint:") {
		t.Fatalf("exit %d\n%s\n%s", code, stdout, stderr)
	}
	if running, _, _ := supervisor.IsRunning(p, testRunner); running {
		t.Error("the runner is running without enforced isolation")
	}
	if strings.Contains(stdout, "Listening for Jobs") {
		t.Errorf("the runner was launched:\n%s", stdout)
	}
}

func TestStatusIsolationLine(t *testing.T) {
	useFake(t, healthyFake())
	p := installRunner(t)
	dir := p.RunnerDir(testRunner)

	stdout, _, _ := runCLI(t, "--data-dir", p.Root, "status")
	if !strings.Contains(stdout, "Isolation    container jobs required ✗   cleanup hook ✗   cleanup image ✓   last cleanup: never") {
		t.Errorf("before install:\n%s", stdout)
	}

	if err := isolation.Install(t.Context(), healthyFake(), dir, hooks.CleanupImage); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hooks.LastCleanupPath(dir), []byte(`{"time":"2026-10-15T10:05:41Z","result":"failed","detail":"boom"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout, _, _ = runCLI(t, "--data-dir", p.Root, "status")
	if !strings.Contains(stdout, "container jobs required ✓   cleanup hook ✓   cleanup image ✓   last cleanup: failed at") || !strings.Contains(stdout, "(boom)") {
		t.Errorf("after install:\n%s", stdout)
	}

	stdout, _, _ = runCLI(t, "--data-dir", p.Root, "status", "--json")
	var rep struct {
		Isolation struct {
			JobContainerRequired struct{ OK bool } `json:"jobContainerRequired"`
			CleanupHook          struct{ OK bool } `json:"cleanupHook"`
			LastCleanup          struct{ Result string }
		} `json:"isolation"`
	}
	if err := jsonUnmarshal(stdout, &rep); err != nil || !rep.Isolation.JobContainerRequired.OK || !rep.Isolation.CleanupHook.OK || rep.Isolation.LastCleanup.Result != "failed" {
		t.Errorf("json: %v %+v", err, rep)
	}
}

func TestDoctorIsolationCheck(t *testing.T) {
	useFake(t, healthyFake())
	p := installRunner(t)
	dir := p.RunnerDir(testRunner)

	stdout, _, code := runCLI(t, "--data-dir", p.Root, "doctor")
	if code != 3 || !strings.Contains(stdout, "[FAIL] isolation") || !strings.Contains(stdout, "ownci start") {
		t.Errorf("missing enforcement: exit %d\n%s", code, stdout)
	}

	if err := isolation.Install(t.Context(), healthyFake(), dir, hooks.CleanupImage); err != nil {
		t.Fatal(err)
	}
	stdout, _, code = runCLI(t, "--data-dir", p.Root, "doctor")
	if code != 0 || !strings.Contains(stdout, "[PASS] isolation") {
		t.Errorf("installed: exit %d\n%s", code, stdout)
	}

	// An absent image only warns.
	f := healthyFake()
	f.On("docker", "image", "inspect").Exit(1)
	useFake(t, f)
	stdout, _, code = runCLI(t, "--data-dir", p.Root, "doctor")
	if code != 0 || !strings.Contains(stdout, "[WARN] isolation") || !strings.Contains(stdout, "docker pull "+hooks.CleanupImage) {
		t.Errorf("image missing: exit %d\n%s", code, stdout)
	}
}

func TestDoctorWithoutRunnerHasNoIsolationCheck(t *testing.T) {
	useFake(t, healthyFake())
	stdout, _, _ := runCLI(t, "doctor")
	if strings.Contains(stdout, "isolation") {
		t.Errorf("doctor mentions isolation without a configured runner:\n%s", stdout)
	}
}

func jsonUnmarshal(s string, v any) error { return json.Unmarshal([]byte(s), v) }
