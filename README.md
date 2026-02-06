# virt-platform-operator

The **virt-platform-operator** transforms OpenShift Virtualization from a software toolkit into a "Managed Platform Experience" by automatically managing the platform substrate required for virtualization at production scale.

## Overview

This operator embraces a **"Zero API Surface"** philosophy:
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

## User Control Annotations

Users can customize ANY managed resource (including HCO) via annotations:

### JSON Patch Override
```yaml
apiVersion: machineconfiguration.openshift.io/v1
kind: MachineConfig
metadata:
  name: 50-virt-swap-enable
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

The operator will not manage these fields, allowing manual control.

### Full Opt-Out
```yaml
metadata:
  annotations:
    platform.kubevirt.io/mode: unmanaged
```

The operator will stop managing this resource entirely.

## Getting Started

### Prerequisites

- OpenShift cluster with OpenShift Virtualization (HCO) installed
- `kubectl` or `oc` CLI access
- Go 1.25+ (for development)

### Installation

1. Build and push the operator image:
```bash
make docker-build docker-push
```

2. Deploy to cluster:
```bash
make deploy
```

3. Verify installation:
```bash
kubectl get deployment -n virt-platform-operator-system
kubectl logs -n virt-platform-operator-system deployment/virt-platform-operator-controller-manager
```

### Local Development (Kind)

For local development and testing with Kind (Kubernetes in Docker):

**Quick Start:**
```bash
# Setup local cluster with CRDs and mock HCO
make kind-setup

# Deploy operator
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
virt-platform-operator/
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
