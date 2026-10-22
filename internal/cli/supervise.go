package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/ashfaqc137/ownci/internal/buildinfo"
	"github.com/ashfaqc137/ownci/internal/config"
	"github.com/ashfaqc137/ownci/internal/isolation"
	"github.com/ashfaqc137/ownci/internal/runnercontainer"
	"github.com/ashfaqc137/ownci/internal/supervisor"
	"github.com/spf13/cobra"
)

// tuneSupervisor lets tests shorten timings; nil in production.
var tuneSupervisor func(*supervisor.Options)

// beforeLaunch runs before every launch of the runner (restarts and
// container-mode recycles included), in the foreground command and in the
// detached supervisor alike. It removes a leftover runner container, re-asserts
// the isolation policy (a tampered or runner-rewritten .env, a modified hook,
// a missing image are repaired), refuses to go on if it still does not hold,
// and clears leftovers of a job that was cut short.
func beforeLaunch(ctx context.Context, app *App, cfg *config.Config, pol isolation.Policy) error {
	ex := newExecRunner(app.Logger)
	dir := app.Paths.RunnerDir(cfg.Runner.Name)

	if pol.Mode == config.ModeContainerRunner {
		if err := runnercontainer.CheckPath(dir); err != nil {
			return &UserError{Msg: err.Error(), Hint: hintOf(err)}
		}
		// A container left by a killed supervisor holds the GitHub session.
		if err := runnercontainer.Remove(ctx, ex, cfg.Runner.Name); err != nil {
			return &UserError{Msg: err.Error(), Hint: "Run 'docker rm -f " + runnercontainer.ContainerName(cfg.Runner.Name) + "', then start again."}
		}
	}
	fixed, err := isolation.Repair(ctx, ex, dir, pol)
	for _, what := range fixed {
		app.Logger.Warn("isolation repaired", "what", what)
		fmt.Fprintf(app.Err, "ownci: %s\n", what)
	}
	if err != nil {
		return err
	}
	if rep := isolation.Verify(ctx, ex, dir, pol); !rep.EnforcementOK() {
		return &UserError{
			Msg:  "isolation is not enforced, refusing to start: " + strings.Join(rep.Problems(), "; "),
			Hint: "Run 'ownci setup --repo " + cfg.Runner.Repo + " --force' to reinstall it.",
		}
	}
	return isolation.RunCleanup(ctx, ex, dir, pol.CleanupImage)
}

// runSupervisor runs the supervisor for the configured runner until it stops
// or fails for good. image overrides the configured runner image for this
// session (container-runner mode, `start --image`).
func runSupervisor(ctx context.Context, app *App, cfg *config.Config, mode string, echo io.Writer, image string) (supervisor.State, error) {
	pol := isolation.PolicyFor(cfg.Runner, image)
	o := supervisor.Options{
		Paths:        app.Paths,
		Name:         cfg.Runner.Name,
		Mode:         mode,
		OwnciVersion: buildinfo.Version,
		Echo:         echo,
		Logger:       app.Logger,
		BeforeLaunch: func(ctx context.Context) error { return beforeLaunch(ctx, app, cfg, pol) },
	}
	if pol.Mode == config.ModeContainerRunner {
		name, ex := cfg.Runner.Name, newExecRunner(app.Logger)
		o.Command = append([]string{"docker"}, runnercontainer.Args(runnercontainer.Spec{
			Runner: name, RunnerDir: app.Paths.RunnerDir(name), Image: pol.RunnerImage,
			UID: os.Getuid(), GID: os.Getgid(),
		})...)
		o.AfterExit = func(ctx context.Context) {
			if err := runnercontainer.Remove(ctx, ex, name); err != nil {
				app.Logger.Warn("removing the runner container failed", "err", err)
			}
		}
		o.LaunchFailed = runnercontainer.LaunchFailed
		o.RecycleAfterJob = true
	}
	if tuneSupervisor != nil {
		tuneSupervisor(&o)
	}
	return supervisor.Run(ctx, o)
}

// alreadyRunningError builds the "already running" message from the lock and
// the (informational) pidfile.
func alreadyRunningError(info supervisor.PidInfo) *UserError {
	msg := "the runner is already running"
	if info.PID > 0 {
		msg = fmt.Sprintf("the runner is already running (pid %d, since %s)", info.PID, fmtTime(info.StartedAt))
	}
	return &UserError{Msg: msg + " — see 'ownci status'", Hint: "Stop it first with 'ownci stop'."}
}

// failureError turns the final state of a supervisor into a command result:
// nil after a requested stop, a UserError for a terminal failure.
func failureError(name string, st supervisor.State, err error) error {
	if errors.Is(err, supervisor.ErrAlreadyRunning) {
		return alreadyRunningError(supervisor.PidInfo{})
	}
	if err != nil {
		return err
	}
	if !st.Phase.Failed() {
		return nil
	}
	return &UserError{Msg: failureMessage(name, st), Hint: st.Phase.Hint()}
}

func failureMessage(name string, st supervisor.State) string {
	switch st.Phase {
	case supervisor.PhaseCrashed:
		if st.ExitCode == nil { // never got as far as running the runner
			return fmt.Sprintf("the runner '%s' could not be started: %s", name, lastLine(st.LastError))
		}
		if strings.HasPrefix(st.LastError, supervisor.LaunchFailedPrefix) { // docker could not start the container
			return fmt.Sprintf("the runner '%s' could not be started (exit code %d): %s", name, *st.ExitCode, lastLine(st.LastError))
		}
		msg := fmt.Sprintf("the runner '%s' keeps crashing and was not restarted again", name)
		if st.ExitCode != nil {
			msg += fmt.Sprintf(" (last exit code %d)", *st.ExitCode)
		}
		return msg
	case supervisor.PhaseVersionDeprecated:
		return fmt.Sprintf("the runner '%s' exited because its version is deprecated (exit code 7)", name)
	case supervisor.PhaseRegistrationLost:
		return fmt.Sprintf("the registration of runner '%s' no longer exists on GitHub", name)
	case supervisor.PhaseDuplicateSession:
		return fmt.Sprintf("another process is already connected to GitHub as runner '%s'", name)
	}
	return fmt.Sprintf("the runner '%s' ended in phase %s", name, st.Phase)
}

// newSuperviseCmd is the hidden entry point `start --detach` re-executes.
func newSuperviseCmd() *cobra.Command {
	var name, image string
	cmd := &cobra.Command{
		Use:    "__supervise",
		Short:  "internal: run the runner supervisor (used by 'start --detach')",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			app := appFrom(cmd)
			cfg, err := loadConfigFor(app)
			if err != nil {
				return err
			}
			if cfg.Runner.Name != name {
				return &UserError{Msg: fmt.Sprintf("--name %q does not match the configured runner %q", name, cfg.Runner.Name)}
			}
			st, err := runSupervisor(cmd.Context(), app, cfg, supervisor.ModeDetached, nil, image)
			if err := failureError(name, st, err); err != nil {
				return err
			}
			fmt.Fprintf(app.Out, "runner '%s' stopped\n", name)
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "runner name")
	cmd.Flags().StringVar(&image, "image", "", "runner image for this session (container-runner mode)")
	_ = cmd.MarkFlagRequired("name")
	return cmd
}

// loadConfigFor loads the config or explains how to create it.
func loadConfigFor(app *App) (*config.Config, error) {
	cfg, err := config.Load(app.Paths)
	if errors.Is(err, config.ErrNotConfigured) {
		return nil, &UserError{
			Msg:  "OwnCI is not set up yet",
			Hint: "Run 'ownci setup --repo owner/name' first.",
		}
	}
	if err != nil {
		return nil, &UserError{Msg: err.Error(), Hint: "Re-run 'ownci setup --force' to recreate the configuration."}
	}
	return cfg, nil
}

// fmtTime renders t in local time: the clock for today, the date otherwise.
func fmtTime(t time.Time) string {
	if t.IsZero() {
		return "unknown"
	}
	t = t.Local()
	if time.Since(t) < 24*time.Hour && t.Day() == time.Now().Day() {
		return t.Format("15:04:05")
	}
	return t.Format("2006-01-02 15:04:05")
}

// lastLine is the last non-empty line of s, shortened for a one-line display.
func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	l := strings.TrimSpace(lines[len(lines)-1])
	if len(l) > 200 {
		l = l[:200] + "…"
	}
	return l
}
