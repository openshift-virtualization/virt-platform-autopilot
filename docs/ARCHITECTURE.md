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

## Activation Gate (Opt-Out)

The autopilot is **GA** and **enabled by default**. Once the operator is deployed it reconciles all eligible assets (existing `install` mode and condition logic still applies) without any additional opt-in on the HCO CR.

The `platform.kubevirt.io/autopilot` annotation is now purely an **opt-out** switch:

### Disabling the autopilot

Set the annotation to `"false"` to make the autopilot go idle. It will not reconcile any resources — not even the HCO golden config — while disabled:

```yaml
apiVersion: hco.kubevirt.io/v1beta1
kind: HyperConverged
metadata:
  name: kubevirt-hyperconverged
  namespace: openshift-cnv
  annotations:
    platform.kubevirt.io/autopilot: "false"
```

```bash
kubectl annotate hyperconverged kubevirt-hyperconverged -n openshift-cnv \
  platform.kubevirt.io/autopilot=false
```

### Re-enabling the autopilot

Removing the annotation (or setting any value other than `"false"`, e.g. `"true"`) re-enables management:

```bash
kubectl annotate --overwrite hyperconverged kubevirt-hyperconverged -n openshift-cnv \
  platform.kubevirt.io/autopilot-
```

**When the autopilot is disabled** the reconciler logs a message and returns immediately, re-queuing after the standard 5-minute interval:

```
Autopilot disabled via annotation, keeping idle.
  annotation=platform.kubevirt.io/autopilot value=false
```

**Rationale:** Now that the project is GA, the autopilot manages the platform out of the box. The opt-out switch lets administrators disable automated management on specific clusters when needed.

**Implementation:** The annotation is evaluated at the very start of `PlatformReconciler.Reconcile()` in `pkg/controller/platform_controller.go` via `overrides.IsAutopilotEnabled()` from `pkg/overrides/validation.go`.

## Three-Tier Management Model

The autopilot manages resources across three tiers based on criticality and activation conditions:

### 1. Always-On (Phase 1)

Critical baseline configurations applied to all clusters:

- **MachineConfig**: OS-level optimizations
  - Swap optimization for memory management
  - NUMA topology awareness
  - PCI device passthrough enablement
- **KubeletConfig**: Kubelet performance settings
- **Operators**: Third-party operator CRs
  - **Monitoring UI Plugin** (`monitoring-ui-plugin`): Enables the Perses dashboard UI in
    the OpenShift console via the Cluster Observability Operator (COO). Automatically skipped
    when the `uiplugins.observability.openshift.io` CRD is absent. When ACM is present it
    manages the same `monitoring` UIPlugin and adds `spec.monitoring.acm.*` fields; SSA field
    managers don't conflict because autopilot only owns `spec.monitoring.perses.enabled`.
  - MTV (Migration Toolkit for Virtualization)
  - MetalLB (Load balancing)
  - Monitoring UIPlugin (see `monitoring-ui-plugin` above)

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
| `8443` | `/metrics` | Prometheus metrics | HTTPS + mTLS (in-cluster Prometheus only) |
| `8081` | `/debug/*` | Debug/render endpoints | Localhost only |
| `8082` | `/healthz`, `/readyz` | Health probes | Kubernetes probes |

The metrics endpoint is served over HTTPS using a serving certificate minted by
the OpenShift service-ca operator and requires client-certificate authentication
(`RequireAndVerifyClientCert`); only the in-cluster Prometheus service account
(`system:serviceaccount:openshift-monitoring:prometheus-k8s`) is authorized. The
minimum TLS version and cipher suites follow the cluster TLS security profile
(the HCO CR `spec.security.tlsSecurityProfile`, falling back to the APIServer
`cluster` CR, then to the Intermediate profile).

### Debug Endpoints (Port 8081)

Localhost-only endpoints for debugging and inspection. Access via port-forward:

```bash
kubectl port-forward -n openshift-cnv deployment/virt-platform-autopilot 8081:8081
```

**Available endpoints:**

- `/debug/render` - Render all assets based on current HCO state
- `/debug/render/{asset}` - Render specific asset by name
- `/debug/exclusions` - List excluded/filtered assets with reasons
- `/debug/features` - Feature catalog with maturity, install mode, and opt-in conditions
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

Users control the autopilot at several levels, from broadest to narrowest:

| Level | Scope | Mechanism |
|-------|-------|-----------|
| **Global opt-out** | All eligible assets | `platform.kubevirt.io/autopilot: "false"` on HCO disables the autopilot entirely (enabled by default — see [Activation Gate](#activation-gate-opt-out)) |
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

The autopilot exposes Prometheus metrics over HTTPS (mTLS) on port 8443 (`/metrics`):

- `kubevirt_autopilot_asset_reconcile_total` - Total reconciliations per asset
- `kubevirt_autopilot_asset_reconcile_errors_total` - Reconciliation errors per asset
- `kubevirt_autopilot_asset_apply_total` - Successful applies per asset
- `kubevirt_autopilot_drift_detected_total` - Drift detections per asset
- `kubevirt_autopilot_throttle_delayed_total` - Reconciliations delayed by throttling

### Alerts

The autopilot fires alerts only when user intervention is required:

- **VirtPlatformAutopilotSyncFailed**: Asset reconciliation failing repeatedly
- **VirtPlatformAutopilotDependencyMissing**: Required CRD or dependency not found
- **VirtPlatformAutopilotThrashingDetected**: Excessive reconciliation indicating configuration issue
- **VirtPlatformAutopilotTombstoneStuck**: Tombstone deletion failing

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
│   ├── rbac-gen/                  # RBAC generation tool
│   └── feature-status-gen/        # Feature status table/JSON generator
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
│   │   ├── observability/         # PrometheusRules
│   │   ├── operators/             # Third-party operator CRs (UIPlugin, MetalLB, MTV…)
│   │   └── metadata.yaml          # Asset catalog
│   └── tombstones/                # Obsolete resources for deletion
├── config/                        # Kubernetes manifests for deployment
└── docs/                          # Documentation
    └── generated/                 # Auto-generated artifacts (feature-status.json)
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

### Feature Catalog

The metadata catalog also contains a `features:` section that maps user-facing features to the underlying assets and groups. This is the source of truth for the feature status table in `README.md` and the structured JSON artifact at `docs/generated/feature-status.json`.

```yaml
features:
  - name: Swap Enablement
    description: Enables OpenShift worker nodes to safely use swap for virtualization workloads; swap requires pre-provisioned dedicated storage to be available
    assets: [swap-enable]

  - name: Logging
    description: Integrated logging stack with LokiStack and ClusterLogForwarder
    maturity: tp
    groups: [logging, audit-logging]

  - name: KubeVirt Metrics Exporter
    description: Per-node VM storage I/O latency collection
    maturity: dp
    groups: [kubevirt-metrics-exporter]
```

**Feature fields:**

- `name`: Human-readable feature name (displayed in the README table)
- `description`: One-line description of the feature
- `maturity`: Explicit maturity level — `dp` (Development Preview) or `tp` (Technology Preview). Only required for opt-in features; omit for GA features (auto-derived)
- `assets`: List of individual asset names this feature comprises
- `groups`: List of asset group names this feature comprises
- `requires`: List of hard dependencies (e.g. operators) required for the feature to function
- `recommended`: Optional integrations that improve UX/visibility but are not required for core function (for example dashboards/UI plugins)

**Coverage validation:** The generator errors if any asset in the `assets:` section is not referenced by a feature entry (by name or group). Assets intentionally excluded from feature tracking (e.g. internal-only or not yet user-facing) must be listed in `excluded_assets:`:

```yaml
excluded_assets:
  - hco-golden-config
```

**Maturity derivation rules:**

| `maturity` field | Referenced assets' `install` | Derived maturity |
|---|---|---|
| `"dp"` | any | DP |
| `"tp"` | any | TP |
| omitted | all `always` | GA |
| omitted | any `opt-in` | DP (fallback) |

The opt-in annotation is automatically derived from the referenced assets' `conditions` — no duplication needed in the feature entry.

Run `make generate-feature-status` after modifying the `features:` section to regenerate the README table and JSON artifact. CI validates consistency via `make verify-feature-status`.

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

### Feature Status Generation

The feature status table in `README.md` and the structured JSON artifact at `docs/generated/feature-status.json` are auto-generated from the `features:` section in `metadata.yaml`:

```bash
# After adding/modifying features in metadata.yaml, regenerate
make generate-feature-status
```

This reads the feature catalog, resolves each feature's assets (by name or group), derives the maturity level and opt-in conditions, and carries dependency metadata (`requires` + `recommended`) into generated output:

- **`docs/generated/feature-status.json`** — structured data for CI/test consumption (e.g. verifying that all opt-in features have working annotation paths)
- **`README.md`** — markdown table injected between `<!-- BEGIN FEATURE STATUS -->` / `<!-- END FEATURE STATUS -->` sentinel comments

CI validates both outputs via `make verify-feature-status`, which diffs the committed files against a fresh generation. The generator lives at `cmd/feature-status-gen/main.go`.

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
