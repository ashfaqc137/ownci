# t06 — `ownci setup`: authenticate, gate, download, register, persist

> Read `tasks/00-context.md` first (D2, D3, D4, D5, D11, §4 config schema, §7 "config.sh"), then the Implementation notes of t01–t05.

## Goal
`ownci setup --repo owner/name` takes a machine from nothing to a **registered, configured runner**, safely and repeatably: preflight → token → repo checks (public-repo gate, admin) → existing-install detection → verified download → `config.sh` registration with the opt-in label → persisted OwnCI config → clear next step.

## Depends on / current state
- t02 config/Paths/Save, t03 doctor checks + `docker.Check` + `execx`, t04 `runner.Install` + `setup --download-only` + `--name/--force` flags + default name logic + fake runner, t05 `github` client, `ResolveToken`, `ParseRepo`, `WebBaseURL`, fakegh.

## What later tasks build on
- `config.RunnerConfig` filled in. t07 `start`/`status` require it.
- `runner.Configure(...)` (config.sh wrapper) and `runner.ReadRunnerFile(dir)` (`.runner` parsing with BOM). t07 status and t09 uninstall use them.
- The setup flow function `setup.Run(ctx, deps, opts)` (or similar, in `internal/runner` or a new `internal/setup` package). t08 inserts `.env` + hook installation at a marked step.

## Scope

### Flags
`--repo` (required unless `--download-only`), `--name`, `--labels` (comma list; extra labels added after `ownci`; validate `[A-Za-z0-9._-]`, max 10; `self-hosted`, `linux`, `x64`, `arm64` are rejected because they would defeat D2), `--force`, `--allow-public-repo`, `--dry-run`, `--download-only` (t04), `--skip-docker-check` (hidden; for debugging only, prints a warning).

### Flow (each step logs at debug; user sees concise progress lines `✓ …` / `✗ …`)
1. **Preflight:** run the doctor checks `platform`, `user`, `data-dir`, `docker` (t03). Any FAIL → abort with that check's hint. `runner-deps` FAIL → warn only (the runner may still work; t04 reports the version check).
2. **Existing install detection** (local, before any network):
   - `config.json` has a runner **and** `runners/<name>/.runner` exists → refuse: `"OwnCI is already set up for <repo> as '<name>'"`, hint: `ownci status`, or `ownci uninstall --unregister` then setup again, or `--force` to re-register in place.
   - `--force` with an existing registration → re-register in place using `config.sh --replace`. Do **not** delete files. The runner must not be running (check the t07 lock if present; until t07 exists, skip the check and leave a `// t07:` TODO).
   - A config mismatch (config says repo A, `--repo` B) → refuse even with `--force`. Hint: uninstall first. (D11: one runner per data dir.)
3. **Token:** `github.ResolveToken`. Print `✓ GitHub token from <source>`. Never print the token.
4. **Repo checks:** `GetRepo`.
   - Public (`private == false`) and no `--allow-public-repo` → refuse with an explanation of the fork-PR risk (D3) and a link to GitHub's self-hosted runner security docs (`https://docs.github.com/actions/hosting-your-own-runners/managing-self-hosted-runners/about-self-hosted-runners#self-hosted-runner-security`).
   - Public **with** the flag → a prominent warning that recommends repo setting *"Require approval for all outside collaborators"*.
   - `Admin == false` → refuse with the permission hint.
5. **Name collision on GitHub:** `FindRunner(name)`. Present and not `--force` → refuse ("a runner named X already exists on <repo>; choose --name or use --force to replace").
6. **Download & extract:** `runner.Install` (t04) unless the runner dir already contains an extracted runner and we're re-registering with `--force`.
7. **Registration token:** `RegistrationToken` (registered as a secret).
8. **Configure:** `runner.Configure(ctx, exec, dir, ConfigureOptions{URL: WebBaseURL()+"/"+repo, Token, Name, Labels: ["ownci", extra…], Replace: force})` runs
   `./config.sh --unattended --url … --token … --name … --labels ownci,… --no-default-labels --work _work [--replace]` with `Dir = runnerDir`. On failure, show the last 20 lines of config.sh output (redacted) and hint `ownci doctor`. Never log the full command unredacted.
9. **Post-configure verification:** `ReadRunnerFile(dir)` parses `.runner` (strip BOM) → `agentName` must equal `name`. Optionally `FindRunner` again: present → `✓ Runner registered on GitHub (id N)`. If absent, warn but don't fail (GitHub eventual consistency).
10. **// t08 insertion point:** write `.env` + install hooks (leave a clearly marked no-op call `installIsolation(...)` that t08 implements).
11. **Persist** `config.json` (`installedVersion` from the install result, `isolation: "docker-job-container"`, `createdAt`).
12. **Next steps** output:
    ```
    ✓ OwnCI is set up for owner/name as runner 'ownci-host' (labels: ownci)
    Next:
      1. Start contributing compute:   ownci start
      2. Target it from a workflow:    runs-on: [ownci]   and   container: <image>
         (jobs without 'container:' will be rejected by this runner)
    ```

### `--dry-run`
Perform steps 1–5 (read-only GitHub calls are allowed), and print what would be downloaded (URL, SHA) and the exact `config.sh` command with the token shown as `***`. No files are written except logs. No registration token is requested (it would be wasted).

### Failure atomicity
- If configure fails after download: keep the extracted runner (it's reusable) but **don't** write config.json. A re-run of setup reuses the extracted dir (detect "extracted but unregistered": the dir exists without `.runner`, so Install with implicit force for that dir only).
- If config.json saving fails after a successful registration → print a loud error with the manual cleanup hint (`ownci uninstall --unregister --name X`, available after t09).

## Out of scope
Starting the runner (t07). `.env`/hooks (t08; leave the insertion point). Unregistering (t09).

## Files
`internal/runner/{configure.go,runnerfile.go,*_test.go}`, `internal/setup/{setup.go,setup_test.go}` (or equivalent), `internal/cli/setup.go`.

## Tests (required)
Use `httptest` + `fakegh.New`, the fake runner tarball (served by fakegh) with the dev-override equivalent injected directly (pass `Asset` / base URLs via deps, not env, in unit tests), and the real fake `config.sh` via `execx.NewOS` (skip if bash/curl missing). Docker check via `execx.Fake`.
- Happy path: config.json written 0600 with all fields; `.runner` exists; fakegh state lists the runner with labels `[ownci]` only; `.fake-config-args` contains `--no-default-labels` and `--labels ownci` and `***`.
- Extra labels → `ownci,gpu`; forbidden label `self-hosted` → usage error.
- Public repo refused; allowed with the flag (warning in output).
- Non-admin refused with the permission hint.
- Second setup refused; `--force` re-registers with `--replace`; a different `--repo` is refused even with `--force`.
- GitHub name collision refused; `--force` proceeds.
- Docker preflight failure aborts **before** any network call (assert that the fakegh request count is 0).
- `--dry-run`: no runner dir, no config, no registration token requested; output contains `--token ***`.
- config.sh failure (fakegh rejects the token; simulate by having fakegh mark the token used beforehand) → no config.json, extracted dir retained, rerun succeeds.
- No token string appears in stdout, stderr or ownci.log (assert across all tests via a helper).

## Manual verification (dev build against fakegh; no real GitHub)
```bash
make check && make build-dev
rm -rf /tmp/ownci-t06 /tmp/fakegh-t06.json
go run ./tools/fakegh --state /tmp/fakegh-t06.json --public-repos me/pub --no-admin me/noadmin &
sleep 2; eval "$(go run ./tools/fakegh --print-env)"
D="--data-dir /tmp/ownci-t06"
./bin/ownci-dev $D setup --repo me/demo --dry-run          # expect: plan + config.sh line with --token ***, nothing created under runners/
./bin/ownci-dev $D setup --repo me/demo                    # requires Docker WSL integration ON (else: docker FAIL + hint — that's correct too)
#   expect: ✓ lines, "OwnCI is set up for me/demo as runner 'ownci-<host>' (labels: ownci)", Next steps
stat -c '%a %n' /tmp/ownci-t06/config/config.json           # 600
python3 -m json.tool /tmp/ownci-t06/config/config.json
curl -s $OWNCI_DEV_GITHUB_API_URL/_fake/state | python3 -m json.tool   # runner present, labels ["ownci"]
./bin/ownci-dev $D setup --repo me/demo; echo "exit=$?"    # refused: already set up, exit 1
./bin/ownci-dev $D setup --repo me/demo --force            # re-registers (--replace)
./bin/ownci-dev --data-dir /tmp/ownci-t06b setup --repo me/pub; echo "exit=$?"      # refused: public repo
./bin/ownci-dev --data-dir /tmp/ownci-t06c setup --repo me/noadmin; echo "exit=$?"  # refused: admin required
grep -c 'FAKE-REG' /tmp/ownci-t06/logs/ownci.log            # 0 (tokens redacted)
kill %1
```

## Definition of done
- [ ] Global DoD.
- [ ] D2 and D3 are enforced and covered by tests.
- [ ] Setup is safe to re-run. Every refusal has a hint.
- [ ] Implementation notes: the setup step list with the t08 insertion point, and the `Configure`/`ReadRunnerFile` signatures.

## Implementation notes

**Step list** (`internal/setup/setup.go`, `setup.Run(ctx, Deps, Options) error`; numbers match the spec):
1 `preflight` (platform, user, data-dir, docker; `runner-deps` and any WARN only warn; stops at the first FAIL, before any network) → 2 `detectExisting` (local) → 3 `ResolveToken` → 4 `checkRepo` (D3 public gate, admin) → 5 `checkNameFree` → `--dry-run` stops here (`dryRun`) → 6 `install` → 7 `RegistrationToken` → 8 `runner.Configure` → 9 `verify` → **10 `installIsolation(ctx, deps, runnerDir)` — the t08 insertion point (currently a no-op in `setup.go`; t08 writes `.env` + hook there)** → 11 `config.Save` → 12 next-steps output.

**APIs for later tasks**
- `runner.Configure(ctx, ex execx.Runner, dir string, o ConfigureOptions) error` with `ConfigureOptions{URL, Token, Name string; Labels []string; Replace bool}`; `runner.ConfigureArgs(o, maskToken bool) []string`. On failure the `*runner.Error` message holds the last 20 lines of config.sh output (redacted) and hints `ownci doctor`. t09 can reuse the same pattern for `config.sh remove`.
- `runner.ReadRunnerFile(dir) (RunnerFile{AgentID, PoolID int64; AgentName, ServerURL, GitHubURL, WorkFolder string}, error)` strips the BOM; accepts numeric or quoted ids. `runner.Registered(dir) bool` = `.runner` exists. Neither touches `.credentials*`.
- `setup.Deps{Paths, Platform, Exec, HTTP, Logger, Out, Err, Getenv, Geteuid, Hostname, AssetFor, APIBaseURL, WebBaseURL, Now}`, `setup.Options{Repo, Name, Labels, Force, AllowPublicRepo, DryRun, SkipDockerCheck}`, `setup.ValidateLabels`. `*setup.Error{Msg, Hint, Err, Usage}` has `UserHint()`; the cli maps `Usage` errors to exit 2 and renders the rest through the existing `hinted` path (exit 1).
- `cli.runSetup` builds `Deps` from `App` plus the existing test seams (`newExecRunner`, `geteuid`, `newHTTPClient`, `assetFor`).
- Test harness worth reusing (`internal/setup/setup_test.go`): `newEnv` (fakegh + `httptest` + temp data dir + request counter), `routeRunner` (sends `config.sh` and `Runner.Listener` to the real OS, everything else to an `execx.Fake`), `assertNoSecrets`, `burnFirstRegistrationToken`.

**Behavior decisions / deviations**
- **Bug found and fixed in t05's client**: `github.WithHTTPClient(nil)` set a nil client (the cli's `newHTTPClient()` returns nil), which would have panicked on the first real request. It now keeps the default client; test added.
- Reserved labels are broader than the spec's four: `self-hosted, linux, windows, macos, x64, x86, arm, arm64` (case-insensitive). Labels are trimmed and de-duplicated; a user-supplied `ownci` is dropped (it is always added). `--labels` is a comma-separated `StringSlice`.
- Name resolution: with a saved config and no `--name`, the configured name is reused (so a different hostname doesn't look like a different runner). An explicit `--name` or `--repo` that differs from the saved config is refused even with `--force` (D11), hint `uninstall --unregister`.
- A `.runner` file with no `config.json` (an earlier save failed) is treated as registered: it needs `--force`, with its own message.
- A corrupt `config.json` stops setup with the `Load` error plus a `--force` hint. With `--force` it is ignored with a warning.
- `--force` always passes `--replace` to config.sh (needed when a same-named runner exists on GitHub). With `--force` on an already-registered runner the extracted runner is reused (no download); the version comes from `Runner.Listener --version`.
- An extracted-but-unregistered directory (a failed earlier setup) is replaced on retry without `--force`.
- `createdAt` is preserved on `--force` re-registration.
- `--dry-run` still goes through steps 1–5, so it refuses non-admin/public repos too, and needs a token. It downloads nothing and requests no registration token.
- Preflight skipped Docker only with the hidden `--skip-docker-check` (prints a warning).
- The `TestStubsNotImplemented` list no longer includes `setup`; `TestSetupWithoutDownloadOnlyStillStub` was replaced by usage-error tests.

**Known limitations / TODOs**
- `// t07:` in `detectExisting`: `--force` must refuse while the runner is running (lock check).
- `installIsolation` is a no-op until t08, so a runner set up now does not yet enforce D1 (no `.env`).
- A failed `FindRunner` after configure only warns.

**Verification run (2026-10-15)**: `make check` green; new tests in `internal/setup`, `internal/runner` (`configure_test.go`), `internal/cli`, `internal/github`. Manual flow with `bin/ownci-dev` against `tools/fakegh` (spec's script, with `--skip-docker-check` where noted): dry-run printed the plan with `--token ***` and created nothing; setup wrote `config.json` mode 600 and fakegh listed one runner with labels `["ownci"]`; second setup refused (exit 1); `--force` re-registered with `--replace` and no re-download; `me/pub` refused (and allowed with the flag, with the warning); `me/noadmin` refused with the permission hint; `--repo me/other --force` refused; `grep -c FAKE-REG logs/ownci.log` → 0. The real Docker check passed on this machine (WSL integration is now on).
