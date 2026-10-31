package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ashfaqc137/ownci/internal/buildinfo"
	"github.com/ashfaqc137/ownci/internal/config"
	"github.com/ashfaqc137/ownci/internal/docker"
	"github.com/ashfaqc137/ownci/internal/github"
	"github.com/ashfaqc137/ownci/internal/isolation"
	"github.com/ashfaqc137/ownci/internal/runner"
	"github.com/ashfaqc137/ownci/internal/runnercontainer"
	"github.com/ashfaqc137/ownci/internal/supervisor"
	"github.com/spf13/cobra"
)

// apiBaseURL is a test seam over github.APIBaseURL.
var apiBaseURL = github.APIBaseURL

// statusReport is everything `ownci status` knows; --json prints it as is.
type statusReport struct {
	Ownci struct {
		Version string `json:"version"`
		Commit  string `json:"commit"`
	} `json:"ownci"`
	DataDir      string `json:"dataDir"`
	Configured   bool   `json:"configured"`
	ConfigError  string `json:"configError,omitempty"`
	RunnerDirSet bool   `json:"runnerDirExists"`

	Runner *statusRunner `json:"runner,omitempty"`

	Installed      bool   `json:"installed"`
	RunnerVersion  string `json:"runnerVersion,omitempty"`
	VersionSource  string `json:"versionSource,omitempty"` // state | listener | config
	Registered     bool   `json:"registered"`
	RegisteredNote string `json:"registeredNote,omitempty"`

	Process struct {
		Running    bool       `json:"running"`
		PID        int        `json:"pid,omitempty"`
		Mode       string     `json:"mode,omitempty"`
		Since      *time.Time `json:"since,omitempty"`
		Restarts   int        `json:"restarts"`
		Unexpected bool       `json:"stoppedUnexpectedly,omitempty"`
	} `json:"process"`

	State *supervisor.State `json:"state,omitempty"`
	// StateTrusted is false when the state file cannot be tied to the current
	// supervisor (stale file from an earlier run).
	StateTrusted bool `json:"stateTrusted"`

	GitHub *githubStatus `json:"github,omitempty"`
	Docker *dockerStatus `json:"docker,omitempty"`

	Isolation *isolation.Report `json:"isolation,omitempty"`

	// Mode is the execution mode (host or container-runner); Container is
	// Docker's view of the runner container in container-runner mode.
	Mode      string           `json:"mode,omitempty"`
	Image     string           `json:"image,omitempty"`
	Container *containerStatus `json:"container,omitempty"`

	LastError string `json:"lastError,omitempty"`
}

type statusRunner struct {
	Name   string   `json:"name"`
	Repo   string   `json:"repo"`
	Labels []string `json:"labels"`
	Dir    string   `json:"dir"`
}

type githubStatus struct {
	Checked bool   `json:"checked"`
	Skipped string `json:"skipped,omitempty"`
	Found   bool   `json:"found"`
	Online  bool   `json:"online"`
	Busy    bool   `json:"busy"`
	Error   string `json:"error,omitempty"`
	Warning string `json:"warning,omitempty"`
	// Visibility is the repository's visibility on GitHub ("" when unknown).
	Visibility string `json:"visibility,omitempty"`
}

type containerStatus struct {
	Name  string `json:"name"`
	State string `json:"state"` // running, exited, … or "none"
	Image string `json:"image,omitempty"`
	Error string `json:"error,omitempty"`
	// ForeignDataDir is set when the container name is taken by the runner of
	// another data directory (State is then "none" for this one).
	ForeignDataDir string `json:"foreignDataDir,omitempty"`
}

type dockerStatus struct {
	OK      bool   `json:"ok"`
	Version string `json:"version,omitempty"`
	OS      string `json:"os,omitempty"`
	Detail  string `json:"detail,omitempty"`
}

func newStatusCmd() *cobra.Command {
	var (
		asJSON bool
		remote bool
	)
	cmd := &cobra.Command{
		Use:         "status",
		Annotations: map[string]string{noLayoutAnnotation: "true"},
		Short:       "Show installation, configuration and runner state",
		Long: `Show the actual local state of the runner.

"running" means a supervisor holds the runner's lock; installed files alone
never count. With --remote the runner's status on GitHub is queried as well
(needs a token, like setup).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			app := appFrom(cmd)
			rep := gatherStatus(cmd.Context(), app, remote)
			if asJSON {
				enc := json.NewEncoder(app.Out)
				enc.SetIndent("", "  ")
				return enc.Encode(rep)
			}
			renderStatus(app.Out, rep)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the status as JSON")
	cmd.Flags().BoolVar(&remote, "remote", false, "also ask GitHub whether the runner is online")
	return cmd
}

func gatherStatus(ctx context.Context, app *App, remote bool) *statusReport {
	rep := &statusReport{DataDir: app.Paths.Root}
	rep.Ownci.Version, rep.Ownci.Commit = buildinfo.Version, buildinfo.Commit
	ex := newExecRunner(app.Logger)

	cfg, err := config.Load(app.Paths)
	switch {
	case err == nil:
		rep.Configured = true
	case errors.Is(err, config.ErrNotConfigured):
		rep.RunnerDirSet = anyRunnerDir(app.Paths)
		return rep
	default:
		rep.ConfigError = err.Error()
		rep.RunnerDirSet = anyRunnerDir(app.Paths)
		return rep
	}

	rc := cfg.Runner
	dir := app.Paths.RunnerDir(rc.Name)
	rep.Runner = &statusRunner{rc.Name, rc.Repo, rc.Labels, dir}
	_, statErr := os.Stat(dir)
	rep.RunnerDirSet = statErr == nil
	_, listenerErr := os.Stat(filepath.Join(dir, "bin", "Runner.Listener"))
	rep.Installed = listenerErr == nil

	// Process: the lock decides.
	running, info, err := supervisor.IsRunning(app.Paths, rc.Name)
	if err != nil {
		app.Logger.Warn("checking the runner lock failed", "err", err)
	}
	st, err := supervisor.ReadState(app.Paths.StateFile(rc.Name))
	if err != nil {
		app.Logger.Warn("reading runner state failed", "err", err)
	}
	rep.State = st
	rep.Process.Running = running
	if running {
		rep.Process.PID, rep.Process.Mode = info.PID, info.Mode
		if !info.StartedAt.IsZero() {
			t := info.StartedAt
			rep.Process.Since = &t
		}
		// The state describes this supervisor only if the pids agree.
		rep.StateTrusted = st != nil && (info.PID == 0 || st.SupervisorPID == info.PID)
		if rep.StateTrusted {
			rep.Process.Restarts = st.Restarts
		}
	} else if st != nil && st.Phase.Active() {
		rep.Process.Unexpected = true
	}
	if st != nil && !running && st.Phase.Failed() {
		rep.LastError = st.LastError
	} else if st != nil && st.LastError != "" && (!running || rep.StateTrusted) {
		rep.LastError = st.LastError
	}

	// Installed version: the running version beats the one from install time.
	switch {
	case st != nil && st.RunnerVersion != "":
		rep.RunnerVersion, rep.VersionSource = st.RunnerVersion, "state"
	case rep.Installed:
		vctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		v, verr := runner.InstalledVersion(vctx, ex, dir)
		cancel()
		if verr == nil && v != "" {
			rep.RunnerVersion, rep.VersionSource = v, "listener"
		}
	}
	if rep.RunnerVersion == "" && rc.InstalledVersion != "" {
		rep.RunnerVersion, rep.VersionSource = rc.InstalledVersion, "config"
	}

	// Registration.
	switch {
	case !runner.Registered(dir):
		rep.RegisteredNote = "no .runner file"
	default:
		rf, rerr := runner.ReadRunnerFile(dir)
		switch {
		case rerr != nil:
			rep.RegisteredNote = "unreadable .runner: " + rerr.Error()
		case rf.AgentName != rc.Name:
			rep.RegisteredNote = fmt.Sprintf(".runner agentName is '%s', expected '%s'", rf.AgentName, rc.Name)
		default:
			rep.Registered = true
		}
	}

	dst := docker.Check(ctx, ex)
	rep.Docker = &dockerStatus{OK: dst.OK(), Version: dst.ServerVersion, OS: dst.ServerOS}
	if dst.Err != nil {
		rep.Docker.Detail = dst.Err.Error()
	}

	pol := isolation.PolicyFor(rc, "")
	rep.Mode, rep.Image = pol.Mode, pol.RunnerImage
	if rep.RunnerDirSet {
		iso := isolation.Verify(ctx, ex, dir, pol)
		rep.Isolation = &iso
	}
	if pol.Mode == config.ModeContainerRunner && dst.OK() {
		cs := &containerStatus{Name: runnercontainer.ContainerName(rc.Name)}
		info, err := runnercontainer.Inspect(ctx, ex, rc.Name)
		switch {
		case errors.Is(err, runnercontainer.ErrNoContainer):
			cs.State = "none"
		case err != nil:
			cs.Error = err.Error()
		case !info.Owned(app.Paths.Root):
			cs.State, cs.ForeignDataDir = "none", info.DataDir
		default:
			cs.State, cs.Image = info.State, info.Image
		}
		rep.Container = cs
	}

	if remote {
		rep.GitHub = remoteStatus(ctx, app, cfg, rep)
	}
	return rep
}

func remoteStatus(ctx context.Context, app *App, cfg *config.Config, rep *statusReport) *githubStatus {
	gs := &githubStatus{}
	ex := newExecRunner(app.Logger)
	token, src, err := github.ResolveToken(ctx, os.Getenv, ex)
	if err != nil {
		gs.Skipped = "no token"
		return gs
	}
	repo, err := github.ParseRepo(cfg.Runner.Repo)
	if err != nil {
		gs.Error = err.Error()
		return gs
	}
	gh := github.NewClient(token, github.WithBaseURL(apiBaseURL()), github.WithHTTPClient(newHTTPClient()),
		github.WithLogger(app.Logger), github.WithTokenSource(src))
	if info, err := gh.GetRepo(ctx, repo); err == nil {
		gs.Visibility = info.Visibility
		if !info.Private && !cfg.Runner.AllowPublicRepo {
			defer func() {
				gs.Warning = joinWarnings(gs.Warning, repo.String()+" is now public: pull requests from forks could run code on this machine, and 'ownci start' will refuse to run")
			}()
		}
	}
	found, err := gh.FindRunner(ctx, repo, cfg.Runner.Name)
	gs.Checked = true
	if err != nil {
		gs.Error = err.Error()
		return gs
	}
	if found == nil {
		return gs
	}
	gs.Found, gs.Online, gs.Busy = true, found.Status == "online", found.Busy
	localUp := rep.Process.Running && rep.StateTrusted && rep.State != nil &&
		(rep.State.Phase == supervisor.PhaseListening || rep.State.Phase == supervisor.PhaseBusy)
	switch {
	case localUp && !gs.Online:
		gs.Warning = "locally listening, but GitHub shows the runner offline: GitHub hasn't seen it yet, or there is a connectivity issue"
	case !rep.Process.Running && gs.Online:
		gs.Warning = "not running locally, but GitHub shows the runner online: another process may be using this registration"
	}
	return gs
}

func joinWarnings(a, b string) string {
	if a == "" {
		return b
	}
	return a + "; " + b
}

func anyRunnerDir(p config.Paths) bool {
	entries, err := os.ReadDir(p.RunnersDir)
	return err == nil && len(entries) > 0
}

func renderStatus(w io.Writer, r *statusReport) {
	row := func(label, format string, a ...any) { fmt.Fprintf(w, "%-12s %s\n", label, fmt.Sprintf(format, a...)) }
	row("OwnCI", "%s (commit %s)", r.Ownci.Version, r.Ownci.Commit)

	if r.Runner == nil {
		switch {
		case r.ConfigError != "":
			row("Configured", "no (config unreadable: %s)", r.ConfigError)
			fmt.Fprintln(w, "Hint: re-run 'ownci setup --repo <owner/name> --force' to recreate the configuration.")
		default:
			row("Configured", "no")
			fmt.Fprintln(w, "Hint: run 'ownci setup --repo owner/name' to set up a runner.")
		}
		row("Data dir", "%s", r.DataDir)
		row("Runner dir", "%s", yesNo(r.RunnerDirSet, "present", "none"))
		return
	}

	row("Runner", "%s  →  %s   labels: %s", r.Runner.Name, r.Runner.Repo, strings.Join(r.Runner.Labels, ","))
	row("Mode", "%s", modeLine(r))
	if r.Installed {
		row("Installed", "yes  (runner %s at %s)", orDash(r.RunnerVersion), r.Runner.Dir)
	} else {
		row("Installed", "no  (%s is missing bin/Runner.Listener)", r.Runner.Dir)
	}
	if r.Registered {
		row("Configured", "yes  (.runner agentName matches)")
	} else {
		row("Configured", "no  (%s)", r.RegisteredNote)
	}

	renderProcess(row, r)
	renderState(row, r)
	if r.State != nil && r.State.LastJob != nil && (!r.Process.Running || r.StateTrusted) {
		j := r.State.LastJob
		row("Last job", "%s → %s at %s", j.Name, j.Result, fmtTime(j.FinishedAt))
	}
	if r.Isolation != nil {
		row("Isolation", "%s", isolationLine(r.Isolation))
	}
	if r.GitHub != nil {
		row("GitHub", "%s", githubLine(r.GitHub))
		if r.GitHub.Warning != "" {
			fmt.Fprintf(w, "Warning: %s\n", r.GitHub.Warning)
		}
	}
	if r.Docker != nil {
		if r.Docker.OK {
			row("Docker", "ok (%s, %s)", r.Docker.Version, r.Docker.OS)
		} else {
			row("Docker", "NOT OK: %s", r.Docker.Detail)
		}
	}
	if r.LastError == "" {
		row("Last error", "—")
	} else {
		row("Last error", "%s", lastLine(r.LastError))
	}
	if r.State != nil && !r.Process.Running && r.State.Phase.Failed() {
		fmt.Fprintf(w, "Hint: %s\n", r.State.Phase.Hint())
	}
}

func renderProcess(row func(string, string, ...any), r *statusReport) {
	p := r.Process
	switch {
	case p.Running:
		var parts []string
		if p.PID > 0 {
			parts = append(parts, fmt.Sprintf("supervisor pid %d", p.PID))
		}
		if p.Mode != "" {
			parts = append(parts, p.Mode)
		}
		if p.Since != nil {
			parts = append(parts, "since "+fmtTime(*p.Since))
		}
		parts = append(parts, fmt.Sprintf("restarts %d", p.Restarts))
		row("Process", "running  (%s)", strings.Join(parts, ", "))
	case p.Unexpected:
		row("Process", "stopped (unexpectedly; last state: %s at %s)", r.State.Phase, fmtTime(r.State.Since))
	default:
		row("Process", "not running")
	}
}

func renderState(row func(string, string, ...any), r *statusReport) {
	st := r.State
	switch {
	case r.Process.Running && !r.StateTrusted:
		row("State", "unknown  (the supervisor has not recorded its state yet)")
	case r.Process.Running:
		if st.Phase == supervisor.PhaseBusy {
			row("State", "busy: running job %q  (since %s)", st.CurrentJob, fmtTime(st.Since))
		} else {
			row("State", "%s  (since %s)", st.Phase, fmtTime(st.Since))
		}
	case st != nil && !st.Phase.Active():
		row("State", "%s  (since %s)", st.Phase, fmtTime(st.Since))
	default:
		row("State", "—")
	}
}

func modeLine(r *statusReport) string {
	if r.Mode != config.ModeContainerRunner {
		return config.ModeHost + "  (runner on this machine; jobs must declare container:)"
	}
	line := fmt.Sprintf("%s  (image %s)", r.Mode, r.Image)
	if c := r.Container; c != nil {
		switch {
		case c.Error != "":
			line += fmt.Sprintf("   container %s: unknown (%s)", c.Name, c.Error)
		case c.ForeignDataDir != "":
			line += fmt.Sprintf("   container %s: in use by the runner of %s (use another --name)", c.Name, c.ForeignDataDir)
		case c.Image != "" && c.Image != r.Image:
			line += fmt.Sprintf("   container %s: %s (image %s)", c.Name, c.State, c.Image)
		default:
			line += fmt.Sprintf("   container %s: %s", c.Name, c.State)
		}
	}
	return line
}

func isolationLine(i *isolation.Report) string {
	mark := func(ok bool) string { return yesNo(ok, "✓", "✗") }
	last := i.LastCleanup.Result
	if last != "never" {
		last += " at " + fmtTime(i.LastCleanup.Time)
		if i.LastCleanup.Result == "failed" && i.LastCleanup.Detail != "" {
			last += " (" + i.LastCleanup.Detail + ")"
		}
	}
	if i.JobContainerRequired == nil {
		runnerImage := i.RunnerImage != nil && i.RunnerImage.OK
		return fmt.Sprintf("runner image %s   cleanup hook %s   cleanup image %s   last cleanup: %s",
			mark(runnerImage), mark(i.CleanupHook.OK), mark(i.CleanupImage.OK), last)
	}
	return fmt.Sprintf("container jobs required %s   cleanup hook %s   cleanup image %s   last cleanup: %s",
		mark(i.JobContainerRequired.OK), mark(i.CleanupHook.OK), mark(i.CleanupImage.OK), last)
}

func githubLine(g *githubStatus) string {
	switch {
	case g.Skipped != "":
		return "skipped (" + g.Skipped + ")"
	case g.Error != "":
		return "error: " + g.Error
	case !g.Found:
		return "runner not found on GitHub"
	case !g.Online:
		return "offline"
	case g.Busy:
		return "online, busy"
	}
	return "online, idle"
}

func yesNo(b bool, yes, no string) string {
	if b {
		return yes
	}
	return no
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
