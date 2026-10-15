// Package execx is the seam through which ownci runs external commands, so
// tests can replace them with a Fake.
package execx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/ashfaqc137/ownci/internal/logging"
)

// Cmd describes one command invocation.
type Cmd struct {
	Name  string
	Args  []string
	Dir   string
	Env   []string // appended to os.Environ()
	Stdin io.Reader
}

// String renders the command line for logs (callers must still redact).
func (c Cmd) String() string { return strings.Join(append([]string{c.Name}, c.Args...), " ") }

// Result is the outcome of a command that ran to completion.
type Result struct {
	Stdout, Stderr []byte
	ExitCode       int
}

// Runner runs external commands.
type Runner interface {
	// Run returns an error only when the command could not be started or the
	// context ended; a non-zero exit is reported through Result.ExitCode.
	Run(ctx context.Context, c Cmd) (Result, error)
	LookPath(name string) (string, error)
}

type osRunner struct{ log *slog.Logger }

// NewOS returns a Runner backed by os/exec. It logs every command line
// (redacted) and its exit code at debug level. logger may be nil.
func NewOS(logger *slog.Logger) Runner {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return osRunner{log: logger}
}

func (o osRunner) LookPath(name string) (string, error) { return exec.LookPath(name) }

func (o osRunner) Run(ctx context.Context, c Cmd) (Result, error) {
	cmd := exec.CommandContext(ctx, c.Name, c.Args...)
	cmd.Dir = c.Dir
	cmd.Stdin = c.Stdin
	if len(c.Env) > 0 {
		cmd.Env = append(os.Environ(), c.Env...)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	// Don't hang on pipes inherited by grandchildren after the kill.
	cmd.WaitDelay = 2 * time.Second

	line := logging.Redact(c.String())
	start := time.Now()
	err := cmd.Run()
	res := Result{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}

	var exitErr *exec.ExitError
	switch {
	case ctx.Err() != nil:
		o.log.Debug("exec cancelled", "cmd", line, "err", ctx.Err())
		return res, fmt.Errorf("running %s: %w", c.Name, ctx.Err())
	case errors.As(err, &exitErr):
		res.ExitCode = exitErr.ExitCode()
	case err != nil:
		o.log.Debug("exec failed to start", "cmd", line, "err", err)
		return res, fmt.Errorf("running %s: %w", c.Name, err)
	}
	o.log.Debug("exec", "cmd", line, "exit", res.ExitCode, "took", time.Since(start).Round(time.Millisecond))
	return res, nil
}
