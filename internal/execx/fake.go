package execx

import (
	"context"
	"fmt"
	"os/exec"
	"slices"
	"sync"
)

// Fake is a scripted Runner for tests. Register responses with On and
// SetPath, run the code under test, then inspect Calls.
//
//	f := execx.NewFake()
//	f.SetPath("docker", "/usr/bin/docker")
//	f.On("docker", "version").Stdout(`{"Server":{}}`)
//	f.On("docker", "run").Exit(125).Stderr("boom")
//	f.On("ldconfig").Fail(errors.New("cannot start"))
//
// A call matches the rule with the longest args prefix; among equals the
// last registered wins. An unmatched call returns an error so tests fail
// loudly instead of silently succeeding.
type Fake struct {
	mu    sync.Mutex
	rules []*Rule
	paths map[string]string
	calls []Cmd
}

// Rule is one scripted response.
type Rule struct {
	name   string
	prefix []string
	res    Result
	err    error
}

// NewFake returns an empty Fake: nothing is on PATH and no command is scripted.
func NewFake() *Fake { return &Fake{paths: map[string]string{}} }

// On scripts the response for name run with args starting with argPrefix.
// With no further calls the response is an empty success.
func (f *Fake) On(name string, argPrefix ...string) *Rule {
	r := &Rule{name: name, prefix: argPrefix}
	f.mu.Lock()
	f.rules = append(f.rules, r)
	f.mu.Unlock()
	return r
}

func (r *Rule) Stdout(s string) *Rule { r.res.Stdout = []byte(s); return r }
func (r *Rule) Stderr(s string) *Rule { r.res.Stderr = []byte(s); return r }
func (r *Rule) Exit(code int) *Rule   { r.res.ExitCode = code; return r }

// Fail makes Run return err (as if the command could not be started).
func (r *Rule) Fail(err error) *Rule { r.err = err; return r }

// SetPath puts name on the fake PATH at path.
func (f *Fake) SetPath(name, path string) {
	f.mu.Lock()
	f.paths[name] = path
	f.mu.Unlock()
}

// Calls returns every command passed to Run, in order.
func (f *Fake) Calls() []Cmd {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

func (f *Fake) LookPath(name string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if p, ok := f.paths[name]; ok {
		return p, nil
	}
	return "", &exec.Error{Name: name, Err: exec.ErrNotFound}
}

func (f *Fake) Run(_ context.Context, c Cmd) (Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, c)
	var best *Rule
	for _, r := range f.rules {
		if r.name == c.Name && len(r.prefix) <= len(c.Args) &&
			slices.Equal(r.prefix, c.Args[:len(r.prefix)]) &&
			(best == nil || len(r.prefix) >= len(best.prefix)) {
			best = r
		}
	}
	if best == nil {
		return Result{}, fmt.Errorf("execx.Fake: unexpected command: %s", c)
	}
	return best.res, best.err
}
