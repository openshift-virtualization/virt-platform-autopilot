# Resource Lifecycle Management

This document describes two complementary features for managing the lifecycle of operator-managed resources:

1. **Tombstoning** - Clean deletion of obsolete resources during upgrades
2. **Root Exclusion** - Prevention of resource creation from Day 0

## Overview

During operator upgrades, resources can become obsolete (removed features, renamed resources, consolidated configurations). Simply removing YAML from the assets directory leaves orphaned objects in the cluster. These features provide explicit, safe mechanisms for lifecycle management.

## Tombstoning

### Purpose

Tombstoning enables explicit deletion of legacy resources with built-in safety checks. When a feature is removed or a resource is renamed, the old resource is moved to the tombstones directory to be deleted during the next reconciliation.

### Directory Structure

```
/assets
  ├── active/          # Current managed resources (Apply - Desired State)
  │   ├── descheduler/
  │   ├── hco/
  │   ├── operators/
  │   └── metadata.yaml
  ├── crds/            # CRDs (unchanged location)
  └── tombstones/      # Obsolete resources (Delete - Legacy)
      └── v1.1-cleanup/  # Optional organizational subfolders
```

### Tombstone File Format

Tombstone files are minimal YAML manifests with a **required safety label**:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: obsolete-tuning-config
  namespace: openshift-cnv
  labels:
    platform.kubevirt.io/managed-by: virt-platform-autopilot  # REQUIRED
```

**Required fields:**
- `apiVersion`
- `kind`
- `metadata.name`
- `metadata.labels["platform.kubevirt.io/managed-by"]` = `"virt-platform-autopilot"`

**Optional field:**
- `metadata.namespace` (omit for cluster-scoped resources)

### Safety Mechanism

The tombstoning system includes multiple safety checks:

1. **Label verification**: Resources are only deleted if they have the exact label `platform.kubevirt.io/managed-by=virt-platform-autopilot`
2. **Load-time validation**: Tombstone files are validated when loaded - missing labels cause startup failure
3. **Best-effort execution**: If one tombstone fails to delete, others are still processed
4. **Idempotency**: Already-deleted resources are silently skipped (no error)

### Workflow

**Creating a tombstone:**

1. Identify obsolete resource (e.g., `assets/active/descheduler/old-config.yaml`)
2. Move file to tombstones directory:
   ```bash
   git mv assets/active/descheduler/old-config.yaml assets/tombstones/v1.1-cleanup/
   ```
3. Verify the file contains the required `platform.kubevirt.io/managed-by` label
4. Commit and release

**On operator upgrade:**

1. Operator loads tombstones from `assets/tombstones/`
2. For each tombstone:
   - Check if resource exists in cluster
   - Verify it has the management label
   - Delete if label matches (skip if label missing/incorrect)
3. Emit events and update metrics

**Removing a tombstone** (after 2-3 releases):

1. Confirm resource is deleted from all supported clusters
2. Remove file from tombstones directory:
   ```bash
   git rm assets/tombstones/v1.1-cleanup/old-config.yaml
   ```
3. Commit

### RBAC Automation

The RBAC generator automatically scans the `tombstones/` directory and adds the `delete` verb to the ClusterRole for any resource types found:

```bash
make generate-rbac
```

Generated ClusterRole example:
```yaml
- apiGroups: [""]
  resources: ["configmaps"]
  verbs: ["delete", "create", "get", "list", "patch", "update", "watch"]  # delete added
```

### Observability

**Metrics:**

```prometheus
virt_platform_tombstone_status{kind, name, namespace}
# Values:
#  1 = Resource still exists (not yet deleted)
#  0 = Resource deleted successfully
# -1 = Deletion error
# -2 = Skipped (label mismatch - safety check triggered)
```

**Events:**

- `TombstoneDeleted` (Normal): Resource successfully deleted
- `TombstoneFailed` (Warning): Deletion failed (check logs, RBAC, finalizers)
- `TombstoneSkipped` (Warning): Label mismatch - resource not managed by autopilot

**Alert:**

```yaml
- alert: VirtPlatformTombstoneStuck
  expr: virt_platform_tombstone_status < 0
  for: 30m
```

See runbook: `docs/runbooks/VirtPlatformTombstoneStuck.md`

## Root Exclusion (Day 0 Prevention)

### Purpose

Root exclusion prevents specific resources from being created in the first place. This is useful for:

- Disabling features not relevant to the deployment
- Preventing resource creation in environments where they would fail
- Temporary workarounds for known issues

### Annotation Format

Set the `platform.kubevirt.io/disabled-resources` annotation on the HyperConverged CR:

```yaml
apiVersion: hco.kubevirt.io/v1
kind: HyperConverged
metadata:
  name: kubevirt-hyperconverged
  namespace: openshift-cnv
  annotations:
    platform.kubevirt.io/disabled-resources: "KubeDescheduler/cluster, MachineConfig/50-swap-enable"
```

Format: `"Kind/Name, Kind/Name, ..."`

### Implementation

1. Operator parses the annotation on each reconciliation
2. After rendering assets, filters out excluded resources in-memory
3. Excluded resources are never applied (ServerSideApply is never called)
4. Logs each skipped resource for transparency

**Example log:**
```
Skipping resource due to Root Exclusion kind=KubeDescheduler name=cluster annotation=platform.kubevirt.io/disabled-resources
```

### Use Cases

**Disable KubeDescheduler in non-cluster environments:**
```yaml
annotations:
  platform.kubevirt.io/disabled-resources: "KubeDescheduler/cluster"
```

**Disable swap on specific clusters:**
```yaml
annotations:
  platform.kubevirt.io/disabled-resources: "MachineConfig/50-swap-enable"
```

**Temporary workaround for failing resource:**
```yaml
annotations:
  platform.kubevirt.io/disabled-resources: "PersesDataSource/virt-metrics"
```

### Comparison with `mode: unmanaged`

| Feature | Root Exclusion | `mode: unmanaged` |
|---------|---------------|-------------------|
| Scope | Specific resources (Kind/Name) | Individual resource (annotation per object) |
| When | Day 0 (prevents creation) | Day 1+ (stops reconciliation) |
| RBAC | No impact (resource never created) | Full RBAC still required |
| Use case | Disable features cluster-wide | Opt out of management per resource |

**When to use Root Exclusion:**
- Cluster-wide feature disablement
- Resources that should never be created
- Temporary workarounds before feature flag available

**When to use `mode: unmanaged`:**
- Per-resource customization
- Gradual migration to external management
- Temporary user overrides

### Limitations

- **Case-sensitive**: `KubeDescheduler/cluster` ≠ `kubedescheduler/cluster`
- **Exact match**: Must match Kind and Name exactly
- **No wildcard support**: Cannot use `*` or regex
- **No namespace filtering**: Cannot exclude `ConfigMap/foo` only in namespace `bar`

## Troubleshooting

### Tombstone Not Deleted

1. Check if resource exists:
   ```bash
   kubectl get <kind> <name> -n <namespace>
   ```

2. Verify label:
   ```bash
   kubectl get <kind> <name> -n <namespace> -o jsonpath='{.metadata.labels}'
   ```

   Should contain: `"platform.kubevirt.io/managed-by": "virt-platform-autopilot"`

3. Check for finalizers:
   ```bash
   kubectl get <kind> <name> -n <namespace> -o jsonpath='{.metadata.finalizers}'
   ```

   If finalizers present, they may block deletion. Check operator logs for the finalizer owner.

4. Check RBAC:
   ```bash
   kubectl auth can-i delete <resource> --as system:serviceaccount:openshift-cnv:virt-platform-autopilot
   ```

5. Check events:
   ```bash
   kubectl get events -n openshift-cnv --field-selector involvedObject.kind=HyperConverged
   ```

### Resource Created Despite Root Exclusion

1. Verify annotation syntax:
   ```bash
   kubectl get hco kubevirt-hyperconverged -n openshift-cnv \
     -o jsonpath='{.metadata.annotations.platform\.kubevirt\.io/disabled-resources}'
   ```

2. Check operator logs for "Skipping resource due to Root Exclusion" message

3. Verify Kind/Name matches exactly (case-sensitive)

4. Check if resource was created before annotation was added
   - Root exclusion only prevents creation, doesn't delete existing resources
   - Use tombstoning to remove existing resources

## Best Practices

### Tombstoning

1. **Lifecycle**: Keep tombstones for 2-3 releases, then remove
2. **Organization**: Use subdirectories like `v1.1-cleanup/` for clarity
3. **Testing**: Test tombstone deletion in staging before production release
4. **Monitoring**: Set up alerts for stuck tombstones
5. **Documentation**: Document why resources were tombstoned (commit message)

### Root Exclusion

1. **Temporary**: Use root exclusion as a temporary measure, not permanent solution
2. **Documentation**: Document why resources are excluded
3. **Alternatives**: Consider if feature gates or component-level disable is better
4. **Migration path**: Plan to remove exclusions when proper fix is available

### General

1. **Prefer feature flags**: Use metadata.yaml conditions when possible
2. **Gradual rollout**: Test lifecycle changes in dev/staging first
3. **Monitor metrics**: Watch tombstone_status and compliance_status metrics
4. **Clean up**: Remove old tombstones and unused exclusions regularly

## Examples

### Example 1: Rename a ConfigMap

**Before (v1.0):**
```
assets/active/observability/metrics-config.yaml
```

**After (v1.1):**
1. Create new resource:
   ```
   assets/active/observability/prometheus-config.yaml
   ```

2. Move old resource to tombstones:
   ```bash
   git mv assets/active/observability/metrics-config.yaml \
          assets/tombstones/v1.1-cleanup/metrics-config.yaml
   ```

3. Verify label in tombstone file:
   ```yaml
   labels:
     platform.kubevirt.io/managed-by: virt-platform-autopilot
   ```

4. Release v1.1 - operator will:
   - Create `prometheus-config`
   - Delete `metrics-config` (tombstone)

### Example 2: Disable Feature Temporarily

User wants to disable KubeDescheduler due to known issue:

```bash
kubectl annotate hco kubevirt-hyperconverged -n openshift-cnv \
  platform.kubevirt.io/disabled-resources="KubeDescheduler/cluster"
```

Operator will skip creating/updating KubeDescheduler.

To re-enable:
```bash
kubectl annotate hco kubevirt-hyperconverged -n openshift-cnv \
  platform.kubevirt.io/disabled-resources-
```

### Example 3: Remove Obsolete Feature

Removing MTV (Migration Toolkit) integration:

1. Create tombstone:
   ```yaml
   # assets/tombstones/v1.2-cleanup/mtv-operator.yaml
   apiVersion: operators.coreos.com/v1alpha1
   kind: Subscription
   metadata:
     name: mtv-operator
     namespace: openshift-mtv
     labels:
       platform.kubevirt.io/managed-by: virt-platform-autopilot
   ```

2. Remove from active:
   ```bash
   git rm assets/active/operators/mtv.yaml.tpl
   ```

3. Update metadata.yaml to remove MTV asset entry

4. Regenerate RBAC:
   ```bash
   make generate-rbac  # Adds delete verb for Subscription
   ```

5. Release - operator deletes MTV subscription

6. After 2 releases, clean up tombstone:
   ```bash
   git rm assets/tombstones/v1.2-cleanup/mtv-operator.yaml
   ```

## Migration Guide (for Existing Deployments)

If you have an existing deployment and want to adopt lifecycle management:

1. **Audit current resources**: Identify which resources are managed by autopilot
   ```bash
   kubectl get all,cm,secrets -A -l platform.kubevirt.io/managed-by=virt-platform-autopilot
   ```

2. **Ensure labels**: All managed resources should have the management label
   - Newer versions auto-apply labels
   - Older resources may need manual labeling

3. **Plan tombstones**: For any resources to be removed, create tombstones with proper labels

4. **Test in staging**: Validate tombstone deletion in non-production environment

5. **Monitor**: Watch metrics and events during rollout

## References

- Specification: `/claude_assets/reclaiming_leftovers.md`
- Runbook: `docs/runbooks/VirtPlatformTombstoneStuck.md`
- RBAC generation: `cmd/rbac-gen/main.go`
- Implementation: `pkg/engine/tombstone.go`, `pkg/engine/exclusion.go`
