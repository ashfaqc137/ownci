//go:build linux

package supervisor

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/ashfaqc137/ownci/internal/config"
	"github.com/ashfaqc137/ownci/internal/logging"
)

const (
	maxLineBytes = 1 << 20
	tailLines    = 20
)

// Options configure Run. Zero values select the production defaults; tests
// shorten the timings.
type Options struct {
	Paths        config.Paths
	Name         string
	Mode         string // ModeForeground | ModeDetached
	OwnciVersion string

	Echo io.Writer // runner output is copied here (foreground mode); may be nil
	Env  []string  // appended to the runner's environment

	Grace         time.Duration   // SIGINT → SIGKILL (default 30s)
	Backoff       []time.Duration // delays before restarts (default 5s, 15s, 45s)
	MaxRestarts   int             // within RestartWindow (default 3)
	RestartWindow time.Duration   // default 10m

	// Signals delivers stop requests. nil = SIGINT/SIGTERM of this process.
	Signals <-chan os.Signal
	// BeforeLaunch runs before every launch of the runner, restarts included.
	// An error ends the run in phase crashed.
	BeforeLaunch func(ctx context.Context) error

	Logger *slog.Logger
	Now    func() time.Time
}

func (o Options) withDefaults() Options {
	if o.Mode == "" {
		o.Mode = ModeForeground
	}
	if o.Grace <= 0 {
		o.Grace = 30 * time.Second
	}
	if len(o.Backoff) == 0 {
		o.Backoff = []time.Duration{5 * time.Second, 15 * time.Second, 45 * time.Second}
	}
	if o.MaxRestarts <= 0 {
		o.MaxRestarts = 3
	}
	if o.RestartWindow <= 0 {
		o.RestartWindow = 10 * time.Minute
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return o
}

// Run supervises the runner until a stop is requested or it fails for good.
// It returns the final state. The error is for problems of the supervisor
// itself (ErrAlreadyRunning, I/O); a crashed runner is a state, not an error.
func Run(ctx context.Context, o Options) (State, error) {
	o = o.withDefaults()
	if err := os.MkdirAll(o.Paths.RunDir, 0o700); err != nil {
		return State{}, fmt.Errorf("creating run directory: %w", err)
	}
	lock, err := acquireLock(o.Paths.LockFile(o.Name))
	if err != nil {
		return State{}, err
	}
	defer lock.Close() // releases the flock

	pidPath := o.Paths.PidFile(o.Name)
	now := o.Now()
	if err := writePidFile(pidPath, PidInfo{
		PID: os.Getpid(), Mode: o.Mode, StartedAt: now, OwnciVersion: o.OwnciVersion,
	}); err != nil {
		return State{}, fmt.Errorf("writing pid file: %w", err)
	}
	defer os.Remove(pidPath)

	logFile, err := os.OpenFile(o.Paths.RunnerLog(o.Name), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return State{}, fmt.Errorf("opening runner log: %w", err)
	}
	defer logFile.Close()

	sigs := o.Signals
	if sigs == nil {
		ch := make(chan os.Signal, 2)
		signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
		defer signal.Stop(ch)
		sigs = ch
	}

	t := &tracker{
		path: o.Paths.StateFile(o.Name), log: o.Logger, now: o.Now,
		sink: io.MultiWriter(logFile), echo: o.Echo,
		st: State{Name: o.Name, Phase: PhaseStarting, Since: now, SupervisorPID: os.Getpid()},
	}
	t.persist()

	s := &supervisor{o: o, t: t, sigs: sigs, dir: o.Paths.RunnerDir(o.Name)}
	return s.loop(ctx)
}

type supervisor struct {
	o    Options
	t    *tracker
	sigs <-chan os.Signal
	dir  string
}

func (s *supervisor) loop(ctx context.Context) (State, error) {
	t, o := s.t, s.o
	var restarts []time.Time
	for {
		if o.BeforeLaunch != nil {
			if err := o.BeforeLaunch(ctx); err != nil {
				t.finish(PhaseCrashed, nil, "preparing to start the runner: "+err.Error())
				return t.snapshot(), err
			}
		}
		if pids := killOrphans(s.dir); len(pids) > 0 {
			t.notice(fmt.Sprintf("stopped %d leftover runner process(es) from an earlier run: %v", len(pids), pids))
		}
		exit, stopped := s.runOnce(ctx)
		code := exit
		switch {
		case stopped:
			t.finish(PhaseStopped, &code, "")
			return t.snapshot(), nil
		case exit == 7:
			t.finish(PhaseVersionDeprecated, &code, "runner exited with code 7 (version deprecated)")
			return t.snapshot(), nil
		case t.sawRegistrationLost():
			t.finish(PhaseRegistrationLost, &code, "")
			return t.snapshot(), nil
		case t.sawDuplicateSession():
			t.finish(PhaseDuplicateSession, &code, "")
			return t.snapshot(), nil
		}

		// Crash or unexpected exit: restart with backoff, within a budget.
		cutoff := o.Now().Add(-o.RestartWindow)
		recent := restarts[:0]
		for _, r := range restarts {
			if r.After(cutoff) {
				recent = append(recent, r)
			}
		}
		restarts = recent
		if len(restarts) >= o.MaxRestarts {
			t.finish(PhaseCrashed, &code, strings.Join(t.tailCopy(), "\n"))
			return t.snapshot(), nil
		}
		delay := o.Backoff[min(len(restarts), len(o.Backoff)-1)]
		t.notice(fmt.Sprintf("runner exited with code %d; restarting in %s", exit, delay))
		t.update(func(st *State) {
			st.ExitCode = &code
			st.RunnerPID = 0
			st.setPhase(PhaseStarting, o.Now())
		})
		select {
		case <-time.After(delay):
		case <-s.sigs:
			t.finish(PhaseStopped, &code, "")
			return t.snapshot(), nil
		case <-ctx.Done():
			t.finish(PhaseStopped, &code, "")
			return t.snapshot(), nil
		}
		restarts = append(restarts, o.Now())
		t.update(func(st *State) { st.Restarts++ })
	}
}

// runOnce launches run.sh and waits for it. It returns the exit code (-1 if
// killed by a signal or never started) and whether a stop was requested.
func (s *supervisor) runOnce(ctx context.Context) (exitCode int, stopped bool) {
	t, o := s.t, s.o
	t.resetMarkers()
	cmd := newRunCmd(s.dir, o.Env)
	pr, pw, err := os.Pipe()
	if err != nil {
		t.notice("creating output pipe: " + err.Error())
		return -1, false
	}
	cmd.Stdout, cmd.Stderr = pw, pw
	if err := cmd.Start(); err != nil {
		pw.Close()
		pr.Close()
		t.notice("starting the runner: " + err.Error())
		return -1, false
	}
	pw.Close() // the child holds the write end now
	pgid := cmd.Process.Pid
	t.update(func(st *State) {
		st.RunnerPID = pgid
		st.ExitCode = nil
		st.setPhase(PhaseStarting, o.Now())
	})

	scanDone := make(chan struct{})
	go func() {
		defer close(scanDone)
		readLines(pr, t.onLine)
	}()
	waitCh := make(chan error, 1)
	go func() { waitCh <- cmd.Wait() }()

	var (
		graceC   <-chan time.Time
		ctxDone  = ctx.Done()
		sigs     = s.sigs
		stopping bool
	)
	requestStop := func() {
		if stopping {
			_ = syscall.Kill(-pgid, syscall.SIGKILL) // second request: no more patience
			return
		}
		stopping = true
		t.update(func(st *State) { st.setPhase(PhaseStopping, o.Now()) })
		_ = syscall.Kill(-pgid, syscall.SIGINT)
		graceC = time.After(o.Grace)
	}
wait:
	for {
		select {
		case <-waitCh:
			break wait
		case <-sigs:
			requestStop()
		case <-ctxDone:
			ctxDone = nil // handle once; later cancellation adds nothing
			requestStop()
		case <-graceC:
			t.notice("runner did not stop within the grace period; killing it")
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
			graceC = nil
		}
	}
	// Sweep anything left in the group (the listener can outlive run.sh).
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
	select {
	case <-scanDone:
	case <-time.After(2 * time.Second):
		pr.Close() // a stray process still holds the pipe
		<-scanDone
	}
	pr.Close()

	code := -1
	if ps := cmd.ProcessState; ps != nil {
		code = ps.ExitCode()
	}
	return code, stopping
}

func readLines(r io.Reader, fn func(string)) {
	br := bufio.NewReaderSize(r, 64*1024)
	var acc []byte
	for {
		part, isPrefix, err := br.ReadLine()
		if len(acc) < maxLineBytes {
			acc = append(acc, part...)
		}
		if !isPrefix && (len(part) > 0 || len(acc) > 0 || err == nil) {
			fn(string(acc))
			acc = acc[:0]
		}
		if err != nil {
			if len(acc) > 0 {
				fn(string(acc))
			}
			return
		}
	}
}

// tracker owns the State and the output sinks; every goroutine goes through it.
type tracker struct {
	mu   sync.Mutex
	st   State
	path string
	log  *slog.Logger
	now  func() time.Time
	sink io.Writer // runner log file
	echo io.Writer // terminal, may be nil
	tail []string

	regLost, dupSession bool
}

func (t *tracker) persist() {
	if err := writeJSONAtomic(t.path, t.st); err != nil {
		t.log.Warn("writing runner state failed", "path", t.path, "err", err)
	}
}

func (t *tracker) update(fn func(*State)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	fn(&t.st)
	t.persist()
}

func (t *tracker) snapshot() State {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.st
}

func (t *tracker) resetMarkers() {
	t.mu.Lock()
	t.regLost, t.dupSession = false, false
	t.mu.Unlock()
}

func (t *tracker) sawRegistrationLost() bool { t.mu.Lock(); defer t.mu.Unlock(); return t.regLost }
func (t *tracker) sawDuplicateSession() bool { t.mu.Lock(); defer t.mu.Unlock(); return t.dupSession }

func (t *tracker) tailCopy() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.tail...)
}

// finish records a terminal phase. A non-empty lastError replaces the one from
// the output (which is kept when lastError is "").
func (t *tracker) finish(p Phase, code *int, lastError string) {
	t.update(func(st *State) {
		st.setPhase(p, t.now())
		st.CurrentJob = ""
		st.RunnerPID = 0
		st.ExitCode = code
		if lastError != "" {
			st.LastError = lastError
		}
	})
}

// emit writes one already-redacted line to the log and the terminal.
func (t *tracker) emit(line string) {
	fmt.Fprintln(t.sink, line)
	if t.echo != nil {
		fmt.Fprintln(t.echo, line)
	}
}

// notice records an OwnCI message in the runner log, marked as not coming from
// the runner.
func (t *tracker) notice(msg string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.emit(logging.Redact("[ownci] " + msg))
}

func (t *tracker) onLine(line string) {
	line = logging.Redact(strings.TrimRight(line, "\r"))
	t.mu.Lock()
	defer t.mu.Unlock()
	t.emit(line)
	t.tail = append(t.tail, line)
	if len(t.tail) > tailLines {
		t.tail = t.tail[len(t.tail)-tailLines:]
	}
	ev := ParseLine(line)
	switch ev.Kind {
	case EvRegistrationLost:
		t.regLost = true
	case EvDuplicateSession:
		t.dupSession = true
	}
	if t.st.Apply(ev, t.now()) {
		t.persist()
	}
}

// LastLines returns up to n trailing lines of the file at path ("" → none).
func LastLines(path string, n int) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	const window = 64 * 1024
	fi, err := f.Stat()
	if err != nil {
		return nil
	}
	off := max(fi.Size()-window, 0)
	buf := make([]byte, fi.Size()-off)
	if _, err := f.ReadAt(buf, off); err != nil && err != io.EOF {
		return nil
	}
	text := strings.TrimRight(string(buf), "\n")
	if text == "" {
		return nil
	}
	lines := strings.Split(text, "\n")
	if off > 0 && len(lines) > 1 {
		lines = lines[1:] // the first line may be cut in half
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

func newRunCmd(dir string, extraEnv []string) *execCmd {
	return newExecCmd(filepath.Join(dir, "run.sh"), dir, extraEnv)
}
