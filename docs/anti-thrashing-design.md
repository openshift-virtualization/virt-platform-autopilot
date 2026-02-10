# Anti-Thrashing Design: Pause-with-Annotation

## Problem Statement

When an external actor (user, another controller, automated script) repeatedly modifies a resource that the operator manages, we enter an "edit war" scenario. Without proper protection:

- **Alert flapping**: Metrics spike repeatedly, alerts fire/resolve in cycles
- **Resource waste**: Operator consumes reconciliation cycles fighting a losing battle
- **No resolution**: Conflict never stops until manually intervened
- **State loss**: Operator restart loses knowledge of the conflict

## Design Philosophy: Pause-with-Annotation

When an edit war is detected, **stop fighting and pause reconciliation** by setting an annotation on the conflicting resource. This is GitOps-friendly, self-documenting, and provides a clear recovery path.

### Why Pause Instead of Exponential Backoff?

**Exponential backoff approach** (rejected):
- ❌ Delays the problem, doesn't solve it
- ❌ Alerts still flap (metric increments on every retry)
- ❌ Wastes reconciliation cycles (keeps trying in background)
- ❌ State lost on operator restart (backoff timers reset)

**Pause-with-annotation approach** (chosen):
- ✅ Zero wasted cycles (reconciliation fully stopped)
- ✅ Stable metrics and alerts (one increment, stays high until resolved)
- ✅ Survives operator restarts (annotation persists in etcd)
- ✅ Clear recovery path (remove annotation or fix conflict)
- ✅ GitOps-friendly (self-documenting in Git history)
- ✅ Self-healing (removing annotation resumes reconciliation)

## Architecture: Two-Level Protection

### Level 1: TokenBucket (Short-term)
**Purpose**: Protect API server from rapid reconciliation storms

**Behavior**:
- Capacity: 5 updates per 1-minute window
- Refills after window expires
- Returns `ThrottledError` when exhausted

**Use case**: Temporary spikes (e.g., user making quick edits, flapping external controller)

### Level 2: ThrashingDetector (Long-term)
**Purpose**: Detect persistent edit wars and pause reconciliation

**Behavior**:
- Tracks consecutive throttle events
- Threshold: 3 consecutive throttles = edit war detected
- Action: Set `platform.kubevirt.io/reconcile-paused=true` annotation
- Metric: Increment `virt_platform_thrashing_total` **once**
- Recovery: User removes annotation to resume

**Use case**: Persistent conflicts (external controller, automated scripts, user repeatedly modifying)

## Implementation

### Threshold Calculation

```
3 throttles × 5 updates/throttle = 15 modifications in ~1 minute
```

This means a resource was modified **15 times within 1 minute** before pausing. This is clearly an edit war, not normal user behavior.

### Patcher Integration

```go
// Step 1.5: Check if reconciliation is paused due to edit war
if liveExists && overrides.IsPaused(live) {
    logger.Info("Reconciliation paused due to edit war detection",
        "name", assetMeta.Name,
        "kind", desired.GetKind(),
        "namespace", desired.GetNamespace(),
        "objectName", desired.GetName(),
    )
    // Skip reconciliation - annotation is self-documenting
    return false, nil
}

// Step 6: Anti-thrashing gate (two-level protection)
if err := p.throttle.Record(resourceKey); err != nil {
    if throttling.IsThrottled(err) {
        // Token bucket exhausted - check thrashing detector
        shouldPause := p.thrashingDetector.RecordThrottle(resourceKey)

        if shouldPause {
            // Edit war detected - pause reconciliation
            logger.Info("Edit war detected, pausing reconciliation",
                "key", resourceKey,
                "attempts", p.thrashingDetector.GetAttempts(resourceKey),
            )

            // Emit metric only once when threshold is reached
            if p.thrashingDetector.ShouldEmitMetric(resourceKey) {
                observability.IncThrashing(desired)
            }

            // Set pause annotation on live object
            if liveExists {
                if err := p.setPauseAnnotation(ctx, live); err != nil {
                    logger.Error(err, "Failed to set pause annotation")
                }
            }

            // Record event explaining the pause and recovery steps
            if p.eventRecorder != nil {
                p.eventRecorder.ThrashingDetected(
                    renderCtx.HCO,
                    desired.GetKind(),
                    desired.GetNamespace(),
                    desired.GetName(),
                    p.thrashingDetector.GetAttempts(resourceKey),
                )
            }

            return false, fmt.Errorf("reconciliation paused due to edit war")
        }

        // First or second throttle - log and continue
        logger.Info("Asset update throttled (anti-thrashing)", "attempts", ...)
        return false, err
    }
}

// Step 8: Record success (resets thrashing state)
if applied {
    p.thrashingDetector.RecordSuccess(resourceKey)
}
```

## Metric Behavior

### Stable Metrics (No Flapping)

```prometheus
# Before edit war
virt_platform_thrashing_total{kind="ConfigMap",name="my-cm",namespace="default"} 0

# Edit war detected (3rd throttle hit)
virt_platform_thrashing_total{kind="ConfigMap",name="my-cm",namespace="default"} 1

# Subsequent reconciliation attempts (while paused) - NO INCREMENT
virt_platform_thrashing_total{kind="ConfigMap",name="my-cm",namespace="default"} 1

# ... stays at 1 until conflict is resolved or annotation is removed
```

**Key improvement**: Metric increments **once** when thrashing is detected, then stays stable. This makes alerts reliable:

```yaml
- alert: VirtPlatformThrashingDetected
  expr: virt_platform_thrashing_total > 0
  for: 5m
  labels:
    severity: warning
  annotations:
    summary: "Edit war detected on {{ $labels.kind }}/{{ $labels.name }}"
    description: |
      The operator detected conflicting updates on {{ $labels.kind }}
      {{ $labels.namespace }}/{{ $labels.name }}. Reconciliation has been
      paused to prevent infinite loops.

      Check the resource for annotation:
        platform.kubevirt.io/reconcile-paused: "true"

      Resolution:
      1. Identify the conflicting actor (check audit logs, ArgoCD, other controllers)
      2. Either:
         - Stop the conflicting actor
         - OR set 'platform.kubevirt.io/mode: unmanaged' if external management is intentional
      3. Remove 'platform.kubevirt.io/reconcile-paused' annotation to resume
```

## Event Examples

### Short-term Throttle (Token Bucket)
```yaml
Type: Warning
Reason: Throttled
Message: |
  Update throttled for ConfigMap/default/my-cm (limit: 5 updates per 1m).
  This is temporary protection against rapid updates.
```

### Long-term Thrashing (Edit War)
```yaml
Type: Warning
Reason: ThrashingDetected
Message: |
  Edit war detected for ConfigMap/default/my-cm after 3 consecutive throttles.
  Reconciliation paused. Another actor is modifying this resource, conflicting
  with operator management. Remove annotation 'platform.kubevirt.io/reconcile-paused=true'
  to resume, or set 'platform.kubevirt.io/mode=unmanaged' if external management
  is intentional.
```

## Recovery Procedures

### 1. Identify the Conflicting Actor

Check Kubernetes audit logs to find who's modifying the resource:

```bash
# Find recent modifications to the resource
kubectl get events -n <namespace> --sort-by='.lastTimestamp' | grep <resource-name>

# Check audit logs (if enabled)
kubectl logs -n kube-system kube-apiserver-* | grep <resource-name>
```

Common culprits:
- Another operator or controller
- ArgoCD or other GitOps tools
- Automated scripts or CI/CD pipelines
- Users manually editing via kubectl

### 2. Resolve the Conflict

**Option A: Stop the conflicting actor**
```bash
# Example: Scale down conflicting deployment
kubectl scale deployment conflicting-controller --replicas=0 -n <namespace>

# Remove pause annotation to resume
kubectl annotate <kind> <name> platform.kubevirt.io/reconcile-paused- -n <namespace>
```

**Option B: Mark resource as unmanaged**
```bash
# If external management is intentional, mark resource as unmanaged
kubectl annotate <kind> <name> platform.kubevirt.io/mode=unmanaged -n <namespace>

# This tells the operator to stop managing this resource entirely
```

**Option C: Fix the underlying issue**
```bash
# Example: Update HyperConverged CR to match external configuration
kubectl edit hyperconverged kubevirt-hyperconverged -n kubevirt-hyperconverged

# Remove pause annotation to resume
kubectl annotate <kind> <name> platform.kubevirt.io/reconcile-paused- -n <namespace>
```

### 3. Verify Recovery

After removing the pause annotation:

```bash
# Check that operator resumes reconciliation
kubectl get events -n <namespace> --sort-by='.lastTimestamp' | grep <resource-name>

# Verify pause annotation is gone
kubectl get <kind> <name> -n <namespace> -o jsonpath='{.metadata.annotations}'

# Check that metric doesn't increment again (stable)
curl -s http://localhost:8080/metrics | grep virt_platform_thrashing_total
```

## GitOps Benefits

The pause annotation creates a **self-documenting audit trail** in Git:

```yaml
# In your GitOps repo, you'll see:
apiVersion: v1
kind: ConfigMap
metadata:
  name: my-cm
  namespace: default
  annotations:
    # This annotation appears when operator detects edit war
    platform.kubevirt.io/reconcile-paused: "true"

    # Git commit shows when it was added:
    # commit 7f3a1b2c - "operator: pause reconciliation due to edit war"
    # Date: 2026-02-10 18:30:00
```

This makes it easy to:
- See exactly when the conflict started
- Track who resolved it and how
- Understand the history of the conflict

## Testing

### Unit Tests
See `pkg/throttling/thrashing_detector_test.go` (11 tests):
- Threshold detection (3 throttles trigger pause)
- Metric emission (only once per episode)
- Success reset
- Concurrent access safety

### Integration Tests
See `test/anti_thrashing_integration_test.go` (multiple scenarios):
- Pause annotation behavior
- Reconciliation skipping while paused
- Recovery by removing annotation
- Metric stability

### E2E Tests
See `test/e2e/anti_thrashing_e2e_test.go`:
- Full workflow with running operator
- External actor fighting operator
- Event recording
- Prometheus metric validation

## Benefits Summary

1. **Stable Alerts**: Fire once, stay firing, resolve cleanly (no flapping)
2. **Resource Efficiency**: Zero reconciliation cycles wasted while paused
3. **Clear User Feedback**: Events explain what happened and how to recover
4. **Automatic Recovery**: Resets on successful reconciliation after annotation removal
5. **GitOps-Friendly**: Self-documenting in Git history
6. **Survives Restarts**: Annotation persists in etcd, operator remembers after restart
7. **Actionable**: Users know exactly what to do (remove annotation or mark unmanaged)
