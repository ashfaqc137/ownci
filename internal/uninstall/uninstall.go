// Package uninstall implements `ownci uninstall`: stop the runner, optionally
// unregister it from GitHub, optionally delete OwnCI's own files, and say
// exactly what is left. It prints through the writers it is given; flags and
// exit codes stay in cli.
package uninstall

import (
	"bufio"
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
	"github.com/ashfaqc137/ownci/internal/execx"
	"github.com/ashfaqc137/ownci/internal/github"
	"github.com/ashfaqc137/ownci/internal/runner"
	"github.com/ashfaqc137/ownci/internal/runnercontainer"
	"github.com/ashfaqc137/ownci/internal/supervisor"
)

// Error is an uninstall failure with an actionable hint.
type Error struct {
	Msg, Hint string
	Err       error
	Usage     bool // bad input: the cli reports exit code 2
}

func (e *Error) Error() string    { return e.Msg }
func (e *Error) Unwrap() error    { return e.Err }
func (e *Error) UserHint() string { return e.Hint }

// Options are the flags of `ownci uninstall`.
type Options struct {
	Name       string // "" = the configured runner
	Unregister bool
	Purge      bool
	LocalOnly  bool
	Yes        bool
}

// Deps are the collaborators, injected so tests need no network or Docker.
type Deps struct {
	Paths       config.Paths
	Exec        execx.Runner
	HTTP        *http.Client
	Logger      *slog.Logger
	In          io.Reader
	Out, Err    io.Writer
	Getenv      func(string) string
	Home        string
	IsTerminal  func() bool // stdin is interactive; nil = false
	APIBaseURL  string
	WebBaseURL  string
	StopTimeout time.Duration // default 45s
}

type run struct {
	d    Deps
	o    Options
	name string
	dir  string
	cfg  *config.RunnerConfig // nil when config.json is missing or unreadable
	repo *github.Repo         // nil when it cannot be determined

	registered bool // .runner present: a registration probably exists on GitHub
	sum        summary
}

// Run executes the uninstall flow (tasks/t09-uninstall-logs.md).
func Run(ctx context.Context, d Deps, o Options) error {
	if d.Logger == nil {
		d.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if d.StopTimeout <= 0 {
		d.StopTimeout = 45 * time.Second
	}
	if o.Unregister && o.LocalOnly {
		return &Error{Msg: "--unregister and --local-only contradict each other", Usage: true,
			Hint: "--local-only skips GitHub entirely; drop one of the flags."}
	}
	r := &run{d: d, o: o}

	// 1. Resolve the target.
	found, err := r.resolve()
	if err != nil {
		return err
	}
	if !found {
		fmt.Fprintf(d.Out, "Nothing to uninstall in %s\n", d.Paths.Root)
		return nil
	}
	// Refuse an unsafe purge before anything irreversible happens.
	if o.Purge {
		if err := checkPurgeSafe(d.Paths, d.Home); err != nil {
			return err
		}
	}

	// 2. Confirm.
	if err := r.confirm(ctx); err != nil {
		return err
	}

	// 3. Stop.
	if err := r.stop(ctx); err != nil {
		return err
	}

	// 4. Unregister.
	var unregErr error
	if o.Unregister {
		unregErr = r.unregister(ctx)
		if unregErr != nil && !o.Purge {
			r.sum.github = "still registered (unregistering failed)"
			r.printSummary()
			return unregErr // keep every file so the user can retry
		}
	}

	// 5. Purge.
	if o.Purge {
		r.sum.files = r.purge(ctx)
	}

	// 6. Summary.
	r.finishSummary(unregErr)
	r.printSummary()
	if unregErr != nil {
		return unregErr
	}
	return nil
}

// resolve finds the runner to remove: the configured one, or --name for a
// directory whose config is missing or broken.
func (r *run) resolve() (bool, error) {
	d, o := r.d, r.o
	cfg, err := config.Load(d.Paths)
	switch {
	case err == nil:
		r.cfg = cfg.Runner
	case errors.Is(err, config.ErrNotConfigured):
	default:
		fmt.Fprintf(d.Err, "Warning: the configuration is unreadable (%v); relying on --name and the files on disk\n", err)
	}

	r.name = o.Name
	if r.name == "" && r.cfg != nil {
		r.name = r.cfg.Name
	}
	if r.name == "" {
		names := runnerDirs(d.Paths)
		switch len(names) {
		case 0:
			return false, nil
		case 1:
			r.name = names[0]
		default:
			return false, &Error{Msg: "several runner directories exist and the configuration does not name one",
				Hint: "Pass --name <runner> (one of: " + strings.Join(names, ", ") + ")."}
		}
	}
	if err := runner.ValidateName(r.name); err != nil {
		return false, &Error{Msg: err.Error(), Hint: hintOf(err), Usage: true}
	}
	r.dir = d.Paths.RunnerDir(r.name)
	if r.cfg != nil && r.cfg.Name != r.name {
		r.cfg = nil // --name picked another runner: the config describes a different one
	}

	_, statErr := os.Lstat(r.dir)
	dirExists := statErr == nil
	if !dirExists && r.cfg == nil {
		return false, nil
	}
	r.registered = runner.Registered(r.dir)
	r.repo = r.findRepo()
	return true, nil
}

func (r *run) findRepo() *github.Repo {
	try := func(s string) *github.Repo {
		if s == "" {
			return nil
		}
		repo, err := github.ParseRepo(s)
		if err != nil {
			return nil
		}
		return &repo
	}
	if r.cfg != nil {
		if repo := try(r.cfg.Repo); repo != nil {
			return repo
		}
	}
	if r.registered {
		if rf, err := runner.ReadRunnerFile(r.dir); err == nil {
			return try(rf.GitHubURL)
		}
	}
	return nil
}

func (r *run) settingsURL() string {
	if r.repo == nil {
		return r.d.WebBaseURL + "/<owner>/<repo>/settings/actions/runners"
	}
	return fmt.Sprintf("%s/%s/settings/actions/runners", r.d.WebBaseURL, r.repo)
}

// confirm prints the plan and asks, unless --yes. Without a terminal and
// without --yes it refuses instead of guessing.
func (r *run) confirm(_ context.Context) error {
	d, o := r.d, r.o
	fmt.Fprintf(d.Out, "Uninstall plan for runner '%s':\n", r.name)
	fmt.Fprintln(d.Out, "  - stop the runner if it is running")
	switch {
	case o.Unregister:
		fmt.Fprintf(d.Out, "  - unregister it from %s on GitHub\n", orUnknown(r.repo))
	case o.LocalOnly:
		fmt.Fprintln(d.Out, "  - leave GitHub alone (--local-only)")
	}
	if o.Purge {
		fmt.Fprintf(d.Out, "  - delete OwnCI's files for this runner under %s\n", d.Paths.Root)
	} else {
		fmt.Fprintln(d.Out, "  - keep all files (add --purge to delete them)")
	}
	if o.Purge && !o.Unregister && !o.LocalOnly && r.registered {
		fmt.Fprintf(d.Err, "\nWARNING: the runner is still registered on GitHub. Without --unregister the registration\n"+
			"  stays there as an offline runner. Remove it later at:\n  %s\n\n", r.settingsURL())
	}
	if o.Yes {
		return nil
	}
	if d.IsTerminal == nil || !d.IsTerminal() {
		return &Error{
			Msg:   "cannot ask for confirmation: standard input is not a terminal",
			Hint:  "Pass --yes to proceed without asking.",
			Usage: true,
		}
	}
	fmt.Fprint(d.Out, "Proceed? [y/N] ")
	line, _ := bufio.NewReader(d.In).ReadString('\n')
	if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
		return &Error{Msg: "aborted: nothing was changed", Hint: "Run again and answer 'y' to proceed."}
	}
	return nil
}

func (r *run) stop(ctx context.Context) error {
	d := r.d
	running, _, err := supervisor.IsRunning(d.Paths, r.name)
	if err != nil {
		return &Error{Msg: "checking whether the runner is running: " + err.Error(), Err: err}
	}
	if !running {
		r.sum.process = "was not running"
		r.removeContainer(ctx)
		return nil
	}
	err = supervisor.Stop(ctx, d.Paths, r.name, supervisor.StopOptions{Timeout: d.StopTimeout, Force: true})
	if err != nil && !errors.Is(err, supervisor.ErrNotRunning) {
		return &Error{Msg: "stopping the runner: " + err.Error(), Err: err,
			Hint: "Stop it by hand (see 'ownci status' for the pid), then run uninstall again."}
	}
	fmt.Fprintf(d.Out, "✓ Runner '%s' stopped\n", r.name)
	r.sum.process = "stopped"
	r.removeContainer(ctx)
	return nil
}

// removeContainer removes the runner container (container-runner mode) if
// one is left, e.g. by a killed supervisor. Without a readable config the mode
// is unknown, so it looks anyway.
func (r *run) removeContainer(ctx context.Context) {
	if r.cfg != nil && r.cfg.EffectiveMode() != config.ModeContainerRunner {
		return
	}
	if _, err := r.d.Exec.LookPath("docker"); err != nil {
		return
	}
	info, err := runnercontainer.Inspect(ctx, r.d.Exec, r.name)
	if err != nil {
		return
	}
	if !info.Owned(r.d.Paths.Root) {
		return // another data directory's runner of the same name: not ours
	}
	if err := runnercontainer.Remove(ctx, r.d.Exec, r.name, r.d.Paths.Root); err != nil {
		fmt.Fprintf(r.d.Err, "Warning: %v (remove it with 'docker rm -f %s')\n", err, runnercontainer.ContainerName(r.name))
		return
	}
	fmt.Fprintf(r.d.Out, "✓ Removed the runner container %s\n", runnercontainer.ContainerName(r.name))
}

// unregister removes the registration: config.sh remove first, the REST API
// as a fallback (a corrupt or already-removed local registration).
func (r *run) unregister(ctx context.Context) error {
	d := r.d
	if r.repo == nil {
		return &Error{
			Msg:  "cannot tell which repository the runner is registered for",
			Hint: "Remove it by hand at " + r.settingsURL() + ", then use --local-only.",
		}
	}
	token, src, err := github.ResolveToken(ctx, d.Getenv, d.Exec)
	if err != nil {
		return &Error{
			Msg:  "no GitHub token to unregister the runner with",
			Hint: "Set GITHUB_TOKEN (admin on " + r.repo.String() + ") or run 'gh auth login'. Or use --local-only and remove the runner by hand at " + r.settingsURL() + ".",
			Err:  err,
		}
	}
	gh := github.NewClient(token, github.WithBaseURL(d.APIBaseURL), github.WithHTTPClient(d.HTTP),
		github.WithLogger(d.Logger), github.WithTokenSource(src))

	rm, err := gh.RemoveToken(ctx, *r.repo)
	if err != nil {
		return err
	}
	if _, statErr := os.Stat(filepath.Join(r.dir, "config.sh")); statErr == nil {
		if err := runner.Remove(ctx, d.Exec, r.dir, rm.Token); err == nil {
			fmt.Fprintf(d.Out, "✓ Runner unregistered from %s\n", r.repo)
			r.sum.github = "unregistered"
			return nil
		} else {
			d.Logger.Warn("config.sh remove failed; falling back to the API", "err", err)
			fmt.Fprintf(d.Err, "Note: config.sh remove did not work (%s); removing the registration through the API\n", firstLine(err.Error()))
		}
	}

	found, err := gh.FindRunner(ctx, *r.repo, r.name)
	if err != nil {
		return r.unregisterFailed(err)
	}
	if found == nil {
		fmt.Fprintln(d.Out, "✓ Runner was not registered on GitHub (nothing to remove)")
		r.sum.github = "not registered (nothing to remove)"
		return nil
	}
	if err := gh.DeleteRunner(ctx, *r.repo, found.ID); err != nil {
		return r.unregisterFailed(err)
	}
	fmt.Fprintf(d.Out, "✓ Runner unregistered from %s\n", r.repo)
	r.sum.github = "unregistered"
	return nil
}

func (r *run) unregisterFailed(err error) error {
	return &Error{
		Msg:  "could not unregister the runner from GitHub: " + err.Error(),
		Hint: "Retry, or remove it by hand at " + r.settingsURL() + " and re-run with --local-only.",
		Err:  err,
	}
}

func runnerDirs(p config.Paths) []string {
	es, err := os.ReadDir(p.RunnersDir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range es {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names
}

func orUnknown(r *github.Repo) string {
	if r == nil {
		return "its repository"
	}
	return r.String()
}

func firstLine(s string) string {
	l, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return l
}

func hintOf(err error) string {
	var h interface{ UserHint() string }
	if errors.As(err, &h) {
		return h.UserHint()
	}
	return ""
}
