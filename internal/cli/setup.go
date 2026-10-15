package cli

import "github.com/spf13/cobra"

func newSetupCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "setup",
		Short: "Download, verify and register the official GitHub Actions runner",
		Args:  cobra.ArbitraryArgs,
		RunE: func(*cobra.Command, []string) error {
			return ErrNotImplemented
		},
	}
}
