package runner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ashfaqc137/ownci/internal/config"
	"github.com/ashfaqc137/ownci/internal/execx"
	"github.com/ashfaqc137/ownci/internal/testutil"
)

func setup(t *testing.T, version string) (InstallOptions, *execx.Fake) {
	t.Helper()
	data, sha := testutil.FakeRunnerTarball(testutil.FakeRunnerOptions{Version: version})
	srv, _ := server(t, data)
	paths := config.NewPaths(filepath.Join(t.TempDir(), "data"))
	if err := paths.Ensure(); err != nil {
		t.Fatal(err)
	}
	ex := execx.NewFake()
	ex.On(filepath.Join(paths.RunnerDir("r1"), "bin", "Runner.Listener"), "--version").Stdout(version + "\n")
	return InstallOptions{
		Paths: paths, Name: "r1", Exec: ex,
		Asset: Asset{URL: srv.URL + "/actions-runner-test.tar.gz", SHA256: sha, Version: version},
	}, ex
}

func TestInstallFresh(t *testing.T) {
	o, _ := setup(t, "2.1.0")
	res, err := Install(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if res.InstalledVersion != "2.1.0" || res.ExpectedVersion != "2.1.0" || res.VersionErr != nil {
		t.Errorf("res = %+v", res)
	}
	if res.Dir != o.Paths.RunnerDir("r1") {
		t.Errorf("dir = %s", res.Dir)
	}
	for _, f := range []string{"config.sh", "run.sh", "bin/Runner.Listener", "bin/installdependencies.sh"} {
		fi, err := os.Stat(filepath.Join(res.Dir, f))
		if err != nil || fi.Mode().Perm() != 0o755 {
			t.Errorf("%s: %v %v", f, fi, err)
		}
	}
	if _, err := os.Stat(res.ArchivePath); err != nil {
		t.Errorf("archive not cached: %v", err)
	}
}

func TestInstallRefusesRegisteredEvenWithForce(t *testing.T) {
	o, _ := setup(t, "2.1.0")
	dir := o.Paths.RunnerDir("r1")
	os.MkdirAll(dir, 0o700)
	os.WriteFile(filepath.Join(dir, ".runner"), []byte("{}"), 0o600)
	for _, force := range []bool{false, true} {
		o.Force = force
		_, err := Install(context.Background(), o)
		var re *Error
		if !errors.As(err, &re) || !strings.Contains(re.Msg, "registered") || !strings.Contains(re.Hint, "uninstall --unregister") {
			t.Fatalf("force=%v: err = %v", force, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, ".runner")); err != nil {
		t.Error(".runner must survive")
	}
}

func TestInstallExistingNeedsForce(t *testing.T) {
	o, _ := setup(t, "2.1.0")
	dir := o.Paths.RunnerDir("r1")
	os.MkdirAll(dir, 0o700)
	os.WriteFile(filepath.Join(dir, "old"), []byte("x"), 0o600)

	_, err := Install(context.Background(), o)
	var re *Error
	if !errors.As(err, &re) || !strings.Contains(re.Hint, "--force") {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "old")); err != nil {
		t.Error("existing dir must be untouched without --force")
	}

	o.Force = true
	if _, err := Install(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "old")); !os.IsNotExist(err) {
		t.Error("old content should be replaced")
	}
	if _, err := os.Stat(filepath.Join(dir, "config.sh")); err != nil {
		t.Error(err)
	}
}

func TestInstallFailedDownloadKeepsExisting(t *testing.T) {
	o, _ := setup(t, "2.1.0")
	dir := o.Paths.RunnerDir("r1")
	os.MkdirAll(dir, 0o700)
	os.WriteFile(filepath.Join(dir, "old"), []byte("x"), 0o600)
	o.Force = true
	o.Asset.SHA256 = strings.Repeat("0", 64)
	if _, err := Install(context.Background(), o); !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "old")); err != nil {
		t.Error("existing install must survive a failed download")
	}
}

func TestInstallVersionMismatchReported(t *testing.T) {
	o, ex := setup(t, "2.1.0")
	o.Asset.Version = "9.9.9" // pinned says 9.9.9, the tarball says 2.1.0
	res, err := Install(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if res.ExpectedVersion != "9.9.9" || res.InstalledVersion != "2.1.0" {
		t.Errorf("res = %+v", res)
	}
	if len(ex.Calls()) != 1 {
		t.Errorf("calls = %v", ex.Calls())
	}
}

func TestInstallVersionProbeFailureIsNotFatal(t *testing.T) {
	o, _ := setup(t, "2.1.0")
	ex := execx.NewFake()
	ex.On(filepath.Join(o.Paths.RunnerDir("r1"), "bin", "Runner.Listener"), "--version").
		Exit(134).Stderr("Couldn't find a valid ICU package")
	o.Exec = ex
	res, err := Install(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	var re *Error
	if !errors.As(res.VersionErr, &re) || !strings.Contains(re.Hint, "installdependencies.sh") || !strings.Contains(re.Msg, "ICU") {
		t.Fatalf("VersionErr = %v", res.VersionErr)
	}
	if res.InstalledVersion != "" {
		t.Errorf("InstalledVersion = %q", res.InstalledVersion)
	}
	if _, err := os.Stat(filepath.Join(res.Dir, "run.sh")); err != nil {
		t.Error("install must still succeed")
	}
}

func TestInstallRejectsBadName(t *testing.T) {
	o, _ := setup(t, "2.1.0")
	for _, name := range []string{"", "../x", "A", "a/b", "-x", strings.Repeat("a", 65)} {
		o.Name = name
		if _, err := Install(context.Background(), o); err == nil {
			t.Errorf("name %q accepted", name)
		}
	}
}

func TestDefaultName(t *testing.T) {
	for in, want := range map[string]string{
		"MyHost.local":            "ownci-myhost",
		"dev_box 01":              "ownci-dev-box-01",
		"":                        "ownci-local",
		"---":                     "ownci-local",
		"host-":                   "ownci-host",
		strings.Repeat("x", 100):  "ownci-" + strings.Repeat("x", 58),
		"ünï":                     "ownci-n",
		"laptop.corp.example.com": "ownci-laptop",
	} {
		got := DefaultName(in)
		if got != want {
			t.Errorf("DefaultName(%q) = %q, want %q", in, got, want)
		}
		if err := ValidateName(got); err != nil {
			t.Errorf("DefaultName(%q) = %q is invalid: %v", in, got, err)
		}
	}
}
