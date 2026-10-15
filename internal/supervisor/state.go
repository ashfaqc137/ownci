//go:build linux

package supervisor

import (
	"encoding/json"
	"errors"
	"os"
	"time"
)

// Phase is where the runner is in its lifecycle.
type Phase string

const (
	PhaseStarting          Phase = "starting"
	PhaseConnected         Phase = "connected"
	PhaseListening         Phase = "listening"
	PhaseBusy              Phase = "busy"
	PhaseReconnecting      Phase = "reconnecting"
	PhaseStopping          Phase = "stopping"
	PhaseStopped           Phase = "stopped"
	PhaseCrashed           Phase = "crashed"
	PhaseRegistrationLost  Phase = "registration_lost"
	PhaseVersionDeprecated Phase = "version_deprecated"
	PhaseDuplicateSession  Phase = "duplicate_session"
)

// JobInfo describes the last finished job.
type JobInfo struct {
	Name       string    `json:"name"`
	Result     string    `json:"result"`
	FinishedAt time.Time `json:"finishedAt"`
}

// State is run/<name>.state.json, rewritten atomically on every transition.
type State struct {
	Name          string    `json:"name"`
	Phase         Phase     `json:"phase"`
	Since         time.Time `json:"since"`
	RunnerVersion string    `json:"runnerVersion,omitempty"`
	RunnerPID     int       `json:"runnerPid,omitempty"`
	CurrentJob    string    `json:"currentJob,omitempty"`
	LastJob       *JobInfo  `json:"lastJob,omitempty"`
	LastError     string    `json:"lastError,omitempty"`
	ExitCode      *int      `json:"exitCode,omitempty"`
	Restarts      int       `json:"restarts"`
	SupervisorPID int       `json:"supervisorPid"`
}

// Active reports whether the phase claims a live runner.
func (p Phase) Active() bool {
	switch p {
	case PhaseStarting, PhaseConnected, PhaseListening, PhaseBusy, PhaseReconnecting, PhaseStopping:
		return true
	}
	return false
}

// Failed reports whether the phase is a terminal failure.
func (p Phase) Failed() bool {
	switch p {
	case PhaseCrashed, PhaseRegistrationLost, PhaseVersionDeprecated, PhaseDuplicateSession:
		return true
	}
	return false
}

// Hint is what the user should do about a failure phase ("" otherwise).
func (p Phase) Hint() string {
	switch p {
	case PhaseVersionDeprecated:
		return "the runner could not self-update; run 'ownci setup --force' after upgrading ownci"
	case PhaseRegistrationLost:
		return "the registration no longer exists on GitHub; run 'ownci setup --force' to register again"
	case PhaseDuplicateSession:
		return "another runner process with this registration is running elsewhere; stop it first"
	case PhaseCrashed:
		return "see the runner log with 'ownci logs', and check 'ownci doctor'"
	}
	return ""
}

func (s *State) setPhase(p Phase, now time.Time) {
	if s.Phase != p {
		s.Phase, s.Since = p, now
	}
}

// Apply advances the state for one parsed output line and reports whether
// anything changed (and so must be persisted). Nothing moves a runner out of
// `stopping`: once a stop is requested only the process exit ends it.
func (s *State) Apply(ev Event, now time.Time) bool {
	stopping := s.Phase == PhaseStopping
	switch ev.Kind {
	case EvVersion:
		if s.RunnerVersion == ev.Version {
			return false
		}
		s.RunnerVersion = ev.Version
		return true
	case EvConnected:
		if s.Phase != PhaseStarting && s.Phase != PhaseReconnecting {
			return false
		}
		s.setPhase(PhaseConnected, now)
		return true
	case EvListening:
		if stopping {
			return false
		}
		s.setPhase(PhaseListening, now)
		s.CurrentJob = ""
		return true
	case EvJobStart:
		if stopping {
			return false
		}
		s.setPhase(PhaseBusy, now)
		s.CurrentJob = ev.Job
		return true
	case EvJobDone:
		s.LastJob = &JobInfo{Name: ev.Job, Result: ev.Result, FinishedAt: now}
		s.CurrentJob = ""
		if s.Phase == PhaseBusy {
			s.setPhase(PhaseListening, now)
		}
		return true
	case EvConnectError:
		if stopping || s.Phase == PhaseReconnecting {
			return false
		}
		s.setPhase(PhaseReconnecting, now)
		s.LastError = ev.Line
		return true
	case EvRegistrationLost:
		s.LastError = ev.Line
		return true
	case EvDuplicateSession:
		s.LastError = ev.Line
		return true
	}
	return false
}

// ReadState returns nil, nil when no state file exists.
func ReadState(path string) (*State, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var s State
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	return &s, nil
}
