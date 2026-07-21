# KubeVirt Service Provider E2E Testing

This document describes the E2E test suite for the KubeVirt Service Provider.

## Overview

The KubeVirt SP test suite validates VM lifecycle management through the DCM platform, including:
- Direct KubeVirt SP API tests (health, CRUD operations)
- Full platform integration tests (catalog → policy → placement → VM provisioning)
- VM status monitoring and NATS event verification

## Test Files

| File | Purpose | Labels |
|------|---------|--------|
| `sp_kubevirt_helpers_test.go` | Helper functions, VM spec builders, provider discovery | — |
| `sp_kubevirt_api_test.go` | Direct KubeVirt SP API tests (health, CRUD) | `sp`, `kubevirt` |
| `core_platform_kubevirt_test.go` | End-to-end VM provisioning via DCM catalog | `core`, `platform`, `kubevirt` |

## Prerequisites

### Cluster Requirements

- **OpenShift 4.x** with CNV (Container-Native Virtualization) installed, OR
- **Kubernetes 1.25+** with KubeVirt installed
- At least one **StorageClass** available for PVC provisioning
- Service account with VirtualMachine CRUD permissions in the configured namespace

### DCM Stack

- Full DCM control plane deployed (`./scripts/deploy-dcm.sh`)
- KubeVirt SP running and registered with DCM
- NATS server running (for status events)

### Cluster Access

The KubeVirt SP requires cluster access. Configure via:

```bash
# Option 1: Kubeconfig file
export SP_K8S_KUBECONFIG=/path/to/kubeconfig

# Option 2: Existing oc/kubectl session
oc login <cluster-url>

# Option 3: Deploy script handles login
./scripts/deploy-dcm.sh --kubevirt-service-provider \
  --cluster-api <cluster-url> \
  --cluster-password <password>
```

## Running Tests

### Run All KubeVirt Tests

```bash
make test-kubevirt
```

### Run Specific Test Suites

```bash
# Direct SP API tests only
ginkgo -r --label-filter="sp && kubevirt" tests/e2e/

# Core platform integration tests
ginkgo -r --label-filter="core && platform && kubevirt" tests/e2e/

# All KubeVirt-related tests
ginkgo -r --label-filter="kubevirt" tests/e2e/
```

### Configuration

| Environment Variable | Default | Purpose |
|---------------------|---------|---------|
| `DCM_KUBEVIRT_SP_URL` | `http://localhost:8081/api/v1alpha1` | KubeVirt SP endpoint |
| `DCM_GATEWAY_URL` | `http://localhost:8080/api/v1alpha1` | DCM control plane API |
| `DCM_NATS_URL` | `nats://localhost:4222` | NATS server for events |

### Skip Behavior

Tests automatically skip if:
- KubeVirt SP is not reachable at the configured URL
- Cluster access is not available
- Required StorageClass is missing

## Test Plan

See [test-plans/FLPATH-2897-kubevirt-sp.md](../../test-plans/FLPATH-2897-kubevirt-sp.md) for the complete test plan, including:
- 30 test cases covering registration, health, CRUD, validation, monitoring
- Mapping to upstream unit test coverage
- Environment requirements and success criteria

## Current Status

### Implemented

- [x] Test plan document (FLPATH-2897)
- [x] Helper functions and VM spec builders
- [x] Health endpoint test (TC-04)
- [x] 404 handling test

### TODO

- [ ] Deploy script integration (`--kubevirt-service-provider` flag)
- [ ] Compose override file for KubeVirt SP
- [ ] Provider registry configuration
- [ ] CreateVM test implementation (TC-06)
- [ ] GetVM test implementation (TC-12)
- [ ] ListVMs test implementation (TC-15)
- [ ] DeleteVM test implementation (TC-18)
- [ ] Full catalog-to-VM provisioning flow (TC-23)
- [ ] VM status monitoring tests (TC-21, TC-22)
- [ ] Label verification tests (TC-26, TC-27)
- [ ] Validation tests (TC-09, TC-25, TC-29)

## Next Steps

1. **Deploy Script Integration**
   - Add `providers/kubevirt.conf` provider registry entry
   - Create `compose-kubevirt-sp.yaml` compose override
   - Add validation hook for CNV/KubeVirt prerequisites

2. **Complete Test Implementation**
   - Implement skipped test cases in `sp_kubevirt_api_test.go`
   - Complete catalog-item-instance flow in `core_platform_kubevirt_test.go`
   - Add VM status polling and verification

3. **CI Integration**
   - Add `test-kubevirt` target to Makefile
   - Update `.github/workflows/validate-tests.yaml`
   - Document cluster setup requirements for CI

## Related Documentation

- [KubeVirt SP Repository](https://github.com/dcm-project/kubevirt-service-provider)
- [KubeVirt API Documentation](https://kubevirt.io/api-reference/)
- [Container SP Tests](sp_container_api_test.go) (reference implementation)
- [ACM Cluster SP Tests](sp_acm_cluster_api_test.go) (reference implementation)
