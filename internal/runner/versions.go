// Package runner downloads, verifies, extracts and inspects the official
// GitHub Actions runner.
package runner

import (
	"fmt"
	"strings"
)

// Asset is one downloadable runner archive.
type Asset struct {
	Arch, URL, SHA256, Version string
}

// Release is a runner version with its per-arch assets.
type Release struct {
	Version string
	Assets  map[string]Asset
}

const downloadBase = "https://github.com/actions/runner/releases/download/"

func asset(version, arch, sha string) Asset {
	return Asset{
		Arch:    arch,
		Version: version,
		SHA256:  sha,
		URL:     fmt.Sprintf("%sv%s/actions-runner-%s-%s.tar.gz", downloadBase, version, arch, version),
	}
}

// Pinned is the runner version OwnCI downloads on first install (D5). The
// runner self-updates afterwards. Pinned 2026-10-15 from actions/runner
// v2.338.0 (published 2026-10-06); SHA256 values come from the release notes.
var Pinned = Release{
	Version: "2.338.0",
	Assets: map[string]Asset{
		"linux-x64":   asset("2.338.0", "linux-x64", "af4b794c1bc41d73d40535e3fe092a39f9679cd8d965954c2aca25a05ca41d32"),
		"linux-arm64": asset("2.338.0", "linux-arm64", "628b4a7258487b80c1d3c221095a7ded349f5ae0175fd3dfab708d07428f041b"),
	},
}

// AssetFor returns the asset to download for a runner arch ("linux-x64" or
// "linux-arm64"). Dev builds may override it through OWNCI_DEV_RUNNER_*.
func AssetFor(arch string) (Asset, error) {
	if a, ok, err := devOverride(arch); err != nil {
		return Asset{}, err
	} else if ok {
		return a, nil
	}
	a, ok := Pinned.Assets[arch]
	if !ok {
		return Asset{}, fmt.Errorf("no runner available for architecture %q", arch)
	}
	return a, nil
}

// fileName is the archive's base name, used for the cache file.
func (a Asset) fileName() string {
	u := a.URL
	if i := strings.IndexAny(u, "?#"); i >= 0 {
		u = u[:i]
	}
	return u[strings.LastIndex(u, "/")+1:]
}
