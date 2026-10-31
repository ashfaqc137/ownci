package logging

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	ghp = "ghp_abcdefghijklmnopqrstuvwxyz0123456789"
	gho = "gho_abcdefghijklmnopqrstuvwxyz0123456789"
	ghs = "ghs_abcdefghijklmnopqrstuvwxyz0123456789"
	pat = "github_pat_11ABCDEFG0abcdefghij_klmnopqrstuvwxyz0123456789"
)

func TestRedact(t *testing.T) {
	RegisterSecret("registered-secret-value")
	RegisterSecret("short") // ignored: < 8 chars
	tests := []struct{ name, in, want string }{
		{"ghp", "token " + ghp + " end", "token *** end"},
		{"gho", gho, "***"},
		{"ghs", "x=" + ghs, "x=***"},
		{"github_pat", pat, "***"},
		{"registered", "got registered-secret-value here", "got *** here"},
		{"short registered ignored", "a short value", "a short value"},
		{"token flag space", "config.sh --token AABBCCDDEEFF --name x", "config.sh --token *** --name x"},
		{"token flag equals", "config.sh --token=AABBCCDDEEFF --name x", "config.sh --token=*** --name x"},
		{"unrelated", "ownci start args=[status] datadir=/tmp/x", "ownci start args=[status] datadir=/tmp/x"},
		{"too-short gh prefix", "ghp_short", "ghp_short"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Redact(tc.in); got != tc.want {
				t.Errorf("Redact(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestLoggerNeverLeaks(t *testing.T) {
	RegisterSecret("another-registered-secret")
	var file, errb bytes.Buffer
	log := newLogger(&file, &errb)
	log.Info("msg with "+ghp, "attr", gho, "grp", map[string]string{}, "e", errString("boom "+ghs))
	log.With("pre", pat).WithGroup("g").Debug("hi", "k", "another-registered-secret", "cmd", "x --token=ZZZZZZZZ")
	for name, out := range map[string]string{"file": file.String(), "stderr": errb.String()} {
		for _, secret := range []string{ghp, gho, ghs, pat, "another-registered-secret", "ZZZZZZZZ"} {
			if strings.Contains(out, secret) {
				t.Errorf("%s leaked %q:\n%s", name, secret, out)
			}
		}
		if !strings.Contains(out, "***") {
			t.Errorf("%s has no mask:\n%s", name, out)
		}
	}
}

type errString string

func (e errString) Error() string { return string(e) }

func TestSetupFileAndStderr(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ownci.log")
	var stderr bytes.Buffer

	log, closer, err := Setup(Options{LogFile: path, Stderr: &stderr})
	if err != nil {
		t.Fatal(err)
	}
	log.Debug("quiet " + ghp)
	closer.Close()
	if stderr.Len() != 0 {
		t.Errorf("non-verbose wrote to stderr: %q", stderr.String())
	}

	log, closer, err = Setup(Options{LogFile: path, Verbose: true, Stderr: &stderr})
	if err != nil {
		t.Fatal(err)
	}
	log.Debug("loud")
	closer.Close()
	if !strings.Contains(stderr.String(), "loud") {
		t.Errorf("verbose stderr = %q", stderr.String())
	}

	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "quiet") || !strings.Contains(string(b), "loud") {
		t.Errorf("log file should append both runs: %q", b)
	}
	if strings.Contains(string(b), ghp) {
		t.Error("token in log file")
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %04o", fi.Mode().Perm())
	}
}
