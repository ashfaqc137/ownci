//go:build linux

package supervisor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/ashfaqc137/ownci/internal/config"
)

var (
	// ErrNotRunning means no supervisor holds the lock.
	ErrNotRunning = errors.New("runner is not running")
	// ErrStopTimeout means the supervisor did not exit in time (use Force).
	ErrStopTimeout = errors.New("runner did not stop in time")
)

// IsRunning reports whether a supervisor holds the lock for name. The pidfile
// is returned for display only; it may be missing or stale.
func IsRunning(paths config.Paths, name string) (bool, PidInfo, error) {
	held, err := lockHeld(paths.LockFile(name))
	if err != nil {
		return false, PidInfo{}, err
	}
	if !held {
		return false, PidInfo{}, nil
	}
	info, _ := ReadPidFile(paths.PidFile(name))
	return true, info, nil
}

// StopOptions tune Stop. The zero value is production behavior.
type StopOptions struct {
	Timeout time.Duration // wait for a graceful stop (default 45s)
	Force   bool          // SIGKILL the supervisor and runner after Timeout
	// IsOwnci verifies that a pid is an ownci process before it is signalled
	// (a pidfile can outlive its process). Default: look at /proc/<pid>/cmdline.
	IsOwnci func(pid int) bool
}

// Stop asks the supervisor to stop and waits for it to release the lock.
func Stop(ctx context.Context, paths config.Paths, name string, o StopOptions) error {
	if o.Timeout <= 0 {
		o.Timeout = 45 * time.Second
	}
	if o.IsOwnci == nil {
		o.IsOwnci = isOwnciProcess
	}
	running, info, err := IsRunning(paths, name)
	if err != nil {
		return err
	}
	if !running {
		return ErrNotRunning
	}
	if info.PID <= 0 || !o.IsOwnci(info.PID) {
		return fmt.Errorf("runner '%s' holds its lock but %s does not name a live ownci process", name, paths.PidFile(name))
	}
	if err := syscall.Kill(info.PID, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("signalling supervisor pid %d: %w", info.PID, err)
	}
	if waitUnlocked(ctx, paths, name, o.Timeout) {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !o.Force {
		return ErrStopTimeout
	}

	// Kill the supervisor first so it cannot restart the runner, then sweep
	// the runner's process group.
	_ = syscall.Kill(info.PID, syscall.SIGKILL)
	if st, _ := ReadState(paths.StateFile(name)); st != nil && st.RunnerPID > 0 {
		_ = syscall.Kill(-st.RunnerPID, syscall.SIGKILL)
	}
	if !waitUnlocked(ctx, paths, name, 5*time.Second) {
		return fmt.Errorf("supervisor pid %d is still alive after SIGKILL", info.PID)
	}
	_ = os.Remove(paths.PidFile(name))
	st, _ := ReadState(paths.StateFile(name))
	if st == nil {
		st = &State{Name: name}
	}
	st.Phase, st.Since, st.RunnerPID, st.CurrentJob = PhaseStopped, time.Now(), 0, ""
	_ = writeJSONAtomic(paths.StateFile(name), st)
	return nil
}

func waitUnlocked(ctx context.Context, paths config.Paths, name string, d time.Duration) bool {
	deadline := time.NewTimer(d)
	defer deadline.Stop()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		if held, err := lockHeld(paths.LockFile(name)); err == nil && !held {
			return true
		}
		select {
		case <-tick.C:
		case <-deadline.C:
			return false
		case <-ctx.Done():
			return false
		}
	}
}

// isOwnciProcess is true for `ownci …` and for the hidden `__supervise`
// re-exec, whatever the binary is called.
func isOwnciProcess(pid int) bool {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil {
		return false
	}
	args := strings.Split(strings.TrimRight(string(b), "\x00"), "\x00")
	if len(args) == 0 {
		return false
	}
	if strings.HasPrefix(filepath.Base(args[0]), "ownci") {
		return true
	}
	for _, a := range args[1:] {
		if a == "__supervise" {
			return true
		}
	}
	return false
}
