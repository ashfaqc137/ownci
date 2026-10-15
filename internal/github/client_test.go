package github

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ashfaqc137/ownci/internal/logging"
	"github.com/ashfaqc137/ownci/internal/testutil/fakegh"
)

const testToken = "ghp_ClientTestSecretToken123456"

var repo = Repo{"me", "private-one"}

func newFake(t *testing.T, opts fakegh.Options) (*fakegh.Server, *httptest.Server) {
	t.Helper()
	fg := fakegh.New(opts)
	ts := httptest.NewServer(fg)
	t.Cleanup(ts.Close)
	return fg, ts
}

func client(ts *httptest.Server, opts ...Option) *Client {
	return NewClient(testToken, append([]Option{WithBaseURL(ts.URL)}, opts...)...)
}

func TestGetRepo(t *testing.T) {
	_, ts := newFake(t, fakegh.Options{PublicRepos: []string{"me/public-one"}, NoAdmin: []string{"me/noadmin"}})
	c := client(ts)
	ctx := context.Background()

	info, err := c.GetRepo(ctx, repo)
	if err != nil || !info.Private || !info.Admin || info.Visibility != "private" || info.FullName != "me/private-one" {
		t.Errorf("private: %+v, %v", info, err)
	}
	if !strings.HasSuffix(info.HTMLURL, "/web/me/private-one") {
		t.Errorf("html_url = %q", info.HTMLURL)
	}
	info, err = c.GetRepo(ctx, Repo{"me", "public-one"})
	if err != nil || info.Private || info.Visibility != "public" {
		t.Errorf("public: %+v, %v", info, err)
	}
	info, err = c.GetRepo(ctx, Repo{"me", "noadmin"})
	if err != nil || info.Admin {
		t.Errorf("noadmin: %+v, %v", info, err)
	}
}

func TestTokensAndRunnerLifecycle(t *testing.T) {
	fg, ts := newFake(t, fakegh.Options{})
	c := client(ts)
	ctx := context.Background()

	reg, err := c.RegistrationToken(ctx, repo)
	if err != nil || !strings.HasPrefix(reg.Token, "FAKE-REG-") || reg.ExpiresAt.IsZero() {
		t.Fatalf("registration token: %+v, %v", reg, err)
	}
	rm, err := c.RemoveToken(ctx, repo)
	if err != nil || !strings.HasPrefix(rm.Token, "FAKE-RM-") {
		t.Fatalf("remove token: %+v, %v", rm, err)
	}
	for _, tok := range []string{reg.Token, rm.Token} {
		if strings.Contains(logging.Redact("x "+tok), tok) {
			t.Errorf("returned token %q not registered with redactor", tok)
		}
	}

	if got, err := c.FindRunner(ctx, repo, "ownci-box"); err != nil || got != nil {
		t.Fatalf("absent runner: %v, %v", got, err)
	}
	fg.AddRunner("me/private-one", "ownci-box", "ownci", "extra")
	got, err := c.FindRunner(ctx, repo, "ownci-box")
	if err != nil || got == nil || got.Status != "online" || got.Busy || got.OS != "Linux" ||
		strings.Join(got.Labels, ",") != "ownci,extra" {
		t.Fatalf("FindRunner = %+v, %v", got, err)
	}
	if err := c.DeleteRunner(ctx, repo, got.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := c.FindRunner(ctx, repo, "ownci-box"); got != nil {
		t.Errorf("runner still present: %+v", got)
	}
	var ae *APIError
	if err := c.DeleteRunner(ctx, repo, 424242); !errors.As(err, &ae) || ae.Status != 404 {
		t.Errorf("delete missing: %v", err)
	}
}

func TestListRunnersPagination(t *testing.T) {
	fg, ts := newFake(t, fakegh.Options{})
	for i := 0; i < 250; i++ {
		fg.AddRunner("me/private-one", fmt.Sprintf("r-%03d", i), "ownci")
	}
	fg.AddRunner("me/other", "elsewhere")
	runners, err := client(ts).ListRunners(context.Background(), repo)
	if err != nil || len(runners) != 250 {
		t.Fatalf("got %d runners, %v", len(runners), err)
	}
	seen := map[int64]bool{}
	for _, r := range runners {
		seen[r.ID] = true
	}
	if len(seen) != 250 {
		t.Errorf("duplicate runners across pages: %d unique", len(seen))
	}
	// Exactly one full page: the client must still ask for page 2 and stop.
	fg2, ts2 := newFake(t, fakegh.Options{})
	for i := 0; i < 100; i++ {
		fg2.AddRunner("me/private-one", fmt.Sprintf("r-%03d", i))
	}
	if runners, err := client(ts2).ListRunners(context.Background(), repo); err != nil || len(runners) != 100 {
		t.Errorf("full page: %d, %v", len(runners), err)
	}
}

func TestRequestHeaders(t *testing.T) {
	var got http.Header
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.Write([]byte(`{"full_name":"me/private-one"}`))
	}))
	defer ts.Close()
	if _, err := client(ts).GetRepo(context.Background(), repo); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"Authorization":        "Bearer " + testToken,
		"Accept":               "application/vnd.github+json",
		"X-Github-Api-Version": "2022-11-28",
	}
	for k, v := range want {
		if got.Get(k) != v {
			t.Errorf("%s = %q, want %q", k, got.Get(k), v)
		}
	}
	if ua := got.Get("User-Agent"); !strings.HasPrefix(ua, "ownci/") {
		t.Errorf("User-Agent = %q", ua)
	}
}

func errServer(status int, body string, hdr map[string]string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for k, v := range hdr {
			w.Header().Set(k, v)
		}
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
}

func TestErrorMapping(t *testing.T) {
	type tc struct {
		name     string
		status   int
		body     string
		hdr      map[string]string
		src      TokenSource
		wantMsg  string
		wantHint string
	}
	cases := []tc{
		{"401", 401, `{"message":"Bad credentials"}`, nil, SourceGHToken, "invalid or expired", "$GH_TOKEN"},
		{"401 gh", 401, `{"message":"Bad credentials"}`, nil, SourceGH, "invalid or expired", "gh auth login"},
		{"403 permission", 403, `{"message":"Resource not accessible"}`, nil, "", "admin access to me/private-one is required", "Administration: read & write"},
		{"403 rate limit", 403, `{"message":"API rate limit exceeded"}`, map[string]string{"X-RateLimit-Remaining": "0", "X-RateLimit-Reset": "1893456000"}, "", "rate limit", "Try again after"},
		{"404", 404, `{"message":"Not Found"}`, nil, "", "repository not found or token cannot see it", "owner/name"},
		{"500", 500, `{"message":"Server exploded","documentation_url":"https://docs.example/x"}`, nil, "", "(HTTP 500): Server exploded", ""},
		{"502 html", 502, `<html>bad gateway</html>`, nil, "", "(HTTP 502): Bad Gateway", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ts := errServer(c.status, c.body, c.hdr)
			defer ts.Close()
			_, err := client(ts, WithTokenSource(c.src)).GetRepo(context.Background(), repo)
			var ae *APIError
			if !errors.As(err, &ae) || ae.Status != c.status {
				t.Fatalf("err = %v", err)
			}
			if !strings.Contains(ae.Error(), c.wantMsg) {
				t.Errorf("msg %q lacks %q", ae.Error(), c.wantMsg)
			}
			if !strings.Contains(ae.UserHint(), c.wantHint) {
				t.Errorf("hint %q lacks %q", ae.UserHint(), c.wantHint)
			}
			if strings.Contains(ae.Error()+ae.UserHint(), testToken) {
				t.Error("token leaked into error")
			}
		})
	}
}

func TestForbiddenWithoutRateLimitHeaderIsPermission(t *testing.T) {
	ts := errServer(403, `{"message":"nope"}`, map[string]string{"X-RateLimit-Remaining": "42"})
	defer ts.Close()
	_, err := client(ts).GetRepo(context.Background(), repo)
	if err == nil || strings.Contains(err.Error(), "rate limit") {
		t.Fatalf("err = %v", err)
	}
}

func TestFakeGHBadTokenIs401(t *testing.T) {
	_, ts := newFake(t, fakegh.Options{})
	_, err := NewClient("bad-token", WithBaseURL(ts.URL), WithTokenSource(SourceGitHubToken)).GetRepo(context.Background(), repo)
	var ae *APIError
	if !errors.As(err, &ae) || ae.Status != 401 {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(ae.Error()+ae.UserHint(), "bad-token") {
		t.Error("token leaked into error")
	}
}

func TestNoAdminRepoCannotMintTokens(t *testing.T) {
	_, ts := newFake(t, fakegh.Options{NoAdmin: []string{"me/x"}})
	_, err := client(ts).RegistrationToken(context.Background(), Repo{"me", "x"})
	var ae *APIError
	if !errors.As(err, &ae) || ae.Status != 403 || !strings.Contains(ae.Error(), "admin access to me/x") {
		t.Fatalf("err = %v", err)
	}
}

func TestNetworkErrorHasHintAndNoToken(t *testing.T) {
	ts := httptest.NewServer(http.NotFoundHandler())
	ts.Close() // nothing listens any more
	_, err := client(ts).GetRepo(context.Background(), repo)
	var ge *Error
	if !errors.As(err, &ge) || ge.UserHint() == "" || strings.Contains(err.Error(), testToken) {
		t.Fatalf("err = %v", err)
	}
}

func TestDebugLogsContainNoToken(t *testing.T) {
	_, ts := newFake(t, fakegh.Options{})
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	c := client(ts, WithLogger(log))
	ctx := context.Background()
	reg, err := c.RegistrationToken(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = c.GetRepo(ctx, repo)
	_, _ = c.ListRunners(ctx, repo)
	out := buf.String()
	if !strings.Contains(out, "method=POST") || !strings.Contains(out, "status=201") || !strings.Contains(out, "registration-token") {
		t.Errorf("expected method/path/status in logs:\n%s", out)
	}
	for _, secret := range []string{testToken, reg.Token} {
		if strings.Contains(out, secret) {
			t.Errorf("log contains %q:\n%s", secret, out)
		}
	}
}
