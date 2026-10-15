// Package fakegh is an in-memory fake of the GitHub REST endpoints OwnCI uses,
// plus a side channel for the fake runner's config.sh. It backs tools/fakegh
// (manual dev flows) and httptest servers in tests:
//
//	srv := httptest.NewServer(fakegh.New(fakegh.Options{}))
package fakegh

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ashfaqc137/ownci/internal/testutil"
)

// RunnerVersion is the version reported by the fake runner tarball served at
// /_fake/runner.tar.gz.
const RunnerVersion = "9.9.9-fake"

// Options configures a Server. Repos are "owner/name".
type Options struct {
	PublicRepos []string // repos reported as public (default: all private)
	NoAdmin     []string // repos where permissions.admin is false
	StatePath   string   // JSON file persisted on every change ("" = memory only)
	WebURL      string   // base for html_url (default: http://<request host>/web)
	Now         func() time.Time
}

// Runner is a registered runner, shaped like GitHub's API objects.
type Runner struct {
	ID     int64   `json:"id"`
	Name   string  `json:"name"`
	OS     string  `json:"os"`
	Status string  `json:"status"`
	Busy   bool    `json:"busy"`
	Labels []label `json:"labels"`
}

type label struct {
	Name string `json:"name"`
}

type tokenRec struct {
	Kind string `json:"kind"` // "registration" | "remove"
	Repo string `json:"repo"`
	Used bool   `json:"used"`
}

// State is the persisted (and /_fake/state) view of the server.
type State struct {
	NextID  int64                `json:"next_id"`
	Runners map[string][]Runner  `json:"runners"` // repo → runners
	Tokens  map[string]*tokenRec `json:"tokens"`
}

// Server is the fake GitHub. It implements http.Handler.
type Server struct {
	opts    Options
	mux     *http.ServeMux
	tarball []byte
	sha     string

	mu    sync.Mutex
	state State
}

// New returns a Server and panics if StatePath holds an unreadable file; use
// Open to handle that error.
func New(opts Options) *Server {
	s, err := Open(opts)
	if err != nil {
		panic(err)
	}
	return s
}

// Open returns a Server, loading opts.StatePath if it exists.
func Open(opts Options) (*Server, error) {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	s := &Server{opts: opts, mux: http.NewServeMux(), state: newState()}
	s.tarball, s.sha = testutil.FakeRunnerTarball(testutil.FakeRunnerOptions{Version: RunnerVersion})
	if opts.StatePath != "" {
		if b, err := os.ReadFile(opts.StatePath); err == nil {
			if err := json.Unmarshal(b, &s.state); err != nil {
				return nil, fmt.Errorf("reading fakegh state %s: %w", opts.StatePath, err)
			}
			if s.state.Runners == nil {
				s.state.Runners = map[string][]Runner{}
			}
			if s.state.Tokens == nil {
				s.state.Tokens = map[string]*tokenRec{}
			}
		} else if !os.IsNotExist(err) {
			return nil, err
		}
	}
	s.routes()
	return s, nil
}

func newState() State {
	return State{NextID: 1, Runners: map[string][]Runner{}, Tokens: map[string]*tokenRec{}}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.mux.ServeHTTP(w, r) }

// RunnerSHA256 is the checksum of the tarball served at /_fake/runner.tar.gz.
func (s *Server) RunnerSHA256() string { return s.sha }

// Snapshot returns a copy of the current state.
func (s *Server) Snapshot() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, _ := json.Marshal(s.state)
	var out State
	_ = json.Unmarshal(b, &out)
	return out
}

// AddRunner seeds a runner (for pagination tests and similar).
func (s *Server) AddRunner(repo, name string, labels ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.addRunnerLocked(repo, name, labels)
	s.saveLocked()
}

// EnvLines returns the export lines for pointing a dev build at base
// (e.g. "http://127.0.0.1:8787").
func EnvLines(base string) []string {
	_, sha := testutil.FakeRunnerTarball(testutil.FakeRunnerOptions{Version: RunnerVersion})
	return []string{
		"export OWNCI_DEV_GITHUB_API_URL=" + base,
		"export OWNCI_DEV_GITHUB_WEB_URL=" + base + "/web",
		"export OWNCI_DEV_RUNNER_URL=" + base + "/_fake/runner.tar.gz",
		"export OWNCI_DEV_RUNNER_SHA256=" + sha,
		"export OWNCI_DEV_RUNNER_VERSION=" + RunnerVersion,
		"export GITHUB_TOKEN=fake-token",
	}
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /repos/{owner}/{repo}", s.auth(s.getRepo))
	s.mux.HandleFunc("POST /repos/{owner}/{repo}/actions/runners/registration-token", s.auth(s.newToken("registration")))
	s.mux.HandleFunc("POST /repos/{owner}/{repo}/actions/runners/remove-token", s.auth(s.newToken("remove")))
	s.mux.HandleFunc("GET /repos/{owner}/{repo}/actions/runners", s.auth(s.listRunners))
	s.mux.HandleFunc("DELETE /repos/{owner}/{repo}/actions/runners/{id}", s.auth(s.deleteRunner))

	s.mux.HandleFunc("POST /_fake/register", s.fakeRegister)
	s.mux.HandleFunc("DELETE /_fake/register/{name}", s.fakeUnregister)
	s.mux.HandleFunc("GET /_fake/runner.tar.gz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/gzip")
		_, _ = w.Write(s.tarball)
	})
	s.mux.HandleFunc("GET /_fake/state", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, s.Snapshot())
	})
	s.mux.HandleFunc("GET /web/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "fakegh web page for %s\n", r.URL.Path)
	})
}

// auth requires a non-empty Bearer token; "bad-token" is rejected.
func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || strings.TrimSpace(tok) == "" || tok == "bad-token" {
			apiError(w, http.StatusUnauthorized, "Bad credentials")
			return
		}
		next(w, r)
	}
}

func repoKey(r *http.Request) string { return r.PathValue("owner") + "/" + r.PathValue("repo") }

func (s *Server) listed(list []string, repo string) bool {
	return slices.ContainsFunc(list, func(x string) bool { return strings.EqualFold(x, repo) })
}

func (s *Server) getRepo(w http.ResponseWriter, r *http.Request) {
	repo := repoKey(r)
	private := !s.listed(s.opts.PublicRepos, repo)
	web := s.opts.WebURL
	if web == "" {
		web = "http://" + r.Host + "/web"
	}
	vis := "public"
	if private {
		vis = "private"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"full_name":   repo,
		"html_url":    web + "/" + repo,
		"private":     private,
		"visibility":  vis,
		"permissions": map[string]bool{"admin": !s.listed(s.opts.NoAdmin, repo)},
	})
}

func (s *Server) newToken(kind string) http.HandlerFunc {
	prefix := map[string]string{"registration": "FAKE-REG-", "remove": "FAKE-RM-"}[kind]
	return func(w http.ResponseWriter, r *http.Request) {
		repo := repoKey(r)
		if s.listed(s.opts.NoAdmin, repo) {
			apiError(w, http.StatusForbidden, "Must have admin rights to Repository.")
			return
		}
		tok := prefix + randHex(12)
		s.mu.Lock()
		s.state.Tokens[tok] = &tokenRec{Kind: kind, Repo: repo}
		s.saveLocked()
		s.mu.Unlock()
		writeJSON(w, http.StatusCreated, map[string]string{
			"token":      tok,
			"expires_at": s.opts.Now().Add(time.Hour).UTC().Format(time.RFC3339),
		})
	}
}

func (s *Server) listRunners(w http.ResponseWriter, r *http.Request) {
	repo := repoKey(r)
	page, perPage := 1, 30
	if n, err := strconv.Atoi(r.URL.Query().Get("page")); err == nil && n > 0 {
		page = n
	}
	if n, err := strconv.Atoi(r.URL.Query().Get("per_page")); err == nil && n > 0 {
		perPage = min(n, 100)
	}
	s.mu.Lock()
	all := s.state.Runners[repo]
	total := len(all)
	lo := min((page-1)*perPage, total)
	hi := min(lo+perPage, total)
	out := slices.Clone(all[lo:hi])
	s.mu.Unlock()
	if out == nil {
		out = []Runner{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"total_count": total, "runners": out})
}

func (s *Server) deleteRunner(w http.ResponseWriter, r *http.Request) {
	repo := repoKey(r)
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		apiError(w, http.StatusNotFound, "Not Found")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	runners := s.state.Runners[repo]
	i := slices.IndexFunc(runners, func(x Runner) bool { return x.ID == id })
	if i < 0 {
		apiError(w, http.StatusNotFound, "Not Found")
		return
	}
	s.state.Runners[repo] = slices.Delete(runners, i, i+1)
	s.saveLocked()
	w.WriteHeader(http.StatusNoContent)
}

// fakeRegister is called by the fake runner's config.sh.
func (s *Server) fakeRegister(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token  string   `json:"token"`
		Name   string   `json:"name"`
		Labels []string `json:"labels"`
		URL    string   `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" {
		apiError(w, http.StatusBadRequest, "bad register request")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rec := s.state.Tokens[body.Token]
	if rec == nil || rec.Kind != "registration" || rec.Used {
		apiError(w, http.StatusUnauthorized, "Http response code: Unauthorized")
		return
	}
	rec.Used = true
	s.addRunnerLocked(rec.Repo, body.Name, body.Labels)
	s.saveLocked()
	writeJSON(w, http.StatusCreated, map[string]string{"status": "registered"})
}

// fakeUnregister is called by the fake runner's `config.sh remove`.
func (s *Server) fakeUnregister(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	s.mu.Lock()
	defer s.mu.Unlock()
	rec := s.state.Tokens[r.URL.Query().Get("token")]
	if rec == nil || rec.Kind != "remove" || rec.Used {
		apiError(w, http.StatusUnauthorized, "Http response code: Unauthorized")
		return
	}
	runners := s.state.Runners[rec.Repo]
	i := slices.IndexFunc(runners, func(x Runner) bool { return x.Name == name })
	if i < 0 {
		apiError(w, http.StatusNotFound, "Not Found")
		return
	}
	rec.Used = true
	s.state.Runners[rec.Repo] = slices.Delete(runners, i, i+1)
	s.saveLocked()
	w.WriteHeader(http.StatusNoContent)
}

// addRunnerLocked registers name in repo, replacing a runner of the same name.
func (s *Server) addRunnerLocked(repo, name string, labels []string) {
	run := Runner{ID: s.state.NextID, Name: name, OS: "Linux", Status: "online"}
	s.state.NextID++
	for _, l := range labels {
		run.Labels = append(run.Labels, label{Name: l})
	}
	runners := s.state.Runners[repo]
	if i := slices.IndexFunc(runners, func(x Runner) bool { return x.Name == name }); i >= 0 {
		runners = slices.Delete(runners, i, i+1)
	}
	s.state.Runners[repo] = append(runners, run)
}

// saveLocked persists state atomically. Callers hold s.mu.
func (s *Server) saveLocked() {
	if s.opts.StatePath == "" {
		return
	}
	b, err := json.MarshalIndent(s.state, "", "  ")
	if err != nil {
		return
	}
	tmp := filepath.Join(filepath.Dir(s.opts.StatePath), "."+filepath.Base(s.opts.StatePath)+".tmp")
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, s.opts.StatePath)
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return strings.ToUpper(hex.EncodeToString(b))
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func apiError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"message": msg, "documentation_url": "https://docs.github.com/rest"})
}
