package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"
)

// Exit codes (00-context §5).
const (
	exitOK        = 0
	exitFailure   = 1
	exitUsage     = 2
	exitDoctorBad = 3 // used by doctor (t03)
)

// UserError is a failure the user can act on. Hint is optional.
type UserError struct {
	Msg  string
	Hint string
	Code int
}

func (e *UserError) Error() string { return e.Msg }

// hinted is implemented by errors from other packages (docker.Error,
// runner.Error) that carry an actionable hint, since they cannot import cli.
type hinted interface{ UserHint() string }

// exitError ends the process with Code without printing anything; the command
// has already reported the problem itself (e.g. doctor's FAIL lines).
type exitError struct{ Code int }

func (e *exitError) Error() string { return fmt.Sprintf("exit status %d", e.Code) }

// ErrNotImplemented is returned by commands that are still stubs.
var ErrNotImplemented = &UserError{
	Msg:  "not implemented",
	Hint: "This command is not implemented yet.",
	Code: exitUsage,
}

// renderError prints err to w as "Error: ..." (+ "Hint: ...") and returns the
// process exit code. Shared by Execute and the tests.
func renderError(w io.Writer, err error) int {
	if err == nil {
		return exitOK
	}
	var ee *exitError
	if errors.As(err, &ee) {
		return ee.Code
	}
	var ue *UserError
	if errors.As(err, &ue) {
		fmt.Fprintf(w, "Error: %s\n", ue.Msg)
		if ue.Hint != "" {
			fmt.Fprintf(w, "Hint: %s\n", ue.Hint)
		}
		if ue.Code != 0 {
			return ue.Code
		}
		return exitFailure
	}
	fmt.Fprintf(w, "Error: %s\n", err)
	var h hinted
	if errors.As(err, &h) {
		if hint := h.UserHint(); hint != "" {
			fmt.Fprintf(w, "Hint: %s\n", hint)
		}
		return exitFailure
	}
	if isUsageError(err) {
		return exitUsage
	}
	return exitFailure
}

// isUsageError recognises cobra's plain-string usage errors (unknown
// command/flag, bad flag value, missing flag argument).
func isUsageError(err error) bool {
	msg := err.Error()
	for _, p := range []string{
		"unknown command", "unknown flag", "unknown shorthand flag",
		"flag needs an argument", "invalid argument", "required flag",
		"accepts ", "requires at least", "requires at most",
	} {
		if strings.Contains(msg, p) {
			return true
		}
	}
	return false
}

// hintOf returns the hint an error carries, or "".
func hintOf(err error) string {
	var h hinted
	if errors.As(err, &h) {
		return h.UserHint()
	}
	return ""
}
