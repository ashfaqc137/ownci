//go:build linux

package supervisor

import (
	"regexp"
	"strings"
)

// EventKind classifies one line of runner output (00-context §7 markers).
type EventKind int

const (
	EvNone EventKind = iota
	EvConnected
	EvVersion
	EvListening
	EvJobStart
	EvJobDone
	EvConnectError
	EvRegistrationLost
	EvDuplicateSession
	EvExiting
)

// Event is the parsed form of a marker line.
type Event struct {
	Kind    EventKind
	Version string // EvVersion
	Job     string // EvJobStart, EvJobDone
	Result  string // EvJobDone
	Line    string // the original line (used for error text)
}

var (
	versionRe = regexp.MustCompile(`Current runner version: '([^']*)'`)
	jobDoneRe = regexp.MustCompile(`Job (.+) completed with result: (\S+)`)
	jobRunRe  = regexp.MustCompile(`Running job: (.+?)\s*$`)
)

// ParseLine recognises the runner's stdout markers by substring, so the UTC
// timestamp prefix and the "√ " decoration do not matter. Unknown lines yield
// EvNone.
func ParseLine(line string) Event {
	ev := Event{Line: line}
	switch {
	case strings.Contains(line, "The runner no longer exists on the server"),
		strings.Contains(line, "registration has been deleted from the server"):
		ev.Kind = EvRegistrationLost
	case strings.Contains(line, "A session for this runner already exists."):
		ev.Kind = EvDuplicateSession
	case strings.Contains(line, "Runner connect error:"):
		ev.Kind = EvConnectError
	case strings.Contains(line, "Listening for Jobs"):
		ev.Kind = EvListening
	case jobDoneRe.MatchString(line):
		m := jobDoneRe.FindStringSubmatch(line)
		ev.Kind, ev.Job, ev.Result = EvJobDone, m[1], m[2]
	case jobRunRe.MatchString(line):
		ev.Kind, ev.Job = EvJobStart, jobRunRe.FindStringSubmatch(line)[1]
	case versionRe.MatchString(line):
		ev.Kind, ev.Version = EvVersion, versionRe.FindStringSubmatch(line)[1]
	case strings.Contains(line, "Connected to GitHub"):
		ev.Kind = EvConnected
	case strings.Contains(line, "Exiting..."):
		ev.Kind = EvExiting
	}
	return ev
}
