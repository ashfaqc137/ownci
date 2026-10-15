package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"

	"github.com/ashfaqc137/ownci/internal/buildinfo"
	"github.com/ashfaqc137/ownci/internal/config"
	"github.com/ashfaqc137/ownci/internal/supervisor"
	"github.com/spf13/cobra"
)

// configuredRunnerVersion is the runner version of the configured runner, or
// "" when ownci is not set up. It only reads: version must not create the data
// directory. The running version (after a self-update) beats the installed one.
func configuredRunnerVersion(cmd *cobra.Command) string {
	var flagVal string
	if f := cmd.Flag("data-dir"); f != nil {
		flagVal = f.Value.String()
	}
	home, _ := os.UserHomeDir()
	root, err := config.ResolveDataDir(flagVal, os.Getenv, home)
	if err != nil {
		return ""
	}
	paths := config.NewPaths(root)
	cfg, err := config.Load(paths)
	if err != nil {
		return ""
	}
	if st, _ := supervisor.ReadState(paths.StateFile(cfg.Runner.Name)); st != nil && st.RunnerVersion != "" {
		return st.RunnerVersion
	}
	return cfg.Runner.InstalledVersion
}

func newVersionCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print the ownci version and build information",
		Long: `Print the ownci version and build information.

version does not touch the data directory. To check the data directory layout
without another command, combine --verbose with --data-dir: the directory is
then created and logged to as for any other command.`,
		Args:        cobra.NoArgs,
		Annotations: map[string]string{skipAppAnnotation: "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			runnerVersion := configuredRunnerVersion(cmd)
			if asJSON {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				m := map[string]any{
					"version":  buildinfo.Version,
					"commit":   buildinfo.Commit,
					"date":     buildinfo.Date,
					"go":       runtime.Version(),
					"os":       runtime.GOOS,
					"arch":     runtime.GOARCH,
					"devBuild": buildinfo.DevBuild,
				}
				if runnerVersion != "" {
					m["runner"] = runnerVersion
				}
				return enc.Encode(m)
			}
			fmt.Fprintf(out, "ownci %s (commit %s, built %s)\n", buildinfo.Version, buildinfo.Commit, buildinfo.Date)
			fmt.Fprintf(out, "go: %s %s/%s\n", runtime.Version(), runtime.GOOS, runtime.GOARCH)
			if runnerVersion != "" {
				fmt.Fprintf(out, "runner: %s\n", runnerVersion)
			}
			if buildinfo.DevBuild {
				fmt.Fprintln(out, "build: dev")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print version information as JSON")
	return cmd
}
