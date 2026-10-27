package cli

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/ashfaqc137/ownci/internal/logtail"
	"github.com/spf13/cobra"
)

var logSources = []string{"runner", "ownci", "supervisor", "diag"}

func newLogsCmd() *cobra.Command {
	var (
		source string
		lines  int
		follow bool
	)
	cmd := &cobra.Command{
		Use:         "logs",
		Annotations: map[string]string{noLayoutAnnotation: "true"},
		Short:       "Show OwnCI and runner logs",
		Long: `Show the end of a log file.

Sources:
  runner      what the runner printed while supervised (default)
  ownci       ownci's own diagnostics (secrets are redacted)
  supervisor  output of the background supervisor ('start --detach')
  diag        the newest of the runner's own _diag/Runner_*.log files`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			app := appFrom(cmd)
			if lines < 0 {
				return &UserError{Msg: "-n must not be negative", Code: exitUsage}
			}
			path, hint, err := logPath(app, source)
			if err != nil {
				return err
			}
			size, err := logtail.Print(app.Out, path, lines)
			if os.IsNotExist(err) {
				return &UserError{Msg: fmt.Sprintf("there is no %s log yet: %s does not exist", source, path), Hint: hint}
			}
			if err != nil {
				return fmt.Errorf("reading %s: %w", path, err)
			}
			if !follow {
				return nil
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return logtail.Follow(ctx, app.Out, path, size, 500*time.Millisecond)
		},
	}
	f := cmd.Flags()
	f.StringVar(&source, "source", "runner", "log to show: runner, ownci, supervisor or diag")
	f.IntVarP(&lines, "lines", "n", 100, "number of lines to show")
	f.BoolVarP(&follow, "follow", "f", false, "keep printing new lines (Ctrl-C to stop)")
	return cmd
}

// logPath maps a source to its file, plus a hint for when it does not exist.
func logPath(app *App, source string) (path, hint string, err error) {
	if source == "ownci" {
		return app.Paths.OwnciLog, "ownci writes it on every command; run any ownci command first.", nil
	}
	known := false
	for _, s := range logSources {
		known = known || s == source
	}
	if !known {
		return "", "", &UserError{Msg: fmt.Sprintf("unknown log source %q", source), Hint: "Use one of: runner, ownci, supervisor, diag.", Code: exitUsage}
	}
	cfg, err := loadConfigFor(app)
	if err != nil {
		return "", "", err
	}
	name := cfg.Runner.Name
	switch source {
	case "runner":
		return app.Paths.RunnerLog(name), "It is created when the runner first starts ('ownci start').", nil
	case "supervisor":
		return logPathSupervisor(app, name), "It is created by 'ownci start --detach'.", nil
	default: // diag
		matches, _ := filepath.Glob(filepath.Join(app.Paths.RunnerDir(name), "_diag", "Runner_*.log"))
		newest, newestMod := "", time.Time{}
		for _, m := range matches {
			if fi, err := os.Stat(m); err == nil && fi.ModTime().After(newestMod) {
				newest, newestMod = m, fi.ModTime()
			}
		}
		if newest == "" {
			newest = filepath.Join(app.Paths.RunnerDir(name), "_diag", "Runner_*.log")
		}
		return newest, "The runner writes these to its _diag directory once it has run.", nil
	}
}
