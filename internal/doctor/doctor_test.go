package doctor

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/ashfaqc137/ownci/internal/execx"
	"github.com/ashfaqc137/ownci/internal/platform"
)

func TestRunOrderAndPanicRecovery(t *testing.T) {
	var order []string
	mk := func(name string, f func() Result) Check {
		return Check{Name: name, Run: func(context.Context) Result { order = append(order, name); return f() }}
	}
	rep := Run(context.Background(), []Check{
		mk("a", func() Result { return Result{Level: Pass} }),
		mk("b", func() Result { panic("kaboom") }),
		mk("c", func() Result { return Result{Level: Warn, Hint: "h"} }),
	})
	if strings.Join(order, ",") != "a,b,c" || len(rep.Results) != 3 {
		t.Fatalf("order = %v, results = %v", order, rep.Results)
	}
	if rep.Results[1].Level != Fail || !strings.Contains(rep.Results[1].Detail, "kaboom") || rep.Results[1].Name != "b" {
		t.Errorf("panic result = %+v", rep.Results[1])
	}
	if !rep.Failed() || rep.Count(Pass) != 1 || rep.Count(Warn) != 1 || rep.Count(Fail) != 1 {
		t.Errorf("counts wrong: %+v", rep)
	}
}

func TestReportJSON(t *testing.T) {
	b, _ := json.Marshal(Report{Results: []Result{{Name: "x", Level: Warn, Detail: "d", Hint: "h"}}})
	if !strings.Contains(string(b), `"level":"warn"`) {
		t.Errorf("json = %s", b)
	}
}

func run(c Check) Result { return Run(context.Background(), []Check{c}).Results[0] }

func TestPlatformCheck(t *testing.T) {
	ok := platform.DetectWith(func(string) ([]byte, error) { return nil, errors.New("x") }, func(string) string { return "" }, "linux", "amd64")
	if r := run(PlatformCheck(ok)); r.Level != Pass || !strings.Contains(r.Detail, "linux-x64") {
		t.Errorf("%+v", r)
	}
	bad := platform.Info{OS: "darwin", Arch: "arm64"}
	if r := run(PlatformCheck(bad)); r.Level != Fail || r.Hint == "" {
		t.Errorf("%+v", r)
	}
}

func TestWSLCheck(t *testing.T) {
	for _, tc := range []struct {
		info platform.Info
		want string
	}{
		{platform.Info{}, "not running under WSL"},
		{platform.Info{IsWSL: true, WSLDistro: "Ubuntu", KernelRelease: "6.1-microsoft-standard-WSL2"}, "WSL2 (Ubuntu)"},
		{platform.Info{IsWSL: true}, "WSL"},
	} {
		if r := run(WSLCheck(tc.info)); r.Level != Pass || r.Detail != tc.want {
			t.Errorf("got %+v, want %q", r, tc.want)
		}
	}
}

func TestUserCheck(t *testing.T) {
	if r := run(UserCheck(func() int { return 1000 })); r.Level != Pass {
		t.Errorf("%+v", r)
	}
	if r := run(UserCheck(func() int { return 0 })); r.Level != Fail || !strings.Contains(r.Hint, "normal user") {
		t.Errorf("%+v", r)
	}
}

func TestDataDirCheck(t *testing.T) {
	dir := t.TempDir()
	if r := run(DataDirCheck(dir)); r.Level != Pass {
		t.Errorf("%+v", r)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("probe file left behind: %v", entries)
	}
	if r := run(DataDirCheck(dir + "/missing")); r.Level != Fail || r.Hint == "" {
		t.Errorf("%+v", r)
	}
}

func TestRunnerDepsCheck(t *testing.T) {
	withLdconfig := func(out string, exit int) *execx.Fake {
		f := execx.NewFake()
		f.SetPath("ldconfig", "/sbin/ldconfig")
		f.On("/sbin/ldconfig", "-p").Stdout(out).Exit(exit)
		return f
	}
	if r := run(RunnerDepsCheck(withLdconfig("libicuuc.so.74 (libc6,x86-64) => /lib/libicuuc.so.74", 0))); r.Level != Pass {
		t.Errorf("found: %+v", r)
	}
	if r := run(RunnerDepsCheck(withLdconfig("libc.so.6 => /lib/libc.so.6", 0))); r.Level != Fail || !strings.Contains(r.Hint, "apt-get install -y libicu-dev") {
		t.Errorf("missing: %+v", r)
	}
	if r := run(RunnerDepsCheck(withLdconfig("", 1))); r.Level != Warn {
		t.Errorf("ldconfig fails: %+v", r)
	}
	if r := run(RunnerDepsCheck(execx.NewFake())); r.Level != Warn || r.Hint == "" {
		t.Errorf("no ldconfig: %+v", r)
	}
	f := execx.NewFake()
	f.SetPath("/sbin/ldconfig", "/sbin/ldconfig") // only found via the fallback path
	f.On("/sbin/ldconfig", "-p").Stdout("libicuuc")
	if r := run(RunnerDepsCheck(f)); r.Level != Pass {
		t.Errorf("fallback path: %+v", r)
	}
}

func TestDockerCheck(t *testing.T) {
	f := execx.NewFake()
	f.SetPath("docker", "/usr/bin/docker")
	f.On("docker", "version").Stdout(`{"Server":{"Version":"27.0.3","Os":"linux"}}`)
	if r := run(DockerCheck(f)); r.Level != Pass || r.Detail != "27.0.3 (linux)" {
		t.Errorf("%+v", r)
	}
	f = execx.NewFake()
	f.SetPath("docker", "/usr/bin/docker")
	f.On("docker", "version").Stderr("Cannot connect to the Docker daemon").Exit(1)
	if r := run(DockerCheck(f)); r.Level != Fail || r.Hint == "" {
		t.Errorf("%+v", r)
	}
}

func TestGitCheck(t *testing.T) {
	f := execx.NewFake()
	f.SetPath("git", "/usr/bin/git")
	if r := run(GitCheck(f)); r.Level != Pass {
		t.Errorf("%+v", r)
	}
	if r := run(GitCheck(execx.NewFake())); r.Level != Warn || r.Hint == "" {
		t.Errorf("%+v", r)
	}
}

func TestRunnerContainerCheck(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	run := func(f *execx.Fake, dir string) Result { return RunnerContainerCheck(f, dir, "img").Run(ctx) }

	f := execx.NewFake()
	f.On("docker", "image", "inspect").Stdout("sha256:x")
	f.On("docker", "run").Stdout("ok")
	if r := run(f, dir); r.Level != Pass {
		t.Errorf("healthy: %+v", r)
	}

	f = execx.NewFake()
	f.On("docker", "image", "inspect").Exit(1)
	if r := run(f, dir); r.Level != Warn || !strings.Contains(r.Hint, "docker pull img") {
		t.Errorf("image missing: %+v", r)
	}

	f = execx.NewFake()
	f.On("docker", "image", "inspect").Stdout("sha256:x")
	f.On("docker", "run").Exit(3).Stdout("missing: setpriv")
	if r := run(f, dir); r.Level != Fail || !strings.Contains(r.Detail, "lacks: setpriv") || r.Hint == "" {
		t.Errorf("probe failure: %+v", r)
	}

	f = execx.NewFake()
	f.On("docker", "image", "inspect").Stdout("sha256:x")
	f.On("docker", "run").Stdout("optional: git\nok")
	if r := run(f, dir); r.Level != Warn || !strings.Contains(r.Detail, "git is missing") {
		t.Errorf("recommended tool missing: %+v", r)
	}

	if r := run(execx.NewFake(), "/mnt/c/ownci/runners/x"); r.Level != Fail || !strings.Contains(r.Detail, "Windows drive") {
		t.Errorf("windows path: %+v", r)
	}
}
