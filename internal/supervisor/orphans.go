//go:build linux

package supervisor

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// killOrphans SIGKILLs every process that still runs from runnerDir and waits
// briefly for them to exit. It returns the pids it signalled.
//
// The caller holds the supervisor lock, so nothing legitimate runs from the
// directory: what is left is the runner of a supervisor that was SIGKILLed
// (the listener outlives it, keeps the GitHub session and makes a new runner
// loop on "A session for this runner already exists").
func killOrphans(runnerDir string) []int {
	pids := orphanPids(runnerDir, os.Getpid())
	for _, pid := range pids {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		alive := false
		for _, pid := range pids {
			if syscall.Kill(pid, 0) == nil && !isZombie(pid) {
				alive = true
				break
			}
		}
		if !alive {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	return pids
}

// orphanPids lists the runner's processes under runnerDir (see
// runnerProcess), except self.
func orphanPids(runnerDir string, self int) []int {
	prefix := filepath.Clean(runnerDir) + string(filepath.Separator)
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	var pids []int
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid == self {
			continue
		}
		b, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
		if err != nil || len(b) == 0 {
			continue
		}
		if runnerProcess(strings.Split(strings.TrimRight(string(b), "\x00"), "\x00"), prefix) {
			pids = append(pids, pid)
		}
	}
	return pids
}

// runnerProcess reports whether a command line belongs to the runner: an
// executable under the runner directory (bin/Runner.Listener, ...) or a shell
// running one of its scripts (run.sh, run-helper.sh). Other programs that
// merely name a file there, such as an editor open on .env, are left alone.
func runnerProcess(args []string, prefix string) bool {
	under := func(p string) bool {
		return strings.HasPrefix(filepath.Clean(p)+string(filepath.Separator), prefix)
	}
	if len(args) == 0 {
		return false
	}
	if under(args[0]) {
		return true
	}
	switch filepath.Base(args[0]) {
	case "bash", "sh", "dash":
		return len(args) > 1 && strings.HasSuffix(args[1], ".sh") && under(args[1])
	}
	return false
}

func isZombie(pid int) bool {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return true
	}
	s := string(b)
	i := strings.LastIndex(s, ")")
	return i >= 0 && i+2 < len(s) && s[i+2] == 'Z'
}
