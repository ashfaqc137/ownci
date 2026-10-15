package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/ashfaqc137/ownci/internal/doctor"
	"github.com/ashfaqc137/ownci/internal/execx"
	"github.com/spf13/cobra"
)

// Test seams: tests replace these to avoid real commands and the real uid.
var (
	newExecRunner = func(l *slog.Logger) execx.Runner { return execx.NewOS(l) }
	geteuid       = os.Geteuid
	// newHTTPClient returns nil to let packages use their own default client.
	newHTTPClient = func() *http.Client { return nil }
)

func newDoctorCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check that this machine can run OwnCI (platform, Docker, dependencies)",
		Long: `Check that this machine can run OwnCI. Read-only.

Exits 3 when any check fails; warnings do not affect the exit code.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			app := appFrom(cmd)
			ex := newExecRunner(app.Logger)
			rep := doctor.Run(cmd.Context(), []doctor.Check{
				doctor.PlatformCheck(app.Platform),
				doctor.WSLCheck(app.Platform),
				doctor.UserCheck(geteuid),
				doctor.DataDirCheck(app.Paths.Root),
				doctor.RunnerDepsCheck(ex),
				doctor.DockerCheck(ex),
				doctor.GitCheck(ex),
			})
			var err error
			if asJSON {
				err = writeDoctorJSON(app.Out, rep)
			} else {
				writeDoctorText(app.Out, rep)
			}
			if err != nil {
				return err
			}
			if rep.Failed() {
				return &exitError{Code: exitDoctorBad}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the report as JSON")
	return cmd
}

func writeDoctorJSON(w io.Writer, rep doctor.Report) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(struct {
		doctor.Report
		Summary map[string]int `json:"summary"`
	}{rep, map[string]int{
		"passed":   rep.Count(doctor.Pass),
		"warnings": rep.Count(doctor.Warn),
		"failed":   rep.Count(doctor.Fail),
	}})
}

func writeDoctorText(w io.Writer, rep doctor.Report) {
	width := 0
	for _, r := range rep.Results {
		width = max(width, len(r.Name))
	}
	tags := map[doctor.Level]string{doctor.Pass: "[PASS]", doctor.Warn: "[WARN]", doctor.Fail: "[FAIL]"}
	indent := strings.Repeat(" ", len("[PASS] "))
	for _, r := range rep.Results {
		fmt.Fprintf(w, "%s %-*s  %s\n", tags[r.Level], width, r.Name, r.Detail)
		if r.Hint != "" && r.Level != doctor.Pass {
			fmt.Fprintf(w, "%sHint: %s\n", indent, r.Hint)
		}
	}
	warnings := rep.Count(doctor.Warn)
	plural := "s"
	if warnings == 1 {
		plural = ""
	}
	fmt.Fprintf(w, "\n%d passed, %d warning%s, %d failed\n", rep.Count(doctor.Pass), warnings, plural, rep.Count(doctor.Fail))
}
