package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/ashfaqc137/ownci/internal/buildinfo"
	"github.com/ashfaqc137/ownci/internal/logging"
)

const (
	defaultAPI     = "https://api.github.com"
	defaultWeb     = "https://github.com"
	requestTimeout = 30 * time.Second
	perPage        = 100
	maxBody        = 4 << 20
)

// APIBaseURL is the REST base URL: api.github.com, or the dev override.
func APIBaseURL() string {
	if u := devAPIURL(); u != "" {
		return strings.TrimRight(u, "/")
	}
	return defaultAPI
}

// WebBaseURL is the web base URL: github.com, or the dev override.
func WebBaseURL() string {
	if u := devWebURL(); u != "" {
		return strings.TrimRight(u, "/")
	}
	return defaultWeb
}

// Client talks to the GitHub REST API. It never logs or returns its token.
type Client struct {
	baseURL string
	http    *http.Client
	token   string
	src     TokenSource
	ua      string
	log     *slog.Logger
}

// Option configures a Client.
type Option func(*Client)

func WithBaseURL(u string) Option { return func(c *Client) { c.baseURL = strings.TrimRight(u, "/") } }

// WithHTTPClient sets the HTTP client; nil keeps the default.
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) {
		if h != nil {
			c.http = h
		}
	}
}
func WithLogger(l *slog.Logger) Option     { return func(c *Client) { c.log = l } }
func WithTokenSource(s TokenSource) Option { return func(c *Client) { c.src = s } }

// NewClient returns a client for token. The base URL defaults to APIBaseURL().
func NewClient(token string, opts ...Option) *Client {
	logging.RegisterSecret(token)
	c := &Client{
		baseURL: APIBaseURL(),
		http:    &http.Client{},
		token:   token,
		ua:      "ownci/" + buildinfo.Version,
		log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// RepoInfo is the subset of GET /repos/{owner}/{repo} OwnCI uses.
type RepoInfo struct {
	FullName, HTMLURL, Visibility string
	Private, Admin                bool
}

func (c *Client) GetRepo(ctx context.Context, r Repo) (RepoInfo, error) {
	var v struct {
		FullName    string `json:"full_name"`
		HTMLURL     string `json:"html_url"`
		Visibility  string `json:"visibility"`
		Private     bool   `json:"private"`
		Permissions struct {
			Admin bool `json:"admin"`
		} `json:"permissions"`
	}
	if err := c.do(ctx, http.MethodGet, repoPath(r), nil, r, &v); err != nil {
		return RepoInfo{}, err
	}
	vis := v.Visibility
	if vis == "" {
		vis = "public"
		if v.Private {
			vis = "private"
		}
	}
	return RepoInfo{FullName: v.FullName, HTMLURL: v.HTMLURL, Visibility: vis, Private: v.Private, Admin: v.Permissions.Admin}, nil
}

// Token is a short-lived runner registration or removal token.
type Token struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

func (c *Client) RegistrationToken(ctx context.Context, r Repo) (Token, error) {
	return c.runnerToken(ctx, r, "registration-token")
}

func (c *Client) RemoveToken(ctx context.Context, r Repo) (Token, error) {
	return c.runnerToken(ctx, r, "remove-token")
}

func (c *Client) runnerToken(ctx context.Context, r Repo, kind string) (Token, error) {
	var t Token
	if err := c.do(ctx, http.MethodPost, repoPath(r)+"/actions/runners/"+kind, nil, r, &t); err != nil {
		return Token{}, err
	}
	logging.RegisterSecret(t.Token)
	return t, nil
}

// Runner is a self-hosted runner registered with a repository.
type Runner struct {
	ID     int64
	Name   string
	OS     string
	Status string
	Busy   bool
	Labels []string
}

// ListRunners returns every runner of the repository, following pagination.
func (c *Client) ListRunners(ctx context.Context, r Repo) ([]Runner, error) {
	var all []Runner
	for page := 1; ; page++ {
		var v struct {
			Runners []struct {
				ID     int64  `json:"id"`
				Name   string `json:"name"`
				OS     string `json:"os"`
				Status string `json:"status"`
				Busy   bool   `json:"busy"`
				Labels []struct {
					Name string `json:"name"`
				} `json:"labels"`
			} `json:"runners"`
		}
		q := url.Values{"per_page": {strconv.Itoa(perPage)}, "page": {strconv.Itoa(page)}}
		if err := c.do(ctx, http.MethodGet, repoPath(r)+"/actions/runners", q, r, &v); err != nil {
			return nil, err
		}
		for _, x := range v.Runners {
			run := Runner{ID: x.ID, Name: x.Name, OS: x.OS, Status: x.Status, Busy: x.Busy}
			for _, l := range x.Labels {
				run.Labels = append(run.Labels, l.Name)
			}
			all = append(all, run)
		}
		if len(v.Runners) < perPage {
			return all, nil
		}
	}
}

// FindRunner returns the runner called name, or nil, nil when there is none.
func (c *Client) FindRunner(ctx context.Context, r Repo, name string) (*Runner, error) {
	runners, err := c.ListRunners(ctx, r)
	if err != nil {
		return nil, err
	}
	for i := range runners {
		if runners[i].Name == name {
			return &runners[i], nil
		}
	}
	return nil, nil
}

func (c *Client) DeleteRunner(ctx context.Context, r Repo, id int64) error {
	return c.do(ctx, http.MethodDelete, repoPath(r)+"/actions/runners/"+strconv.FormatInt(id, 10), nil, r, nil)
}

func repoPath(r Repo) string {
	return "/repos/" + url.PathEscape(r.Owner) + "/" + url.PathEscape(r.Name)
}

// do performs one request with the standard headers and a 30 s timeout. A
// non-2xx response becomes an *APIError; out (if non-nil) receives the JSON
// body. Only method, path, status and duration are logged.
func (c *Client) do(ctx context.Context, method, path string, query url.Values, repo Repo, out any) error {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	u := c.baseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, nil)
	if err != nil {
		return fmt.Errorf("building GitHub request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("User-Agent", c.ua)

	start := time.Now()
	resp, err := c.http.Do(req)
	if err != nil {
		c.log.Debug("github request failed", "method", method, "path", path, "took", time.Since(start).Round(time.Millisecond))
		return &Error{
			Msg:  "could not reach GitHub: " + unwrapURLError(err),
			Hint: "Check your network connection.",
			Err:  err,
		}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	c.log.Debug("github request", "method", method, "path", path, "status", resp.StatusCode, "took", time.Since(start).Round(time.Millisecond))

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		var e struct {
			Message string `json:"message"`
			DocURL  string `json:"documentation_url"`
		}
		_ = json.NewDecoder(bytes.NewReader(body)).Decode(&e)
		if e.Message == "" {
			e.Message = http.StatusText(resp.StatusCode)
		}
		repoName := ""
		if repo != (Repo{}) {
			repoName = repo.String()
		}
		return newAPIError(resp.StatusCode, logging.Redact(e.Message), e.DocURL, repoName, c.src,
			resp.Header.Get("X-RateLimit-Remaining"), resp.Header.Get("X-RateLimit-Reset"))
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decoding GitHub response for %s %s: %w", method, path, err)
	}
	return nil
}

// unwrapURLError drops the URL wrapper so messages stay short.
func unwrapURLError(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err.Error()
	}
	return err.Error()
}
