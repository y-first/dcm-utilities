#!/usr/bin/env bash
# cleanup-kubevirt-e2e.sh — remove leftovers from kubevirt / DCM E2E runs
#
# Cleans:
#   - catalog-item-instances / catalog-items matching e2e-/probe- prefixes
#   - GLOBAL policies with e2e- display names
#   - service-type-instances for e2e-/dcm- VMs
#   - VMs via KubeVirt SP API
#   - VirtualMachines / VMIs / PVCs in the kubevirt VM namespace
#   - stale messages on the NATS dcm-status stream (avoids TC-22 ID mismatches)
#
# Usage:
#   ./scripts/cleanup-kubevirt-e2e.sh
#   DCM_GATEWAY_URL=http://localhost:8080/api/v1alpha1 \
#   DCM_KUBEVIRT_SP_URL=http://localhost:8081/api/v1alpha1 \
#   KUBECONFIG=/path/to/kubeconfig \
#   KUBERNETES_NAMESPACE=vms \
#   ./scripts/cleanup-kubevirt-e2e.sh
#
# Env (defaults match a local/titan90 DCM compose deploy):
#   DCM_GATEWAY_URL       default http://localhost:8080/api/v1alpha1
#   DCM_KUBEVIRT_SP_URL   default http://localhost:8081/api/v1alpha1
#   DCM_NATS_URL          default nats://localhost:4222 (informational)
#   KUBECONFIG            required for cluster VM/PVC cleanup (oc/kubectl)
#   KUBERNETES_NAMESPACE  VM namespace (default: vms)
#   NATS_CONTAINER        podman/docker name for NATS (default: dcm-e2e_nats_1)
#   NATS_NETWORK          compose network (default: dcm-e2e_default)
#   NATS_BOX_IMAGE        default docker.io/natsio/nats-box:0.14.3
#   DRY_RUN=1             print actions without deleting
#   KEEP_PET_CLINIC=1     keep non-e2e catalog items (default: keep pet-clinic)

set -euo pipefail

GW="${DCM_GATEWAY_URL:-http://localhost:8080/api/v1alpha1}"
SP="${DCM_KUBEVIRT_SP_URL:-http://localhost:8081/api/v1alpha1}"
NS="${KUBERNETES_NAMESPACE:-${KUBEVIRT_NAMESPACE:-vms}}"
NATS_CONTAINER="${NATS_CONTAINER:-dcm-e2e_nats_1}"
NATS_NETWORK="${NATS_NETWORK:-dcm-e2e_default}"
NATS_BOX_IMAGE="${NATS_BOX_IMAGE:-docker.io/natsio/nats-box:0.14.3}"
DRY_RUN="${DRY_RUN:-0}"
KEEP_PET_CLINIC="${KEEP_PET_CLINIC:-1}"

OC_BIN=""
if command -v oc >/dev/null 2>&1; then
  OC_BIN=oc
elif command -v kubectl >/dev/null 2>&1; then
  OC_BIN=kubectl
fi

log()  { printf '==> %s\n' "$*"; }
info() { printf '    %s\n' "$*"; }

log "Gateway: $GW"
log "KubeVirt SP: $SP"
log "Namespace: $NS"
[[ "$DRY_RUN" == "1" ]] && log "DRY_RUN=1 (no deletes)"

# --- health (best-effort) ---
gw_code=$(curl -sS -o /dev/null -w '%{http_code}' --connect-timeout 3 "$GW/health" 2>/dev/null || echo 000)
sp_code=$(curl -sS -o /dev/null -w '%{http_code}' --connect-timeout 3 "$SP/vms/health" 2>/dev/null || echo 000)
info "health gw=$gw_code sp=$sp_code"

# --- catalog-item-instances ---
log "Cleaning catalog-item-instances"
if curl -sS -f "$GW/catalog-item-instances" -o /tmp/dcm-cleanup-cii.json 2>/dev/null; then
  python3 - "$GW" "$DRY_RUN" <<'PY'
import json, os, sys, urllib.request
gw, dry = sys.argv[1], sys.argv[2] == "1"
d = json.load(open("/tmp/dcm-cleanup-cii.json"))
items = d.get("results") or d.get("catalog_item_instances") or d.get("items") or []
if isinstance(d, list):
    items = d
print(f"    cii count {len(items)}")
for it in items:
    if not isinstance(it, dict):
        continue
    uid = it.get("uid") or it.get("id")
    name = str(it.get("display_name") or "")
    if not (name.startswith("e2e-") or name.startswith("probe") or "kubevirt" in name.lower()):
        print(f"    skip cii {name}")
        continue
    url = f"{gw}/catalog-item-instances/{uid}"
    if dry:
        print(f"    DRY_RUN DELETE {url}")
        continue
    req = urllib.request.Request(url, method="DELETE")
    try:
        with urllib.request.urlopen(req) as r:
            print(f"    deleted cii {name} -> {r.status}")
    except Exception as e:
        print(f"    cii del fail {name}: {e}")
PY
else
  info "could not list catalog-item-instances (gateway down?)"
fi

# --- catalog-items ---
log "Cleaning catalog-items"
if curl -sS -f "$GW/catalog-items" -o /tmp/dcm-cleanup-ci.json 2>/dev/null; then
  python3 - "$GW" "$DRY_RUN" "$KEEP_PET_CLINIC" <<'PY'
import json, sys, urllib.request
gw, dry, keep_pet = sys.argv[1], sys.argv[2] == "1", sys.argv[3] == "1"
d = json.load(open("/tmp/dcm-cleanup-ci.json"))
items = d.get("results") or d.get("catalog_items") or d.get("items") or []
if isinstance(d, list):
    items = d
print(f"    ci count {len(items)}")
for it in items:
    if not isinstance(it, dict):
        continue
    uid = it.get("uid") or it.get("id")
    name = str(it.get("display_name") or "")
    # keep shared fixtures unless forced
    if keep_pet and name.lower() in ("pet clinic", "pet-clinic"):
        print(f"    keep ci {name}")
        continue
    if not (name.startswith("e2e-") or name.startswith("probe")):
        print(f"    skip ci {name}")
        continue
    url = f"{gw}/catalog-items/{uid}"
    if dry:
        print(f"    DRY_RUN DELETE {url}")
        continue
    req = urllib.request.Request(url, method="DELETE")
    try:
        with urllib.request.urlopen(req) as r:
            print(f"    deleted ci {name} -> {r.status}")
    except Exception as e:
        print(f"    ci del fail {name}: {e}")
PY
else
  info "could not list catalog-items"
fi

# --- policies ---
log "Cleaning e2e policies"
if curl -sS -f "$GW/policies" -o /tmp/dcm-cleanup-pol.json 2>/dev/null; then
  python3 - "$GW" "$DRY_RUN" <<'PY'
import json, sys, urllib.request
gw, dry = sys.argv[1], sys.argv[2] == "1"
d = json.load(open("/tmp/dcm-cleanup-pol.json"))
pols = d.get("policies") or d.get("results") or []
print(f"    policy count {len(pols)}")
for p in pols:
    name = str(p.get("display_name") or "")
    pid = p.get("id") or p.get("uid")
    if not name.startswith("e2e-"):
        print(f"    skip pol {name}")
        continue
    url = f"{gw}/policies/{pid}"
    if dry:
        print(f"    DRY_RUN DELETE {url}")
        continue
    req = urllib.request.Request(url, method="DELETE")
    try:
        with urllib.request.urlopen(req) as r:
            print(f"    deleted pol {name} -> {r.status}")
    except Exception as e:
        print(f"    pol del fail {name}: {e}")
PY
else
  info "could not list policies"
fi

# --- service-type-instances ---
log "Cleaning service-type-instances"
if curl -sS -f "$GW/service-type-instances" -o /tmp/dcm-cleanup-sti.json 2>/dev/null; then
  python3 - "$GW" "$DRY_RUN" <<'PY'
import json, sys, urllib.request
gw, dry = sys.argv[1], sys.argv[2] == "1"
d = json.load(open("/tmp/dcm-cleanup-sti.json"))
items = d.get("results") or d.get("service_type_instances") or d.get("items") or []
if isinstance(d, list):
    items = d
print(f"    sti count {len(items)}")
for i in items:
    if not isinstance(i, dict):
        continue
    iid = i.get("id") or i.get("uid")
    name = str(((i.get("spec") or {}).get("metadata") or {}).get("name") or "")
    status = i.get("status")
    if not (name.startswith("e2e-") or name.startswith("dcm-")):
        print(f"    skip sti {iid} {status} {name}")
        continue
    url = f"{gw}/service-type-instances/{iid}"
    if dry:
        print(f"    DRY_RUN DELETE {url}")
        continue
    req = urllib.request.Request(url, method="DELETE")
    try:
        with urllib.request.urlopen(req) as r:
            print(f"    deleted sti {name or iid} ({status}) -> {r.status}")
    except Exception as e:
        print(f"    sti del fail {name or iid}: {e}")
PY
else
  info "could not list service-type-instances"
fi

# --- VMs via SP ---
log "Cleaning VMs via KubeVirt SP"
if curl -sS -f "$SP/vms" -o /tmp/dcm-cleanup-vms.json 2>/dev/null; then
  python3 - "$SP" "$DRY_RUN" <<'PY'
import json, sys, urllib.request
sp, dry = sys.argv[1], sys.argv[2] == "1"
d = json.load(open("/tmp/dcm-cleanup-vms.json"))
vms = d.get("vms") or d.get("results") or []
print(f"    sp vm count {len(vms)}")
for v in vms:
    if not isinstance(v, dict):
        continue
    vid = v.get("id") or v.get("uid") or (v.get("metadata") or {}).get("uid")
    name = (v.get("metadata") or {}).get("name") or v.get("name") or ""
    if not vid:
        continue
    url = f"{sp}/vms/{vid}"
    if dry:
        print(f"    DRY_RUN DELETE {url} ({name})")
        continue
    req = urllib.request.Request(url, method="DELETE")
    try:
        with urllib.request.urlopen(req) as r:
            print(f"    deleted vm {name or vid} -> {r.status}")
    except Exception as e:
        print(f"    vm del fail {name or vid}: {e}")
PY
else
  info "could not list SP VMs"
fi

# --- cluster resources ---
log "Cleaning cluster VMs/VMIs/PVCs in namespace $NS"
if [[ -n "$OC_BIN" && -n "${KUBECONFIG:-}" ]]; then
  if [[ "$DRY_RUN" == "1" ]]; then
    info "DRY_RUN: would delete vm/vmi/pvc --all -n $NS"
    "$OC_BIN" get vm,vmi,pvc -n "$NS" 2>/dev/null || info "(none or ns missing)"
  else
    "$OC_BIN" delete vm --all -n "$NS" --wait=false 2>/dev/null || true
    "$OC_BIN" delete vmi --all -n "$NS" --wait=false 2>/dev/null || true
    "$OC_BIN" delete pvc --all -n "$NS" --wait=false 2>/dev/null || true
    info "remaining:"
    "$OC_BIN" get vm,vmi,pvc -n "$NS" 2>/dev/null || info "(clean or ns missing)"
  fi
else
  info "skip cluster cleanup (need oc/kubectl + KUBECONFIG)"
fi

# --- NATS purge (stale dcm.* events break TC-22) ---
log "Purging NATS dcm-status stream messages"
RUNTIME=""
if command -v podman >/dev/null 2>&1; then
  RUNTIME=podman
elif command -v docker >/dev/null 2>&1; then
  RUNTIME=docker
fi

if [[ -n "$RUNTIME" ]]; then
  if [[ "$DRY_RUN" == "1" ]]; then
    info "DRY_RUN: would purge stream dcm-status via $NATS_BOX_IMAGE on $NATS_NETWORK"
  else
    if "$RUNTIME" inspect "$NATS_CONTAINER" >/dev/null 2>&1; then
      "$RUNTIME" run --rm --network "$NATS_NETWORK" "$NATS_BOX_IMAGE" \
        nats --server "nats://${NATS_CONTAINER}:4222" stream purge dcm-status -f 2>/dev/null \
        && info "purged dcm-status" \
        || info "purge skipped (stream missing or nats-box failed)"
      "$RUNTIME" run --rm --network "$NATS_NETWORK" "$NATS_BOX_IMAGE" \
        nats --server "nats://${NATS_CONTAINER}:4222" stream info dcm-status 2>/dev/null \
        | grep -E 'Messages:|Subjects:' || true
    else
      info "NATS container $NATS_CONTAINER not found; skip purge"
    fi
  fi
else
  info "no podman/docker; skip NATS purge"
fi

log "Cleanup complete"
info "Re-run tests with:"
info "  export KUBECONFIG=... DCM_GATEWAY_URL=$GW DCM_KUBEVIRT_SP_URL=$SP DCM_NATS_URL=\${DCM_NATS_URL:-nats://localhost:4222}"
info "  make test-kubevirt-sp"
