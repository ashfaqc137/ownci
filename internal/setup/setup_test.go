package setup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"

	"github.com/ashfaqc137/ownci/internal/config"
	"github.com/ashfaqc137/ownci/internal/execx"
	"github.com/ashfaqc137/ownci/internal/logging"
	"github.com/ashfaqc137/ownci/internal/platform"
	"github.com/ashfaqc137/ownci/internal/runner"
	"github.com/ashfaqc137/ownci/internal/testutil/fakegh"
)

const (
	userToken     = "fake-user-token"
	healthyDocker = `{"Client":{"Version":"27.0.1"},"Server":{"Version":"27.0.1","Os":"linux"}}`
	shimOutput    = "The command 'docker' could not be found in this WSL 2 distro."
)

// routeRunner sends the runner's own scripts (config.sh, Runner.Listener) to
// the real OS and everything else (docker, ldconfig, gh) to a Fake.
type routeRunner struct {
	fake *execx.Fake
	real execx.Runner
}

func (r routeRunner) LookPath(n string) (string, error) { return r.fake.LookPath(n) }
func (r routeRunner) Run(ctx context.Context, c execx.Cmd) (execx.Result, error) {
	switch filepath.Base(c.Name) {
	case "config.sh", "Runner.Listener":
		return r.real.Run(ctx, c)
	}
	return r.fake.Run(ctx, c)
}

type env struct {
	t      *testing.T
	fg     *fakegh.Server
	ts     *httptest.Server
	reqs   atomic.Int64
	fake   *execx.Fake
	deps   Deps
	out    bytes.Buffer
	errOut bytes.Buffer
}

func healthyFake() *execx.Fake {
	f := execx.NewFake()
	f.SetPath("docker", "/usr/bin/docker")
	f.SetPath("ldconfig", "/sbin/ldconfig")
	f.On("docker", "version").Stdout(healthyDocker)
	f.On("docker", "pull")
	f.On("/sbin/ldconfig", "-p").Stdout("libicuuc.so.74 => /lib/libicuuc.so.74")
	return f
}

func newEnv(t *testing.T, opts fakegh.Options, wrap func(http.Handler) http.Handler) *env {
	t.Helper()
	for _, bin := range []string{"bash", "curl"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not on PATH", bin)
		}
	}
	e := &env{t: t, fake: healthyFake()}
	e.fg = fakegh.New(opts)
	var h http.Handler = e.fg
	if wrap != nil {
		h = wrap(h)
	}
	e.ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		e.reqs.Add(1)
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(e.ts.Close)
	t.Setenv("OWNCI_DEV_GITHUB_API_URL", e.ts.URL) // read by the fake config.sh
	t.Setenv("RUNNER_ALLOW_RUNASROOT", "1")        // the fake refuses root otherwise

	paths := config.NewPaths(filepath.Join(t.TempDir(), "data"))
	if err := paths.Ensure(); err != nil {
		t.Fatal(err)
	}
	logger, closer, err := logging.Setup(logging.Options{LogFile: paths.OwnciLog, Verbose: true, Stderr: &bytes.Buffer{}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closer.Close() })

	e.deps = Deps{
		Paths:      paths,
		Platform:   platform.Info{OS: "linux", Arch: "amd64", RunnerArch: "linux-x64"},
		Exec:       routeRunner{fake: e.fake, real: execx.NewOS(logger)},
		Logger:     logger,
		Out:        &e.out,
		Err:        &e.errOut,
		Getenv:     func(k string) string { return map[string]string{"GITHUB_TOKEN": userToken}[k] },
		Geteuid:    func() int { return 1000 },
		Hostname:   "box.local",
		APIBaseURL: e.ts.URL,
		WebBaseURL: e.ts.URL + "/web",
		AssetFor: func(string) (runner.Asset, error) {
			return runner.Asset{Arch: "linux-x64", URL: e.ts.URL + "/_fake/runner.tar.gz", SHA256: e.fg.RunnerSHA256(), Version: fakegh.RunnerVersion}, nil
		},
	}
	return e
}

func (e *env) run(o Options) error {
	e.t.Helper()
	e.out.Reset()
	e.errOut.Reset()
	if o.Repo == "" {
		o.Repo = "me/demo"
	}
	return Run(context.Background(), e.deps, o)
}

func (e *env) runnerDir(name string) string { return e.deps.Paths.RunnerDir(name) }

func (e *env) loadConfig() *config.RunnerConfig {
	e.t.Helper()
	c, err := config.Load(e.deps.Paths)
	if err != nil {
		e.t.Fatalf("config.Load: %v", err)
	}
	return c.Runner
}

func (e *env) noConfig() {
	e.t.Helper()
	if _, err := config.Load(e.deps.Paths); !errors.Is(err, config.ErrNotConfigured) {
		e.t.Errorf("config exists or is broken: %v", err)
	}
}

// assertNoSecrets checks that neither the user's token nor any token fakegh
// issued appears in stdout, stderr or the log file.
func (e *env) assertNoSecrets() {
	e.t.Helper()
	secrets := []string{userToken}
	for tok := range e.fg.Snapshot().Tokens {
		secrets = append(secrets, tok)
	}
	logb, _ := os.ReadFile(e.deps.Paths.OwnciLog)
	for name, text := range map[string]string{"stdout": e.out.String(), "stderr": e.errOut.String(), "ownci.log": string(logb)} {
		for _, s := range secrets {
			if strings.Contains(text, s) {
				e.t.Errorf("%s contains secret %q:\n%s", name, s, text)
			}
		}
	}
}

func hintOf(err error) string {
	var h interface{ UserHint() string }
	if errors.As(err, &h) {
		return h.UserHint()
	}
	return ""
}

func wantErr(t *testing.T, err error, msg, hint string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected an error containing %q", msg)
	}
	if !strings.Contains(err.Error(), msg) {
		t.Errorf("error %q lacks %q", err, msg)
	}
	if hint != "" && !strings.Contains(hintOf(err), hint) {
		t.Errorf("hint %q lacks %q", hintOf(err), hint)
	}
	if hintOf(err) == "" {
		t.Errorf("error %q has no hint", err)
	}
}

func fakeLabels(e *env, repo string) [][]string {
	var all [][]string
	for _, r := range e.fg.Snapshot().Runners[repo] {
		var ls []string
		for _, l := range r.Labels {
			ls = append(ls, l.Name)
		}
		all = append(all, ls)
	}
	return all
}

func TestHappyPath(t *testing.T) {
	e := newEnv(t, fakegh.Options{}, nil)
	if err := e.run(Options{}); err != nil {
		t.Fatal(err)
	}
	dir := e.runnerDir("ownci-box")
	if !runner.Registered(dir) {
		t.Fatal(".runner missing")
	}
	fi, err := os.Stat(e.deps.Paths.ConfigFile)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("config.json: %v, %v", fi, err)
	}
	rc := e.loadConfig()
	if rc.Name != "ownci-box" || rc.Provider != "github" || rc.Repo != "me/demo" ||
		rc.URL != e.ts.URL+"/web/me/demo" || strings.Join(rc.Labels, ",") != "ownci" ||
		rc.InstalledVersion != fakegh.RunnerVersion || rc.Isolation != "docker-job-container" || rc.CreatedAt.IsZero() {
		t.Errorf("config = %+v", rc)
	}
	if got := fakeLabels(e, "me/demo"); len(got) != 1 || strings.Join(got[0], ",") != "ownci" {
		t.Errorf("fakegh runners labels = %v", got)
	}
	args, err := os.ReadFile(filepath.Join(dir, ".fake-config-args"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--no-default-labels", "--labels ownci", "--token ***", "--name ownci-box", "--unattended"} {
		if !strings.Contains(string(args), want) {
			t.Errorf("config.sh args %q lack %q", args, want)
		}
	}
	if strings.Contains(string(args), "--replace") {
		t.Errorf("fresh setup used --replace: %q", args)
	}
	out := e.out.String()
	for _, want := range []string{
		"✓ Preflight checks passed (platform, user, data-dir, docker)",
		"✓ GitHub token from GITHUB_TOKEN",
		"✓ Runner registered on GitHub (id ",
		"✓ OwnCI is set up for me/demo as runner 'ownci-box' (labels: ownci)",
		"ownci start", "runs-on: [ownci]", "jobs without 'container:' will be rejected",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout lacks %q:\n%s", want, out)
		}
	}
	e.assertNoSecrets()
}

func TestExtraLabels(t *testing.T) {
	e := newEnv(t, fakegh.Options{}, nil)
	if err := e.run(Options{Labels: []string{"gpu", "big"}}); err != nil {
		t.Fatal(err)
	}
	if got := fakeLabels(e, "me/demo"); len(got) != 1 || strings.Join(got[0], ",") != "ownci,gpu,big" {
		t.Errorf("labels = %v", got)
	}
	if rc := e.loadConfig(); strings.Join(rc.Labels, ",") != "ownci,gpu,big" {
		t.Errorf("config labels = %v", rc.Labels)
	}
	args, _ := os.ReadFile(filepath.Join(e.runnerDir("ownci-box"), ".fake-config-args"))
	if !strings.Contains(string(args), "--labels ownci,gpu,big") {
		t.Errorf("args = %q", args)
	}
}

func TestForbiddenLabelRejectedBeforeAnything(t *testing.T) {
	e := newEnv(t, fakegh.Options{}, nil)
	err := e.run(Options{Labels: []string{"self-hosted"}})
	var se *Error
	if !errors.As(err, &se) || !se.Usage {
		t.Fatalf("err = %v", err)
	}
	if e.reqs.Load() != 0 || len(e.fake.Calls()) != 0 {
		t.Errorf("work done before validation: %d requests, %d commands", e.reqs.Load(), len(e.fake.Calls()))
	}
}

func TestPublicRepo(t *testing.T) {
	e := newEnv(t, fakegh.Options{PublicRepos: []string{"me/pub"}}, nil)
	err := e.run(Options{Repo: "me/pub"})
	wantErr(t, err, "public repository", "--allow-public-repo")
	if !strings.Contains(hintOf(err), "docs.github.com") {
		t.Errorf("hint lacks the docs link: %q", hintOf(err))
	}
	if _, statErr := os.Stat(e.runnerDir("ownci-box")); statErr == nil {
		t.Error("runner dir created for a refused repo")
	}
	e.noConfig()

	if err := e.run(Options{Repo: "me/pub", AllowPublicRepo: true}); err != nil {
		t.Fatal(err)
	}
	w := e.errOut.String()
	if !strings.Contains(w, "WARNING") || !strings.Contains(w, "Require approval for all outside collaborators") {
		t.Errorf("stderr lacks the public-repo warning:\n%s", w)
	}
	if e.loadConfig().Repo != "me/pub" {
		t.Error("config not saved")
	}
}

func TestNonAdminRefused(t *testing.T) {
	e := newEnv(t, fakegh.Options{NoAdmin: []string{"me/noadmin"}}, nil)
	err := e.run(Options{Repo: "me/noadmin"})
	wantErr(t, err, "no admin access to me/noadmin", "Administration: read & write")
	if _, statErr := os.Stat(e.runnerDir("ownci-box")); statErr == nil {
		t.Error("runner dir created without admin")
	}
	e.noConfig()
}

func TestSecondSetupRefusedThenForce(t *testing.T) {
	e := newEnv(t, fakegh.Options{}, nil)
	if err := e.run(Options{}); err != nil {
		t.Fatal(err)
	}
	first := e.loadConfig()
	before := e.reqs.Load()

	err := e.run(Options{})
	wantErr(t, err, "already set up for me/demo as 'ownci-box'", "--force")
	if e.reqs.Load() != before {
		t.Error("refusal must happen before any network call")
	}

	if err := e.run(Options{Force: true}); err != nil {
		t.Fatal(err)
	}
	args, _ := os.ReadFile(filepath.Join(e.runnerDir("ownci-box"), ".fake-config-args"))
	if !strings.Contains(string(args), "--replace") {
		t.Errorf("force did not pass --replace: %q", args)
	}
	if got := fakeLabels(e, "me/demo"); len(got) != 1 {
		t.Errorf("runners after force = %v", got)
	}
	if !strings.Contains(e.out.String(), "Reusing the extracted runner") {
		t.Errorf("force should not re-download:\n%s", e.out.String())
	}
	if again := e.loadConfig(); !again.CreatedAt.Equal(first.CreatedAt) {
		t.Errorf("createdAt changed: %v → %v", first.CreatedAt, again.CreatedAt)
	}
	e.assertNoSecrets()
}

func TestForceRefusedWhileRunnerRuns(t *testing.T) {
	e := newEnv(t, fakegh.Options{}, nil)
	if err := e.run(Options{}); err != nil {
		t.Fatal(err)
	}
	// Hold the runner's lock like a live supervisor.
	lock, err := os.OpenFile(e.deps.Paths.LockFile("ownci-box"), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	before := e.reqs.Load()
	err = e.run(Options{Force: true})
	wantErr(t, err, "is running", "ownci stop")
	if e.reqs.Load() != before {
		t.Error("refusal must happen before any network call")
	}
	// Once the lock is gone, force works again.
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	if err := e.run(Options{Force: true}); err != nil {
		t.Fatal(err)
	}
}

func TestDifferentRepoOrNameRefusedEvenWithForce(t *testing.T) {
	e := newEnv(t, fakegh.Options{}, nil)
	if err := e.run(Options{}); err != nil {
		t.Fatal(err)
	}
	err := e.run(Options{Repo: "me/other", Force: true})
	wantErr(t, err, "already set up for me/demo", "uninstall")
	err = e.run(Options{Name: "ownci-second", Force: true})
	wantErr(t, err, "already set up for me/demo", "uninstall")
	if e.loadConfig().Repo != "me/demo" {
		t.Error("config was changed by a refused setup")
	}
}

func TestNameCollisionOnGitHub(t *testing.T) {
	e := newEnv(t, fakegh.Options{}, nil)
	e.fg.AddRunner("me/demo", "ownci-box", "old")
	err := e.run(Options{})
	wantErr(t, err, "runner named 'ownci-box' already exists on me/demo", "--force")
	if _, statErr := os.Stat(e.runnerDir("ownci-box")); statErr == nil {
		t.Error("runner dir created despite the collision")
	}
	if err := e.run(Options{Force: true}); err != nil {
		t.Fatal(err)
	}
	if got := fakeLabels(e, "me/demo"); len(got) != 1 || strings.Join(got[0], ",") != "ownci" {
		t.Errorf("runner not replaced: %v", got)
	}
}

func TestDockerPreflightFailureAbortsBeforeNetwork(t *testing.T) {
	e := newEnv(t, fakegh.Options{}, nil)
	e.fake.On("docker", "version").Exit(1).Stderr(shimOutput)
	err := e.run(Options{})
	wantErr(t, err, "docker check failed", "WSL integration")
	if n := e.reqs.Load(); n != 0 {
		t.Errorf("%d requests made before the Docker check", n)
	}
	if _, statErr := os.Stat(e.runnerDir("ownci-box")); statErr == nil {
		t.Error("runner dir created")
	}
}

func TestSkipDockerCheckWarns(t *testing.T) {
	e := newEnv(t, fakegh.Options{}, nil)
	e.fake.On("docker", "version").Exit(1).Stderr(shimOutput)
	if err := e.run(Options{SkipDockerCheck: true}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(e.errOut.String(), "skipping the Docker check") || strings.Contains(e.out.String(), "docker)") {
		t.Errorf("stderr %q / stdout %q", e.errOut.String(), e.out.String())
	}
}

func TestRunnerDepsFailureOnlyWarns(t *testing.T) {
	e := newEnv(t, fakegh.Options{}, nil)
	e.fake.On("/sbin/ldconfig", "-p").Stdout("libc.so.6 => /lib/libc.so.6")
	if err := e.run(Options{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(e.errOut.String(), "runner-deps") || !strings.Contains(e.errOut.String(), "ICU") {
		t.Errorf("stderr = %q", e.errOut.String())
	}
}

func TestRootUserRefused(t *testing.T) {
	e := newEnv(t, fakegh.Options{}, nil)
	e.deps.Geteuid = func() int { return 0 }
	wantErr(t, e.run(Options{}), "user check failed", "normal user")
	if e.reqs.Load() != 0 {
		t.Error("network used despite root")
	}
}

func TestMissingTokenHint(t *testing.T) {
	e := newEnv(t, fakegh.Options{}, nil)
	e.deps.Getenv = func(string) string { return "" }
	wantErr(t, e.run(Options{}), "no GitHub token", "gh auth login")
	if e.reqs.Load() != 0 {
		t.Error("requests made without a token")
	}
}

func TestRejectedTokenMentionsSource(t *testing.T) {
	e := newEnv(t, fakegh.Options{}, nil)
	e.deps.Getenv = func(k string) string { return map[string]string{"GH_TOKEN": "bad-token"}[k] }
	err := e.run(Options{})
	wantErr(t, err, "invalid or expired", "$GH_TOKEN")
	if strings.Contains(err.Error()+hintOf(err), "bad-token") {
		t.Error("token leaked into the error")
	}
}

func TestDryRun(t *testing.T) {
	e := newEnv(t, fakegh.Options{}, nil)
	if err := e.run(Options{DryRun: true, Labels: []string{"gpu"}}); err != nil {
		t.Fatal(err)
	}
	out := e.out.String()
	for _, want := range []string{
		"Dry run", e.ts.URL + "/_fake/runner.tar.gz", e.fg.RunnerSHA256(),
		"--token ***", "--labels ownci,gpu", "--no-default-labels", "--url " + e.ts.URL + "/web/me/demo",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("dry-run output lacks %q:\n%s", want, out)
		}
	}
	if _, err := os.Stat(e.runnerDir("ownci-box")); err == nil {
		t.Error("dry-run created the runner dir")
	}
	e.noConfig()
	if n := len(e.fg.Snapshot().Tokens); n != 0 {
		t.Errorf("dry-run requested %d tokens", n)
	}
	entries, _ := os.ReadDir(e.deps.Paths.CacheDir)
	if len(entries) != 0 {
		t.Errorf("dry-run downloaded something: %v", entries)
	}
	e.assertNoSecrets()
}

func TestDryRunStillRefuses(t *testing.T) {
	e := newEnv(t, fakegh.Options{PublicRepos: []string{"me/pub"}}, nil)
	wantErr(t, e.run(Options{Repo: "me/pub", DryRun: true}), "public repository", "--allow-public-repo")
}

// burnFirstRegistrationToken consumes the first registration token fakegh
// issues, so config.sh's registration with it is rejected.
func burnFirstRegistrationToken(next http.Handler) http.Handler {
	var once sync.Once
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		burn := false
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/registration-token") {
			once.Do(func() { burn = true })
		}
		if !burn {
			next.ServeHTTP(w, r)
			return
		}
		rec := httptest.NewRecorder()
		next.ServeHTTP(rec, r)
		var v struct{ Token string }
		_ = json.Unmarshal(rec.Body.Bytes(), &v)
		body := fmt.Sprintf(`{"token":%q,"name":"burner","labels":[]}`, v.Token)
		next.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/_fake/register", strings.NewReader(body)))
		for k, vals := range rec.Header() {
			w.Header()[k] = vals
		}
		w.WriteHeader(rec.Code)
		w.Write(rec.Body.Bytes())
	})
}

func TestConfigureFailureKeepsRunnerAndRetrySucceeds(t *testing.T) {
	e := newEnv(t, fakegh.Options{}, burnFirstRegistrationToken)
	err := e.run(Options{})
	wantErr(t, err, "config.sh failed", "ownci doctor")
	if !strings.Contains(err.Error(), "Unauthorized") {
		t.Errorf("error should show config.sh output: %v", err)
	}
	e.noConfig()
	dir := e.runnerDir("ownci-box")
	if _, statErr := os.Stat(filepath.Join(dir, "config.sh")); statErr != nil {
		t.Errorf("extracted runner was not kept: %v", statErr)
	}
	if runner.Registered(dir) {
		t.Error(".runner written despite the failure")
	}
	e.assertNoSecrets()

	// Retry: the extracted-but-unregistered directory is replaced, no --force.
	if err := e.run(Options{}); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if !runner.Registered(dir) || e.loadConfig().Name != "ownci-box" {
		t.Error("retry did not complete setup")
	}
	e.assertNoSecrets()
}

func TestConfigSaveFailureIsLoud(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission bits do not bind root")
	}
	e := newEnv(t, fakegh.Options{}, nil)
	if err := os.Chmod(e.deps.Paths.ConfigDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(e.deps.Paths.ConfigDir, 0o700) })
	err := e.run(Options{})
	wantErr(t, err, "registered on GitHub, but saving OwnCI's config failed", "uninstall --unregister --name ownci-box")
}

func TestCorruptConfig(t *testing.T) {
	e := newEnv(t, fakegh.Options{}, nil)
	if err := os.WriteFile(e.deps.Paths.ConfigFile, []byte("{nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	wantErr(t, e.run(Options{}), "parsing", "--force")
	if err := e.run(Options{Force: true}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(e.errOut.String(), "ignoring unreadable config") {
		t.Errorf("stderr = %q", e.errOut.String())
	}
	e.loadConfig()
}

func TestRegisteredDirWithoutConfigNeedsForce(t *testing.T) {
	e := newEnv(t, fakegh.Options{}, nil)
	if err := e.run(Options{}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(e.deps.Paths.ConfigFile); err != nil {
		t.Fatal(err)
	}
	wantErr(t, e.run(Options{}), "already registered", "--force")
	if err := e.run(Options{Force: true}); err != nil {
		t.Fatal(err)
	}
	e.loadConfig()
}

func TestInvalidInputs(t *testing.T) {
	e := newEnv(t, fakegh.Options{}, nil)
	wantErr(t, e.run(Options{Repo: "nonsense"}), "invalid repository", "owner/name")
	wantErr(t, e.run(Options{Name: "../evil"}), "invalid runner name", "a-z")
	if e.reqs.Load() != 0 {
		t.Error("requests made for invalid input")
	}
}
