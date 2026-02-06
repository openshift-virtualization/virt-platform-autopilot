#!/usr/bin/env bash

# Script to fetch CRDs from upstream repositories for testing
# This enables envtest and Kind testing without requiring operator installations

set -e

CRDS_DIR="assets/crds"
TEMP_DIR=$(mktemp -d)

echo "Updating CRDs in $CRDS_DIR"

# Create directory structure
mkdir -p "$CRDS_DIR"/{kubevirt,openshift,remediation,operators,storage}

# Function to fetch a file from GitHub
fetch_github_file() {
    local repo=$1
    local branch=$2
    local file_path=$3
    local output_path=$4

    local url="https://raw.githubusercontent.com/${repo}/${branch}/${file_path}"
    echo "Fetching: $url"

    if curl -sSfL "$url" -o "$output_path"; then
        echo "  ✓ Saved to $output_path"
    else
        echo "  ✗ Failed to fetch $url" >&2
        return 1
    fi
}

# Fetch HyperConverged CRD
echo ""
echo "=== Fetching KubeVirt HCO CRD ==="
fetch_github_file \
    "kubevirt/hyperconverged-cluster-operator" \
    "main" \
    "deploy/crds/hco00.crd.yaml" \
    "$CRDS_DIR/kubevirt/hyperconverged-crd.yaml" || true

# Fetch OpenShift MachineConfig CRDs
echo ""
echo "=== Fetching OpenShift MachineConfig CRDs ==="

# Note: These are examples - actual paths may vary
# MachineConfig CRDs are typically embedded in the operator
echo "  Note: MachineConfig CRDs may require manual download from OpenShift release"
echo "  See: https://github.com/openshift/machine-config-operator"

# Fetch KubeDescheduler CRD
echo ""
echo "=== Fetching KubeDescheduler CRD ==="
fetch_github_file \
    "openshift/cluster-kube-descheduler-operator" \
    "master" \
    "manifests/stable/cluster-kube-descheduler-operator.crd.yaml" \
    "$CRDS_DIR/openshift/operator.openshift.io_kubedeschedulers.yaml" || true

# Fetch NodeHealthCheck CRDs
echo ""
echo "=== Fetching Medik8s remediation CRDs ==="

fetch_github_file \
    "medik8s/node-healthcheck-operator" \
    "main" \
    "config/crd/bases/remediation.medik8s.io_nodehealthchecks.yaml" \
    "$CRDS_DIR/remediation/nodehealthchecks.remediation.medik8s.io.yaml" || true

fetch_github_file \
    "medik8s/self-node-remediation" \
    "main" \
    "config/crd/bases/self-node-remediation.medik8s.io_selfnoderemediations.yaml" \
    "$CRDS_DIR/remediation/selfnoderemediations.self-node-remediation.medik8s.io.yaml" || true

fetch_github_file \
    "medik8s/fence-agents-remediation" \
    "main" \
    "config/crd/bases/fence-agents-remediation.medik8s.io_fenceagentsremediations.yaml" \
    "$CRDS_DIR/remediation/fenceagentsremediations.fence-agents-remediation.medik8s.io.yaml" || true

# Fetch MTV (Forklift) CRD
echo ""
echo "=== Fetching MTV (Forklift) CRD ==="
fetch_github_file \
    "kubev2v/forklift" \
    "main" \
    "operator/config/crd/bases/forklift.konveyor.io_forkliftcontrollers.yaml" \
    "$CRDS_DIR/operators/forklift.konveyor.io_forkliftcontrollers.yaml" || true

# Fetch MetalLB CRD
echo ""
echo "=== Fetching MetalLB CRD ==="
fetch_github_file \
    "metallb/metallb-operator" \
    "main" \
    "config/crd/bases/metallb.io_metallbs.yaml" \
    "$CRDS_DIR/operators/metallb.io_metallbs.yaml" || true

# Fetch AAQ CRD
echo ""
echo "=== Fetching AAQ CRD ==="
fetch_github_file \
    "kubevirt/application-aware-quota" \
    "main" \
    "config/crd/bases/aaq.kubevirt.io_aaqoperatorconfigs.yaml" \
    "$CRDS_DIR/operators/aaq.kubevirt.io_aaqoperatorconfigs.yaml" || true

# Update README with versions and timestamp
echo ""
echo "=== Updating README ==="
cat > "$CRDS_DIR/README.md" <<EOF
# CRD Collection for Testing

This directory contains CRDs required for envtest and Kind testing.

**Last updated:** $(date -u +"%Y-%m-%d %H:%M:%S UTC")

## CRD Sources

### KubeVirt Ecosystem
- **HyperConverged**: https://github.com/kubevirt/hyperconverged-cluster-operator
  - Path: \`deploy/crds/hco.kubevirt.io_hyperconvergeds.yaml\`

### OpenShift Platform
- **MachineConfig**: https://github.com/openshift/machine-config-operator
  - Note: CRDs may need manual extraction from OpenShift release images
- **KubeDescheduler**: https://github.com/openshift/cluster-kube-descheduler-operator
  - Path: \`manifests/stable/cluster-kube-descheduler-operator.crd.yaml\`

### Medik8s Remediation
- **NodeHealthCheck**: https://github.com/medik8s/node-healthcheck-operator
  - Path: \`config/crd/bases/remediation.medik8s.io_nodehealthchecks.yaml\`
- **Self Node Remediation**: https://github.com/medik8s/self-node-remediation
  - Path: \`config/crd/bases/self-node-remediation.medik8s.io_selfnoderemediations.yaml\`
- **Fence Agents**: https://github.com/medik8s/fence-agents-remediation
  - Path: \`config/crd/bases/fence-agents-remediation.medik8s.io_fenceagentsremediations.yaml\`

### Third-Party Operators
- **MTV (Forklift)**: https://github.com/kubev2v/forklift
  - Path: \`operator/config/crd/bases/forklift.konveyor.io_forkliftcontrollers.yaml\`
- **MetalLB**: https://github.com/metallb/metallb-operator
  - Path: \`config/crd/bases/metallb.io_metallbs.yaml\`
- **AAQ**: https://github.com/kubevirt/application-aware-quota
  - Path: \`config/crd/bases/aaq.kubevirt.io_aaqoperatorconfigs.yaml\`

## Update Instructions

Run \`make update-crds\` to fetch the latest CRDs from upstream.
Run \`make verify-crds\` to validate CRDs load correctly in envtest.

## Usage in Tests

### envtest
\`\`\`go
testEnv = &envtest.Environment{
    CRDDirectoryPaths: []string{
        filepath.Join("..", "internal", "assets", "crds", "kubevirt"),
        filepath.Join("..", "internal", "assets", "crds", "openshift"),
        filepath.Join("..", "internal", "assets", "crds", "remediation"),
        filepath.Join("..", "internal", "assets", "crds", "operators"),
    },
}
\`\`\`

### Kind
CRDs are automatically installed by \`make kind-setup\` or \`make kind-install-crds\`.

## Manual Installation

To install CRDs into a cluster manually:
\`\`\`bash
kubectl apply -f internal/assets/crds/kubevirt/
kubectl apply -f internal/assets/crds/openshift/
kubectl apply -f internal/assets/crds/remediation/
kubectl apply -f internal/assets/crds/operators/
\`\`\`
EOF

# Clean up
rm -rf "$TEMP_DIR"

echo ""
echo "✓ CRD update complete!"
echo ""
echo "Summary:"
find "$CRDS_DIR" -name "*.yaml" -type f | wc -l | xargs echo "  CRD files:"
echo ""
echo "Next steps:"
echo "  - Verify CRDs: make verify-crds"
echo "  - Install to Kind: make kind-install-crds"
echo "  - View README: cat $CRDS_DIR/README.md"
