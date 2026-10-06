# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What This Repo Is

DCM Utilities — a shared repository for common scripts and tooling used across the [dcm-project](https://github.com/dcm-project) ecosystem. Houses the E2E deploy script and the E2E test suite.

This repo contains **bash scripts and a Go-based E2E test suite**. The shell scripts have no build step; the Go tests in `tests/e2e/` are compiled on-demand by Ginkgo. It also contains E2E test plans and results under `test-plans/`.

## Cursor Integration

This repo includes `.cursor/` with rules, prompts, and agents for Cursor IDE. When using Cursor, context is loaded automatically from `.cursor/rules/` and task-specific prompts are available via `@<prompt-name>`. See `.cursor/prompts/README.md` for the full list.

## Important: Keep Docs Up to Date

When making changes in a PR, always check whether `CLAUDE.md`, `README.md`, and relevant `.cursor/` files need updating to reflect the change. This includes new flags, changed behavior, new scripts, or modified conventions. Update all affected files as part of the same PR.

## Linting

```bash
shellcheck scripts/*.sh scripts/kind/*.sh scripts/compose/*.sh scripts/kubevirt/*.sh tests/*.sh
```

CI runs ShellCheck on changed `*.sh` files via `.github/workflows/lint.yaml` (only on PRs/pushes to `main`, only on changed files). Always validate locally before pushing.

## Key Script: `scripts/deploy-dcm.sh`

Deploys the full DCM stack for E2E testing by cloning control-plane and running its selected Compose model. Auth mode also loads `deploy/compose.auth.yaml` with the `auth` profile. The script polls the control-plane health endpoint until it responds 2xx.

**Flow:** clone control-plane → bootstrap `deploy/.env` → run `podman-compose up -d` with the base Compose model and, when auth is enabled, `deploy/compose.auth.yaml` plus the `auth` profile → verify containers running → poll `/api/v1alpha1/health` → collect container versions from Quay.io API → write `dcm-versions.json`.

**Compose credentials:** After clone, the script copies `deploy/.env.example` to `deploy/.env` when missing and upserts DB/auth keys (lab defaults unless overridden by shell env). Control-plane compose reads these via `env_file: .env`. Pass `--auth-enabled` or set `AUTH_DISABLED=false` to load `deploy/compose.auth.yaml`, add the compose `auth` profile (Keycloak), and write auth credentials into `.env`. The authentication overlay is loaded before user and provider compose overrides so later overrides retain precedence.

**Modes:** The script has four mutually exclusive modes:
- **Deploy** (default): full clone + bring-up + health check. Pass `--cleanup-on-failure` to auto-teardown on error (default leaves partial state for debugging).
- `--running-versions`: query already-running containers, resolve git SHAs via Quay.io API, write `dcm-versions.json`
- `--tear-down`: stop containers, remove volumes, delete deploy directory
- `--cluster-prereqs-only`: install ACM/MCE/CNV on the cluster, then exit (no compose stack). Used by Helm CI before `helm upgrade --install`. With `--agent-embedded-sps`, auto-enables `--deploy-cnv` for `vm` and `--deploy-acm` for `cluster` when those flags are not set.

**Version pinning:** Pass `--version <TAG>` to pin all DCM service images to a specific version. Three modes:
- `--version main` — use `:main` images (the default)
- `--version v0.1.0-rc.1` — pin all images to an explicit tag
- `--version release` — auto-resolve the latest semver tag from Quay.io

When a non-main version is specified, `--control-plane-branch` is auto-derived to the corresponding release branch (e.g. `v0.1.0-rc.1` → `release/v0.1.0`) unless explicitly passed.

**Service providers:** Configured via `providers/*.conf` files (see "Provider Registry" below). Enable with `--<label>-service-provider` or `--all-service-providers`.

**Environment agent (embedded SPs):** Pass `--with-environment-agent` with `--agent-embedded-sps LIST` to enable the control-plane `environment-agent` Compose profile in the same bring-up. `LIST` is comma-separated: `container`, `vm`, `cluster`, `storage`, `network`. The script resolves a cluster kubeconfig (required; OCP path, no Kind), writes it to `AGENT_KUBECONFIG_HOST` as an absolute path, upserts agent env into `deploy/.env`, and polls agent health on `http://localhost:${AGENT_PORT}/api/v1alpha1/health` (default port **8081**, override with `--agent-port` / `AGENT_PORT`). Embedded SPs are mutually exclusive with overlapping standalone provider flags (e.g. embedded `vm` vs `--kubevirt-service-provider`). Embedding `cluster` requires `SP_CLUSTER_NAMESPACE` (default `clusters`) and `SP_PULL_SECRET` (or `ACM_CLUSTER_SP_PULL_SECRET`); if unset, pull secret is resolved from `openshift-config/pull-secret`. Prefer this path for network SP (`AGENT_EMBEDDED_SPS=network`); the standalone `--k8s-network-service-provider` flag is legacy. Other useful overrides: `ENVIRONMENT_AGENT_VERSION`, `AGENT_NAME`, `AGENT_ENVIRONMENT`, `AGENT_COST`, `SP_CONTAINER_NAMESPACE`, `SP_VM_NAMESPACE`, `SP_STORAGE_NAMESPACE`, `SP_BASE_DOMAIN`. Teardown auto-detects agent mode from `AGENT_EMBEDDED_SPS` in `deploy/.env`.

**ACM/MCE deployment:** Pass `--deploy-acm` or `--deploy-mce` to install Red Hat ACM or MCE on the OCP cluster before starting the DCM stack. This clones the [acm-cluster-service-provider](https://github.com/dcm-project/acm-cluster-service-provider) repo and runs its `hack/deploy-acm-mce.sh` script. Can take 10–20 minutes. Requires `oc` and `jq`. These are opt-in flags, not enabled by default.

**Cluster authentication:** When any provider is enabled **or** the environment agent is enabled, the script resolves cluster access in priority order: explicit `--kubeconfig`, existing `oc`/`kubectl` session, or `oc login` via `--cluster-api` + `--cluster-password`.

**Control-plane authentication:** Pass `--auth-enabled` (or set `AUTH_DISABLED=false`) to load the auth Compose override and profile, start Keycloak, and enable JWT validation. Use the same flag on `--tear-down` when tearing down an auth-enabled stack. The E2E suite supports authenticated runs; use `--auth-disruptive` to opt in to the disruptive authentication phase. There is no `--auth-advanced` runner flag. Agent + auth together is not the documented default path yet.

**Podman Compose networking:** The script uses `--in-pod false` by default so services run on the Compose bridge network and resolve service names through its DNS. Set `PODMAN_COMPOSE_IN_POD=true` only when pod-mode networking is required by the environment.
**GitOps reconciliation:** Pass `--gitops` to add the separate published `dcm-gitops` reconciler container. It uses the same PostgreSQL database as control-plane and persists cloned repositories in the Compose `gitops_data` volume. Set `DCM_GITOPS_VERSION` to pin only that image, or use `--version` to pin all DCM images.

Run `./scripts/deploy-dcm.sh --help` for all flags and environment variable overrides.

## Local dev scripts

| Path | Purpose |
|------|---------|
| `scripts/kind/` | Kind + compose networking (kubeconfig, connect/disconnect) |
| `scripts/compose/` | Compose network teardown (not Kind-specific) |
| `scripts/kubevirt/` | KubeVirt install on any cluster (`kubectl` context) |

See each directory's `README.md` for env vars. Consumer repos set `UTILITIES_DIR ?= ../utilities`.

**Operational behavior (keep docs in sync when changing these scripts):**

- `install-kubevirt.sh` — Best-effort skip when `kv` CRs are visible; reminds the operator to
  verify KubeVirt/CNV is not already installed before running.
- `kind-disconnect.sh` — Uses `kind_try_resolve_from_context` from `kind-env.sh`; exits 0 when
  the current context is not Kind.
- `network-teardown.sh` — Explicit `CONTAINER_ENGINE` is never overridden by auto-detect; the
  `remove` step may run after compose has deleted networks on that runtime. Auto-detect only when
  `CONTAINER_ENGINE` is unset.

### Provider Registry

Service providers are defined declaratively in `providers/*.conf` files. Each conf file specifies:

| Key | Purpose |
|-----|---------|
| `PROVIDER_LABEL` | Short name for display and flag generation |
| `PROVIDER_FLAG` | CLI flag name (e.g. `kubevirt-service-provider`) |
| `COMPOSE_PROFILE` | Compose profile name from control-plane deploy compose (if applicable) |
| `COMPOSE_OVERRIDE` | Compose override file relative to repo root (if applicable) |
| `CLI_REQUIREMENT` | CLI tool needed: `oc`, `oc-or-kubectl`, or empty |
| `NAMESPACE_FLAG` / `NAMESPACE_ENV` / `NAMESPACE_DEFAULT` | Namespace configuration |
| `KUBECONFIG_EXPORT` / `NAMESPACE_EXPORT` | Env var names for compose substitution |
| `VALIDATE_HOOK` | Function name for provider-specific validation |

**To add a new provider:** drop a `.conf` file in `providers/` and (if needed) add a validation hook function in `deploy-dcm.sh`. No other changes to the deploy script are required — flags, usage, arg parsing, and env exports are all generated from the registry.

Current providers: `kubevirt`, `k8s-container`, `k8s-storage`, `k8s-network`, `acm-cluster`, `three-tier-app-demo`, `three-tier-app-demo-2`, `three-tier-app-demo-3`.

Host ports published for direct SP access (compose overrides): KubeVirt **8081**, k8s-container **8082**, ACM cluster **8083**, three-tier **8084**–**8086**, k8s-container-2/3 **8087**–**8088**, k8s-storage **8089**. Environment-agent (via `--with-environment-agent`) also publishes **8081** by default — combining with standalone KubeVirt requires `--agent-port` ≠ 8081.

**k8s-network:** Embed via `--with-environment-agent --agent-embedded-sps network` ([environment-agent](https://github.com/dcm-project/environment-agent)). Do not rely on the legacy utilities `--k8s-network-service-provider` / Quay standalone image path ([FLPATH-4881](https://redhat.atlassian.net/browse/FLPATH-4881) obsolete). See `test-plans/FLPATH-3227-k8s-network-sp.md`.

### Script Structure

The script is organized into sections separated by comment banners. Key functions:

| Function | Purpose |
|----------|---------|
| `load_providers` | Scans `providers/*.conf` and populates parallel arrays |
| `validate_deploy_dir` | Guards against `rm -rf` on system paths |
| `check_required_tools` | Verifies `git`, `podman`, `curl`, `jq`, etc. are installed |
| `tear_down` | Stops containers, removes volumes, deletes deploy dir |
| `resolve_kubeconfig` | Resolves cluster credentials (kubeconfig file, existing session, or `oc login`) |
| `validate_kubevirt_provider` | Checks CNV CRDs and creates namespace via `oc` |
| `ensure_provider_namespace` | Ensures a namespace exists via `oc` or `kubectl` |
| `validate_k8s_container_provider` | Validates k8s container SP prerequisites |
| `validate_k8s_storage_provider` | Validates k8s storage SP prerequisites |
| `validate_acm_cluster_provider` | Validates ACM cluster SP prerequisites |
| `ensure_deploy_env` | Bootstraps `deploy/.env` from `.env.example` and upserts credentials |
| `upsert_deploy_env_var` | Idempotently sets a key in `deploy/.env` |
| `resolve_provider_cli` | Resolves `oc`/`kubectl` per provider's `CLI_REQUIREMENT` |
| `collect_provider_compose` | Collects compose profiles/overrides for an enabled provider |
| `verify_health` | Confirms all compose services are running, then polls health endpoints with timeout |
| `resolve_git_sha` | Queries Quay.io tag API to map image digest → git commit SHA |
| `get_running_versions` | Iterates running containers, calls `resolve_git_sha`, writes JSON |

Argument parsing happens inline (not in a function) via a `while/case` loop. Provider flags are matched dynamically via `match_provider_flag` against the loaded registry.

## Shell Conventions

- Scripts use `set -euo pipefail` and `bash` (not POSIX sh).
- Constants are `readonly` at the top of the file.
- Logging helpers: `log()` for section headers (`==>`), `info()` for indented details, `err()` for stderr.
- Argument parsing uses a `while/case` loop with `require_arg` validation; flags take precedence over environment variables of the same name.
- Compose profiles are passed via array expansion: `${COMPOSE_PROFILES[@]+"${COMPOSE_PROFILES[@]}"}` (safe for empty arrays under `set -u`).

## `test-plans/`

E2E test plans and results for DCM service providers. Each file is named by Jira ticket (e.g. `FLPATH-3014-container-sp-api.md`). Test results are in `e2e-test-results-<date>.md`.

Test plans include:
- Scope and tier breakdowns (what's testable at each infrastructure level)
- Cross-references to upstream repo test plans (`.ai/test-plans/`) to avoid duplicating unit/integration coverage
- Code-verified behavior notes from actual PR implementations

## E2E Test Suite

The `tests/` directory contains the Ginkgo/Gomega E2E test framework.

### Structure

```
tests/
  run-e2e.sh                         # Test harness: deploy → resolve CLI → test → teardown
  compose-sp-test.yaml               # Compose override: publishes container SP port (auto-injected by provider registry)
  compose-acm-cluster-sp.yaml        # Compose override: adds ACM cluster SP service (auto-injected by provider registry)
  e2e/
    go.mod                            # Standalone Go module
    internal/resolve/                 # Plain Go package (no e2e build tag): pure STI-resolution
                                       # logic with real go test unit coverage — see Conventions below
    suite_test.go                     # Ginkgo bootstrap
    api_helpers_test.go               # HTTP helpers, env config, BeforeSuite connectivity check
    cli_helpers_test.go               # CLI binary execution helper (runDCM)
    sp_helpers_test.go                # Container SP direct-API + NATS + kubectl/podman helpers
    sp_acm_cluster_helpers_test.go    # ACM Cluster SP HTTP helpers + init/require guards
    api_health_test.go                # Health endpoint smoke tests (Label: "smoke")
    api_providers_test.go             # Provider CRUD lifecycle tests (API)
    api_policies_test.go              # Policy CRUD lifecycle tests (API)
    sp_container_api_test.go          # Container SP CRUD tests (Label: "sp", "container")
    sp_container_status_test.go       # Container SP NATS status events (Label: "sp", "container", "nats")
    sp_acm_cluster_api_test.go        # ACM Cluster SP API tests (Label: "sp", "acm-cluster")
    core_platform_test.go             # Core platform provisioning happy path (Label: "core", "platform")
    cli_version_test.go               # CLI version command test (Label: "smoke", "cli")
    cli_providers_test.go             # CLI sp provider read tests (Label: "cli")
    cli_policy_test.go                # CLI policy CRUD tests (Label: "cli")
    rehydration_helpers_test.go        # Rehydration types, provider discovery, lifecycle helpers
    rehydration_happy_path_test.go     # Core rehydration flow (Label: "rehydration", "happy-path")
    rehydration_failover_test.go       # Failover + deferred delete (Label: "rehydration", "failover", "disruptive")
    rehydration_policy_test.go         # Sovereignty + intent (Label: "rehydration", "policy")
    rehydration_negative_test.go       # Error paths + concurrency (Label: "rehydration", "negative")
    rehydration_data_integrity_test.go # Integrity + regressions (Label: "rehydration", "integrity")
    rehydration_api_contract_test.go   # RFC 7807 response shapes (Label: "rehydration", "contract")
    rehydration_cli_test.go            # CLI rehydrate commands (Label: "rehydration", "cli")
    rehydration_persistence_test.go    # SPRM restart, ServiceType (Label: "rehydration", "disruptive")
```

### Running Tests

```bash
make test-e2e              # Run all E2E tests (stack must be running)
make test-smoke            # Run smoke tests only (health checks + CLI version)
make test-cli              # Run CLI tests only
make test-sp               # Run container SP tests (SP must be deployed)
make test-acm-sp           # Run ACM cluster SP tests (ACM SP must be deployed)
make test-core             # Run core platform tests (full control plane provisioning flow)
make test-rehydration      # Run all rehydration tests (multi-provider + podman required)
make test-rehydration-safe # Run non-disruptive rehydration tests only
make test-rehydration-cli  # Run rehydration CLI tests only
make test-e2e-full         # Full lifecycle: deploy → test → teardown
make download-cli          # Download latest DCM CLI from GitHub releases
```

The test harness (`tests/run-e2e.sh`) supports `--skip-deploy`, `--skip-teardown`, `--skip-cli`, `--dcm-cli-path`, `--label-filter`, `--gateway-url`, `--junit-report`, and service provider flags (`--k8s-container-service-provider`, `--all-service-providers`, `--kubeconfig`, `--cluster-api`, `--cluster-password`, etc.).

All test targets support JUnit XML output: `make test-e2e JUNIT_REPORT=results.xml`

### Test Layers

| Layer | What it tests | Label |
|-------|--------------|-------|
| **Core platform tests** | Full provisioning flow through control plane | `core`, `platform` |
| **API tests** | HTTP CRUD operations against the control plane | (none) |
| **SP tests** | Container SP direct API + NATS status events | `sp`, `container` |
| **ACM SP tests** | ACM Cluster SP API (health, registration, validation, CRUD) | `sp`, `acm-cluster` |
| **Cluster tests** | Tests requiring `kubectl`/`oc` cluster access | `cluster` |
| **Disruptive tests** | Tests that stop/start infrastructure (e.g. NATS) | `disruptive` |
| **CLI tests** | DCM CLI binary against the live stack | `cli` |
| **Smoke tests** | Health checks + CLI version (quick validation) | `smoke` |
| **Rehydration tests** | Rehydration lifecycle, failover, policy, integrity | `rehydration` |
| **Rehydration subtypes** | happy-path, failover, policy, negative, integrity, contract | see file headers |

### CLI Binary Resolution

CLI tests require the `dcm` binary. Resolution order:
1. `DCM_CLI_PATH` env var or `--dcm-cli-path` flag
2. `dcm` in `$PATH`
3. Previously downloaded binary in `bin/dcm` (from `make download-cli`)
4. Auto-download from GitHub releases (`dcm-project/cli`, requires `gh`)

CLI tests are skipped (not failed) if no binary is available.

### Conventions

- All test files use `//go:build e2e` build tag, **except** `tests/e2e/internal/*` packages (e.g. `internal/resolve`), which hold pure, non-network logic with no `e2e` tag and real unit-test coverage. CI runs `go test ./internal/...` (unlike the e2e-tagged suite, which CI only vets/compiles — see `validate-tests.yaml`)
- API tests use raw `net/http` (no generated clients) for independence from service repos
- CLI tests use `os/exec` to run the actual binary (not in-process Cobra)
- `DCM_GATEWAY_URL` env var overrides the control plane API endpoint (default: `http://localhost:8080/api/v1alpha1`)
- `DCM_CONTAINER_SP_URL` env var overrides the container SP endpoint (default: `http://localhost:8082/api/v1alpha1`)
- `DCM_STORAGE_SP_URL` env var overrides the storage SP endpoint (default: `http://localhost:8089/api/v1alpha1`)
- `DCM_ACM_CLUSTER_SP_URL` env var overrides the ACM cluster SP endpoint (default: `http://localhost:8083/api/v1alpha1`)
- `DCM_AGENT_URL` env var overrides the environment-agent endpoint (default: `http://localhost:8081/api/v1alpha1`)
- `DCM_NETWORK_SP_ENABLED=true` requires the embedded Network SP. The suite waits up to 30 seconds for the agent and provider to become ready, then fails if they do not. When unset or `false`, Network SP specs skip immediately.
- `DCM_NATS_URL` env var overrides the NATS server (default: `nats://localhost:4222`)
- `DCM_CLI_PATH` env var specifies the CLI binary path
- `DCM_CONTAINER_PROVIDER_NAME` env var overrides which container provider to target in core platform tests (default: first `service_type=container` provider found)
- Ginkgo labels (`auth`, `smoke`, `cli`, `sp`, `container`, `acm-cluster`, `nats`, `cluster`, `disruptive`, `core`, `platform`, `rehydration`, `happy-path`, `failover`, `policy`, `negative`, `integrity`, `contract`) enable selective test runs via `--label-filter`
- SP tests skip gracefully if the container SP or ACM cluster SP isn't reachable (no hard failure)
- Cluster tests skip gracefully if `kubectl`/`oc` is unavailable or the cluster is unreachable
- Disruptive tests skip if `podman` is unavailable; exclude from normal runs with `--label-filter '!disruptive'`

## `dcm-versions.json`

Artifact produced by the deploy script (both deploy mode and `--running-versions`). Maps container image names to their digest and the git commit SHA that produced the image (resolved via Quay.io tag API). The file is gitignored at the repo root; the copy under `scripts/` is the authoritative output location.
