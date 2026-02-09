# Integration Tests with envtest

This directory contains integration tests for virt-platform-operator using [envtest](https://book.kubebuilder.io/reference/envtest.html), which provides a real Kubernetes API server for testing.

## Why envtest instead of fake client?

The **Patched Baseline Algorithm** relies heavily on **Server-Side Apply (SSA)** for field ownership tracking and drift detection. The fake client doesn't properly implement SSA semantics, particularly:

- Field ownership tracking via `managedFields`
- Conflict resolution between field managers
- SSA dry-run for drift detection

Therefore, we use envtest which starts a real API server (etcd + kube-apiserver) to properly test SSA behavior.

## Test Structure

### Test Files

- **`integration_suite_test.go`** - Main test suite setup, starts/stops envtest
- **`crd_helpers.go`** - Utilities for dynamic CRD management during tests
- **`crd_scenarios_test.go`** - Tests for CRD lifecycle scenarios (missing, dynamic, removal)
- **`controller_integration_test.go`** - Controller reconciliation tests with SSA verification

### CRD Management

The test suite starts with **minimal CRDs** (only HCO) to test soft dependency handling:

```go
// BeforeSuite - Only loads essential CRDs
testEnv = &envtest.Environment{
    CRDDirectoryPaths: []string{
        filepath.Join("..", "assets", "crds", "kubevirt"),  // HCO only
    },
}
```

Tests can dynamically install additional CRDs using helper functions:

```go
// Install OpenShift CRDs (MachineConfig, KubeletConfig)
err := InstallCRDs(ctx, k8sClient, CRDSetOpenShift)

// Install remediation CRDs (NodeHealthCheck, SNR, FAR)
err := InstallCRDs(ctx, k8sClient, CRDSetRemediation)

// Install operator CRDs (MTV, MetalLB)
err := InstallCRDs(ctx, k8sClient, CRDSetOperators)
```

## Available CRD Sets

| CRD Set | Path | CRDs Included |
|---------|------|---------------|
| `CRDSetCore` | `kubevirt/` | HyperConverged (always loaded) |
| `CRDSetOpenShift` | `openshift/` | MachineConfig, KubeletConfig |
| `CRDSetRemediation` | `remediation/` | NodeHealthCheck, SelfNodeRemediation, FenceAgentsRemediation |
| `CRDSetOperators` | `operators/` | ForkliftController, MetalLB |

## Test Scenarios

### 1. Missing CRD Scenarios

Tests verify the operator handles missing optional CRDs gracefully:

```go
It("should handle missing CRDs gracefully", func() {
    // Verify CRD is not installed
    ExpectCRDNotInstalled(ctx, k8sClient, "machineconfigs.machineconfiguration.openshift.io")

    // Controller should:
    // - Log warning about missing CRD
    // - Skip assets requiring this CRD
    // - Continue managing other assets
})
```

### 2. Dynamic CRD Installation

Tests verify the operator detects and uses CRDs installed after startup:

```go
It("should detect newly installed CRDs", func() {
    // Start without CRD
    ExpectCRDNotInstalled(ctx, k8sClient, "nodehealthchecks.remediation.medik8s.io")

    // Dynamically install CRD
    err := InstallCRDs(ctx, k8sClient, CRDSetRemediation)
    Expect(err).NotTo(HaveOccurred())

    // Controller should automatically start managing resources
})
```

### 3. SSA Field Ownership

Tests verify Server-Side Apply tracks field ownership correctly:

```go
It("should track field ownership via managedFields", func() {
    // Apply resource with SSA
    err := k8sClient.Patch(ctx, obj, client.Apply,
        client.FieldOwner("virt-platform-operator"),
        client.ForceOwnership)

    // Verify managedFields are set
    managedFields := obj.GetManagedFields()
    // Find our field manager in managedFields
})
```

### 4. Drift Detection

Tests verify drift detection using SSA dry-run:

```go
It("should detect drift via SSA dry-run", func() {
    // Apply original state with SSA
    k8sClient.Patch(ctx, original, client.Apply, ...)

    // User modifies resource (creates drift)
    k8sClient.Update(ctx, modified)

    // Dry-run re-apply to detect drift
    k8sClient.Patch(ctx, desired, client.Apply, client.DryRunAll)

    // Compare desired vs actual to detect drift
})
```

## Running Tests

### Run all integration tests
```bash
make test-integration
```

### Run specific test file
```bash
KUBEBUILDER_ASSETS="$(bin/setup-envtest use 1.35.0 --bin-dir ./bin -p path)" \
    bin/ginkgo -v test/crd_scenarios_test.go
```

### Run with verbose output
```bash
KUBEBUILDER_ASSETS="$(bin/setup-envtest use 1.35.0 --bin-dir ./bin -p path)" \
    bin/ginkgo -v --trace ./test/...
```

### Focus on specific test
```bash
# Using Ginkgo's focus feature
KUBEBUILDER_ASSETS="$(bin/setup-envtest use 1.35.0 --bin-dir ./bin -p path)" \
    bin/ginkgo -v --focus="should detect drift" ./test/...
```

## Helper Functions

### CRD Installation

```go
// Install a CRD set
InstallCRDs(ctx, k8sClient, CRDSetOpenShift)

// Check if CRD is installed
installed := IsCRDInstalled(ctx, k8sClient, "machineconfigs.machineconfiguration.openshift.io")

// Wait for CRD to become available
err := WaitForCRD(ctx, k8sClient, "nodehealthchecks.remediation.medik8s.io", 10*time.Second)

// Gomega matchers
ExpectCRDInstalled(ctx, k8sClient, "metallbs.metallb.io")
ExpectCRDNotInstalled(ctx, k8sClient, "somecrd.example.com")
```

### CRD Removal

```go
// Uninstall a CRD set (for testing removal scenarios)
UninstallCRDs(ctx, k8sClient, CRDSetOperators)
```

## Writing New Tests

### Template for Controller Tests

```go
var _ = Describe("My Controller Feature", func() {
    Context("when testing scenario", func() {
        BeforeEach(func() {
            // Install required CRDs if not loaded by default
            err := InstallCRDs(ctx, k8sClient, CRDSetOpenShift)
            Expect(err).NotTo(HaveOccurred())
        })

        It("should do something", func() {
            // Test implementation
        })
    })
})
```

### Testing SSA Behavior

```go
It("should verify SSA field ownership", func() {
    // Create object
    obj := &unstructured.Unstructured{}
    obj.SetAPIVersion("v1")
    obj.SetKind("ConfigMap")
    // ... set fields

    // Apply with SSA
    err := k8sClient.Patch(ctx, obj, client.Apply,
        client.FieldOwner("my-manager"),
        client.ForceOwnership)
    Expect(err).NotTo(HaveOccurred())

    // Fetch and verify managedFields
    fetched := &unstructured.Unstructured{}
    // ... set GVK
    err = k8sClient.Get(ctx, key, fetched)
    Expect(err).NotTo(HaveOccurred())

    managedFields := fetched.GetManagedFields()
    // Verify our manager is present
})
```

## Debugging Tests

### Enable API server logs

Uncomment in `integration_suite_test.go`:

```go
testEnv = &envtest.Environment{
    // ...
    AttachControlPlaneOutput: true,  // Enables API server logs
}
```

### Check envtest setup

```bash
# Verify envtest binaries are downloaded
bin/setup-envtest list

# Use specific Kubernetes version
bin/setup-envtest use 1.35.0
```

### View test output with trace

```bash
bin/ginkgo -v --trace ./test/...
```
