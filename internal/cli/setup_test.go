package cli

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ashfaqc137/ownci/internal/execx"
	"github.com/ashfaqc137/ownci/internal/runner"
	"github.com/ashfaqc137/ownci/internal/testutil"
)

// useFakeRunnerServer serves the fake runner tarball and points setup at it.
// It returns the data dir to pass as --data-dir.
func useFakeRunnerServer(t *testing.T, sha string, listener *execx.Fake) string {
	t.Helper()
	data, realSHA := testutil.FakeRunnerTarball(testutil.FakeRunnerOptions{Version: "2.1.0"})
	if sha == "" {
		sha = realSHA
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write(data) }))
	t.Cleanup(srv.Close)

	oldAsset, oldRunner := assetFor, newExecRunner
	assetFor = func(arch string) (runner.Asset, error) {
		return runner.Asset{Arch: "linux-x64", URL: srv.URL + "/actions-runner-linux-x64-2.1.0.tar.gz", SHA256: sha, Version: "2.1.0"}, nil
	}
	newExecRunner = func(*slog.Logger) execx.Runner { return listener }
	t.Cleanup(func() { assetFor, newExecRunner = oldAsset, oldRunner })
	return filepath.Join(t.TempDir(), "data")
}

func listenerFake(dataDir, name, stdout string, exit int, stderr string) *execx.Fake {
	f := execx.NewFake()
	f.On(filepath.Join(dataDir, "runners", name, "bin", "Runner.Listener"), "--version").Stdout(stdout).Exit(exit).Stderr(stderr)
	return f
}

func TestSetupDownloadOnly(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	f := listenerFake(dir, "t04", "2.1.0\n", 0, "")
	useFakeRunnerServer(t, "", f)

	stdout, stderr, code := runCLI(t, "--data-dir", dir, "setup", "--download-only", "--name", "t04")
	if code != 0 {
		t.Fatalf("exit %d\n%s\n%s", code, stdout, stderr)
	}
	runnerDir := filepath.Join(dir, "runners", "t04")
	want := "Runner 2.1.0 (linux-x64) verified and installed at " + runnerDir
	if !strings.Contains(stdout, want) {
		t.Errorf("stdout = %q, want containing %q", stdout, want)
	}
	if _, err := os.Stat(filepath.Join(runnerDir, "run.sh")); err != nil {
		t.Error(err)
	}
	if stderr != "" {
		t.Errorf("stderr = %q", stderr)
	}

	// Second run: refuses, hint mentions --force.
	_, stderr, code = runCLI(t, "--data-dir", dir, "setup", "--download-only", "--name", "t04")
	if code != 1 || !strings.Contains(stderr, "already exists") || !strings.Contains(stderr, "--force") {
		t.Errorf("exit %d, stderr %q", code, stderr)
	}
	if _, _, code = runCLI(t, "--data-dir", dir, "setup", "--download-only", "--name", "t04", "--force"); code != 0 {
		t.Errorf("--force exit = %d", code)
	}
}

func TestSetupDownloadOnlyChecksumMismatch(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	useFakeRunnerServer(t, strings.Repeat("0", 64), execx.NewFake())
	_, stderr, code := runCLI(t, "--data-dir", dir, "setup", "--download-only", "--name", "t04")
	if code != 1 || !strings.Contains(stderr, "checksum mismatch") || !strings.Contains(stderr, "Hint:") {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	if _, err := os.Stat(filepath.Join(dir, "runners", "t04")); !os.IsNotExist(err) {
		t.Error("nothing may be extracted on a mismatch")
	}
	if entries, _ := os.ReadDir(filepath.Join(dir, "cache")); len(entries) != 0 {
		t.Errorf("cache not clean: %v", entries)
	}
}

func TestSetupDownloadOnlyVersionProbeFails(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	f := listenerFake(dir, "t04", "", 134, "Couldn't find a valid ICU package")
	useFakeRunnerServer(t, "", f)
	stdout, stderr, code := runCLI(t, "--data-dir", dir, "setup", "--download-only", "--name", "t04")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if strings.Contains(stdout, "verified and installed") || !strings.Contains(stderr, "Warning:") || !strings.Contains(stderr, "installdependencies.sh") {
		t.Errorf("stdout %q stderr %q", stdout, stderr)
	}
}

func TestSetupDownloadOnlyBadName(t *testing.T) {
	useFakeRunnerServer(t, "", execx.NewFake())
	_, stderr, code := runCLI(t, "--data-dir", filepath.Join(t.TempDir(), "d"), "setup", "--download-only", "--name", "../evil")
	if code != 1 || !strings.Contains(stderr, "invalid runner name") || !strings.Contains(stderr, "Hint:") {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
}

func TestSetupUsageErrors(t *testing.T) {
	cases := map[string][]string{
		"missing repo":    {"setup"},
		"forbidden label": {"setup", "--repo", "me/x", "--labels", "self-hosted"},
		"invalid label":   {"setup", "--repo", "me/x", "--labels", "a b"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			_, stderr, code := runCLI(t, args...)
			if code != 2 || !strings.Contains(stderr, "Error:") || !strings.Contains(stderr, "Hint:") {
				t.Fatalf("exit %d, stderr %q", code, stderr)
			}
		})
	}
}

func TestSetupInvalidRepo(t *testing.T) {
	_, stderr, code := runCLI(t, "setup", "--repo", "not a repo")
	if code != 1 || !strings.Contains(stderr, "invalid repository") || !strings.Contains(stderr, "owner/name") {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
}

func TestSetupHelpShowsFlags(t *testing.T) {
	stdout, _, code := runCLI(t, "setup", "--help")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	for _, f := range []string{"--repo", "--labels", "--allow-public-repo", "--dry-run", "--force", "--download-only"} {
		if !strings.Contains(stdout, f) {
			t.Errorf("help lacks %s", f)
		}
	}
	if strings.Contains(stdout, "skip-docker-check") {
		t.Error("--skip-docker-check must stay hidden")
	}
}

func TestDevWarning(t *testing.T) {
	env := []string{"PATH=/bin", "OWNCI_DEV_RUNNER_URL=http://x", "OWNCI_DEV_GITHUB_API_URL=http://y", "OWNCI_DEV_EMPTY="}
	if got := devWarning(true, env); got != "DEV BUILD: overrides active (OWNCI_DEV_GITHUB_API_URL, OWNCI_DEV_RUNNER_URL)" {
		t.Errorf("got %q", got)
	}
	if got := devWarning(false, env); got != "" {
		t.Errorf("release build warned: %q", got)
	}
	if got := devWarning(true, []string{"PATH=/bin", "OWNCI_DEV_EMPTY="}); got != "" {
		t.Errorf("no overrides but warned: %q", got)
	}
}
