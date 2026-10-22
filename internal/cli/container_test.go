package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ashfaqc137/ownci/internal/config"
	"github.com/ashfaqc137/ownci/internal/hooks"
	"github.com/ashfaqc137/ownci/internal/isolation"
	"github.com/ashfaqc137/ownci/internal/runner"
	"github.com/ashfaqc137/ownci/internal/runnercontainer"
	"github.com/ashfaqc137/ownci/internal/supervisor"
	"github.com/ashfaqc137/ownci/internal/testutil"
)

const testImage = "my/runner:1@sha256:abc"

// installContainerRunner is installRunner in container-runner mode. Every
// command in these tests runs the real exec runner against the fake docker on
// PATH, in-process and in the re-executed supervisor alike.
func installContainerRunner(t *testing.T) (config.Paths, testutil.FakeDocker) {
	t.Helper()
	p := installRunner(t)
	fd := testutil.UseFakeDocker(t)
	cfg, err := config.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Runner.Mode, cfg.Runner.Image, cfg.Runner.Isolation = config.ModeContainerRunner, testImage, "docker-runner-container"
	if err := config.Save(p, cfg); err != nil {
		t.Fatal(err)
	}
	return p, fd
}

func runnerLaunches(fd testutil.FakeDocker) int {
	return len(fd.CallsMatching("run --rm --init --pull never --name ownci-" + testRunner))
}

func TestStartContainerRunnerEndToEnd(t *testing.T) {
	useChildBinary(t)
	p, fd := installContainerRunner(t)
	t.Cleanup(func() { stopRunner(t, p) })
	dir := p.RunnerDir(testRunner)
	container := runnercontainer.ContainerName(testRunner)
	// A container left behind by a killed supervisor holds the session.
	fd.SetContainer(container, "running "+testImage)

	stdout, stderr, code := runCLI(t, "--data-dir", p.Root, "start", "--detach", "--wait", "30s")
	if code != 0 || !strings.Contains(stdout, "is listening for jobs on me/demo in a container ("+testImage+")") {
		t.Fatalf("start: code %d\n%s\n%s", code, stdout, stderr)
	}
	calls := strings.Join(fd.Calls(), "\n")
	rm, run := strings.Index(calls, "rm --force "+container), strings.Index(calls, "run --rm --init")
	if rm < 0 || run < 0 || rm > run {
		t.Errorf("want the leftover container removed before the launch:\n%s", calls)
	}
	for _, want := range []string{"-v " + dir + ":" + dir + " -w " + dir, hooks.JobCompletedPath(dir) + ":ro", "--entrypoint bash " + testImage + " -c"} {
		if !strings.Contains(calls, want) {
			t.Errorf("docker run lacks %q:\n%s", want, calls)
		}
	}
	env, _ := runner.ReadEnv(dir)
	if _, ok := env[isolation.EnvRequireContainer]; ok {
		t.Errorf(".env still requires job containers: %v", env)
	}

	// A job without container: runs (inside the runner container), the
	// workspace is wiped, and the runner is relaunched fresh.
	if err := os.WriteFile(filepath.Join(dir, ".fake-job-request"), []byte("build nocontainer"), 0o600); err != nil {
		t.Fatal(err)
	}
	st := waitForJobs(t, p, 1, "build")
	if st.LastJob.Result != "Succeeded" {
		t.Errorf("LastJob = %+v", st.LastJob)
	}
	waitUntil(t, func() bool {
		st, _ := supervisor.ReadState(p.StateFile(testRunner))
		return runnerLaunches(fd) == 2 && st != nil && st.Phase == supervisor.PhaseListening
	})
	if got := listDir(t, filepath.Join(dir, "_work")); got != "_temp" {
		t.Errorf("_work contains %q after the job, want only _temp", got)
	}
	if st, _ := supervisor.ReadState(p.StateFile(testRunner)); st.Restarts != 0 {
		t.Errorf("a recycle counted as a restart: %+v", st)
	}

	stdout, _, _ = runCLI(t, "--data-dir", p.Root, "status")
	for _, want := range []string{
		"Mode         container-runner  (image " + testImage + ")   container " + container + ": running",
		"Isolation    runner image ✓   cleanup hook ✓   cleanup image ✓   last cleanup: ok at",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("status lacks %q:\n%s", want, stdout)
		}
	}

	stdout, _, code = runCLI(t, "--data-dir", p.Root, "stop")
	if code != 0 || !strings.Contains(stdout, "stopped") {
		t.Fatalf("stop: %d\n%s", code, stdout)
	}
	if fd.ContainerExists(container) {
		t.Error("the runner container is still there after stop")
	}
}

func TestStartContainerRunnerImageOverride(t *testing.T) {
	useChildBinary(t)
	p, fd := installContainerRunner(t)
	t.Cleanup(func() { stopRunner(t, p) })

	if stdout, stderr, code := runCLI(t, "--data-dir", p.Root, "start", "--detach", "--wait", "30s", "--image", "other:2"); code != 0 {
		t.Fatalf("start: %d\n%s\n%s", code, stdout, stderr)
	}
	if !strings.Contains(strings.Join(fd.Calls(), "\n"), "--entrypoint bash other:2 -c") {
		t.Errorf("--image not used:\n%s", strings.Join(fd.Calls(), "\n"))
	}
	stdout, _, _ := runCLI(t, "--data-dir", p.Root, "status")
	if !strings.Contains(stdout, "container ownci-"+testRunner+": running (image other:2)") {
		t.Errorf("status:\n%s", stdout)
	}
}

func TestStartContainerRunnerMissingImageFailsFast(t *testing.T) {
	useChildBinary(t)
	p, fd := installContainerRunner(t)
	t.Cleanup(func() { stopRunner(t, p) })
	// Docker cannot start the container (e.g. the image vanished): exit 125.
	t.Setenv("FAKE_DOCKER_RUNNER_EXIT", "125")

	_, stderr, code := runCLI(t, "--data-dir", p.Root, "start", "--detach", "--wait", "30s")
	if code == 0 || !strings.Contains(stderr, "could not be started") {
		t.Fatalf("code %d\n%s", code, stderr)
	}
	if n := runnerLaunches(fd); n != 1 {
		t.Errorf("%d launches, want 1 (a launch failure is not retried)", n)
	}
}

func TestStartImageNeedsContainerMode(t *testing.T) {
	p := installRunner(t)
	_, stderr, code := runCLI(t, "--data-dir", p.Root, "start", "--image", "x:1")
	if code != exitUsage || !strings.Contains(stderr, "--image needs a runner set up with --mode container-runner") {
		t.Errorf("code %d\n%s", code, stderr)
	}
}

func TestStopRemovesLeftoverContainer(t *testing.T) {
	p, fd := installContainerRunner(t)
	container := runnercontainer.ContainerName(testRunner)
	fd.SetContainer(container, "running "+testImage)
	stdout, _, code := runCLI(t, "--data-dir", p.Root, "stop")
	if code != 0 || !strings.Contains(stdout, "is not running") || !strings.Contains(stdout, "Removed the leftover runner container "+container) {
		t.Errorf("code %d\n%s", code, stdout)
	}
	if fd.ContainerExists(container) {
		t.Error("container still exists")
	}
}

func TestDoctorContainerRunner(t *testing.T) {
	p, _ := installContainerRunner(t)
	if err := isolation.Install(t.Context(), healthyFake(), p.RunnerDir(testRunner), isolation.PolicyFor(&config.RunnerConfig{Mode: config.ModeContainerRunner, Image: testImage}, "")); err != nil {
		t.Fatal(err)
	}
	stdout, _, _ := runCLI(t, "--data-dir", p.Root, "doctor")
	for _, want := range []string{"[PASS] isolation", "runner runs in a container", "[PASS] runner-container", "image " + testImage + " can host the runner"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("doctor lacks %q:\n%s", want, stdout)
		}
	}

	t.Setenv("FAKE_DOCKER_PROBE_FAIL", "1")
	stdout, _, code := runCLI(t, "--data-dir", p.Root, "doctor")
	if code != exitDoctorBad || !strings.Contains(stdout, "[FAIL] runner-container") || !strings.Contains(stdout, "lacks: libicu") {
		t.Errorf("probe failure: code %d\n%s", code, stdout)
	}
}
