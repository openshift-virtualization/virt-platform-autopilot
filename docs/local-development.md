# Local Development Guide

This guide covers setting up a local development environment for virt-platform-autopilot using Kind (Kubernetes in Docker).

## Prerequisites

- Go 1.22 or later
- Docker or Podman
- kubectl
- make

## Quick Start

### 1. Clone and Setup

```bash
cd virt-platform-autopilot
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
kubectl label node kind-virt-platform-autopilot-worker feature.node.kubernetes.io/pci-present=true

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
kubectl get pods -n openshift-cnv

# Describe the pod
kubectl describe pod -n openshift-cnv -l app=virt-platform-autopilot

# Check events
kubectl get events -n openshift-cnv --sort-by='.lastTimestamp'

# View logs
make logs-local
```

### Image Not Loading

```bash
# Verify image exists locally
docker images | grep virt-platform-autopilot
# or
podman images | grep virt-platform-autopilot

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
kubectl cluster-info --context kind-virt-platform-autopilot

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
kind export kubeconfig --name virt-platform-autopilot

# Or use context directly
kubectl --context kind-virt-platform-autopilot get nodes
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
   - Terminal 3: `kubectl get pods -n openshift-cnv -w` (watch pods)

5. **Resource Cleanup**: Kind clusters are ephemeral - don't worry about breaking things

6. **Podman Users**: If you encounter issues, ensure podman socket is running:
   ```bash
   systemctl --user enable --now podman.socket
   ```

## Deploying to OpenShift Cluster

For testing on a real OpenShift cluster instead of Kind.

### Prerequisites

- Access to an OpenShift cluster (kubeconfig configured)
- Container registry access (e.g., quay.io, docker.io)
- Registry credentials configured locally
- OpenShift Virtualization (HCO) installed on the cluster

### Build and Push Custom Image

```bash
# Set your image repository and tag
export IMG=quay.io/YOUR_USERNAME/virt-platform-autopilot:v1.0.0-dev

# Build the image
make docker-build

# Tag for your registry
docker tag virt-platform-autopilot:latest ${IMG}

# Login to your registry (if needed)
docker login quay.io

# Push to registry
docker push ${IMG}
```

**Using Podman:**

```bash
export IMG=quay.io/YOUR_USERNAME/virt-platform-autopilot:v1.0.0-dev
make docker-build CONTAINER_TOOL=podman
podman tag virt-platform-autopilot:latest ${IMG}
podman login quay.io
podman push ${IMG}
```

### Deploy to OpenShift

#### Option 1: Using Kustomize (Recommended)

Update the image reference and deploy:

```bash
# Navigate to manager config
cd config/manager

# Set the image (updates kustomization.yaml)
kustomize edit set image virt-platform-autopilot=${IMG}

# Return to project root
cd ../..

# Deploy to cluster
oc apply -k config/default

# Verify deployment
oc get deployment -n openshift-cnv
oc get pods -n openshift-cnv -l app=virt-platform-autopilot
```

#### Option 2: Direct Deployment with Image Override

Deploy without modifying files:

```bash
# Deploy base manifests
oc apply -k config/default

# Update the image after deployment
oc set image deployment/virt-platform-autopilot-virt-platform-autopilot \
  manager=${IMG} -n openshift-cnv

# Watch rollout
oc rollout status deployment/virt-platform-autopilot-virt-platform-autopilot -n openshift-cnv
```

#### Option 3: Manual Edit

```bash
# Edit the deployment directly
oc edit deployment virt-platform-autopilot-virt-platform-autopilot -n openshift-cnv

# Update the image field:
# spec.template.spec.containers[0].image: quay.io/YOUR_USERNAME/virt-platform-autopilot:v1.0.0-dev
```

### Verify Deployment

```bash
# Check pod status
oc get pods -n openshift-cnv

# View logs
oc logs -f -n openshift-cnv -l app=virt-platform-autopilot

# Check if HCO is being reconciled
oc get hyperconverged -n openshift-cnv -o yaml

# View managed resources
oc get machineconfigs | grep kubevirt
oc get kubeletconfigs | grep virt
oc get nodehealthchecks
```

### Update After Code Changes

When you make code changes and want to redeploy:

```bash
# Rebuild and push with new tag
export IMG=quay.io/YOUR_USERNAME/virt-platform-autopilot:v1.0.0-dev-$(date +%Y%m%d-%H%M%S)
make docker-build
docker tag virt-platform-autopilot:latest ${IMG}
docker push ${IMG}

# Update deployment
oc set image deployment/virt-platform-autopilot-virt-platform-autopilot \
  manager=${IMG} -n openshift-cnv

# Force restart if using same tag
oc rollout restart deployment/virt-platform-autopilot-virt-platform-autopilot -n openshift-cnv

# Watch the rollout
oc rollout status deployment/virt-platform-autopilot-virt-platform-autopilot -n openshift-cnv

# Check logs
oc logs -f -n openshift-cnv -l app=virt-platform-autopilot
```

### Using Debug Endpoints on OpenShift

Access debug endpoints via port-forward:

```bash
# Port-forward to debug server
oc port-forward -n openshift-cnv \
  deployment/virt-platform-autopilot-virt-platform-autopilot 8081:8081

# In another terminal, access debug endpoints
curl http://localhost:8081/debug/render
curl http://localhost:8081/debug/render?format=json | jq '.'
curl http://localhost:8081/debug/exclusions
curl http://localhost:8081/debug/tombstones
```

### Troubleshooting OpenShift Deployment

#### Image Pull Errors

```bash
# Check image pull status
oc describe pod -n openshift-cnv -l app=virt-platform-autopilot

# Verify image exists in registry
docker pull ${IMG}

# Check if registry is public or needs credentials
# For private registries, create image pull secret:
oc create secret docker-registry quay-secret \
  --docker-server=quay.io \
  --docker-username=YOUR_USERNAME \
  --docker-password=YOUR_PASSWORD \
  -n openshift-cnv

# Patch service account to use the secret
oc patch serviceaccount virt-platform-autopilot-virt-platform-autopilot \
  -n openshift-cnv \
  -p '{"imagePullSecrets": [{"name": "quay-secret"}]}'
```

#### RBAC Permissions

```bash
# Check if ClusterRole is created
oc get clusterrole | grep virt-platform-autopilot

# Check RoleBinding
oc get clusterrolebinding | grep virt-platform-autopilot

# View RBAC permissions
oc describe clusterrole virt-platform-autopilot-virt-platform-autopilot-role

# Test permissions as the service account
oc auth can-i create machineconfigs \
  --as=system:serviceaccount:openshift-cnv:virt-platform-autopilot-virt-platform-autopilot
```

#### Pod Not Starting

```bash
# Check pod events
oc describe pod -n openshift-cnv -l app=virt-platform-autopilot

# Check pod logs (even if not running)
oc logs -n openshift-cnv -l app=virt-platform-autopilot --previous

# Check SecurityContextConstraints (OCP-specific)
oc get pod -n openshift-cnv -l app=virt-platform-autopilot -o yaml | grep scc

# Verify namespace exists
oc get namespace openshift-cnv
```

#### Operator Not Reconciling

```bash
# Check if HCO exists
oc get hyperconverged -n openshift-cnv

# Check operator logs for errors
oc logs -n openshift-cnv -l app=virt-platform-autopilot | grep -i error

# Check leader election
oc logs -n openshift-cnv -l app=virt-platform-autopilot | grep "leader"

# Verify operator is watching the right namespace
oc logs -n openshift-cnv -l app=virt-platform-autopilot | head -20
```

### Clean Up

Remove the operator from the cluster:

```bash
# Delete all resources
oc delete -k config/default

# Or manually
oc delete deployment virt-platform-autopilot-virt-platform-autopilot -n openshift-cnv
oc delete clusterrolebinding virt-platform-autopilot-virt-platform-autopilot-rolebinding
oc delete clusterrole virt-platform-autopilot-virt-platform-autopilot-role
oc delete serviceaccount virt-platform-autopilot-virt-platform-autopilot -n openshift-cnv

# Managed resources will remain - delete manually if needed
oc get machineconfigs | grep kubevirt
oc get nodehealthchecks
```

### Tips for OpenShift Development

1. **Use timestamped tags** to avoid caching issues:
   ```bash
   export IMG=quay.io/YOUR_USERNAME/virt-platform-autopilot:dev-$(date +%Y%m%d-%H%M%S)
   ```

2. **Set ImagePullPolicy to Always** for development:
   ```bash
   oc patch deployment virt-platform-autopilot-virt-platform-autopilot \
     -n openshift-cnv \
     --type='json' \
     -p='[{"op": "replace", "path": "/spec/template/spec/containers/0/imagePullPolicy", "value": "Always"}]'
   ```

3. **Watch multiple resources** during development:
   ```bash
   watch 'oc get pods,machineconfigs,nodehealthchecks -n openshift-cnv'
   ```

4. **Keep logs in a separate terminal**:
   ```bash
   oc logs -f -n openshift-cnv -l app=virt-platform-autopilot --tail=100
   ```

5. **Test on non-production clusters** to avoid impacting real workloads

6. **Use private registry namespaces** to avoid conflicts with official images

## Next Steps

- Read [README](../README.md) for overview and quick start
- Read [Adding Assets Guide](adding-assets.md) for extending the platform
- Read [Architecture Documentation](ARCHITECTURE.md) for technical details
- Read [Debug Endpoints](debug-endpoints.md) for debugging and inspection tools
