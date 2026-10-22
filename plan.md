# OwnCI — Product Requirements Document

**Tagline:** Your PR. Your Compute.  
**Project:** OwnCI  
**Product type:** Open-source local CI compute agent  
**Primary language:** Go  
**Initial CI provider:** GitHub Actions  
**Initial isolation backend:** Docker  
**Distribution roadmap:** WSL → Linux → Windows → macOS  
**Document status:** Draft — implementation-ready specification

---

## 1. Product Overview

OwnCI is an open-source CLI that allows developers to contribute their local machine's compute resources to CI jobs.

GitHub Actions continues to own workflow orchestration, job queuing, job execution semantics, and pull request check reporting. OwnCI manages the local runner installation, configuration, lifecycle, and supported execution-isolation setup.

When a developer starts OwnCI, their machine becomes available to execute eligible CI jobs. The resulting check status appears in GitHub as it normally would for a self-hosted runner.

The long-term goal is to support multiple CI providers and interchangeable isolation backends without coupling the product to a single CI platform or container engine.

### Product philosophy

**Your PR. Your Compute.**

Developers should be able to contribute their own compute resources to CI without operating a separate CI infrastructure stack.

### Core principles

1. Reuse existing CI provider infrastructure.
2. Keep the core implementation small and maintainable.
3. Separate provider integrations from isolation implementations.
4. Make local execution transparent and observable.
5. Treat security and isolation as explicit requirements.
6. Expand platform support incrementally, validating each platform before declaring it supported.

## 2. Problem Statement

CI checks consume compute resources and may incur infrastructure costs. Developers often have machines capable of running many CI workloads, but using those machines as CI workers can require manual installation, registration, configuration, and process management.

Existing self-hosted runners provide the underlying capability, but developers must understand and maintain the runner lifecycle themselves.

OwnCI simplifies this process by providing a single CLI to install, configure, start, stop, and diagnose a locally managed CI runner.

### Problems OwnCI solves

- Reduce the friction of setting up local compute for CI.
- Reuse developer-owned compute resources.
- Preserve the existing CI provider's workflow and PR check experience.
- Simplify runner lifecycle management.
- Allow different CI providers and execution environments to be supported through explicit abstractions.
- Make the setup approachable for individual developers and small engineering teams.

### Success criterion

A developer installs OwnCI, configures it for a GitHub repository, starts it, and sees a real pull request check execute using their local machine's compute resources.

The execution environment must match the isolation policy OwnCI claims to enforce.

## 3. Target Users

### Primary audience

- Individual developers who want to contribute local compute to CI.
- Small engineering teams seeking to use existing development machines for eligible CI workloads.
- Open-source contributors interested in local-first CI infrastructure.

### Initial use cases

**Configure:** Install the official runner and register it with GitHub.

**Contribute compute:** Start OwnCI and allow eligible GitHub Actions jobs to run on the local machine.

**Stop contributing compute:** Stop the local runner without deleting its reusable configuration.

**Diagnose:** Inspect runner state, dependency health, and diagnostic logs.

## 4. Product Scope

### MVP

- Go CLI distributed as a platform-specific binary.
- GitHub Actions as the initial provider.
- Official GitHub Actions runner downloaded during setup.
- Persistent runner registration.
- Docker as the initial isolation backend.
- Local configuration and application-data management.
- Runner process supervision.
- Setup, start, stop, status, and uninstall commands.
- Real end-to-end GitHub PR check validation.
- Initial distribution targeting Linux execution inside WSL2.

### Explicitly out of scope for the MVP

- Implementing a custom CI runner protocol or workflow engine.
- GitLab CI integration.
- Podman, VM, or bare-metal execution backends.
- A hosted service or central orchestration control plane.
- Automatic interception of every workflow step without a supported execution mechanism.
- Guaranteed isolation of arbitrary untrusted PR code without a tested security design.
- Windows-native and macOS-native execution support in the first milestone.

## 5. High-Level Architecture

OwnCI is a Go control plane around an official CI provider runner and a supported local execution environment.

```text
Developer
   |
   v
OwnCI CLI (Go)
   |
   +-- Command layer
   +-- Configuration and local state
   +-- Provider abstraction
   |      |
   |      +-- GitHub Actions adapter
   |      +-- GitLab CI adapter (future)
   |
   +-- Runner manager
   |      |
   |      +-- Download and verify
   |      +-- Configure and register
   |      +-- Start / Stop / Status
   |      +-- Process supervision
   |
   +-- Isolation configuration
          |
          +-- Docker (initial)
          +-- Podman (future)
          +-- VM (future)
          +-- Bare metal (future)

Official CI runner <----> CI provider
       |
       v
Supported job execution environment
       |
       v
Developer's local compute
```

### Responsibility boundaries

**OwnCI**

- Provides CLI commands.
- Manages configuration and local storage.
- Downloads and validates runner artifacts.
- Coordinates provider-specific lifecycle operations.
- Starts, stops, and monitors runner processes.
- Validates dependencies and supported execution configurations.
- Provides diagnostics and manages resources that OwnCI owns.

**Official CI provider runner**

- Communicates with the provider.
- Receives assigned jobs.
- Implements the provider's workflow execution semantics.
- Executes workflow steps and supported actions.
- Reports logs and results to the provider.

**Isolation backend**

- Provides the configured execution environment where supported.
- Manages execution resources through its supported integration.
- Supports appropriate cleanup and lifecycle handling.

**CI provider**

- Owns workflow definitions, job queues, scheduling, execution semantics, and PR check reporting.

### Important execution constraint

The official runner does not necessarily execute every workflow step inside a Docker container. For GitHub Actions, container jobs and ordinary host-executed jobs have different execution behavior.

OwnCI must validate how the selected execution environment is actually enforced. It must not claim universal container isolation solely because Docker is installed or an isolation interface exists.

## 6. Extensibility Model

OwnCI must separate two dimensions.

### 6.1 CI provider

The provider adapter handles provider-specific runner setup, registration, lifecycle, and supported execution integration.

Initial implementation:

- GitHub Actions.

Future implementation:

- GitLab CI.

Provider differences must be represented explicitly rather than hidden behind an overly generic interface.

### 6.2 Isolation backend

The isolation backend represents the execution environment selected for supported jobs.

Initial implementation:

- Docker containers.

Future implementations:

- Podman containers.
- Virtual machines.
- Bare-metal execution.

Not every provider/backend combination will necessarily be supported.

The core must be able to reject unsupported combinations and explain why.

### Design rule

Provider orchestration remains provider-owned. OwnCI must not reimplement job polling, workflow interpretation, or provider-specific job execution.

## 7. CLI Contract

The CLI executable is named `ownci`.

### `ownci setup`

Provision the local runner.

Responsibilities:

1. Detect the operating system and CPU architecture.
2. Validate prerequisites.
3. Validate Docker availability for the selected execution mode.
4. Authenticate with GitHub.
5. Select the repository or supported registration target.
6. Obtain short-lived runner registration credentials.
7. Download and verify the official runner archive.
8. Configure the runner and its registration.
9. Persist OwnCI configuration.
10. Display setup results and the next action.

Setup should be repeatable where practical and must not overwrite existing configuration without appropriate safeguards.

### `ownci start`

Start contributing compute.

Responsibilities:

1. Validate configuration and installation.
2. Check the required dependencies.
3. Launch the official runner process.
4. Supervise the process and surface startup errors.
5. Display useful runtime status.

### `ownci stop`

Stop contributing compute.

Responsibilities:

1. Stop the managed runner gracefully.
2. Handle shutdown signals.
3. Preserve registration and configuration.
4. Report cleanup failures.

### `ownci status`

Report the actual local state, including:

- Installation status.
- Runner version.
- Process state.
- Provider and repository association.
- Selected isolation backend.
- Dependency health.
- Recent errors.

An installed runner must not be reported as running or online merely because its files exist.

### `ownci uninstall`

Remove the local installation.

Responsibilities:

- Stop managed processes.
- Offer an explicit option to unregister the runner.
- Remove OwnCI-managed files when requested.
- Avoid deleting unrelated user data.
- Explain any remaining files or provider-side registration.

### Configuration and data directory

Support an explicit data directory:

```bash
ownci setup --data-dir /path/to/ownci-data
```

Illustrative storage layout:

```text
ownci-data/
├── runner/        # Official runner installation
├── config/        # OwnCI configuration and metadata
├── logs/          # OwnCI diagnostics
└── cache/         # Optional reusable downloads
```

Use platform-appropriate application-data directories by default.

Protect sensitive configuration with suitable filesystem permissions. Never write secrets to ordinary diagnostic logs.

## 8. Runner Lifecycle and Version Management

OwnCI will use the official GitHub Actions runner rather than implementing its protocol.

### Initial strategy

- Pin a known-good runner version.
- Download it during setup.
- Verify artifact integrity before extraction.
- Store it locally.
- Reuse the persistent runner registration across sessions.
- Make runner upgrades deliberate and testable.
- Keep the OwnCI version distinct from the runner version.

JIT configuration is not required for the MVP. Persistent registration is sufficient for the initial lifecycle.

Fresh execution environments may be created per job where the selected execution mechanism supports that behavior.

## 9. Distribution and Platform Roadmap

Platform support will be delivered incrementally in the following order:

1. WSL2.
2. Native Linux.
3. Native Windows.
4. Native macOS.

A platform is supported only after its installation, lifecycle, runner connectivity, execution behavior, and cleanup have been validated.

### Phase 1 — WSL2 (initial MVP)

**Goal:** Prove OwnCI's core architecture and end-to-end CI workflow using a Linux environment inside WSL2.

Initial target:

- WSL2 running a supported Linux distribution.
- Linux build of the OwnCI CLI.
- Official Linux GitHub Actions runner.
- Docker-based job execution.
- Local configuration and runner storage within the WSL Linux filesystem.

Docker connectivity must be tested explicitly. The supported development setup may use Docker Engine inside WSL or Docker Desktop integration, but the MVP must document and validate the selected configuration.

Initial distribution method:

- Build a Linux binary for the target architecture.
- Provide a documented manual installation process.
- Provide a release artifact and checksum.
- Document prerequisite installation and Docker configuration.
- Validate setup, start, stop, status, and a real GitHub PR check.

**Exit criteria:**

- OwnCI installs and configures the official runner from WSL.
- The runner connects to GitHub.
- A real PR check executes using local compute.
- The documented execution-isolation behavior is verified.
- Stop and restart preserve reusable configuration.
- Errors and logs are useful enough to diagnose common failures.

### Phase 2 — Native Linux

**Goal:** Make OwnCI available on native Linux installations.

Add:

- Linux release binaries for supported CPU architectures.
- Documented installation instructions.
- Native Linux Docker Engine compatibility.
- Filesystem permission and process-lifecycle validation.
- Integration tests for the supported Linux environments.

The WSL and native Linux distributions should share as much implementation as possible.

**Exit criteria:**

- A developer can install OwnCI on a supported native Linux distribution.
- Runner registration and lifecycle work without WSL-specific assumptions.
- Docker execution and cleanup pass the integration tests.
- Installation and upgrade instructions are documented.

### Phase 3 — Native Windows

**Goal:** Provide a first-class Windows installation and lifecycle experience.

Add:

- Windows CLI binaries.
- Windows-specific paths, process management, signals, and shutdown behavior.
- Windows installation and upgrade instructions.
- Appropriate runner selection and Docker execution configuration.
- Tests for the selected Windows execution architecture.

The implementation must explicitly decide whether Windows uses the native Windows runner or a Linux execution environment through Docker Desktop/WSL2. These are different execution models and must not be treated as interchangeable.

Linux containers require an appropriate Linux container runtime. Native Windows support does not automatically imply Linux container compatibility.

**Exit criteria:**

- Installation, setup, start, stop, and status work on supported Windows versions.
- The supported provider runner and execution model are documented.
- A real PR check succeeds using the intended local compute.
- Process shutdown, paths, permissions, and cleanup are validated.

### Phase 4 — Native macOS

**Goal:** Provide native macOS distribution and validate the macOS execution model.

Add:

- macOS CLI binaries for supported architectures.
- macOS-specific application-data paths and process handling.
- Installation and upgrade instructions.
- Compatibility tests for the selected runner and Docker Desktop configuration.
- Documentation of required permissions and limitations.

The architecture must explicitly distinguish native macOS runner execution from Linux container execution through Docker Desktop.

**Exit criteria:**

- Installation and lifecycle commands work on supported macOS versions.
- The official runner connects to the provider.
- The supported execution model is validated with a real PR check.
- Cleanup and diagnostics work as documented.

### Distribution policy

For all platforms:

- Publish versioned release artifacts.
- Include checksums and appropriate integrity verification.
- Keep the OwnCI CLI version and runner version independently identifiable.
- Document external dependencies.
- Avoid bundling Docker or a container engine into the CLI.
- Use a platform-specific installation experience where appropriate.
- Do not advertise a platform as supported until its acceptance criteria pass.

Distribution can initially use GitHub Releases and manual installation. Package managers and more automated installation methods can be added as the project matures.

## 10. Security Requirements

OwnCI executes code originating from CI workflows. Security is a core product requirement.

- Do not assume containers automatically protect the host from malicious code.
- Minimize host directory mounts and credential exposure.
- Do not expose the Docker socket to untrusted job code without explicit justification and safeguards.
- Avoid privileged containers unless absolutely necessary.
- Protect configuration and credential files.
- Redact secrets from logs.
- Ensure cleanup only affects resources owned by OwnCI.
- Document which repositories and workflow trust levels are supported.
- Distinguish trusted repository workflows from untrusted fork PRs.
- Reject execution configurations that cannot satisfy the documented security policy.

The MVP must have an explicit trust model. It must not claim to safely execute arbitrary untrusted PR code without a tested isolation design.

## 11. Reliability Requirements

- Setup must detect existing installations and prevent accidental destructive changes.
- Start and stop must provide actionable errors.
- Status must distinguish installed, configured, running, and connected states where those states can be determined.
- Runner process failure must not be reported as healthy.
- Shutdown must preserve reusable registration state.
- Downloads must be verified before extraction.
- Upgrades must be deliberate and recoverable.
- Logs must support troubleshooting of common setup and startup issues.
- If the local machine sleeps or OwnCI stops, jobs may remain queued or become unavailable until the runner reconnects. This limitation must be documented.

## 12. MVP Acceptance Criteria

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

## 13. Long-Term Vision

OwnCI aims to become a provider-agnostic local CI execution platform.

### Provider expansion

- GitHub Actions.
- GitLab CI.
- Additional providers only when there is a concrete use case.

### Isolation expansion

- Docker.
- Podman.
- Virtual machines.
- Bare-metal execution.

### Potential capabilities

- Runner version policies and controlled upgrades.
- Multiple local runner configurations.
- Resource limits and concurrency management.
- Cache policies and cleanup.
- Better observability and diagnostics.
- Optional ephemeral runner registration.
- Additional installation channels.

These are future possibilities, not MVP commitments.

## 14. Design Principles

1. Reuse official provider runners and protocols.
2. Keep the core small and modular.
3. Introduce interfaces only where implementations genuinely vary.
4. Separate provider concerns from isolation concerns.
5. Model capability differences explicitly.
6. Treat security boundaries as implementation requirements, not naming conventions.
7. Build and validate one platform at a time.
8. Prefer observable behavior and actionable errors.
9. Prioritize the end-to-end PR check over speculative features.

## 15. Initial Engineering Sequence

1. Create the Go module and `ownci` CLI skeleton.
2. Implement configuration, local storage, and platform path handling.
3. Implement runner download, integrity verification, and version pinning.
4. Implement GitHub authentication, registration, and configuration.
5. Implement process supervision and lifecycle commands.
6. Validate a real GitHub PR check using local compute.
7. Validate the Docker execution model and cleanup behavior.
8. Complete the WSL2 distribution and documentation.
9. Add reliability, diagnostics, and security tests.
10. Expand to native Linux, then Windows, then macOS.

## 16. Open Technical Questions

These questions must be resolved through implementation and integration testing:

1. Which exact GitHub Actions runner configuration will enforce the intended Docker execution behavior?
2. Must workflows explicitly declare `container:`, or will a different supported deployment model be used?
3. Will the initial WSL setup use Docker Engine inside WSL or Docker Desktop integration?
4. Which WSL distribution and CPU architecture will be supported first?
5. How will GitHub authentication and runner registration minimize required permissions?
6. Which runner version will be pinned initially?
7. What trust model will apply to pull requests from forks and other untrusted sources?
8. How will native Windows choose between a Windows runner and a Linux execution environment?
9. Which CPU architectures and OS versions will be supported in each distribution phase?

## Final Product Definition

OwnCI is an open-source Go CLI that manages an official CI provider runner on a developer's machine and enables supported CI jobs to use local compute through a documented execution-isolation model.

The first release targets WSL2, using the official GitHub Actions runner and Docker. Native Linux follows, then Windows, and finally macOS.

The first milestone is a real pull request check, visible in GitHub, whose compute executes locally and whose isolation behavior has been verified.

**OwnCI — Your PR. Your Compute.**
