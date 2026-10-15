package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/ashfaqc137/ownci/internal/github"
	"github.com/ashfaqc137/ownci/internal/runner"
	"github.com/ashfaqc137/ownci/internal/setup"
	"github.com/spf13/cobra"
)

// assetFor is a test seam over runner.AssetFor.
var assetFor = runner.AssetFor

func newSetupCmd() *cobra.Command {
	var (
		o            setup.Options
		downloadOnly bool
	)
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Download, verify and register the official GitHub Actions runner",
		Long: `Set this machine up as a GitHub Actions runner for one repository.

Downloads and verifies the official runner, registers it with the label
'ownci' only (workflows use 'runs-on: [ownci]'), and saves OwnCI's config.
Needs a token with admin access to the repository: $GITHUB_TOKEN, $GH_TOKEN
or 'gh auth login'. The token is never stored.`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if downloadOnly {
				return runDownloadOnly(cmd, o.Name, o.Force)
			}
			if o.Repo == "" {
				return &UserError{
					Msg:  "--repo is required",
					Hint: "Run 'ownci setup --repo owner/name'.",
					Code: exitUsage,
				}
			}
			return runSetup(cmd, o)
		},
	}
	f := cmd.Flags()
	f.StringVar(&o.Repo, "repo", "", "repository to serve: owner/name or its GitHub URL")
	f.StringVar(&o.Name, "name", "", "runner name (default: ownci-<hostname>)")
	f.StringSliceVar(&o.Labels, "labels", nil, "extra runner labels, comma-separated (the label 'ownci' is always added)")
	f.BoolVar(&o.Force, "force", false, "re-register an existing runner in place, or replace an unregistered runner directory")
	f.BoolVar(&o.AllowPublicRepo, "allow-public-repo", false, "allow a public repository (fork PRs can run code on this machine)")
	f.BoolVar(&o.DryRun, "dry-run", false, "show what would be done without changing anything")
	f.BoolVar(&downloadOnly, "download-only", false, "download, verify and extract the runner without registering it")
	f.BoolVar(&o.SkipDockerCheck, "skip-docker-check", false, "skip the Docker preflight check (debugging only)")
	_ = f.MarkHidden("skip-docker-check")
	return cmd
}

func runSetup(cmd *cobra.Command, o setup.Options) error {
	app := appFrom(cmd)
	host, _ := os.Hostname()
	err := setup.Run(cmd.Context(), setup.Deps{
		Paths:      app.Paths,
		Platform:   app.Platform,
		Exec:       newExecRunner(app.Logger),
		HTTP:       newHTTPClient(),
		Logger:     app.Logger,
		Out:        app.Out,
		Err:        app.Err,
		Getenv:     os.Getenv,
		Geteuid:    geteuid,
		Hostname:   host,
		AssetFor:   assetFor,
		APIBaseURL: github.APIBaseURL(),
		WebBaseURL: github.WebBaseURL(),
	}, o)
	var se *setup.Error
	if errors.As(err, &se) && se.Usage {
		return &UserError{Msg: se.Msg, Hint: se.Hint, Code: exitUsage}
	}
	return err
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
