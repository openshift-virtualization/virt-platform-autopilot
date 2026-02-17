# virt-platform-autopilot

The **virt-platform-autopilot** transforms OpenShift Virtualization from a software toolkit into a "Managed Platform Experience" by automatically managing the platform substrate required for virtualization at production scale.

## Overview

This autopilot embraces a **"Zero API Surface"** philosophy:
- **No new CRDs**: No custom resource definitions to manage
- **No API modifications**: No new fields added to existing APIs
- **Consistent management**: ALL resources (including HCO) managed the same way

### Key Features

- **Opinionated automation**: Applies golden reference configurations for HCO and platform components
- **HCO dual role**: Managed resource (golden config applied) + configuration source (read for rendering other assets)
- **Annotation-based control**: Users customize ANY managed resource via standard Kubernetes annotations
- **Patched Baseline algorithm**: Renders opinionated state → applies user patches → masks ignored fields → detects drift → applies via SSA
- **Graceful degradation**: Soft dependencies ensure stability even if optional components are missing

## Architecture

### Three-Tier Management

1. **Always-On** (Phase 1): Critical baseline configurations
   - NodeHealthCheck for remediation
   - MachineConfig: swap, NUMA, PCI passthrough
   - Kubelet performance settings
   - MTV, MetalLB, Observability operators

2. **Context-Aware** (Phase 1 opt-in): Activated based on conditions
   - KubeDescheduler LoadAware profile (annotation-based)
   - CPU Manager for guaranteed workloads (feature-gate)

3. **Advanced** (Phase 2/3): Specialized features
   - VFIO device assignment
   - USB passthrough
   - AAQ operator

### Reconciliation Flow

```
1. Apply golden HCO reference (with user annotations respected)
2. Read effective HCO state → Build RenderContext
3. Apply all other assets (MachineConfig, Descheduler, etc.) using RenderContext
```

### Patched Baseline Algorithm

```
For each asset:
1. Render template → Opinionated State
2. Apply user JSON patch (in-memory) → Modified State
3. Mask ignored fields from live object → Effective Desired State
4. Drift detection via SSA dry-run
5. Anti-thrashing gate (token bucket)
6. Apply via Server-Side Apply
7. Record update for throttling
```

### Controller Endpoints

The controller exposes three HTTP endpoints on separate ports:

| Port | Endpoint | Purpose | Access |
|------|----------|---------|--------|
| `8080` | `/metrics` | Prometheus metrics | Public (service) |
| `8081` | `/debug/*` | Debug/render endpoints | Localhost only |
| `8082` | `/healthz`, `/readyz` | Health probes | Kubernetes probes |

**Debug Endpoints** (localhost-only, access via port-forward):
- `/debug/render` - Render all assets based on current HCO
- `/debug/render/{asset}` - Render specific asset
- `/debug/exclusions` - List excluded/filtered assets with reasons
- `/debug/tombstones` - List tombstones
- `/debug/health` - Health check

See [Debug Endpoints Documentation](docs/debug-endpoints.md) for detailed usage.

**Render Command** (offline CLI):
```bash
# Render assets offline without cluster
virt-platform-autopilot render --hco-file=hco.yaml --output=status

# Or use HCO from cluster
virt-platform-autopilot render --kubeconfig=/path/to/config
```

## User Control Annotations

Users can customize ANY managed resource (including HCO) via annotations:

### JSON Patch Override
```yaml
apiVersion: machineconfiguration.openshift.io/v1
kind: MachineConfig
metadata:
  name: 99-kubevirt-swap-optimization
  annotations:
    platform.kubevirt.io/patch: |
      [
        {"op": "replace", "path": "/spec/config/systemd/units/0/contents", "value": "..."}
      ]
```

### Field Masking (Loose Ownership)
```yaml
apiVersion: hco.kubevirt.io/v1beta1
kind: HyperConverged
metadata:
  annotations:
    platform.kubevirt.io/ignore-fields: "/spec/liveMigrationConfig/parallelMigrationsPerCluster,/spec/featureGates"
```

These fields will not be managed, allowing manual control.

### Full Opt-Out
```yaml
metadata:
  annotations:
    platform.kubevirt.io/mode: unmanaged
```

This resource will not be managed entirely.

## Resource Lifecycle Management

The autopilot provides two mechanisms for managing resource lifecycle during upgrades:

### Tombstoning

Safely delete obsolete resources when features are removed or resources are renamed:

```bash
# Move obsolete resource to tombstones directory
git mv assets/active/config/old-resource.yaml assets/tombstones/v1.1-cleanup/

# On next reconciliation, operator will delete the resource
# (only if it has the platform.kubevirt.io/managed-by label)
```

**Safety features:**
- Label verification prevents accidental deletion
- Best-effort execution (continues even if some deletions fail)
- Idempotent (already-deleted resources are skipped)

### Root Exclusion

Prevent specific resources from being created:

```yaml
apiVersion: hco.kubevirt.io/v1beta1
kind: HyperConverged
metadata:
  annotations:
    platform.kubevirt.io/disabled-resources: "KubeDescheduler/cluster, MachineConfig/50-swap-enable"
```

**Use cases:**
- Disable features not needed in specific deployments
- Temporary workarounds for known issues
- Prevent resource creation in environments where it would fail

For detailed documentation, see: [Resource Lifecycle Management](docs/LIFECYCLE_MANAGEMENT.md)

## Getting Started

### Prerequisites

- OpenShift cluster with OpenShift Virtualization (HCO) installed
- `kubectl` or `oc` CLI access
- Go 1.25+ (for development)

### Installation

1. Build and push the image:
```bash
make docker-build docker-push
```

2. Deploy to cluster:
```bash
make deploy
```

3. Verify installation:
```bash
kubectl get deployment -n openshift-cnv
kubectl logs -n openshift-cnv deployment/virt-platform-autopilot
```

### Local Development (Kind)

For local development and testing with Kind (Kubernetes in Docker):

**Quick Start:**
```bash
# Setup local cluster with CRDs and mock HCO
make kind-setup

# Deploy autopilot
make deploy-local

# View logs
make logs-local

# Make changes and redeploy
make redeploy-local
```

**Supports both Docker and Podman** - automatically detected.

See [docs/local-development.md](docs/local-development.md) for complete guide.

### Development Commands

Build locally:
```bash
make build
```

Run tests:
```bash
make test              # Unit tests
make test-integration  # Integration tests with envtest
```

Run locally (requires cluster access):
```bash
make run
```

Development cycle (format + test + redeploy):
```bash
make dev-cycle
```

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
│   ├── overrides/                 # User override logic
│   ├── throttling/                # Anti-thrashing protection
│   └── util/                      # Utilities
├── assets/                        # Embedded asset templates
│   ├── hco/                       # Golden HCO reference (reconcile_order: 0)
│   ├── machine-config/            # OS-level configs
│   ├── kubelet/                   # Kubelet settings
│   ├── descheduler/               # KubeDescheduler
│   ├── node-health/               # NodeHealthCheck
│   ├── operators/                 # Third-party operator CRs
│   └── metadata.yaml              # Asset catalog
├── config/                        # Kubernetes manifests
└── Makefile
```

## Asset Catalog

See [docs/assets.md](docs/assets.md) for complete asset reference.

## Contributing

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Run tests: `make test`
5. Submit a pull request

## License

Apache License 2.0
