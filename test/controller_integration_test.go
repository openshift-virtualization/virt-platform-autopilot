package test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("Platform Controller Integration", func() {
	Context("SSA fundamentals (without controller)", func() {
		// These tests verify envtest supports SSA correctly
		// They use ConfigMap as a simple resource type
		// TODO: Add actual controller tests once controller is integrated

		It("should demonstrate SSA field ownership tracking", func() {
			// This test demonstrates how SSA tracks field ownership via managedFields
			// This is critical for the Patched Baseline algorithm

			By("creating a resource with SSA")
			obj := &unstructured.Unstructured{}
			obj.SetAPIVersion("v1")
			obj.SetKind("ConfigMap")
			obj.SetName("test-ssa")
			obj.SetNamespace("default")
			obj.Object["data"] = map[string]interface{}{
				"key1": "value1",
			}

			// Use the modern Apply() API for unstructured objects
			// Convert unstructured to ApplyConfiguration
			applyConfig := client.ApplyConfigurationFromUnstructured(obj)
			err := k8sClient.Apply(ctx, applyConfig, client.FieldOwner("test-manager"), client.ForceOwnership)
			Expect(err).NotTo(HaveOccurred())

			By("verifying managedFields are tracked")
			fetched := &unstructured.Unstructured{}
			fetched.SetAPIVersion("v1")
			fetched.SetKind("ConfigMap")
			key := client.ObjectKey{Name: "test-ssa", Namespace: "default"}

			err = k8sClient.Get(ctx, key, fetched)
			Expect(err).NotTo(HaveOccurred())

			managedFields := fetched.GetManagedFields()
			Expect(managedFields).NotTo(BeEmpty())

			// Verify our field manager is present
			found := false
			for _, mf := range managedFields {
				if mf.Manager == "test-manager" {
					found = true
					Expect(mf.Operation).To(Equal(metav1.ManagedFieldsOperationApply))
					break
				}
			}
			Expect(found).To(BeTrue(), "Expected to find field manager 'test-manager' in managedFields")

			// Cleanup
			err = k8sClient.Delete(ctx, fetched)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should detect drift via SSA dry-run", func() {
			// This test demonstrates drift detection using SSA dry-run
			// This is a core component of the Patched Baseline algorithm

			By("creating initial resource with SSA")
			obj := &unstructured.Unstructured{}
			obj.SetAPIVersion("v1")
			obj.SetKind("ConfigMap")
			obj.SetName("test-drift")
			obj.SetNamespace("default")
			obj.Object["data"] = map[string]interface{}{
				"key1": "original-value",
			}

			// Use the modern Apply() API with conversion
			applyConfig := client.ApplyConfigurationFromUnstructured(obj)
			err := k8sClient.Apply(ctx, applyConfig, client.FieldOwner("operator"), client.ForceOwnership)
			Expect(err).NotTo(HaveOccurred())

			By("simulating user modification (creates drift)")
			fetched := &unstructured.Unstructured{}
			fetched.SetAPIVersion("v1")
			fetched.SetKind("ConfigMap")
			key := client.ObjectKey{Name: "test-drift", Namespace: "default"}

			err = k8sClient.Get(ctx, key, fetched)
			Expect(err).NotTo(HaveOccurred())

			// User modifies the value
			fetched.Object["data"] = map[string]interface{}{
				"key1": "user-modified-value",
			}
			err = k8sClient.Update(ctx, fetched)
			Expect(err).NotTo(HaveOccurred())

			By("detecting drift with SSA dry-run")
			// For drift detection, we need to create desired state from scratch
			// (not a deep copy of original, which has stale resourceVersion)
			desired := &unstructured.Unstructured{}
			desired.SetAPIVersion("v1")
			desired.SetKind("ConfigMap")
			desired.SetName("test-drift")
			desired.SetNamespace("default")
			desired.Object["data"] = map[string]interface{}{
				"key1": "original-value",
			}

			// Use Apply() with DryRunAll for drift detection
			desiredConfig := client.ApplyConfigurationFromUnstructured(desired)
			err = k8sClient.Apply(ctx, desiredConfig,
				client.FieldOwner("operator"),
				client.ForceOwnership,
				client.DryRunAll)
			Expect(err).NotTo(HaveOccurred())

			// Fetch actual state
			actual := &unstructured.Unstructured{}
			actual.SetAPIVersion("v1")
			actual.SetKind("ConfigMap")
			err = k8sClient.Get(ctx, key, actual)
			Expect(err).NotTo(HaveOccurred())

			// Compare - drift should be detected
			actualData, _, _ := unstructured.NestedMap(actual.Object, "data")
			desiredData, _, _ := unstructured.NestedMap(desired.Object, "data")
			Expect(actualData).NotTo(Equal(desiredData), "Drift should be detected")

			// Cleanup
			err = k8sClient.Delete(ctx, actual)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should handle field ownership conflicts", func() {
			// This test demonstrates how SSA handles conflicting field managers

			By("creating resource with first manager")
			obj := &unstructured.Unstructured{}
			obj.SetAPIVersion("v1")
			obj.SetKind("ConfigMap")
			obj.SetName("test-conflict")
			obj.SetNamespace("default")
			obj.Object["data"] = map[string]interface{}{
				"key1": "manager1-value",
			}

			// Use Apply() API with conversion
			applyConfig := client.ApplyConfigurationFromUnstructured(obj)
			err := k8sClient.Apply(ctx, applyConfig, client.FieldOwner("manager1"), client.ForceOwnership)
			Expect(err).NotTo(HaveOccurred())

			By("second manager modifying same field with ForceOwnership")
			obj2 := obj.DeepCopy()
			obj2.Object["data"] = map[string]interface{}{
				"key1": "manager2-value",
			}
			// Clear managedFields for SSA (required by API server)
			obj2.SetManagedFields(nil)

			// Use Apply() API with different field owner
			applyConfig2 := client.ApplyConfigurationFromUnstructured(obj2)
			err = k8sClient.Apply(ctx, applyConfig2, client.FieldOwner("manager2"), client.ForceOwnership)
			Expect(err).NotTo(HaveOccurred())

			By("verifying manager2 now owns the field")
			fetched := &unstructured.Unstructured{}
			fetched.SetAPIVersion("v1")
			fetched.SetKind("ConfigMap")
			key := client.ObjectKey{Name: "test-conflict", Namespace: "default"}

			err = k8sClient.Get(ctx, key, fetched)
			Expect(err).NotTo(HaveOccurred())

			// Check managedFields
			managedFields := fetched.GetManagedFields()
			manager2Found := false
			for _, mf := range managedFields {
				if mf.Manager == "manager2" {
					manager2Found = true
					break
				}
			}
			Expect(manager2Found).To(BeTrue())

			// Cleanup
			err = k8sClient.Delete(ctx, fetched)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("when optional CRDs are missing (controller scenarios)", func() {
		PIt("should gracefully handle missing MachineConfig CRD", func() {
			// TODO: Implement once controller is integrated
			// This tests the soft dependency handling
			// The operator should log a warning but continue operating

			// Controller should:
			// 1. Logs a warning about missing CRD
			// 2. Skips reconciling MachineConfig assets
			// 3. Continues reconciling other assets successfully
		})

		PIt("should start managing resources when CRDs appear dynamically", func() {
			// TODO: Implement once controller is integrated

			// Controller should:
			// 1. Detects new CRD installation via watch or periodic check
			// 2. Automatically starts reconciling previously-skipped assets
			// 3. Creates NodeHealthCheck resources as defined in asset templates
		})
	})
})
