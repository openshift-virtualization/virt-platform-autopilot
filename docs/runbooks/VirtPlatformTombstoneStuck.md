# VirtPlatformTombstoneStuck Runbook

## Alert Description

**Alert Name:** `VirtPlatformTombstoneStuck`

**Severity:** Warning

**Summary:** A tombstoned resource cannot be deleted by virt-platform-autopilot

**Triggers when:** `virt_platform_tombstone_status < 0` for 30 minutes

**Metric values:**
- `-1` = Deletion error (API error, permissions, finalizers, webhooks)
- `-2` = Label mismatch (resource not managed by virt-platform-autopilot)

## Impact

**Immediate impact:** Low
- Obsolete resource remains in cluster but is not actively used
- Cluster functionality is not affected
- Operator continues normal reconciliation

**Long-term impact:** Medium
- Cluster configuration drift (obsolete resources accumulating)
- Potential conflicts if resource is recreated with same name
- Cleanup complexity increases over time

## Diagnosis

### Step 1: Identify the Stuck Tombstone

```bash
# Get alert details from Prometheus/Alertmanager
# Note the kind, name, and namespace from alert labels

# Check metric value
kubectl exec -n openshift-cnv deployment/virt-platform-autopilot -- \
  curl -s localhost:8080/metrics | grep 'virt_platform_tombstone_status{kind="<KIND>",name="<NAME>"'
```

**Interpret the value:**
- `-1`: Deletion error (proceed to Step 2)
- `-2`: Label mismatch (proceed to Step 3)

### Step 2: Deletion Error (-1)

The resource exists and has the correct label, but deletion failed.

#### Check if Resource Exists

```bash
kubectl get <KIND> <NAME> -n <NAMESPACE>
```

If `NotFound`: False alarm - metric will update on next reconciliation (wait 5 minutes)

#### Check for Finalizers

```bash
kubectl get <KIND> <NAME> -n <NAMESPACE> -o jsonpath='{.metadata.finalizers}'
```

**Finalizers present:**
- Finalizers prevent deletion until their controller removes them
- Common finalizers:
  - `foregroundDeletion`: Waiting for dependent resources to delete
  - `<operator-name>/<finalizer-name>`: Waiting for operator cleanup logic

**Resolution:**
1. Check logs of the controller that owns the finalizer
2. Verify the controller is running and healthy
3. If controller is missing/broken, manually remove finalizer:
   ```bash
   kubectl patch <KIND> <NAME> -n <NAMESPACE> --type=json \
     -p='[{"op": "remove", "path": "/metadata/finalizers/<INDEX>"}]'
   ```
   **⚠️ WARNING:** Only remove finalizers if you understand the implications!

#### Check Deletion Timestamp

```bash
kubectl get <KIND> <NAME> -n <NAMESPACE> -o jsonpath='{.metadata.deletionTimestamp}'
```

**DeletionTimestamp set:**
- Resource is already being deleted
- Stuck waiting for finalizers or webhooks
- Follow finalizer troubleshooting above

**DeletionTimestamp not set:**
- Deletion was not successful
- Proceed to check RBAC and logs

#### Check RBAC Permissions

```bash
kubectl auth can-i delete <RESOURCE> -n <NAMESPACE> \
  --as system:serviceaccount:openshift-cnv:virt-platform-autopilot
```

**Permission denied:**
1. This should not happen if RBAC was generated correctly
2. Regenerate RBAC:
   ```bash
   make generate-rbac
   git diff config/rbac/role.yaml  # Verify delete verb is present
   ```
3. Apply updated RBAC:
   ```bash
   kubectl apply -f config/rbac/role.yaml
   ```

#### Check Operator Logs

```bash
kubectl logs -n openshift-cnv deployment/virt-platform-autopilot --tail=200 | grep -i tombstone
```

Look for:
- API errors (rate limiting, timeout)
- Webhook validation failures
- Conflict errors (resource being modified)

**Common errors:**

**Webhook validation failure:**
```
Error: admission webhook "..." denied the request: <reason>
```
- Check webhook configuration and logs
- Webhook may be blocking deletion
- May need to disable webhook temporarily or fix validation logic

**Conflict error:**
```
Error: the object has been modified; please apply your changes...
```
- Another controller is modifying the resource
- Wait and retry (operator will retry automatically)

### Step 3: Label Mismatch (-2)

Resource exists but does **not** have the required management label.

#### Verify Label

```bash
kubectl get <KIND> <NAME> -n <NAMESPACE> -o yaml | grep -A5 'labels:'
```

Expected label: `platform.kubevirt.io/managed-by: virt-platform-autopilot`

**Label missing or incorrect:**

This is the **safety mechanism working as intended**. The resource was likely:
1. Created manually by a user
2. Created by another operator
3. Pre-existing before autopilot was installed
4. Had the label removed manually

**Resolution:**

**Option A: Add the label (if resource should be managed by autopilot)**
```bash
kubectl label <KIND> <NAME> -n <NAMESPACE> \
  platform.kubevirt.io/managed-by=virt-platform-autopilot
```

On next reconciliation, the operator will delete it.

**Option B: Remove tombstone (if resource should NOT be managed)**

If this resource was never managed by autopilot, remove the tombstone definition:

1. Find tombstone file in `assets/tombstones/` directory
2. Remove the file:
   ```bash
   git rm assets/tombstones/<path-to-file>
   ```
3. Build and deploy updated operator image

**Option C: Manual deletion (immediate cleanup)**
```bash
kubectl delete <KIND> <NAME> -n <NAMESPACE>
```

Then remove tombstone file as in Option B.

## Remediation

### Automated Resolution

The operator retries tombstone deletion on every reconciliation (default: 5 minutes).

Once the underlying issue is fixed:
1. Operator will automatically delete the resource
2. Metric will update to `0` (deleted)
3. Alert will resolve after 30 minutes

### Manual Intervention

If automated resolution fails after 1 hour:

1. **Understand why tombstone exists**: Check git history for when tombstone was added
   ```bash
   git log --all --full-history -- "assets/tombstones/*/<filename>"
   ```

2. **Verify resource is truly obsolete**: Confirm the resource is no longer needed

3. **Force deletion** (if safe):
   ```bash
   # Remove finalizers if stuck
   kubectl patch <KIND> <NAME> -n <NAMESPACE> --type=json \
     -p='[{"op": "remove", "path": "/metadata/finalizers"}]'

   # Force delete
   kubectl delete <KIND> <NAME> -n <NAMESPACE> --force --grace-period=0
   ```
   **⚠️ WARNING:** Only use force deletion as a last resort!

4. **Remove tombstone** (if resource cannot be deleted):
   ```bash
   git rm assets/tombstones/<path-to-file>
   # Build and deploy updated operator
   ```

## Prevention

### For Developers

1. **Test tombstones in staging** before production release
2. **Ensure all managed resources have the label**:
   ```yaml
   labels:
     platform.kubevirt.io/managed-by: virt-platform-autopilot
   ```
3. **Run RBAC generator** when adding new resource types:
   ```bash
   make generate-rbac
   ```
4. **Document tombstone lifecycle** in commit messages

### For Operators

1. **Monitor tombstone metrics**:
   ```promql
   virt_platform_tombstone_status
   ```
2. **Set up alerts** (already configured)
3. **Review stuck tombstones** within 1 hour of alert
4. **Clean up old tombstones** - remove tombstone files after 2-3 releases

## Escalation

**Escalate if:**
- Tombstone has been stuck for > 4 hours
- Multiple tombstones are stuck simultaneously
- Deletion is blocking a critical upgrade
- Manual force deletion also fails

**Escalation path:**
1. Check #virt-platform-autopilot Slack channel
2. File GitHub issue with:
   - Resource details (kind, name, namespace)
   - Metric value (-1 or -2)
   - Logs from operator pod
   - Output of diagnostic commands
3. Tag on-call engineer if blocking production

## Additional Resources

- [Lifecycle Management Documentation](../LIFECYCLE_MANAGEMENT.md)
- [Tombstone Implementation](../../pkg/engine/tombstone.go)
- [Safety Label Specification](../../claude_assets/reclaiming_leftovers.md)
- [Kubernetes Finalizers](https://kubernetes.io/docs/concepts/overview/working-with-objects/finalizers/)

## Changelog

- 2026-02-12: Initial runbook created
