package cli

import "github.com/spf13/cobra"

func newStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the runner gracefully, keeping its configuration",
		Args:  cobra.ArbitraryArgs,
		RunE: func(*cobra.Command, []string) error {
			return ErrNotImplemented
		},
	}
}
