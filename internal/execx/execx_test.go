package execx

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestOSRunOutputAndExit(t *testing.T) {
	res, err := NewOS(nil).Run(context.Background(), Cmd{Name: "bash", Args: []string{"-c", "echo out; echo err >&2; exit 3"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 3 || string(res.Stdout) != "out\n" || string(res.Stderr) != "err\n" {
		t.Fatalf("got %+v", res)
	}
}

func TestOSRunEnvAndStdin(t *testing.T) {
	res, err := NewOS(nil).Run(context.Background(), Cmd{
		Name: "bash", Args: []string{"-c", `read x; echo "$x $FOO"`},
		Env: []string{"FOO=bar"}, Stdin: strings.NewReader("in\n"),
	})
	if err != nil || string(res.Stdout) != "in bar\n" {
		t.Fatalf("got %q, %v", res.Stdout, err)
	}
}

func TestOSRunMissingBinary(t *testing.T) {
	if _, err := NewOS(nil).Run(context.Background(), Cmd{Name: "ownci-no-such-binary"}); err == nil {
		t.Fatal("want error")
	}
	if _, err := NewOS(nil).LookPath("ownci-no-such-binary"); err == nil {
		t.Fatal("want LookPath error")
	}
}

func TestOSRunTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := NewOS(nil).Run(ctx, Cmd{Name: "bash", Args: []string{"-c", "exec sleep 5"}})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Errorf("took %s", time.Since(start))
	}
}

func TestOSRunLogsRedacted(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	const tok = "ghp_abcdefghijklmnopqrstuvwxyz0123456789"
	if _, err := NewOS(log).Run(context.Background(), Cmd{Name: "echo", Args: []string{"--token", tok}}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if strings.Contains(out, tok) || !strings.Contains(out, "--token ***") || !strings.Contains(out, "exit=0") {
		t.Errorf("log = %s", out)
	}
}

func TestFake(t *testing.T) {
	f := NewFake()
	f.SetPath("docker", "/usr/bin/docker")
	f.On("docker", "version").Stdout("v")
	f.On("docker").Stdout("generic")
	f.On("docker", "run").Exit(125).Stderr("boom")
	f.On("ldconfig").Fail(errors.New("cannot start"))

	ctx := context.Background()
	for _, tc := range []struct {
		cmd  Cmd
		out  string
		exit int
	}{
		{Cmd{Name: "docker", Args: []string{"version", "--format", "x"}}, "v", 0},
		{Cmd{Name: "docker", Args: []string{"ps"}}, "generic", 0},
		{Cmd{Name: "docker", Args: []string{"run", "img"}}, "", 125},
	} {
		res, err := f.Run(ctx, tc.cmd)
		if err != nil || string(res.Stdout) != tc.out || res.ExitCode != tc.exit {
			t.Errorf("%s: got %+v, %v", tc.cmd, res, err)
		}
	}
	if _, err := f.Run(ctx, Cmd{Name: "ldconfig"}); err == nil {
		t.Error("want scripted failure")
	}
	if _, err := f.Run(ctx, Cmd{Name: "unknown"}); err == nil {
		t.Error("unscripted command must error")
	}
	if len(f.Calls()) != 5 || f.Calls()[0].Name != "docker" {
		t.Errorf("calls = %v", f.Calls())
	}
	if p, err := f.LookPath("docker"); err != nil || p != "/usr/bin/docker" {
		t.Errorf("LookPath = %q, %v", p, err)
	}
	if _, err := f.LookPath("git"); err == nil {
		t.Error("git should be missing")
	}
}
