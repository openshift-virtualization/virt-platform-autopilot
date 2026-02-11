# VirtPlatformSyncFailed Runbook

## Alert Description

**Severity:** Critical
**Alert Name:** `VirtPlatformSyncFailed`

virt-platform-autopilot has failed to apply the Golden State to a managed resource for **15 minutes** or longer. This indicates the automation is broken and requires immediate attention.

## Symptom

The `virt_platform_compliance_status` metric has been set to `0` (drifted/failed) for more than 15 minutes for a specific resource.

**Alert Expression:**
```promql
virt_platform_compliance_status == 0
```

**Alert Duration:** `for: 15m`

## Impact

- The affected resource is not in the desired Golden State
- Platform automation is not functioning correctly for this resource
- Manual drift or configuration issues are not being automatically corrected
- Platform features may be degraded or disabled

## Common Causes

### 1. Validation Webhook Rejections

**Symptoms:**
- Operator logs show admission errors
- Webhook validation failures in events

**Diagnosis:**
```bash
# Check operator logs for webhook errors
kubectl logs -n openshift-cnv -l app=virt-platform-autopilot --tail=100 | grep -i "admission\|webhook"

# Check events for the affected resource
kubectl get events -n <namespace> --field-selector involvedObject.name=<resource-name>
```

**Resolution:**
- Review webhook policies (e.g., Gatekeeper, Kyverno)
- Check if OPA/admission controllers are blocking the change
- If policy is correct, add exemption for platform automation
- If policy is incorrect, update or disable it

### 2. Resource Conflicts / Race Conditions

**Symptoms:**
- Conflict errors in operator logs
- Another controller is modifying the same resource

**Diagnosis:**
```bash
# Check audit logs to see who else is updating the resource
oc adm node-logs <node> --path=kube-apiserver/audit.log | \
  grep "<resource-name>" | jq '.user'

# Check for thrashing metric (indicates edit war)
oc exec -n openshift-cnv deploy/virt-platform-autopilot -- \
  curl -s localhost:8080/metrics | grep virt_platform_thrashing_total
```

**Resolution:**
- Identify the conflicting controller/user
- If change is intentional: Apply `platform.kubevirt.io/mode: unmanaged` annotation
- If change is unintentional: Disable or fix the conflicting automation
- See also: [VirtPlatformThrashingDetected](./VirtPlatformThrashingDetected.md)

### 3. API Server Issues

**Symptoms:**
- Timeout errors in operator logs
- API server returning 5xx errors
- High latency in reconcile_duration metric

**Diagnosis:**
```bash
# Check API server health
kubectl get --raw /healthz
kubectl get --raw /readyz

# Check reconcile duration metrics (high values = API stress)
oc exec -n openshift-cnv deploy/virt-platform-autopilot -- \
  curl -s localhost:8080/metrics | grep virt_platform_reconcile_duration_seconds

# Check API server logs
oc adm node-logs <master-node> --path=kube-apiserver/kube-apiserver.log
```

**Resolution:**
- If API server is overloaded: Investigate cluster resource usage
- Check etcd health: `oc get etcd -o jsonpath='{.items[0].status.conditions[?(@.type=="EtcdMembersAvailable")].status}'`
- Consider scaling control plane if consistently overloaded

### 4. Missing CRD or API Resources

**Symptoms:**
- "no matches for kind" errors in operator logs
- CRD exists but APIService is unavailable

**Diagnosis:**
```bash
# Check if CRD exists
kubectl get crd <crd-name>

# Check APIService status
kubectl get apiservices

# Check dependency metric
oc exec -n openshift-cnv deploy/virt-platform-autopilot -- \
  curl -s localhost:8080/metrics | grep virt_platform_missing_dependency
```

**Resolution:**
- If CRD missing: Install the required operator
- If APIService unavailable: Check operator pod health
- See also: [VirtPlatformDependencyMissing](./VirtPlatformDependencyMissing.md)

### 5. Operator RBAC Issues

**Symptoms:**
- "Forbidden" errors in operator logs
- Permission denied when applying resources

**Diagnosis:**
```bash
# Check operator pod logs for RBAC errors
kubectl logs -n openshift-cnv -l app=virt-platform-autopilot | grep -i "forbidden\|rbac"

# Verify ServiceAccount permissions
kubectl auth can-i update <resource> --as=system:serviceaccount:openshift-cnv:virt-platform-autopilot
```

**Resolution:**
- Verify ClusterRole and ClusterRoleBinding are correctly deployed
- Check if custom RBAC policies are blocking the operator
- Reinstall operator if RBAC resources are missing

## Troubleshooting Steps

### Step 1: Check Operator Logs

```bash
# Get recent operator logs
kubectl logs -n openshift-cnv -l app=virt-platform-autopilot --tail=200

# Follow logs in real-time
kubectl logs -n openshift-cnv -l app=virt-platform-autopilot -f

# Look for specific error patterns
kubectl logs -n openshift-cnv -l app=virt-platform-autopilot | \
  grep -E "ERROR|Failed|failed to apply"
```

### Step 2: Inspect the Affected Resource

```bash
# Get the resource state
kubectl get <kind> <name> -n <namespace> -o yaml

# Check for conflicting annotations
kubectl get <kind> <name> -n <namespace> -o jsonpath='{.metadata.annotations}'

# Check resource events
kubectl get events -n <namespace> --field-selector involvedObject.name=<name>
```

### Step 3: Check Metrics

```bash
# Method 1: Port-forward (for interactive session)
kubectl port-forward -n openshift-cnv svc/virt-platform-autopilot-metrics 8080:8080
# Then in another terminal:
curl -s localhost:8080/metrics | grep "virt_platform_compliance_status"

# Method 2: Direct exec (one-off query)
oc exec -n openshift-cnv deploy/virt-platform-autopilot -- \
  curl -s localhost:8080/metrics | grep "virt_platform_compliance_status"

# Check for resources failing to sync (only show failures)
oc exec -n openshift-cnv deploy/virt-platform-autopilot -- \
  curl -s localhost:8080/metrics | grep "virt_platform_compliance_status" | grep '} 0$'

# Check for thrashing (edit wars)
oc exec -n openshift-cnv deploy/virt-platform-autopilot -- \
  curl -s localhost:8080/metrics | grep "virt_platform_thrashing_total"

# Check reconcile duration (API performance)
oc exec -n openshift-cnv deploy/virt-platform-autopilot -- \
  curl -s localhost:8080/metrics | grep "virt_platform_reconcile_duration_seconds"
```

### Step 4: Manual Reconciliation

If the issue is transient, trigger a manual reconciliation:

```bash
# Add annotation to force reconciliation
kubectl annotate <kind> <name> -n <namespace> \
  platform.kubevirt.io/force-reconcile="$(date +%s)"

# Watch for compliance status to change
watch -n 2 "oc exec -n openshift-cnv deploy/virt-platform-autopilot -- curl -s localhost:8080/metrics | grep virt_platform_compliance_status"
```

## Resolution Procedures

### If Change is Intentional (Stop Automation)

```bash
# Mark resource as unmanaged to stop automation
kubectl annotate <kind> <name> -n <namespace> \
  platform.kubevirt.io/mode=unmanaged

# Alert will resolve automatically within eval window
```

### If Change is Unintentional (Restore Golden State)

```bash
# Check if Golden State template has changed
kubectl get <kind> <name> -n <namespace> -o yaml > current.yaml
# Compare with assets/<category>/<resource>.yaml

# If template is correct, operator should auto-heal
# Monitor logs for success:
kubectl logs -n openshift-cnv -l app=virt-platform-autopilot -f | grep "successfully applied"
```

### If Operator is Stuck (Restart)

```bash
# Restart operator pod
kubectl delete pod -n openshift-cnv -l app=virt-platform-autopilot

# Watch operator restart and reconcile
kubectl get pods -n openshift-cnv -w
```

## Alert Resolution

The alert will automatically resolve when:

1. `virt_platform_compliance_status` returns to `1` (synced) for the affected resource
2. This state is maintained for the evaluation interval (30s)

**To verify resolution:**
```bash
# Check metric value (should return 1 for synced)
oc exec -n openshift-cnv deploy/virt-platform-autopilot -- \
  curl -s localhost:8080/metrics | \
  grep "virt_platform_compliance_status.*<kind>.*<name>" | \
  grep '} 1$'

# Or verify no resources are failing (should return empty)
oc exec -n openshift-cnv deploy/virt-platform-autopilot -- \
  curl -s localhost:8080/metrics | \
  grep "virt_platform_compliance_status" | grep '} 0$'
```

## Prevention

- Monitor `virt_platform_thrashing_total` metric to detect edit wars early
- Use `platform.kubevirt.io/mode: unmanaged` for intentional customizations
- Configure admission policies to allow platform automation
- Ensure API server has adequate resources for cluster size

## Related Alerts

- [VirtPlatformThrashingDetected](./VirtPlatformThrashingDetected.md) - Edit war indicator
- [VirtPlatformDependencyMissing](./VirtPlatformDependencyMissing.md) - Missing CRD indicator

## References

- [Platform Autopilot Architecture](../architecture.md)
- [Metrics Specification](../../claude_assets/metrics_plan.md)
- [Customization Guide](../customization.md)
