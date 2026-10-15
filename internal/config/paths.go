package config

import (
	"fmt"
	"os"
	"path/filepath"
)

// MarkerName is the file that proves OwnCI created a data directory.
const MarkerName = ".ownci-data-dir"

const markerContent = "ownci data directory — managed by ownci; safe to delete with 'ownci uninstall --purge'\n"

// Paths holds every location inside the data directory (00-context §4).
type Paths struct {
	Root, Marker, ConfigDir, ConfigFile, LogsDir, OwnciLog, CacheDir, RunDir, RunnersDir string
}

// NewPaths computes the layout under root. It touches nothing.
func NewPaths(root string) Paths {
	return Paths{
		Root:       root,
		Marker:     filepath.Join(root, MarkerName),
		ConfigDir:  filepath.Join(root, "config"),
		ConfigFile: filepath.Join(root, "config", "config.json"),
		LogsDir:    filepath.Join(root, "logs"),
		OwnciLog:   filepath.Join(root, "logs", "ownci.log"),
		CacheDir:   filepath.Join(root, "cache"),
		RunDir:     filepath.Join(root, "run"),
		RunnersDir: filepath.Join(root, "runners"),
	}
}

func (p Paths) RunnerDir(name string) string { return filepath.Join(p.RunnersDir, name) }
func (p Paths) RunnerLog(name string) string { return filepath.Join(p.LogsDir, "runner-"+name+".log") }
func (p Paths) LockFile(name string) string  { return filepath.Join(p.RunDir, name+".lock") }
func (p Paths) PidFile(name string) string   { return filepath.Join(p.RunDir, name+".pid") }
func (p Paths) StateFile(name string) string { return filepath.Join(p.RunDir, name+".state.json") }

// Foreign reports whether the root exists without OwnCI's marker, i.e. it was
// a pre-existing directory OwnCI adopted. `uninstall --purge` must refuse it.
func (p Paths) Foreign() bool {
	if _, err := os.Stat(p.Root); err != nil {
		return false
	}
	_, err := os.Stat(p.Marker)
	return err != nil
}

// Ensure creates the layout with 0700 directories and writes the marker.
// It is idempotent.
func (p Paths) Ensure() error { return p.EnsureNotify(nil) }

// EnsureNotify is Ensure that also reports noteworthy events (permissions
// tightened, foreign directory adopted) through notify. The log file lives
// inside the data dir, so the caller can only log them after Ensure returns.
func (p Paths) EnsureNotify(notify func(msg string)) error {
	if notify == nil {
		notify = func(string) {}
	}
	writeMarker := false
	fi, err := os.Stat(p.Root)
	switch {
	case os.IsNotExist(err):
		writeMarker = true
	case err != nil:
		return fmt.Errorf("checking data directory: %w", err)
	case !fi.IsDir():
		return fmt.Errorf("data directory %s exists and is not a directory", p.Root)
	default:
		entries, err := os.ReadDir(p.Root)
		if err != nil {
			return fmt.Errorf("reading data directory: %w", err)
		}
		_, markerErr := os.Stat(p.Marker)
		switch {
		case markerErr == nil:
		case len(entries) == 0:
			writeMarker = true
		default:
			notify("data directory exists, is not empty and has no " + MarkerName + " marker; using it without a marker")
		}
	}

	for _, d := range []string{p.Root, p.ConfigDir, p.LogsDir, p.CacheDir, p.RunDir, p.RunnersDir} {
		if err := ensureDir(d, notify); err != nil {
			return err
		}
	}
	if writeMarker {
		if err := os.WriteFile(p.Marker, []byte(markerContent), 0o600); err != nil {
			return fmt.Errorf("writing marker file: %w", err)
		}
	}
	return nil
}

func ensureDir(d string, notify func(string)) error {
	if err := os.MkdirAll(d, 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", d, err)
	}
	fi, err := os.Stat(d)
	if err != nil {
		return err
	}
	if fi.Mode().Perm() != 0o700 {
		notify(fmt.Sprintf("tightening %s from %04o to 0700", d, fi.Mode().Perm()))
		if err := os.Chmod(d, 0o700); err != nil {
			return fmt.Errorf("setting permissions on %s: %w", d, err)
		}
	}
	return nil
}
