package cli

import (
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/ashfaqc137/ownci/internal/execx"
)

const healthyDocker = `{"Server":{"Version":"27.0.3","Os":"linux"}}`

// useFake installs a Fake runner and a non-root uid for the test.
func useFake(t *testing.T, f *execx.Fake) {
	t.Helper()
	oldRunner, oldUID := newExecRunner, geteuid
	newExecRunner = func(*slog.Logger) execx.Runner { return f }
	geteuid = func() int { return 1000 }
	t.Cleanup(func() { newExecRunner, geteuid = oldRunner, oldUID })
}

func healthyFake() *execx.Fake {
	f := execx.NewFake()
	f.SetPath("docker", "/usr/bin/docker")
	f.SetPath("git", "/usr/bin/git")
	f.SetPath("ldconfig", "/sbin/ldconfig")
	f.On("docker", "version").Stdout(healthyDocker)
	f.On("/sbin/ldconfig", "-p").Stdout("libicuuc.so.74 => /lib/libicuuc.so.74")
	return f
}

func TestDoctorAllPass(t *testing.T) {
	useFake(t, healthyFake())
	stdout, stderr, code := runCLI(t, "doctor")
	if code != 0 {
		t.Fatalf("exit = %d\n%s\n%s", code, stdout, stderr)
	}
	for _, want := range []string{"[PASS] platform", "[PASS] docker", "27.0.3 (linux)", "0 failed"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("missing %q in:\n%s", want, stdout)
		}
	}
}

func TestDoctorDockerFailure(t *testing.T) {
	f := healthyFake()
	f.On("docker", "version").Stderr("The command 'docker' could not be found in this WSL 2 distro.").Exit(1)
	useFake(t, f)
	stdout, stderr, code := runCLI(t, "doctor")
	if code != 3 {
		t.Fatalf("exit = %d, want 3\n%s", code, stdout)
	}
	if !strings.Contains(stdout, "[FAIL] docker") || !strings.Contains(stdout, "Hint: Enable WSL integration") || !strings.Contains(stdout, "1 failed") {
		t.Errorf("stdout:\n%s", stdout)
	}
	if stderr != "" {
		t.Errorf("exit-3 must not print an Error line: %q", stderr)
	}
}

func TestDoctorWarningsKeepExitZero(t *testing.T) {
	f := execx.NewFake() // healthy, but git is not on PATH
	f.SetPath("docker", "/usr/bin/docker")
	f.SetPath("ldconfig", "/sbin/ldconfig")
	f.On("docker", "version").Stdout(healthyDocker)
	f.On("/sbin/ldconfig", "-p").Stdout("libicuuc")
	useFake(t, f)
	stdout, _, code := runCLI(t, "doctor")
	if code != 0 || !strings.Contains(stdout, "[WARN] git") || !strings.Contains(stdout, "1 warning,") {
		t.Fatalf("exit = %d\n%s", code, stdout)
	}
}

func TestDoctorRootFails(t *testing.T) {
	useFake(t, healthyFake())
	geteuid = func() int { return 0 }
	stdout, _, code := runCLI(t, "doctor")
	if code != 3 || !strings.Contains(stdout, "[FAIL] user") {
		t.Fatalf("exit = %d\n%s", code, stdout)
	}
}

func TestDoctorJSON(t *testing.T) {
	useFake(t, healthyFake())
	stdout, _, code := runCLI(t, "doctor", "--json")
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	var rep struct {
		Results []struct{ Name, Level string }
		Summary map[string]int
	}
	if err := json.Unmarshal([]byte(stdout), &rep); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, stdout)
	}
	if len(rep.Results) != 7 || rep.Summary["failed"] != 0 || rep.Results[0].Level != "pass" {
		t.Errorf("report = %+v", rep)
	}
}
