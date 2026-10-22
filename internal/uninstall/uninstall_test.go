package uninstall

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ashfaqc137/ownci/internal/config"
	"github.com/ashfaqc137/ownci/internal/execx"
	"github.com/ashfaqc137/ownci/internal/hooks"
	"github.com/ashfaqc137/ownci/internal/testutil"
	"github.com/ashfaqc137/ownci/internal/testutil/fakegh"
)

const (
	rname = "t09"
	repo  = "me/demo"
)

// routeExec sends the runner's own scripts to the real OS and everything else
// to a handler (a Fake or a custom docker emulation).
type routeExec struct {
	real  execx.Runner
	other execx.Runner
}

func (r routeExec) LookPath(n string) (string, error) { return r.other.LookPath(n) }
func (r routeExec) Run(ctx context.Context, c execx.Cmd) (execx.Result, error) {
	if filepath.Base(c.Name) == "config.sh" {
		return r.real.Run(ctx, c)
	}
	return r.other.Run(ctx, c)
}

type env struct {
	t      *testing.T
	fg     *fakegh.Server
	reqs   atomic.Int64
	parent string // directory containing the data dir
	paths  config.Paths
	d      Deps
	out    bytes.Buffer
	errOut bytes.Buffer
	tokens map[string]string
	other  execx.Runner
}

func newEnv(t *testing.T) *env {
	t.Helper()
	for _, bin := range []string{"bash", "curl"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not on PATH", bin)
		}
	}
	e := &env{t: t, fg: fakegh.New(fakegh.Options{}), tokens: map[string]string{"GITHUB_TOKEN": "fake-user-token"}, other: execx.NewFake()}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		e.reqs.Add(1)
		e.fg.ServeHTTP(w, r)
	}))
	t.Cleanup(ts.Close)
	t.Setenv("OWNCI_DEV_GITHUB_API_URL", ts.URL) // read by the fake config.sh
	t.Setenv("RUNNER_ALLOW_RUNASROOT", "1")

	e.parent = t.TempDir()
	e.paths = config.NewPaths(filepath.Join(e.parent, "data"))
	if err := e.paths.Ensure(); err != nil {
		t.Fatal(err)
	}
	e.install(rname)
	e.fg.AddRunner(repo, rname, "ownci")
	e.reqs.Store(0)

	e.d = Deps{
		Paths:      e.paths,
		Exec:       routeExec{real: execx.NewOS(nil), other: e},
		In:         strings.NewReader(""),
		Out:        &e.out,
		Err:        &e.errOut,
		Getenv:     func(k string) string { return e.tokens[k] },
		Home:       "/home/someone-else",
		APIBaseURL: ts.URL,
		WebBaseURL: ts.URL + "/web",
	}
	return e
}

// Run/LookPath make env itself the "other" runner so tests can swap e.other.
func (e *env) Run(ctx context.Context, c execx.Cmd) (execx.Result, error) { return e.other.Run(ctx, c) }
func (e *env) LookPath(n string) (string, error)                          { return e.other.LookPath(n) }

// install lays out a set-up runner called name with its files, like setup does.
func (e *env) install(name string) {
	t := e.t
	dir := e.paths.RunnerDir(name)
	testutil.WriteFakeRunner(t, dir)
	rj := `{"agentId":7,"agentName":"` + name + `","gitHubUrl":"http://example.test/web/` + repo + `"}`
	write(t, filepath.Join(dir, ".runner"), "\xef\xbb\xbf"+rj)
	write(t, filepath.Join(dir, ".credentials"), "secret")
	write(t, filepath.Join(dir, "_work", "w", "file"), "x")
	write(t, e.paths.StateFile(name), "{}")
	write(t, e.paths.RunnerLog(name), "line\n")
	write(t, filepath.Join(e.paths.LogsDir, "supervisor-"+name+".log"), "line\n")
	write(t, filepath.Join(e.paths.CacheDir, "runner.tar.gz"), "archive")
	write(t, e.paths.OwnciLog, "log\n")
	cfg := &config.Config{Runner: &config.RunnerConfig{
		Name: name, Provider: "github", Repo: repo, URL: "http://example.test/web/" + repo,
		Labels: []string{"ownci"}, Isolation: "docker-job-container", CreatedAt: time.Now(),
	}}
	if err := config.Save(e.paths, cfg); err != nil {
		t.Fatal(err)
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (e *env) run(o Options) error {
	e.t.Helper()
	e.out.Reset()
	e.errOut.Reset()
	return Run(context.Background(), e.d, o)
}

func (e *env) listed() bool { return len(e.fg.Snapshot().Runners[repo]) > 0 }

func exists(p string) bool { _, err := os.Lstat(p); return err == nil }

func wantErr(t *testing.T, err error, msg, hint string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected an error containing %q", msg)
	}
	if !strings.Contains(err.Error(), msg) {
		t.Errorf("error %q lacks %q", err, msg)
	}
	var h interface{ UserHint() string }
	if !errors.As(err, &h) || h.UserHint() == "" {
		t.Errorf("error %q has no hint", err)
	} else if hint != "" && !strings.Contains(h.UserHint(), hint) {
		t.Errorf("hint %q lacks %q", h.UserHint(), hint)
	}
}

func TestPlainUninstallStopsOnlyAndKeepsEverything(t *testing.T) {
	e := newEnv(t)
	if err := e.run(Options{Yes: true}); err != nil {
		t.Fatal(err)
	}
	if !exists(e.paths.RunnerDir(rname)) || !exists(e.paths.ConfigFile) || !exists(e.paths.Marker) {
		t.Error("plain uninstall deleted files")
	}
	if !e.listed() || e.reqs.Load() != 0 {
		t.Errorf("GitHub was touched (requests: %d)", e.reqs.Load())
	}
	out := e.out.String()
	for _, want := range []string{"Uninstall summary", "Runner process   was not running", "still registered (remove at ", "/me/demo/settings/actions/runners", "kept at " + e.paths.Root, "Docker images"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

func TestUnregisterThroughConfigShRemove(t *testing.T) {
	e := newEnv(t)
	if err := e.run(Options{Unregister: true, Yes: true}); err != nil {
		t.Fatalf("%v\n%s\n%s", err, e.out.String(), e.errOut.String())
	}
	if e.listed() {
		t.Error("fakegh still lists the runner")
	}
	used := 0
	for _, tok := range e.fg.Snapshot().Tokens {
		if tok.Kind == "remove" && tok.Used {
			used++
		}
	}
	if used != 1 {
		t.Errorf("used remove tokens = %d, want 1 (config.sh remove)", used)
	}
	if exists(filepath.Join(e.paths.RunnerDir(rname), ".runner")) {
		t.Error("config.sh remove did not delete .runner")
	}
	if !exists(e.paths.ConfigFile) || !exists(e.paths.RunnerDir(rname)) {
		t.Error("files were deleted without --purge")
	}
	if !strings.Contains(e.out.String(), "✓ Runner unregistered from me/demo") || !strings.Contains(e.out.String(), "GitHub           unregistered") {
		t.Errorf("output:\n%s", e.out.String())
	}
	for _, tok := range []string{"fake-user-token"} {
		if strings.Contains(e.out.String()+e.errOut.String(), tok) {
			t.Errorf("token %q printed", tok)
		}
	}
}

func TestUnregisterFallsBackToAPIWhenRunnerFileIsCorrupt(t *testing.T) {
	e := newEnv(t)
	write(t, filepath.Join(e.paths.RunnerDir(rname), ".runner"), "garbage, not json")
	if err := e.run(Options{Unregister: true, Yes: true}); err != nil {
		t.Fatalf("%v\n%s\n%s", err, e.out.String(), e.errOut.String())
	}
	if e.listed() {
		t.Error("the API fallback did not remove the runner")
	}
	if !strings.Contains(e.errOut.String(), "through the API") || !strings.Contains(e.out.String(), "unregistered") {
		t.Errorf("stdout:\n%s\nstderr:\n%s", e.out.String(), e.errOut.String())
	}
}

func TestUnregisterWhenAlreadyGoneOnGitHub(t *testing.T) {
	e := newEnv(t)
	write(t, filepath.Join(e.paths.RunnerDir(rname), ".runner"), "garbage")
	// Remove it on GitHub behind ownci's back.
	runners := e.fg.Snapshot().Runners[repo]
	req, _ := http.NewRequest(http.MethodDelete, e.d.APIBaseURL+"/repos/"+repo+"/actions/runners/"+itoa(runners[0].ID), nil)
	req.Header.Set("Authorization", "Bearer x")
	if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode != 204 {
		t.Fatalf("setup: %v %v", resp, err)
	}
	if err := e.run(Options{Unregister: true, Yes: true}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(e.out.String(), "Runner was not registered on GitHub (nothing to remove)") {
		t.Errorf("output:\n%s", e.out.String())
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func TestUnregisterFailureKeepsFiles(t *testing.T) {
	e := newEnv(t)
	e.tokens["GITHUB_TOKEN"] = "bad-token" // fakegh rejects it
	err := e.run(Options{Unregister: true, Yes: true})
	wantErr(t, err, "", "")
	if !exists(e.paths.RunnerDir(rname)) || !exists(e.paths.ConfigFile) {
		t.Error("files were deleted although unregistering failed")
	}
	if !e.listed() {
		t.Error("runner disappeared")
	}
	if !strings.Contains(e.out.String(), "still registered") {
		t.Errorf("summary:\n%s", e.out.String())
	}
}

func TestNoTokenSuggestsLocalOnly(t *testing.T) {
	e := newEnv(t)
	e.tokens = map[string]string{}
	err := e.run(Options{Unregister: true, Yes: true})
	wantErr(t, err, "no GitHub token", "--local-only")
	if !exists(e.paths.RunnerDir(rname)) {
		t.Error("files were deleted")
	}
}

func TestPurgeRemovesOnlyOwnciFiles(t *testing.T) {
	e := newEnv(t)
	sibling := filepath.Join(e.parent, "sibling.txt")
	write(t, sibling, "mine")
	write(t, filepath.Join(e.parent, "other-dir", "x"), "mine")

	if err := e.run(Options{Unregister: true, Purge: true, Yes: true}); err != nil {
		t.Fatalf("%v\n%s\n%s", err, e.out.String(), e.errOut.String())
	}
	if exists(e.paths.Root) {
		t.Errorf("data dir still exists: %v", entriesOf(e.paths.Root))
	}
	if !exists(sibling) || !exists(filepath.Join(e.parent, "other-dir", "x")) {
		t.Error("a file outside the data dir was deleted")
	}
	if e.listed() {
		t.Error("runner still registered")
	}
	if !strings.Contains(e.out.String(), "removed ("+e.paths.Root+" is gone)") {
		t.Errorf("summary:\n%s", e.out.String())
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

func TestPurgeKeepsDataDirWithForeignFiles(t *testing.T) {
	e := newEnv(t)
	userFile := filepath.Join(e.paths.Root, "my-notes.txt")
	write(t, userFile, "important")
	write(t, filepath.Join(e.paths.LogsDir, "mine.log"), "important too")

	if err := e.run(Options{LocalOnly: true, Purge: true, Yes: true}); err != nil {
		t.Fatal(err)
	}
	if !exists(userFile) || !exists(filepath.Join(e.paths.LogsDir, "mine.log")) {
		t.Fatal("a user file was deleted")
	}
	if !exists(e.paths.Marker) {
		t.Error("the marker was removed from a directory that still holds foreign files")
	}
	if exists(e.paths.RunnerDir(rname)) || exists(e.paths.ConfigFile) || exists(e.paths.CacheDir) {
		t.Error("OwnCI's own files were not removed")
	}
	out := e.out.String()
	if !strings.Contains(out, "my-notes.txt") || !strings.Contains(out, "mine.log") || !strings.Contains(out, "partly removed") {
		t.Errorf("summary does not list the leftovers:\n%s", out)
	}
}

func TestPurgeRefusesWithoutMarker(t *testing.T) {
	e := newEnv(t)
	if err := os.Remove(e.paths.Marker); err != nil {
		t.Fatal(err)
	}
	err := e.run(Options{Unregister: true, Purge: true, Yes: true})
	wantErr(t, err, "not an OwnCI data directory", "Nothing was deleted")
	if !exists(e.paths.RunnerDir(rname)) || !exists(e.paths.ConfigFile) {
		t.Error("files deleted")
	}
	if !e.listed() || e.reqs.Load() != 0 {
		t.Error("the refusal must come before the unregistration")
	}
}

func TestCheckPurgeSafe(t *testing.T) {
	t.Run("home directory", func(t *testing.T) {
		root := t.TempDir()
		p := config.NewPaths(root)
		write(t, p.Marker, "x")
		wantErr(t, checkPurgeSafe(p, root), "home directory", "")
	})
	t.Run("symlinked layout directory", func(t *testing.T) {
		root := t.TempDir()
		p := config.NewPaths(root)
		write(t, p.Marker, "x")
		outside := t.TempDir()
		if err := os.Symlink(outside, p.RunnersDir); err != nil {
			t.Fatal(err)
		}
		wantErr(t, checkPurgeSafe(p, ""), "symbolic link", "")
	})
	t.Run("marker is a directory", func(t *testing.T) {
		root := t.TempDir()
		p := config.NewPaths(root)
		if err := os.MkdirAll(p.Marker, 0o755); err != nil {
			t.Fatal(err)
		}
		wantErr(t, checkPurgeSafe(p, ""), "not an OwnCI data directory", "")
	})
	t.Run("ok", func(t *testing.T) {
		root := t.TempDir()
		p := config.NewPaths(root)
		write(t, p.Marker, "x")
		if err := checkPurgeSafe(p, "/somewhere/else"); err != nil {
			t.Error(err)
		}
	})
	t.Run("within", func(t *testing.T) {
		for path, want := range map[string]bool{"/a/b/c": true, "/a/b": false, "/a/bc": false, "/a/b/../x": false, "/a": false} {
			if got := within("/a/b", path); got != want {
				t.Errorf("within(/a/b, %s) = %v", path, got)
			}
		}
	})
}

func TestNonTerminalWithoutYesIsAUsageError(t *testing.T) {
	e := newEnv(t)
	err := e.run(Options{Unregister: true, Purge: true})
	var ue *Error
	if !errors.As(err, &ue) || !ue.Usage || !strings.Contains(ue.Hint, "--yes") {
		t.Fatalf("err = %v", err)
	}
	if !exists(e.paths.RunnerDir(rname)) || !e.listed() || e.reqs.Load() != 0 {
		t.Error("something was done despite the refusal")
	}
}

func TestConfirmationPrompt(t *testing.T) {
	e := newEnv(t)
	e.d.IsTerminal = func() bool { return true }

	e.d.In = strings.NewReader("n\n")
	err := e.run(Options{LocalOnly: true, Purge: true})
	wantErr(t, err, "aborted", "")
	if !exists(e.paths.RunnerDir(rname)) || !strings.Contains(e.out.String(), "Proceed? [y/N]") {
		t.Error("declined prompt changed things or never asked")
	}

	e.d.In = strings.NewReader("y\n")
	if err := e.run(Options{LocalOnly: true, Purge: true}); err != nil {
		t.Fatal(err)
	}
	if exists(e.paths.RunnerDir(rname)) {
		t.Error("confirmed purge did nothing")
	}
}

func TestLocalOnlyPurgeMakesNoGitHubCalls(t *testing.T) {
	e := newEnv(t)
	if err := e.run(Options{LocalOnly: true, Purge: true, Yes: true}); err != nil {
		t.Fatal(err)
	}
	if e.reqs.Load() != 0 {
		t.Errorf("GitHub requests = %d, want 0", e.reqs.Load())
	}
	if !strings.Contains(e.out.String(), "skipped (--local-only)") || strings.Contains(e.errOut.String(), "WARNING") {
		t.Errorf("stdout:\n%s\nstderr:\n%s", e.out.String(), e.errOut.String())
	}
	if !e.listed() {
		t.Error("--local-only touched the registration")
	}
}

func TestPurgeWithoutUnregisterWarnsLoudly(t *testing.T) {
	e := newEnv(t)
	if err := e.run(Options{Purge: true, Yes: true}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(e.errOut.String(), "WARNING: the runner is still registered on GitHub") || !strings.Contains(e.errOut.String(), "/me/demo/settings/actions/runners") {
		t.Errorf("stderr:\n%s", e.errOut.String())
	}
	if !strings.Contains(e.out.String(), "still registered (remove at ") {
		t.Errorf("summary:\n%s", e.out.String())
	}
}

func TestUnregisterAndLocalOnlyContradict(t *testing.T) {
	e := newEnv(t)
	err := e.run(Options{Unregister: true, LocalOnly: true, Yes: true})
	var ue *Error
	if !errors.As(err, &ue) || !ue.Usage {
		t.Fatalf("err = %v", err)
	}
}

func TestNothingToUninstall(t *testing.T) {
	e := newEnv(t)
	if err := os.RemoveAll(e.paths.RunnersDir); err != nil {
		t.Fatal(err)
	}
	os.Remove(e.paths.ConfigFile)
	if err := e.run(Options{Yes: true, Purge: true}); err != nil {
		t.Fatal(err)
	}
	if got := e.out.String(); !strings.Contains(got, "Nothing to uninstall in "+e.paths.Root) {
		t.Errorf("output: %q", got)
	}
	if !exists(e.paths.Marker) {
		t.Error("purge ran although there was nothing to uninstall")
	}
}

func TestNameWorksWithoutConfig(t *testing.T) {
	e := newEnv(t)
	if err := os.Remove(e.paths.ConfigFile); err != nil {
		t.Fatal(err)
	}
	if err := e.run(Options{Name: rname, LocalOnly: true, Purge: true, Yes: true}); err != nil {
		t.Fatal(err)
	}
	if exists(e.paths.RunnerDir(rname)) {
		t.Error("runner dir survived")
	}
	// Corrupt config is tolerated too, with a warning.
	e2 := newEnv(t)
	write(t, e2.paths.ConfigFile, "{not json")
	if err := e2.run(Options{LocalOnly: true, Purge: true, Yes: true}); err != nil {
		t.Fatal(err) // a single runner directory is picked up without --name
	}
	if exists(e2.paths.RunnerDir(rname)) || !strings.Contains(e2.errOut.String(), "unreadable") {
		t.Errorf("stderr: %s", e2.errOut.String())
	}
}

func TestInvalidNameIsRejected(t *testing.T) {
	e := newEnv(t)
	err := e.run(Options{Name: "../../etc", Purge: true, Yes: true})
	var ue *Error
	if !errors.As(err, &ue) || !ue.Usage {
		t.Fatalf("err = %v", err)
	}
	if !exists(e.paths.RunnerDir(rname)) {
		t.Error("files deleted")
	}
}

func TestPurgeKeepsSharedFilesWhileAnotherRunnerRemains(t *testing.T) {
	e := newEnv(t)
	e.install("other")
	if err := e.run(Options{LocalOnly: true, Purge: true, Yes: true, Name: rname}); err != nil {
		t.Fatal(err)
	}
	if exists(e.paths.RunnerDir(rname)) || exists(e.paths.RunnerLog(rname)) || exists(e.paths.StateFile(rname)) {
		t.Error("the runner's own files remain")
	}
	for _, p := range []string{e.paths.RunnerDir("other"), e.paths.RunnerLog("other"), e.paths.CacheDir, e.paths.Marker, e.paths.OwnciLog} {
		if !exists(p) {
			t.Errorf("%s was removed although another runner remains", p)
		}
	}
	if cfg, err := config.Load(e.paths); err != nil || cfg.Runner.Name != "other" {
		t.Errorf("config = %+v, %v", cfg, err)
	}
	if !strings.Contains(e.out.String(), "other runners remain") {
		t.Errorf("summary:\n%s", e.out.String())
	}
}

// dockerEmu answers `docker run` for the cleanup of root-owned files: like the
// real container (root) it can delete what the user cannot, which is emulated
// by restoring permissions before running the rm script.
type dockerEmu struct {
	t      *testing.T
	calls  []execx.Cmd
	failRM bool
}

func (d *dockerEmu) LookPath(n string) (string, error) {
	if n == "docker" {
		return "/usr/bin/docker", nil
	}
	return "", exec.ErrNotFound
}

func (d *dockerEmu) Run(_ context.Context, c execx.Cmd) (execx.Result, error) {
	d.calls = append(d.calls, c)
	if d.failRM {
		return execx.Result{ExitCode: 125, Stderr: []byte("Unable to find image")}, nil
	}
	var work string
	for i, a := range c.Args {
		if a == "-v" {
			work = strings.TrimSuffix(c.Args[i+1], ":/w")
		}
	}
	_ = exec.Command("chmod", "-R", "u+rwx", work).Run()
	if err := exec.Command("sh", "-c", "rm -rf "+work+"/* "+work+"/.[!.]*").Run(); err != nil {
		d.t.Fatal(err)
	}
	return execx.Result{}, nil
}

func makeReadOnlyLeftover(t *testing.T, e *env) string {
	t.Helper()
	ro := filepath.Join(e.paths.RunnerDir(rname), "_work", "rootowned")
	write(t, filepath.Join(ro, "rootfile"), "x")
	if err := os.Chmod(ro, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(ro, 0o700) })
	return ro
}

func TestPurgeUsesCleanupContainerForRootOwnedFiles(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can delete read-only directories")
	}
	e := newEnv(t)
	emu := &dockerEmu{t: t}
	e.other = emu
	makeReadOnlyLeftover(t, e)

	if err := e.run(Options{LocalOnly: true, Purge: true, Yes: true}); err != nil {
		t.Fatalf("%v\n%s", err, e.out.String())
	}
	if len(emu.calls) != 1 {
		t.Fatalf("docker calls = %d", len(emu.calls))
	}
	args := strings.Join(emu.calls[0].Args, " ")
	for _, want := range []string{"run --rm", "--pull never", "--network none", "-v " + filepath.Join(e.paths.RunnerDir(rname), "_work") + ":/w", hooks.CleanupImage} {
		if !strings.Contains(args, want) {
			t.Errorf("docker args lack %q:\n%s", want, args)
		}
	}
	if exists(e.paths.Root) {
		t.Errorf("data dir remains: %v", entriesOf(e.paths.Root))
	}
}

func TestPurgeWithoutDockerReportsManualCleanup(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can delete read-only directories")
	}
	for name, other := range map[string]execx.Runner{"no docker": execx.NewFake(), "container fails": &dockerEmu{failRM: true}} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			if emu, ok := other.(*dockerEmu); ok {
				emu.t = t
			}
			e.other = other
			makeReadOnlyLeftover(t, e)

			if err := e.run(Options{LocalOnly: true, Purge: true, Yes: true}); err != nil {
				t.Fatal(err)
			}
			dir := e.paths.RunnerDir(rname)
			if !exists(dir) {
				t.Fatal("the directory with root-owned files vanished?")
			}
			out := e.out.String()
			if !strings.Contains(out, "sudo rm -rf "+dir) || !strings.Contains(out, "left: "+dir) {
				t.Errorf("summary lacks the manual cleanup advice:\n%s", out)
			}
			if !exists(e.paths.Root) {
				t.Error("data dir was removed although a runner dir is left")
			}
			// The other files were still removed.
			if exists(e.paths.ConfigFile) || exists(e.paths.RunnerLog(rname)) {
				t.Error("the rest of the purge did not happen")
			}
		})
	}
}

func TestPurgeNeverFollowsSymlinks(t *testing.T) {
	e := newEnv(t)
	outside := t.TempDir()
	write(t, filepath.Join(outside, "precious"), "keep")
	// A symlink inside the runner dir pointing outside must only be unlinked.
	if err := os.Symlink(outside, filepath.Join(e.paths.RunnerDir(rname), "link")); err != nil {
		t.Fatal(err)
	}
	if err := e.run(Options{LocalOnly: true, Purge: true, Yes: true}); err != nil {
		t.Fatal(err)
	}
	if !exists(filepath.Join(outside, "precious")) {
		t.Error("purge followed a symlink out of the data dir")
	}
}

func setMode(t *testing.T, e *env, mode string) {
	t.Helper()
	cfg, err := config.Load(e.paths)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Runner.Mode, cfg.Runner.Image = mode, ""
	if mode == config.ModeContainerRunner {
		cfg.Runner.Image = "img"
	}
	if err := config.Save(e.paths, cfg); err != nil {
		t.Fatal(err)
	}
}

func TestUninstallRemovesTheRunnerContainer(t *testing.T) {
	e := newEnv(t)
	setMode(t, e, config.ModeContainerRunner)
	f := execx.NewFake()
	f.SetPath("docker", "/usr/bin/docker")
	f.On("docker", "inspect").Stdout("running img\n")
	f.On("docker", "stop").Stdout("ownci-" + rname + "\n")
	f.On("docker", "rm").Stdout("ownci-" + rname + "\n")
	e.other = f
	if err := e.run(Options{Yes: true}); err != nil {
		t.Fatal(err)
	}
	var rm string
	for _, c := range f.Calls() {
		if c.Name == "docker" && c.Args[0] == "rm" {
			rm = strings.Join(c.Args, " ")
		}
	}
	if rm != "rm --force ownci-"+rname || !strings.Contains(e.out.String(), "✓ Removed the runner container ownci-"+rname) {
		t.Errorf("rm call %q, output:\n%s", rm, e.out.String())
	}
}

func TestUninstallHostModeLeavesDockerAlone(t *testing.T) {
	e := newEnv(t)
	f := execx.NewFake()
	f.SetPath("docker", "/usr/bin/docker")
	e.other = f
	if err := e.run(Options{Yes: true}); err != nil {
		t.Fatal(err)
	}
	for _, c := range f.Calls() {
		if c.Name == "docker" {
			t.Errorf("host-mode uninstall called docker: %v", c.Args)
		}
	}
}
