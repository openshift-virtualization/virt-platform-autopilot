#!/usr/bin/env bash
#
# Kind cluster management script for virt-platform-operator
#
# This script works with both Docker and Podman out of the box.
# No special configuration or workarounds needed - Kind v1.34.0
# works perfectly with Podman on Fedora/RHEL with SELinux enforcing.
#

set -e

CLUSTER_NAME="${CLUSTER_NAME:-virt-platform-operator}"
KIND_VERSION="${KIND_VERSION:-v0.31.0}"
KUBERNETES_VERSION="${KUBERNETES_VERSION:-v1.35.0}"

# Detect container runtime (docker or podman)
detect_runtime() {
    if command -v docker &> /dev/null && docker info &> /dev/null; then
        echo "docker"
    elif command -v podman &> /dev/null; then
        echo "podman"
    else
        echo "ERROR: Neither docker nor podman found" >&2
        exit 1
    fi
}

CONTAINER_RUNTIME=$(detect_runtime)
echo "Using container runtime: $CONTAINER_RUNTIME"

# Configure Kind to use podman if applicable
if [ "$CONTAINER_RUNTIME" = "podman" ]; then
    export KIND_EXPERIMENTAL_PROVIDER=podman
    echo "Configured Kind to use podman"
fi

# Function to check if kind is installed
check_kind() {
    if ! command -v kind &> /dev/null; then
        echo "kind not found. Installing kind $KIND_VERSION..."
        install_kind
    else
        echo "kind is already installed: $(kind version)"
    fi
}

# Function to install kind
install_kind() {
    local os=$(uname -s | tr '[:upper:]' '[:lower:]')
    local arch=$(uname -m)

    case $arch in
        x86_64)
            arch="amd64"
            ;;
        aarch64|arm64)
            arch="arm64"
            ;;
        *)
            echo "Unsupported architecture: $arch"
            exit 1
            ;;
    esac

    local kind_url="https://kind.sigs.k8s.io/dl/${KIND_VERSION}/kind-${os}-${arch}"

    echo "Downloading kind from $kind_url"
    curl -Lo ./kind "$kind_url"
    chmod +x ./kind
    sudo mv ./kind /usr/local/bin/kind

    echo "kind installed successfully"
}

# Function to create the cluster
create_cluster() {
    echo "Creating Kind cluster: $CLUSTER_NAME"

    kind create cluster \
        --name "$CLUSTER_NAME" \
        --image "kindest/node:${KUBERNETES_VERSION}"

    echo "Cluster created successfully"
    echo "Setting kubectl context to kind-$CLUSTER_NAME"
    kubectl cluster-info --context "kind-$CLUSTER_NAME"
}

# Function to delete the cluster
delete_cluster() {
    echo "Deleting Kind cluster: $CLUSTER_NAME"
    kind delete cluster --name "$CLUSTER_NAME"
    echo "Cluster deleted successfully"
}

# Function to list clusters
list_clusters() {
    echo "Available Kind clusters:"
    kind get clusters
}

# Function to load images into the cluster
load_image() {
    local image=$1
    echo "Loading image $image into cluster $CLUSTER_NAME"
    kind load docker-image "$image" --name "$CLUSTER_NAME"
    echo "Image loaded successfully"
}

# Function to export kubeconfig
export_kubeconfig() {
    local output_file="${1:-./kubeconfig}"
    kind export kubeconfig --name "$CLUSTER_NAME" --kubeconfig "$output_file"
    echo "Kubeconfig exported to $output_file"
}

# Function to check cluster status
status() {
    if kind get clusters 2>/dev/null | grep -q "^${CLUSTER_NAME}$"; then
        echo "Cluster '$CLUSTER_NAME' is running"
        kubectl cluster-info --context "kind-$CLUSTER_NAME"
        return 0
    else
        echo "Cluster '$CLUSTER_NAME' is not running"
        return 1
    fi
}

# Function to install CRDs (for HCO and other dependencies)
install_crds() {
    echo "Installing CRDs from assets/crds/"

    if [ ! -d "assets/crds" ]; then
        echo "ERROR: CRD directory not found. Run 'make update-crds' first."
        exit 1
    fi

    # Install all CRDs from the crds directory
    for crd_file in assets/crds/**/*.yaml; do
        if [ -f "$crd_file" ] && [ "$(basename "$crd_file")" != "README.md" ]; then
            echo "Installing CRD: $crd_file"
            kubectl apply -f "$crd_file" --context "kind-$CLUSTER_NAME" || true
        fi
    done

    echo "CRDs installed"
}

# Function to create a mock HCO instance
create_mock_hco() {
    echo "Creating mock HyperConverged instance for testing"

    cat <<EOF | kubectl apply --context "kind-$CLUSTER_NAME" -f -
apiVersion: v1
kind: Namespace
metadata:
  name: openshift-cnv
---
apiVersion: hco.kubevirt.io/v1beta1
kind: HyperConverged
metadata:
  name: kubevirt-hyperconverged
  namespace: openshift-cnv
spec:
  liveMigrationConfig:
    parallelMigrationsPerCluster: 5
    parallelOutboundMigrationsPerNode: 2
EOF

    echo "Mock HCO created in openshift-cnv namespace"
}

# Main script logic
case "${1:-}" in
    create)
        check_kind
        create_cluster
        ;;
    delete)
        delete_cluster
        ;;
    list)
        list_clusters
        ;;
    status)
        status
        ;;
    load-image)
        if [ -z "${2:-}" ]; then
            echo "Usage: $0 load-image <image-name>"
            exit 1
        fi
        load_image "$2"
        ;;
    export-kubeconfig)
        export_kubeconfig "${2:-./kubeconfig}"
        ;;
    install-crds)
        install_crds
        ;;
    create-mock-hco)
        create_mock_hco
        ;;
    setup)
        check_kind
        create_cluster
        install_crds
        create_mock_hco
        echo ""
        echo "✓ Kind cluster setup complete!"
        echo "  Cluster name: $CLUSTER_NAME"
        echo "  Context: kind-$CLUSTER_NAME"
        echo ""
        echo "Next steps:"
        echo "  1. Build and load the operator image: make kind-load"
        echo "  2. Deploy the operator: make deploy-local"
        echo "  3. View logs: make logs-local"
        ;;
    *)
        echo "Usage: $0 {create|delete|list|status|setup|load-image|export-kubeconfig|install-crds|create-mock-hco}"
        echo ""
        echo "Commands:"
        echo "  create              Create a new Kind cluster"
        echo "  delete              Delete the Kind cluster"
        echo "  list                List all Kind clusters"
        echo "  status              Check if cluster is running"
        echo "  setup               Complete setup (create + install CRDs + mock HCO)"
        echo "  load-image <img>    Load a container image into the cluster"
        echo "  export-kubeconfig   Export kubeconfig for the cluster"
        echo "  install-crds        Install CRDs into the cluster"
        echo "  create-mock-hco     Create a mock HyperConverged instance"
        echo ""
        echo "Environment variables:"
        echo "  CLUSTER_NAME        Name of the cluster (default: virt-platform-operator)"
        echo "  KIND_VERSION        Version of Kind to install (default: v0.20.0)"
        echo "  KUBERNETES_VERSION  Kubernetes version (default: v1.28.0)"
        exit 1
        ;;
esac
