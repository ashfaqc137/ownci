package cli

import (
	"fmt"
	"os"

	"github.com/ashfaqc137/ownci/internal/runner"
	"github.com/spf13/cobra"
)

// assetFor is a test seam over runner.AssetFor.
var assetFor = runner.AssetFor

func newSetupCmd() *cobra.Command {
	var (
		downloadOnly bool
		name         string
		force        bool
	)
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Download, verify and register the official GitHub Actions runner",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !downloadOnly {
				return ErrNotImplemented // full setup arrives in t06
			}
			return runDownloadOnly(cmd, name, force)
		},
	}
	cmd.Flags().BoolVar(&downloadOnly, "download-only", false, "download, verify and extract the runner without registering it")
	cmd.Flags().StringVar(&name, "name", "", "runner name (default: ownci-<hostname>)")
	cmd.Flags().BoolVar(&force, "force", false, "replace an existing, unregistered runner directory")
	return cmd
}

func runDownloadOnly(cmd *cobra.Command, name string, force bool) error {
	app := appFrom(cmd)
	if err := app.Platform.Supported(); err != nil {
		return &UserError{Msg: err.Error(), Hint: "OwnCI supports Linux (including WSL2) on amd64 and arm64."}
	}
	if name == "" {
		host, _ := os.Hostname()
		name = runner.DefaultName(host)
	}
	asset, err := assetFor(app.Platform.RunnerArch)
	if err != nil {
		return err
	}
	res, err := runner.Install(cmd.Context(), runner.InstallOptions{
		Paths:    app.Paths,
		Name:     name,
		Asset:    asset,
		Exec:     newExecRunner(app.Logger),
		HTTP:     newHTTPClient(),
		Force:    force,
		Progress: app.Out,
		Logger:   app.Logger,
	})
	if err != nil {
		return err
	}
	if res.VersionErr != nil {
		fmt.Fprintf(app.Err, "Warning: runner extracted to %s but its version could not be read: %v\n", res.Dir, res.VersionErr)
		if h, ok := res.VersionErr.(hinted); ok {
			fmt.Fprintf(app.Err, "Hint: %s\n", h.UserHint())
		}
		return nil
	}
	if res.InstalledVersion != res.ExpectedVersion {
		fmt.Fprintf(app.Err, "Warning: installed runner reports version %s, expected %s\n", res.InstalledVersion, res.ExpectedVersion)
	}
	fmt.Fprintf(app.Out, "Runner %s (%s) verified and installed at %s\n", res.InstalledVersion, asset.Arch, res.Dir)
	return nil
}
