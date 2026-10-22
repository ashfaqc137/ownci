package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/ashfaqc137/ownci/internal/config"
	"github.com/ashfaqc137/ownci/internal/docker"
	"github.com/ashfaqc137/ownci/internal/runner"
	"github.com/ashfaqc137/ownci/internal/supervisor"
	"github.com/spf13/cobra"
)

// executablePath is a test seam: tests re-exec the test binary instead.
var executablePath = os.Executable

const wslNote = "Note: the runner stops if WSL shuts down or the machine sleeps."

func newStartCmd() *cobra.Command {
	var (
		detach bool
		wait   time.Duration
		image  string
	)
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start contributing compute: run the registered runner",
		Long: `Start the registered runner and keep it supervised.

By default the runner runs in the foreground: its output is shown and Ctrl-C
stops it gracefully. With --detach it runs in the background; stop it with
'ownci stop'.

In container-runner mode (setup --mode container-runner) --image runs this
session with another runner image; the image needs the runner's dependencies.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			app := appFrom(cmd)
			cfg, err := checkStartPreconditions(cmd.Context(), app)
			if err != nil {
				return err
			}
			if image != "" && cfg.Runner.EffectiveMode() != config.ModeContainerRunner {
				return &UserError{Msg: "--image needs a runner set up with --mode " + config.ModeContainerRunner, Code: exitUsage,
					Hint: "In host mode each job names its image with 'container:'. Switch with 'ownci setup --repo " + cfg.Runner.Repo + " --force --mode " + config.ModeContainerRunner + "'."}
			}
			if app.Platform.IsWSL {
				fmt.Fprintln(app.Err, wslNote)
			}
			if detach {
				return startDetached(cmd.Context(), app, cfg, wait, image)
			}
			fmt.Fprintf(app.Out, "Starting runner '%s' for %s%s (Ctrl-C to stop)\n", cfg.Runner.Name, cfg.Runner.Repo, modeNote(cfg, image))
			st, err := runSupervisor(cmd.Context(), app, cfg, supervisor.ModeForeground, app.Out, image)
			if err := failureError(cfg.Runner.Name, st, err); err != nil {
				return err
			}
			fmt.Fprintf(app.Out, "Runner '%s' stopped (registration kept; start again with 'ownci start')\n", cfg.Runner.Name)
			return nil
		},
	}
	cmd.Flags().BoolVar(&detach, "detach", false, "run the runner in the background")
	cmd.Flags().DurationVar(&wait, "wait", 60*time.Second, "with --detach: how long to wait for the runner to start listening")
	cmd.Flags().StringVar(&image, "image", "", "container-runner mode: runner image for this session (default: the one chosen at setup)")
	return cmd
}

// modeNote describes a container-runner session for the start messages.
func modeNote(cfg *config.Config, image string) string {
	if cfg.Runner.EffectiveMode() != config.ModeContainerRunner {
		return ""
	}
	if image == "" {
		image = cfg.Runner.Image
	}
	return " in a container (" + image + ")"
}

// checkStartPreconditions verifies everything start needs and returns the
// config. Each failure says what to do next.
func checkStartPreconditions(ctx context.Context, app *App) (*config.Config, error) {
	cfg, err := loadConfigFor(app)
	if err != nil {
		return nil, err
	}
	name := cfg.Runner.Name
	if !runner.Registered(app.Paths.RunnerDir(name)) {
		return nil, &UserError{
			Msg:  fmt.Sprintf("runner '%s' has no registration in %s", name, app.Paths.RunnerDir(name)),
			Hint: "Run 'ownci setup --repo " + cfg.Runner.Repo + " --force' to register it again.",
		}
	}
	if st := docker.Check(ctx, newExecRunner(app.Logger)); !st.OK() {
		return nil, st.Err // carries its own hint
	}
	running, info, err := supervisor.IsRunning(app.Paths, name)
	if err != nil {
		return nil, err
	}
	if running {
		return nil, alreadyRunningError(info)
	}
	return cfg, nil
}

// startDetached re-executes ownci as a background supervisor and waits until
// the runner is listening, has failed, or the wait expires.
func startDetached(ctx context.Context, app *App, cfg *config.Config, wait time.Duration, image string) error {
	name := cfg.Runner.Name
	exe, err := executablePath()
	if err != nil {
		return fmt.Errorf("locating the ownci executable: %w", err)
	}
	supLog, err := os.OpenFile(logPathSupervisor(app, name), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("opening supervisor log: %w", err)
	}
	defer supLog.Close()

	args := []string{"__supervise", "--name", name, "--data-dir", app.Paths.Root}
	if image != "" {
		args = append(args, "--image", image)
	}
	child := exec.Command(exe, args...)
	child.Stdout, child.Stderr = supLog, supLog // Stdin nil = /dev/null
	child.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := child.Start(); err != nil {
		return fmt.Errorf("starting the background supervisor: %w", err)
	}
	pid := child.Process.Pid
	died := make(chan struct{})
	go func() { _ = child.Wait(); close(died) }()

	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for {
		st, _ := supervisor.ReadState(app.Paths.StateFile(name))
		// Only trust a state written by the supervisor we just started.
		if st != nil && st.SupervisorPID == pid {
			switch {
			case st.Phase == supervisor.PhaseListening || st.Phase == supervisor.PhaseBusy:
				fmt.Fprintf(app.Out, "✓ Runner '%s' is listening for jobs on %s%s (pid %d). Stop with: ownci stop\n", name, cfg.Runner.Repo, modeNote(cfg, image), pid)
				return nil
			case st.Phase.Failed():
				return detachFailure(app, name, *st)
			}
		}
		select {
		case <-died:
			// Re-read: a failure phase may have been written just before exit.
			if st, _ := supervisor.ReadState(app.Paths.StateFile(name)); st != nil && st.SupervisorPID == pid && st.Phase.Failed() {
				return detachFailure(app, name, *st)
			}
			return detachDied(app, name)
		case <-deadline.C:
			fmt.Fprintf(app.Out, "Runner '%s' is still starting — check 'ownci status'\n", name)
			return nil
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
	}
}

func detachFailure(app *App, name string, st supervisor.State) error {
	printLogTail(app, name)
	return &UserError{Msg: failureMessage(name, st), Hint: st.Phase.Hint()}
}

func detachDied(app *App, name string) error {
	printLogTail(app, name)
	lines := supervisor.LastLines(logPathSupervisor(app, name), 5)
	return &UserError{
		Msg:  "the background supervisor exited before the runner started listening",
		Hint: "Supervisor output:\n  " + strings.Join(lines, "\n  ") + "\nSee " + logPathSupervisor(app, name),
	}
}

func printLogTail(app *App, name string) {
	lines := supervisor.LastLines(app.Paths.RunnerLog(name), 20)
	if len(lines) == 0 {
		return
	}
	fmt.Fprintf(app.Err, "Last lines of %s:\n", app.Paths.RunnerLog(name))
	for _, l := range lines {
		fmt.Fprintln(app.Err, "  "+l)
	}
}

func logPathSupervisor(app *App, name string) string {
	return app.Paths.LogsDir + "/supervisor-" + name + ".log"
}
