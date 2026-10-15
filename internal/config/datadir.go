// Package config resolves the OwnCI data directory and reads/writes config.json.
package config

import (
	"errors"
	"fmt"
	"path/filepath"
)

// ResolveDataDir picks the data directory: flag, $OWNCI_DATA_DIR,
// $XDG_DATA_HOME/ownci, then <home>/.local/share/ownci. The result is
// absolute and clean. It refuses "/" and the home directory itself so that a
// later `uninstall --purge` can never wipe either.
func ResolveDataDir(flagVal string, getenv func(string) string, home string) (string, error) {
	dir := flagVal
	switch {
	case dir != "":
	case getenv("OWNCI_DATA_DIR") != "":
		dir = getenv("OWNCI_DATA_DIR")
	case getenv("XDG_DATA_HOME") != "":
		dir = filepath.Join(getenv("XDG_DATA_HOME"), "ownci")
	case home != "":
		dir = filepath.Join(home, ".local", "share", "ownci")
	default:
		return "", errors.New("cannot determine the data directory: no home directory; use --data-dir or OWNCI_DATA_DIR")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("resolving data directory %q: %w", dir, err)
	}
	if abs == "/" || (home != "" && abs == filepath.Clean(home)) {
		return "", fmt.Errorf("refusing to use %q as the data directory: pick a dedicated directory", abs)
	}
	return abs, nil
}
