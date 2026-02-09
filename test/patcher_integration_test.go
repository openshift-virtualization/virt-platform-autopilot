/*
Copyright 2026 The KubeVirt Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kubevirt/virt-platform-operator/pkg/engine"
	"github.com/kubevirt/virt-platform-operator/pkg/overrides"
	"github.com/kubevirt/virt-platform-operator/pkg/throttling"
)

var _ = Describe("Patched Baseline Algorithm Integration", func() {
	var (
		testNs string
	)

	BeforeEach(func() {
		testNs = "test-patcher-" + randString(5)

		// Create test namespace
		ns := &unstructured.Unstructured{}
		ns.SetGroupVersionKind(nsGVK)
		ns.SetName(testNs)
		Expect(k8sClient.Create(ctx, ns)).To(Succeed())
	})

	AfterEach(func() {
		// Clean up namespace
		ns := &unstructured.Unstructured{}
		ns.SetGroupVersionKind(nsGVK)
		ns.SetName(testNs)
		_ = k8sClient.Delete(ctx, ns)
	})

	Describe("Full Patched Baseline Flow", func() {
		It("should successfully reconcile a static asset", func() {
			// Create a simple ConfigMap asset for testing
			asset := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "v1",
					"kind":       "ConfigMap",
					"metadata": map[string]interface{}{
						"name":      "test-config",
						"namespace": testNs,
					},
					"data": map[string]interface{}{
						"key1": "value1",
					},
				},
			}

			// Apply the asset using Applier directly (simulating what Patcher does)
			applier := engine.NewApplier(k8sClient, apiReader)
			applied, err := applier.Apply(ctx, asset, true)
			Expect(err).NotTo(HaveOccurred())
			Expect(applied).To(BeTrue())

			// Verify object was created
			created := &unstructured.Unstructured{}
			created.SetGroupVersionKind(asset.GroupVersionKind())
			err = k8sClient.Get(ctx, client.ObjectKey{
				Namespace: testNs,
				Name:      "test-config",
			}, created)
			Expect(err).NotTo(HaveOccurred())
			Expect(created.GetName()).To(Equal("test-config"))

			// Verify data
			data, found, err := unstructured.NestedStringMap(created.Object, "data")
			Expect(err).NotTo(HaveOccurred())
			Expect(found).To(BeTrue())
			Expect(data["key1"]).To(Equal("value1"))
		})

		It("should detect and apply drift", func() {
			// Create initial object
			obj := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "v1",
					"kind":       "ConfigMap",
					"metadata": map[string]interface{}{
						"name":      "drift-test",
						"namespace": testNs,
					},
					"data": map[string]interface{}{
						"replicas": "3",
					},
				},
			}

			applier := engine.NewApplier(k8sClient, apiReader)
			_, err := applier.Apply(ctx, obj, true)
			Expect(err).NotTo(HaveOccurred())

			// Manually modify the object to simulate drift
			live := &unstructured.Unstructured{}
			live.SetGroupVersionKind(obj.GroupVersionKind())
			err = k8sClient.Get(ctx, client.ObjectKey{
				Namespace: testNs,
				Name:      "drift-test",
			}, live)
			Expect(err).NotTo(HaveOccurred())

			// Change data
			err = unstructured.SetNestedField(live.Object, "5", "data", "replicas")
			Expect(err).NotTo(HaveOccurred())
			err = k8sClient.Update(ctx, live)
			Expect(err).NotTo(HaveOccurred())

			// Detect drift
			driftDetector := engine.NewDriftDetector(k8sClient)
			hasDrift, err := driftDetector.DetectDrift(ctx, obj, live)
			Expect(err).NotTo(HaveOccurred())
			Expect(hasDrift).To(BeTrue(), "Should detect drift when values differ")

			// Re-apply to fix drift
			_, err = applier.Apply(ctx, obj, true)
			Expect(err).NotTo(HaveOccurred())

			// Verify drift was fixed
			fixed := &unstructured.Unstructured{}
			fixed.SetGroupVersionKind(obj.GroupVersionKind())
			err = k8sClient.Get(ctx, client.ObjectKey{
				Namespace: testNs,
				Name:      "drift-test",
			}, fixed)
			Expect(err).NotTo(HaveOccurred())

			data, _, _ := unstructured.NestedStringMap(fixed.Object, "data")
			Expect(data["replicas"]).To(Equal("3"), "Drift should be corrected")
		})

		It("should apply JSON patch from annotation", func() {
			// Create object with patch annotation
			obj := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "v1",
					"kind":       "ConfigMap",
					"metadata": map[string]interface{}{
						"name":      "patch-test",
						"namespace": testNs,
						"annotations": map[string]interface{}{
							overrides.PatchAnnotation: `[{"op": "add", "path": "/data/newKey", "value": "patched"}]`,
						},
					},
					"data": map[string]interface{}{
						"original": "value",
					},
				},
			}

			// Apply patch
			patched, err := overrides.ApplyJSONPatch(obj)
			Expect(err).NotTo(HaveOccurred())
			Expect(patched).To(BeTrue())

			// Verify patch was applied
			data, found, err := unstructured.NestedStringMap(obj.Object, "data")
			Expect(err).NotTo(HaveOccurred())
			Expect(found).To(BeTrue())
			Expect(data["original"]).To(Equal("value"), "Original data should remain")
			Expect(data["newKey"]).To(Equal("patched"), "Patched data should be added")

			// Apply to cluster
			applier := engine.NewApplier(k8sClient, apiReader)
			_, err = applier.Apply(ctx, obj, true)
			Expect(err).NotTo(HaveOccurred())

			// Verify in cluster
			live := &unstructured.Unstructured{}
			live.SetGroupVersionKind(obj.GroupVersionKind())
			err = k8sClient.Get(ctx, client.ObjectKey{
				Namespace: testNs,
				Name:      "patch-test",
			}, live)
			Expect(err).NotTo(HaveOccurred())

			liveData, _, _ := unstructured.NestedStringMap(live.Object, "data")
			Expect(liveData["newKey"]).To(Equal("patched"))
		})

		It("should mask ignored fields", func() {
			// Create initial object in cluster
			live := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "v1",
					"kind":       "ConfigMap",
					"metadata": map[string]interface{}{
						"name":      "mask-test",
						"namespace": testNs,
						"annotations": map[string]interface{}{
							overrides.AnnotationIgnoreFields: "/data/userManaged",
						},
					},
					"data": map[string]interface{}{
						"userManaged":     "user-value",
						"operatorManaged": "operator-value",
					},
				},
			}

			applier := engine.NewApplier(k8sClient, apiReader)
			_, err := applier.Apply(ctx, live, true)
			Expect(err).NotTo(HaveOccurred())

			// Create desired state (operator wants to change both fields)
			desired := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "v1",
					"kind":       "ConfigMap",
					"metadata": map[string]interface{}{
						"name":      "mask-test",
						"namespace": testNs,
					},
					"data": map[string]interface{}{
						"userManaged":     "operator-new-value",
						"operatorManaged": "operator-new-value",
					},
				},
			}

			// Apply masking
			masked, err := overrides.MaskIgnoredFields(desired, live)
			Expect(err).NotTo(HaveOccurred())

			// Verify userManaged field was masked (kept from live)
			data, _, _ := unstructured.NestedStringMap(masked.Object, "data")
			Expect(data["userManaged"]).To(Equal("user-value"), "Masked field should keep live value")
			Expect(data["operatorManaged"]).To(Equal("operator-new-value"), "Unmasked field should use desired value")
		})

		It("should skip reconciliation for unmanaged resources", func() {
			// Create object with unmanaged annotation
			obj := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "v1",
					"kind":       "ConfigMap",
					"metadata": map[string]interface{}{
						"name":      "unmanaged-test",
						"namespace": testNs,
						"annotations": map[string]interface{}{
							overrides.AnnotationMode: overrides.ModeUnmanaged,
						},
					},
					"data": map[string]interface{}{
						"key": "user-value",
					},
				},
			}

			applier := engine.NewApplier(k8sClient, apiReader)
			_, err := applier.Apply(ctx, obj, true)
			Expect(err).NotTo(HaveOccurred())

			// Verify object is marked as unmanaged
			Expect(overrides.IsUnmanaged(obj)).To(BeTrue())

			// In real Patcher, this would be skipped. Let's verify the check works
			live := &unstructured.Unstructured{}
			live.SetGroupVersionKind(obj.GroupVersionKind())
			err = k8sClient.Get(ctx, client.ObjectKey{
				Namespace: testNs,
				Name:      "unmanaged-test",
			}, live)
			Expect(err).NotTo(HaveOccurred())

			Expect(overrides.IsUnmanaged(live)).To(BeTrue(), "Live object should be unmanaged")

			// Verify data wasn't changed
			data, _, _ := unstructured.NestedStringMap(live.Object, "data")
			Expect(data["key"]).To(Equal("user-value"), "Unmanaged object should not be modified")
		})
	})

	Describe("Throttling Behavior", func() {
		It("should throttle rapid updates", func() {
			// Create a token bucket with very low limits
			tb := throttling.NewTokenBucketWithSettings(2, 1*time.Minute)

			resourceKey := "ConfigMap/test-ns/test-resource"

			// First 2 updates should succeed
			Expect(tb.Record(resourceKey)).To(Succeed())
			Expect(tb.Record(resourceKey)).To(Succeed())

			// Third update should be throttled
			err := tb.Record(resourceKey)
			Expect(err).To(HaveOccurred())
			Expect(throttling.IsThrottled(err)).To(BeTrue())

			// Reset and verify it works again
			tb.Reset(resourceKey)
			Expect(tb.Record(resourceKey)).To(Succeed())
		})

		It("should have independent throttling per resource", func() {
			tb := throttling.NewTokenBucketWithSettings(1, 1*time.Minute)

			key1 := "ConfigMap/ns1/resource1"
			key2 := "ConfigMap/ns2/resource2"

			// Exhaust key1
			Expect(tb.Record(key1)).To(Succeed())
			Expect(throttling.IsThrottled(tb.Record(key1))).To(BeTrue())

			// key2 should still work
			Expect(tb.Record(key2)).To(Succeed())
		})
	})

	Describe("SSA Field Ownership", func() {
		It("should track field ownership with SSA", func() {
			obj := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "v1",
					"kind":       "ConfigMap",
					"metadata": map[string]interface{}{
						"name":      "ssa-test",
						"namespace": testNs,
					},
					"data": map[string]interface{}{
						"field1": "value1",
					},
				},
			}

			applier := engine.NewApplier(k8sClient, apiReader)
			_, err := applier.Apply(ctx, obj, true)
			Expect(err).NotTo(HaveOccurred())

			// Retrieve and check managedFields
			live := &unstructured.Unstructured{}
			live.SetGroupVersionKind(obj.GroupVersionKind())
			err = k8sClient.Get(ctx, client.ObjectKey{
				Namespace: testNs,
				Name:      "ssa-test",
			}, live)
			Expect(err).NotTo(HaveOccurred())

			managedFields := live.GetManagedFields()
			Expect(managedFields).NotTo(BeEmpty(), "SSA should create managedFields")

			// Find our manager
			found := false
			for _, mf := range managedFields {
				if mf.Manager == "virt-platform-operator" {
					found = true
					Expect(mf.Operation).To(Equal(metav1.ManagedFieldsOperationApply))
					break
				}
			}
			Expect(found).To(BeTrue(), "Should find our field manager")
		})

		It("should handle conflicts with different field managers", func() {
			obj := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "v1",
					"kind":       "ConfigMap",
					"metadata": map[string]interface{}{
						"name":      "conflict-test",
						"namespace": testNs,
					},
					"data": map[string]interface{}{
						"shared": "operator-value",
					},
				},
			}

			// Apply as operator
			applier := engine.NewApplier(k8sClient, apiReader)
			_, err := applier.Apply(ctx, obj, true)
			Expect(err).NotTo(HaveOccurred())

			// Simulate another manager (user) updating the same field
			live := &unstructured.Unstructured{}
			live.SetGroupVersionKind(obj.GroupVersionKind())
			err = k8sClient.Get(ctx, client.ObjectKey{
				Namespace: testNs,
				Name:      "conflict-test",
			}, live)
			Expect(err).NotTo(HaveOccurred())

			// Update with different manager
			err = unstructured.SetNestedField(live.Object, "user-value", "data", "shared")
			Expect(err).NotTo(HaveOccurred())

			// Use Update instead of Apply to simulate user change
			err = k8sClient.Update(ctx, live)
			Expect(err).NotTo(HaveOccurred())

			// Re-apply as operator with force ownership
			_, err = applier.Apply(ctx, obj, true)
			Expect(err).NotTo(HaveOccurred())

			// Verify operator won the conflict (force ownership)
			final := &unstructured.Unstructured{}
			final.SetGroupVersionKind(obj.GroupVersionKind())
			err = k8sClient.Get(ctx, client.ObjectKey{
				Namespace: testNs,
				Name:      "conflict-test",
			}, final)
			Expect(err).NotTo(HaveOccurred())

			data, _, _ := unstructured.NestedStringMap(final.Object, "data")
			Expect(data["shared"]).To(Equal("operator-value"), "Operator should win with force ownership")
		})
	})

	Describe("Combined Scenarios", func() {
		It("should handle patch + mask + drift + throttle in one flow", func() {
			// Create initial object in cluster (simulating existing resource)
			live := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "v1",
					"kind":       "ConfigMap",
					"metadata": map[string]interface{}{
						"name":      "combined-test",
						"namespace": testNs,
						"annotations": map[string]interface{}{
							overrides.PatchAnnotation:        `[{"op": "add", "path": "/data/patched", "value": "yes"}]`,
							overrides.AnnotationIgnoreFields: "/data/userField",
						},
					},
					"data": map[string]interface{}{
						"userField":     "user-controlled",
						"operatorField": "old-operator-value",
					},
				},
			}

			applier := engine.NewApplier(k8sClient, apiReader)
			_, err := applier.Apply(ctx, live, true)
			Expect(err).NotTo(HaveOccurred())

			// Reload live object to get full state from cluster
			err = k8sClient.Get(ctx, client.ObjectKey{
				Namespace: testNs,
				Name:      "combined-test",
			}, live)
			Expect(err).NotTo(HaveOccurred())

			// Create desired state (what operator wants)
			desiredState := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "v1",
					"kind":       "ConfigMap",
					"metadata": map[string]interface{}{
						"name":      "combined-test",
						"namespace": testNs,
					},
					"data": map[string]interface{}{
						"userField":     "operator-wants-to-change-this",
						"operatorField": "new-operator-value",
					},
				},
			}

			// Simulate Patcher flow:
			// 1. Copy patch annotation from live to desiredState
			liveAnnotations := live.GetAnnotations()
			desiredAnnotations := desiredState.GetAnnotations()
			if desiredAnnotations == nil {
				desiredAnnotations = make(map[string]string)
			}
			if patchStr, exists := liveAnnotations[overrides.PatchAnnotation]; exists {
				desiredAnnotations[overrides.PatchAnnotation] = patchStr
				desiredState.SetAnnotations(desiredAnnotations)
			}

			// 2. Apply JSON patch
			patched, err := overrides.ApplyJSONPatch(desiredState)
			Expect(err).NotTo(HaveOccurred())
			Expect(patched).To(BeTrue())

			// 3. Mask ignored fields
			masked, err := overrides.MaskIgnoredFields(desiredState, live)
			Expect(err).NotTo(HaveOccurred())

			// Verify combined effects
			data, _, _ := unstructured.NestedStringMap(masked.Object, "data")
			Expect(data["userField"]).To(Equal("user-controlled"), "User field should be masked")
			Expect(data["operatorField"]).To(Equal("new-operator-value"), "Operator field should be updated")
			Expect(data["patched"]).To(Equal("yes"), "Patch should be applied")

			// 4. Detect drift
			driftDetector := engine.NewDriftDetector(k8sClient)
			hasDrift, err := driftDetector.DetectDrift(ctx, masked, live)
			Expect(err).NotTo(HaveOccurred())
			Expect(hasDrift).To(BeTrue(), "Should detect drift due to operatorField change")

			// 5. Check throttling (should pass first time)
			tb := throttling.NewTokenBucket()
			resourceKey := throttling.MakeResourceKey(testNs, "combined-test", "ConfigMap")
			Expect(tb.Record(resourceKey)).To(Succeed())

			// 6. Apply with SSA
			_, err = applier.Apply(ctx, masked, true)
			Expect(err).NotTo(HaveOccurred())

			// Verify final state
			final := &unstructured.Unstructured{}
			final.SetGroupVersionKind(masked.GroupVersionKind())
			err = k8sClient.Get(ctx, client.ObjectKey{
				Namespace: testNs,
				Name:      "combined-test",
			}, final)
			Expect(err).NotTo(HaveOccurred())

			finalData, _, _ := unstructured.NestedStringMap(final.Object, "data")
			Expect(finalData["userField"]).To(Equal("user-controlled"), "User field preserved by masking")
			Expect(finalData["operatorField"]).To(Equal("new-operator-value"), "Operator field updated")
			Expect(finalData["patched"]).To(Equal("yes"), "Patched field added")
		})
	})

	Describe("Object Adoption and Labeling", func() {
		It("should automatically label managed objects", func() {
			// Create an object that will be managed
			obj := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "v1",
					"kind":       "ConfigMap",
					"metadata": map[string]interface{}{
						"name":      "label-test",
						"namespace": testNs,
					},
					"data": map[string]interface{}{
						"key": "value",
					},
				},
			}

			applier := engine.NewApplier(k8sClient, apiReader)
			applied, err := applier.Apply(ctx, obj, true)
			Expect(err).NotTo(HaveOccurred())
			Expect(applied).To(BeTrue())

			// Verify the managed-by label was added
			final := &unstructured.Unstructured{}
			final.SetGroupVersionKind(obj.GroupVersionKind())
			err = k8sClient.Get(ctx, client.ObjectKey{
				Namespace: testNs,
				Name:      "label-test",
			}, final)
			Expect(err).NotTo(HaveOccurred())

			labels := final.GetLabels()
			Expect(labels).NotTo(BeNil())
			Expect(labels[engine.ManagedByLabel]).To(Equal(engine.ManagedByValue),
				"Object should have managed-by label")
		})

		It("should adopt existing unlabeled objects", func() {
			// Create an object without the managed-by label (simulating pre-existing resource)
			unlabeled := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "v1",
					"kind":       "ConfigMap",
					"metadata": map[string]interface{}{
						"name":      "adoption-test",
						"namespace": testNs,
						"labels": map[string]interface{}{
							"existing-label": "keep-me",
						},
					},
					"data": map[string]interface{}{
						"original": "data",
					},
				},
			}

			// Create object directly via k8s client (bypassing applier to avoid auto-labeling)
			err := k8sClient.Create(ctx, unlabeled)
			Expect(err).NotTo(HaveOccurred())

			// Verify it exists but lacks the managed-by label
			current := &unstructured.Unstructured{}
			current.SetGroupVersionKind(unlabeled.GroupVersionKind())
			err = k8sClient.Get(ctx, client.ObjectKey{
				Namespace: testNs,
				Name:      "adoption-test",
			}, current)
			Expect(err).NotTo(HaveOccurred())
			Expect(engine.HasManagedByLabel(current)).To(BeFalse(), "Should not have label initially")

			// Now apply using the applier - it should adopt the object by adding the label
			updated := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "v1",
					"kind":       "ConfigMap",
					"metadata": map[string]interface{}{
						"name":      "adoption-test",
						"namespace": testNs,
					},
					"data": map[string]interface{}{
						"original": "data",
						"added":    "by-operator",
					},
				},
			}

			applier := engine.NewApplier(k8sClient, apiReader)
			_, err = applier.Apply(ctx, updated, true)
			Expect(err).NotTo(HaveOccurred())

			// Verify the object was adopted (labeled) and updated
			final := &unstructured.Unstructured{}
			final.SetGroupVersionKind(updated.GroupVersionKind())
			err = k8sClient.Get(ctx, client.ObjectKey{
				Namespace: testNs,
				Name:      "adoption-test",
			}, final)
			Expect(err).NotTo(HaveOccurred())

			// Check label was added
			Expect(engine.HasManagedByLabel(final)).To(BeTrue(), "Should have managed-by label after adoption")

			// Check existing label was preserved
			labels := final.GetLabels()
			Expect(labels["existing-label"]).To(Equal("keep-me"), "Existing labels should be preserved")

			// Check data was updated
			data, _, _ := unstructured.NestedStringMap(final.Object, "data")
			Expect(data["original"]).To(Equal("data"))
			Expect(data["added"]).To(Equal("by-operator"))
		})

		It("should re-label objects if label is removed", func() {
			// Create a labeled object
			labeled := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "v1",
					"kind":       "ConfigMap",
					"metadata": map[string]interface{}{
						"name":      "relabel-test",
						"namespace": testNs,
					},
					"data": map[string]interface{}{
						"key": "value",
					},
				},
			}

			applier := engine.NewApplier(k8sClient, apiReader)
			_, err := applier.Apply(ctx, labeled, true)
			Expect(err).NotTo(HaveOccurred())

			// Verify label exists
			current := &unstructured.Unstructured{}
			current.SetGroupVersionKind(labeled.GroupVersionKind())
			err = k8sClient.Get(ctx, client.ObjectKey{
				Namespace: testNs,
				Name:      "relabel-test",
			}, current)
			Expect(err).NotTo(HaveOccurred())
			Expect(engine.HasManagedByLabel(current)).To(BeTrue())

			// Simulate user removing the label
			labels := current.GetLabels()
			delete(labels, engine.ManagedByLabel)
			current.SetLabels(labels)
			err = k8sClient.Update(ctx, current)
			Expect(err).NotTo(HaveOccurred())

			// Verify label was removed
			unlabeled := &unstructured.Unstructured{}
			unlabeled.SetGroupVersionKind(current.GroupVersionKind())
			err = k8sClient.Get(ctx, client.ObjectKey{
				Namespace: testNs,
				Name:      "relabel-test",
			}, unlabeled)
			Expect(err).NotTo(HaveOccurred())
			Expect(engine.HasManagedByLabel(unlabeled)).To(BeFalse(), "Label should be removed")

			// Apply again - should re-add the label
			_, err = applier.Apply(ctx, labeled, true)
			Expect(err).NotTo(HaveOccurred())

			// Verify label was restored
			relabeled := &unstructured.Unstructured{}
			relabeled.SetGroupVersionKind(labeled.GroupVersionKind())
			err = k8sClient.Get(ctx, client.ObjectKey{
				Namespace: testNs,
				Name:      "relabel-test",
			}, relabeled)
			Expect(err).NotTo(HaveOccurred())
			Expect(engine.HasManagedByLabel(relabeled)).To(BeTrue(), "Label should be restored")
		})
	})
})
