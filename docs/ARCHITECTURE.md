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

- **KubeDescheduler**: LoadAware profile for intelligent workload balancing
  - Activated via `platform.kubevirt.io/enable-descheduler: "true"` annotation
  - Balances VM workloads across cluster nodes
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

1. **Managed resource**: The autopilot applies opinionated golden configurations to HCO
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

Users have three levels of control over managed resources:

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

- `virt_platform_asset_reconcile_total` - Total reconciliations per asset
- `virt_platform_asset_reconcile_errors_total` - Reconciliation errors per asset
- `virt_platform_asset_apply_total` - Successful applies per asset
- `virt_platform_drift_detected_total` - Drift detections per asset
- `virt_platform_throttle_delayed_total` - Reconciliations delayed by throttling

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
    file: hco/hyperconverged.yaml
    phase: 1
    install: always
    component: core
    reconcile_order: 0  # HCO must be first

  - name: kubevirt-swap-optimization
    file: machine-config/99-kubevirt-swap.yaml
    phase: 1
    install: always
    component: machine-config
    reconcile_order: 10

  - name: descheduler-loadaware
    file: descheduler/kubedescheduler.yaml
    phase: 1
    install: opt-in
    component: scheduling
    reconcile_order: 20
    conditions:
      annotations:
        - key: platform.kubevirt.io/enable-descheduler
          value: "true"
```

**Metadata fields:**

- `name`: Unique asset identifier
- `file`: Template file path relative to `assets/active/`
- `phase`: Rollout phase (1=GA, 2=Tech Preview, 3=Experimental)
- `install`: `always` or `opt-in` (requires condition)
- `component`: Logical grouping for organization
- `reconcile_order`: Processing order (lower = earlier)
- `conditions`: Activation conditions (annotations, hardware, feature gates)

### Soft Dependencies

The autopilot gracefully handles missing CRDs and dependencies:

```go
// Example: Template checks if CRD exists
{{- if .ClusterCapabilities.HasCRD "kubedeschedulers.descheduler.kubevirt.io" }}
apiVersion: descheduler.kubevirt.io/v1
kind: KubeDescheduler
# ... resource spec
{{- end }}
```

If a CRD is not installed:
- Asset is skipped during rendering
- No error is raised
- Alert fires if dependency is expected but missing
- Automatic retry when dependency becomes available

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
