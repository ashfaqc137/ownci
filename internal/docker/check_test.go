package docker

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ashfaqc137/ownci/internal/execx"
)

const healthy = `{"Client":{"Version":"27.0.3"},"Server":{"Version":"27.0.3","Os":"linux"}}`

func fake(configure func(*execx.Fake)) *execx.Fake {
	f := execx.NewFake()
	f.SetPath("docker", "/usr/bin/docker")
	configure(f)
	return f
}

func hint(t *testing.T, st Status) string {
	t.Helper()
	var e *Error
	if !errors.As(st.Err, &e) {
		t.Fatalf("Err = %v, want *Error", st.Err)
	}
	if e.Hint == "" {
		t.Error("missing hint")
	}
	return e.Hint
}

func TestCheckHealthy(t *testing.T) {
	f := fake(func(f *execx.Fake) { f.On("docker", "version").Stdout(healthy) })
	st := check(context.Background(), f, "")
	if !st.OK() || !st.CLIFound || !st.DaemonReachable || st.ServerVersion != "27.0.3" || st.ServerOS != "linux" || st.CLIPath != "/usr/bin/docker" {
		t.Fatalf("got %+v", st)
	}
	args := f.Calls()[0].Args
	if len(args) != 3 || args[1] != "--format" {
		t.Errorf("args = %v", args)
	}
}

func TestCheckClientWrapper(t *testing.T) {
	f := fake(func(f *execx.Fake) { f.On("docker", "version").Stdout(healthy) })
	if st := (Client{Exec: f}).Check(context.Background()); !st.OK() {
		t.Fatalf("got %+v", st)
	}
}

func TestCheckFailures(t *testing.T) {
	shim := "\nThe command 'docker' could not be found in this WSL 2 distro.\nWe recommend to activate the WSL integration in Docker Desktop settings.\n"
	tests := []struct {
		name     string
		stdout   string
		stderr   string
		exit     int
		wantMsg  string
		wantHint string
		shim     bool
		daemon   bool
	}{
		{"shim", "", shim, 1, "WSL 2 distro", "enable 'Ubuntu'", true, false},
		{"daemon down", `{"Client":{}}`, "Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?", 1, "cannot connect", "Start Docker", false, false},
		{"permission", `{"Client":{}}`, "permission denied while trying to connect to the Docker daemon socket at unix:///var/run/docker.sock", 1, "permission denied", "usermod -aG docker", false, false},
		{"windows containers", `{"Server":{"Version":"27.0.3","Os":"windows"}}`, "", 0, "need linux", "Linux containers", false, true},
		{"no server", `{"Client":{"Version":"27"},"Server":null}`, "", 0, "no server", "Start Docker", false, false},
		{"malformed json", "<html>not json</html>", "", 0, "unexpected output", "docker version", false, false},
		{"other failure", "", "something odd", 1, "exit 1", "docker version", false, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := fake(func(f *execx.Fake) { f.On("docker", "version").Stdout(tc.stdout).Stderr(tc.stderr).Exit(tc.exit) })
			st := check(context.Background(), f, "Ubuntu")
			if st.OK() || !strings.Contains(st.Err.Error(), tc.wantMsg) {
				t.Fatalf("Err = %v, want containing %q", st.Err, tc.wantMsg)
			}
			if h := hint(t, st); !strings.Contains(h, tc.wantHint) {
				t.Errorf("hint = %q, want containing %q", h, tc.wantHint)
			}
			if st.DesktopShim != tc.shim || st.DaemonReachable != tc.daemon {
				t.Errorf("shim=%v daemon=%v", st.DesktopShim, st.DaemonReachable)
			}
			if !st.CLIFound {
				t.Error("CLIFound should be true")
			}
		})
	}
}

func TestCheckShimHintWithoutDistro(t *testing.T) {
	f := fake(func(f *execx.Fake) { f.On("docker", "version").Stderr(shimMarker).Exit(1) })
	if h := hint(t, check(context.Background(), f, "")); !strings.Contains(h, "enable this distro") {
		t.Errorf("hint = %q", h)
	}
}

func TestCheckCLIMissing(t *testing.T) {
	st := check(context.Background(), execx.NewFake(), "")
	if st.OK() || st.CLIFound {
		t.Fatalf("got %+v", st)
	}
	hint(t, st)
}

func TestCheckRunError(t *testing.T) {
	f := fake(func(f *execx.Fake) { f.On("docker", "version").Fail(errors.New("deadline exceeded")) })
	st := check(context.Background(), f, "")
	if st.OK() || !strings.Contains(st.Err.Error(), "deadline exceeded") {
		t.Fatalf("got %+v", st)
	}
	hint(t, st)
}

func TestMalformedOutputExcerptTruncated(t *testing.T) {
	f := fake(func(f *execx.Fake) { f.On("docker", "version").Stdout(strings.Repeat("x", 1000)) })
	st := check(context.Background(), f, "")
	if n := len(st.Err.Error()); n > 400 {
		t.Errorf("message not truncated: %d bytes", n)
	}
}
