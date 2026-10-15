# t05 — GitHub REST client, token resolution, `tools/fakegh` dev server

> Read `tasks/00-context.md` first (D3, D4, D8, §7 "GitHub REST endpoints used"), then the Implementation notes of t01–t04 (especially the fake runner contract in t04).

## Goal
A small, well-tested GitHub client covering exactly the endpoints OwnCI needs, token resolution that never leaks the token, and a fake GitHub server (`tools/fakegh`) so the real CLI can be driven end to end without a GitHub account.

## Depends on / current state
- t02: logging with `RegisterSecret`/`Redact`.
- t03: `execx.Runner` (for `gh auth token`).
- t04: `testutil.FakeRunnerTarball`, fake `config.sh`, which calls `$OWNCI_DEV_GITHUB_API_URL/_fake/register` (POST) and `/_fake/register/<name>?token=` (DELETE) when that env var is set; the dev-warning helper in `internal/cli`.

## What later tasks build on
- `github.Client` methods (t06 setup, t07 `status --remote`, t09 uninstall).
- `github.ResolveToken`.
- `github.ParseRepo`.
- `github.APIBaseURL()` / `WebBaseURL()`, which honor dev overrides.
- `tools/fakegh` for every manual dev flow from t06 onward.

## Scope

### `internal/github`
```go
type Repo struct { Owner, Name string }               // String() "owner/name"
func ParseRepo(s string) (Repo, error)                 // accepts "owner/name", "https://github.com/owner/name(.git)(/)", "git@github.com:owner/name.git"; validates chars [A-Za-z0-9-_.]
type TokenSource string                                // "GITHUB_TOKEN" | "GH_TOKEN" | "gh"
func ResolveToken(ctx context.Context, getenv func(string) string, r execx.Runner) (token string, src TokenSource, err error)
    // order D4; `gh auth token` via execx; empty everywhere → UserError hint:
    // "set GITHUB_TOKEN to a token with admin access to the repo, or run 'gh auth login'"
    // calls logging.RegisterSecret(token)
type Client struct { /* baseURL, http, token, userAgent "ownci/<version>", logger */ }
func NewClient(token string, opts ...Option) *Client   // WithBaseURL, WithHTTPClient, WithLogger
type RepoInfo struct { FullName, HTMLURL, Visibility string; Private, Admin bool }
func (c *Client) GetRepo(ctx, Repo) (RepoInfo, error)
type Token struct { Token string; ExpiresAt time.Time }
func (c *Client) RegistrationToken(ctx, Repo) (Token, error)  // RegisterSecret on the returned token
func (c *Client) RemoveToken(ctx, Repo) (Token, error)        // RegisterSecret
type Runner struct { ID int64; Name, OS, Status string; Busy bool; Labels []string }
func (c *Client) ListRunners(ctx, Repo) ([]Runner, error)       // follows pagination (page=N until fewer than per_page)
func (c *Client) FindRunner(ctx, Repo, name string) (*Runner, error) // nil, nil when absent
func (c *Client) DeleteRunner(ctx, Repo, id int64) error
func APIBaseURL() string   // "https://api.github.com" or dev override OWNCI_DEV_GITHUB_API_URL
func WebBaseURL() string   // "https://github.com" or dev override OWNCI_DEV_GITHUB_WEB_URL
```
- Headers per 00-context §7. JSON decoding is tolerant of unknown fields. Context timeouts: 30 s per request.
- **Error mapping** (`*APIError{Status int; Message, DocURL string}` plus `UserError` wrapping in the cli layer):
  - 401 → "GitHub token is invalid or expired" + hint about the token source used.
  - 403 → the token lacks permission ("admin access to <repo> is required: fine-grained PAT 'Administration: read & write', or classic 'repo' scope"). If the `X-RateLimit-Remaining: 0` header is present, return a rate-limit message instead.
  - 404 → "repository not found or token cannot see it".
  - Others → status + GitHub `message`.
- Never include the token in errors or logs. Debug-log method, path, status and duration only.
- Dev overrides via build-tagged files `override_dev.go` / `override_release.go` (same pattern as t04).

### `tools/fakegh` (`package main`, dev tool, never shipped)
- `go run ./tools/fakegh [--addr 127.0.0.1:8787] [--state /tmp/fakegh.json] [--public-repos owner/pub] [--no-admin owner/x]` serves:
  - `GET /repos/{o}/{r}` → any repo exists. `private:true` unless listed in `--public-repos`. `permissions.admin:true` unless listed in `--no-admin`.
  - `POST …/registration-token`, `POST …/remove-token` → `201` with a random token `FAKE-REG-…` / `FAKE-RM-…` and an expiry 1 h ahead. Tokens are recorded as unused.
  - `GET …/actions/runners` (with pagination), `DELETE …/actions/runners/{id}`.
  - Requires `Authorization: Bearer <anything non-empty>`, else 401. The token `bad-token` → 401, to exercise errors.
  - **Fake-runner side channel** (used by the fake `config.sh`): `POST /_fake/register` `{token,name,labels,url}`: the token must be an unused registration token (else 401), then the runner is added (`status:"online"`, `busy:false`, labels from the comma list) and the token is marked used. `DELETE /_fake/register/{name}?token=` requires an unused remove token and deletes the runner.
  - `GET /_fake/runner.tar.gz` serves `testutil.FakeRunnerTarball` (version `9.9.9-fake`). `GET /_fake/state` dumps the in-memory state as JSON (handy for verification).
  - State is persisted to `--state` on every change, so restarting fakegh keeps runners.
- On start, print ready-to-paste exports:
  ```
  export OWNCI_DEV_GITHUB_API_URL=http://127.0.0.1:8787
  export OWNCI_DEV_GITHUB_WEB_URL=http://127.0.0.1:8787/web
  export OWNCI_DEV_RUNNER_URL=http://127.0.0.1:8787/_fake/runner.tar.gz
  export OWNCI_DEV_RUNNER_SHA256=<sha>
  export OWNCI_DEV_RUNNER_VERSION=9.9.9-fake
  export GITHUB_TOKEN=fake-token
  ```
- `--print-env` prints only those export lines (for `--addr`) and exits without serving. This works because the fake tarball is **deterministic** (fixed mtimes/uid/gid/order in `FakeRunnerTarball`; make it so in this task if t04 didn't), so its SHA256 is stable. Typical use: `eval "$(go run ./tools/fakegh --print-env)"`.
- Implement the handler as a reusable `http.Handler` in `internal/testutil/fakegh` (package `fakegh`), and keep `tools/fakegh/main.go` thin. Then Go tests in t05–t09 can spin it up with `httptest.NewServer(fakegh.New(opts))`.

## Out of scope
Using the client from commands (t06+). GHES support (the dev override is enough for now). GitHub App / OAuth device flow.

## Files
`internal/github/{repo.go,token.go,client.go,errors.go,override_dev.go,override_release.go,*_test.go}`, `internal/testutil/fakegh/{fakegh.go,fakegh_test.go}`, `tools/fakegh/main.go`.

## Tests (required)
- `ParseRepo` table (valid forms, invalid: `owner`, `a/b/c`, `owner/na me`, empty).
- `ResolveToken`: env precedence, `gh` fallback via `execx.Fake`, `gh` missing / not logged in → hint, and the token is registered with the redactor (log something containing it → redacted).
- Client against `httptest` + `fakegh.New`: GetRepo private/public/admin false; registration/remove tokens; ListRunners pagination (fakegh option to force `per_page` small or seed >100 runners); FindRunner absent → nil; DeleteRunner; 401 (`bad-token`), 403 without and with rate-limit header, 404, 500 with message.
- Header assertions: `Authorization`, `Accept`, `X-GitHub-Api-Version`, `User-Agent`.
- Captured debug logs contain no token.
- fakegh: register flow end to end. Run the **fake runner's `config.sh`** (extracted via `testutil.WriteFakeRunner`) against an `httptest` fakegh with `OWNCI_DEV_GITHUB_API_URL` set: register with a fresh token → runner listed; reusing the token → config.sh exits 1; remove with a remove token → runner gone. (Skip with `t.Skip` if `curl` is not on PATH.)
- dev override test (`-tags dev`): `APIBaseURL()` honors the env var.

## Manual verification
```bash
make check
go run ./tools/fakegh --public-repos me/public-one --state /tmp/fakegh-t05.json &   # serves; prints export lines
sleep 2
eval "$(go run ./tools/fakegh --print-env)"; env | grep OWNCI_DEV_   # 5 OWNCI_DEV_* vars set
curl -s -H 'Authorization: Bearer x' http://127.0.0.1:8787/repos/me/private-one | python3 -m json.tool   # private:true, permissions.admin:true
curl -s -H 'Authorization: Bearer x' http://127.0.0.1:8787/repos/me/public-one | grep '"private"'      # false
curl -s -X POST -H 'Authorization: Bearer x' http://127.0.0.1:8787/repos/me/private-one/actions/runners/registration-token   # 201 FAKE-REG-…
curl -s -o /dev/null -w '%{http_code}\n' -H 'Authorization: Bearer bad-token' http://127.0.0.1:8787/repos/me/x  # 401
curl -s http://127.0.0.1:8787/_fake/runner.tar.gz | sha256sum   # equals OWNCI_DEV_RUNNER_SHA256 printed at startup
go test ./internal/github/... ./internal/testutil/fakegh/... -v -run 'Client|Register'   # PASS
kill %1
```

## Definition of done
- [ ] Global DoD.
- [ ] No token appears in any error string or log in tests (assert explicitly).
- [ ] fakegh is usable both as `go run ./tools/fakegh` and as `httptest` in tests.
- [ ] Implementation notes: client API, fakegh flags/endpoints, how to start fakegh for later tasks' manual flows.
