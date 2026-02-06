# Development Scripts

This directory contains scripts for local development and testing.

## Local Development with Kind

### Quick Start

```bash
# 1. Setup local Kind cluster with everything needed
make kind-setup

# 2. Deploy the operator
make deploy-local

# 3. View logs
make logs-local

# 4. Make code changes, then quick redeploy
make redeploy-local
```

### Container Runtime Support

The scripts automatically detect and support both Docker and Podman:

- **Docker**: Standard Kind workflow
- **Podman**: Automatically configures `KIND_EXPERIMENTAL_PROVIDER=podman`

### Available Make Targets

#### Cluster Management
```bash
make kind-setup          # Complete setup (create cluster + CRDs + mock HCO)
make kind-create         # Create cluster only
make kind-delete         # Delete cluster
make kind-status         # Check cluster status
```

#### Operator Deployment
```bash
make deploy-local        # Full deploy (build + load + deploy + wait)
make redeploy-local      # Quick redeploy (rebuild + restart)
make undeploy-local      # Remove operator
make kind-load           # Build and load image only
```

#### Debugging
```bash
make logs-local          # Tail operator logs
make status-local        # Show operator status
make debug-local         # Show all debugging info
```

#### Development Cycle
```bash
make dev-cycle           # Format + test + redeploy (fast iteration)
```

### Script Details

#### `kind-cluster.sh`

Manages Kind clusters with support for Docker and Podman.

**Usage:**
```bash
./hack/kind-cluster.sh {create|delete|list|status|setup|...}
```

**Commands:**
- `create` - Create a new Kind cluster
- `delete` - Delete the Kind cluster
- `list` - List all Kind clusters
- `status` - Check if cluster is running
- `setup` - Complete setup (create + install CRDs + mock HCO)
- `load-image <img>` - Load a container image into the cluster
- `export-kubeconfig` - Export kubeconfig for the cluster
- `install-crds` - Install CRDs into the cluster
- `create-mock-hco` - Create a mock HyperConverged instance

**Environment Variables:**
- `CLUSTER_NAME` - Name of the cluster (default: `virt-platform-operator`)
- `KIND_VERSION` - Version of Kind to install (default: `v0.20.0`)
- `KUBERNETES_VERSION` - Kubernetes version (default: `v1.28.0`)

**Examples:**
```bash
# Create cluster with custom name
CLUSTER_NAME=my-cluster ./hack/kind-cluster.sh create

# Use specific Kubernetes version
KUBERNETES_VERSION=v1.27.0 ./hack/kind-cluster.sh create

# Load custom image
./hack/kind-cluster.sh load-image myimage:latest
```

#### `deploy-local.sh`

Builds, loads, and deploys the operator to Kind cluster.

**Usage:**
```bash
./hack/deploy-local.sh {deploy|redeploy|undeploy|status|build|load}
```

**Commands:**
- `deploy` - Full deployment (build + load + deploy + wait)
- `redeploy` - Quick redeploy (rebuild + reload image + restart)
- `undeploy` - Remove operator from cluster
- `status` - Show operator status
- `build` - Build operator image only
- `load` - Load image into cluster only

**Environment Variables:**
- `CLUSTER_NAME` - Name of the cluster (default: `virt-platform-operator`)
- `IMAGE_NAME` - Operator image name (default: `virt-platform-operator:latest`)
- `NAMESPACE` - Operator namespace (default: `virt-platform-operator-system`)

**Examples:**
```bash
# Deploy with custom image name
IMAGE_NAME=my-operator:dev ./hack/deploy-local.sh deploy

# Quick rebuild and restart
./hack/deploy-local.sh redeploy
```

#### `kind-config.yaml`

Kind cluster configuration with:
- 1 control-plane node (dual-role as worker)
- 1 additional worker node
- Port mappings for accessing services (30080, 30443)
- Custom networking (pod/service subnets)

### Typical Development Workflow

1. **Initial Setup:**
   ```bash
   make kind-setup
   ```

2. **Deploy Operator:**
   ```bash
   make deploy-local
   ```

3. **Watch Logs:**
   ```bash
   make logs-local
   ```

4. **Make Code Changes:**
   ```bash
   # Edit code...
   vim pkg/controller/platform_controller.go
   ```

5. **Quick Redeploy:**
   ```bash
   make redeploy-local
   ```

6. **Check Status:**
   ```bash
   make debug-local
   ```

7. **Iterate:**
   ```bash
   make dev-cycle  # Format + test + redeploy
   ```

### Testing with Mock HCO

A mock HyperConverged instance is created in the `openshift-cnv` namespace:

```bash
# View HCO
kubectl get hyperconverged -n openshift-cnv

# Edit HCO to test operator
kubectl edit hyperconverged kubevirt-hyperconverged -n openshift-cnv

# Test user annotations
kubectl annotate hyperconverged kubevirt-hyperconverged -n openshift-cnv \
  platform.kubevirt.io/patch='[{"op":"replace","path":"/spec/liveMigrationConfig/parallelMigrationsPerCluster","value":10}]'
```

### Troubleshooting

**Cluster won't start:**
```bash
# Check Docker/Podman is running
docker info  # or: podman info

# Delete and recreate cluster
make kind-delete
make kind-setup
```

**Image not loading:**
```bash
# Verify image exists
docker images | grep virt-platform-operator

# Manually load image
make kind-load
```

**Operator not starting:**
```bash
# Check pod status
kubectl get pods -n virt-platform-operator-system

# View events
kubectl get events -n virt-platform-operator-system --sort-by='.lastTimestamp'

# Describe pod
kubectl describe pod -n virt-platform-operator-system -l app=virt-platform-operator

# Check logs
make logs-local
```

**CRDs missing:**
```bash
# Install CRDs manually
make kind-install-crds

# Verify CRDs
kubectl get crds | grep -E "hco|machine|descheduler"
```

### Clean Up

```bash
# Remove operator
make undeploy-local

# Delete cluster
make kind-delete
```
