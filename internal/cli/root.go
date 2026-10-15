// Package cli wires up the ownci cobra commands. It is the only package that
// prints user-facing output or reads flags.
package cli

import (
	"context"
	"io"
	"os"

	"github.com/spf13/cobra"
)

// Persistent flag values plus the log closer of the running invocation.
type rootFlags struct {
	dataDir string
	verbose bool
	closer  io.Closer
}

// close releases the log file. Safe to call more than once; needed because
// cobra skips PersistentPostRunE when a command fails.
func (f *rootFlags) close() {
	if f.closer != nil {
		f.closer.Close()
		f.closer = nil
	}
}

// NewRootCmd builds the root command with all subcommands attached.
func NewRootCmd() *cobra.Command {
	cmd, _ := newRoot()
	return cmd
}

func newRoot() (*cobra.Command, *rootFlags) {
	f := &rootFlags{}
	cmd := &cobra.Command{
		Use:           "ownci",
		Short:         "Your PR. Your Compute. — run GitHub Actions jobs on your own machine",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			printDevWarning(cmd.ErrOrStderr())
			// `version` needs no data dir, unless the user asks for verbose
			// logging into an explicit one (handy for checking the layout).
			if cmd.Annotations[skipAppAnnotation] == "true" && !(f.verbose && f.dataDir != "") {
				return nil
			}
			app, closer, err := setupApp(cmd, f)
			if err != nil {
				return err
			}
			f.closer = closer
			if cmd.Context() == nil {
				cmd.SetContext(context.Background())
			}
			withApp(cmd, app)
			return nil
		},
		PersistentPostRunE: func(*cobra.Command, []string) error {
			f.close()
			return nil
		},
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
	return cmd, f
}

// Execute runs the CLI and returns the process exit code.
func Execute() int {
	cmd, f := newRoot()
	defer f.close()
	return renderError(os.Stderr, cmd.Execute())
}
