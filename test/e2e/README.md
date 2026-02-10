# E2E Tests

End-to-end tests for the virt-platform-operator running on a real Kubernetes cluster (kind).

## What E2E Tests Cover

E2E tests validate the **full controller/manager/watch infrastructure** that integration tests cannot cover:

### ✅ Tested by E2E
- **Manager startup** with cache configuration
- **Dynamic watch setup** in SetupWithManager
- **Cache filtering** (DefaultLabelSelector + ByObject exemptions)
- **Real-time drift detection** via watches triggering reconciliation
- **CRD event handlers** (create/delete/update)
- **Unlabeled HCO adoption** via cache exemption
- **Full controller reconciliation loop**
- **Event recording** during actual reconciliation
- **Operator deployment and lifecycle**

### ❌ NOT Tested by E2E (covered by integration tests)
- Applier logic (SSA, drift detection algorithms)
- Patcher logic (Patched Baseline algorithm)
- Template rendering
- Override system (patch, ignore-fields, unmanaged)
- Low-level unit logic

## Integration Tests vs E2E Tests

| Aspect | Integration Tests | E2E Tests |
|--------|------------------|-----------|
| **Environment** | envtest (API server only) | kind (full K8s cluster) |
| **Manager** | ❌ Not running | ✅ Running |
| **Controller** | ❌ Not running | ✅ Running |
| **Watches** | ❌ Not configured | ✅ Configured & active |
| **Cache** | ❌ Not tested | ✅ Tested |
| **Deployment** | ❌ No pods | ✅ Real pods |
| **Focus** | Applier/Patcher logic | Controller/Manager infrastructure |
| **Speed** | Fast (~20s) | Slower (~2-5min) |
| **CI** | Always run | Run on kind |

## Running E2E Tests

### Locally
```bash
# Run all E2E tests (builds image once, then runs tests)
make test-e2e

# Or manually (requires image to be built first)
make docker-build               # Build image first
./hack/run-e2e.sh              # Then run E2E tests

# Keep cluster for debugging
CLEANUP=false make test-e2e
```

### In CI
E2E tests run automatically in `.github/workflows/test-e2e.yml` on:
- Every push
- Every pull request

### Prerequisites
- Docker or Podman (auto-detected)
- kind
- kubectl
- Go 1.23+

**Note:** The build system automatically detects and uses either `docker` or `podman`.
You can override the container tool: `CONTAINER_TOOL=podman make test-e2e`

## Test Scenarios

### 1. Operator Deployment
- Verifies operator pod starts successfully
- Checks deployment is ready
- Validates pod is in Running state

### 2. Unlabeled HCO Adoption
**Tests cache exemption for HCO**
- Creates HCO without `platform.kubevirt.io/managed-by` label
- Verifies operator can see it (cache exemption working)
- Validates operator labels it during reconciliation
- Confirms reconciliation happens (status updated)

**Why this matters:** Proves ByObject cache exemption works in real cluster

### 3. Real-Time Drift Detection
**Tests watches trigger reconciliation**
- Creates managed resource (e.g., MachineConfig)
- Modifies resource to create drift
- Verifies operator detects and corrects drift within seconds
- Confirms watch triggered reconciliation (not periodic sync)

**Why this matters:** Proves dynamic watch system enables real-time drift detection

### 4. Dynamic Watch Configuration
**Tests SetupWithManager**
- Verifies operator only watches installed CRDs
- Checks logs for "Adding watch for managed resource type"
- Validates operator doesn't crash when CRDs are missing

**Why this matters:** Proves operator adapts to available CRDs dynamically

### 5. Cache Optimization
**Tests label-based filtering**
- Verifies unlabeled resources aren't cached
- Confirms labeled resources are cached
- Validates HCO/CRD exemptions work

**Why this matters:** Proves memory optimization works without breaking functionality

### 6. Event Recording
**Tests events during reconciliation**
- Verifies operator emits events
- Checks event source is correct
- Validates events contain meaningful information

**Why this matters:** Proves observability works in real cluster

## Future E2E Test Scenarios

### CRD Lifecycle (TODO)
- Install new CRD → verify operator restarts → watch configured
- Delete watched CRD → verify operator restarts → watch removed
- Update CRD → verify cache invalidation → reconciliation triggered

### Multi-Resource Drift (TODO)
- Create multiple managed resources
- Modify all simultaneously
- Verify all trigger independent reconciliations
- Confirm no thundering herd

### Performance (TODO)
- Measure reconciliation latency
- Verify drift detection happens within seconds
- Check memory usage with label filtering
- Validate watch overhead is acceptable

## Debugging E2E Tests

### View operator logs
```bash
kubectl logs -n openshift-cnv -l app=virt-platform-operator --tail=100
```

### Check HCO status
```bash
kubectl get hyperconverged -n openshift-cnv kubevirt-hyperconverged -o yaml
```

### View events
```bash
kubectl get events -n openshift-cnv
```

### Keep cluster for investigation
```bash
CLEANUP=false ./hack/run-e2e.sh
# Tests fail, cluster stays up for debugging
# Cleanup manually: CLUSTER_NAME=virt-platform-operator-e2e ./hack/kind-cluster.sh delete
```

## Architecture

```
┌─────────────────────────────────────────────────────────┐
│                     E2E Test Flow                       │
├─────────────────────────────────────────────────────────┤
│                                                         │
│  1. hack/run-e2e.sh                                    │
│     ├─ Create kind cluster                             │
│     ├─ Build operator image                            │
│     ├─ Deploy operator (hack/deploy-local.sh)         │
│     └─ Run ginkgo ./test/e2e/...                       │
│                                                         │
│  2. Test connects to kind cluster                      │
│     ├─ Real API server                                 │
│     ├─ Real controller-runtime manager                 │
│     ├─ Real watches configured                         │
│     └─ Real operator pods running                      │
│                                                         │
│  3. Tests verify end-to-end behavior                   │
│     ├─ Manager/controller infrastructure               │
│     ├─ Watch-based reconciliation                      │
│     ├─ Cache filtering                                 │
│     └─ Full reconciliation loop                        │
│                                                         │
└─────────────────────────────────────────────────────────┘
```

## Continuous Improvement

As new controller features are added, corresponding E2E tests should be added:
- New CRD types → test dynamic watch setup
- New cache optimizations → test filtering works
- New reconciliation logic → test end-to-end behavior
- New event types → test events are emitted

**Rule of thumb:** If it involves manager/controller/watches/cache, it needs an E2E test.
