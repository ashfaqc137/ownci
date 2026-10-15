package fakegh_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ashfaqc137/ownci/internal/testutil"
	"github.com/ashfaqc137/ownci/internal/testutil/fakegh"
)

func call(t *testing.T, method, url, auth string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(method, url, nil)
	if auth != "" {
		req.Header.Set("Authorization", "Bearer "+auth)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestAuthRules(t *testing.T) {
	ts := httptest.NewServer(fakegh.New(fakegh.Options{}))
	defer ts.Close()
	for auth, want := range map[string]int{"": 401, "bad-token": 401, "anything": 200} {
		if code, _ := call(t, "GET", ts.URL+"/repos/me/x", auth); code != want {
			t.Errorf("auth %q → %d, want %d", auth, code, want)
		}
	}
}

func TestRunnerTarballMatchesAdvertisedSHA(t *testing.T) {
	fg := fakegh.New(fakegh.Options{})
	ts := httptest.NewServer(fg)
	defer ts.Close()
	_, body := call(t, "GET", ts.URL+"/_fake/runner.tar.gz", "")
	sum := sha256.Sum256([]byte(body))
	if hex.EncodeToString(sum[:]) != fg.RunnerSHA256() {
		t.Error("served tarball differs from RunnerSHA256")
	}
	var found bool
	for _, l := range fakegh.EnvLines("http://x") {
		if l == "export OWNCI_DEV_RUNNER_SHA256="+fg.RunnerSHA256() {
			found = true
		}
	}
	if !found {
		t.Error("EnvLines SHA256 differs from the served tarball (not deterministic?)")
	}
}

func TestStatePersistsAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	fg := fakegh.New(fakegh.Options{StatePath: path})
	fg.AddRunner("me/x", "keep-me", "ownci")

	fg2, err := fakegh.Open(fakegh.Options{StatePath: path})
	if err != nil {
		t.Fatal(err)
	}
	if got := fg2.Snapshot().Runners["me/x"]; len(got) != 1 || got[0].Name != "keep-me" {
		t.Errorf("runners after restart: %+v", got)
	}
	// IDs keep increasing after a restart.
	fg2.AddRunner("me/x", "second")
	if r := fg2.Snapshot().Runners["me/x"]; r[1].ID <= r[0].ID {
		t.Errorf("ids not monotonic: %+v", r)
	}
	if err := os.WriteFile(path, []byte("{nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := fakegh.Open(fakegh.Options{StatePath: path}); err == nil {
		t.Error("corrupt state should fail Open")
	}
}

func mintToken(t *testing.T, base, kind string) string {
	t.Helper()
	code, body := call(t, "POST", base+"/repos/me/x/actions/runners/"+kind, "tok")
	var v struct{ Token string }
	if code != 201 || json.Unmarshal([]byte(body), &v) != nil || v.Token == "" {
		t.Fatalf("%s → %d %s", kind, code, body)
	}
	return v.Token
}

func runnerNames(t *testing.T, fg *fakegh.Server) []string {
	var n []string
	for _, r := range fg.Snapshot().Runners["me/x"] {
		n = append(n, r.Name)
	}
	return n
}

// The fake runner's config.sh talks to the side channel: register with a
// fresh token, fail on reuse, remove with a remove token.
func TestFakeRunnerConfigRegisterFlow(t *testing.T) {
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl not on PATH")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not on PATH")
	}
	fg := fakegh.New(fakegh.Options{})
	ts := httptest.NewServer(fg)
	defer ts.Close()

	dir := t.TempDir()
	testutil.WriteFakeRunner(t, dir)
	config := func(args ...string) (string, error) {
		cmd := exec.Command(filepath.Join(dir, "config.sh"), args...)
		cmd.Env = append(os.Environ(), "OWNCI_DEV_GITHUB_API_URL="+ts.URL, "RUNNER_ALLOW_RUNASROOT=1")
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		err := cmd.Run()
		return out.String(), err
	}

	reg := mintToken(t, ts.URL, "registration-token")
	out, err := config("--unattended", "--url", "https://github.com/me/x", "--token", reg, "--name", "box", "--labels", "ownci,gpu")
	if err != nil {
		t.Fatalf("register failed: %v\n%s", err, out)
	}
	runners := fg.Snapshot().Runners["me/x"]
	if len(runners) != 1 || runners[0].Name != "box" || runners[0].Status != "online" || runners[0].Busy ||
		len(runners[0].Labels) != 2 || runners[0].Labels[1].Name != "gpu" {
		t.Fatalf("runners = %+v", runners)
	}

	// Token reuse is rejected (replace so config.sh gets as far as the call).
	if out, err := config("--unattended", "--url", "https://github.com/me/x", "--token", reg, "--name", "box2", "--replace"); err == nil {
		t.Fatalf("reused token accepted:\n%s", out)
	}
	if got := runnerNames(t, fg); len(got) != 1 {
		t.Errorf("reuse registered a runner: %v", got)
	}

	rm := mintToken(t, ts.URL, "remove-token")
	if out, err := config("remove", "--token", rm); err != nil {
		t.Fatalf("remove failed: %v\n%s", err, out)
	}
	if got := runnerNames(t, fg); len(got) != 0 {
		t.Errorf("runner not removed: %v", got)
	}
	// A registration token cannot unregister.
	if strings.Contains(strings.Join(runnerNames(t, fg), ","), "box") {
		t.Error("unexpected runner")
	}
}

func TestRegistrationTokenCannotRemove(t *testing.T) {
	fg := fakegh.New(fakegh.Options{})
	ts := httptest.NewServer(fg)
	defer ts.Close()
	fg.AddRunner("me/x", "box")
	reg := mintToken(t, ts.URL, "registration-token")
	if code, _ := call(t, "DELETE", ts.URL+"/_fake/register/box?token="+reg, ""); code != 401 {
		t.Errorf("registration token removed a runner: %d", code)
	}
	if got := runnerNames(t, fg); len(got) != 1 {
		t.Errorf("runners = %v", got)
	}
}
