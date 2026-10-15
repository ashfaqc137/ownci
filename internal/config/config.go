package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// SchemaVersion is the only config schema this build understands.
const SchemaVersion = 1

// ErrNotConfigured means setup has not completed (no config or no runner).
var ErrNotConfigured = errors.New("ownci is not configured")

// Config is config/config.json. It never holds secrets.
type Config struct {
	SchemaVersion int           `json:"schemaVersion"`
	Runner        *RunnerConfig `json:"runner,omitempty"`
}

// RunnerConfig describes the registered runner (filled by setup, t06).
type RunnerConfig struct {
	Name             string    `json:"name"`
	Provider         string    `json:"provider"`
	Repo             string    `json:"repo"`
	URL              string    `json:"url"`
	Labels           []string  `json:"labels"`
	InstalledVersion string    `json:"installedVersion"`
	Isolation        string    `json:"isolation"`
	CreatedAt        time.Time `json:"createdAt"`
}

const resetHint = "re-run 'ownci setup --force' to recreate it"

// Load reads the config. It returns ErrNotConfigured when the file is missing
// or has no runner.
func Load(p Paths) (*Config, error) {
	b, err := os.ReadFile(p.ConfigFile)
	if os.IsNotExist(err) {
		return nil, ErrNotConfigured
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", p.ConfigFile, err)
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("parsing %s: %w (%s)", p.ConfigFile, err, resetHint)
	}
	if c.SchemaVersion != SchemaVersion {
		return nil, fmt.Errorf("%s has unsupported schemaVersion %d (expected %d); %s",
			p.ConfigFile, c.SchemaVersion, SchemaVersion, resetHint)
	}
	if c.Runner == nil {
		return nil, ErrNotConfigured
	}
	return &c, nil
}

// Save writes the config atomically (temp file, fsync, rename) with mode 0600.
func Save(p Paths, c *Config) error {
	out := *c
	out.SchemaVersion = SchemaVersion
	b, err := json.MarshalIndent(&out, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding config: %w", err)
	}
	b = append(b, '\n')

	tmp, err := os.CreateTemp(filepath.Dir(p.ConfigFile), ".config-*.tmp")
	if err != nil {
		return fmt.Errorf("creating temp config: %w", err)
	}
	defer os.Remove(tmp.Name()) // no-op after a successful rename
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return fmt.Errorf("writing config: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("setting config permissions: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("syncing config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing config: %w", err)
	}
	if err := os.Rename(tmp.Name(), p.ConfigFile); err != nil {
		return fmt.Errorf("replacing %s: %w", p.ConfigFile, err)
	}
	return nil
}
