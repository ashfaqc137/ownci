package runner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ashfaqc137/ownci/internal/execx"
)

func TestConfigureArgs(t *testing.T) {
	o := ConfigureOptions{URL: "https://github.com/o/r", Token: "REGTOKEN123", Name: "n", Labels: []string{"ownci", "gpu"}}
	got := strings.Join(ConfigureArgs(o), " ")
	want := "--unattended --url https://github.com/o/r --name n --labels ownci,gpu --no-default-labels --work _work"
	if got != want {
		t.Errorf("args = %q\nwant   %q", got, want)
	}
	o.Replace = true
	if got := strings.Join(ConfigureArgs(o), " "); strings.Contains(got, "REGTOKEN123") || !strings.HasSuffix(got, "--replace") {
		t.Errorf("args = %q", got)
	}
}

func TestConfigureRunsConfigShInDir(t *testing.T) {
	f := execx.NewFake()
	f.On("/r/config.sh", "--unattended")
	if err := Configure(context.Background(), f, "/r", ConfigureOptions{URL: "u", Token: "TOKEN-ABCDEFGH", Name: "n", Labels: []string{"ownci"}}); err != nil {
		t.Fatal(err)
	}
	c := f.Calls()
	if len(c) != 1 || c[0].Dir != "/r" {
		t.Fatalf("calls = %+v", c)
	}
	if strings.Contains(c[0].String(), "TOKEN-ABCDEFGH") || !slices.Contains(c[0].Env, TokenEnv+"=TOKEN-ABCDEFGH") {
		t.Errorf("the token must travel in %s, not argv: %+v", TokenEnv, c[0])
	}
}

func TestConfigureFailureShowsRedactedTail(t *testing.T) {
	f := execx.NewFake()
	var out strings.Builder
	for i := 0; i < 40; i++ {
		out.WriteString("line " + string(rune('A'+i%26)) + "\n")
	}
	f.On("/r/config.sh").Exit(1).Stdout(out.String()).Stderr("bad token TOKEN-ABCDEFGH here")
	err := Configure(context.Background(), f, "/r", ConfigureOptions{Token: "TOKEN-ABCDEFGH"})
	var re *Error
	if !errors.As(err, &re) {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(re.Msg, "TOKEN-ABCDEFGH") || !strings.Contains(re.Msg, "bad token *** here") {
		t.Errorf("message not redacted: %q", re.Msg)
	}
	if n := strings.Count(re.Msg, "\n"); n > 21 {
		t.Errorf("message has %d lines, want at most 20 of output", n)
	}
	if !strings.Contains(re.Hint, "ownci doctor") {
		t.Errorf("hint = %q", re.Hint)
	}
}

func TestConfigureStartFailure(t *testing.T) {
	f := execx.NewFake()
	f.On("/r/config.sh").Fail(errors.New("exec format error"))
	err := Configure(context.Background(), f, "/r", ConfigureOptions{Token: "TOKEN-ABCDEFGH"})
	var re *Error
	if !errors.As(err, &re) || re.Hint == "" {
		t.Fatalf("err = %v", err)
	}
}

func TestReadRunnerFile(t *testing.T) {
	dir := t.TempDir()
	body := "\xef\xbb\xbf" + `{"agentId":42,"agentName":"box","poolId":1,"serverUrl":"https://s","gitHubUrl":"https://github.com/o/r","workFolder":"_work","extra":true}`
	if err := os.WriteFile(filepath.Join(dir, ".runner"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ReadRunnerFile(dir)
	want := RunnerFile{AgentID: 42, AgentName: "box", PoolID: 1, ServerURL: "https://s", GitHubURL: "https://github.com/o/r", WorkFolder: "_work"}
	if err != nil || got != want {
		t.Fatalf("got %+v, %v", got, err)
	}
	if !Registered(dir) {
		t.Error("Registered = false")
	}
}

func TestReadRunnerFileVariants(t *testing.T) {
	dir := t.TempDir()
	if _, err := ReadRunnerFile(dir); err == nil {
		t.Error("missing file should error")
	}
	if Registered(dir) {
		t.Error("Registered on empty dir")
	}
	p := filepath.Join(dir, ".runner")
	os.WriteFile(p, []byte(`{"agentId":"7","agentName":"q"}`), 0o600) // quoted id, no BOM
	if got, err := ReadRunnerFile(dir); err != nil || got.AgentID != 7 || got.AgentName != "q" {
		t.Errorf("quoted id: %+v, %v", got, err)
	}
	os.WriteFile(p, []byte(`{nope`), 0o600)
	if _, err := ReadRunnerFile(dir); err == nil {
		t.Error("malformed file should error")
	}
}
