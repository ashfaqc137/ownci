// Package doctor runs read-only health checks and collects their results.
package doctor

import (
	"context"
	"fmt"
)

// Level is the severity of a check result.
type Level int

const (
	Pass Level = iota
	Warn
	Fail
)

func (l Level) String() string {
	switch l {
	case Pass:
		return "pass"
	case Warn:
		return "warn"
	default:
		return "fail"
	}
}

// MarshalText makes JSON output use "pass"/"warn"/"fail".
func (l Level) MarshalText() ([]byte, error) { return []byte(l.String()), nil }

// Result is the outcome of one check. Hint is set for Warn and Fail.
type Result struct {
	Name   string `json:"name"`
	Level  Level  `json:"level"`
	Detail string `json:"detail"`
	Hint   string `json:"hint,omitempty"`
}

// Check is a named, self-contained check.
type Check struct {
	Name string
	Run  func(ctx context.Context) Result
}

// Report is the ordered list of results.
type Report struct {
	Results []Result `json:"results"`
}

// Failed reports whether any check failed.
func (r Report) Failed() bool { return r.Count(Fail) > 0 }

// Count returns how many results have the given level.
func (r Report) Count(l Level) int {
	n := 0
	for _, res := range r.Results {
		if res.Level == l {
			n++
		}
	}
	return n
}

// Run executes the checks sequentially. A panicking check becomes a Fail.
func Run(ctx context.Context, checks []Check) Report {
	rep := Report{Results: make([]Result, 0, len(checks))}
	for _, c := range checks {
		rep.Results = append(rep.Results, runOne(ctx, c))
	}
	return rep
}

func runOne(ctx context.Context, c Check) (res Result) {
	defer func() {
		if p := recover(); p != nil {
			res = Result{Name: c.Name, Level: Fail, Detail: fmt.Sprintf("check panicked: %v", p),
				Hint: "This is a bug in ownci; please report it."}
		}
	}()
	res = c.Run(ctx)
	res.Name = c.Name
	return res
}
