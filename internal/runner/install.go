package runner

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/ashfaqc137/ownci/internal/config"
	"github.com/ashfaqc137/ownci/internal/execx"
	"github.com/ashfaqc137/ownci/internal/progress"
)

var nameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// ValidateName checks a runner name. Names become directory and file names
// under the data dir, so this is also a path-traversal guard.
func ValidateName(name string) error {
	if !nameRE.MatchString(name) {
		return &Error{
			Msg:  fmt.Sprintf("invalid runner name %q", name),
			Hint: "Use 1-64 characters from a-z, 0-9 and '-', starting with a letter or digit.",
		}
	}
	return nil
}

// DefaultName returns "ownci-<short hostname>", lowercased and reduced to
// [a-z0-9-], at most 64 characters.
func DefaultName(hostname string) string {
	short, _, _ := strings.Cut(hostname, ".")
	var b strings.Builder
	prevDash := true // also trims a leading dash
	for _, r := range strings.ToLower(short) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			prevDash = false
		} else if !prevDash {
			b.WriteByte('-')
			prevDash = true
		}
	}
	host := strings.TrimRight(b.String(), "-")
	if host == "" {
		host = "local"
	}
	name := "ownci-" + host
	if len(name) > 64 {
		name = strings.TrimRight(name[:64], "-")
	}
	return name
}

// InstallOptions configures Install. HTTP, Progress and Logger may be nil.
type InstallOptions struct {
	Paths    config.Paths
	Name     string
	Asset    Asset
	Exec     execx.Runner
	HTTP     *http.Client
	Force    bool
	Progress *progress.Printer
	Logger   *slog.Logger
}

// InstallResult describes a finished install. A failed `--version` probe
// (commonly missing ICU) does not fail the install; it sets VersionErr.
type InstallResult struct {
	Dir, ArchivePath, ExpectedVersion, InstalledVersion string
	VersionErr                                          error
}

// Install downloads (or reuses) the runner archive, verifies it, extracts it
// into runners/<name>/ and probes the installed version.
func Install(ctx context.Context, o InstallOptions) (InstallResult, error) {
	if err := ValidateName(o.Name); err != nil {
		return InstallResult{}, err
	}
	dir := o.Paths.RunnerDir(o.Name)
	exists, err := checkExisting(dir, o.Name, o.Force)
	if err != nil {
		return InstallResult{}, err
	}

	// Download before touching an existing install, so a failed download
	// never destroys a working one.
	archive, err := Fetch(ctx, FetchOptions{
		Asset: o.Asset, CacheDir: o.Paths.CacheDir, HTTP: o.HTTP, Progress: o.Progress, Logger: o.Logger,
	})
	if err != nil {
		return InstallResult{}, err
	}
	o.Progress.Step("Extracting the runner")
	defer o.Progress.End()
	if exists {
		if err := os.RemoveAll(dir); err != nil {
			return InstallResult{}, fmt.Errorf("removing existing runner dir: %w", err)
		}
	}
	if err := Extract(archive, dir); err != nil {
		return InstallResult{}, fmt.Errorf("extracting runner: %w", err)
	}

	res := InstallResult{Dir: dir, ArchivePath: archive, ExpectedVersion: o.Asset.Version}
	res.InstalledVersion, res.VersionErr = InstalledVersion(ctx, o.Exec, dir)
	return res, nil
}

func checkExisting(dir, name string, force bool) (exists bool, err error) {
	if _, err := os.Lstat(dir); errors.Is(err, fs.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, fmt.Errorf("checking %s: %w", dir, err)
	}
	if _, err := os.Lstat(filepath.Join(dir, ".runner")); err == nil {
		return true, &Error{
			Msg:  fmt.Sprintf("runner %q is registered", name),
			Hint: "Run 'ownci uninstall --unregister' first, or choose another --name.",
		}
	}
	if !force {
		return true, &Error{
			Msg:  fmt.Sprintf("runner directory %s already exists", dir),
			Hint: "Use --force to replace it, or choose another --name.",
		}
	}
	return true, nil
}

// InstalledVersion runs `<dir>/bin/Runner.Listener --version`.
func InstalledVersion(ctx context.Context, ex execx.Runner, dir string) (string, error) {
	bin := filepath.Join(dir, "bin", "Runner.Listener")
	res, err := ex.Run(ctx, execx.Cmd{Name: bin, Args: []string{"--version"}})
	if err != nil {
		return "", &Error{
			Msg:  fmt.Sprintf("running %s --version: %v", bin, err),
			Hint: "Install the runner's system dependencies: sudo " + filepath.Join(dir, "bin", "installdependencies.sh"),
		}
	}
	if res.ExitCode != 0 {
		detail := strings.TrimSpace(string(res.Stderr))
		if len(detail) > 300 {
			detail = detail[:300] + "…"
		}
		return "", &Error{
			Msg:  fmt.Sprintf("%s --version failed (exit %d): %s", bin, res.ExitCode, detail),
			Hint: "Install the runner's system dependencies: sudo " + filepath.Join(dir, "bin", "installdependencies.sh"),
		}
	}
	return strings.TrimSpace(string(res.Stdout)), nil
}
