package cli

import (
	"errors"
	"fmt"
	"time"

	"github.com/ashfaqc137/ownci/internal/config"
	"github.com/ashfaqc137/ownci/internal/supervisor"
	"github.com/spf13/cobra"
)

func newStopCmd() *cobra.Command {
	var (
		timeout time.Duration
		force   bool
	)
	cmd := &cobra.Command{
		Use:   "stop",
		Short: "Stop the runner gracefully, keeping its configuration",
		Long: `Stop the runner started by 'ownci start' and wait for it to exit.

The registration and configuration are kept, so 'ownci start' brings the
runner back. A running job is cancelled. If the runner does not stop within
--timeout, --force kills it.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			app := appFrom(cmd)
			cfg, err := config.Load(app.Paths)
			if errors.Is(err, config.ErrNotConfigured) {
				fmt.Fprintln(app.Out, "OwnCI is not set up; there is no runner to stop")
				return nil
			}
			if err != nil {
				return &UserError{Msg: err.Error(), Hint: "Re-run 'ownci setup --force' to recreate the configuration."}
			}
			name := cfg.Runner.Name
			if running, _, err := supervisor.IsRunning(app.Paths, name); err == nil && running {
				fmt.Fprintf(app.Out, "Stopping runner '%s' (a running job is cancelled)…\n", name)
			}
			err = supervisor.Stop(cmd.Context(), app.Paths, name, supervisor.StopOptions{Timeout: timeout, Force: force})
			switch {
			case errors.Is(err, supervisor.ErrNotRunning):
				fmt.Fprintf(app.Out, "Runner '%s' is not running\n", name)
				return nil
			case errors.Is(err, supervisor.ErrStopTimeout):
				return &UserError{
					Msg:  fmt.Sprintf("the runner did not stop within %s", timeout),
					Hint: "Run 'ownci stop --force' to kill it.",
				}
			case err != nil:
				return &UserError{Msg: err.Error(), Hint: "Check 'ownci status'; as a last resort stop the process by hand."}
			}
			fmt.Fprintf(app.Out, "✓ Runner '%s' stopped (registration kept; start again with 'ownci start')\n", name)
			return nil
		},
	}
	cmd.Flags().DurationVar(&timeout, "timeout", 45*time.Second, "how long to wait for a graceful stop")
	cmd.Flags().BoolVar(&force, "force", false, "kill the runner if it does not stop within --timeout")
	return cmd
}
