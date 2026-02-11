# VirtPlatformDependencyMissing Runbook

## Alert Description

**Severity:** Warning
**Alert Name:** `VirtPlatformDependencyMissing`

virt-platform-autopilot has detected that an **optional CRD (soft dependency)** is missing from the cluster. Related platform features will not be configured until the CRD is installed.

## Symptom

The `virt_platform_missing_dependency` metric has been set to `1` (missing) for more than **5 minutes** for a specific CRD.

**Alert Expression:**
```promql
virt_platform_missing_dependency == 1
```

**Alert Duration:** `for: 5m`

## Impact

**This is a WARNING, not a critical failure.** The cluster is functional, but feature-incomplete.

- **Missing CRD:** The specified CustomResourceDefinition is not installed
- **Feature degraded:** Platform features requiring this CRD will be skipped
- **Automation limited:** Assets depending on this CRD will not be created
- **Informational only:** The operator will continue managing other resources normally

**Philosophy:** "Inform, Don't Crash"
- The operator detects the missing CRD and logs a warning
- Assets requiring the CRD are gracefully skipped
- Other assets continue to be managed successfully
- When the CRD appears, automation resumes automatically

## Common Missing Dependencies

### 1. KubeDescheduler (Load-Aware Scheduling)

**CRD:** `kubedeschedulers.operator.openshift.io`
**Provided by:** `cluster-kube-descheduler-operator`

**Features affected:**
- LoadAware scheduling profile
- VM workload rebalancing

**Check if installed:**
```bash
# Check CRD exists
kubectl get crd kubedeschedulers.operator.openshift.io

# Check operator is installed
oc get clusteroperator kube-descheduler-operator

# Check descheduler instance
kubectl get kubedescheduler cluster -n openshift-kube-descheduler-operator
```

**Install if missing:**
```bash
# Install via OperatorHub (OpenShift Console)
# Or via CLI:
cat <<EOF | oc apply -f -
apiVersion: operators.coreos.com/v1alpha1
kind: Subscription
metadata:
  name: cluster-kube-descheduler-operator
  namespace: openshift-kube-descheduler-operator
spec:
  channel: stable
  name: cluster-kube-descheduler-operator
  source: redhat-operators
  sourceNamespace: openshift-marketplace
EOF
```

### 2. NodeHealthCheck (Node Auto-Remediation)

**CRD:** `nodehealthchecks.remediation.medik8s.io`
**Provided by:** `node-healthcheck-operator` (via MediK8s)

**Features affected:**
- Automatic node health monitoring
- Self-node remediation integration
- Fence-agents remediation integration
- Unhealthy node detection

**Check if installed:**
```bash
# Check CRD exists
kubectl get crd nodehealthchecks.remediation.medik8s.io

# Check operator is installed
oc get csv -n openshift-operators | grep node-healthcheck

# Check NHC instance
kubectl get nodehealthcheck -A
```

**Install if missing:**
```bash
# Install via OperatorHub (OpenShift Console)
# Search for "Node Health Check Operator"
# Or via CLI:
cat <<EOF | oc apply -f -
apiVersion: operators.coreos.com/v1alpha1
kind: Subscription
metadata:
  name: node-healthcheck-operator
  namespace: openshift-operators
spec:
  channel: stable
  name: node-healthcheck-operator
  source: redhat-operators
  sourceNamespace: openshift-marketplace
EOF
```

### 3. MetalLB (LoadBalancer Services)

**CRD:** `metallbs.metallb.io`
**Provided by:** `metallb-operator`

**Features affected:**
- LoadBalancer service type support
- IP address management for VMs
- Bare-metal ingress connectivity

**Check if installed:**
```bash
# Check CRD exists
kubectl get crd metallbs.metallb.io

# Check operator is installed
oc get csv -n metallb-system | grep metallb-operator

# Check MetalLB instance
kubectl get metallb -n metallb-system
```

**Install if missing:**
```bash
# Install via OperatorHub (OpenShift Console)
# Or via CLI:
cat <<EOF | oc apply -f -
apiVersion: operators.coreos.com/v1alpha1
kind: Subscription
metadata:
  name: metallb-operator
  namespace: metallb-system
spec:
  channel: stable
  name: metallb-operator
  source: redhat-operators
  sourceNamespace: openshift-marketplace
EOF
```

### 5. Forklift (VM Migration)

**CRD:** `forkliftcontrollers.forklift.konveyor.io`
**Provided by:** `forklift-operator` (Migration Toolkit for Virtualization)

**Features affected:**
- VM migration from VMware/oVirt
- Migration plan management
- Migration validation
- Live Cross-Cluster Migration

**Check if installed:**
```bash
# Check CRD exists
kubectl get crd forkliftcontrollers.forklift.konveyor.io

# Check operator is installed
oc get csv -n openshift-mtv | grep forklift-operator
```

**Install if missing:**
```bash
# Install via OperatorHub (OpenShift Console)
# Search for "Migration Toolkit for Virtualization"
```

## Troubleshooting Steps

### Step 1: Identify Missing CRD

```bash
# Check which CRD is missing from alert labels
# Alert will include: group, version, kind labels

# Query metric to see all missing dependencies
oc exec -n openshift-cnv deploy/virt-platform-autopilot -- \
  curl -s localhost:8080/metrics | grep virt_platform_missing_dependency

# Show only missing dependencies (value=1)
oc exec -n openshift-cnv deploy/virt-platform-autopilot -- \
  curl -s localhost:8080/metrics | grep virt_platform_missing_dependency | grep '} 1$'

# Example output:
# virt_platform_missing_dependency{group="operator.openshift.io",kind="KubeDescheduler",version="v1"} 1
```

### Step 2: Determine if CRD Should Be Installed

```bash
# Check cluster capabilities
oc get clusterversion version -o jsonpath='{.status.capabilities}'

# Check if operator is available
oc get packagemanifests -n openshift-marketplace | grep <operator-name>

# Check cluster size and use case
# Small dev cluster: May not need all features
# Production cluster: Should have full stack
```

### Step 3: Check Operator Health (if supposed to be installed)

```bash
# List all installed operators
oc get csv -A

# Check specific operator status
oc get csv -n <namespace> <csv-name> -o yaml

# Check operator pod health
oc get pods -n <namespace> | grep <operator-name>
oc logs -n <namespace> <operator-pod>
```

### Step 4: Review Autopilot Logs

```bash
# Check operator logs for CRD detection
kubectl logs -n openshift-cnv -l app=virt-platform-autopilot | \
  grep -i "crd.*missing\|dependency\|not found"

# Example log:
# "Skipping asset NodeHealthCheck/virt-workers: CRD nodehealthchecks.remediation.medik8s.io not found"
```

## Resolution Procedures

### Option 1: Install the Missing Operator

If the feature is desired, install the operator providing the CRD:

```bash
# Example: Installing kube-descheduler-operator
cat <<EOF | oc apply -f -
apiVersion: operators.coreos.com/v1alpha1
kind: Subscription
metadata:
  name: cluster-kube-descheduler-operator
  namespace: openshift-kube-descheduler-operator
spec:
  channel: stable
  name: cluster-kube-descheduler-operator
  source: redhat-operators
  sourceNamespace: openshift-marketplace
EOF

# Wait for operator to install
oc wait --for=condition=AtLatestKnown subscription/cluster-kube-descheduler-operator \
  -n openshift-kube-descheduler-operator --timeout=300s

# Verify CRD is installed
kubectl get crd kubedeschedulers.operator.openshift.io

# Autopilot will automatically detect the CRD and resume managing related assets
# Alert will resolve within 5 minutes
```

### Option 2: Silence the Alert (Intentionally Not Installing)

If the feature is not needed (e.g., dev cluster, specific use case):

**Method A: Disable Monitoring for This CRD**
```bash
# Create a PrometheusRule to suppress the alert for specific CRDs
cat <<EOF | oc apply -f -
apiVersion: monitoring.coreos.com/v1
kind: PrometheusRule
metadata:
  name: virt-platform-autopilot-alert-suppressions
  namespace: openshift-cnv
spec:
  groups:
    - name: virt-platform-autopilot.suppressions
      rules:
        - alert: VirtPlatformDependencyMissingSuppressed
          expr: |
            virt_platform_missing_dependency{kind="KubeDescheduler"} == 1
          labels:
            severity: none
EOF
```

**Method B: Accept the Warning**
- The alert is informational - safe to ignore if feature not needed
- Does not affect other platform functionality
- Can be acknowledged in Alertmanager

### Option 3: Mark Related Assets as Unmanaged

If you want to manage the affected resources manually:

```bash
# Example: Unmanage NodeHealthCheck resources
kubectl annotate nodehealthcheck virt-workers -n openshift-operators \
  platform.kubevirt.io/mode=unmanaged \
  --overwrite

# Autopilot will skip these assets, metric will clear
```

## Alert Resolution

The alert will automatically resolve when:

1. The missing CRD is installed in the cluster
2. The operator detects the CRD via discovery
3. `virt_platform_missing_dependency` metric changes to `0` (present)
4. This state is maintained for the evaluation interval (30s)

**To verify resolution:**
```bash
# Check CRD exists
kubectl get crd <crd-name>

# Check metric value (should be 0 for present)
oc exec -n openshift-cnv deploy/virt-platform-autopilot -- \
  curl -s localhost:8080/metrics | \
  grep "virt_platform_missing_dependency.*<kind>" | \
  grep '} 0$'

# Or verify no missing dependencies (should return empty)
oc exec -n openshift-cnv deploy/virt-platform-autopilot -- \
  curl -s localhost:8080/metrics | \
  grep virt_platform_missing_dependency | grep '} 1$'

# Example:
# virt_platform_missing_dependency{group="operator.openshift.io",kind="KubeDescheduler",version="v1"} 0
```

## Prevention

### 1. Pre-Install Optional Operators

For production clusters, install the full stack:

```bash
# Install all recommended operators during cluster setup
# - cluster-kube-descheduler-operator
# - node-healthcheck-operator
# - metallb-operator (if bare-metal)
# - forklift-operator (if migration needed)
```

### 2. Monitor CRD Installation

```bash
# List all CRDs required by virt-platform-autopilot
kubectl get crd | grep -E "kubedescheduler|nodehealthcheck|metallb|machineconfig|forklift"
```

## Related Alerts

- [VirtPlatformSyncFailed](./VirtPlatformSyncFailed.md) - Sync failure indicator (this alert may fire if missing CRD causes asset apply to fail)

## References

- [Soft Dependency Design](../../claude_assets/architecture.md)
- [Operator Catalog](https://docs.openshift.com/container-platform/latest/operators/understanding/olm-understanding-operatorhub.html)
- [CRD Discovery](https://kubernetes.io/docs/tasks/extend-kubernetes/custom-resources/custom-resource-definitions/)
