package runnercontainer

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ashfaqc137/ownci/internal/execx"
)

func TestArgsGolden(t *testing.T) {
	dir := "/home/dev/.local/share/ownci/runners/ownci-box"
	got := Args(Spec{Runner: "ownci-box", RunnerDir: dir, Image: "img@sha256:abc", UID: 1000, GID: 1001})
	hook := dir + "/ownci/job-completed.sh"
	want := []string{
		"run", "--rm", "--init", "--pull", "never",
		"--name", "ownci-ownci-box",
		"--label", "ownci.managed=true", "--label", "ownci.runner=ownci-box",
		"--user", "0:0",
		"-v", dir + ":" + dir, "-w", dir,
		"-v", hook + ":" + hook + ":ro",
		"-v", "/var/run/docker.sock:/var/run/docker.sock",
		"-e", "OWNCI_UID=1000", "-e", "OWNCI_GID=1001",
		"-e", "RUNNER_MANUALLY_TRAP_SIG=1", "-e", "ACTIONS_RUNNER_RETURN_VERSION_DEPRECATED_EXIT_CODE=1",
		"--entrypoint", "bash", "img@sha256:abc",
		"-c", entrypointScript, "ownci-entrypoint", "./run.sh", "--once",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Args mismatch:\n got  %q\n want %q", got, want)
	}
}

func TestDefaultImageIsDigestPinned(t *testing.T) {
	if !strings.Contains(DefaultImage, "@sha256:") || len(DefaultImage[strings.Index(DefaultImage, "@sha256:")+8:]) != 64 {
		t.Errorf("DefaultImage %q is not pinned by a sha256 digest", DefaultImage)
	}
}

func TestEntrypointScriptIsValidBash(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not on PATH")
	}
	out, err := exec.Command("bash", "-n", "-c", entrypointScript).CombinedOutput()
	if err != nil {
		t.Fatalf("bash -n: %v\n%s", err, out)
	}
	for _, want := range []string{"setpriv --reuid", "unset ACTIONS_RUNNER_PRINT_LOG_TO_STDOUT", "/etc/sudoers.d/ownci", "> .path", `exit 64`} {
		if !strings.Contains(entrypointScript, want) {
			t.Errorf("entrypoint lacks %q", want)
		}
	}
}

func TestEntrypointRefusesRoot(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not on PATH")
	}
	cmd := exec.Command("bash", "-c", entrypointScript, EntrypointArg0, "true")
	cmd.Env = []string{"OWNCI_UID=0", "OWNCI_GID=0", "PATH=" + os.Getenv("PATH")}
	cmd.Dir = t.TempDir()
	out, err := cmd.CombinedOutput()
	var ee *exec.ExitError
	if !errors.As(err, &ee) || ee.ExitCode() != 64 || !strings.Contains(string(out), "refusing to run the runner as root") {
		t.Errorf("err=%v out=%s", err, out)
	}
}

func TestLaunchFailed(t *testing.T) {
	for code, want := range map[int]bool{0: false, 1: false, 7: false, 125: true, 126: true, 127: true, 130: false} {
		if LaunchFailed(code) != want {
			t.Errorf("LaunchFailed(%d) = %v", code, !want)
		}
	}
}

func TestCheckPath(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(base, "real")
	if err := os.MkdirAll(filepath.Join(real, "runners"), 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		dir  string
		want string // "" = ok
	}{
		{filepath.Join(real, "runners", "r1"), ""},             // does not exist yet: the existing prefix counts
		{filepath.Join(real, "runners"), ""},                   // exists
		{"relative/runners/r1", "not a clean absolute path"},   // relative
		{real + "/runners/../runners/r1", "not a clean"},       // unclean
		{filepath.Join(real, "a:b"), "contains ':' or ','"},    // mount syntax
		{filepath.Join(real, "a,b"), "contains ':' or ','"},    // mount syntax
		{"/mnt/c/Users/dev/ownci/runners/r1", "Windows drive"}, // WSL drive
		{filepath.Join(link, "runners", "r1"), "goes through a symlink"},
	} {
		err := CheckPath(tc.dir)
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("CheckPath(%q) = %v, want ok", tc.dir, err)
		case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
			t.Errorf("CheckPath(%q) = %v, want %q", tc.dir, err, tc.want)
		case err != nil:
			var e *Error
			if !errors.As(err, &e) || e.Hint == "" {
				t.Errorf("CheckPath(%q): no hint: %v", tc.dir, err)
			}
		}
	}
	if err := CheckPath("/mnt/wsl/foo"); err != nil && strings.Contains(err.Error(), "Windows") {
		t.Errorf("/mnt/wsl is not a Windows drive: %v", err)
	}
}

func TestRemove(t *testing.T) {
	ctx := context.Background()
	f := execx.NewFake()
	f.On("docker", "stop").Stdout("ownci-r1\n")
	f.On("docker", "rm").Stdout("ownci-r1\n")
	if err := Remove(ctx, f, "r1"); err != nil {
		t.Fatal(err)
	}
	// SIGINT first, so the listener closes its GitHub session; then remove.
	calls := f.Calls()
	if len(calls) != 2 || strings.Join(calls[0].Args, " ") != "stop --signal SIGINT --time 30 ownci-r1" ||
		strings.Join(calls[1].Args, " ") != "rm --force ownci-r1" {
		t.Errorf("calls = %+v", calls)
	}

	f = execx.NewFake()
	f.On("docker", "stop").Exit(1).Stderr("Error response from daemon: No such container: ownci-r1")
	f.On("docker", "rm").Exit(1).Stderr("Error response from daemon: No such container: ownci-r1")
	if err := Remove(ctx, f, "r1"); err != nil {
		t.Errorf("missing container: %v", err)
	}

	f = execx.NewFake()
	f.On("docker", "stop").Exit(1).Stderr("Cannot connect to the Docker daemon")
	f.On("docker", "rm").Exit(1).Stderr("Cannot connect to the Docker daemon")
	if err := Remove(ctx, f, "r1"); err == nil || !strings.Contains(err.Error(), "Cannot connect") {
		t.Errorf("daemon down: %v", err)
	}
}

func TestInspect(t *testing.T) {
	ctx := context.Background()
	f := execx.NewFake()
	f.On("docker", "inspect").Stdout("running img@sha256:abc\n")
	info, err := Inspect(ctx, f, "r1")
	if err != nil || info.State != "running" || info.Image != "img@sha256:abc" {
		t.Errorf("Inspect = %+v, %v", info, err)
	}
	if c := f.Calls()[0]; c.Args[len(c.Args)-1] != "ownci-r1" {
		t.Errorf("call = %v", c.Args)
	}

	f = execx.NewFake()
	f.On("docker", "inspect").Exit(1).Stderr("Error: No such container: ownci-r1")
	if _, err := Inspect(ctx, f, "r1"); !errors.Is(err, ErrNoContainer) {
		t.Errorf("missing: %v", err)
	}

	f = execx.NewFake()
	f.On("docker", "inspect").Exit(1).Stderr("permission denied")
	if _, err := Inspect(ctx, f, "r1"); err == nil || errors.Is(err, ErrNoContainer) {
		t.Errorf("other failure: %v", err)
	}
}

func TestProbe(t *testing.T) {
	ctx := context.Background()
	f := execx.NewFake()
	f.On("docker", "run").Stdout("ok\n")
	if warnings, err := Probe(ctx, f, "img"); err != nil || len(warnings) != 0 {
		t.Fatalf("healthy: %v %v", warnings, err)
	}
	args := strings.Join(f.Calls()[0].Args, " ")
	for _, want := range []string{"run --rm --pull never --network none", "--entrypoint bash img -c", "ownci-probe"} {
		if !strings.Contains(args, want) {
			t.Errorf("probe args lack %q: %s", want, args)
		}
	}

	// Recommended tools only warn.
	f = execx.NewFake()
	f.On("docker", "run").Stdout("optional: git sudo\nok\n")
	warnings, err := Probe(ctx, f, "img")
	if err != nil || len(warnings) != 2 || !strings.Contains(warnings[0], "actions/checkout") || !strings.Contains(warnings[1], "sudo apt-get") {
		t.Errorf("optional tools: %q %v", warnings, err)
	}

	// Required tools fail, and the warnings still come back.
	f = execx.NewFake()
	f.On("docker", "run").Exit(3).Stdout("optional: sudo\nmissing: docker libicu\n")
	warnings, err = Probe(ctx, f, "img")
	var e *Error
	if !errors.As(err, &e) || !strings.Contains(e.Msg, "lacks: docker libicu") || !strings.Contains(e.Hint, "docker CLI") || len(warnings) != 1 {
		t.Errorf("missing tools: %q %v", warnings, err)
	}

	f = execx.NewFake()
	f.On("docker", "run").Exit(127).Stderr(`exec: "bash": executable file not found`)
	if _, err := Probe(ctx, f, "img"); !errors.As(err, &e) || !strings.Contains(e.Msg, "could not be probed (exit 127)") {
		t.Errorf("no bash: %v", err)
	}
}

// TestProbeScript runs the real probe script with a PATH that hides tools, so
// the script and Probe's parsing are tested together.
func TestProbeScript(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not on PATH")
	}
	bin := t.TempDir()
	for _, tool := range []string{"setpriv", "useradd", "groupadd", "usermod", "getent", "stat", "git"} {
		if err := os.WriteFile(filepath.Join(bin, tool), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// ldconfig reports ICU; docker and sudo are absent.
	if err := os.WriteFile(filepath.Join(bin, "ldconfig"), []byte("#!/bin/sh\necho 'libicuuc.so.70 => /x'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	bashPath, _ := exec.LookPath("bash")
	grepPath, _ := exec.LookPath("grep")
	for _, tool := range map[string]string{"bash": bashPath, "grep": grepPath} {
		if err := os.Symlink(tool, filepath.Join(bin, filepath.Base(tool))); err != nil && !os.IsExist(err) {
			t.Fatal(err)
		}
	}
	cmd := exec.Command(bashPath, "-c", probeScript, "ownci-probe")
	cmd.Env = []string{"PATH=" + bin}
	out, _ := cmd.Output()
	code := cmd.ProcessState.ExitCode()
	if code != 3 || !strings.Contains(string(out), "missing: docker") || !strings.Contains(string(out), "optional: sudo") {
		t.Errorf("exit %d, output:\n%s", code, out)
	}
}
