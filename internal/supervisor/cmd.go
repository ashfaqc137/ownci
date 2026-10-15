//go:build linux

package supervisor

import (
	"os"
	"os/exec"
	"syscall"
)

type execCmd = exec.Cmd

// newExecCmd builds the run.sh command (00-context §7): the env vars make
// run.sh forward SIGINT and report the real exit code, and the runner gets its
// own process group so a stop reaches the whole tree.
func newExecCmd(script, dir string, extraEnv []string) *execCmd {
	cmd := exec.Command(script)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"RUNNER_MANUALLY_TRAP_SIG=1",
		"ACTIONS_RUNNER_RETURN_VERSION_DEPRECATED_EXIT_CODE=1")
	cmd.Env = append(cmd.Env, extraEnv...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return cmd
}
