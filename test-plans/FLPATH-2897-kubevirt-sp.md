# Test Plan: FLPATH-2897 — DCM: kubevirt provider

**Ticket:** [FLPATH-2897](https://redhat.atlassian.net/browse/FLPATH-2897)
**Status:** Closed
**Assignee:** Ondra Machacek
**Parent:** DCM: Service provider api lifecycle
**Repo:** [dcm-project/kubevirt-service-provider](https://github.com/dcm-project/kubevirt-service-provider)
**Dependencies:** FLPATH-2843 (DCM: Service provider api lifecycle), FLPATH-2987 (DCM: SP Resource manager)

## Summary

The KubeVirt SP implements a CRUD API for managing VMs as KubeVirt VirtualMachines. It supports creating, listing, getting, and deleting VMs. The API registers with DCM as `serviceType: vm` with `operations: CREATE, DELETE, READ`. Resources are managed via DCM labels (`dcm.project/managed-by`, `dcm.project/instance-id`).

## Scope

This plan covers **E2E tests against a real KubeVirt cluster** — verifying behavior that unit/integration tests with fake clients cannot cover. For input validation, handler logic, and KubeVirt API edge cases, see the repo's own test coverage.

**E2E focus areas:** Real VirtualMachine lifecycle, label verification on cluster, cross-service registration flow, pagination against real data, VM status propagation (PENDING → RUNNING), and API responses with live VMI status.

### Upstream Test Coverage (in repo)

The repo has comprehensive unit tests in `internal/`:

- **Handler tests:** `internal/handlers/v1alpha1/kubevirt_test.go` — CreateVM, GetVM, ListVMs, DeleteVM (Ginkgo, mocked KubeVirt client)
- **Client tests:** `internal/kubevirt/client_test.go` — KubeVirt client operations
- **Mapper tests:** `internal/kubevirt/mapper_test.go` — VMSpec ↔ VirtualMachine conversion
- **Monitor tests:** `internal/monitor/phase_test.go`, `service_test.go` — VM phase monitoring

Key test coverage in repo (no need to duplicate at E2E):
- OpenAPI request validation
- Handler error mapping and RFC 7807 format
- VMSpec to VirtualMachine conversion
- VM phase mapping (Pending, Scheduling, Running, etc.)
- Label extraction and management
- Graceful error handling

### References

- OpenAPI spec: `api/v1alpha1/openapi.yaml` in repo
- [KubeVirt API documentation](https://kubevirt.io/api-reference/)

## Prerequisites

- OpenShift/Kubernetes cluster with KubeVirt/CNV installed
- NATS server running (for status monitoring)
- DCM stack deployed (`./scripts/deploy-dcm.sh`)
- KubeVirt SP running and registered with DCM
- Cluster has available storage classes for VM disks

## API Endpoints Under Test

| Method | Path | Operation |
|--------|------|-----------|
| POST | `/api/v1alpha1/vms` | Create a VM |
| GET | `/api/v1alpha1/vms` | List VMs |
| GET | `/api/v1alpha1/vms/{vm_id}` | Get a VM |
| DELETE | `/api/v1alpha1/vms/{vm_id}` | Delete a VM |
| GET | `/api/v1alpha1/health` | Health check |

## Test Cases

### Registration

#### TC-01: SP registers with DCM on startup

| Step | Action | Expected |
|------|--------|----------|
| 1 | Start SP with `DCM_REGISTRATION_URL` pointing at SPRM | SP starts, health passes |
| 2 | Query DCM provider list | Provider with `serviceType: vm` exists |
| 3 | Check registered operations | `CREATE, DELETE, READ` |
| 4 | Check registered endpoint | Contains `/api/v1alpha1/vms` |
| 5 | Check `schemaVersion` | `v1alpha1` |

#### TC-02: Registration retries on failure

| Step | Action | Expected |
|------|--------|----------|
| 1 | Start SP with SPRM unavailable | SP starts, health passes, registration retries |
| 2 | Start SPRM | SP eventually registers successfully (exponential backoff) |

#### TC-03: Registration stops retrying on 4xx

| Step | Action | Expected |
|------|--------|----------|
| 1 | SP sends registration with invalid payload | SPRM returns 4xx |
| 2 | Check SP logs | Non-retryable error logged, no infinite retry loop |

### Health Endpoint

#### TC-04: Health returns healthy when cluster is reachable

| Step | Action | Expected |
|------|--------|----------|
| 1 | `curl http://<sp>:8081/api/v1alpha1/health` | HTTP 200 |
| 2 | Parse response | `status: "healthy"`, `path: "/api/v1alpha1/health"` |

#### TC-05: Health returns unhealthy when cluster is unreachable

| Step | Action | Expected |
|------|--------|----------|
| 1 | Stop cluster API access or revoke kubeconfig | — |
| 2 | `curl http://<sp>:8081/api/v1alpha1/health` | HTTP 200 |
| 3 | Parse response | `status: "unhealthy"` |

### Create VM

#### TC-06: Create a VM with valid spec

| Step | Action | Expected |
|------|--------|----------|
| 1 | POST `/api/v1alpha1/vms` with valid VM spec (vcpu, memory, storage, guest_os) | HTTP 201 |
| 2 | Check response body | Contains `id`, VM details, `path` |
| 3 | Verify on KubeVirt cluster | VirtualMachine created with DCM labels |
| 4 | Check labels on VirtualMachine | `dcm.project/managed-by=dcm`, `dcm.project/instance-id=<id>` |
| 5 | Check VM template labels | Same DCM labels applied to spec.template |

#### TC-07: Create with custom ID (via query parameter)

| Step | Action | Expected |
|------|--------|----------|
| 1 | POST `/api/v1alpha1/vms?id=my-vm-uuid` | HTTP 201 |
| 2 | Check response `id` in path | Contains `my-vm-uuid` |
| 3 | GET `/api/v1alpha1/vms/my-vm-uuid` | HTTP 200 |

#### TC-08: Create with auto-generated ID (no query parameter)

| Step | Action | Expected |
|------|--------|----------|
| 1 | POST `/api/v1alpha1/vms` (no `id` query) | HTTP 201 |
| 2 | Check response path | Contains valid UUID |

#### TC-09: Create returns error for invalid spec

| Step | Action | Expected |
|------|--------|----------|
| 1 | POST with empty body | HTTP 400 |
| 2 | POST with missing required fields (e.g., no vcpu) | HTTP 400 with descriptive error |
| 3 | POST with invalid memory format | HTTP 400 (validation error) |
| 4 | POST with invalid storage capacity | HTTP 400 |

#### TC-10: Create returns 409 when VM with same instance ID exists

| Step | Action | Expected |
|------|--------|----------|
| 1 | Create VM with `id=test-duplicate` | HTTP 201 |
| 2 | POST again with same `id=test-duplicate` | HTTP 409 |
| 3 | Check response | Detail mentions the duplicate ID |

#### TC-11: Create provisions VirtualMachine with correct spec mapping

| Step | Action | Expected |
|------|--------|----------|
| 1 | POST with specific vcpu count, memory size, disk capacity | HTTP 201 |
| 2 | Get VirtualMachine from cluster | Resources match request spec |
| 3 | Check VM running config | vcpu, memory, disks correctly mapped |

### Get VM

#### TC-12: Get an existing VM

| Step | Action | Expected |
|------|--------|----------|
| 1 | Create a VM, note `id` | — |
| 2 | GET `/api/v1alpha1/vms/{id}` | HTTP 200 |
| 3 | Response includes VM spec | vcpu, memory, storage, metadata present |
| 4 | Response includes path | `/api/v1alpha1/vms/{id}` |

#### TC-13: Get a non-existent VM

| Step | Action | Expected |
|------|--------|----------|
| 1 | GET `/api/v1alpha1/vms/does-not-exist` | HTTP 404 |
| 2 | Check response format | RFC 7807 problem details |

#### TC-14: Get returns VM with current status

| Step | Action | Expected |
|------|--------|----------|
| 1 | Create VM and wait for it to start | — |
| 2 | GET the VM | Status reflects actual VM phase (Running, Pending, etc.) |

### List VMs

#### TC-15: List returns all managed VMs

| Step | Action | Expected |
|------|--------|----------|
| 1 | Create 3 VMs | — |
| 2 | GET `/api/v1alpha1/vms` | HTTP 200 |
| 3 | Check results | All 3 VMs present in `vms` array |

#### TC-16: List only returns DCM-managed VMs

| Step | Action | Expected |
|------|--------|----------|
| 1 | Create a VirtualMachine manually (no DCM labels) in the same namespace | — |
| 2 | GET `/api/v1alpha1/vms` | Manual VM NOT in results |
| 3 | Verify label selector | Only VMs with `dcm.project/managed-by=dcm` returned |

#### TC-17: List skips VMs that fail conversion

**Priority:** P2 (edge case)

| Step | Action | Expected |
|------|--------|----------|
| 1a | Create VM with DCM labels but missing `.spec.template` | — |
| 1b | Create VM with unparseable memory (`memory: "invalid"`) | — |
| 1c | Create VM with `dcm.project/instance-id` but no `dcm.project/managed-by` | — |
| 2 | GET `/api/v1alpha1/vms` | HTTP 200, malformed VMs skipped (not in list) |
| 3 | Check logs | Warning logged about each conversion failure |

**Concrete malformed scenarios:**
- Missing `.spec.template` (structural)
- Invalid memory format (validation)
- Incomplete DCM labels (label mismatch)

### Delete VM

#### TC-18: Delete an existing VM

| Step | Action | Expected |
|------|--------|----------|
| 1 | Create a VM, note `id` | — |
| 2 | DELETE `/api/v1alpha1/vms/{id}` | HTTP 204 |
| 3 | Verify on KubeVirt cluster | VirtualMachine removed |
| 4 | GET the same `id` | HTTP 404 |

#### TC-19: Delete a non-existent VM

| Step | Action | Expected |
|------|--------|----------|
| 1 | DELETE `/api/v1alpha1/vms/does-not-exist` | HTTP 404 |
| 2 | Check response format | RFC 7807 problem details |

#### TC-20: Delete handles KubeVirt errors gracefully

| Step | Action | Expected |
|------|--------|----------|
| 1 | Revoke cluster permissions temporarily | — |
| 2 | DELETE a VM | HTTP 500 or appropriate error |
| 3 | Check response | Error detail describes the issue |

### VM Status Monitoring

#### TC-21: VM status transitions are monitored

| Step | Action | Expected |
|------|--------|----------|
| 1 | Create a VM | Initial status may be PENDING or PROVISIONING |
| 2 | Wait for VM to start | — |
| 3 | GET the VM | Status eventually becomes RUNNING |
| 4 | Check status field | Reflects VirtualMachine phase |

#### TC-22: Status events published to NATS

**Priority:** P0 (critical for NATS integration validation)

| Step | Action | Expected |
|------|--------|----------|
| 1 | Subscribe to NATS subject for VM status | — |
| 2 | Create a VM | — |
| 3 | Monitor NATS messages | Status events published as VM transitions states |
| 4 | Validate CloudEvent schema | Contains all required fields (see below) |
| 5 | Verify event ordering | PENDING before RUNNING |
| 6 | Count events | 2-4 events expected for typical VM creation |

**CloudEvent Schema Requirements:**
- `specversion` (string): CloudEvents version
- `type` (string): Event type identifier
- `source` (string): Event source
- `data.instance_id` (string): Matches VM instance ID
- `data.status` (string enum): PENDING, PROVISIONING, RUNNING, etc.
- `data.timestamp` (ISO 8601): Event timestamp
- `data.service_type` (string): Equals "vm"

**Delivery Semantics:** At-least-once (events may duplicate, ordering preserved per instance)

**Typical Event Flow:** VM creation generates 2-4 events:
1. PENDING (VM resource created)
2. PROVISIONING (VMI being scheduled)
3. RUNNING (VMI started, guest OS booting)
4. RUNNING (periodic heartbeat - optional)

### E2E via DCM Gateway

#### TC-23: Create VM through full DCM flow

**Preconditions:** Full DCM stack + KubeVirt SP deployed and registered.

| Step | Action | Expected |
|------|--------|----------|
| 1 | Create a CatalogItem for vm type | — |
| 2 | Create a routing policy targeting KubeVirt provider | — |
| 3 | Create a CatalogItemInstance | Placement Manager routes to KubeVirt SP |
| 4 | Verify VM creation | VirtualMachine created on KubeVirt cluster |
| 5 | Check status propagation via NATS | CloudEvent published with VM status |
| 6 | Wait for RUNNING status | ServiceTypeInstance reaches RUNNING |

#### TC-24: VM lifecycle through DCM catalog

| Step | Action | Expected |
|------|--------|----------|
| 1 | Create catalog-item-instance for VM | Instance created |
| 2 | Verify service-type-instance shows RUNNING | — |
| 3 | DELETE catalog-item-instance | Triggers VM deletion |
| 4 | Verify VirtualMachine deleted from cluster | VM removed |
| 5 | Verify service-type-instance deleted | Returns 404 |

### OpenAPI Validation

#### TC-25: Requests are validated against OpenAPI spec

| Step | Action | Expected |
|------|--------|----------|
| 1 | POST with unknown fields | Handled per spec (rejected or ignored) |
| 2 | POST with wrong content type | HTTP 400/415 |
| 3 | POST with malformed JSON | HTTP 400 |

### Label Management

#### TC-26: DCM labels are applied correctly

| Step | Action | Expected |
|------|--------|----------|
| 1 | Create VM with specific instance ID | — |
| 2 | Get VirtualMachine from cluster | Labels include `dcm.project/instance-id=<id>` |
| 3 | Check VirtualMachine metadata | `dcm.project/managed-by=dcm` present |
| 4 | Check VirtualMachine template | Template also has DCM labels |

#### TC-27: Instance ID extracted from labels

| Step | Action | Expected |
|------|--------|----------|
| 1 | Create VM via DCM | — |
| 2 | List VMs via API | Each VM has correct ID from label |
| 3 | Manually check cluster | Label value matches API response ID |

### Error Handling

#### TC-28: Client handles KubeVirt API errors

**Priority:** P2 (improves error handling coverage)

| Scenario | Action | Expected |
|----------|--------|----------|
| 28a: Storage class | POST VM with non-existent storage class | HTTP 400/500, error mentions storage class |
| 28b: Resource quota | POST VM when namespace quota exceeded (CPU/memory) | HTTP 400/500, error mentions quota/resources |
| 28c: Invalid resources | POST VM with invalid CPU/memory combination rejected by KubeVirt admission | HTTP 400, error from KubeVirt admission webhook |
| 28d: Image pull failure | POST VM using containerDisk with unreachable image | VM created but status shows ImagePullBackOff |
| 28e: Insufficient capacity | POST VM when cluster lacks capacity for scheduling | VM created but status shows scheduling failure |

**Specific Error Scenarios:**
1. **Unavailable storage class:** `storageClassName: "does-not-exist"`
2. **Quota exceeded:** Namespace with `ResourceQuota` limiting memory
3. **Invalid admission:** Memory < 64Mi (below KubeVirt minimum)
4. **Image pull:** `image: "quay.io/invalid/missing:latest"`
5. **Capacity:** Request more CPU than available nodes

#### TC-29: Mapper conversion errors are handled

| Step | Action | Expected |
|------|--------|----------|
| 1 | POST with memory value that cannot be parsed | HTTP 400 |
| 2 | Check error detail | Describes the validation failure |

### Namespace Isolation

#### TC-30: VMs are created in configured namespace

| Step | Action | Expected |
|------|--------|----------|
| 1 | Configure SP with `SP_K8S_NAMESPACE=dcm-vms` | — |
| 2 | Create VM via API | — |
| 3 | Check cluster | VirtualMachine in `dcm-vms` namespace |
| 4 | VMs in other namespaces | Not returned by List operation |

### Concurrency & Performance

#### TC-31: Concurrent VM creation

**Priority:** P1 (common production scenario)

| Step | Action | Expected |
|------|--------|----------|
| 1 | Create 5 VMs in parallel with different instance IDs | — |
| 2 | Check all responses | All return HTTP 201 |
| 3 | Verify on cluster | All 5 VMs created with correct DCM labels |
| 4 | Check for label conflicts | No duplicate instance-id labels |

**Rationale:** KubeVirt admission controllers and DCM label uniqueness need validation under concurrent load.

### VM Lifecycle State Changes

#### TC-32: VM state transitions beyond create/delete

**Priority:** P1 (validates monitoring resilience)

| Step | Action | Expected |
|------|--------|----------|
| 1 | Create VM, wait for RUNNING status | — |
| 2 | Stop VM via KubeVirt API (not DCM) | VM enters Stopped phase |
| 3 | Verify NATS event published | Status event reflects "Stopped" |
| 4 | Start VM via KubeVirt API | VM returns to RUNNING |
| 5 | Verify DCM API reflects state | GET /vms/{id} shows RUNNING |

**Rationale:** Real VMs get stopped, restarted, migrated. Need to verify monitoring tracks external state changes.

### Storage Class Handling

#### TC-33: Storage class selection and error handling

**Priority:** P2 (common production configuration)

| Step | Action | Expected |
|------|--------|----------|
| 1 | Create VM when multiple storage classes exist | VM uses default or specified class |
| 2 | Create VM requesting non-existent storage class | HTTP 400 or 500 with descriptive error |
| 3 | Verify PVC provisioned with correct storage class | `kubectl get pvc` shows expected class |

**Rationale:** Validates SP behavior with multiple/missing storage classes, not just "at least one exists."

## Upstream Test Coverage (in repo)

**Run:** `make test` in the repo.

Key test files:
- `internal/handlers/v1alpha1/kubevirt_test.go` — CRUD handler tests (CreateVM, GetVM, ListVMs, DeleteVM)
- `internal/kubevirt/client_test.go` — KubeVirt client operations
- `internal/kubevirt/mapper_test.go` — VMSpec to VirtualMachine mapping
- `internal/kubevirt/errors_test.go` — Error handling
- `internal/monitor/phase_test.go` — VM phase monitoring
- `internal/monitor/service_test.go` — Monitor service tests
- `internal/registration/registration_test.go` — SPRM registration

### E2E vs Unit/Integration Boundary

| Concern | Tested by repo (mocked client) | E2E adds value |
|---------|-------------------------------|----------------|
| Input validation / 400 errors | Yes | Minimal — confirms middleware wiring |
| VirtualMachine creation | Yes (mocked client) | **Yes** — real scheduling, storage, VMI lifecycle |
| VMSpec to VM conversion | Yes | **Yes** — validates against actual KubeVirt API |
| DCM label verification | Yes | **Yes** — labels survive real KubeVirt admission |
| VM status monitoring | Yes (mocked phase) | **Yes** — real VMI phase transitions |
| Registration with live SPRM | No | **Yes** — full compose network flow |
| Storage class integration | No | **Yes** — real PVC provisioning |

## Key Configuration

| Variable | Default | Purpose |
|----------|---------|---------|
| `DCM_REGISTRATION_URL` | *(required)* | URL of DCM service-provider-manager |
| `SP_ENDPOINT` | *(required)* | This SP's externally-reachable URL |
| `SP_NAME` | *(required)* | Provider name for registration |
| `SP_K8S_NAMESPACE` | *(required)* | Namespace for VirtualMachines |
| `SP_K8S_KUBECONFIG` | *(optional)* | Path to kubeconfig (uses in-cluster if unset) |
| `SP_NATS_URL` | `nats://localhost:4222` | NATS server for status events |

## Test Environment Requirements

- **OpenShift 4.x** or **Kubernetes 1.25+** with KubeVirt/CNV
- **Storage:** At least one StorageClass available for PVCs
- **Network:** Service/Pod networking functional
- **Resources:** Sufficient CPU/memory for test VMs (minimal specs acceptable)
- **Permissions:** Service account with VirtualMachine CRUD permissions

### Automated Prerequisite Checks

Tests automatically verify prerequisites and skip gracefully if not met:

| Check | Function | Tests Affected | Skip Message |
|-------|----------|----------------|--------------|
| **Cluster Access** | `checkClusterAccess()` | All cluster-dependent tests | "kubectl/oc cluster access required" |
| **StorageClass Availability** | `checkStorageClass()` | VM creation tests (TC-06, TC-23) | "At least one StorageClass required" |
| **KubeVirt SP Reachable** | `requireKubevirtSP()` | All KubeVirt SP tests | "KubeVirt SP not available" |
| **NATS Reachable** | `requireNATS()` | Status monitoring tests (TC-21, TC-22) | "NATS server not available" |

**Storage Class Check Details:**
- Verifies at least one StorageClass exists in cluster
- Uses `kubectl get storageclass -o json` or `oc get storageclass -o json`
- Logs count of available storage classes
- Helper function `getDefaultStorageClass()` identifies default class
- Tests skip with clear message if no storage classes found

**Benefits:**
- Tests don't fail due to missing prerequisites
- Clear skip messages guide environment setup
- Supports both OpenShift (`oc`) and Kubernetes (`kubectl`)
- Prerequisite validation happens in `BeforeEach` or `BeforeAll` hooks

## Implementation branches (utilities)

| Branch | Role |
|--------|------|
| `kubevirt-provider-tests-titan90` | Green E2E suite used for day-to-day / CI-style runs |
| `kubevirt-provider-tests-deferred` | Disruptive tests (`DCM_DISRUPTIVE=1`) and intentionally skipped cases (TC-08, TC-17, TC-28a/b/d/e, TC-02/03/20, etc.) |

See `tests/e2e/KUBEVIRT_TESTING.md` on each branch for the coverage table. Promote deferred cases by implementing them on `kubevirt-provider-tests-deferred`, then merging into the green branch when they pass without Skip.

## Success Criteria

- Green-suite TCs pass against a live KubeVirt cluster (`kubevirt-provider-tests-titan90`)
- Deferred / disruptive TCs tracked on `kubevirt-provider-tests-deferred` until SP or fixture gaps are closed
- VM creation results in actual VirtualMachine on cluster
- VM lifecycle (create → running → delete) completes successfully
- Status monitoring reflects real VMI phase changes
- DCM end-to-end flow provisions VMs through catalog/policy/placement
- Concurrent VM creation succeeds without label conflicts
- NATS events conform to CloudEvent schema and ordering requirements
- Storage class selection and error handling work correctly
- No regression in existing container SP or ACM cluster SP tests

## Test Case Summary

| Category | Test Cases | Priority |
|----------|------------|----------|
| Registration | TC-01 to TC-03 | P1 |
| Health Endpoint | TC-04 to TC-05 | P1 |
| Create VM | TC-06 to TC-11 | P0 |
| Get VM | TC-12 to TC-14 | P0 |
| List VMs | TC-15 to TC-17 | P1 |
| Delete VM | TC-18 to TC-20 | P0 |
| Status Monitoring | TC-21 to TC-22 | P0 |
| E2E via Gateway | TC-23 to TC-24 | P1 |
| OpenAPI Validation | TC-25 | P1 |
| Label Management | TC-26 to TC-27 | P1 |
| Error Handling | TC-28 to TC-29 | P2 |
| Namespace Isolation | TC-30 | P1 |
| Concurrency | TC-31 | P1 |
| VM Lifecycle States | TC-32 | P1 |
| Storage Class | TC-33 | P2 |
| **Total** | **33 test cases** | — |
