#!/usr/bin/env bash

set -e

# E2E test runner for kind cluster
# This script:
# 1. Creates kind cluster
# 2. Deploys operator (expects image already built via 'make docker-build')
# 3. Runs E2E tests
# 4. Cleans up (optional)
#
# Note: When called via 'make test-e2e', the image is already built by docker-build target.
#       When called directly, ensure image is built first or set IMAGE_NAME to existing image.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

CLUSTER_NAME="${CLUSTER_NAME:-virt-platform-autopilot-e2e}"
IMAGE_NAME="${IMAGE_NAME:-virt-platform-autopilot:latest}"
CLEANUP="${CLEANUP:-true}"
CONTAINER_TOOL="${CONTAINER_TOOL:-$(command -v docker 2>/dev/null || command -v podman 2>/dev/null)}"

log_info() {
    echo "[INFO] $*"
}

log_error() {
    echo "[ERROR] $*" >&2
}

cleanup() {
    if [ "$CLEANUP" = "true" ]; then
        log_info "Cleaning up kind cluster..."
        CLUSTER_NAME="$CLUSTER_NAME" "${SCRIPT_DIR}/kind-cluster.sh" delete || true
    else
        log_info "Skipping cleanup (CLEANUP=false)"
        log_info "To clean up manually: CLUSTER_NAME=$CLUSTER_NAME ./hack/kind-cluster.sh delete"
    fi
}

# Trap errors and cleanup
trap cleanup EXIT

main() {
    log_info "Starting E2E test suite"
    log_info "Cluster: $CLUSTER_NAME"
    log_info "Image: $IMAGE_NAME"

    # Create kind cluster
    log_info "Creating kind cluster..."
    CLUSTER_NAME="$CLUSTER_NAME" "${SCRIPT_DIR}/kind-cluster.sh" create

    # Deploy operator (image already built by make test-e2e)
    log_info "Deploying operator to kind cluster (using pre-built image)..."
    cd "$PROJECT_ROOT"
    CLUSTER_NAME="$CLUSTER_NAME" IMAGE_NAME="$IMAGE_NAME" "${SCRIPT_DIR}/deploy-local.sh" deploy-prebuilt

    # Wait for operator to be ready
    log_info "Waiting for operator to be ready..."
    kubectl wait --for=condition=available --timeout=2m \
        deployment/virt-platform-autopilot \
        -n openshift-cnv \
        --context "kind-${CLUSTER_NAME}" || {
        log_error "Operator deployment failed to become ready"
        kubectl get pods -n openshift-cnv --context "kind-${CLUSTER_NAME}"
        kubectl logs -n openshift-cnv -l control-plane=controller-manager --tail=50 --context "kind-${CLUSTER_NAME}"
        exit 1
    }

    log_info "Operator is ready, running E2E tests..."

    # Run E2E tests with proper kubeconfig
    export KUBECONFIG="${HOME}/.kube/config"
    export KUBE_CONTEXT="kind-${CLUSTER_NAME}"

    cd "$PROJECT_ROOT"
    go run github.com/onsi/ginkgo/v2/ginkgo -v --trace \
        --timeout=10m \
        --poll-progress-after=60s \
        ./test/e2e/...

    log_info "E2E tests completed successfully!"
}

main "$@"
