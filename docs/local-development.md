# Local Development Guide

This guide covers setting up a local development environment for virt-platform-operator using Kind (Kubernetes in Docker).

## Prerequisites

- Go 1.22 or later
- Docker or Podman
- kubectl
- make

## Quick Start

### 1. Clone and Setup

```bash
cd virt-platform-operator
go mod tidy
```

### 2. Create Local Cluster

The easiest way is to use the all-in-one setup command:

```bash
make kind-setup
```

This will:
- Install Kind (if needed)
- Create a local Kubernetes cluster
- Install required CRDs
- Create a mock HyperConverged instance

### 3. Deploy the Operator

```bash
make deploy-local
```

This will:
- Build the operator image
- Load it into the Kind cluster
- Deploy the operator
- Wait for it to be ready

### 4. View Logs

```bash
make logs-local
```

### 5. Make Changes and Test

```bash
# Edit code
vim pkg/controller/platform_controller.go

# Quick rebuild and redeploy
make redeploy-local

# Or use the full dev cycle (format + test + redeploy)
make dev-cycle
```

## Container Runtime Support

The scripts automatically detect and support both Docker and Podman:

### Using Docker (Default)
```bash
make kind-setup
make deploy-local
```

### Using Podman
```bash
# Podman is automatically detected
# Just run the same commands
make kind-setup
make deploy-local
```

The scripts will:
- Detect which runtime is available
- Configure Kind appropriately (with `KIND_EXPERIMENTAL_PROVIDER=podman` for Podman)
- Handle image loading correctly for each runtime

## Available Commands

### Cluster Management

```bash
make kind-setup          # Complete setup (recommended for first time)
make kind-create         # Create cluster only
make kind-delete         # Delete cluster
make kind-status         # Check if cluster is running
```

### Operator Deployment

```bash
make deploy-local        # Full deploy (build + load + deploy + wait)
make redeploy-local      # Quick redeploy (rebuild + restart pods)
make undeploy-local      # Remove operator from cluster
make kind-load           # Build and load image only
```

### Debugging and Development

```bash
make logs-local          # Tail operator logs
make status-local        # Show operator status
make debug-local         # Show comprehensive debug info
make dev-cycle           # Format + test + redeploy (fast iteration)
```

## Typical Development Workflow

### Initial Setup (Once)

```bash
# 1. Setup cluster
make kind-setup

# 2. Deploy operator
make deploy-local

# 3. Watch it run
make logs-local
```

### Development Iteration

```bash
# 1. Make code changes
vim pkg/...

# 2. Quick redeploy
make redeploy-local

# 3. Check logs
make logs-local
```

### Testing User Overrides

The operator supports three annotation-based override mechanisms. Test them on the mock HCO:

#### 1. JSON Patch Override

```bash
# Apply a JSON patch to override specific fields
kubectl annotate hyperconverged kubevirt-hyperconverged -n openshift-cnv \
  'platform.kubevirt.io/patch=[{"op":"replace","path":"/spec/liveMigrationConfig/parallelMigrationsPerCluster","value":10}]'

# Watch the operator reconcile
make logs-local

# Verify the change
kubectl get hyperconverged kubevirt-hyperconverged -n openshift-cnv -o yaml | grep parallelMigrationsPerCluster
```

#### 2. Field Masking (Ignore Fields)

```bash
# Tell operator to not manage certain fields
kubectl annotate hyperconverged kubevirt-hyperconverged -n openshift-cnv \
  'platform.kubevirt.io/ignore-fields=/spec/featureGates,/spec/liveMigrationConfig/parallelMigrationsPerCluster'

# Manually edit those fields
kubectl edit hyperconverged kubevirt-hyperconverged -n openshift-cnv

# Operator won't revert your changes
make logs-local
```

#### 3. Full Opt-Out

```bash
# Completely disable operator management
kubectl annotate hyperconverged kubevirt-hyperconverged -n openshift-cnv \
  'platform.kubevirt.io/mode=unmanaged'

# Operator will stop reconciling this resource
make logs-local
```

## Inspecting Managed Resources

The operator creates and manages various resources:

```bash
# View all managed resources
make debug-local

# Check specific resources
kubectl get hyperconverged -n openshift-cnv
kubectl get machineconfigs
kubectl get kubeletconfigs
kubectl get nodehealthchecks
kubectl get kubedeschedulers
```

## Testing Asset Conditions

### Hardware Detection

The operator detects hardware capabilities and conditionally applies assets:

```bash
# Label a node to simulate GPU presence
kubectl label node kind-virt-platform-operator-worker feature.node.kubernetes.io/pci-present=true

# Watch operator reconcile PCI passthrough MachineConfig
make logs-local

# Check if asset was created
kubectl get machineconfigs | grep pci-passthrough
```

### Feature Gates

Test opt-in features:

```bash
# Enable LoadAware descheduler via annotation
kubectl annotate hyperconverged kubevirt-hyperconverged -n openshift-cnv \
  'platform.kubevirt.io/enable-loadaware=true'

# Watch operator create KubeDescheduler
make logs-local

# Verify
kubectl get kubedeschedulers
```

## Debugging Issues

### Operator Not Starting

```bash
# Check pod status
kubectl get pods -n virt-platform-operator-system

# Describe the pod
kubectl describe pod -n virt-platform-operator-system -l app=virt-platform-operator

# Check events
kubectl get events -n virt-platform-operator-system --sort-by='.lastTimestamp'

# View logs
make logs-local
```

### Image Not Loading

```bash
# Verify image exists locally
docker images | grep virt-platform-operator
# or
podman images | grep virt-platform-operator

# Rebuild and reload
make kind-load
```

### CRDs Missing

```bash
# Install CRDs
make kind-install-crds

# Verify
kubectl get crds | grep -E "hyperconverged|machineconfig|nodehealthcheck"
```

### Cluster Issues

```bash
# Check cluster status
make kind-status

# View cluster info
kubectl cluster-info --context kind-virt-platform-operator

# If cluster is broken, recreate it
make kind-delete
make kind-setup
```

## Advanced Usage

### Custom Cluster Name

```bash
# Create with custom name
CLUSTER_NAME=my-dev-cluster make kind-setup

# Deploy to custom cluster
CLUSTER_NAME=my-dev-cluster make deploy-local

# Use with any command
CLUSTER_NAME=my-dev-cluster make logs-local
```

### Custom Image Name

```bash
# Build with custom tag
IMAGE_NAME=my-operator:dev make deploy-local
```

### Multiple Clusters

```bash
# Create development cluster
CLUSTER_NAME=virt-platform-dev make kind-setup
CLUSTER_NAME=virt-platform-dev make deploy-local

# Create testing cluster
CLUSTER_NAME=virt-platform-test make kind-setup
CLUSTER_NAME=virt-platform-test make deploy-local

# Switch between them
kubectl config use-context kind-virt-platform-dev
kubectl config use-context kind-virt-platform-test
```

### Direct Access to Kind Cluster

```bash
# Get kubeconfig
kind export kubeconfig --name virt-platform-operator

# Or use context directly
kubectl --context kind-virt-platform-operator get nodes
```

## Clean Up

### Remove Operator Only

```bash
make undeploy-local
```

### Delete Cluster

```bash
make kind-delete
```

### Complete Clean Up

```bash
make undeploy-local
make kind-delete
```

## Tips and Best Practices

1. **Fast Iteration**: Use `make redeploy-local` instead of `make deploy-local` after code changes (much faster)

2. **Watch Logs**: Keep a terminal with `make logs-local` running while developing

3. **Use dev-cycle**: `make dev-cycle` runs format, tests, and redeploys in one command

4. **Multiple Terminals**: Run these in separate terminals:
   - Terminal 1: `make logs-local` (watch logs)
   - Terminal 2: Code editing and `make redeploy-local`
   - Terminal 3: `kubectl get pods -n virt-platform-operator-system -w` (watch pods)

5. **Resource Cleanup**: Kind clusters are ephemeral - don't worry about breaking things

6. **Podman Users**: If you encounter issues, ensure podman socket is running:
   ```bash
   systemctl --user enable --now podman.socket
   ```

## Next Steps

- Read [User Guide](user-guide.md) for annotation usage
- Read [Assets Documentation](assets.md) for asset catalog reference
- Check [hack/README.md](../hack/README.md) for detailed script documentation
