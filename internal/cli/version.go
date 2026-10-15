package cli

import (
	"encoding/json"
	"fmt"
	"runtime"

	"github.com/ashfaqc137/ownci/internal/buildinfo"
	"github.com/spf13/cobra"
)

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
			// t07: runner version
			if asJSON {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(map[string]any{
					"version":  buildinfo.Version,
					"commit":   buildinfo.Commit,
					"date":     buildinfo.Date,
					"go":       runtime.Version(),
					"os":       runtime.GOOS,
					"arch":     runtime.GOARCH,
					"devBuild": buildinfo.DevBuild,
				})
			}
			fmt.Fprintf(out, "ownci %s (commit %s, built %s)\n", buildinfo.Version, buildinfo.Commit, buildinfo.Date)
			fmt.Fprintf(out, "go: %s %s/%s\n", runtime.Version(), runtime.GOOS, runtime.GOARCH)
			if buildinfo.DevBuild {
				fmt.Fprintln(out, "build: dev")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print version information as JSON")
	return cmd
}
