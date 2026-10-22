//go:build linux

package supervisor

import (
	"os"
	"os/exec"
	"syscall"
)

type execCmd = exec.Cmd

// newExecCmd builds the runner command (00-context §7): the env vars make
// run.sh forward SIGINT and report the real exit code, and the runner gets its
// own process group so a stop reaches the whole tree. In container mode argv
// is `docker run …`, which forwards the signals into the container.
func newExecCmd(argv []string, dir string, extraEnv []string) *execCmd {
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"RUNNER_MANUALLY_TRAP_SIG=1",
		"ACTIONS_RUNNER_RETURN_VERSION_DEPRECATED_EXIT_CODE=1")
	cmd.Env = append(cmd.Env, extraEnv...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return cmd
}
