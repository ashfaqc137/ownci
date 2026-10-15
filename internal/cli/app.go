package cli

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/ashfaqc137/ownci/internal/buildinfo"
	"github.com/ashfaqc137/ownci/internal/config"
	"github.com/ashfaqc137/ownci/internal/logging"
	"github.com/ashfaqc137/ownci/internal/platform"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// skipAppAnnotation marks commands that must work without a data directory.
const skipAppAnnotation = "skipApp"

// noLayoutAnnotation marks commands that must not create OwnCI's directory
// layout inside a non-empty directory that OwnCI did not create.
const noLayoutAnnotation = "noLayout"

// foreignNonEmpty reports whether the root is an existing, non-empty directory
// without OwnCI's marker.
func foreignNonEmpty(p config.Paths) bool {
	if !p.Foreign() {
		return false
	}
	entries, err := os.ReadDir(p.Root)
	return err == nil && len(entries) > 0
}

// App is the per-invocation state shared by all commands. It is built in the
// root's PersistentPreRunE; commands take what they need from it.
type App struct {
	Paths    config.Paths
	Logger   *slog.Logger
	Platform platform.Info
	Out, Err io.Writer
}

type appKey struct{}

// appFrom returns the App built by the root pre-run. It panics for commands
// marked skipApp, which must not call it.
func appFrom(cmd *cobra.Command) *App {
	a, ok := cmd.Context().Value(appKey{}).(*App)
	if !ok {
		panic("cli: App not initialised for command " + cmd.CommandPath())
	}
	return a
}

// setupApp resolves the data dir, creates it, starts logging and detects the
// platform. The returned closer closes the log file.
func setupApp(cmd *cobra.Command, f *rootFlags) (*App, io.Closer, error) {
	home, _ := os.UserHomeDir()
	root, err := config.ResolveDataDir(f.dataDir, os.Getenv, home)
	if err != nil {
		return nil, nil, &UserError{
			Msg:  err.Error(),
			Hint: "Pass --data-dir <dir> or set OWNCI_DATA_DIR to a dedicated directory.",
			Code: exitUsage,
		}
	}

	paths := config.NewPaths(root)
	// A command that only removes things must not litter a directory that is
	// not OwnCI's: skip the layout (and the log file inside it) there.
	untouched := cmd.Annotations[noLayoutAnnotation] == "true" && foreignNonEmpty(paths)
	logFile := paths.OwnciLog
	var notes []string
	if untouched {
		logFile = os.DevNull
	} else if err := paths.EnsureNotify(func(m string) { notes = append(notes, m) }); err != nil {
		return nil, nil, &UserError{
			Msg:  fmt.Sprintf("preparing data directory %s: %v", root, err),
			Hint: "Check that the directory is writable, or choose another with --data-dir.",
		}
	}

	logger, closer, err := logging.Setup(logging.Options{
		LogFile: logFile,
		Verbose: f.verbose,
		Stderr:  cmd.ErrOrStderr(),
	})
	if err != nil {
		return nil, nil, &UserError{Msg: err.Error(), Hint: "Check permissions on " + paths.LogsDir + "."}
	}

	app := &App{
		Paths:    paths,
		Logger:   logger,
		Platform: platform.Detect(),
		Out:      cmd.OutOrStdout(),
		Err:      cmd.ErrOrStderr(),
	}
	for _, n := range notes {
		logger.Debug(n)
	}
	if paths.Foreign() {
		logger.Warn("data directory was not created by ownci; 'uninstall --purge' will refuse to delete it", "datadir", root)
	}
	logger.Debug("ownci start",
		"args", describeInvocation(cmd),
		"version", buildinfo.Version,
		"datadir", root)
	return app, closer, nil
}

// describeInvocation lists the command, the flags that were set and the
// positional arguments. Redaction of tokens is done by the logger.
func describeInvocation(cmd *cobra.Command) string {
	parts := []string{cmd.CommandPath()}
	cmd.Flags().Visit(func(fl *pflag.Flag) {
		parts = append(parts, "--"+fl.Name+"="+fl.Value.String())
	})
	return strings.Join(append(parts, cmd.Flags().Args()...), " ")
}

func withApp(cmd *cobra.Command, app *App) {
	cmd.SetContext(context.WithValue(cmd.Context(), appKey{}, app))
}
