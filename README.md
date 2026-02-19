# virt-platform-autopilot

Production-ready OpenShift Virtualization with zero manual configuration. The autopilot detects your hardware, applies best practices, and configures the platform automatically. **Convention over Configuration** for enterprise virtualization.

## The Problem

Traditional virtualization platform setup requires:
- Manual NUMA topology configuration
- GPU/PCI device passthrough setup
- High-performance networking configuration
- Load-aware workload placement policies
- Auto-remediation for node failures
- Coordinating multiple operators, MachineConfigs, and KubeletConfigs

**That's weeks of work requiring deep Kubernetes expertise.**

## The Solution

The autopilot applies opinionated best practices and manages your platform automatically:
* **Production defaults** - Optimized HCO, platform configurations, kubelet settings
* **Operational excellence** - Auto-remediation, intelligent workload placement
* **Zero API surface** - No new CRDs, no status fields to check
* **Silent operation** - Alerts only when user intervention is required
* **GitOps-friendly** - Declarative control via annotations, fully customizable
* **Convention over Configuration** - Opinionated entry point, flexible when needed

**Install once. Run VMs. Customize via GitOps when needed.**

## Quick Start

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

## How It Works

The autopilot continuously evaluates your infrastructure and applies production best practices. Configuration happens automatically based on hardware detection, platform capabilities, and operational requirements.

**For example:**
- **NUMA topology awareness** for performance optimization
- **Node auto-remediation** for reliability
- **Intelligent workload placement** for resource efficiency
- **Optimized platform defaults** for production stability
- **Swap optimization** for memory management
- **CPU management** for guaranteed workloads

The configuration adapts to your environment - if hardware capabilities are detected, appropriate optimizations are applied automatically.

## User Control

While the autopilot provides opinionated defaults, you maintain full control through standard Kubernetes annotations.

### GitOps-Friendly Customization

**JSON Patch Override** - Customize any managed resource:
```yaml
apiVersion: machineconfiguration.openshift.io/v1
kind: MachineConfig
metadata:
  name: 90-worker-swap-online
  annotations:
    platform.kubevirt.io/patch: |
      [
        {"op": "replace", "path": "/spec/config/systemd/units/0/contents", "value": "..."}
      ]
```

**Field Masking** - Exclude specific fields from management:
```yaml
apiVersion: hco.kubevirt.io/v1beta1
kind: HyperConverged
metadata:
  annotations:
    platform.kubevirt.io/ignore-fields: "/spec/liveMigrationConfig/parallelMigrationsPerCluster,/spec/featureGates"
```

**Full Opt-Out** - Stop managing a resource entirely:
```yaml
metadata:
  annotations:
    platform.kubevirt.io/mode: unmanaged
```

All customizations are declarative and version-control friendly - perfect for GitOps workflows.

For detailed control mechanisms, see the [Architecture documentation](docs/ARCHITECTURE.md).

## Architecture

The autopilot uses a **"Patched Baseline"** approach:
1. Renders opinionated defaults from templates
2. Applies user customizations via annotations
3. Detects drift and reconciles to desired state

**Key principles:**
- **Zero API Surface**: No new CRDs, no status fields to monitor
- **Silent operation**: Alerts only when user intervention is required
- **GitOps-native**: All control via standard Kubernetes annotations
- **Convention over Configuration**: Opinionated defaults, customizable when needed

**Three-Tier Management:**
1. **Always-On**: Critical baseline configurations (MachineConfig, NodeHealthCheck, Kubelet settings)
2. **Context-Aware**: Activated based on conditions (KubeDescheduler, CPU Manager)
3. **Advanced**: Specialized features (VFIO, USB passthrough, AAQ operator)

**For technical details, see:** [ARCHITECTURE.md](docs/ARCHITECTURE.md)

## Documentation

- [Architecture Deep-Dive](docs/ARCHITECTURE.md) - Technical implementation details, design philosophy, reconciliation flow
- [Adding Assets](docs/adding-assets.md) - Guide for extending the platform with new components
- [Local Development](docs/local-development.md) - Setting up dev environment with Kind
- [Lifecycle Management](docs/lifecycle-management.md) - Tombstoning and resource exclusions
- [Debug Endpoints](docs/debug-endpoints.md) - Debugging and inspection tools
- [Runbooks](docs/runbooks/) - Operational guides for alerts

## Contributing

Contributions are welcome! To add new platform components or extend the autopilot, see the [Adding Assets Guide](docs/adding-assets.md).

**Development workflow:**
1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Run tests: `make test`
5. Submit a pull request

**Development commands:**

```bash
# Build locally
make build

# Run tests
make test              # Unit tests
make test-integration  # Integration tests

# Local development with Kind
make kind-setup        # Setup local cluster
make deploy-local      # Deploy autopilot
make logs-local        # View logs
make redeploy-local    # Redeploy after changes

# Development cycle (format + test + redeploy)
make dev-cycle
```

See [Local Development Guide](docs/local-development.md) for complete instructions, including deploying custom builds to OpenShift clusters.

## License

Apache License 2.0
