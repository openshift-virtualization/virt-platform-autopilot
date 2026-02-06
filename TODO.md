# virt-platform-operator TODO

This document tracks implementation progress against the original plan in `claude_assets/claude_plan.md`.

## Design Philosophy (from Plan)

**"Zero API Surface"** - No new CRDs, no API modifications, annotation-based control only.

**Key Principle**: ALL resources managed the same way using the **Patched Baseline Algorithm**:
1. Render asset template → Opinionated State
2. Apply user JSON patch from annotation (in-memory) → Modified State
3. Mask ignored fields from annotation → Effective Desired State
4. Drift detection via SSA dry-run
5. Anti-thrashing gate (token bucket)
6. Apply via Server-Side Apply
7. Record update for throttling

**User Control Annotations** (applies to ANY managed resource, including HCO):
- `platform.kubevirt.io/patch` - RFC 6902 JSON Patch for specific overrides
- `platform.kubevirt.io/ignore-fields` - RFC 6901 JSON Pointers for loose ownership
- `platform.kubevirt.io/mode: unmanaged` - Full opt-out

---

## Phase 1: Foundation (Week 1-2) - PARTIALLY COMPLETE

### ✅ Completed
- [x] Project bootstrap (go.mod, Makefile, Dockerfile, cmd/main.go)
- [x] Asset loader with //go:embed (pkg/assets/loader.go)
- [x] Asset registry (pkg/assets/registry.go)
- [x] Asset catalog (assets/metadata.yaml) with reconcile_order support
- [x] HCO context builder (pkg/controller/hco_context.go) - passes full HCO + hardware
- [x] Basic reconciler (pkg/controller/platform_controller.go)
  - [x] Reconcile assets in reconcile_order (HCO first at order 0)
  - [x] Read HCO after reconciliation for RenderContext
- [x] Golden HCO reference (assets/hco/golden-config.yaml.tpl)
- [x] Template rendering fix for unstructured objects (use `dig` helper)
- [x] Swap MachineConfig asset (assets/machine-config/01-swap-enable.yaml)
- [x] CRD collection structure (assets/crds/)
- [x] Hardware detection logic for conditional assets
- [x] Basic drift detection via SSA (pkg/engine/drift.go)
- [x] Basic SSA application (pkg/engine/applier.go)

### ❌ Missing from Phase 1
- [ ] **Complete Patched Baseline Algorithm** (pkg/engine/patcher.go)
  - Current: Only basic rendering + SSA
  - Missing: User patch application, field masking, throttling integration
- [ ] **RBAC generation tool** (cmd/rbac-gen/main.go)
  - Current: Manually maintained config/rbac/role.yaml
  - Missing: Build tool to scan assets → generate RBAC
- [ ] **CRD update script** (hack/update-crds.sh)
  - Current: Manual CRD collection
  - Missing: Automated fetching from upstream
- [ ] **envtest integration** for proper SSA testing
  - Current: No integration tests
  - Missing: Real API server testing (fake client insufficient for SSA)

---

## Phase 2: Core Algorithm & User Overrides (Week 3-4) - NOT STARTED

### ❌ User Override System (CRITICAL - Core Design Feature)

**Status**: Not implemented. This is fundamental to the "Zero API Surface" philosophy.

**Required packages** (from plan lines 393-403):
- [ ] `pkg/overrides/jsonpatch.go` - RFC 6902 JSON Patch application
  - Use github.com/evanphx/json-patch/v5
  - Apply patch to unstructured object in-memory
  - Error handling for invalid patches
- [ ] `pkg/overrides/jsonpointer.go` - RFC 6901 JSON Pointer field masking
  - Parse comma-separated pointers from annotation
  - Extract values from live object
  - Set into desired object (operator yields control)
- [ ] `pkg/overrides/validation.go` - Security validation
  - Define sensitiveKinds map (MachineConfig, etc.)
  - Block JSON patches on sensitive resources
  - Emit events for validation failures

**What this enables**:
- Users can customize ANY managed resource via annotations
- HCO itself can be customized (patch, ignore-fields, unmanaged)
- Selective field ownership (ignore-fields lets users manage specific fields)
- Complete opt-out per resource (mode: unmanaged)

### ❌ Complete Patched Baseline Algorithm

**Current state**: We have basic rendering and SSA, but NOT the full algorithm.

**Missing integration** (pkg/engine/patcher.go should orchestrate):
- [ ] Check opt-out annotation (mode: unmanaged) before processing
- [ ] Apply user patch from `platform.kubevirt.io/patch` annotation (in-memory)
- [ ] Mask ignored fields from `platform.kubevirt.io/ignore-fields` annotation
- [ ] Anti-thrashing gate before SSA
- [ ] Record update timestamp for throttling

### ❌ Phase 1 Asset Templates

**Always-On Assets** (from plan lines 423-432):
- [x] assets/machine-config/01-swap-enable.yaml
- [ ] assets/machine-config/02-pci-passthrough.yaml.tpl (PCI/IOMMU)
- [ ] assets/machine-config/03-numa.yaml.tpl (NUMA topology)
- [ ] assets/kubelet/perf-settings.yaml.tpl (nodeStatusMaxImages, maxPods)
- [x] assets/node-health/standard-remediation.yaml
- [ ] assets/operators/mtv.yaml.tpl (MTV operator CR)
- [x] assets/operators/metallb.yaml.tpl (MetalLB operator CR)
- [x] assets/operators/observability.yaml.tpl (Observability UI plugin)

**Opt-In Assets** (from plan lines 434-436):
- [x] assets/descheduler/recommended.yaml.tpl (KubeDescheduler LoadAware)
- [ ] assets/kubelet/cpu-manager.yaml.tpl (CPU manager for guaranteed cpu)

**Milestone for Phase 2**: User annotations working (patch, ignore-fields, unmanaged) on all resources.

---

## Phase 3: Safety & Context-Awareness (Week 5-6) - NOT STARTED

### ❌ Anti-Thrashing Protection (CRITICAL)

**Status**: Not implemented. Required to prevent infinite reconciliation loops.

**Required** (from plan lines 592-598):
- [ ] `pkg/throttling/token_bucket.go` - Token bucket implementation
  - Per-resource key tracking
  - Configurable capacity (5 updates) and window (1 minute)
  - Refill on window expiration
  - Return ThrottledError when exhausted
- [ ] Update pkg/engine/patcher.go to check throttle before applying
- [ ] Event recording for "ThrashingDetected"

**Why critical**: Without this, conflicting user modifications can cause reconciliation storms.

### ❌ Event Recording

**Status**: Not implemented.

**Required** (from plan lines 699-703):
- [ ] `pkg/util/events.go` - Event helpers
  - InvalidPatch, InvalidIgnoreFields, PatchBlocked
  - ThrashingDetected, CRDMissing
  - ReconciliationSucceeded, DriftDetected

### ❌ Soft Dependency Handling

**Status**: Basic error handling exists, but not comprehensive.

**Required** (from plan lines 693-697):
- [ ] `pkg/util/crd_checker.go` - Check if CRD exists before creating resource
- [ ] Log warning but don't fail reconciliation
- [ ] Skip watch registration for missing CRDs

### ✅ Asset Condition Evaluation

**Status**: Partially implemented.

- [x] Hardware-detection conditions (pciDevicesPresent, numaNodesPresent, etc.)
- [x] Feature-gate conditions
- [x] Annotation conditions
- [ ] Better error handling for condition evaluation failures

### ❌ Phase 2 & 3 Asset Templates

**Phase 2 Assets** (from plan lines 437-441):
- [ ] assets/machine-config/04-vfio-assign.yaml.tpl (VFIO device assignment)
- [ ] assets/operators/aaq.yaml.tpl (AAQ quota operator)
- [ ] assets/operators/node-maintenance.yaml.tpl (Node maintenance operator)
- [ ] assets/operators/fence-agents.yaml.tpl (Fence agents remediation)

**Phase 3 Assets** (from plan line 443):
- [ ] assets/machine-config/05-usb-passthrough.yaml.tpl (USB passthrough)

---

## Phase 4: Build Tooling & Testing (Week 6+) - NOT STARTED

### ❌ RBAC Generation Tool

**Status**: Not implemented. Role.yaml manually maintained (plan lines 712-717).

**Required**: cmd/rbac-gen/main.go
- [ ] Walk assets/ directory (exclude assets/crds/)
- [ ] Parse YAML/templates (replace {{ }} with dummy values)
- [ ] Extract GVKs from parsed resources
- [ ] Generate ClusterRole with exact permissions
- [ ] Output to config/rbac/role.yaml with "DO NOT EDIT" header
- [ ] Integrate with Makefile (`make generate-rbac`)

**Current workaround**: Manually maintain config/rbac/role.yaml.

### ❌ CRD Management

**Status**: Manual CRD collection (plan lines 719-723).

**Required**: hack/update-crds.sh
- [ ] Fetch CRDs from upstream repositories
- [ ] Organize into assets/crds/ structure
- [ ] Update assets/crds/README.md with versions and sources
- [ ] Validate CRDs can be loaded by envtest
- [ ] Makefile targets: `make update-crds`, `make verify-crds`

### ❌ Testing Infrastructure (CRITICAL)

**Status**: No tests. Plan requires envtest for SSA verification (plan lines 731-747).

**Why envtest required**: Fake client doesn't implement SSA semantics correctly. Real API server needed for:
- Field ownership verification
- Managed fields tracking
- Drift detection via SSA dry-run
- User override conflicts

**Required test files**:

**Unit Tests** (can use fake client):
- [ ] pkg/overrides/jsonpatch_test.go - JSON Patch application
- [ ] pkg/overrides/jsonpointer_test.go - Field masking
- [ ] pkg/overrides/validation_test.go - Security validation
- [ ] pkg/throttling/token_bucket_test.go - Anti-thrashing
- [ ] pkg/assets/loader_test.go - Template rendering
- [ ] pkg/assets/registry_test.go - Catalog loading

**Integration Tests** (requires envtest):
- [ ] test/integration_suite_test.go - envtest setup with all CRDs
- [ ] pkg/controller/platform_controller_test.go - Controller with real API server
- [ ] pkg/engine/patcher_test.go - Patched Baseline algorithm with SSA
- [ ] pkg/engine/drift_test.go - Drift detection via SSA dry-run

**Test scenarios to cover**:
- [ ] Asset creation with SSA (verify managedFields)
- [ ] Drift detection (modify resource, verify reconciliation)
- [ ] User patch annotation (apply JSON patch, verify override)
- [ ] Ignore-fields annotation (user modifies field, verify operator doesn't revert)
- [ ] Unmanaged mode (resource ignored by operator)
- [ ] Anti-thrashing (rapid updates trigger throttle)
- [ ] Field ownership conflicts (operator vs user modifications)
- [ ] HCO golden config with user customization
- [ ] Context-aware assets (hardware detection, feature gates)

**Coverage target**: >80% for integration tests with envtest.

### ❌ Documentation

**Required** (from plan lines 749-752):
- [ ] README.md - Project overview, architecture, getting started
- [ ] docs/user-guide.md - How to use annotations for customization
  - Examples of patch, ignore-fields, unmanaged mode
  - Security considerations (what can/cannot be patched)
- [ ] docs/assets.md - Asset catalog reference
  - List of all managed assets
  - Conditions for each asset
  - Template variables available
- [ ] docs/architecture.md - Patched Baseline algorithm explanation
  - Algorithm flow diagram
  - Reconciliation order explanation
  - HCO dual role (managed + config source)

---

## Critical Missing Features (High Priority)

These are core to the design philosophy and should be implemented before adding more assets:

### 1. User Override System (Phase 2)
**Why critical**: This is the entire value proposition - "Zero API Surface" with annotation-based control.

Without this, users cannot:
- Customize HCO golden config
- Override opinionated settings
- Take control of specific fields
- Opt-out of management

**Implementation order**:
1. pkg/overrides/jsonpatch.go
2. pkg/overrides/jsonpointer.go
3. pkg/overrides/validation.go
4. Integration into pkg/engine/patcher.go
5. Tests with envtest

### 2. Anti-Thrashing Protection (Phase 3)
**Why critical**: Without throttling, conflicting modifications cause infinite loops.

**Scenario**: User applies patch, operator reconciles, user patch conflicts, operator re-applies, repeat...

**Implementation**:
1. pkg/throttling/token_bucket.go
2. Integration into pkg/engine/patcher.go (check before SSA)
3. Event emission for "ThrashingDetected"

### 3. Complete Patched Baseline Algorithm (Phase 2)
**Why critical**: Current implementation is incomplete - missing steps 2, 3, 5, 7 from the algorithm.

**Current flow**:
```
1. Render template ✅
2. Apply user patch ❌ (MISSING)
3. Mask ignored fields ❌ (MISSING)
4. Drift detection ✅
5. Anti-thrashing gate ❌ (MISSING)
6. SSA application ✅
7. Record update ❌ (MISSING)
```

**Fix**: Update pkg/engine/patcher.go to implement full algorithm.

### 4. Testing with envtest (Phase 4)
**Why critical**: Cannot verify SSA semantics without real API server.

**Risk**: Current code may not handle field ownership correctly, causing conflicts with users.

---

## Implementation Progress by Package

### ✅ Fully Implemented
- `pkg/assets/loader.go` - Asset loading from embedded FS
- `pkg/context/render_context.go` - RenderContext data structure
- `pkg/controller/hco_context.go` - Hardware detection and context building
- `pkg/engine/renderer.go` - Template rendering with sprig
- `pkg/engine/applier.go` - Basic SSA application
- `pkg/engine/drift.go` - SSA dry-run drift detection

### ⚠️ Partially Implemented
- `pkg/controller/platform_controller.go` - Basic reconciliation, but missing:
  - User override support
  - Throttling integration
  - Proper error handling
- `pkg/assets/registry.go` - Asset catalog, but missing:
  - Better condition error handling
  - Soft dependency checks
- `pkg/engine/patcher.go` - Basic flow, but missing:
  - User patch application (step 2)
  - Field masking (step 3)
  - Throttling gate (step 5)
  - Update recording (step 7)

### ❌ Not Implemented
- `pkg/overrides/` - Entire package missing
  - jsonpatch.go
  - jsonpointer.go
  - validation.go
- `pkg/throttling/` - Entire package missing
  - token_bucket.go
- `pkg/util/` - Missing utilities
  - crd_checker.go
  - events.go
- `cmd/rbac-gen/` - RBAC generation tool
- `test/` - All testing infrastructure

---

## Asset Implementation Status

### Phase 0: HCO Golden Config
- [x] assets/hco/golden-config.yaml.tpl (managed first, then read for context)

### Phase 1: Always-On (MVP)
- [x] assets/machine-config/01-swap-enable.yaml (Swap configuration)
- [ ] assets/machine-config/02-pci-passthrough.yaml.tpl (IOMMU for PCI passthrough)
- [ ] assets/machine-config/03-numa.yaml.tpl (NUMA topology)
- [ ] assets/kubelet/perf-settings.yaml.tpl (nodeStatusMaxImages, maxPods)
- [x] assets/node-health/standard-remediation.yaml (NodeHealthCheck + SNR)
- [ ] assets/operators/mtv.yaml.tpl (MTV operator CR)
- [x] assets/operators/metallb.yaml.tpl (MetalLB operator CR)
- [x] assets/operators/observability.yaml.tpl (Observability UI plugin)

### Phase 1: Opt-In
- [x] assets/descheduler/recommended.yaml.tpl (KubeDescheduler LoadAware)
- [ ] assets/kubelet/cpu-manager.yaml.tpl (CPU manager for guaranteed cpu)

### Phase 2: Advanced
- [ ] assets/machine-config/04-vfio-assign.yaml.tpl (VFIO device assignment)
- [ ] assets/operators/aaq.yaml.tpl (AAQ quota operator)
- [ ] assets/operators/node-maintenance.yaml.tpl (Node maintenance)
- [ ] assets/operators/fence-agents.yaml.tpl (Fence agents remediation)

### Phase 3: Specialized
- [ ] assets/machine-config/05-usb-passthrough.yaml.tpl (USB passthrough)

---

## CRD Dependencies

### Available in Cluster (Working)
- [x] HyperConverged (`hco.kubevirt.io/v1beta1`)
- [x] MachineConfig (`machineconfiguration.openshift.io/v1`)
- [x] NodeHealthCheck (`remediation.medik8s.io/v1alpha1`)
- [x] ForkliftController (`forklift.konveyor.io/v1beta1`) - CRD present
- [x] MetalLB (`metallb.io/v1beta1`) - CRD present

### Need CRD Installation
- [ ] KubeletConfig (`machineconfiguration.openshift.io/v1`) - Not in Kind
- [ ] KubeDescheduler (`operator.openshift.io/v1`) - Not in Kind
- [ ] UIPlugin (`observability.openshift.io/v1alpha1`) - Not in Kind
- [ ] AAQOperatorConfig (TBD)
- [ ] NodeMaintenance (TBD)
- [ ] FenceAgentsRemediation (`fence-agents-remediation.medik8s.io/v1alpha1`)

---

## Known Issues

### 1. Reconciler Warning
**Issue**: "Reconciler returned both a result with RequeueAfter and a non-nil error"
- **Location**: pkg/controller/platform_controller.go:121, 144
- **Impact**: Harmless warning, reconciliation still works
- **Fix**: Return either error OR RequeueAfter, not both

### 2. SSA Dry-Run Warning
**Issue**: "metadata.managedFields must be nil"
- **Location**: During drift detection
- **Impact**: Falls back to simple drift check, no functional impact
- **Fix**: Clear managedFields before dry-run apply

### 3. Incomplete Patched Baseline Algorithm
**Issue**: Missing user override support
- **Location**: pkg/engine/patcher.go
- **Impact**: Users cannot customize managed resources
- **Fix**: Implement Phase 2 (User Override System)

### 4. No Anti-Thrashing Protection
**Issue**: Conflicting modifications can cause infinite loops
- **Location**: Missing pkg/throttling/
- **Impact**: Risk of reconciliation storms
- **Fix**: Implement Phase 3 (Anti-Thrashing Protection)

---

## Recommended Implementation Order

Based on the original plan and current state:

### Immediate (Week 1): Complete Phase 2 - User Overrides
1. Implement pkg/overrides/jsonpatch.go
2. Implement pkg/overrides/jsonpointer.go
3. Implement pkg/overrides/validation.go
4. Update pkg/engine/patcher.go to use overrides
5. Add basic unit tests (fake client OK for overrides package)

**Why first**: Core design feature, enables user control without new APIs.

### Next (Week 2): Complete Phase 3 - Safety
1. Implement pkg/throttling/token_bucket.go
2. Integrate throttling into pkg/engine/patcher.go
3. Implement pkg/util/events.go
4. Add pkg/util/crd_checker.go for soft dependencies
5. Add unit tests for throttling

**Why next**: Prevents production issues (infinite loops, crashes).

### Then (Week 3-4): Testing Infrastructure
1. Create test/integration_suite_test.go with envtest setup
2. Add integration tests for Patched Baseline algorithm
3. Add integration tests for user overrides
4. Add integration tests for drift detection
5. Achieve >80% integration coverage

**Why important**: Verify SSA semantics work correctly, catch field ownership bugs.

### Finally (Week 5+): Build Tooling & Remaining Assets
1. Implement cmd/rbac-gen/main.go
2. Create hack/update-crds.sh
3. Add remaining Phase 1 assets (PCI, NUMA, kubelet, MTV)
4. Add Phase 2 assets (VFIO, AAQ, node-maintenance, fence-agents)
5. Add Phase 3 assets (USB passthrough)
6. Write documentation

---

## Nice to have
- [ ] Configuring controller runtime chache to watch only managed objects with a label selector (not for CRDs)
- [ ] Always label managed objects for tracking and visibility
- [ ] If the user removes the label our cached client will be blind, we detect the object as missing and try to recreate. Let's detect the issue and add back the missing label
- [ ] VEP to limit RBACs to specific objects

## Success Criteria (from Original Plan)

### Technical Goals
- [ ] Zero API surface (no CRDs, no new fields) ✅
- [ ] Consistent management pattern (ALL resources managed same way) ✅
- [ ] HCO dual role (managed + config source) ✅
- [ ] **Patched Baseline algorithm fully implemented** ❌ **INCOMPLETE**
- [ ] **All three user override mechanisms functional** ❌ **NOT IMPLEMENTED**
- [ ] **Anti-thrashing protection working** ❌ **NOT IMPLEMENTED**
- [ ] Build-time RBAC generation from assets ❌
- [ ] Soft dependency handling ⚠️ **PARTIAL**
- [ ] **>80% integration test coverage with envtest** ❌ **NO TESTS**

### Operational Goals
- [ ] Phase 1 Always assets deployed automatically ⚠️ **PARTIAL** (5/8)
- [ ] Phase 1 Opt-in assets conditionally applied ⚠️ **PARTIAL** (1/2)
- [ ] Phase 2/3 assets available ❌ **NOT STARTED**
- [ ] **Users can customize via annotations** ❌ **NOT IMPLEMENTED**
- [ ] Operator handles missing CRDs gracefully ⚠️ **PARTIAL**
- [ ] Asset catalog matches plan scope ⚠️ **PARTIAL**

### Current Status
**Phase 1**: 60% complete (foundation working, missing overrides)
**Phase 2**: 0% complete (user override system not started)
**Phase 3**: 10% complete (basic hardware detection, missing throttling/events)
**Phase 4**: 0% complete (no tests, no RBAC gen, minimal docs)

**Overall**: ~20% complete against original plan.

---

## Next Steps

To align with the original plan:

1. **Acknowledge the gap**: Current implementation is missing core features
2. **Prioritize user overrides**: This is the fundamental value proposition
3. **Add anti-thrashing**: Required for production safety
4. **Write tests**: envtest integration tests to verify SSA behavior
5. **Document**: Explain Patched Baseline algorithm and user control

The current implementation provides a solid foundation (asset loading, rendering, basic reconciliation), but the differentiating features (annotation-based user control, anti-thrashing, complete Patched Baseline algorithm) are not yet implemented.
