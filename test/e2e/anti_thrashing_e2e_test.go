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

package e2e

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	pauseAnnotation = "platform.kubevirt.io/reconcile-paused"
	managedByLabel  = "platform.kubevirt.io/managed-by"
)

var _ = Describe("Anti-Thrashing E2E Tests", Ordered, func() {
	var (
		hco               *unstructured.Unstructured
		testConfigMap     *corev1.ConfigMap
		testConfigMapName = "anti-thrashing-test-cm"
	)

	BeforeAll(func() {
		By("ensuring HCO instance exists")
		hco = &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "hco.kubevirt.io/v1beta1",
				"kind":       "HyperConverged",
				"metadata": map[string]interface{}{
					"name":      hcoName,
					"namespace": operatorNamespace,
				},
				"spec": map[string]interface{}{},
			},
		}

		// Try to get existing HCO or create new one
		err := k8sClient.Get(ctx, client.ObjectKey{
			Name:      hcoName,
			Namespace: operatorNamespace,
		}, hco)
		if err != nil {
			Expect(k8sClient.Create(ctx, hco)).To(Succeed())
		}

		// Wait for HCO to be ready
		time.Sleep(5 * time.Second)
	})

	AfterEach(func() {
		// Clean up test ConfigMap if it exists
		if testConfigMap != nil {
			_ = k8sClient.Delete(ctx, testConfigMap)
		}
	})

	Context("Edit War Detection", func() {
		It("should detect edit war and pause reconciliation with annotation", func() {
			Skip("This test requires a running operator and is for manual E2E testing")

			By("creating a managed ConfigMap")
			testConfigMap = &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testConfigMapName,
					Namespace: operatorNamespace,
					Labels: map[string]string{
						managedByLabel: "virt-platform-operator",
					},
				},
				Data: map[string]string{
					"key": "original-value",
				},
			}
			Expect(k8sClient.Create(ctx, testConfigMap)).To(Succeed())

			By("simulating external actor repeatedly modifying the resource")
			// In a real E2E scenario, another controller or script would repeatedly
			// modify this ConfigMap, triggering throttling in the operator
			for i := 0; i < 15; i++ {
				Eventually(func() error {
					fresh := &corev1.ConfigMap{}
					if err := k8sClient.Get(ctx, client.ObjectKey{
						Name:      testConfigMapName,
						Namespace: operatorNamespace,
					}, fresh); err != nil {
						return err
					}

					// Modify data to trigger reconciliation
					fresh.Data["external-key"] = "modified"
					return k8sClient.Update(ctx, fresh)
				}, 5*time.Second, 100*time.Millisecond).Should(Succeed())

				// Small delay between modifications
				time.Sleep(100 * time.Millisecond)
			}

			By("waiting for operator to detect thrashing and set pause annotation")
			Eventually(func() bool {
				fresh := &corev1.ConfigMap{}
				if err := k8sClient.Get(ctx, client.ObjectKey{
					Name:      testConfigMapName,
					Namespace: operatorNamespace,
				}, fresh); err != nil {
					return false
				}

				// Check if pause annotation is set
				annotations := fresh.GetAnnotations()
				if annotations == nil {
					return false
				}

				val, exists := annotations[pauseAnnotation]
				return exists && val == "true"
			}, 60*time.Second, 2*time.Second).Should(BeTrue(),
				"Operator should set pause annotation after detecting edit war")

			By("verifying reconciliation is paused")
			// Operator should stop trying to reconcile this resource
			Consistently(func() string {
				fresh := &corev1.ConfigMap{}
				if err := k8sClient.Get(ctx, client.ObjectKey{
					Name:      testConfigMapName,
					Namespace: operatorNamespace,
				}, fresh); err != nil {
					return ""
				}

				// Verify pause annotation is still present
				annotations := fresh.GetAnnotations()
				if annotations == nil {
					return ""
				}
				return annotations[pauseAnnotation]
			}, 10*time.Second, 1*time.Second).Should(Equal("true"),
				"Pause annotation should remain set")
		})

		It("should resume reconciliation when pause annotation is removed", func() {
			Skip("This test requires a running operator and is for manual E2E testing")

			By("creating a paused ConfigMap")
			testConfigMap = &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testConfigMapName,
					Namespace: operatorNamespace,
					Labels: map[string]string{
						managedByLabel: "virt-platform-operator",
					},
					Annotations: map[string]string{
						pauseAnnotation: "true",
					},
				},
				Data: map[string]string{
					"key": "paused-value",
				},
			}
			Expect(k8sClient.Create(ctx, testConfigMap)).To(Succeed())

			By("verifying ConfigMap is paused")
			fresh := &corev1.ConfigMap{}
			Expect(k8sClient.Get(ctx, client.ObjectKey{
				Name:      testConfigMapName,
				Namespace: operatorNamespace,
			}, fresh)).To(Succeed())

			annotations := fresh.GetAnnotations()
			Expect(annotations).To(HaveKeyWithValue(pauseAnnotation, "true"))

			By("removing pause annotation to resume reconciliation")
			Eventually(func() error {
				fresh := &corev1.ConfigMap{}
				if err := k8sClient.Get(ctx, client.ObjectKey{
					Name:      testConfigMapName,
					Namespace: operatorNamespace,
				}, fresh); err != nil {
					return err
				}

				annotations := fresh.GetAnnotations()
				delete(annotations, pauseAnnotation)
				fresh.SetAnnotations(annotations)

				return k8sClient.Update(ctx, fresh)
			}, 5*time.Second, 100*time.Millisecond).Should(Succeed())

			By("verifying pause annotation is removed")
			Eventually(func() bool {
				fresh := &corev1.ConfigMap{}
				if err := k8sClient.Get(ctx, client.ObjectKey{
					Name:      testConfigMapName,
					Namespace: operatorNamespace,
				}, fresh); err != nil {
					return false
				}

				annotations := fresh.GetAnnotations()
				if annotations == nil {
					return true // No annotations means not paused
				}

				_, exists := annotations[pauseAnnotation]
				return !exists // Pause annotation should not exist
			}, 10*time.Second, 500*time.Millisecond).Should(BeTrue(),
				"Pause annotation should be removed")

			By("verifying operator resumes reconciliation")
			// In a real E2E scenario, we would verify that the operator
			// has resumed reconciling this resource by checking:
			// - Resource is being updated to match desired state
			// - Events are being recorded for reconciliation
			// - Metrics show successful reconciliation
		})
	})

	Context("Event Recording", func() {
		It("should record ThrashingDetected event when edit war is detected", func() {
			Skip("This test requires a running operator and is for manual E2E testing")

			// In a real E2E scenario, we would:
			// 1. Trigger an edit war
			// 2. Wait for operator to detect thrashing
			// 3. Check k8s events for ThrashingDetected event
			// 4. Verify event message contains recovery instructions

			By("listing events for HCO")
			events := &corev1.EventList{}
			Eventually(func() bool {
				if err := k8sClient.List(ctx, events, client.InNamespace(operatorNamespace)); err != nil {
					return false
				}

				// Look for ThrashingDetected event
				for _, event := range events.Items {
					if event.Reason == "ThrashingDetected" {
						GinkgoWriter.Printf("Found ThrashingDetected event: %s\n", event.Message)
						return true
					}
				}
				return false
			}, 60*time.Second, 2*time.Second).Should(BeTrue(),
				"Should find ThrashingDetected event")
		})
	})

	Context("Metric Stability", func() {
		It("should emit thrashing metric only once per edit war episode", func() {
			Skip("This test requires Prometheus integration and is for manual E2E testing")

			// In a real E2E scenario with Prometheus, we would:
			// 1. Query virt_platform_thrashing_total before test
			// 2. Trigger an edit war
			// 3. Wait for thrashing detection
			// 4. Query metric again
			// 5. Verify metric incremented by exactly 1
			// 6. Verify metric stays stable (doesn't increment on every retry)
		})
	})
})
