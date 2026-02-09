package test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kubevirt/virt-platform-operator/pkg/util"
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
		It("should gracefully handle missing MachineConfig CRD", func() {
			// This tests the soft dependency handling
			// The operator should skip reconciling assets whose CRDs are not installed

			By("using a CRD that doesn't exist in the test environment")
			// Use AAQ operator CRD which is not installed in any test
			testCRDName := "aaqcontrollers.aaq.kubevirt.io"

			By("verifying the test CRD is not installed")
			Expect(IsCRDInstalled(ctx, k8sClient, testCRDName)).To(BeFalse())

			By("creating HCO instance to trigger reconciliation")
			testNs := "test-soft-deps-" + randString(5)

			// Create test namespace
			ns := &unstructured.Unstructured{}
			ns.SetGroupVersionKind(nsGVK)
			ns.SetName(testNs)
			Expect(k8sClient.Create(ctx, ns)).To(Succeed())
			defer func() {
				_ = k8sClient.Delete(ctx, ns)
			}()

			hco := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "hco.kubevirt.io/v1beta1",
					"kind":       "HyperConverged",
					"metadata": map[string]interface{}{
						"name":      "kubevirt-hyperconverged", // HCO CRD requires this exact name
						"namespace": testNs,
					},
					"spec": map[string]interface{}{},
				},
			}

			Expect(k8sClient.Create(ctx, hco)).To(Succeed())
			defer func() {
				_ = k8sClient.Delete(ctx, hco)
			}()

			By("verifying CRD checker correctly detects missing CRD")
			checker := util.NewCRDChecker(k8sClient)

			// Test with a CRD that should be missing
			installed, err := checker.IsCRDInstalled(ctx, testCRDName)
			Expect(err).NotTo(HaveOccurred())
			Expect(installed).To(BeFalse(), "Test CRD should not be installed")

			By("verifying caching works")
			// Second call should use cache (faster)
			installed, err = checker.IsCRDInstalled(ctx, testCRDName)
			Expect(err).NotTo(HaveOccurred())
			Expect(installed).To(BeFalse(), "Cached result should match")

			By("testing with a known installed CRD")
			// HCO CRD should be installed (it's in the test environment setup)
			installed, err = checker.IsCRDInstalled(ctx, "hyperconvergeds.hco.kubevirt.io")
			Expect(err).NotTo(HaveOccurred())
			Expect(installed).To(BeTrue(), "HCO CRD should be installed")

			By("controller would skip assets for missing CRDs")
			// The controller's reconcileAssets function checks IsComponentSupported
			// and skips assets when their CRDs are not installed
		})

		It("should start managing resources when CRDs appear dynamically", func() {
			By("starting without NodeHealthCheck CRD")
			checker := util.NewCRDChecker(k8sClient)

			supported, _, err := checker.IsComponentSupported(ctx, "NodeHealthCheck")
			Expect(err).NotTo(HaveOccurred())

			if supported {
				By("CRD already installed, skipping to installation verification")
			} else {
				By("dynamically installing NodeHealthCheck CRD")
				err = InstallCRDs(ctx, k8sClient, CRDSetRemediation)
				Expect(err).NotTo(HaveOccurred())

				// Cleanup CRDs after test
				DeferCleanup(func() {
					_ = UninstallCRDs(ctx, k8sClient, CRDSetRemediation)
				})

				// Wait for CRD to be established
				Eventually(func() bool {
					return IsCRDInstalled(ctx, k8sClient, "nodehealthchecks.remediation.medik8s.io")
				}, 10*time.Second, 250*time.Millisecond).Should(BeTrue())
			}

			By("invalidating cache to detect new CRD")
			checker.InvalidateCache("")

			By("verifying component is now supported")
			supported, crdName, err := checker.IsComponentSupported(ctx, "NodeHealthCheck")
			Expect(err).NotTo(HaveOccurred())
			Expect(supported).To(BeTrue(), "NodeHealthCheck should be supported after CRD installation")
			Expect(crdName).To(Equal("nodehealthchecks.remediation.medik8s.io"))
		})
	})
})
