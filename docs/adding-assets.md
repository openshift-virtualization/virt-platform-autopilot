# Adding Assets Guide

This guide explains how to extend the virt-platform-autopilot by adding new managed resources (assets).

## Philosophy

Adding new assets to the autopilot requires **no code changes** - everything is template-driven:

- ✅ **No code required**: Create YAML templates, update metadata catalog
- ✅ **Template-driven**: Use Go templates for dynamic rendering
- ✅ **Soft dependencies**: Gracefully handle missing CRDs
- ✅ **Declarative**: Define conditions for when assets should be applied

## Quick Start

Follow these steps to add a new asset:

### 1. Create Template File

Create a YAML file in the appropriate subdirectory under `assets/active/`:

```bash
# Choose the right category
assets/active/
├── hco/              # HyperConverged resource (only one, order: 0)
├── machine-config/   # MachineConfig resources
├── kubelet/          # KubeletConfig resources
├── node-health/      # NodeHealthCheck resources
├── descheduler/      # Descheduler resources
└── operators/        # Third-party operator CRs

# Create your asset
vi assets/active/machine-config/05-my-feature.yaml
```

### 2. Add Entry to Metadata Catalog

Edit `assets/active/metadata.yaml` and add your asset:

```yaml
assets:
  # ... existing assets ...

  - name: my-new-feature
    path: active/machine-config/05-my-feature.yaml
    phase: 1
    install: always  # or opt-in
    component: MachineConfig
    reconcile_order: 1
    conditions: []  # or add conditions (see below)
```

### 3. Test with Render Command

Test your asset offline before deploying:

```bash
# Render all assets including your new one
virt-platform-autopilot render --hco-file=test-hco.yaml --output=yaml

# Or render just your asset
virt-platform-autopilot render --hco-file=test-hco.yaml --output=yaml | grep -A50 "my-new-feature"
```

### 4. (Optional) Add Conditions

If your asset should only be applied in specific scenarios, add conditions:

```yaml
conditions:
  # Annotation-based activation
  - type: annotation
    key: platform.kubevirt.io/enable-my-feature
    value: "true"

  # Hardware detection
  - type: hardware-detection
    detector: gpuPresent

  # Feature gate
  - type: feature-gate
    value: MyFeature
```

### 5. Update RBAC

If your asset introduces new resource types, regenerate RBAC:

```bash
make generate-rbac
```

This scans all assets and generates the necessary ClusterRole permissions.

## Template Examples

### Example 1: Simple Static YAML

For resources that don't need dynamic values:

**File:** `assets/active/node-health/standard-remediation.yaml`

```yaml
apiVersion: remediation.medik8s.io/v1alpha1
kind: NodeHealthCheck
metadata:
  name: virt-node-health-check
  namespace: openshift-operators
spec:
  minHealthy: 51%
  remediationTemplate:
    apiVersion: self-node-remediation.medik8s.io/v1alpha1
    kind: SelfNodeRemediationTemplate
    name: self-node-remediation-automatic-strategy-template
    namespace: openshift-operators
  selector:
    matchExpressions:
      - key: node-role.kubernetes.io/worker
        operator: Exists
  unhealthyConditions:
    - duration: 5m
      status: "False"
      type: Ready
    - duration: 5m
      status: Unknown
      type: Ready
```

No templating needed - this is applied as-is.

### Example 2: Templated with HCO Context

Use `.HCO.Object` to access HyperConverged resource fields:

**File:** `assets/active/descheduler/eviction-limits.yaml.tpl`

```yaml
apiVersion: operator.openshift.io/v1
kind: KubeDescheduler
metadata:
  name: cluster
  namespace: openshift-kube-descheduler-operator
spec:
  managementState: Managed
  mode: Automatic
  deschedulingIntervalSeconds: 60
  evictionLimits:
    # Read from HCO spec with default fallback
    {{- $migTotal := dig "spec" "liveMigrationConfig" "parallelMigrationsPerCluster" 5 .HCO.Object }}
    total: {{ $migTotal }}
    {{- $migNode := dig "spec" "liveMigrationConfig" "parallelOutboundMigrationsPerNode" 2 .HCO.Object }}
    node: {{ $migNode }}
```

**Template functions available:**
- `dig`: Safely access nested fields with defaults
- `.HCO.Object`: Access HyperConverged resource
- `.HCO.Namespace`: HCO namespace
- `.HCO.Name`: HCO name

### Example 3: Conditional Rendering

Skip rendering entirely if conditions aren't met:

```yaml
{{- if crdHasEnum "kubedeschedulers.operator.openshift.io" "spec.profiles" "KubeVirtRelieveAndMigrate" }}
apiVersion: operator.openshift.io/v1
kind: KubeDescheduler
metadata:
  name: cluster
spec:
  profiles:
    - KubeVirtRelieveAndMigrate
{{- else }}
# Fallback configuration or skip entirely
{{- end }}
```

### Example 4: Multiple Conditions

Combine multiple checks for complex logic:

```yaml
{{- $crdExists := crdExists "gpus.nvidia.com/v1" }}
{{- $annotationSet := hasAnnotation .HCO.Object "platform.kubevirt.io/enable-gpu" "true" }}
{{- if and $crdExists $annotationSet }}
apiVersion: nvidia.com/v1
kind: ClusterPolicy
metadata:
  name: gpu-operator-config
spec:
  # GPU configuration
{{- end }}
```

## Soft Dependencies

Handle missing CRDs gracefully to avoid failures:

### Check CRD Existence

```yaml
{{- if crdExists "kubedeschedulers.operator.openshift.io" }}
apiVersion: operator.openshift.io/v1
kind: KubeDescheduler
# ... resource spec
{{- end }}
```

If the CRD is missing:
- Asset is skipped during rendering
- No error is raised
- Reconciliation continues with other assets
- Asset is automatically applied when CRD becomes available

### Check Object Existence

```yaml
{{- if objectExists "PrometheusRule" "openshift-monitoring" "my-rules" }}
# Configuration that depends on the PrometheusRule
{{- end }}
```

### Check Enum Values (API Version Compatibility)

```yaml
{{- if crdHasEnum "kubedeschedulers.operator.openshift.io" "spec.profiles" "KubeVirtRelieveAndMigrate" }}
  profiles:
    - KubeVirtRelieveAndMigrate
{{- else if crdHasEnum "kubedeschedulers.operator.openshift.io" "spec.profiles" "LongLifecycle" }}
  profiles:
    - LongLifecycle
{{- else }}
  # Fallback for older API versions
{{- end }}
```

## Metadata Catalog Reference

The `assets/active/metadata.yaml` catalog defines all managed assets.

### Metadata Fields

```yaml
- name: my-asset                           # Unique identifier
  path: active/category/my-asset.yaml      # Template file path
  phase: 1                                 # Rollout phase (1=GA, 2=TP, 3=Experimental)
  install: always                          # always | opt-in
  component: MachineConfig                 # Logical grouping
  reconcile_order: 10                      # Processing order (lower = earlier)
  conditions: []                           # Activation conditions (optional)
```

### Field Descriptions

**name**: Unique identifier for the asset. Used in logs, metrics, debug endpoints.

**path**: Relative path from `assets/` directory to template file.
- Use `.yaml` for static resources
- Use `.yaml.tpl` for Go templates

**phase**: Rollout phase indicating maturity:
- `0`: Critical foundation (HCO only)
- `1`: Generally Available (production-ready)
- `2`: Tech Preview (experimental, supported)
- `3`: Experimental (unsupported, for testing)

**install**: When this asset should be applied:
- `always`: Applied to all clusters automatically
- `opt-in`: Requires conditions to be met (annotation, hardware, feature gate)

**component**: Logical grouping for organization. Examples:
- `HyperConverged`
- `MachineConfig`
- `KubeletConfig`
- `NodeHealthCheck`
- `KubeDescheduler`
- `ForkliftController`
- `MetalLB`

**reconcile_order**: Processing order (lower numbers first).
- `0`: HCO only (must be first - serves as RenderContext source)
- `1-9`: Critical baseline (MachineConfig, Kubelet, NodeHealthCheck)
- `10-19`: Scheduling and placement (Descheduler)
- `20+`: Optional operators and advanced features

**conditions**: Array of conditions that must ALL be true for asset to be applied.

### Condition Types

#### Annotation Condition

Asset is applied only if HCO has specific annotation:

```yaml
conditions:
  - type: annotation
    key: platform.kubevirt.io/enable-my-feature
    value: "true"
```

Users enable with:
```bash
kubectl annotate -n openshift-cnv hyperconverged kubevirt-hyperconverged \
  platform.kubevirt.io/enable-my-feature=true
```

#### Hardware Detection Condition

Asset is applied if hardware is detected:

```yaml
conditions:
  - type: hardware-detection
    detector: pciDevicesPresent  # or numaNodesPresent, gpuPresent, etc.
```

Available detectors:
- `pciDevicesPresent`: PCI passthrough-capable devices detected
- `numaNodesPresent`: Multi-NUMA topology detected
- `gpuPresent`: GPU devices detected
- `sriovCapable`: SR-IOV network interfaces detected

#### Feature Gate Condition

Asset is applied if feature gate is enabled:

```yaml
conditions:
  - type: feature-gate
    value: CPUManager
```

Feature gates are typically set in HCO spec or platform configuration.

#### Multiple Conditions (AND Logic)

All conditions must be true:

```yaml
conditions:
  - type: annotation
    key: platform.kubevirt.io/openshift
    value: "true"
  - type: hardware-detection
    detector: gpuPresent
```

This asset is applied only on OpenShift clusters with GPUs.

## Testing Your Asset

### 1. Offline Rendering

Test template syntax and rendering without a cluster:

```bash
# Create test HCO YAML
cat > test-hco.yaml <<EOF
apiVersion: hco.kubevirt.io/v1beta1
kind: HyperConverged
metadata:
  name: kubevirt-hyperconverged
  namespace: openshift-cnv
  annotations:
    platform.kubevirt.io/enable-my-feature: "true"
spec:
  liveMigrationConfig:
    parallelMigrationsPerCluster: 10
    parallelOutboundMigrationsPerNode: 2
EOF

# Render all assets
virt-platform-autopilot render --hco-file=test-hco.yaml --output=yaml

# Check for errors
virt-platform-autopilot render --hco-file=test-hco.yaml --output=status
```

### 2. Debug Endpoints

Test rendering with live cluster context:

```bash
# Port-forward to debug endpoint
kubectl port-forward -n openshift-cnv deployment/virt-platform-autopilot 8081:8081

# Render all assets
curl http://localhost:8081/debug/render

# Render specific asset
curl http://localhost:8081/debug/render/my-asset

# Check exclusions (filtered assets)
curl http://localhost:8081/debug/exclusions
```

See [Debug Endpoints Documentation](debug-endpoints.md) for details.

### 3. Integration Tests

Add integration test coverage:

```go
// pkg/controller/controller_test.go
func TestMyAssetRendering(t *testing.T) {
    // Setup test environment
    ctx := context.Background()

    // Create test HCO
    hco := &hcov1beta1.HyperConverged{
        ObjectMeta: metav1.ObjectMeta{
            Name:      "test-hco",
            Namespace: "openshift-cnv",
            Annotations: map[string]string{
                "platform.kubevirt.io/enable-my-feature": "true",
            },
        },
    }

    // Test rendering
    // ... test logic ...
}
```

Run tests:
```bash
make test-integration
```

### 4. Local Deployment Testing

Test with full controller in Kind cluster:

```bash
# Setup local cluster
make kind-setup

# Deploy autopilot
make deploy-local

# Check logs
make logs-local

# Verify asset was applied
kubectl get <resource-type> <resource-name>

# Make changes and redeploy
make redeploy-local
```

## Template Helper Functions

The following helper functions are available in templates:

### Resource Lookups

- `crdExists "apiVersion"` - Check if CRD is installed
- `objectExists "Kind" "Namespace" "Name"` - Check if object exists
- `crdHasEnum "crdName" "fieldPath" "enumValue"` - Check if CRD schema has enum value
- `prometheusRuleHasRecordingRule "namespace" "name" "recordName"` - Check PrometheusRule

### Data Access

- `dig "key1" "key2" ... default object` - Safely access nested fields with default
- `.HCO.Object` - HyperConverged resource
- `.HCO.Namespace` - HCO namespace
- `.HCO.Name` - HCO name
- `.ClusterCapabilities` - Cluster capabilities and version info

### Annotations

- `hasAnnotation object "key" "value"` - Check if annotation exists with value

### Standard Go Template Functions

All standard Go template functions are available:
- `if`, `else`, `end`
- `range`
- `with`
- `and`, `or`, `not`
- String functions: `trim`, `trimPrefix`, `trimSuffix`, `lower`, `upper`

## RBAC Generation

The autopilot needs RBAC permissions for all resource types it manages.

### Automatic Generation

After adding new resource types, regenerate RBAC:

```bash
make generate-rbac
```

This tool:
1. Scans all templates in `assets/active/`
2. Extracts unique `apiVersion` and `kind` combinations
3. Generates ClusterRole with required permissions
4. Updates `config/rbac/role.yaml`

### Manual RBAC (if needed)

If automatic generation doesn't cover your use case, manually edit:

**File:** `config/rbac/role.yaml`

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: virt-platform-autopilot-role
rules:
  - apiGroups: ["my-api-group.io"]
    resources: ["myresources"]
    verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
```

## Best Practices

### 1. Use Meaningful Names

```yaml
# Good
- name: kubevirt-swap-optimization

# Bad
- name: asset-1
```

### 2. Set Appropriate Reconcile Order

- `0`: HCO only
- `1-9`: Infrastructure (MachineConfig, Kubelet, NodeHealthCheck)
- `10-19`: Scheduling and placement
- `20+`: Optional features

### 3. Provide Sensible Defaults

```yaml
{{- $timeout := dig "spec" "workloadUpdateStrategy" "timeout" "5m" .HCO.Object }}
timeout: {{ $timeout }}
```

### 4. Handle Missing CRDs Gracefully

```yaml
{{- if crdExists "my-crd.example.com" }}
# Only render if CRD exists
{{- end }}
```

### 5. Use Conditions for Opt-In Features

Features that aren't universally applicable should be `install: opt-in`:

```yaml
- name: gpu-operator
  install: opt-in
  conditions:
    - type: annotation
      key: platform.kubevirt.io/enable-gpu
      value: "true"
    - type: hardware-detection
      detector: gpuPresent
```

### 6. Test Offline First

Always test with the render command before deploying:

```bash
virt-platform-autopilot render --hco-file=test-hco.yaml --output=status
```

### 7. Document Complex Templates

Add comments for complex template logic:

```yaml
{{- /* Select descheduler profile based on CRD version */ -}}
{{- if crdHasEnum "kubedeschedulers.operator.openshift.io" "spec.profiles" "KubeVirtRelieveAndMigrate" }}
  # Preferred profile for KubeVirt workloads
  profiles:
    - KubeVirtRelieveAndMigrate
{{- else }}
  # Fallback for older API versions
  profiles:
    - LongLifecycle
{{- end }}
```

## Common Patterns

### Pattern 1: Version-Aware Configuration

Adapt to different API versions:

```yaml
{{- $apiVersion := "v1" }}
{{- if crdHasEnum "myresource.example.com" "spec.mode" "advanced" }}
  {{- $apiVersion = "v2" }}
{{- end }}
apiVersion: myresource.example.com/{{ $apiVersion }}
kind: MyResource
```

### Pattern 2: Environment-Specific Settings

Different settings for different environments:

```yaml
{{- $replicas := 1 }}
{{- if hasAnnotation .HCO.Object "platform.kubevirt.io/environment" "production" }}
  {{- $replicas = 3 }}
{{- end }}
spec:
  replicas: {{ $replicas }}
```

### Pattern 3: Conditional Subsections

Include entire sections conditionally:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: my-config
data:
  {{- if hasAnnotation .HCO.Object "platform.kubevirt.io/enable-debug" "true" }}
  debug: "true"
  log-level: "debug"
  {{- end }}
  required-setting: "value"
```

## Troubleshooting

### Template Syntax Error

**Error:** `template: asset:5: unexpected "}"...`

**Fix:** Check Go template syntax. Common issues:
- Missing `{{-` or `}}`
- Unmatched `if`/`end` blocks
- Invalid function calls

**Debug:**
```bash
virt-platform-autopilot render --hco-file=test-hco.yaml --output=status
```

### Asset Not Applied

**Possible causes:**
1. Condition not met (check annotations, hardware detection)
2. CRD not installed (check `crdExists` guards)
3. Asset filtered by `disabled-resources` annotation

**Debug:**
```bash
# Check exclusions
kubectl port-forward -n openshift-cnv deployment/virt-platform-autopilot 8081:8081
curl http://localhost:8081/debug/exclusions

# Check if asset renders
curl http://localhost:8081/debug/render/my-asset
```

### RBAC Permission Denied

**Error:** `forbidden: User "system:serviceaccount:openshift-cnv:virt-platform-autopilot" cannot create resource...`

**Fix:** Regenerate RBAC:
```bash
make generate-rbac
make deploy
```

### Thrashing (Constant Reconciliation)

**Cause:** Template produces different output on each render (timestamps, random values)

**Fix:** Make templates idempotent - same input should produce same output:

```yaml
# Bad (changes every render)
timestamp: {{ now }}

# Good (stable value)
{{- $timestamp := dig "metadata" "creationTimestamp" "2024-01-01T00:00:00Z" .HCO.Object }}
createdAt: {{ $timestamp }}
```

## Examples from Existing Assets

### HCO Golden Config

**File:** `assets/active/hco/golden-config.yaml.tpl`

Production-ready HCO configuration with opinionated defaults. Must have `reconcile_order: 0`.

### NodeHealthCheck

**File:** `assets/active/node-health/standard-remediation.yaml`

Simple static YAML - no templating needed.

### Descheduler (Conditional)

**File:** `assets/active/descheduler/recommended.yaml.tpl`

Complex conditional rendering based on CRD version, reads HCO for eviction limits.

### PCI Passthrough (Opt-In)

**File:** `assets/active/machine-config/02-pci-passthrough.yaml.tpl`

Requires annotation AND hardware detection to be applied.

## Next Steps

After adding your asset:

1. **Test thoroughly** using render command and debug endpoints
2. **Add integration tests** in `pkg/controller/`
3. **Update documentation** if the feature affects users
4. **Submit PR** with your changes

## Related Documentation

- [ARCHITECTURE.md](ARCHITECTURE.md) - Technical implementation details
- [Debug Endpoints](debug-endpoints.md) - Debugging and inspection tools
- [Lifecycle Management](lifecycle-management.md) - Tombstoning and resource exclusions
- [Local Development](local-development.md) - Setting up dev environment
