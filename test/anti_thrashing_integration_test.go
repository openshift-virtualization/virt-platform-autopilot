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
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"

	"github.com/kubevirt/virt-platform-operator/pkg/observability"
	"github.com/kubevirt/virt-platform-operator/pkg/overrides"
	"github.com/kubevirt/virt-platform-operator/pkg/throttling"
)

var _ = Describe("Anti-Thrashing Integration", func() {
	var (
		testNs string
	)

	BeforeEach(func() {
		testNs = "test-thrashing-" + randString()

		// Create test namespace
		ns := &unstructured.Unstructured{}
		ns.SetGroupVersionKind(nsGVK)
		ns.SetName(testNs)
		Expect(k8sClient.Create(ctx, ns)).To(Succeed())

		// Reset all metrics before each test
		observability.ComplianceStatus.Reset()
		observability.ThrashingTotal.Reset()
		observability.CustomizationInfo.Reset()
		observability.MissingDependency.Reset()
		observability.ReconcileDuration.Reset()
	})

	AfterEach(func() {
		// Clean up namespace
		ns := &unstructured.Unstructured{}
		ns.SetGroupVersionKind(nsGVK)
		ns.SetName(testNs)
		_ = k8sClient.Delete(ctx, ns)
	})

	Context("Throttle Threshold Detection", func() {
		It("should not pause on first throttle", func() {
			detector := throttling.NewThrashingDetector()
			key := "test-ns/test-cm/ConfigMap"

			By("recording first throttle")
			shouldPause := detector.RecordThrottle(key)
			Expect(shouldPause).To(BeFalse(), "first throttle should not trigger pause")

			By("verifying attempt count")
			Expect(detector.GetAttempts(key)).To(Equal(1))
		})

		It("should not pause on second throttle", func() {
			detector := throttling.NewThrashingDetector()
			key := "test-ns/test-cm/ConfigMap"

			By("recording two throttles")
			detector.RecordThrottle(key)
			shouldPause := detector.RecordThrottle(key)
			Expect(shouldPause).To(BeFalse(), "second throttle should not trigger pause")

			By("verifying attempt count")
			Expect(detector.GetAttempts(key)).To(Equal(2))
		})

		It("should pause on third throttle (threshold reached)", func() {
			detector := throttling.NewThrashingDetector()
			key := "test-ns/test-cm/ConfigMap"

			By("recording three throttles")
			detector.RecordThrottle(key)
			detector.RecordThrottle(key)
			shouldPause := detector.RecordThrottle(key)
			Expect(shouldPause).To(BeTrue(), "third throttle should trigger pause")

			By("verifying attempt count")
			Expect(detector.GetAttempts(key)).To(Equal(3))
		})

		It("should continue to pause after threshold", func() {
			detector := throttling.NewThrashingDetector()
			key := "test-ns/test-cm/ConfigMap"

			By("reaching threshold")
			for i := 0; i < 3; i++ {
				detector.RecordThrottle(key)
			}

			By("recording additional throttles")
			shouldPause := detector.RecordThrottle(key)
			Expect(shouldPause).To(BeTrue(), "should continue to pause after threshold")

			shouldPause = detector.RecordThrottle(key)
			Expect(shouldPause).To(BeTrue(), "should continue to pause after threshold")
		})
	})

	Context("Metric Emission", func() {
		It("should emit metric only once when threshold is reached", func() {
			detector := throttling.NewThrashingDetector()
			key := "test-ns/test-cm/ConfigMap"

			By("recording throttles below threshold - should not emit")
			detector.RecordThrottle(key)
			Expect(detector.ShouldEmitMetric(key)).To(BeFalse(), "should not emit at attempt 1")

			detector.RecordThrottle(key)
			Expect(detector.ShouldEmitMetric(key)).To(BeFalse(), "should not emit at attempt 2")

			By("reaching threshold on 3rd throttle")
			detector.RecordThrottle(key)

			By("emitting metric for the first time")
			Expect(detector.ShouldEmitMetric(key)).To(BeTrue(), "should emit when threshold reached")

			By("not emitting metric again")
			Expect(detector.ShouldEmitMetric(key)).To(BeFalse(), "should not emit twice")
			Expect(detector.ShouldEmitMetric(key)).To(BeFalse(), "should not emit again")

			By("recording more throttles - should still not emit")
			detector.RecordThrottle(key)
			Expect(detector.ShouldEmitMetric(key)).To(BeFalse(), "should not emit after threshold already reached")
		})

		It("should emit metric again after reset and new threshold", func() {
			detector := throttling.NewThrashingDetector()
			key := "test-ns/test-cm/ConfigMap"

			By("reaching threshold and emitting metric")
			for i := 0; i < 3; i++ {
				detector.RecordThrottle(key)
			}
			Expect(detector.ShouldEmitMetric(key)).To(BeTrue())

			By("resetting state")
			detector.RecordSuccess(key)
			Expect(detector.GetAttempts(key)).To(Equal(0))

			By("reaching threshold again")
			for i := 0; i < 3; i++ {
				detector.RecordThrottle(key)
			}

			By("emitting metric again (new thrashing episode)")
			Expect(detector.ShouldEmitMetric(key)).To(BeTrue(), "should emit metric for new episode")
		})

		It("should track thrashing metric correctly", func() {
			observability.ThrashingTotal.Reset()

			cm := &unstructured.Unstructured{}
			cm.SetKind("ConfigMap")
			cm.SetName("test-cm")
			cm.SetNamespace(testNs)

			By("incrementing thrashing metric once")
			observability.IncThrashing(cm)

			By("verifying metric value")
			expected := `
				# HELP virt_platform_thrashing_total Total number of reconciliation throttling events (anti-thrashing gate hits)
				# TYPE virt_platform_thrashing_total counter
				virt_platform_thrashing_total{kind="ConfigMap",name="test-cm",namespace="` + testNs + `"} 1
			`
			Expect(testutil.CollectAndCompare(observability.ThrashingTotal, strings.NewReader(expected))).To(Succeed())

			By("not incrementing again (simulating stable behavior)")
			// Don't call IncThrashing again - metric should stay at 1

			By("verifying metric is stable")
			Expect(testutil.CollectAndCompare(observability.ThrashingTotal, strings.NewReader(expected))).To(Succeed())
		})
	})

	Context("Pause Annotation", func() {
		It("should skip reconciliation when pause annotation is present", func() {
			cm := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "v1",
					"kind":       "ConfigMap",
					"metadata": map[string]interface{}{
						"name":      "paused-cm",
						"namespace": testNs,
						"labels": map[string]interface{}{
							"platform.kubevirt.io/managed-by": "virt-platform-operator",
						},
						"annotations": map[string]interface{}{
							overrides.AnnotationReconcilePaused: "true",
						},
					},
					"data": map[string]interface{}{
						"key": "value",
					},
				},
			}

			By("creating resource with pause annotation")
			Expect(k8sClient.Create(ctx, cm)).To(Succeed())

			By("verifying pause annotation check")
			Expect(overrides.IsPaused(cm)).To(BeTrue())
		})

		It("should allow reconciliation when pause annotation is removed", func() {
			cm := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "v1",
					"kind":       "ConfigMap",
					"metadata": map[string]interface{}{
						"name":      "recoverable-cm",
						"namespace": testNs,
						"labels": map[string]interface{}{
							"platform.kubevirt.io/managed-by": "virt-platform-operator",
						},
						"annotations": map[string]interface{}{
							overrides.AnnotationReconcilePaused: "true",
						},
					},
					"data": map[string]interface{}{
						"key": "value",
					},
				},
			}

			By("creating resource with pause annotation")
			Expect(k8sClient.Create(ctx, cm)).To(Succeed())
			Expect(overrides.IsPaused(cm)).To(BeTrue())

			By("removing pause annotation")
			// Get fresh copy
			fresh := &unstructured.Unstructured{}
			fresh.SetGroupVersionKind(cm.GroupVersionKind())
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Namespace: cm.GetNamespace(),
				Name:      cm.GetName(),
			}, fresh)).To(Succeed())

			// Remove annotation
			annotations := fresh.GetAnnotations()
			delete(annotations, overrides.AnnotationReconcilePaused)
			fresh.SetAnnotations(annotations)
			Expect(k8sClient.Update(ctx, fresh)).To(Succeed())

			By("verifying pause is removed")
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Namespace: cm.GetNamespace(),
				Name:      cm.GetName(),
			}, fresh)).To(Succeed())
			Expect(overrides.IsPaused(fresh)).To(BeFalse())
		})
	})

	Context("Success Reset", func() {
		It("should reset thrashing state on successful reconciliation", func() {
			detector := throttling.NewThrashingDetector()
			key := "test-ns/test-cm/ConfigMap"

			By("recording multiple throttles")
			detector.RecordThrottle(key)
			detector.RecordThrottle(key)
			Expect(detector.GetAttempts(key)).To(Equal(2))

			By("recording success")
			detector.RecordSuccess(key)

			By("verifying state is reset")
			Expect(detector.GetAttempts(key)).To(Equal(0))

			By("verifying next throttle starts fresh")
			shouldPause := detector.RecordThrottle(key)
			Expect(shouldPause).To(BeFalse(), "should start from attempt 1 after reset")
			Expect(detector.GetAttempts(key)).To(Equal(1))
		})
	})

	Context("Concurrent Access", func() {
		It("should handle concurrent throttle recording safely", func() {
			detector := throttling.NewThrashingDetector()
			key := "test-ns/test-cm/ConfigMap"

			By("recording throttles concurrently")
			done := make(chan bool, 10)
			for i := 0; i < 10; i++ {
				go func() {
					detector.RecordThrottle(key)
					done <- true
				}()
			}

			// Wait for all goroutines
			for i := 0; i < 10; i++ {
				<-done
			}

			By("verifying all throttles were recorded")
			Expect(detector.GetAttempts(key)).To(Equal(10))
		})
	})

	Context("End-to-End Pause Workflow", func() {
		It("should detect edit war, pause reconciliation, and allow recovery", func() {
			// This test simulates the full workflow:
			// 1. Resource gets modified repeatedly (simulated by hitting throttle threshold)
			// 2. Operator detects thrashing and sets pause annotation
			// 3. Reconciliation stops (patcher checks pause annotation)
			// 4. User removes pause annotation to resume

			detector := throttling.NewThrashingDetector()
			key := testNs + "/fight-cm/ConfigMap"

			By("simulating repeated modifications (throttle hits)")
			for i := 0; i < throttling.ThrashingThreshold; i++ {
				shouldPause := detector.RecordThrottle(key)
				if i < throttling.ThrashingThreshold-1 {
					Expect(shouldPause).To(BeFalse(), "should not pause before threshold")
				} else {
					Expect(shouldPause).To(BeTrue(), "should pause at threshold")
				}
			}

			By("emitting metric once")
			Expect(detector.ShouldEmitMetric(key)).To(BeTrue())
			Expect(detector.ShouldEmitMetric(key)).To(BeFalse(), "should not emit again")

			By("creating resource to test pause annotation")
			cm := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "v1",
					"kind":       "ConfigMap",
					"metadata": map[string]interface{}{
						"name":      "fight-cm",
						"namespace": testNs,
						"labels": map[string]interface{}{
							"platform.kubevirt.io/managed-by": "virt-platform-operator",
						},
					},
					"data": map[string]interface{}{
						"key": "original",
					},
				},
			}
			Expect(k8sClient.Create(ctx, cm)).To(Succeed())

			By("simulating operator setting pause annotation")
			fresh := &unstructured.Unstructured{}
			fresh.SetGroupVersionKind(cm.GroupVersionKind())
			Eventually(func() error {
				if err := k8sClient.Get(ctx, types.NamespacedName{
					Namespace: cm.GetNamespace(),
					Name:      cm.GetName(),
				}, fresh); err != nil {
					return err
				}

				annotations := fresh.GetAnnotations()
				if annotations == nil {
					annotations = make(map[string]string)
				}
				annotations[overrides.AnnotationReconcilePaused] = "true"
				fresh.SetAnnotations(annotations)

				return k8sClient.Update(ctx, fresh)
			}, 5*time.Second).Should(Succeed())

			By("verifying pause annotation is set")
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Namespace: cm.GetNamespace(),
				Name:      cm.GetName(),
			}, fresh)).To(Succeed())
			Expect(overrides.IsPaused(fresh)).To(BeTrue())

			By("simulating user removing pause annotation to resume")
			Eventually(func() error {
				if err := k8sClient.Get(ctx, types.NamespacedName{
					Namespace: cm.GetNamespace(),
					Name:      cm.GetName(),
				}, fresh); err != nil {
					return err
				}

				annotations := fresh.GetAnnotations()
				delete(annotations, overrides.AnnotationReconcilePaused)
				fresh.SetAnnotations(annotations)

				return k8sClient.Update(ctx, fresh)
			}, 5*time.Second).Should(Succeed())

			By("verifying pause is removed")
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Namespace: cm.GetNamespace(),
				Name:      cm.GetName(),
			}, fresh)).To(Succeed())
			Expect(overrides.IsPaused(fresh)).To(BeFalse())

			By("verifying reconciliation can resume (detector state reset)")
			detector.RecordSuccess(key)
			Expect(detector.GetAttempts(key)).To(Equal(0))
		})
	})
})
