package cli

import "github.com/spf13/cobra"

func newUninstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Stop the runner and safely remove OwnCI-managed files",
		Args:  cobra.ArbitraryArgs,
		RunE: func(*cobra.Command, []string) error {
			return ErrNotImplemented
		},
	}
}
