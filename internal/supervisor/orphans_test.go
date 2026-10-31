//go:build linux

package supervisor

import "testing"

func TestRunnerProcess(t *testing.T) {
	const prefix = "/data/runners/r/"
	for _, c := range []struct {
		args []string
		want bool
	}{
		{[]string{"/data/runners/r/bin/Runner.Listener", "run"}, true},
		{[]string{"/data/runners/r/bin/Runner.Worker", "spawnclient", "1", "2"}, true},
		{[]string{"/bin/bash", "/data/runners/r/run.sh"}, true},
		{[]string{"bash", "/data/runners/r/run-helper.sh"}, true},
		{[]string{"vim", "/data/runners/r/.env"}, false},
		{[]string{"less", "/data/runners/r/_diag/Runner_1.log"}, false},
		{[]string{"/bin/bash", "/data/runners/r/.env"}, false},
		{[]string{"/bin/bash", "/data/runners/other/run.sh"}, false},
		{[]string{"/data/runners/rx/bin/Runner.Listener"}, false},
		{nil, false},
	} {
		if got := runnerProcess(c.args, prefix); got != c.want {
			t.Errorf("runnerProcess(%q) = %v, want %v", c.args, got, c.want)
		}
	}
}
