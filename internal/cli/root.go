// Package cli wires up the ownci cobra commands. It is the only package that
// prints user-facing output or reads flags.
package cli

import (
	"os"

	"github.com/spf13/cobra"
)

// Persistent flag values. t02 wires these into config/logging.
type rootFlags struct {
	dataDir string
	verbose bool
}

// NewRootCmd builds the root command with all subcommands attached.
func NewRootCmd() *cobra.Command {
	f := &rootFlags{}
	cmd := &cobra.Command{
		Use:           "ownci",
		Short:         "Your PR. Your Compute. — run GitHub Actions jobs on your own machine",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	cmd.PersistentFlags().StringVar(&f.dataDir, "data-dir", "",
		"OwnCI data directory (default: $OWNCI_DATA_DIR or $XDG_DATA_HOME/ownci)")
	cmd.PersistentFlags().BoolVarP(&f.verbose, "verbose", "v", false, "verbose output")
	cmd.CompletionOptions.DisableDefaultCmd = true
	cmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return &UserError{Msg: err.Error(), Code: exitUsage}
	})

	cmd.AddCommand(
		newVersionCmd(),
		newDoctorCmd(),
		newSetupCmd(),
		newStartCmd(),
		newStopCmd(),
		newStatusCmd(),
		newLogsCmd(),
		newUninstallCmd(),
	)
	return cmd
}

// Execute runs the CLI and returns the process exit code.
func Execute() int {
	return renderError(os.Stderr, NewRootCmd().Execute())
}
