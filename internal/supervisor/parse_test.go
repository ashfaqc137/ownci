//go:build linux

package supervisor

import (
	"testing"
	"time"
)

func TestParseLine(t *testing.T) {
	tests := []struct {
		line string
		want Event
	}{
		{"√ Connected to GitHub", Event{Kind: EvConnected}},
		{"Connected to GitHub", Event{Kind: EvConnected}},
		{"Current runner version: '2.338.0'", Event{Kind: EvVersion, Version: "2.338.0"}},
		{"2026-10-15 10:02:15Z: Listening for Jobs", Event{Kind: EvListening}},
		{"2026-10-15 10:03:00Z: Running job: build", Event{Kind: EvJobStart, Job: "build"}},
		{"2026-10-15 10:03:00Z: Running job: build and test (linux)", Event{Kind: EvJobStart, Job: "build and test (linux)"}},
		{"2026-10-15 10:05:40Z: Job build completed with result: Succeeded", Event{Kind: EvJobDone, Job: "build", Result: "Succeeded"}},
		{"2026-10-15 10:05:40Z: Job build and test (linux) completed with result: Failed", Event{Kind: EvJobDone, Job: "build and test (linux)", Result: "Failed"}},
		{"Runner connect error: The SSL connection could not be established", Event{Kind: EvConnectError}},
		{"The runner no longer exists on the server. Please reconfigure the runner.", Event{Kind: EvRegistrationLost}},
		{"Failed: registration has been deleted from the server", Event{Kind: EvRegistrationLost}},
		{"A session for this runner already exists.", Event{Kind: EvDuplicateSession}},
		{"Exiting...", Event{Kind: EvExiting}},
		{"√ Settings Saved.", Event{Kind: EvNone}},
		{"", Event{Kind: EvNone}},
	}
	for _, tc := range tests {
		got := ParseLine(tc.line)
		tc.want.Line = tc.line
		if got != tc.want {
			t.Errorf("ParseLine(%q) = %+v, want %+v", tc.line, got, tc.want)
		}
	}
}

func TestStateApply(t *testing.T) {
	t0 := time.Date(2026, 10, 15, 10, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Minute)
	s := State{Phase: PhaseStarting, Since: t0}

	steps := []struct {
		ev      Event
		changed bool
		phase   Phase
	}{
		{Event{Kind: EvConnected}, true, PhaseConnected},
		{Event{Kind: EvVersion, Version: "2.1.0"}, true, PhaseConnected},
		{Event{Kind: EvVersion, Version: "2.1.0"}, false, PhaseConnected},
		{Event{Kind: EvListening}, true, PhaseListening},
		{Event{Kind: EvJobStart, Job: "build"}, true, PhaseBusy},
		{Event{Kind: EvJobDone, Job: "build", Result: "Succeeded"}, true, PhaseListening},
		{Event{Kind: EvConnectError, Line: "Runner connect error: x"}, true, PhaseReconnecting},
		{Event{Kind: EvConnectError}, false, PhaseReconnecting},
		{Event{Kind: EvConnected}, true, PhaseConnected},
		{Event{Kind: EvListening}, true, PhaseListening},
		{Event{Kind: EvNone}, false, PhaseListening},
	}
	for i, st := range steps {
		if got := s.Apply(st.ev, t1); got != st.changed || s.Phase != st.phase {
			t.Fatalf("step %d (%+v): changed=%v phase=%s, want %v %s", i, st.ev, got, s.Phase, st.changed, st.phase)
		}
	}
	if s.RunnerVersion != "2.1.0" {
		t.Errorf("RunnerVersion = %q", s.RunnerVersion)
	}
	if s.LastJob == nil || s.LastJob.Name != "build" || s.LastJob.Result != "Succeeded" || !s.LastJob.FinishedAt.Equal(t1) {
		t.Errorf("LastJob = %+v", s.LastJob)
	}
	if s.CurrentJob != "" {
		t.Errorf("CurrentJob = %q", s.CurrentJob)
	}
}

func TestStateApplyStoppingIsSticky(t *testing.T) {
	now := time.Now()
	s := State{Phase: PhaseStopping}
	for _, ev := range []Event{{Kind: EvListening}, {Kind: EvJobStart, Job: "x"}, {Kind: EvConnectError}, {Kind: EvConnected}} {
		s.Apply(ev, now)
		if s.Phase != PhaseStopping {
			t.Fatalf("%+v moved a stopping runner to %s", ev, s.Phase)
		}
	}
}

func TestPhaseClassification(t *testing.T) {
	for _, p := range []Phase{PhaseStarting, PhaseConnected, PhaseListening, PhaseBusy, PhaseReconnecting, PhaseStopping} {
		if !p.Active() || p.Failed() {
			t.Errorf("%s should be active, not failed", p)
		}
	}
	for _, p := range []Phase{PhaseCrashed, PhaseRegistrationLost, PhaseVersionDeprecated, PhaseDuplicateSession} {
		if p.Active() || !p.Failed() || p.Hint() == "" {
			t.Errorf("%s should be failed with a hint", p)
		}
	}
	if PhaseStopped.Active() || PhaseStopped.Failed() {
		t.Error("stopped is neither active nor failed")
	}
}
