package runner

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/ashfaqc137/ownci/internal/execx"
	"github.com/ashfaqc137/ownci/internal/logging"
)

// ConfigureOptions are the inputs to `config.sh`. Labels are passed together
// with --no-default-labels (D2), so the runner carries exactly these labels.
type ConfigureOptions struct {
	URL     string
	Token   string // registration token; never logged or printed
	Name    string
	Labels  []string
	Replace bool
}

// ConfigureArgs returns config.sh's arguments. With maskToken the token is
// replaced by "***", which is what dry-run output and logs show.
func ConfigureArgs(o ConfigureOptions, maskToken bool) []string {
	tok := o.Token
	if maskToken {
		tok = "***"
	}
	args := []string{
		"--unattended", "--url", o.URL, "--token", tok, "--name", o.Name,
		"--labels", strings.Join(o.Labels, ","), "--no-default-labels", "--work", "_work",
	}
	if o.Replace {
		args = append(args, "--replace")
	}
	return args
}

// Configure registers the runner in dir by running ./config.sh. On failure
// the error carries the last 20 lines of its (redacted) output.
func Configure(ctx context.Context, ex execx.Runner, dir string, o ConfigureOptions) error {
	logging.RegisterSecret(o.Token)
	res, err := ex.Run(ctx, execx.Cmd{
		Name: filepath.Join(dir, "config.sh"),
		Args: ConfigureArgs(o, false),
		Dir:  dir,
	})
	if err != nil {
		return &Error{
			Msg:  fmt.Sprintf("could not run config.sh: %v", err),
			Hint: "Run 'ownci doctor', and check that " + dir + " is intact (re-run 'ownci setup --force' to recreate it).",
			Err:  err,
		}
	}
	if res.ExitCode != 0 {
		return &Error{
			Msg:  fmt.Sprintf("config.sh failed (exit %d):\n%s", res.ExitCode, tailLines(res, 20)),
			Hint: "Run 'ownci doctor' to check this machine's dependencies, then retry 'ownci setup'.",
		}
	}
	return nil
}

// tailLines returns the last n non-empty lines of config.sh's output, redacted.
func tailLines(res execx.Result, n int) string {
	var all bytes.Buffer
	all.Write(res.Stdout)
	all.WriteByte('\n')
	all.Write(res.Stderr)
	var lines []string
	for _, l := range strings.Split(all.String(), "\n") {
		if l = strings.TrimRight(l, " \r"); l != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return logging.Redact(strings.Join(lines, "\n"))
}
