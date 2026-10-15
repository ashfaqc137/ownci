//go:build linux

// Package supervisor runs the official runner under OwnCI: single-instance
// locking, output capture, a state machine fed by the runner's own output,
// restart policy, and graceful or forced stop.
package supervisor

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"
)

// ErrAlreadyRunning means another supervisor holds the lock for this runner.
var ErrAlreadyRunning = errors.New("runner is already running")

// acquireLock takes an exclusive, non-blocking flock on path. The lock is the
// source of truth for "running": the kernel drops it when the holder dies, so
// it cannot go stale the way a pidfile can.
//
// A few short retries absorb a concurrent IsRunning probe, which briefly holds
// a shared lock.
func acquireLock(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("opening lock file: %w", err)
	}
	for i := 0; ; i++ {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return f, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			f.Close()
			return nil, fmt.Errorf("locking %s: %w", path, err)
		}
		if i >= 3 {
			f.Close()
			return nil, ErrAlreadyRunning
		}
		time.Sleep(30 * time.Millisecond)
	}
}

// lockHeld reports whether some process holds the lock at path. A missing
// lock file means nobody does.
func lockHeld(path string) (bool, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("opening lock file: %w", err)
	}
	defer f.Close()
	err = syscall.Flock(int(f.Fd()), syscall.LOCK_SH|syscall.LOCK_NB)
	switch {
	case err == nil:
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		return false, nil
	case errors.Is(err, syscall.EWOULDBLOCK):
		return true, nil
	default:
		return false, fmt.Errorf("probing %s: %w", path, err)
	}
}
