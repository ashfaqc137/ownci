package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var managed = map[string]string{
	"ACTIONS_RUNNER_REQUIRE_JOB_CONTAINER": "true",
	"OWNCI_RUNNER_ROOT":                    "/r",
}

const block = EnvMarker + "\nACTIONS_RUNNER_REQUIRE_JOB_CONTAINER=true\nOWNCI_RUNNER_ROOT=/r\n"

func TestEnsureEnv(t *testing.T) {
	tests := []struct {
		name    string
		initial *string // nil = no file
		want    string
	}{
		{"no file", nil, block},
		{"empty file", ptr(""), block},
		{"config.sh style content is preserved", ptr("LANG=C.UTF-8\nPATH=/usr/bin:/bin\n"),
			"LANG=C.UTF-8\nPATH=/usr/bin:/bin\n" + block},
		{"wrong value replaced in place", ptr("A=1\nACTIONS_RUNNER_REQUIRE_JOB_CONTAINER=false\nB=2\n"),
			"A=1\nACTIONS_RUNNER_REQUIRE_JOB_CONTAINER=true\nB=2\n" + EnvMarker + "\nOWNCI_RUNNER_ROOT=/r\n"},
		{"duplicates removed", ptr("OWNCI_RUNNER_ROOT=/x\nA=1\nOWNCI_RUNNER_ROOT=/y\nACTIONS_RUNNER_REQUIRE_JOB_CONTAINER=true\nOWNCI_RUNNER_ROOT=/z\n"),
			"OWNCI_RUNNER_ROOT=/r\nA=1\nACTIONS_RUNNER_REQUIRE_JOB_CONTAINER=true\n"},
		{"CRLF input keeps CRLF", ptr("A=1\r\nB=2\r\n"),
			"A=1\r\nB=2\r\n" + strings.ReplaceAll(block, "\n", "\r\n")},
		{"no trailing newline", ptr("A=1"), "A=1\n" + block},
		{"unknown lines and comments kept", ptr("# a comment\n\nweird line\nA=b=c\n"),
			"# a comment\n\nweird line\nA=b=c\n" + block},
		{"an existing marker is reused", ptr("A=1\n" + EnvMarker + "\nOWNCI_RUNNER_ROOT=/old\n"),
			"A=1\n" + EnvMarker + "\nOWNCI_RUNNER_ROOT=/r\nACTIONS_RUNNER_REQUIRE_JOB_CONTAINER=true\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.initial != nil {
				if err := os.WriteFile(EnvPath(dir), []byte(*tc.initial), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			changed, err := EnsureEnv(dir, managed)
			if err != nil {
				t.Fatal(err)
			}
			got, _ := os.ReadFile(EnvPath(dir))
			if string(got) != tc.want {
				t.Errorf("content:\n%q\nwant:\n%q", got, tc.want)
			}
			if !changed {
				t.Error("changed = false, want true")
			}
			if fi, _ := os.Stat(EnvPath(dir)); fi.Mode().Perm() != 0o600 {
				t.Errorf("mode = %v, want 0600", fi.Mode().Perm())
			}
			// Idempotent.
			if changed, err := EnsureEnv(dir, managed); err != nil || changed {
				t.Errorf("second call: changed=%v err=%v", changed, err)
			}
			again, _ := os.ReadFile(EnvPath(dir))
			if string(again) != string(got) {
				t.Errorf("second call rewrote the file:\n%q", again)
			}
		})
	}
}

func TestEnsureEnvAlreadyCorrectIsUntouched(t *testing.T) {
	dir := t.TempDir()
	content := "LANG=C\n" + block
	if err := os.WriteFile(EnvPath(dir), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	changed, err := EnsureEnv(dir, managed)
	if err != nil || changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	if fi, _ := os.Stat(EnvPath(dir)); fi.Mode().Perm() != 0o644 {
		t.Errorf("an unchanged file must not be rewritten (mode %v)", fi.Mode().Perm())
	}
}

func TestEnvMarkerHasNoEquals(t *testing.T) {
	if strings.Contains(EnvMarker, "=") {
		t.Fatalf("marker %q contains '=': the Listener would read it as a variable", EnvMarker)
	}
}

func TestEnsureEnvRejectsBadEntries(t *testing.T) {
	for _, m := range []map[string]string{{"": "x"}, {"A=B": "x"}, {"A": "x\ny"}, {"A\n": "x"}} {
		if _, err := EnsureEnv(t.TempDir(), m); err == nil {
			t.Errorf("accepted %q", m)
		}
	}
}

func TestEnsureEnvUnwritableDir(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o700)
	if _, err := EnsureEnv(dir, managed); err == nil {
		t.Fatal("expected an error for a read-only directory")
	}
}

func TestReadEnv(t *testing.T) {
	dir := t.TempDir()
	if env, err := ReadEnv(dir); err != nil || len(env) != 0 {
		t.Fatalf("missing file: %v %v", env, err)
	}
	content := "A=1\r\nB=x=y\nA=2\n# comment\nnoequals\n=novalue\nC=\n"
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	env, err := ReadEnv(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"A": "2", "B": "x=y", "C": ""}
	if len(env) != len(want) {
		t.Fatalf("env = %v", env)
	}
	for k, v := range want {
		if got, ok := env[k]; !ok || got != v {
			t.Errorf("env[%q] = %q (present %v), want %q", k, got, ok, v)
		}
	}
}

func ptr(s string) *string { return &s }

func TestEnsureEnvRemovesAbsentKeys(t *testing.T) {
	dir := t.TempDir()
	orig := "LANG=C.UTF-8\nGONE=1\n" + EnvMarker + "\nKEEP=old\nGONE=2\n"
	if err := os.WriteFile(EnvPath(dir), []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}
	changed, err := EnsureEnv(dir, map[string]string{"KEEP": "new"}, "GONE")
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	got, _ := os.ReadFile(EnvPath(dir))
	if want := "LANG=C.UTF-8\n" + EnvMarker + "\nKEEP=new\n"; string(got) != want {
		t.Errorf(".env =\n%s\nwant\n%s", got, want)
	}
	if changed, err := EnsureEnv(dir, map[string]string{"KEEP": "new"}, "GONE"); err != nil || changed {
		t.Errorf("second run: changed=%v err=%v", changed, err)
	}
	for _, bad := range []string{"", "A=B", "KEEP"} {
		if _, err := EnsureEnv(dir, map[string]string{"KEEP": "new"}, bad); err == nil {
			t.Errorf("absent key %q accepted", bad)
		}
	}
}

func TestSetAsideRegistration(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{".runner", ".credentials"} { // no .credentials_rsaparams: missing files are fine
		if err := os.WriteFile(filepath.Join(dir, n), []byte(n), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	restore, discard, err := SetAsideRegistration(dir)
	if err != nil {
		t.Fatal(err)
	}
	if Registered(dir) {
		t.Fatal(".runner still in place")
	}
	if err := restore(); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, ".credentials")); string(b) != ".credentials" || !Registered(dir) {
		t.Fatal("not restored")
	}

	_, discard, err = SetAsideRegistration(dir)
	if err != nil {
		t.Fatal(err)
	}
	discard()
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("left behind: %v", entries)
	}
}
