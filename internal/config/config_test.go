package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestResolveDataDir(t *testing.T) {
	tests := []struct {
		name    string
		flag    string
		env     map[string]string
		home    string
		want    string
		wantErr bool
	}{
		{"flag wins", "/f", map[string]string{"OWNCI_DATA_DIR": "/e", "XDG_DATA_HOME": "/x"}, "/home/u", "/f", false},
		{"env over xdg", "", map[string]string{"OWNCI_DATA_DIR": "/e", "XDG_DATA_HOME": "/x"}, "/home/u", "/e", false},
		{"xdg", "", map[string]string{"XDG_DATA_HOME": "/x"}, "/home/u", "/x/ownci", false},
		{"home default", "", nil, "/home/u", "/home/u/.local/share/ownci", false},
		{"cleaned", "/a/b/../c/", nil, "/home/u", "/a/c", false},
		{"no home", "", nil, "", "", true},
		{"root refused", "/", nil, "/home/u", "", true},
		{"root via dots refused", "/a/..", nil, "/home/u", "", true},
		{"home refused", "/home/u", nil, "/home/u", "", true},
		{"home with slash refused", "/home/u/", nil, "/home/u", "", true},
		{"root via env refused", "", map[string]string{"OWNCI_DATA_DIR": "/"}, "/home/u", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveDataDir(tc.flag, env(tc.env), tc.home)
			if (err != nil) != tc.wantErr || got != tc.want {
				t.Fatalf("got %q, %v; want %q (err=%v)", got, err, tc.want, tc.wantErr)
			}
		})
	}
}

func TestResolveDataDirRelativeBecomesAbsolute(t *testing.T) {
	got, err := ResolveDataDir("rel/dir", env(nil), "/home/u")
	if err != nil || !filepath.IsAbs(got) || !strings.HasSuffix(got, "/rel/dir") {
		t.Fatalf("got %q, %v", got, err)
	}
}

func mode(t *testing.T, path string) os.FileMode {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Mode().Perm()
}

func TestEnsureCreatesLayout(t *testing.T) {
	p := NewPaths(filepath.Join(t.TempDir(), "data"))
	if err := p.Ensure(); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{p.Root, p.ConfigDir, p.LogsDir, p.CacheDir, p.RunDir, p.RunnersDir} {
		if m := mode(t, d); m != 0o700 {
			t.Errorf("%s mode = %04o", d, m)
		}
	}
	b, err := os.ReadFile(p.Marker)
	if err != nil || !strings.Contains(string(b), "managed by ownci") {
		t.Fatalf("marker: %q, %v", b, err)
	}
	if m := mode(t, p.Marker); m != 0o600 {
		t.Errorf("marker mode = %04o", m)
	}
	if p.Foreign() {
		t.Error("fresh dir must not be foreign")
	}
	if err := p.Ensure(); err != nil { // idempotent
		t.Fatal(err)
	}
}

func TestEnsureTightensLoosePermissions(t *testing.T) {
	root := filepath.Join(t.TempDir(), "data")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o755); err != nil {
		t.Fatal(err)
	}
	var notes []string
	p := NewPaths(root)
	if err := p.EnsureNotify(func(m string) { notes = append(notes, m) }); err != nil {
		t.Fatal(err)
	}
	if m := mode(t, root); m != 0o700 {
		t.Errorf("mode = %04o", m)
	}
	if len(notes) == 0 || !strings.Contains(notes[0], "tightening") {
		t.Errorf("notes = %v", notes)
	}
	if _, err := os.Stat(p.Marker); err != nil {
		t.Errorf("empty existing dir should get a marker: %v", err)
	}
}

func TestEnsureForeignDirGetsNoMarker(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "precious.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	var notes []string
	p := NewPaths(root)
	if err := p.EnsureNotify(func(m string) { notes = append(notes, m) }); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p.Marker); !os.IsNotExist(err) {
		t.Errorf("marker must not be written: %v", err)
	}
	if !p.Foreign() {
		t.Error("Foreign() = false")
	}
	if !strings.Contains(strings.Join(notes, "|"), "no "+MarkerName) {
		t.Errorf("notes = %v", notes)
	}
	// stays foreign on later runs
	if err := p.Ensure(); err != nil || !p.Foreign() {
		t.Errorf("second Ensure: err=%v foreign=%v", err, p.Foreign())
	}
}

func TestEnsureRootIsFile(t *testing.T) {
	f := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(f, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := NewPaths(f).Ensure(); err == nil {
		t.Fatal("want error")
	}
}

func TestPathHelpers(t *testing.T) {
	p := NewPaths("/d")
	for got, want := range map[string]string{
		p.RunnerDir("r"): "/d/runners/r",
		p.RunnerLog("r"): "/d/logs/runner-r.log",
		p.LockFile("r"):  "/d/run/r.lock",
		p.PidFile("r"):   "/d/run/r.pid",
		p.StateFile("r"): "/d/run/r.state.json",
		p.ConfigFile:     "/d/config/config.json",
		p.OwnciLog:       "/d/logs/ownci.log",
	} {
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
}

func ensured(t *testing.T) Paths {
	t.Helper()
	p := NewPaths(filepath.Join(t.TempDir(), "data"))
	if err := p.Ensure(); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSaveLoadRoundTrip(t *testing.T) {
	p := ensured(t)
	in := &Config{SchemaVersion: 99, Runner: &RunnerConfig{
		Name: "ownci-h", Provider: "github", Repo: "o/r", URL: "https://github.com/o/r",
		Labels: []string{"ownci"}, InstalledVersion: "2.1.0", Isolation: "docker-job-container",
		CreatedAt: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC),
	}}
	if err := Save(p, in); err != nil {
		t.Fatal(err)
	}
	if m := mode(t, p.ConfigFile); m != 0o600 {
		t.Errorf("mode = %04o", m)
	}
	got, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if got.SchemaVersion != 1 {
		t.Errorf("schemaVersion = %d", got.SchemaVersion)
	}
	if got.Runner.Name != "ownci-h" || !got.Runner.CreatedAt.Equal(in.Runner.CreatedAt) || got.Runner.Labels[0] != "ownci" {
		t.Errorf("round trip: %+v", got.Runner)
	}
	entries, _ := os.ReadDir(p.ConfigDir)
	if len(entries) != 1 {
		t.Errorf("leftover temp files: %v", entries)
	}
}

func TestLoadNotConfigured(t *testing.T) {
	p := ensured(t)
	if _, err := Load(p); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("missing file: %v", err)
	}
	if err := Save(p, &Config{}); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(p); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("nil runner: %v", err)
	}
}

func TestLoadErrors(t *testing.T) {
	for name, content := range map[string]string{
		"bad json":       "{nope",
		"unknown schema": `{"schemaVersion": 2, "runner": {"name": "x"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			p := ensured(t)
			if err := os.WriteFile(p.ConfigFile, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := Load(p)
			if err == nil || errors.Is(err, ErrNotConfigured) {
				t.Fatalf("err = %v", err)
			}
			if !strings.Contains(err.Error(), p.ConfigFile) || !strings.Contains(err.Error(), "setup --force") {
				t.Errorf("err should name path and hint: %v", err)
			}
		})
	}
}
