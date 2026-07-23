# KubeVirt Service Provider E2E Testing

This document describes the E2E test suite for the KubeVirt Service Provider (FLPATH-2897).

## Branches

| Branch | Contents |
|--------|----------|
| `kubevirt-provider-tests-titan90` | Default green suite (ready for CI / day-to-day runs) |
| `kubevirt-provider-tests-deferred` | **This branch** — disruptive + intentionally skipped cases that need fixtures, SP fixes, or `DCM_DISRUPTIVE=1` |

Merge or cherry-pick from `kubevirt-provider-tests-deferred` when enabling deferred coverage.

## Overview

The KubeVirt SP test suite validates VM lifecycle management through the DCM platform, including:
- Direct KubeVirt SP API tests (health, CRUD, validation, labels, concurrency)
- Full platform integration tests (catalog → policy → placement → VM provisioning → delete)
- VM status monitoring and NATS event verification
- Disruptive cases (gated by `DCM_DISRUPTIVE=1`)
- Deferred Skip stubs for SP gaps and env fixtures (TC-08, TC-17, TC-28b/d/e, bad storage class)

## Test Files

| File | Purpose | Labels |
|------|---------|--------|
| `sp_kubevirt_helpers_test.go` | Helpers, VM builders, cluster/label utilities | — |
| `sp_kubevirt_api_test.go` | Direct SP API (health, CRUD, validation, concurrency, storage) + deferred Skip stubs | `sp`, `kubevirt` |
| `sp_kubevirt_status_test.go` | NATS status events + GET status | `sp`, `kubevirt`, `nats` |
| `core_platform_kubevirt_test.go` | Catalog → Running → delete lifecycle | `core`, `platform`, `kubevirt` |
| `sp_kubevirt_disruptive_test.go` | Unhealthy health, namespace isolation, registration stubs | `sp`, `kubevirt`, `disruptive` |

## Prerequisites

- OpenShift/K8s with KubeVirt/CNV and at least one StorageClass
- DCM stack + KubeVirt SP (`./scripts/deploy-dcm.sh --kubevirt-service-provider ...`)
- NATS for status tests
- `oc`/`kubectl` access for cluster assertions

## Running Tests

```bash
export KUBECONFIG=/path/to/kubeconfig
export DCM_GATEWAY_URL=http://localhost:8080/api/v1alpha1
export DCM_KUBEVIRT_SP_URL=http://localhost:8081/api/v1alpha1
export DCM_NATS_URL=nats://localhost:4222
export KUBERNETES_NAMESPACE=vms

# Clean leftovers first
./scripts/cleanup-kubevirt-e2e.sh

make test-kubevirt-sp

# Disruptive suite (this branch only)
DCM_DISRUPTIVE=1 make test-kubevirt-sp
```

## Coverage (FLPATH-2897)

| TC | Status | Notes |
|----|--------|-------|
| TC-01 | Implemented | Provider `service_type`, `schema_version`, `endpoint` |
| TC-02 | Skip (disruptive) | Needs SPRM down + SP restart |
| TC-03 | Skip (disruptive) | Needs invalid registration + log assert |
| TC-04 | Implemented | Health healthy |
| TC-05 | Implemented (gated) | `DCM_DISRUPTIVE=1`; poisons SP `/etc/hosts` |
| TC-06 | Implemented | Create + cluster labels |
| TC-07 | Implemented | Custom `?id=` |
| TC-08 | Skip | SP panics without `?id=` |
| TC-09 | Implemented | Empty/missing/invalid memory |
| TC-10 | Implemented | Duplicate id → 409 |
| TC-11 | Implemented | Domain resources present on cluster VM |
| TC-12 | Implemented | GET existing |
| TC-13 | Implemented | GET missing (404 preferred; 500 tolerated) |
| TC-14 | Implemented | GET status phase |
| TC-15 | Implemented | List contains 3 created VMs |
| TC-16 | Implemented | Unlabeled cluster VM excluded |
| TC-17 | Skip (P2) | Malformed VM list skip |
| TC-18 | Implemented | DELETE + cluster gone |
| TC-19 | Implemented | DELETE missing |
| TC-20 | Skip (disruptive) | Needs RBAC revoke |
| TC-21 | Implemented | Transitions + GET status |
| TC-22 | Implemented | CloudEvent schema; filter by VM id |
| TC-23 | Implemented | Catalog → Running + cluster labels |
| TC-24 | Implemented | Delete instance → STI gone + VM gone |
| TC-25 | Implemented | Malformed JSON / content-type |
| TC-26 | Implemented | DCM labels on VM + template |
| TC-27 | Implemented | Instance id label round-trip |
| TC-28 | Partial | 28c on main path; 28a/b/d/e deferred here |
| TC-29 | Implemented | Invalid memory unit |
| TC-30 | Implemented (gated) | Namespace isolation under disruptive label |
| TC-31 | Implemented | 5 parallel creates |
| TC-32 | Implemented | External halt/start via runStrategy |
| TC-33 | Partial | Happy path on green suite; bad SC error path deferred |

## Deferred / disruptive cases (this branch)

| ID | Test | Blocker |
|----|------|---------|
| TC-02 | Registration retry when SPRM down | Disruptive automation |
| TC-03 | Stop retry on 4xx registration | Invalid payload + log assert |
| TC-05 | Health unhealthy | `DCM_DISRUPTIVE=1` |
| TC-08 | Create without `?id=` | SP nil deref panic |
| TC-17 | List skips malformed VMs | Broken VM YAML + log scrape |
| TC-20 | Delete when KubeVirt access fails | Temporary RBAC revoke |
| TC-28a / TC-33 bad SC | Non-existent storage class error | SP ignores `storage_class` |
| TC-28b | Quota exceeded | ResourceQuota fixture |
| TC-28d | Image pull failure | containerDisk not in minimal VMSpec |
| TC-28e | Insufficient capacity | Oversize CPU vs nodes |
| TC-30 | Namespace isolation | `DCM_DISRUPTIVE=1` |

## Notes

- Cluster VMs use `GenerateName: dcm-`; look up by label `dcm.project/dcm-instance-id=<id>`.
- Catalog fields must use `storage.disks` as an array (control-plane nested_map bug with `disks[0]`).
- STI status casing is `Running` (not `RUNNING`).
- Cleanup: `./scripts/cleanup-kubevirt-e2e.sh`
