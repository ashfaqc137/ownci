package runner

// Error is a failure with an actionable hint. The cli layer renders it as
// "Error: …/Hint: …" through the UserHint method (this package must not
// import cli).
type Error struct {
	Msg  string
	Hint string
	Err  error // optional cause, for errors.Is
}

func (e *Error) Error() string    { return e.Msg }
func (e *Error) Unwrap() error    { return e.Err }
func (e *Error) UserHint() string { return e.Hint }
