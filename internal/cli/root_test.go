package cli

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

// runCLI runs the root command in-process and returns captured output and the
// exit code, rendering errors exactly like Execute.
func runCLI(t *testing.T, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	var out, errb bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&errb)
	cmd.SetArgs(args)
	code = renderError(&errb, cmd.Execute())
	return out.String(), errb.String(), code
}

func TestVersion(t *testing.T) {
	stdout, _, code := runCLI(t, "version")
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if !regexp.MustCompile(`^ownci \S+ \(commit \S+, built \S+\)`).MatchString(stdout) {
		t.Fatalf("unexpected output: %q", stdout)
	}
	if strings.Contains(stdout, "build: dev") {
		t.Fatalf("release build must not report dev: %q", stdout)
	}
}

func TestVersionJSON(t *testing.T) {
	stdout, _, code := runCLI(t, "version", "--json")
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(stdout), &m); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, stdout)
	}
	for _, k := range []string{"version", "commit", "date", "go", "os", "arch", "devBuild"} {
		if _, ok := m[k]; !ok {
			t.Errorf("missing key %q", k)
		}
	}
}

func TestStubsNotImplemented(t *testing.T) {
	for _, name := range []string{"doctor", "setup", "start", "stop", "status", "logs", "uninstall"} {
		t.Run(name, func(t *testing.T) {
			_, stderr, code := runCLI(t, name)
			if code != 2 {
				t.Fatalf("exit = %d, want 2", code)
			}
			if !strings.Contains(stderr, "not implemented") {
				t.Fatalf("stderr = %q", stderr)
			}
			if !strings.Contains(stderr, "Hint:") {
				t.Fatalf("missing hint: %q", stderr)
			}
		})
	}
}

func TestUsageErrors(t *testing.T) {
	for _, args := range [][]string{{"frobnicate"}, {"version", "--bogus"}} {
		_, stderr, code := runCLI(t, args...)
		if code != 2 {
			t.Errorf("%v: exit = %d, want 2 (stderr %q)", args, code, stderr)
		}
	}
}

func TestHelpListsCommands(t *testing.T) {
	stdout, _, code := runCLI(t, "--help")
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	for _, name := range []string{"doctor", "logs", "setup", "start", "status", "stop", "uninstall", "version"} {
		if !regexp.MustCompile(`(?m)^\s+` + name + `\s`).MatchString(stdout) {
			t.Errorf("help does not list %q:\n%s", name, stdout)
		}
	}
}
