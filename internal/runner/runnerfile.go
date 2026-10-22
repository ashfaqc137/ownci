package runner

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// RunnerFile is the part of the runner's `.runner` file OwnCI reads. It never
// reads .credentials or .credentials_rsaparams, which hold secrets.
type RunnerFile struct {
	AgentID    int64
	AgentName  string
	PoolID     int64
	ServerURL  string
	GitHubURL  string
	WorkFolder string
}

// ReadRunnerFile parses <dir>/.runner. The runner writes it with a UTF-8 BOM,
// which is stripped.
func ReadRunnerFile(dir string) (RunnerFile, error) {
	path := filepath.Join(dir, ".runner")
	b, err := os.ReadFile(path)
	if err != nil {
		return RunnerFile{}, fmt.Errorf("reading %s: %w", path, err)
	}
	b = bytes.TrimPrefix(b, []byte("\xef\xbb\xbf"))
	// Ids are numbers in current runners; json.Number also accepts a quoted
	// number, so an older format does not break parsing.
	var raw struct {
		AgentID    json.Number `json:"agentId"`
		AgentName  string      `json:"agentName"`
		PoolID     json.Number `json:"poolId"`
		ServerURL  string      `json:"serverUrl"`
		GitHubURL  string      `json:"gitHubUrl"`
		WorkFolder string      `json:"workFolder"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return RunnerFile{}, fmt.Errorf("parsing %s: %w", path, err)
	}
	agent, _ := raw.AgentID.Int64()
	pool, _ := raw.PoolID.Int64()
	return RunnerFile{
		AgentID: agent, AgentName: raw.AgentName, PoolID: pool,
		ServerURL: raw.ServerURL, GitHubURL: raw.GitHubURL, WorkFolder: raw.WorkFolder,
	}, nil
}

// Registered reports whether dir holds a registered runner (a .runner file).
func Registered(dir string) bool {
	_, err := os.Lstat(filepath.Join(dir, ".runner"))
	return err == nil
}

// registrationFiles are what config.sh writes when it registers. It refuses
// to register again while .runner exists, even with --replace (which only
// replaces a same-named runner on GitHub).
var registrationFiles = []string{".runner", ".credentials", ".credentials_rsaparams"}

const setAsideSuffix = ".ownci-previous"

// SetAsideRegistration moves the local registration files out of config.sh's
// way so the runner in dir can be registered again. Call restore when the new
// registration failed (the old files come back) and discard when it worked.
func SetAsideRegistration(dir string) (restore func() error, discard func(), err error) {
	var moved []string
	restore = func() error {
		var first error
		for _, name := range moved {
			p := filepath.Join(dir, name)
			if err := os.Rename(p+setAsideSuffix, p); err != nil && first == nil {
				first = err
			}
		}
		return first
	}
	discard = func() {
		for _, name := range moved {
			_ = os.Remove(filepath.Join(dir, name+setAsideSuffix))
		}
	}
	for _, name := range registrationFiles {
		p := filepath.Join(dir, name)
		if _, err := os.Lstat(p); errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err := os.Rename(p, p+setAsideSuffix); err != nil {
			_ = restore()
			return nil, nil, fmt.Errorf("setting aside %s: %w", p, err)
		}
		moved = append(moved, name)
	}
	return restore, discard, nil
}
