// Package setup implements `ownci setup`: it takes a machine from nothing to
// a registered, configured runner. It returns errors with hints and writes
// progress to the writers it is given; flag parsing and exit codes stay in cli.
package setup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ashfaqc137/ownci/internal/config"
	"github.com/ashfaqc137/ownci/internal/docker"
	"github.com/ashfaqc137/ownci/internal/doctor"
	"github.com/ashfaqc137/ownci/internal/execx"
	"github.com/ashfaqc137/ownci/internal/github"
	"github.com/ashfaqc137/ownci/internal/hooks"
	"github.com/ashfaqc137/ownci/internal/isolation"
	"github.com/ashfaqc137/ownci/internal/platform"
	"github.com/ashfaqc137/ownci/internal/runner"
	"github.com/ashfaqc137/ownci/internal/runnercontainer"
	"github.com/ashfaqc137/ownci/internal/supervisor"
)

const (
	// IsolationMode is recorded in config.json for host mode (D1).
	IsolationMode = "docker-job-container"
	// IsolationRunnerContainer is recorded for container-runner mode (D13).
	IsolationRunnerContainer = "docker-runner-container"
	securityDocs             = "https://docs.github.com/actions/hosting-your-own-runners/managing-self-hosted-runners/about-self-hosted-runners#self-hosted-runner-security"
	permissionFix            = "Use a fine-grained PAT with 'Administration: read & write' on this repository, or a classic PAT with the 'repo' scope."
)

// Error is a setup failure with an actionable hint. The cli layer renders it
// through UserHint (this package must not import cli).
type Error struct {
	Msg, Hint string
	Err       error
	Usage     bool // bad input: the cli reports exit code 2
}

func (e *Error) Error() string    { return e.Msg }
func (e *Error) Unwrap() error    { return e.Err }
func (e *Error) UserHint() string { return e.Hint }

// Options are the user's choices (the flags of `ownci setup`).
type Options struct {
	Repo            string   // "owner/name" or a GitHub URL
	Name            string   // "" = default (or the configured runner's name)
	Labels          []string // extra labels; "ownci" is always added; nil keeps an existing setup's labels
	Force           bool
	AllowPublicRepo bool
	DryRun          bool
	SkipDockerCheck bool
	// Mode is config.ModeHost or config.ModeContainerRunner; "" keeps the
	// mode of an existing setup (host for a new one).
	Mode string
	// Image is the runner image in container-runner mode; "" selects
	// runnercontainer.DefaultImage.
	Image string
}

// Deps are the collaborators, injected so tests need no network or Docker.
type Deps struct {
	Paths      config.Paths
	Platform   platform.Info
	Exec       execx.Runner
	HTTP       *http.Client // nil = package defaults
	Logger     *slog.Logger
	Out, Err   io.Writer
	Getenv     func(string) string
	Geteuid    func() int
	Hostname   string
	AssetFor   func(arch string) (runner.Asset, error)
	APIBaseURL string // GitHub REST base
	WebBaseURL string // github.com base, used for the runner's --url
	Now        func() time.Time
}

type run struct {
	d     Deps
	o     Options
	repo  github.Repo
	name  string
	dir   string
	mode  string
	image string // runner image, container-runner mode only
}

// Run executes the setup flow. See tasks/t06-setup.md for the step list; the
// numbers in comments refer to it.
func Run(ctx context.Context, d Deps, o Options) error {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Logger == nil {
		d.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	repo, err := github.ParseRepo(o.Repo)
	if err != nil {
		return err
	}
	labels, err := ValidateLabels(o.Labels)
	if err != nil {
		return err
	}
	if o.Mode != "" && !config.ValidMode(o.Mode) {
		return &Error{Usage: true, Msg: fmt.Sprintf("unknown --mode %q", o.Mode),
			Hint: fmt.Sprintf("Use --mode %s (jobs must declare container:) or --mode %s (the runner runs in a container that is the default job image).", config.ModeHost, config.ModeContainerRunner)}
	}
	name := o.Name
	if name == "" {
		name = runner.DefaultName(d.Hostname)
	}
	r := &run{d: d, o: o, repo: repo, name: name}

	// 1. Preflight (no network).
	if err := r.preflight(ctx); err != nil {
		return err
	}
	// 2. Existing install detection (local).
	prev, registered, err := r.detectExisting()
	if err != nil {
		return err
	}
	if err := runner.ValidateName(r.name); err != nil {
		return err
	}
	r.dir = d.Paths.RunnerDir(r.name)
	if o.Labels == nil && prev != nil {
		// Re-running setup (--force, a mode switch) keeps the labels
		// workflows already target.
		if labels, err = ValidateLabels(prev.Labels); err != nil {
			return err
		}
	}
	allLabels := append([]string{"ownci"}, labels...)
	if err := r.resolveMode(prev); err != nil {
		return err
	}

	// 3. Token.
	token, src, err := github.ResolveToken(ctx, d.Getenv, d.Exec)
	if err != nil {
		return err
	}
	r.ok("GitHub token from %s", src)
	gh := github.NewClient(token,
		github.WithBaseURL(d.APIBaseURL), github.WithHTTPClient(d.HTTP),
		github.WithLogger(d.Logger), github.WithTokenSource(src))

	// 4. Repo checks, 5. name collision on GitHub.
	if err := r.checkRepo(ctx, gh); err != nil {
		return err
	}
	if err := r.checkNameFree(ctx, gh); err != nil {
		return err
	}

	repoURL := d.WebBaseURL + "/" + repo.String()
	reuse := registered && o.Force // an extracted, registered runner is kept as is
	if o.DryRun {
		return r.dryRun(repoURL, allLabels, reuse)
	}

	// 6. Download & extract.
	version, err := r.install(ctx, reuse, prev)
	if err != nil {
		return err
	}
	// 6b. The runner image, checked before anything is registered on GitHub.
	if r.mode == config.ModeContainerRunner {
		if err := r.prepareImage(ctx); err != nil {
			return err
		}
	}
	// 7. Registration token (single-use, short-lived).
	reg, err := gh.RegistrationToken(ctx, repo)
	if err != nil {
		return err
	}
	// 8. Configure.
	configure := func() error {
		return runner.Configure(ctx, d.Exec, r.dir, runner.ConfigureOptions{
			URL: repoURL, Token: reg.Token, Name: r.name, Labels: allLabels, Replace: o.Force,
		})
	}
	if reuse {
		err = r.reconfigure(configure)
	} else {
		err = configure()
	}
	if err != nil {
		return err
	}
	r.ok("Runner configured")
	// 9. Verify.
	if err := r.verify(ctx, gh); err != nil {
		return err
	}
	// 10. Isolation: .env enforcement, cleanup hook and images.
	if err := installIsolation(ctx, d, r.dir, repo.String(), r.policy()); err != nil {
		return err
	}
	// 11. Persist.
	created := d.Now().UTC()
	if prev != nil {
		created = prev.CreatedAt
	}
	cfg := &config.Config{Runner: &config.RunnerConfig{
		Name: r.name, Provider: "github", Repo: repo.String(), URL: repoURL, Labels: allLabels,
		InstalledVersion: version, Isolation: IsolationMode, Mode: r.mode, CreatedAt: created,
	}}
	if r.mode == config.ModeContainerRunner {
		cfg.Runner.Isolation, cfg.Runner.Image = IsolationRunnerContainer, r.image
	}
	if err := config.Save(d.Paths, cfg); err != nil {
		return &Error{
			Msg: fmt.Sprintf("the runner is registered on GitHub, but saving OwnCI's config failed: %v", err),
			Hint: fmt.Sprintf("Remove the registration with 'ownci uninstall --unregister --name %s', or retry 'ownci setup --repo %s --force'.",
				r.name, repo),
			Err: err,
		}
	}
	// 12. Next steps.
	fmt.Fprintf(d.Out, "✓ OwnCI is set up for %s as runner '%s' (labels: %s, mode: %s)\n", repo, r.name, strings.Join(allLabels, ","), r.mode)
	if r.mode == config.ModeContainerRunner {
		fmt.Fprintf(d.Out, "Next:\n  1. Start contributing compute:   ownci start\n"+
			"  2. Target it from a workflow:    runs-on: [ownci]\n"+
			"     (jobs without 'container:' run in %s; jobs with one use their own image)\n"+
			"Isolation notes: every job can use the host's Docker daemon through its socket, job\n"+
			"containers can reach the network, and 'services:' run as sibling containers.\n", r.image)
		return nil
	}
	fmt.Fprintf(d.Out, "Next:\n  1. Start contributing compute:   ownci start\n"+
		"  2. Target it from a workflow:    runs-on: [ownci]   and   container: <image>\n"+
		"     (jobs without 'container:' will be rejected by this runner)\n"+
		"Isolation notes: job containers can still reach the network, and 'services:' and\n"+
		"'uses: docker://…' actions run as sibling containers on the same Docker daemon.\n")
	return nil
}

// resolveMode settles the execution mode and runner image: the flags win, an
// existing setup's mode is kept when --mode is not given, and a new setup is
// host mode (D1).
func (r *run) resolveMode(prev *config.RunnerConfig) error {
	r.mode = r.o.Mode
	if r.mode == "" {
		r.mode = config.ModeHost
		if prev != nil {
			r.mode = prev.EffectiveMode()
		}
	}
	if r.mode != config.ModeContainerRunner {
		if r.o.Image != "" {
			return &Error{Usage: true, Msg: "--image needs --mode " + config.ModeContainerRunner,
				Hint: "In host mode each job names its image with 'container:'."}
		}
		return nil
	}
	r.image = r.o.Image
	if r.image == "" {
		r.image = runnercontainer.DefaultImage
	}
	if err := runnercontainer.CheckPath(r.dir); err != nil {
		return &Error{Msg: err.Error(), Hint: hintOr(err, ""), Err: err}
	}
	return nil
}

func (r *run) policy() isolation.Policy {
	p := isolation.HostPolicy()
	if r.mode == config.ModeContainerRunner {
		p.Mode, p.RunnerImage = r.mode, r.image
	}
	return p
}

func (r *run) ok(format string, a ...any) { fmt.Fprintf(r.d.Out, "✓ "+format+"\n", a...) }
func (r *run) warn(format string, a ...any) {
	fmt.Fprintf(r.d.Err, "Warning: "+format+"\n", a...)
}

// preflight runs the doctor checks setup depends on and stops at the first
// failure. runner-deps only warns: the runner may work anyway.
func (r *run) preflight(ctx context.Context) error {
	d := r.d
	checks := []doctor.Check{
		doctor.PlatformCheck(d.Platform),
		doctor.UserCheck(d.Geteuid),
		doctor.DataDirCheck(d.Paths.Root),
	}
	names := []string{"platform", "user", "data-dir"}
	if r.o.SkipDockerCheck {
		r.warn("skipping the Docker check (--skip-docker-check); jobs will fail without a working Docker")
	} else {
		checks = append(checks, doctor.DockerCheck(d.Exec))
		names = append(names, "docker")
	}
	checks = append(checks, doctor.RunnerDepsCheck(d.Exec))

	for _, c := range checks {
		res := doctor.Run(ctx, []doctor.Check{c}).Results[0]
		d.Logger.Debug("preflight", "check", res.Name, "level", res.Level.String(), "detail", res.Detail)
		switch {
		case res.Level == doctor.Pass:
		case res.Name == "runner-deps" || res.Level == doctor.Warn:
			r.warn("%s: %s", res.Name, res.Detail)
			if res.Hint != "" {
				fmt.Fprintf(d.Err, "Hint: %s\n", res.Hint)
			}
		default:
			return &Error{Msg: fmt.Sprintf("%s check failed: %s", res.Name, res.Detail), Hint: res.Hint}
		}
	}
	r.ok("Preflight checks passed (%s)", strings.Join(names, ", "))
	return nil
}

// detectExisting applies the local rules of step 2. It returns the saved
// runner config (if any) and whether the runner directory is registered.
func (r *run) detectExisting() (prev *config.RunnerConfig, registered bool, err error) {
	d, o := r.d, r.o
	cfg, err := config.Load(d.Paths)
	switch {
	case err == nil:
		prev = cfg.Runner
	case errors.Is(err, config.ErrNotConfigured):
	case o.Force:
		r.warn("ignoring unreadable config: %v", err)
	default:
		return nil, false, &Error{Msg: err.Error(), Hint: "Re-run with --force to recreate it.", Err: err}
	}

	if prev != nil {
		// D11: one runner per data dir, so a different repo or name needs an
		// uninstall first, force or not.
		if prev.Repo != r.repo.String() {
			return nil, false, &Error{
				Msg:  fmt.Sprintf("OwnCI is already set up for %s as '%s'; this data directory holds one runner", prev.Repo, prev.Name),
				Hint: "Run 'ownci uninstall --unregister' first, then set up " + r.repo.String() + ". (--force cannot switch repositories.)",
			}
		}
		if r.o.Name == "" {
			r.name = prev.Name
		} else if prev.Name != r.name {
			return nil, false, &Error{
				Msg:  fmt.Sprintf("OwnCI is already set up for %s as '%s'; this data directory holds one runner", prev.Repo, prev.Name),
				Hint: "Run 'ownci uninstall --unregister' first to use a different --name.",
			}
		}
	}
	if err := runner.ValidateName(r.name); err != nil {
		return nil, false, err
	}
	registered = runner.Registered(d.Paths.RunnerDir(r.name))
	if registered && !o.Force {
		msg := fmt.Sprintf("OwnCI is already set up for %s as '%s'", r.repo, r.name)
		if prev == nil {
			msg = fmt.Sprintf("a runner named '%s' is already registered in %s", r.name, d.Paths.RunnerDir(r.name))
		}
		return nil, false, &Error{
			Msg:  msg,
			Hint: "Run 'ownci status', or 'ownci uninstall --unregister' and then set up again, or pass --force to re-register in place.",
		}
	}
	// Re-registering or replacing files under a live runner would corrupt it.
	if o.Force {
		running, info, err := supervisor.IsRunning(d.Paths, r.name)
		if err != nil {
			return nil, false, &Error{Msg: "checking whether the runner is running: " + err.Error(), Err: err}
		}
		if running {
			who := ""
			if info.PID > 0 {
				who = fmt.Sprintf(" (supervisor pid %d)", info.PID)
			}
			return nil, false, &Error{
				Msg:  fmt.Sprintf("runner '%s' is running%s; --force would replace its files while it runs", r.name, who),
				Hint: "Stop it with 'ownci stop', then run setup again.",
			}
		}
	}
	return prev, registered, nil
}

// checkRepo enforces the trust gate (D3) and the admin requirement.
func (r *run) checkRepo(ctx context.Context, gh *github.Client) error {
	info, err := gh.GetRepo(ctx, r.repo)
	if err != nil {
		return err
	}
	r.ok("Repository %s (%s)", r.repo, info.Visibility)
	if !info.Private {
		if !r.o.AllowPublicRepo {
			return &Error{
				Msg:  fmt.Sprintf("%s is a public repository: pull requests from forks could run arbitrary code on this machine through a self-hosted runner", r.repo),
				Hint: "Use a private repository, or pass --allow-public-repo if you accept the risk. See " + securityDocs,
			}
		}
		fmt.Fprintf(r.d.Err, "\nWARNING: %s is a public repository.\n"+
			"  Pull requests from forks can run arbitrary code on this machine.\n"+
			"  Strongly recommended: in the repository's Settings → Actions → General, set\n"+
			"  \"Fork pull request workflows from outside collaborators\" to \"Require approval for all outside collaborators\".\n"+
			"  See %s\n\n", r.repo, securityDocs)
	}
	if !info.Admin {
		return &Error{
			Msg:  fmt.Sprintf("the GitHub token has no admin access to %s, which registering a runner requires", r.repo),
			Hint: permissionFix,
		}
	}
	return nil
}

// checkNameFree refuses to clobber a runner of the same name on GitHub.
func (r *run) checkNameFree(ctx context.Context, gh *github.Client) error {
	existing, err := gh.FindRunner(ctx, r.repo, r.name)
	if err != nil {
		return err
	}
	if existing != nil && !r.o.Force {
		return &Error{
			Msg:  fmt.Sprintf("a runner named '%s' already exists on %s", r.name, r.repo),
			Hint: "Choose another --name, or use --force to replace it.",
		}
	}
	return nil
}

// install downloads and extracts the runner, unless a registered runner is
// being re-registered in place. It returns the installed version ("" when it
// could not be read).
func (r *run) install(ctx context.Context, reuse bool, prev *config.RunnerConfig) (string, error) {
	d := r.d
	if reuse {
		v, err := runner.InstalledVersion(ctx, d.Exec, r.dir)
		if err != nil {
			r.warn("could not read the installed runner version: %v", err)
			if prev != nil {
				return prev.InstalledVersion, nil
			}
		}
		r.ok("Reusing the extracted runner %s", orUnknown(v))
		return v, nil
	}
	asset, err := d.AssetFor(d.Platform.RunnerArch)
	if err != nil {
		return "", err
	}
	// A directory without .runner is a previous, unfinished setup: replace it.
	_, statErr := os.Lstat(r.dir)
	res, err := runner.Install(ctx, runner.InstallOptions{
		Paths: d.Paths, Name: r.name, Asset: asset, Exec: d.Exec, HTTP: d.HTTP,
		Force: statErr == nil, Progress: d.Out, Logger: d.Logger,
	})
	if err != nil {
		return "", err
	}
	if res.VersionErr != nil {
		r.warn("runner extracted to %s but its version could not be read: %v", res.Dir, res.VersionErr)
		return "", nil
	}
	if res.InstalledVersion != res.ExpectedVersion {
		r.warn("installed runner reports version %s, expected %s", res.InstalledVersion, res.ExpectedVersion)
	}
	r.ok("Runner %s downloaded, verified and extracted", res.InstalledVersion)
	return res.InstalledVersion, nil
}

// prepareImage pulls the runner image unless it is present (it may be built
// locally) and probes that it can host the runner.
func (r *run) prepareImage(ctx context.Context) error {
	if _, err := docker.EnsureImage(ctx, r.d.Exec, r.image); err != nil {
		return &Error{Msg: err.Error(), Hint: hintOr(err, "Check the image name and your network."), Err: err}
	}
	warnings, err := runnercontainer.Probe(ctx, r.d.Exec, r.image)
	for _, w := range warnings {
		r.warn("runner image %s: %s", r.image, w)
	}
	if err != nil {
		return &Error{Msg: err.Error(), Hint: hintOr(err, "Check the image with 'docker run --rm -it --entrypoint bash "+r.image+"'."), Err: err}
	}
	r.ok("Runner image %s is ready", r.image)
	return nil
}

// reconfigure registers a runner directory that already holds a
// registration. config.sh refuses an existing .runner even with --replace, so
// the old files are moved aside, and put back if registering fails.
func (r *run) reconfigure(configure func() error) error {
	restore, discard, err := runner.SetAsideRegistration(r.dir)
	if err != nil {
		return &Error{Msg: err.Error(), Hint: "Check that " + r.dir + " is writable.", Err: err}
	}
	if err := configure(); err != nil {
		if rerr := restore(); rerr != nil {
			r.warn("restoring the previous registration files failed: %v", rerr)
		}
		return err
	}
	discard()
	return nil
}

// verify reads .runner back and, best effort, confirms GitHub lists the runner.
func (r *run) verify(ctx context.Context, gh *github.Client) error {
	rf, err := runner.ReadRunnerFile(r.dir)
	if err != nil {
		return &Error{Msg: "config.sh succeeded but the runner file is unreadable: " + err.Error(),
			Hint: "Re-run 'ownci setup --force'.", Err: err}
	}
	if rf.AgentName != r.name {
		return &Error{Msg: fmt.Sprintf("the runner registered as '%s', expected '%s'", rf.AgentName, r.name),
			Hint: "Re-run 'ownci setup --force'."}
	}
	found, err := gh.FindRunner(ctx, r.repo, r.name)
	switch {
	case err != nil:
		r.warn("could not confirm the registration on GitHub: %v", err)
	case found == nil:
		r.warn("GitHub does not list runner '%s' yet (it may take a moment)", r.name)
	default:
		r.ok("Runner registered on GitHub (id %d)", found.ID)
	}
	return nil
}

// dryRun prints the plan without writing anything or requesting a
// registration token.
func (r *run) dryRun(repoURL string, labels []string, reuse bool) error {
	out := r.d.Out
	fmt.Fprintln(out, "Dry run: nothing will be changed.")
	if reuse {
		fmt.Fprintf(out, "Would reuse the extracted runner in %s\n", r.dir)
	} else {
		asset, err := r.d.AssetFor(r.d.Platform.RunnerArch)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "Would download runner %s from %s\n  sha256 %s\n  and extract it to %s\n", asset.Version, asset.URL, asset.SHA256, r.dir)
	}
	args := runner.ConfigureArgs(runner.ConfigureOptions{
		URL: repoURL, Token: "", Name: r.name, Labels: labels, Replace: r.o.Force,
	}, true)
	fmt.Fprintf(out, "Would run, in %s:\n  ./config.sh %s\n", r.dir, strings.Join(args, " "))
	if r.mode == config.ModeContainerRunner {
		fmt.Fprintf(out, "Would install the cleanup hook in %s and pull %s\n", filepath.Join(r.dir, ".env"), hooks.CleanupImage)
		fmt.Fprintf(out, "Would pull and probe the runner image %s (mode %s: jobs without 'container:' run in it)\n", r.image, r.mode)
	} else {
		fmt.Fprintf(out, "Would install the cleanup hook, require job containers in %s and pull %s (mode %s)\n", filepath.Join(r.dir, ".env"), hooks.CleanupImage, r.mode)
	}
	fmt.Fprintf(out, "Would save %s\n", r.d.Paths.ConfigFile)
	return nil
}

// installIsolation makes D1 and D10 real: the job-container requirement and
// the cleanup hook in the runner's .env, and the pinned cleanup image. The
// runner is already registered at this point, so a failure tells the user how
// to retry.
func installIsolation(ctx context.Context, d Deps, dir, repo string, p isolation.Policy) error {
	err := isolation.Install(ctx, d.Exec, dir, p)
	if err == nil {
		return nil
	}
	return &Error{
		Msg: "the runner is registered, but its isolation settings could not be installed: " + err.Error(),
		Hint: fmt.Sprintf("Fix the problem (%s), then run 'ownci setup --repo %s --force'.",
			hintOr(err, "see the message above"), repo),
		Err: err,
	}
}

func hintOr(err error, def string) string {
	var h interface{ UserHint() string }
	if errors.As(err, &h) && h.UserHint() != "" {
		return h.UserHint()
	}
	return def
}

func orUnknown(v string) string {
	if v == "" {
		return "(version unknown)"
	}
	return v
}
