# VirtPlatformThrashingDetected Runbook

## Alert Description

**Severity:** Warning
**Alert Name:** `VirtPlatformThrashingDetected`

virt-platform-autopilot has detected an "**Edit War**" on a managed resource. Another controller or user is repeatedly modifying the resource, conflicting with the autopilot's desired Golden State. Automation has been **paused** (throttled reconciliation) to protect the API server from thrashing.

## Symptom

The `virt_platform_thrashing_total` counter has increased by more than **5 events** in a **10-minute window** for a specific resource.

**Alert Expression:**
```promql
increase(virt_platform_thrashing_total[10m]) > 5
```

**Alert Duration:** Immediate (no `for` clause - fires on first evaluation when threshold exceeded)

## Impact

- **Automation paused**: The operator has stopped actively reconciling this resource
- **Drift detection disabled**: Manual changes will NOT be auto-corrected
- **Golden State suspended**: Resource may drift from desired configuration
- **API server protection**: Prevents infinite reconciliation loops

**Note:** This is a **safety mechanism** - the operator is preventing harm, not broken.

## Common Causes

### 1. Conflicting Automation (Controllers)

**Symptoms:**
- Multiple controllers managing the same field
- Competing desired states
- Rapid back-and-forth updates

**Example:**
- Autopilot sets `spec.replicas: 3`
- HPA controller sets `spec.replicas: 5`
- Autopilot reverts to `3`
- HPA reverts to `5`
- Thrashing counter increments

**Diagnosis:**
```bash
# Check audit logs to identify all actors modifying the resource
oc adm node-logs <master-node> --path=kube-apiserver/audit.log | \
  jq 'select(.objectRef.name=="<resource-name>") | {user: .user.username, time: .requestReceivedTimestamp, verb: .verb}'

# Look for non-operator updates
# Expected: system:serviceaccount:openshift-cnv:virt-platform-autopilot
# Unexpected: Other controllers, users, or operators
```

**Resolution:**

**Option A: Disable Conflicting Controller** (if change is unintentional)
```bash
# Example: Disable HPA if it's conflicting
kubectl delete hpa <hpa-name> -n <namespace>
```

**Option B: Mark Resource as Unmanaged** (if change is intentional)
```bash
# Stop autopilot from managing this resource
kubectl annotate <kind> <name> -n <namespace> \
  platform.kubevirt.io/mode=unmanaged

# Alert will resolve automatically
# Thrashing counter will stop incrementing
```

**Option C: Use Strategic Patch** (if partial management is needed)
```bash
# Apply a patch annotation to customize specific fields
# while allowing autopilot to manage others
kubectl annotate <kind> <name> -n <namespace> \
  platform.kubevirt.io/patch='{"spec": {"replicas": 5}}'

# This tells autopilot to merge this patch with the Golden State
```

### 2. User Manual Edits

**Symptoms:**
- Users repeatedly applying `kubectl apply` or `kubectl edit`
- GitOps tools (Flux, ArgoCD) managing the same resource

**Diagnosis:**
```bash
# Check audit logs for user/tool identity
oc adm node-logs <master-node> --path=kube-apiserver/audit.log | \
  grep "<resource-name>" | jq '.user.username' | sort | uniq -c

# Look for non-system users or GitOps service accounts
```

**Resolution:**

**Option A: Educate Users**
- Inform users that the resource is platform-managed
- Direct them to use customization annotations instead of direct edits

**Option B: Use GitOps Integration** (future enhancement)
```bash
# If using GitOps, configure autopilot to respect external source
kubectl annotate <kind> <name> -n <namespace> \
  platform.kubevirt.io/mode=unmanaged

# Then let GitOps tool manage the resource
```

### 3. Flapping Validation Webhooks

**Symptoms:**
- Webhook sometimes allows update, sometimes rejects
- Intermittent validation failures
- Eventual consistency issues

**Diagnosis:**
```bash
# Check operator logs for webhook rejections
kubectl logs -n openshift-cnv -l app=virt-platform-autopilot | \
  grep -i "webhook\|admission" | tail -50

# Check webhook health
kubectl get validatingwebhookconfigurations
kubectl get mutatingwebhookconfigurations
```

**Resolution:**
```bash
# Identify problematic webhook
kubectl get events --all-namespaces | grep "FailedAdmissionWebhook"

# Options:
# 1. Fix webhook logic to be deterministic
# 2. Add exemption for platform automation
# 3. Disable flaky webhook if not critical
```

### 4. Resource Lock Contention

**Symptoms:**
- Optimistic lock failures
- ResourceVersion conflicts
- Retry storms

**Diagnosis:**
```bash
# Check logs for conflict errors
kubectl logs -n openshift-cnv -l app=virt-platform-autopilot | \
  grep -i "conflict\|resourceVersion" | tail -50
```

**Resolution:**
- Usually transient - wait for contention to resolve
- If persistent, investigate why multiple actors are racing
- Apply conflict resolution strategy (see Option A/B/C above)

## Troubleshooting Steps

### Step 1: Identify the Edit War Participants

```bash
# Get audit logs for the last hour
oc adm node-logs <master-node> --path=kube-apiserver/audit.log | \
  jq -r 'select(.objectRef.name=="<resource-name>" and .verb=="update" or .verb=="patch") |
         "\(.requestReceivedTimestamp) \(.user.username) \(.verb)"' | \
  tail -100

# Expected output:
# 2026-02-11T10:00:00Z system:serviceaccount:openshift-cnv:virt-platform-autopilot patch
# 2026-02-11T10:00:05Z system:serviceaccount:openshift:hpa-controller update
# 2026-02-11T10:00:10Z system:serviceaccount:openshift-cnv:virt-platform-autopilot patch
# 2026-02-11T10:00:15Z system:serviceaccount:openshift:hpa-controller update
# ^ This pattern indicates HPA is fighting with autopilot
```

### Step 2: Compare Desired States

```bash
# Get current resource state
kubectl get <kind> <name> -n <namespace> -o yaml > current.yaml

# Get autopilot's desired state (Golden State)
# Located in assets/<category>/<resource>.yaml

# Compare the two
diff -u current.yaml assets/<category>/<resource>.yaml

# Look for fields being changed by the conflicting actor
```

### Step 3: Check Thrashing Metrics

```bash
# Query thrashing events for the resource
oc exec -n openshift-cnv deploy/virt-platform-autopilot -- \
  curl -s localhost:8080/metrics | \
  grep "virt_platform_thrashing_total.*<kind>.*<name>"

# Example output:
# virt_platform_thrashing_total{kind="HyperConverged",name="kubevirt-hyperconverged",namespace="openshift-cnv"} 12
# ^ 12 thrashing events have occurred

# Show only resources with thrashing (counter > 0)
oc exec -n openshift-cnv deploy/virt-platform-autopilot -- \
  curl -s localhost:8080/metrics | \
  grep "virt_platform_thrashing_total" | grep -v '} 0$'
```

### Step 4: Review Token Bucket State

The operator uses a token bucket to throttle reconciliation. When the bucket is exhausted, thrashing counter increments.

```bash
# Check operator logs for throttling messages
kubectl logs -n openshift-cnv -l app=virt-platform-autopilot | \
  grep -i "throttl\|token bucket\|rate limit"

# Example log:
# "Token bucket exhausted for HyperConverged/kubevirt-hyperconverged, skipping reconcile"
```

## Resolution Procedures

### Resolution Option A: Disable Conflicting Controller

If the external change is **unintentional** (bug, misconfiguration):

```bash
# Identify the conflicting controller from audit logs
# Example: kube-controller-manager/hpa-controller

# Disable or fix the controller
# For HPA example:
kubectl delete hpa <hpa-name> -n <namespace>

# Monitor for thrashing to stop (watch the counter for specific resource)
watch -n 5 "oc exec -n openshift-cnv deploy/virt-platform-autopilot -- curl -s localhost:8080/metrics | grep 'virt_platform_thrashing_total.*<kind>.*<name>'"
```

### Resolution Option B: Mark as Unmanaged

If the external change is **intentional** (you want another tool to manage it):

```bash
# Stop autopilot from managing this resource
kubectl annotate <kind> <name> -n <namespace> \
  platform.kubevirt.io/mode=unmanaged \
  --overwrite

# Verify annotation is set
kubectl get <kind> <name> -n <namespace> -o jsonpath='{.metadata.annotations}'

# Alert will resolve within 10 minutes (evaluation window)
# Thrashing counter will stop incrementing
```

### Resolution Option C: Use Customization Patch

If you want **partial management** (autopilot manages most fields, you customize specific ones):

```bash
# Apply strategic patch annotation
kubectl annotate <kind> <name> -n <namespace> \
  platform.kubevirt.io/patch='{"spec": {"yourField": "yourValue"}}' \
  --overwrite

# Autopilot will merge your patch with the Golden State
# No edit war - both changes coexist
```

### Resolution Option D: Fix Webhook Logic

If the thrashing is caused by flapping webhooks:

```bash
# Identify webhook
kubectl get validatingwebhookconfigurations -o yaml | grep -A 10 "<resource-kind>"

# Fix webhook to be deterministic
# Ensure validation logic doesn't depend on time, randomness, or external state

# Or temporarily disable webhook
kubectl delete validatingwebhookconfiguration <webhook-name>
```

## Alert Resolution

The alert will automatically resolve when:

1. `increase(virt_platform_thrashing_total[10m])` drops to ≤ 5 events
2. Meaning: Fewer than 6 thrashing events in the last 10 minutes

**To verify resolution:**
```bash
# Check recent thrashing rate
oc exec -n openshift-cnv deploy/virt-platform-autopilot -- \
  curl -s localhost:8080/metrics | \
  grep "virt_platform_thrashing_total.*<kind>.*<name>"

# Wait 10 minutes, query again
# If counter stops incrementing, alert will resolve
```

## Prevention

### 1. Document Platform-Managed Resources

Maintain a list of resources managed by autopilot:
```bash
# Query all managed resources
kubectl get all --all-namespaces -o json | \
  jq '.items[] | select(.metadata.annotations["platform.kubevirt.io/managed"]=="true") |
      {kind: .kind, name: .metadata.name, namespace: .metadata.namespace}'
```

### 2. Use Customization Annotations

Instead of direct edits, use customization annotations:
```bash
# For patches (strategic merge)
kubectl annotate <kind> <name> platform.kubevirt.io/patch='<json-patch>'

# For ignore (skip specific fields)
kubectl annotate <kind> <name> platform.kubevirt.io/ignore='spec.replicas,spec.template.spec.tolerations'

# For unmanaged (full ownership transfer)
kubectl annotate <kind> <name> platform.kubevirt.io/mode=unmanaged
```

### 3. Admission Policy Exemptions

Configure validation webhooks to allow platform automation:
```yaml
# Example Kyverno policy
apiVersion: kyverno.io/v1
kind: ClusterPolicy
metadata:
  name: platform-autopilot-exemption
spec:
  rules:
    - match:
        subjects:
          - kind: ServiceAccount
            name: virt-platform-autopilot
            namespace: openshift-cnv
      exclude: true
```

## Related Alerts

- [VirtPlatformSyncFailed](./VirtPlatformSyncFailed.md) - Sync failure indicator
- [VirtPlatformDependencyMissing](./VirtPlatformDependencyMissing.md) - Missing CRD indicator

## References

- [Anti-Thrashing Design](../../claude_assets/throttling_design.md)
- [Customization Guide](../customization.md)
- [Token Bucket Algorithm](https://en.wikipedia.org/wiki/Token_bucket)
