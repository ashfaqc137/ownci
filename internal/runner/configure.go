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
// with --no-default-labels, so the runner carries exactly these labels.
type ConfigureOptions struct {
	URL     string
	Token   string // registration token; never logged or printed
	Name    string
	Labels  []string
	Replace bool
}

// TokenEnv carries the registration or removal token to config.sh. The
// runner reads ACTIONS_RUNNER_INPUT_<arg> like --<arg>; unlike an argument,
// the environment is not visible to other users in /proc/<pid>/cmdline.
const TokenEnv = "ACTIONS_RUNNER_INPUT_TOKEN"

// ConfigureArgs returns config.sh's arguments. The token is not among them: it
// is passed in TokenEnv.
func ConfigureArgs(o ConfigureOptions) []string {
	args := []string{
		"--unattended", "--url", o.URL, "--name", o.Name,
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
		Args: ConfigureArgs(o),
		Dir:  dir,
		Env:  []string{TokenEnv + "=" + o.Token},
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

// Remove unregisters the runner in dir by running `./config.sh remove`. The
// removal token is passed in TokenEnv and never logged. On failure the error carries
// the last 20 lines of the (redacted) output.
func Remove(ctx context.Context, ex execx.Runner, dir, token string) error {
	logging.RegisterSecret(token)
	res, err := ex.Run(ctx, execx.Cmd{
		Name: filepath.Join(dir, "config.sh"),
		Args: []string{"remove"},
		Dir:  dir,
		Env:  []string{TokenEnv + "=" + token},
	})
	if err != nil {
		return &Error{Msg: fmt.Sprintf("could not run config.sh remove: %v", err), Err: err,
			Hint: "Check that " + dir + " is intact."}
	}
	if res.ExitCode != 0 {
		return &Error{
			Msg:  fmt.Sprintf("config.sh remove failed (exit %d):\n%s", res.ExitCode, tailLines(res, 20)),
			Hint: "The registration can also be removed on GitHub (repository Settings → Actions → Runners).",
		}
	}
	return nil
}
