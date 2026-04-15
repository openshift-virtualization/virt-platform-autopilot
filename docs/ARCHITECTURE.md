# Architecture Deep-Dive

This document provides technical details about the virt-platform-autopilot's architecture, design philosophy, and implementation.

## Design Philosophy

The **virt-platform-autopilot** embraces a **"Zero API Surface"** philosophy:

- **No new CRDs**: No custom resource definitions to manage
- **No API modifications**: No new fields added to existing APIs
- **No status fields**: No status checking or polling required
- **Consistent management**: ALL resources (including HCO) managed the same way

### Core Principles

1. **Zero API Surface**
   - Users never need to interact with autopilot-specific APIs
   - All control happens through standard Kubernetes annotations
   - No new resources to learn or monitor

2. **Silent Operation**
   - The autopilot works quietly in the background
   - Alerts fire only when user intervention is required
   - No status fields to poll or check

3. **GitOps-Native**
   - All customization via declarative annotations
   - Version-controllable, auditable, reproducible
   - Perfect for declarative infrastructure workflows

4. **Convention over Configuration**
   - Opinionated defaults based on production best practices
   - Flexible when customization is needed
   - No configuration required for common use cases

## Activation Gate (Opt-In)

> **Early-phase behaviour** — this gate will be removed (behaviour inverted to opt-out) once the project reaches production maturity.

In the current early phase the autopilot is **inactive by default**. It will not reconcile any resources — not even the HCO golden config — unless the `platform.kubevirt.io/autopilot` annotation is explicitly set on the HCO CR.

The annotation accepts two forms:

### Full activation

All eligible assets are reconciled (existing `install` mode and condition logic still applies):

```yaml
apiVersion: hco.kubevirt.io/v1beta1
kind: HyperConverged
metadata:
  name: kubevirt-hyperconverged
  namespace: openshift-cnv
  annotations:
    platform.kubevirt.io/autopilot: "true"
```

```bash
kubectl annotate hyperconverged kubevirt-hyperconverged -n openshift-cnv \
  platform.kubevirt.io/autopilot=true
```

### Selective activation (asset allowlist)

Only the named assets are considered for reconciliation. All other assets — including `hco-golden-config` if omitted — are skipped entirely. The normal opt-in logic (conditions, hardware detection, feature gates, CRD presence) still applies on top of this filter, so listing an asset name is a necessary but not always sufficient condition for it to be applied.

```yaml
annotations:
  platform.kubevirt.io/autopilot: "swap-enable,descheduler-loadaware,node-health-check"
```

```bash
kubectl annotate hyperconverged kubevirt-hyperconverged -n openshift-cnv \
  "platform.kubevirt.io/autopilot=swap-enable,descheduler-loadaware,node-health-check"
```

Asset names correspond to the `name` field in `assets/active/metadata.yaml`. The current set includes:

| Asset name | Group | Component | Notes |
|---|---|---|---|
| `prometheus-alerts` | | PrometheusRule | Soft dependency on Prometheus Operator CRD |
| `swap-enable` | | MachineConfig | Always-on baseline |
| `psi-enable` | `descheduler-loadaware` | MachineConfig | Gate CRD: KubeDescheduler; grouped with `descheduler-loadaware` for allowlist matching |
| `pci-passthrough` | | MachineConfig | Opt-in: hardware + annotation condition |
| `kubelet-perf-settings` | | KubeletConfig | Always-on baseline |
| `kubelet-cpu-manager` | | KubeletConfig | Opt-in: CPUManager feature gate |
| `node-health-check` | | NodeHealthCheck | Always-on baseline |
| `descheduler-loadaware` | | KubeDescheduler | Soft dependency on KubeDescheduler CRD |
| `tuned-default` | | Tuned | Always-on baseline; soft dependency on Tuned CRD |
| `mtv-operator` | | ForkliftController | Opt-in: annotation condition |
| `metallb-operator` | | MetalLB | Opt-in: annotation condition |
| `observability-operator` | | UIPlugin | Opt-in: annotation condition |

The `group` field enables **allowlist grouping**: listing `descheduler-loadaware` in the annotation activates both the `KubeDescheduler` asset (by name) and the `psi-enable` MachineConfig (by group). For example:

```bash
kubectl annotate hyperconverged kubevirt-hyperconverged -n openshift-cnv \
  "platform.kubevirt.io/autopilot=hco-golden-config,descheduler-loadaware"
```

This deploys the HCO golden config, the KubeDescheduler, **and** the PSI MachineConfig (via its group membership), but nothing else.

**When the annotation is absent or empty** the reconciler logs a message and returns immediately, re-queuing after the standard 5-minute interval:

```
Autopilot not enabled, keeping idle. Set annotation to opt in.
  annotation=platform.kubevirt.io/autopilot value=true or comma-separated asset names
```

**Rationale:** The opt-in gate lets cluster administrators install the operator and evaluate it safely before committing to automated management. The selective form lets administrators adopt the autopilot incrementally, one component at a time, without enabling everything at once.

**Future plan:** As the project matures the gate will be inverted — the autopilot will be active by default, and a separate opt-out annotation will allow administrators to disable it on specific clusters.

**Implementation:** The annotation is parsed at the very start of `PlatformReconciler.Reconcile()` in `pkg/controller/platform_controller.go` via `overrides.ParseAutopilotScope()` from `pkg/overrides/validation.go`. `IsAutopilotEnabled()` is a convenience wrapper over `ParseAutopilotScope` for callers that only need the boolean.

## Three-Tier Management Model

The autopilot manages resources across three tiers based on criticality and activation conditions:

### 1. Always-On (Phase 1)

Critical baseline configurations applied to all clusters:

- **NodeHealthCheck**: Automatic node remediation for failed hosts
- **MachineConfig**: OS-level optimizations
  - Swap optimization for memory management
  - NUMA topology awareness
  - PCI device passthrough enablement
- **KubeletConfig**: Kubelet performance settings
- **Operators**: Third-party operator CRs
  - MTV (Migration Toolkit for Virtualization)
  - MetalLB (Load balancing)
  - Observability stack

### 2. Context-Aware (Phase 1 opt-in)

Features activated based on conditions (annotations, hardware detection, feature gates):

- **KubeDescheduler** (`descheduler-loadaware`): LoadAware profile for intelligent workload balancing
  - Soft dependency on the KubeDescheduler CRD; skipped if the operator is not installed
  - Balances VM workloads across cluster nodes
- **PSI MachineConfig** (`psi-enable`): Enables kernel Pressure Stall Information for load-aware descheduling
  - Gate CRD: KubeDescheduler — only deployed when the descheduler operator is present
  - Grouped under `descheduler-loadaware` for allowlist matching
- **CPU Manager**: CPU pinning for guaranteed workloads
  - Activated via feature gate when QoS requirements detected

### 3. Advanced (Phase 2/3)

Specialized features for advanced use cases:

- **VFIO Device Assignment**: GPU and specialized hardware passthrough
- **USB Passthrough**: USB device assignment to VMs
- **AAQ Operator**: Advanced auto-scaling and quotas

## Reconciliation Flow

The autopilot follows a two-stage reconciliation process:

```
1. Apply golden HCO reference (with user annotations respected)
   ↓
2. Read effective HCO state → Build RenderContext
   ↓
3. Apply all other assets (MachineConfig, Descheduler, etc.) using RenderContext
```

### Why HCO Goes First

The HyperConverged object (HCO) serves a dual role:

1. **Managed resource**: The autopilot may apply configurations to HCO
2. **Configuration source**: Other assets read HCO's effective state to inform their rendering

This creates a dependency: HCO must be reconciled first so other assets can access its current state.

### RenderContext

The `RenderContext` is a data structure passed to all asset templates containing:

- **HCO Object**: The current state of the HyperConverged resource
- **Cluster Info**: Platform version, capabilities, detected hardware
- **Metadata**: Asset catalog metadata for conditional rendering

Templates use Go template syntax to access this context:

```yaml
# Example: Reference HCO namespace in another resource
apiVersion: v1
kind: ConfigMap
metadata:
  name: my-config
  namespace: {{ .HCO.Namespace }}
data:
  hco-name: {{ .HCO.Name }}
```

## Patched Baseline Algorithm

The core reconciliation algorithm for each asset:

```
For each asset:
1. Render template → Opinionated State
   - Process Go templates with RenderContext
   - Apply asset-specific logic and conditions

2. Apply user JSON patch (in-memory) → Modified State
   - Read platform.kubevirt.io/patch annotation
   - Apply RFC 6902 JSON Patch operations
   - Modifications happen in-memory before applying to cluster

3. Mask ignored fields from live object → Effective Desired State
   - Read platform.kubevirt.io/ignore-fields annotation
   - Remove masked fields from desired state
   - Allows users to manage specific fields manually

4. Drift detection via SSA dry-run
   - Compare desired state with live state
   - Use Server-Side Apply dry-run to detect differences
   - Skip apply if no drift detected

5. Anti-thrashing gate (token bucket)
   - Check rate limit budget
   - Prevent rapid reconciliation loops
   - Exponential backoff for problematic resources

6. Apply via Server-Side Apply
   - Use SSA with force=true to apply changes
   - Preserves fields managed by other controllers
   - Clean conflict resolution

7. Record update for throttling
   - Update rate limit token bucket
   - Track reconciliation timestamps
   - Enable metrics collection
```

### Server-Side Apply (SSA)

The autopilot uses Kubernetes Server-Side Apply with `fieldManager: virt-platform-autopilot`. This provides:

- **Clean ownership**: Clear field-level ownership tracking
- **Conflict resolution**: Automatic handling of competing controllers
- **Partial updates**: Only manages fields it declares
- **User override safety**: Users can take ownership via `force: true` applies

## Controller Endpoints

The controller exposes HTTP endpoints on three separate ports for security and operational clarity:

| Port | Endpoint | Purpose | Access |
|------|----------|---------|--------|
| `8080` | `/metrics` | Prometheus metrics | Public (service) |
| `8081` | `/debug/*` | Debug/render endpoints | Localhost only |
| `8082` | `/healthz`, `/readyz` | Health probes | Kubernetes probes |

### Debug Endpoints (Port 8081)

Localhost-only endpoints for debugging and inspection. Access via port-forward:

```bash
kubectl port-forward -n openshift-cnv deployment/virt-platform-autopilot 8081:8081
```

**Available endpoints:**

- `/debug/render` - Render all assets based on current HCO state
- `/debug/render/{asset}` - Render specific asset by name
- `/debug/exclusions` - List excluded/filtered assets with reasons
- `/debug/tombstones` - List tombstones (resources marked for deletion)
- `/debug/health` - Health check status

See [Debug Endpoints Documentation](debug-endpoints.md) for detailed usage.

### Render Command (Offline CLI)

Test asset rendering without a running cluster:

```bash
# Render assets offline using HCO file
virt-platform-autopilot render --hco-file=hco.yaml --output=status

# Or use HCO from cluster
virt-platform-autopilot render --kubeconfig=/path/to/config

# Output formats: status, yaml, json
virt-platform-autopilot render --hco-file=hco.yaml --output=yaml
```

This is useful for:
- Testing template changes locally
- Validating asset rendering before deployment
- Debugging template syntax errors
- CI/CD pipeline validation

## User Control Mechanisms

Users control the autopilot at four levels, from broadest to narrowest:

| Level | Scope | Mechanism |
|-------|-------|-----------|
| **Full activation** | All eligible assets | `platform.kubevirt.io/autopilot: "true"` on HCO (see [Activation Gate](#activation-gate-opt-in)) |
| **Selective activation** | Named asset subset | `platform.kubevirt.io/autopilot: "asset-a,asset-b"` on HCO — only listed assets are considered |
| **Resource exclusion** | One or more rendered resources | `platform.kubevirt.io/disabled-resources` on HCO |
| **Field masking** | Specific fields | `platform.kubevirt.io/ignore-fields` on the resource |
| **Full opt-out** | Single resource | `platform.kubevirt.io/mode: unmanaged` on the resource |

### 1. JSON Patch Override

Apply RFC 6902 JSON Patch operations to customize any field:

```yaml
apiVersion: machineconfiguration.openshift.io/v1
kind: MachineConfig
metadata:
  name: 90-worker-swap-online
  annotations:
    platform.kubevirt.io/patch: |
      [
        {"op": "replace", "path": "/spec/config/systemd/units/0/contents", "value": "..."},
        {"op": "add", "path": "/spec/config/storage/files/-", "value": {...}}
      ]
```

**Use cases:**
- Modify specific fields while keeping others managed
- Add new configuration sections
- Override specific values for environment-specific needs

### 2. Field Masking (Loose Ownership)

Exclude specific fields from management, allowing manual control:

```yaml
apiVersion: hco.kubevirt.io/v1beta1
kind: HyperConverged
metadata:
  annotations:
    platform.kubevirt.io/ignore-fields: "/spec/liveMigrationConfig/parallelMigrationsPerCluster,/spec/featureGates/enableCommonBootImageImport"
```

**How it works:**
- Masked fields are removed from the desired state before applying
- The autopilot will not manage or reconcile these fields
- Users can modify masked fields manually without interference
- Changes to masked fields won't trigger drift alerts

**Use cases:**
- Manual tuning of specific settings
- Temporary overrides during testing
- Fields managed by other automation

### 3. Full Opt-Out

Completely stop managing a resource:

```yaml
metadata:
  annotations:
    platform.kubevirt.io/mode: unmanaged
```

**Effect:**
- The autopilot will skip this resource entirely
- No rendering, no drift detection, no reconciliation
- Resource becomes fully manual

**Use cases:**
- Complete manual control for specific resources
- Temporary disabling during troubleshooting
- Resources managed by external tools

## Resource Lifecycle Management

The autopilot provides mechanisms for managing resource lifecycle during upgrades and configuration changes.

### Tombstoning

Safely delete obsolete resources when features are removed or resources are renamed:

```bash
# Move obsolete resource to tombstones directory
git mv assets/active/config/old-resource.yaml assets/tombstones/v1.1-cleanup/
```

On the next reconciliation, the operator will:
1. Detect the tombstoned resource
2. Verify it has the `platform.kubevirt.io/managed-by` label (safety check)
3. Delete the resource from the cluster

**Safety features:**
- Label verification prevents accidental deletion of unrelated resources
- Best-effort execution (continues even if some deletions fail)
- Idempotent (already-deleted resources are skipped)
- Tombstones are processed before active assets

### Root Exclusion

Prevent specific resources from being created or managed:

```yaml
apiVersion: hco.kubevirt.io/v1beta1
kind: HyperConverged
metadata:
  annotations:
    platform.kubevirt.io/disabled-resources: |
      - kind: KubeDescheduler
        name: cluster
      - kind: MachineConfig
        name: 50-swap-enable
```

**Format:** YAML array with `kind`, `name`, and optional `namespace` fields (supports wildcards)

**Use cases:**
- Disable features not needed in specific deployments
- Temporary workarounds for known issues
- Prevent resource creation in environments where it would fail (e.g., CRD not installed)
- Pattern-based exclusions using wildcards (e.g., `name: virt-*`)
- Namespace-specific exclusions (e.g., `namespace: prod-*`)

For detailed documentation, see: [Resource Lifecycle Management](lifecycle-management.md)

## Observability

### Metrics

The autopilot exposes Prometheus metrics on port 8080 (`/metrics`):

- `kubevirt_autopilot_asset_reconcile_total` - Total reconciliations per asset
- `kubevirt_autopilot_asset_reconcile_errors_total` - Reconciliation errors per asset
- `kubevirt_autopilot_asset_apply_total` - Successful applies per asset
- `kubevirt_autopilot_drift_detected_total` - Drift detections per asset
- `kubevirt_autopilot_throttle_delayed_total` - Reconciliations delayed by throttling

### Alerts

The autopilot fires alerts only when user intervention is required:

- **VirtPlatformSyncFailed**: Asset reconciliation failing repeatedly
- **VirtPlatformDependencyMissing**: Required CRD or dependency not found
- **VirtPlatformThrashingDetected**: Excessive reconciliation indicating configuration issue
- **VirtPlatformTombstoneStuck**: Tombstone deletion failing

See [Runbooks](runbooks/) for detailed alert descriptions and remediation steps.

### Events

Kubernetes events are emitted for significant state changes:

- Asset applied successfully
- Drift detected and reconciled
- User patch applied
- Tombstone processed
- Errors and warnings

## Project Structure

```
virt-platform-autopilot/
├── cmd/
│   ├── main.go                    # Manager entrypoint
│   └── rbac-gen/                  # RBAC generation tool
├── pkg/
│   ├── controller/                # Main reconciler
│   ├── engine/                    # Rendering, patching, drift detection
│   ├── assets/                    # Asset loader and registry
│   ├── overrides/                 # User override logic (patch, mask)
│   ├── throttling/                # Anti-thrashing protection
│   └── util/                      # Utilities
├── assets/                        # Embedded asset templates
│   ├── active/                    # Active assets applied to cluster
│   │   ├── hco/                   # Golden HCO reference (reconcile_order: 0)
│   │   ├── machine-config/        # OS-level configs
│   │   ├── kubelet/               # Kubelet settings
│   │   ├── descheduler/           # KubeDescheduler
│   │   ├── node-health/           # NodeHealthCheck
│   │   ├── operators/             # Third-party operator CRs
│   │   └── metadata.yaml          # Asset catalog
│   └── tombstones/                # Obsolete resources for deletion
├── config/                        # Kubernetes manifests for deployment
└── docs/                          # Documentation
```

## Asset Management

### Asset Catalog (`assets/active/metadata.yaml`)

The metadata catalog defines all managed assets and their properties:

```yaml
assets:
  - name: hco-golden-config
    path: active/hco/golden-config.yaml.tpl
    phase: 0
    install: always
    component: HyperConverged
    reconcile_order: 0  # HCO must be first

  - name: swap-enable
    path: active/machine-config/01-swap-enable.yaml
    phase: 1
    install: always
    component: MachineConfig
    reconcile_order: 1

  - name: psi-enable
    group: descheduler-loadaware        # included in allowlist when "descheduler-loadaware" is listed
    gate_crd: kubedeschedulers.operator.openshift.io  # skipped if KubeDescheduler CRD is absent
    path: active/machine-config/04-psi-enable.yaml
    phase: 1
    install: always
    component: MachineConfig
    reconcile_order: 1

  - name: descheduler-loadaware
    path: active/descheduler/recommended.yaml.tpl
    phase: 1
    install: always
    component: KubeDescheduler
    reconcile_order: 1
    conditions: []
```

**Metadata fields:**

- `name`: Unique asset identifier (used by the debug endpoint and the opt-in allowlist)
- `group`: Optional group name for allowlist matching — an asset is included if its `name` **or** its `group` appears in the allowlist
- `path`: Template file path relative to `assets/`
- `gate_crd`: Optional additional CRD that must be present at runtime (on top of the auto-detected `RequiredCRD`); also registered with the CRD watch handler so installs/removals trigger re-reconciliation
- `phase`: Rollout phase (0=HCO bootstrap, 1=standard)
- `install`: `always` or `opt-in` (opt-in without conditions is never applied)
- `component`: Kubernetes Kind of the primary managed resource
- `reconcile_order`: Processing order within a phase (lower = earlier)
- `conditions`: Activation conditions (annotations, hardware detection, feature gates) — all must be satisfied (AND logic)

### Soft Dependencies

The autopilot gracefully handles missing runtime dependencies without raising errors or blocking other assets.

**Missing CRD** — if the CRD required by an asset is not installed, the asset is skipped before rendering. Two mechanisms declare CRD dependencies:

- **`RequiredCRD`** (auto-detected): derived from the `apiVersion`/`kind` of the resource in the template. Guards against the operator not being installed.
- **`gate_crd`** (explicit): set in `metadata.yaml`; declares an additional CRD that must be present. Used when an asset's own CRD is always available (e.g. `MachineConfig`) but deployment should be gated on another operator (e.g. the PSI MachineConfig requires the KubeDescheduler CRD).

In both cases:
- No error is raised
- Reconciliation continues with other assets
- Asset is automatically applied when the CRD becomes available (CRD watch triggers re-reconciliation)

**Missing operator namespace (CRD leftover)** — a subtler case occurs when a CRD exists as a leftover from a previously installed operator whose namespace and workloads have since been removed. In this situation the CRD check passes, the asset renders to a valid object, but the SSA apply fails because the target namespace does not exist. The autopilot detects this condition and treats it as a soft skip:
- No error is raised and no failure event is emitted
- Reconciliation continues with other assets
- The asset will be applied on the next periodic reconciliation cycle (every 5 minutes) once the operator is reinstalled and its namespace recreated

### Adding New Assets

To extend the platform with new components, see the [Adding Assets Guide](adding-assets.md).

## Anti-Thrashing Protection

The autopilot includes sophisticated anti-thrashing mechanisms to prevent reconciliation loops:

### Token Bucket Algorithm

Each asset has a token bucket with:
- **Capacity**: Maximum burst allowance
- **Refill rate**: Tokens added per time period
- **Cost per apply**: Tokens consumed per reconciliation

If an asset exhausts its budget:
- Reconciliation is delayed
- Exponential backoff applies
- Alert fires if thrashing persists

### Drift Detection

The autopilot uses Server-Side Apply dry-run to detect drift:
1. Render desired state
2. Apply user patches and masks
3. SSA dry-run to compare with live state
4. Skip apply if no drift detected

This prevents unnecessary applies when the resource is already in the desired state.

See [Anti-Thrashing Design](anti-thrashing-design.md) for implementation details.

## Development

### RBAC Generation

The autopilot automatically generates RBAC permissions based on managed resource types:

```bash
# After adding new resource types, regenerate RBAC
make generate-rbac
```

This scans `assets/active/` for resource types and generates:
- ClusterRole with required permissions
- RoleBindings for service account

### Testing

```bash
# Unit tests
make test

# Integration tests (uses envtest)
make test-integration

# Local development with Kind
make kind-setup        # Setup local cluster with CRDs
make deploy-local      # Deploy autopilot
make logs-local        # View logs
make redeploy-local    # Redeploy after changes
```

See [Local Development Guide](local-development.md) for complete instructions.

## Future Enhancements

Potential areas for expansion:

- **Hardware detection plugins**: Extensible GPU/device detection
- **Multi-cluster support**: Manage multiple clusters from single control plane
- **Advanced scheduling**: More sophisticated workload placement policies
- **Capacity planning**: Predictive resource allocation
- **Auto-scaling integration**: Dynamic cluster scaling based on VM workloads

## Related Documentation

- [README](../README.md) - Overview and quick start
- [Adding Assets](adding-assets.md) - Guide for extending the platform
- [Local Development](local-development.md) - Development environment setup
- [Lifecycle Management](lifecycle-management.md) - Tombstoning and exclusions
- [Debug Endpoints](debug-endpoints.md) - Debugging tools
- [Anti-Thrashing Design](anti-thrashing-design.md) - Throttling implementation
- [Runbooks](runbooks/) - Alert remediation guides
