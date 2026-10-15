// Package logging sets up slog with mandatory secret redaction.
package logging

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
)

// Options configures Setup.
type Options struct {
	LogFile string    // always written, at Debug level
	Verbose bool      // also log to Stderr at Debug level
	Stderr  io.Writer // defaults to os.Stderr; used only when Verbose
}

// Setup opens the log file (append, 0600) and returns a redacting logger.
// Without Verbose nothing goes to stderr: the CLI prints user messages itself.
// The returned Closer closes the log file.
func Setup(opts Options) (*slog.Logger, io.Closer, error) {
	if opts.LogFile == "" {
		return nil, nil, errors.New("logging: no log file given")
	}
	f, err := os.OpenFile(opts.LogFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, nil, fmt.Errorf("opening log file: %w", err)
	}
	stderr := opts.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}
	if !opts.Verbose {
		stderr = nil
	}
	return newLogger(f, stderr), f, nil
}

// newLogger builds the redacting logger over a file sink and an optional
// stderr sink. Redaction wraps both, so neither can receive a secret.
func newLogger(file, stderr io.Writer) *slog.Logger {
	hopts := &slog.HandlerOptions{Level: slog.LevelDebug}
	h := slog.Handler(slog.NewTextHandler(file, hopts))
	if stderr != nil {
		h = slog.NewMultiHandler(h, slog.NewTextHandler(stderr, hopts))
	}
	return slog.New(redactHandler{h})
}
