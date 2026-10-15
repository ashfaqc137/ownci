package cli

import (
	"errors"
	"os"
	"syscall"
	"unsafe"

	"github.com/ashfaqc137/ownci/internal/github"
	"github.com/ashfaqc137/ownci/internal/uninstall"
	"github.com/spf13/cobra"
)

// stdinIsTerminal is a test seam: uninstall only prompts on a terminal. (A
// character-device check would also accept /dev/null, so ask the tty driver.)
var stdinIsTerminal = func() bool {
	var t syscall.Termios
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, os.Stdin.Fd(), syscall.TCGETS, uintptr(unsafe.Pointer(&t)))
	return errno == 0
}

func newUninstallCmd() *cobra.Command {
	var o uninstall.Options
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Stop the runner and safely remove OwnCI-managed files",
		Long: `Stop the runner and, on request, remove it.

Plain 'ownci uninstall' only stops the runner and explains what is left.
  --unregister   also remove the runner's registration from GitHub (needs a token
                 with admin access, like setup)
  --purge        also delete OwnCI's files: the runner directory, its logs and
                 state, and (when no other runner remains) the rest of the data
                 directory. Only paths of the known layout are removed, and only
                 in a data directory that ownci created (marker file present).
  --local-only   do not touch GitHub at all (for a runner you removed there by hand)

Docker images (the busybox cleanup image, job images) are never removed.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			app := appFrom(cmd)
			home, _ := os.UserHomeDir()
			err := uninstall.Run(cmd.Context(), uninstall.Deps{
				Paths:      app.Paths,
				Exec:       newExecRunner(app.Logger),
				HTTP:       newHTTPClient(),
				Logger:     app.Logger,
				In:         cmd.InOrStdin(),
				Out:        app.Out,
				Err:        app.Err,
				Getenv:     os.Getenv,
				Home:       home,
				IsTerminal: stdinIsTerminal,
				APIBaseURL: apiBaseURL(),
				WebBaseURL: github.WebBaseURL(),
			}, o)
			var ue *uninstall.Error
			if errors.As(err, &ue) && ue.Usage {
				return &UserError{Msg: ue.Msg, Hint: ue.Hint, Code: exitUsage}
			}
			return err
		},
	}
	f := cmd.Flags()
	f.BoolVar(&o.Unregister, "unregister", false, "remove the runner's registration from GitHub")
	f.BoolVar(&o.Purge, "purge", false, "delete OwnCI's files for this runner (and the data directory when it is empty)")
	f.BoolVar(&o.LocalOnly, "local-only", false, "do not contact GitHub (cannot be combined with --unregister)")
	f.BoolVarP(&o.Yes, "yes", "y", false, "do not ask for confirmation")
	f.StringVar(&o.Name, "name", "", "runner name (default: the configured runner)")
	return cmd
}
