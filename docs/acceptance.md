# Acceptance record (MVP)

> A working copy of PRD §12 (`plan.md` is not edited). Tick an item **only** with evidence from running
> [`e2e.md`](e2e.md) against real GitHub: a run URL, a log excerpt or a screenshot path, with the date.
>
> **Status: NOT YET EXECUTED.** Everything below is unchecked: the offline parts (unit/integration tests with
> fakes, the fake-GitHub flow, a real Docker cleanup check) are covered by `make check` and the task notes in
> `tasks/`, but none of the real-GitHub items has been verified yet.

| Field | Value |
|---|---|
| Date | _(fill in)_ |
| Machine / WSL distro / kernel | |
| Docker version | |
| OwnCI version (commit) | |
| Runner version (pinned → actual) | |
| Sandbox repository | |

## Checklist (from PRD §12)

### CLI and installation

- [ ] `ownci setup` provisions the official runner.
- [ ] The runner archive is verified before extraction.
- [ ] Runner files and OwnCI configuration are stored locally.
- [ ] GitHub registration succeeds with appropriate credentials.
- [ ] Configuration persists across restarts.
- [ ] `ownci start` starts the runner.
- [ ] `ownci stop` stops the runner gracefully.
- [ ] `ownci status` reports meaningful state.
- [ ] `ownci uninstall` supports safe cleanup.

### End-to-end CI

- [ ] A real GitHub Actions workflow targets the local runner.
- [ ] A pull request displays the expected check result in GitHub.
- [ ] Job compute runs on the developer's local machine.
- [ ] The selected Docker execution mode is validated.
- [ ] The actual execution boundary is verified rather than assumed.
- [ ] Cleanup and repeated job execution are tested.

### WSL distribution

- [ ] The Linux OwnCI binary runs in the supported WSL2 configuration.
- [ ] Docker connectivity is documented and validated.
- [ ] Runner setup and lifecycle work from WSL.
- [ ] The end-to-end PR check passes.
- [ ] Installation and troubleshooting instructions are available.

### Security and documentation

- [ ] Secrets are not written to ordinary logs.
- [ ] The trust model and isolation limitations are documented.
- [ ] Runner and CLI versions can be inspected.
- [ ] Unsupported configurations fail clearly.

## Evidence

| Item | Evidence (URL / excerpt / path) | Date |
|---|---|---|
| PR check (step 6) | | |
| No-container rejection (step 7) | | |
| Poison test (step 8) | | |
| Stop / restart without re-registration (step 9) | | |
| Kill test (step 10) | | |
| No tokens in logs (step 11) | | |
| Uninstall (step 12) | | |

## Findings

_(copy the Findings table from `e2e.md`)_
