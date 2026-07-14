# virt-platform-autopilot

Production-ready OpenShift Virtualization with zero manual configuration. The autopilot detects your hardware, cluster topology, applies best practices, and configures the platform automatically. **Convention over Configuration** for enterprise virtualization.

## Issue

Traditional virtualization platform setup requires:
- Manual NUMA topology configuration
- GPU/PCI device passthrough setup
- High-performance networking configuration
- Load-aware workload placement policies
- Auto-remediation for node failures
- Coordinating multiple operators, MachineConfigs, and KubeletConfigs

That's weeks of work requiring deep Kubernetes expertise.

## Solution

The autopilot applies opinionated best practices and manages your platform automatically:
* **Production defaults** - Optimized HCO, platform configurations, kubelet settings
* **Operational excellence** - Auto-remediation, intelligent workload placement
* **Zero API surface** - No new CRDs, no status fields to check
* **Silent operation** - Alerts only when user intervention is required
* **GitOps-friendly** - Declarative control via annotations, fully customizable
* **Convention over Configuration** - Opinionated entry point, flexible when needed

Install once. Run VMs. Customize via GitOps when needed.

## Features

### Maturity Levels

Autopilot has three maturity levels for features

| Level                    | Default?      | Description                                |
|--------------------------|---------------|--------------------------------------------|
| Development Preview (DP) | No (opt-in)   | The feature is available as opt-in, with few documentation, for early adopters.
| Technology Preview (TP)  | No (opt-in)   | The feature is avaialble as opt-in, with documentation, on track to GA.
| General Availability     | Yes (opt-out) | The feature is available by default, an admin can decide to opt-out.

### Status

| Feature                | DP Version   | TP Version   | GA Version  |
|------------------------|--------------|--------------|-------------|
| OpenShift SWAP         | 4.22         | 5.0          |             |
| FAR + SBR              | 5.0          |              |             |
| Graceful Node Shutdown | 4.22         |              |             |

## Quick Start

### Prerequisites

- OpenShift cluster with OpenShift Virtualization (HCO) installed
- `kubectl` or `oc` CLI access
- Go 1.26+ (for development)

### Installation

1. Build and push the image:
```bash
make docker-build docker-push
```

2. Deploy to cluster:
```bash
make deploy
```

3. **Enable the autopilot** (opt-in required in this early phase):

   ```bash
   kubectl annotate hyperconverged kubevirt-hyperconverged -n openshift-cnv \
     platform.kubevirt.io/autopilot=true
   ```

   > **Note:** The autopilot is currently **disabled by default**. It will not manage any resources until the opt-in annotation `platform.kubevirt.io/autopilot: "true"` is present on the HCO CR. This allows safe evaluation before committing to automated management. Once the project matures, the default will be inverted to opt-out.

4. Verify installation:
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
- **Perses monitoring dashboards** for CNV observability via the OpenShift console (when the Cluster Observability Operator is installed)

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
1. **Always-On**: Critical baseline configurations (MachineConfig, Kubelet settings)
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
make test                  # Unit tests
make test-integration      # Integration tests

# Local development with Kind
make kind-setup            # Setup local cluster
make deploy-local          # Deploy autopilot
make logs-local            # View logs
make redeploy-local        # Redeploy after changes

# Development cycle (format + test + redeploy)
make dev-cycle
```

See [Local Development Guide](docs/local-development.md) for complete instructions, including deploying custom builds to OpenShift clusters.

## License

Apache License 2.0
