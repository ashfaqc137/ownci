package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ashfaqc137/ownci/internal/supervisor"
)

func TestUninstallStopsARunningRunnerAndKeepsFiles(t *testing.T) {
	useFake(t, healthyFake())
	useChildBinary(t)
	p := installRunner(t)
	t.Cleanup(func() { stopRunner(t, p) })

	if _, _, code := runCLI(t, "--data-dir", p.Root, "start", "--detach", "--wait", "30s"); code != 0 {
		t.Fatal("start failed")
	}
	stdout, stderr, code := runCLI(t, "--data-dir", p.Root, "uninstall", "--yes")
	if code != 0 {
		t.Fatalf("exit %d\n%s\n%s", code, stdout, stderr)
	}
	for _, want := range []string{"Runner process   stopped", "still registered (remove at ", "kept at " + p.Root} {
		if !strings.Contains(stdout, want) {
			t.Errorf("output lacks %q:\n%s", want, stdout)
		}
	}
	if running, _, _ := supervisor.IsRunning(p, testRunner); running {
		t.Error("the runner is still running")
	}
	if _, err := os.Stat(p.RunnerDir(testRunner)); err != nil {
		t.Errorf("files were removed: %v", err)
	}
}

func TestUninstallWithoutYesNeedsATerminal(t *testing.T) {
	useFake(t, healthyFake())
	p := installRunner(t)
	_, stderr, code := runCLI(t, "--data-dir", p.Root, "uninstall", "--purge")
	if code != 2 || !strings.Contains(stderr, "--yes") {
		t.Errorf("exit %d, stderr %q", code, stderr)
	}
	if _, err := os.Stat(p.RunnerDir(testRunner)); err != nil {
		t.Error("files were removed")
	}
}

func TestUninstallFlagConflicts(t *testing.T) {
	useFake(t, healthyFake())
	p := installRunner(t)
	_, stderr, code := runCLI(t, "--data-dir", p.Root, "uninstall", "--unregister", "--local-only", "--yes")
	if code != 2 || !strings.Contains(stderr, "contradict") {
		t.Errorf("exit %d, stderr %q", code, stderr)
	}
}

func TestUninstallPurgeLocalOnly(t *testing.T) {
	useFake(t, healthyFake())
	p := installRunner(t)
	parent := filepath.Dir(p.Root)
	if err := os.WriteFile(filepath.Join(parent, "keep-me"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, code := runCLI(t, "--data-dir", p.Root, "uninstall", "--local-only", "--purge", "--yes")
	if code != 0 {
		t.Fatalf("exit %d\n%s\n%s", code, stdout, stderr)
	}
	if _, err := os.Stat(p.Root); err == nil {
		t.Errorf("data dir remains: %v", entriesOf(p.Root))
	}
	if _, err := os.Stat(filepath.Join(parent, "keep-me")); err != nil {
		t.Error("a sibling file was deleted")
	}
	if !strings.Contains(stdout, "skipped (--local-only)") || !strings.Contains(stdout, "is gone") {
		t.Errorf("summary:\n%s", stdout)
	}
}

func entriesOf(dir string) []string {
	es, _ := os.ReadDir(dir)
	var n []string
	for _, e := range es {
		n = append(n, e.Name())
	}
	return n
}

func TestUninstallPurgeRefusesAForeignDirectory(t *testing.T) {
	useFake(t, healthyFake())
	dir := filepath.Join(t.TempDir(), "not-ownci")
	if err := os.MkdirAll(filepath.Join(dir, "runners", "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "important"), []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, stderr, code := runCLI(t, "--data-dir", dir, "uninstall", "--purge", "--yes", "--name", "x")
	if code != 1 || !strings.Contains(stderr, "not an OwnCI data directory") {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	for _, keep := range []string{"important", filepath.Join("runners", "x")} {
		if _, err := os.Stat(filepath.Join(dir, keep)); err != nil {
			t.Errorf("%s was deleted: %v", keep, err)
		}
	}
}

func TestUninstallNothingToDo(t *testing.T) {
	useFake(t, healthyFake())
	dir := filepath.Join(t.TempDir(), "empty")
	stdout, _, code := runCLI(t, "--data-dir", dir, "uninstall", "--yes")
	if code != 0 || !strings.Contains(stdout, "Nothing to uninstall in "+dir) {
		t.Errorf("exit %d: %s", code, stdout)
	}
}

func TestLogs(t *testing.T) {
	useFake(t, healthyFake())
	p := installRunner(t)
	runnerLog := p.RunnerLog(testRunner)
	if err := os.WriteFile(runnerLog, []byte("one\ntwo\nthree\ntoken ghp_abcdefghijklmnopqrstuvwxyz0123456789\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	stdout, _, code := runCLI(t, "--data-dir", p.Root, "logs", "-n", "2")
	if code != 0 || stdout != "three\ntoken ***\n" {
		t.Errorf("logs -n 2 (exit %d): %q", code, stdout)
	}
	if stdout, _, _ = runCLI(t, "--data-dir", p.Root, "logs"); !strings.HasPrefix(stdout, "one\ntwo\n") {
		t.Errorf("default shows everything up to 100 lines: %q", stdout)
	}

	// ownci's own log exists after any command and never holds secrets.
	stdout, _, code = runCLI(t, "--data-dir", p.Root, "--verbose", "logs", "--source", "ownci", "-n", "5")
	if code != 0 || !strings.Contains(stdout, "ownci start") {
		t.Errorf("ownci log (exit %d): %q", code, stdout)
	}

	// diag shows the newest Runner_*.log.
	diag := filepath.Join(p.RunnerDir(testRunner), "_diag")
	if err := os.MkdirAll(diag, 0o755); err != nil {
		t.Fatal(err)
	}
	older, newer := filepath.Join(diag, "Runner_20261014-000000-utc.log"), filepath.Join(diag, "Runner_20261015-000000-utc.log")
	for f, content := range map[string]string{older: "old diag\n", newer: "new diag\n"} {
		if err := os.WriteFile(f, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chtimes(older, time.Now().Add(-time.Hour), time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if stdout, _, _ = runCLI(t, "--data-dir", p.Root, "logs", "--source", "diag"); stdout != "new diag\n" {
		t.Errorf("diag: %q", stdout)
	}
}

func TestLogsProblems(t *testing.T) {
	useFake(t, healthyFake())
	p := installRunner(t)

	_, stderr, code := runCLI(t, "--data-dir", p.Root, "logs", "--source", "supervisor")
	if code != 1 || !strings.Contains(stderr, "no supervisor log yet") || !strings.Contains(stderr, p.LogsDir) || !strings.Contains(stderr, "Hint: It is created by 'ownci start --detach'") {
		t.Errorf("missing file: exit %d, %q", code, stderr)
	}
	_, stderr, code = runCLI(t, "--data-dir", p.Root, "logs", "--source", "bogus")
	if code != 2 || !strings.Contains(stderr, "unknown log source") {
		t.Errorf("bogus source: exit %d, %q", code, stderr)
	}
	_, _, code = runCLI(t, "--data-dir", p.Root, "logs", "-n", "-3")
	if code != 2 {
		t.Errorf("negative -n: exit %d", code)
	}
	// Not configured: the runner log cannot be located.
	_, stderr, code = runCLI(t, "--data-dir", filepath.Join(t.TempDir(), "d"), "logs")
	if code != 1 || !strings.Contains(stderr, "not set up") {
		t.Errorf("unconfigured: exit %d, %q", code, stderr)
	}
}
