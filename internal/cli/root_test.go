package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// runCLI runs the root command in-process and returns captured output and the
// exit code, rendering errors exactly like Execute.
func runCLI(t *testing.T, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	// Never touch the developer's real data dir: commands without --data-dir
	// fall back to this temporary one.
	t.Setenv("OWNCI_DATA_DIR", t.TempDir())
	var out, errb bytes.Buffer
	cmd, f := newRoot()
	t.Cleanup(f.close)
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

func TestVerboseDataDirCreatesLayout(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	_, stderr, code := runCLI(t, "--data-dir", dir, "--verbose", "version")
	if code != 0 {
		t.Fatalf("exit = %d, stderr %q", code, stderr)
	}
	for _, p := range []string{".ownci-data-dir", "config", "logs/ownci.log", "cache", "run", "runners"} {
		if _, err := os.Stat(filepath.Join(dir, p)); err != nil {
			t.Errorf("missing %s: %v", p, err)
		}
	}
	log, _ := os.ReadFile(filepath.Join(dir, "logs", "ownci.log"))
	if !strings.Contains(string(log), "ownci start") || !strings.Contains(string(log), "datadir="+dir) {
		t.Errorf("log = %q", log)
	}
	if !strings.Contains(stderr, "ownci start") {
		t.Errorf("verbose should echo to stderr: %q", stderr)
	}
}

func TestVersionSkipsDataDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	if _, _, code := runCLI(t, "--data-dir", dir, "version"); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("version created the data dir: %v", err)
	}
}

func TestRefusesRootDataDir(t *testing.T) {
	_, stderr, code := runCLI(t, "--data-dir", "/", "status")
	if code == 0 || code == 2 && strings.Contains(stderr, "not implemented") {
		t.Fatalf("exit = %d, stderr %q", code, stderr)
	}
	if !strings.Contains(stderr, "Error:") || !strings.Contains(stderr, "Hint:") {
		t.Errorf("stderr = %q", stderr)
	}
}

func TestLogRedactsTokenArgs(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	const tok = "ghp_abcdefghijklmnopqrstuvwxyz0123456789"
	runCLI(t, "--data-dir", dir, "setup", "--", "--token", tok)
	log, err := os.ReadFile(filepath.Join(dir, "logs", "ownci.log"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(log), tok) {
		t.Errorf("token leaked into log: %s", log)
	}
}
