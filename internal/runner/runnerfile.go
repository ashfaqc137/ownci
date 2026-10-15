package runner

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// RunnerFile is the part of the runner's `.runner` file OwnCI reads. It never
// touches .credentials or .credentials_rsaparams, which hold secrets.
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
