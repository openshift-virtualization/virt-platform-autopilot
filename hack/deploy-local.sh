#!/usr/bin/env bash

set -e

CLUSTER_NAME="${CLUSTER_NAME:-virt-platform-operator}"
IMAGE_NAME="${IMAGE_NAME:-virt-platform-operator:latest}"
NAMESPACE="${NAMESPACE:-virt-platform-operator-system}"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

log_info() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# Check if cluster exists
check_cluster() {
    if ! kind get clusters 2>/dev/null | grep -q "^${CLUSTER_NAME}$"; then
        log_error "Cluster '$CLUSTER_NAME' not found"
        log_info "Run 'make kind-setup' to create the cluster"
        exit 1
    fi
    log_info "Using cluster: $CLUSTER_NAME"
}

# Detect container runtime
detect_runtime() {
    if command -v docker &> /dev/null && docker info &> /dev/null; then
        echo "docker"
    elif command -v podman &> /dev/null; then
        echo "podman"
    else
        log_error "Neither docker nor podman found"
        exit 1
    fi
}

# Build the operator image
build_image() {
    local runtime=$(detect_runtime)
    log_info "Building operator image with $runtime: $IMAGE_NAME"

    if [ "$runtime" = "podman" ]; then
        podman build -t "$IMAGE_NAME" .
    else
        docker build -t "$IMAGE_NAME" .
    fi

    log_info "Image built successfully"
}

# Load image into Kind cluster
load_image() {
    log_info "Loading image into Kind cluster"

    # Kind uses docker for loading images, even with podman
    # If using podman, we need to save and load the image
    if [ "$(detect_runtime)" = "podman" ]; then
        log_info "Saving image from podman and loading into kind"
        # Podman prefixes images with localhost/, we need to handle both variants
        local podman_image="localhost/$IMAGE_NAME"
        if podman image exists "$podman_image"; then
            podman save "$podman_image" | kind load image-archive /dev/stdin --name "$CLUSTER_NAME"
        else
            podman save "$IMAGE_NAME" | kind load image-archive /dev/stdin --name "$CLUSTER_NAME"
        fi
    else
        kind load docker-image "$IMAGE_NAME" --name "$CLUSTER_NAME"
    fi

    log_info "Image loaded successfully"
}

# Create namespace
create_namespace() {
    log_info "Creating namespace: $NAMESPACE"
    kubectl create namespace "$NAMESPACE" \
        --context "kind-$CLUSTER_NAME" \
        --dry-run=client -o yaml | kubectl apply --context "kind-$CLUSTER_NAME" -f -
}

# Deploy operator manifests
deploy_operator() {
    log_info "Deploying operator manifests"

    # Apply RBAC (skip kustomization.yaml)
    kubectl apply --context "kind-$CLUSTER_NAME" -f config/rbac/service_account.yaml
    kubectl apply --context "kind-$CLUSTER_NAME" -f config/rbac/role.yaml
    kubectl apply --context "kind-$CLUSTER_NAME" -f config/rbac/role_binding.yaml

    # Determine the actual image name to use
    # Podman prefixes with localhost/, so we need to use that in the deployment
    local deploy_image="$IMAGE_NAME"
    if [ "$(detect_runtime)" = "podman" ] && [[ "$IMAGE_NAME" != localhost/* ]]; then
        deploy_image="localhost/$IMAGE_NAME"
    fi

    # Update image in deployment manifest and apply
    cat config/manager/manager.yaml | \
        sed "s|image:.*|image: $deploy_image|" | \
        sed "s|imagePullPolicy:.*|imagePullPolicy: Never|" | \
        kubectl apply --context "kind-$CLUSTER_NAME" -f -

    log_info "Operator deployed successfully"
}

# Wait for operator to be ready
wait_for_operator() {
    log_info "Waiting for operator to be ready..."

    kubectl wait --for=condition=available --timeout=120s \
        deployment/virt-platform-operator-controller-manager \
        -n "$NAMESPACE" \
        --context "kind-$CLUSTER_NAME" || {
        log_warn "Operator did not become ready in time"
        log_info "Check logs with: make logs-local"
        return 1
    }

    log_info "Operator is ready"
}

# Show operator status
show_status() {
    log_info "Operator status:"
    kubectl get pods -n "$NAMESPACE" --context "kind-$CLUSTER_NAME"
    echo ""
    log_info "To view logs, run: make logs-local"
}

# Undeploy operator
undeploy_operator() {
    log_info "Undeploying operator"

    kubectl delete --context "kind-$CLUSTER_NAME" -f config/manager/manager.yaml --ignore-not-found=true
    kubectl delete --context "kind-$CLUSTER_NAME" -f config/rbac/role_binding.yaml --ignore-not-found=true
    kubectl delete --context "kind-$CLUSTER_NAME" -f config/rbac/role.yaml --ignore-not-found=true
    kubectl delete --context "kind-$CLUSTER_NAME" -f config/rbac/service_account.yaml --ignore-not-found=true

    log_info "Operator undeployed"
}

# Main deployment flow
deploy() {
    check_cluster
    build_image
    load_image
    create_namespace
    deploy_operator
    wait_for_operator
    show_status

    echo ""
    log_info "✓ Deployment complete!"
    echo ""
    log_info "Next steps:"
    echo "  - View logs: make logs-local"
    echo "  - Check HCO: kubectl get hyperconverged -n openshift-cnv"
    echo "  - Watch operator: kubectl get pods -n $NAMESPACE -w"
}

# Quick redeploy (rebuild + reload image only)
redeploy() {
    check_cluster
    build_image
    load_image

    log_info "Restarting operator pods"
    kubectl rollout restart deployment/virt-platform-operator-controller-manager \
        -n "$NAMESPACE" \
        --context "kind-$CLUSTER_NAME"

    wait_for_operator
    show_status
}

# Main script
case "${1:-deploy}" in
    deploy)
        deploy
        ;;
    redeploy)
        redeploy
        ;;
    undeploy)
        check_cluster
        undeploy_operator
        ;;
    status)
        check_cluster
        show_status
        ;;
    build)
        build_image
        ;;
    load)
        check_cluster
        load_image
        ;;
    *)
        echo "Usage: $0 {deploy|redeploy|undeploy|status|build|load}"
        echo ""
        echo "Commands:"
        echo "  deploy      Full deployment (build + load + deploy + wait)"
        echo "  redeploy    Quick redeploy (rebuild + reload image + restart)"
        echo "  undeploy    Remove operator from cluster"
        echo "  status      Show operator status"
        echo "  build       Build operator image only"
        echo "  load        Load image into cluster only"
        exit 1
        ;;
esac
