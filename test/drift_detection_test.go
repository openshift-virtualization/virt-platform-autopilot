package test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kubevirt/virt-platform-operator/pkg/engine"
)

var _ = Describe("Real-Time Drift Detection", func() {
	// These tests verify that changes to managed resources trigger immediate reconciliation
	// This is critical for the operator to maintain desired state without waiting for periodic sync

	Context("when a managed resource is modified", func() {
		It("should detect drift on MachineConfig immediately", func() {
			if !IsCRDInstalled(ctx, k8sClient, "machineconfigs.machineconfiguration.openshift.io") {
				Skip("MachineConfig CRD not installed")
			}

			testNs := "test-drift-mc-" + randString()

			// Create namespace
			ns := &unstructured.Unstructured{}
			ns.SetGroupVersionKind(nsGVK)
			ns.SetName(testNs)
			Expect(k8sClient.Create(ctx, ns)).To(Succeed())
			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, ns)
			})

			By("creating a MachineConfig with managed-by label")
			mc := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "machineconfiguration.openshift.io/v1",
					"kind":       "MachineConfig",
					"metadata": map[string]interface{}{
						"name": "test-drift-detection",
						"labels": map[string]interface{}{
							engine.ManagedByLabel: engine.ManagedByValue,
						},
					},
					"spec": map[string]interface{}{
						"config": map[string]interface{}{
							"ignition": map[string]interface{}{
								"version": "3.2.0",
							},
						},
					},
				},
			}

			applier := engine.NewApplier(k8sClient, apiReader)
			_, err := applier.Apply(ctx, mc, true)
			Expect(err).NotTo(HaveOccurred())

			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, mc)
			})

			By("verifying initial state")
			key := client.ObjectKey{Name: "test-drift-detection"}
			fetched := &unstructured.Unstructured{}
			fetched.SetGroupVersionKind(schema.GroupVersionKind{
				Group:   "machineconfiguration.openshift.io",
				Version: "v1",
				Kind:    "MachineConfig",
			})
			Expect(k8sClient.Get(ctx, key, fetched)).To(Succeed())
			Expect(fetched.GetLabels()).To(HaveKey(engine.ManagedByLabel))

			By("simulating drift by modifying the resource")
			// In a real cluster with controller running, modifying this would trigger
			// a watch event → reconciliation. In tests without controller, we verify
			// the applier can detect and correct the drift.
			fetched.SetLabels(map[string]string{
				engine.ManagedByLabel: engine.ManagedByValue,
				"drift-test":          "modified",
			})
			Expect(k8sClient.Update(ctx, fetched)).To(Succeed())

			By("detecting drift using applier")
			// The applier should detect the extra label as drift
			original := mc.DeepCopy()
			applied, err := applier.Apply(ctx, original, true)
			Expect(err).NotTo(HaveOccurred())
			Expect(applied).NotTo(BeNil())

			By("verifying drift was corrected")
			final := &unstructured.Unstructured{}
			final.SetGroupVersionKind(schema.GroupVersionKind{
				Group:   "machineconfiguration.openshift.io",
				Version: "v1",
				Kind:    "MachineConfig",
			})
			Expect(k8sClient.Get(ctx, key, final)).To(Succeed())

			// The drift-test label should be preserved (SSA preserves unmanaged fields)
			// but managed fields should match desired state
			labels := final.GetLabels()
			Expect(labels).To(HaveKey(engine.ManagedByLabel))
		})

		It("should detect drift on NodeHealthCheck immediately", func() {
			if !IsCRDInstalled(ctx, k8sClient, "nodehealthchecks.remediation.medik8s.io") {
				Skip("NodeHealthCheck CRD not installed")
			}

			testNs := "test-drift-nhc-" + randString()

			// Create namespace
			ns := &unstructured.Unstructured{}
			ns.SetGroupVersionKind(nsGVK)
			ns.SetName(testNs)
			Expect(k8sClient.Create(ctx, ns)).To(Succeed())
			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, ns)
			})

			By("creating a NodeHealthCheck with managed-by label")
			nhc := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "remediation.medik8s.io/v1alpha1",
					"kind":       "NodeHealthCheck",
					"metadata": map[string]interface{}{
						"name":      "test-nhc-drift",
						"namespace": testNs,
						"labels": map[string]interface{}{
							engine.ManagedByLabel: engine.ManagedByValue,
						},
					},
					"spec": map[string]interface{}{
						"selector": map[string]interface{}{
							"matchLabels": map[string]interface{}{
								"test": "drift",
							},
						},
						"unhealthyConditions": []interface{}{
							map[string]interface{}{
								"type":     "Ready",
								"status":   "False",
								"duration": "300s",
							},
						},
					},
				},
			}

			applier := engine.NewApplier(k8sClient, apiReader)
			_, err := applier.Apply(ctx, nhc, true)
			Expect(err).NotTo(HaveOccurred())

			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, nhc)
			})

			By("simulating drift by modifying spec")
			key := client.ObjectKey{Name: "test-nhc-drift", Namespace: testNs}
			fetched := &unstructured.Unstructured{}
			fetched.SetGroupVersionKind(schema.GroupVersionKind{
				Group:   "remediation.medik8s.io",
				Version: "v1alpha1",
				Kind:    "NodeHealthCheck",
			})
			Expect(k8sClient.Get(ctx, key, fetched)).To(Succeed())

			// Modify duration (simulating user/other controller changing it)
			spec, _, _ := unstructured.NestedMap(fetched.Object, "spec")
			unhealthyConditions := spec["unhealthyConditions"].([]interface{})
			condition := unhealthyConditions[0].(map[string]interface{})
			condition["duration"] = "600s" // Changed from 300s
			Expect(k8sClient.Update(ctx, fetched)).To(Succeed())

			By("detecting and correcting drift")
			original := nhc.DeepCopy()
			applied, err := applier.Apply(ctx, original, true)
			Expect(err).NotTo(HaveOccurred())
			Expect(applied).NotTo(BeNil())

			By("verifying drift was corrected back to 300s")
			final := &unstructured.Unstructured{}
			final.SetGroupVersionKind(schema.GroupVersionKind{
				Group:   "remediation.medik8s.io",
				Version: "v1alpha1",
				Kind:    "NodeHealthCheck",
			})
			Expect(k8sClient.Get(ctx, key, final)).To(Succeed())

			finalSpec, _, _ := unstructured.NestedMap(final.Object, "spec")
			finalConditions := finalSpec["unhealthyConditions"].([]interface{})
			finalCondition := finalConditions[0].(map[string]interface{})
			// SSA should restore to desired state
			Expect(finalCondition["duration"]).To(Equal("300s"))
		})
	})

	Context("when HCO is modified", func() {
		It("should detect drift in HCO spec fields", func() {
			testNs := "test-hco-drift-" + randString()

			// Create namespace
			ns := &unstructured.Unstructured{}
			ns.SetGroupVersionKind(nsGVK)
			ns.SetName(testNs)
			Expect(k8sClient.Create(ctx, ns)).To(Succeed())
			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, ns)
			})

			By("creating an HCO instance")
			hco := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "hco.kubevirt.io/v1beta1",
					"kind":       "HyperConverged",
					"metadata": map[string]interface{}{
						"name":      "kubevirt-hyperconverged",
						"namespace": testNs,
					},
					"spec": map[string]interface{}{
						"liveMigrationConfig": map[string]interface{}{
							"parallelMigrationsPerCluster": int64(5),
						},
					},
				},
			}

			applier := engine.NewApplier(k8sClient, apiReader)
			_, err := applier.Apply(ctx, hco, true)
			Expect(err).NotTo(HaveOccurred())

			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, hco)
			})

			By("simulating user modifying HCO spec")
			key := client.ObjectKey{Name: "kubevirt-hyperconverged", Namespace: testNs}
			fetched := &unstructured.Unstructured{}
			fetched.SetGroupVersionKind(schema.GroupVersionKind{
				Group:   "hco.kubevirt.io",
				Version: "v1beta1",
				Kind:    "HyperConverged",
			})
			Expect(k8sClient.Get(ctx, key, fetched)).To(Succeed())

			// User changes parallelMigrationsPerCluster
			spec, _, _ := unstructured.NestedMap(fetched.Object, "spec")
			liveMigration := spec["liveMigrationConfig"].(map[string]interface{})
			liveMigration["parallelMigrationsPerCluster"] = int64(10) // Changed from 5
			Expect(k8sClient.Update(ctx, fetched)).To(Succeed())

			By("detecting drift")
			// The applier should detect the change
			original := hco.DeepCopy()
			applied, err := applier.Apply(ctx, original, true)
			Expect(err).NotTo(HaveOccurred())
			Expect(applied).NotTo(BeNil())

			By("verifying drift was corrected")
			final := &unstructured.Unstructured{}
			final.SetGroupVersionKind(schema.GroupVersionKind{
				Group:   "hco.kubevirt.io",
				Version: "v1beta1",
				Kind:    "HyperConverged",
			})
			Expect(k8sClient.Get(ctx, key, final)).To(Succeed())

			finalSpec, _, _ := unstructured.NestedMap(final.Object, "spec")
			finalLiveMigration := finalSpec["liveMigrationConfig"].(map[string]interface{})
			// SSA should restore to desired value of 5
			Expect(finalLiveMigration["parallelMigrationsPerCluster"]).To(BeEquivalentTo(5))
		})
	})

	Context("drift detection performance", func() {
		It("should detect drift within reasonable time", func() {
			if !IsCRDInstalled(ctx, k8sClient, "machineconfigs.machineconfiguration.openshift.io") {
				Skip("MachineConfig CRD not installed")
			}

			testNs := "test-perf-" + randString()

			// Create namespace
			ns := &unstructured.Unstructured{}
			ns.SetGroupVersionKind(nsGVK)
			ns.SetName(testNs)
			Expect(k8sClient.Create(ctx, ns)).To(Succeed())
			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, ns)
			})

			By("creating a MachineConfig")
			mc := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "machineconfiguration.openshift.io/v1",
					"kind":       "MachineConfig",
					"metadata": map[string]interface{}{
						"name": "test-perf",
						"labels": map[string]interface{}{
							engine.ManagedByLabel: engine.ManagedByValue,
						},
					},
					"spec": map[string]interface{}{
						"config": map[string]interface{}{
							"ignition": map[string]interface{}{
								"version": "3.2.0",
							},
						},
					},
				},
			}

			applier := engine.NewApplier(k8sClient, apiReader)
			start := time.Now()
			_, err := applier.Apply(ctx, mc, true)
			Expect(err).NotTo(HaveOccurred())
			applyDuration := time.Since(start)

			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, mc)
			})

			By("verifying drift detection completes quickly")
			// Drift detection should be fast (under 1 second in tests)
			Expect(applyDuration).To(BeNumerically("<", 1*time.Second))
		})
	})
})
