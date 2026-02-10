#!/usr/bin/env bash

# Script to fetch CRDs from upstream repositories for testing
# This enables envtest and Kind testing without requiring operator installations
#
# Usage:
#   ./update-crds.sh           - Fetch CRDs from upstream
#   ./update-crds.sh --verify  - Verify CRDs match upstream (for CI)
#
# Safety features:
# - Fetches to temp location first, only overwrites on success
# - Preserves existing files if fetch fails (network issues, GitHub outages)
# - Verify mode: exits with error if CRDs are out of sync with upstream

set -e

CRDS_DIR="assets/crds"
TEMP_DIR=$(mktemp -d)
VERIFY_MODE=false

# Parse arguments
if [ "$1" = "--verify" ]; then
    VERIFY_MODE=true
    echo "Running in VERIFY mode - will check if CRDs match upstream"
fi

# Track verification results
OUTDATED_COUNT=0
MISSING_COUNT=0
FETCH_FAILED_COUNT=0

echo "Updating CRDs in $CRDS_DIR"

# Create directory structure
mkdir -p "$CRDS_DIR"/{kubevirt,openshift,remediation,oadp,observability,operators}

# Function to fetch a file from GitHub (with safety: fetch to temp, move on success)
fetch_github_file() {
    local repo=$1
    local branch=$2
    local file_path=$3
    local output_path=$4

    local url="https://raw.githubusercontent.com/${repo}/${branch}/${file_path}"
    local temp_file
    temp_file="${TEMP_DIR}/$(basename "$output_path")"

    if $VERIFY_MODE; then
        echo "Checking: $url"
    else
        echo "Fetching: $url"
    fi

    # Fetch to temp location
    if curl -sSfL "$url" -o "$temp_file" 2>/dev/null; then
        if $VERIFY_MODE; then
            # Verify mode: check if content differs
            if [ -f "$output_path" ]; then
                if ! cmp -s "$temp_file" "$output_path"; then
                    echo "  ✗ OUT OF SYNC with upstream" >&2
                    OUTDATED_COUNT=$((OUTDATED_COUNT + 1))
                    return 1
                else
                    echo "  ✓ Up to date"
                    return 0
                fi
            else
                echo "  ✗ MISSING (exists upstream but not locally)" >&2
                MISSING_COUNT=$((MISSING_COUNT + 1))
                return 1
            fi
        else
            # Update mode: move to final location
            mv "$temp_file" "$output_path"
            echo "  ✓ Saved to $output_path"
            return 0
        fi
    else
        # Fetch failed
        if $VERIFY_MODE; then
            echo "  ⚠ Cannot verify (fetch failed)" >&2
            FETCH_FAILED_COUNT=$((FETCH_FAILED_COUNT + 1))
        else
            if [ -f "$output_path" ]; then
                echo "  ⚠ Failed to fetch (keeping existing file)" >&2
            else
                echo "  ✗ Failed to fetch (no existing file)" >&2
            fi
        fi
        return 1
    fi
}

# =============================================================================
# CRD Metadata - Single Source of Truth
# =============================================================================
# Format: "category|name|repo|branch|upstream_path|local_path"

declare -a CRD_METADATA=(
    # KubeVirt Ecosystem
    "KubeVirt Ecosystem|HyperConverged|kubevirt/hyperconverged-cluster-operator|main|deploy/crds/hco00.crd.yaml|kubevirt/hyperconverged-crd.yaml"

    # OpenShift Platform
    "OpenShift Platform|MachineConfig|openshift/api|master|machineconfiguration/v1/zz_generated.crd-manifests/0000_80_machine-config_01_machineconfigs.crd.yaml|openshift/machineconfig-crd.yaml"
    "OpenShift Platform|KubeletConfig|openshift/api|master|machineconfiguration/v1/zz_generated.crd-manifests/0000_80_machine-config_01_kubeletconfigs.crd.yaml|openshift/kubeletconfig-crd.yaml"
    "OpenShift Platform|KubeDescheduler|openshift/cluster-kube-descheduler-operator|main|manifests/kube-descheduler-operator.crd.yaml|openshift/operator.openshift.io_kubedeschedulers.yaml"

    # Medik8s Remediation
    "Medik8s Remediation|NodeHealthCheck|medik8s/node-healthcheck-operator|main|config/crd/bases/remediation.medik8s.io_nodehealthchecks.yaml|remediation/nodehealthchecks.remediation.medik8s.io.yaml"
    "Medik8s Remediation|Self Node Remediation|medik8s/self-node-remediation|main|config/crd/bases/self-node-remediation.medik8s.io_selfnoderemediations.yaml|remediation/selfnoderemediations.self-node-remediation.medik8s.io.yaml"
    "Medik8s Remediation|Fence Agents Remediation|medik8s/fence-agents-remediation|main|config/crd/bases/fence-agents-remediation.medik8s.io_fenceagentsremediations.yaml|remediation/fenceagentsremediations.fence-agents-remediation.medik8s.io.yaml"

    # Third-Party Operators
    "Third-Party Operators|MTV (Forklift)|kubev2v/forklift|main|operator/config/crd/bases/forklift.konveyor.io_forkliftcontrollers.yaml|operators/forklift.konveyor.io_forkliftcontrollers.yaml"
    "Third-Party Operators|MetalLB|metallb/metallb-operator|main|config/crd/bases/metallb.io_metallbs.yaml|operators/metallb.io_metallbs.yaml"
    "Third-Party Operators|AAQ|kubevirt/hyperconverged-cluster-operator|main|deploy/crds/application-aware-quota00.crd.yaml|operators/aaq.kubevirt.io_aaqoperatorconfigs.yaml"
    "Third-Party Operators|NMState|nmstate/kubernetes-nmstate|main|bundle/manifests/nmstate.io_nmstates.yaml|operators/nmstate.io_nmstates.yaml"
    "Third-Party Operators|Node Maintenance Operator|medik8s/node-maintenance-operator|main|bundle/manifests/nodemaintenance.medik8s.io_nodemaintenances.yaml|operators/nodemaintenance.medik8s.io_nodemaintenances.yaml"
)

# Cluster Observability Operator CRDs (multiple files)
declare -a COO_CRDS=(
    "perses.dev_persesglobaldatasources.yaml"
    "perses.dev_persesdatasources.yaml"
    "perses.dev_persesdashboards.yaml"
    "perses.dev_perses.yaml"
    "observability.openshift.io_uiplugins.yaml"
    "observability.openshift.io_observabilityinstallers.yaml"
    "monitoring.rhobs_thanosrulers.yaml"
    "monitoring.rhobs_thanosqueriers.yaml"
    "monitoring.rhobs_servicemonitors.yaml"
    "monitoring.rhobs_scrapeconfigs.yaml"
    "monitoring.rhobs_prometheusrules.yaml"
    "monitoring.rhobs_prometheuses.yaml"
    "monitoring.rhobs_prometheusagents.yaml"
    "monitoring.rhobs_probes.yaml"
    "monitoring.rhobs_podmonitors.yaml"
    "monitoring.rhobs_monitoringstacks.yaml"
    "monitoring.rhobs_alertmanagers.yaml"
    "monitoring.rhobs_alertmanagerconfigs.yaml"
)

# OADP CRDs (multiple files)
declare -a OADP_CRDS=(
    "velero.io_volumesnapshotlocations.yaml"
    "velero.io_serverstatusrequests.yaml"
    "velero.io_schedules.yaml"
    "velero.io_restores.yaml"
    "velero.io_podvolumerestores.yaml"
    "velero.io_podvolumebackups.yaml"
    "velero.io_downloadrequests.yaml"
    "velero.io_deletebackuprequests.yaml"
    "velero.io_datauploads.yaml"
    "velero.io_datadownloads.yaml"
    "velero.io_backupstoragelocations.yaml"
    "velero.io_backups.yaml"
    "velero.io_backuprepositories.yaml"
    "oadp.openshift.io_virtualmachinefilerestores.yaml"
    "oadp.openshift.io_virtualmachinebackupsdiscoveries.yaml"
    "oadp.openshift.io_nonadminrestores.yaml"
    "oadp.openshift.io_nonadmindownloadrequests.yaml"
    "oadp.openshift.io_nonadminbackupstoragelocations.yaml"
    "oadp.openshift.io_nonadminbackupstoragelocationrequests.yaml"
    "oadp.openshift.io_nonadminbackups.yaml"
    "oadp.openshift.io_dataprotectiontests.yaml"
    "oadp.openshift.io_dataprotectionapplications.yaml"
    "oadp.openshift.io_cloudstorages.yaml"
)

# =============================================================================
# Fetch/Verify CRDs
# =============================================================================

# Fetch single-file CRDs
for entry in "${CRD_METADATA[@]}"; do
    IFS='|' read -r category name repo branch upstream_path local_path <<< "$entry"

    echo ""
    if $VERIFY_MODE; then
        echo "=== Verifying $name ==="
    else
        echo "=== Fetching $name ==="
    fi
    fetch_github_file \
        "$repo" \
        "$branch" \
        "$upstream_path" \
        "$CRDS_DIR/$local_path" || true
done

# Fetch Cluster Observability Operator CRDs
echo ""
if $VERIFY_MODE; then
    echo "=== Verifying Cluster Observability Operator CRDs ==="
else
    echo "=== Fetching Cluster Observability Operator CRDs ==="
fi
for crd in "${COO_CRDS[@]}"; do
    fetch_github_file \
        "rhobs/observability-operator" \
        "main" \
        "bundle/manifests/$crd" \
        "$CRDS_DIR/observability/$crd" || true
done

# Fetch OADP CRDs
echo ""
if $VERIFY_MODE; then
    echo "=== Verifying OADP CRDs ==="
else
    echo "=== Fetching OADP CRDs ==="
fi
for crd in "${OADP_CRDS[@]}"; do
    fetch_github_file \
        "openshift/oadp-operator" \
        "oadp-dev" \
        "bundle/manifests/$crd" \
        "$CRDS_DIR/oadp/$crd" || true
done

# =============================================================================
# Generate README (only in update mode)
# =============================================================================

if ! $VERIFY_MODE; then
    echo ""
    echo "=== Generating README ==="

    cat > "$CRDS_DIR/README.md" <<'HEADER'
# CRD Collection for Testing

This directory contains CRDs required for envtest and Kind testing.

CRDs are automatically fetched from upstream repositories using `hack/update-crds.sh`.

## Directory Structure

```
assets/crds/
├── kubevirt/          # KubeVirt ecosystem CRDs
├── openshift/         # OpenShift platform CRDs
├── remediation/       # Medik8s remediation CRDs
├── operators/         # Third-party operator CRDs
├── observability/     # Cluster Observability Operator CRDs
└── oadp/              # OADP backup/restore CRDs
```

## CRD Sources

HEADER

    # Generate source documentation by category
    current_category=""
    for entry in "${CRD_METADATA[@]}"; do
        IFS='|' read -r category name repo branch upstream_path local_path <<< "$entry"

        # Print category header if changed
        if [ "$category" != "$current_category" ]; then
            {
                echo ""
                echo "### $category"
                echo ""
            } >> "$CRDS_DIR/README.md"
            current_category="$category"
        fi

        # Print CRD entry
        cat >> "$CRDS_DIR/README.md" <<EOF
**$name**
- Repository: https://github.com/$repo
- Branch: \`$branch\`
- Path: \`$upstream_path\`
- Local: \`$local_path\`

EOF
    done

    # Add Cluster Observability Operator section
    cat >> "$CRDS_DIR/README.md" <<'COO_SECTION'

### Cluster Observability Operator

**Multiple CRDs** (Perses, UIPlugin, Monitoring)
- Repository: https://github.com/rhobs/observability-operator
- Branch: `main`
- Path: `bundle/manifests/*.yaml`
- Local: `observability/`
- Count: COO_COUNT_PLACEHOLDER files

COO_SECTION

    # Replace observability count
    sed -i "s/COO_COUNT_PLACEHOLDER/${#COO_CRDS[@]}/" "$CRDS_DIR/README.md"

    # Add OADP section
    cat >> "$CRDS_DIR/README.md" <<'OADP_SECTION'

### OADP (OpenShift API for Data Protection)

**Multiple CRDs** (Velero, DataProtection)
- Repository: https://github.com/openshift/oadp-operator
- Branch: `oadp-dev`
- Path: `bundle/manifests/*.yaml`
- Local: `oadp/`
- Count: OADP_COUNT_PLACEHOLDER files

OADP_SECTION

    # Replace OADP count
    sed -i "s/OADP_COUNT_PLACEHOLDER/${#OADP_CRDS[@]}/" "$CRDS_DIR/README.md"

    # Add usage instructions
    cat >> "$CRDS_DIR/README.md" <<'FOOTER'

## Update Instructions

**Fetch latest CRDs from upstream:**
```bash
make update-crds
```

**Verify CRDs match upstream (CI check):**
```bash
make verify-crds
```

**Validate CRDs can be loaded:**
```bash
go test ./test/crd_test.go -v
```

## Usage in Tests

### envtest

```go
testEnv = &envtest.Environment{
    CRDDirectoryPaths: []string{
        filepath.Join("..", "assets", "crds", "kubevirt"),
        filepath.Join("..", "assets", "crds", "openshift"),
        filepath.Join("..", "assets", "crds", "remediation"),
        filepath.Join("..", "assets", "crds", "operators"),
        filepath.Join("..", "assets", "crds", "observability"),
        filepath.Join("..", "assets", "crds", "oadp"),
    },
}
```

### Kind

```bash
kubectl apply -f assets/crds/kubevirt/
kubectl apply -f assets/crds/openshift/
kubectl apply -f assets/crds/remediation/
kubectl apply -f assets/crds/operators/
kubectl apply -f assets/crds/observability/
kubectl apply -f assets/crds/oadp/
```

## Maintenance

This README is automatically generated by `hack/update-crds.sh`.
**Do not edit manually** - changes will be overwritten.

To add a new CRD:
1. Edit the `CRD_METADATA` array in `hack/update-crds.sh`
2. Run `make update-crds`
3. Commit both the new CRD file and updated README

FOOTER
fi

# Clean up
rm -rf "$TEMP_DIR"

# =============================================================================
# Print summary and exit with appropriate code
# =============================================================================

echo ""
if $VERIFY_MODE; then
    echo "=========================================="
    echo "CRD VERIFICATION SUMMARY"
    echo "=========================================="

    total_crds=$(find "$CRDS_DIR" -name "*.yaml" -type f | wc -l)

    echo "Total CRD files: $total_crds"
    echo ""
    echo "Verification results:"
    echo "  ✓ Up to date: $((total_crds - OUTDATED_COUNT - MISSING_COUNT - FETCH_FAILED_COUNT))"
    echo "  ✗ Out of sync: $OUTDATED_COUNT"
    echo "  ✗ Missing: $MISSING_COUNT"
    echo "  ⚠ Cannot verify: $FETCH_FAILED_COUNT"

    if [ $OUTDATED_COUNT -gt 0 ] || [ $MISSING_COUNT -gt 0 ]; then
        echo ""
        echo "❌ VERIFICATION FAILED!"
        echo ""
        echo "CRDs are out of sync with upstream. Run 'make update-crds' to sync."
        exit 1
    elif [ $FETCH_FAILED_COUNT -gt 0 ]; then
        echo ""
        echo "⚠ WARNING: Some CRDs could not be verified (network issues?)"
        echo "Proceeding anyway..."
        exit 0
    else
        echo ""
        echo "✅ All CRDs are up to date with upstream!"
        exit 0
    fi
else
    echo "✓ CRD update complete!"
    echo ""
    echo "Summary:"
    total_crds=$(find "$CRDS_DIR" -name "*.yaml" -type f | wc -l)
    echo "  Total CRD files: $total_crds"
    echo ""
    echo "  By directory:"
    for dir in kubevirt openshift remediation operators observability oadp; do
        if [ -d "$CRDS_DIR/$dir" ]; then
            count=$(find "$CRDS_DIR/$dir" -name "*.yaml" -type f 2>/dev/null | wc -l)
            printf "    %-15s %3d files\n" "$dir/" "$count"
        fi
    done
    echo ""
    echo "Next steps:"
    echo "  - Verify CRDs: make verify-crds"
    echo "  - Validate CRDs: go test ./test/crd_test.go -v"
fi
