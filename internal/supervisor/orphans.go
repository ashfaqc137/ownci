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

// orphanPids lists processes whose executable or first script argument lives
// under runnerDir (bin/Runner.Listener, bin/Runner.Worker, run.sh, ...), except
// self.
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
		args := strings.Split(strings.TrimRight(string(b), "\x00"), "\x00")
		for i := 0; i < len(args) && i < 2; i++ {
			if strings.HasPrefix(filepath.Clean(args[i])+string(filepath.Separator), prefix) {
				pids = append(pids, pid)
				break
			}
		}
	}
	return pids
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
