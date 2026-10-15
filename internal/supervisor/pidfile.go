//go:build linux

package supervisor

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// PidInfo is run/<name>.pid. It is informational: whether the runner is
// running is decided by the lock, not by this file.
type PidInfo struct {
	PID          int       `json:"pid"`
	Mode         string    `json:"mode"` // foreground | detached
	StartedAt    time.Time `json:"startedAt"`
	OwnciVersion string    `json:"ownciVersion"`
}

// Supervisor modes.
const (
	ModeForeground = "foreground"
	ModeDetached   = "detached"
)

// ReadPidFile returns the zero PidInfo and no error when the file is missing.
func ReadPidFile(path string) (PidInfo, error) {
	var p PidInfo
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return p, nil
	}
	if err != nil {
		return p, err
	}
	err = json.Unmarshal(b, &p)
	return p, err
}

func writePidFile(path string, p PidInfo) error { return writeJSONAtomic(path, p) }

// writeJSONAtomic writes v as JSON to path via a temp file and rename, 0600.
func writeJSONAtomic(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op after a successful rename
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
