.PHONY: help e2e-up test-e2e test-smoke test-cli test-sp test-acm-sp test-kubevirt-sp test-core test-rehydration test-rehydration-safe test-rehydration-cli e2e-down test-e2e-full download-cli cli-version lint

# Set JUNIT_REPORT to a filename to produce JUnit XML output.
# Example: make test-e2e JUNIT_REPORT=results.xml
JUNIT_REPORT ?=
CLI_VERSION ?= main
GINKGO_BASE = go run github.com/onsi/ginkgo/v2/ginkgo -r -v --tags=e2e
ifdef JUNIT_REPORT
GINKGO_BASE += --junit-report=$(JUNIT_REPORT)
endif

help: ## Show all available targets
	@grep -hE '^[a-zA-Z0-9_-]+:.*## ' $(MAKEFILE_LIST) | awk -F ':.*## ' '{printf "  %-18s %s\n", $$1, $$2}'

e2e-up: ## Deploy the full DCM stack
	./scripts/deploy-dcm.sh

test-e2e: ## Run all E2E tests (stack must be running)
	cd tests/e2e && $(GINKGO_BASE) .

test-smoke: ## Run smoke tests only (health checks + CLI version)
	cd tests/e2e && $(GINKGO_BASE) --label-filter=smoke .

test-cli: ## Run CLI tests only (stack must be running)
	cd tests/e2e && $(GINKGO_BASE) --label-filter=cli .

test-sp: ## Run all service provider tests (SPs must be deployed with ports published)
	cd tests/e2e && $(GINKGO_BASE) --label-filter=sp .

test-acm-sp: ## Run ACM cluster SP tests only
	cd tests/e2e && $(GINKGO_BASE) --label-filter=acm-cluster .

test-kubevirt-sp: ## Run KubeVirt SP tests only (KubeVirt cluster required)
	cd tests/e2e && $(GINKGO_BASE) --label-filter=kubevirt .

test-core: ## Run core platform tests (full control plane provisioning flow)
	cd tests/e2e && $(GINKGO_BASE) --label-filter=core .

test-rehydration: ## Run all rehydration tests (multi-provider stack with podman required)
	cd tests/e2e && $(GINKGO_BASE) --label-filter=rehydration .

test-rehydration-safe: ## Run non-disruptive rehydration tests only
	cd tests/e2e && $(GINKGO_BASE) --label-filter='rehydration && !disruptive' .

test-rehydration-cli: ## Run rehydration CLI tests only
	cd tests/e2e && $(GINKGO_BASE) --label-filter='rehydration && cli' .

e2e-down: ## Tear down the DCM stack
	./scripts/deploy-dcm.sh --tear-down

test-e2e-full: ## Deploy, test, and tear down (full lifecycle)
	./tests/run-e2e.sh $(if $(JUNIT_REPORT),--junit-report $(JUNIT_REPORT))

download-cli: ## Download DCM CLI from GitHub releases (CLI_VERSION=main)
	@command -v gh >/dev/null 2>&1 || { echo "ERROR: gh CLI required (https://cli.github.com)"; exit 1; }
	@mkdir -p bin
	@OS=$$(uname -s | tr '[:upper:]' '[:lower:]'); ARCH=$$(uname -m); case "$$ARCH" in x86_64) ARCH=amd64;; aarch64) ARCH=arm64;; esac; echo "==> Downloading DCM CLI ($(CLI_VERSION)) for $$OS/$$ARCH"; gh release download $(CLI_VERSION) --repo dcm-project/cli --pattern "cli_*_$${OS}_$${ARCH}.tar.gz" --dir bin --clobber; tar -xzf bin/cli_*_$${OS}_$${ARCH}.tar.gz -C bin dcm; rm -f bin/cli_*_$${OS}_$${ARCH}.tar.gz; chmod +x bin/dcm; echo "    Downloaded to bin/dcm"

CLI_VERSION_FILE ?= dcm-cli-version.json
cli-version: ## Write DCM CLI version info to JSON file
	@DCM_BIN="$${DCM_CLI_PATH:-}"; if [[ -z "$$DCM_BIN" ]]; then if command -v dcm &>/dev/null; then DCM_BIN="$$(command -v dcm)"; elif [[ -x bin/dcm ]]; then DCM_BIN="bin/dcm"; else echo "ERROR: dcm binary not found (set DCM_CLI_PATH or run make download-cli)"; exit 1; fi; fi; RAW="$$("$$DCM_BIN" version 2>&1)"; echo "$$RAW" | awk '/^dcm version/{v=$$0; sub(/^dcm version /,"",v)} /commit:/{sub(/^ *commit: */,""); c=$$0} /built:/{sub(/^ *built: */,""); b=$$0} /go:/{sub(/^ *go: */,""); g=$$0} END{printf "{\"version\":\"%s\",\"commit\":\"%s\",\"built\":\"%s\",\"go\":\"%s\"}\n",v,c,b,g}' | jq . > $(CLI_VERSION_FILE); echo "==> Wrote $(CLI_VERSION_FILE)"; cat $(CLI_VERSION_FILE)

lint: ## Lint all shell scripts with ShellCheck
	shellcheck scripts/*.sh tests/*.sh
