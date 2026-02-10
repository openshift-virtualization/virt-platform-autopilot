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

	"github.com/kubevirt/virt-platform-operator/pkg/engine"
	"github.com/kubevirt/virt-platform-operator/pkg/observability"
	"github.com/kubevirt/virt-platform-operator/pkg/overrides"
)

var _ = Describe("Metrics Integration", func() {
	var (
		testNs  string
		applier *engine.Applier
	)

	BeforeEach(func() {
		testNs = "test-metrics-" + randString()

		// Create test namespace
		ns := &unstructured.Unstructured{}
		ns.SetGroupVersionKind(nsGVK)
		ns.SetName(testNs)
		Expect(k8sClient.Create(ctx, ns)).To(Succeed())

		// Create applier for testing
		applier = engine.NewApplier(k8sClient, apiReader)

		// Reset all metrics before each test to ensure clean state
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

	Context("Compliance Status Metrics", func() {
		It("should set compliance=1 when asset is successfully applied", func() {
			cm := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "v1",
					"kind":       "ConfigMap",
					"metadata": map[string]interface{}{
						"name":      "test-cm",
						"namespace": testNs,
					},
					"data": map[string]interface{}{
						"key": "value",
					},
				},
			}

			By("applying the asset")
			applied, err := applier.Apply(ctx, cm, true)
			Expect(err).NotTo(HaveOccurred())
			Expect(applied).To(BeTrue())

			By("setting compliance metric")
			observability.SetCompliance(cm, 1)

			By("verifying compliance metric is 1 (synced)")
			expected := `
				# HELP virt_platform_compliance_status Compliance status of managed resources (1=synced, 0=drifted/failed)
				# TYPE virt_platform_compliance_status gauge
				virt_platform_compliance_status{kind="ConfigMap",name="test-cm",namespace="` + testNs + `"} 1
			`
			Expect(testutil.CollectAndCompare(observability.ComplianceStatus, strings.NewReader(expected))).To(Succeed())
		})

		It("should set compliance=0 when asset fails to apply", func() {
			invalidCM := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "v1",
					"kind":       "ConfigMap",
					"metadata": map[string]interface{}{
						"namespace": testNs,
						// Missing required "name" field
					},
				},
			}

			By("attempting to apply invalid asset")
			_, err := applier.Apply(ctx, invalidCM, true)
			Expect(err).To(HaveOccurred())

			By("setting compliance metric to 0 (failed)")
			// Set name for metric (even though object is invalid)
			invalidCM.SetName("invalid-cm")
			observability.SetCompliance(invalidCM, 0)

			By("verifying compliance metric is 0 (failed)")
			expected := `
				# HELP virt_platform_compliance_status Compliance status of managed resources (1=synced, 0=drifted/failed)
				# TYPE virt_platform_compliance_status gauge
				virt_platform_compliance_status{kind="ConfigMap",name="invalid-cm",namespace="` + testNs + `"} 0
			`
			Expect(testutil.CollectAndCompare(observability.ComplianceStatus, strings.NewReader(expected))).To(Succeed())
		})

		It("should track compliance for multiple resources independently", func() {
			cm1 := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "v1",
					"kind":       "ConfigMap",
					"metadata": map[string]interface{}{
						"name":      "cm-synced",
						"namespace": testNs,
					},
					"data": map[string]interface{}{"k": "v"},
				},
			}

			cm2 := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "v1",
					"kind":       "ConfigMap",
					"metadata": map[string]interface{}{
						"name":      "cm-failed",
						"namespace": testNs,
					},
					"data": map[string]interface{}{"k": "v"},
				},
			}

			By("setting different compliance states")
			observability.SetCompliance(cm1, 1) // Synced
			observability.SetCompliance(cm2, 0) // Failed

			By("verifying both metrics are tracked independently")
			expected := `
				# HELP virt_platform_compliance_status Compliance status of managed resources (1=synced, 0=drifted/failed)
				# TYPE virt_platform_compliance_status gauge
				virt_platform_compliance_status{kind="ConfigMap",name="cm-failed",namespace="` + testNs + `"} 0
				virt_platform_compliance_status{kind="ConfigMap",name="cm-synced",namespace="` + testNs + `"} 1
			`
			Expect(testutil.CollectAndCompare(observability.ComplianceStatus, strings.NewReader(expected))).To(Succeed())
		})

		It("should update compliance metric when state changes", func() {
			cm := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "v1",
					"kind":       "ConfigMap",
					"metadata": map[string]interface{}{
						"name":      "cm-changing",
						"namespace": testNs,
					},
					"data": map[string]interface{}{"k": "v"},
				},
			}

			By("initially setting compliance to synced")
			observability.SetCompliance(cm, 1)

			By("verifying initial state is 1")
			expected1 := `
				# HELP virt_platform_compliance_status Compliance status of managed resources (1=synced, 0=drifted/failed)
				# TYPE virt_platform_compliance_status gauge
				virt_platform_compliance_status{kind="ConfigMap",name="cm-changing",namespace="` + testNs + `"} 1
			`
			Expect(testutil.CollectAndCompare(observability.ComplianceStatus, strings.NewReader(expected1))).To(Succeed())

			By("updating compliance to failed")
			observability.SetCompliance(cm, 0)

			By("verifying updated state is 0")
			expected0 := `
				# HELP virt_platform_compliance_status Compliance status of managed resources (1=synced, 0=drifted/failed)
				# TYPE virt_platform_compliance_status gauge
				virt_platform_compliance_status{kind="ConfigMap",name="cm-changing",namespace="` + testNs + `"} 0
			`
			Expect(testutil.CollectAndCompare(observability.ComplianceStatus, strings.NewReader(expected0))).To(Succeed())

			By("updating back to synced")
			observability.SetCompliance(cm, 1)

			By("verifying final state is 1")
			Expect(testutil.CollectAndCompare(observability.ComplianceStatus, strings.NewReader(expected1))).To(Succeed())
		})
	})

	Context("Thrashing Counter Metrics", func() {
		It("should increment thrashing counter when throttled", func() {
			cm := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "v1",
					"kind":       "ConfigMap",
					"metadata": map[string]interface{}{
						"name":      "thrashing-cm",
						"namespace": testNs,
					},
				},
			}

			By("incrementing thrashing counter 3 times")
			observability.IncThrashing(cm)
			observability.IncThrashing(cm)
			observability.IncThrashing(cm)

			By("verifying counter value is 3")
			expected := `
				# HELP virt_platform_thrashing_total Total number of reconciliation throttling events (anti-thrashing gate hits)
				# TYPE virt_platform_thrashing_total counter
				virt_platform_thrashing_total{kind="ConfigMap",name="thrashing-cm",namespace="` + testNs + `"} 3
			`
			Expect(testutil.CollectAndCompare(observability.ThrashingTotal, strings.NewReader(expected))).To(Succeed())
		})

		It("should track thrashing independently per resource", func() {
			cm1 := &unstructured.Unstructured{}
			cm1.SetKind("ConfigMap")
			cm1.SetName("cm1")
			cm1.SetNamespace(testNs)

			cm2 := &unstructured.Unstructured{}
			cm2.SetKind("ConfigMap")
			cm2.SetName("cm2")
			cm2.SetNamespace(testNs)

			By("incrementing thrashing for cm1 twice")
			observability.IncThrashing(cm1)
			observability.IncThrashing(cm1)

			By("incrementing thrashing for cm2 once")
			observability.IncThrashing(cm2)

			By("verifying independent counters")
			expected := `
				# HELP virt_platform_thrashing_total Total number of reconciliation throttling events (anti-thrashing gate hits)
				# TYPE virt_platform_thrashing_total counter
				virt_platform_thrashing_total{kind="ConfigMap",name="cm1",namespace="` + testNs + `"} 2
				virt_platform_thrashing_total{kind="ConfigMap",name="cm2",namespace="` + testNs + `"} 1
			`
			Expect(testutil.CollectAndCompare(observability.ThrashingTotal, strings.NewReader(expected))).To(Succeed())
		})

		It("should never decrement thrashing counter (monotonic)", func() {
			cm := &unstructured.Unstructured{}
			cm.SetKind("ConfigMap")
			cm.SetName("monotonic-cm")
			cm.SetNamespace(testNs)

			By("incrementing counter twice")
			observability.IncThrashing(cm)
			observability.IncThrashing(cm)

			By("verifying counter is 2")
			expected2 := `
				# HELP virt_platform_thrashing_total Total number of reconciliation throttling events (anti-thrashing gate hits)
				# TYPE virt_platform_thrashing_total counter
				virt_platform_thrashing_total{kind="ConfigMap",name="monotonic-cm",namespace="` + testNs + `"} 2
			`
			Expect(testutil.CollectAndCompare(observability.ThrashingTotal, strings.NewReader(expected2))).To(Succeed())

			By("incrementing again")
			observability.IncThrashing(cm)

			By("verifying counter increases to 3 (monotonic - never decreases)")
			expected3 := `
				# HELP virt_platform_thrashing_total Total number of reconciliation throttling events (anti-thrashing gate hits)
				# TYPE virt_platform_thrashing_total counter
				virt_platform_thrashing_total{kind="ConfigMap",name="monotonic-cm",namespace="` + testNs + `"} 3
			`
			Expect(testutil.CollectAndCompare(observability.ThrashingTotal, strings.NewReader(expected3))).To(Succeed())
		})
	})

	Context("Customization Info Metrics", func() {
		It("should track patch customization", func() {
			cm := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "v1",
					"kind":       "ConfigMap",
					"metadata": map[string]interface{}{
						"name":      "patched-cm",
						"namespace": testNs,
						"annotations": map[string]interface{}{
							overrides.PatchAnnotation: `[{"op":"add","path":"/data/foo","value":"bar"}]`,
						},
					},
					"data": map[string]interface{}{"key": "value"},
				},
			}

			By("setting patch customization metric")
			observability.SetCustomization(cm, "patch")

			By("verifying patch customization metric")
			expected := `
				# HELP virt_platform_customization_info Tracks intentional customizations (always 1 when present). Type: patch/ignore/unmanaged
				# TYPE virt_platform_customization_info gauge
				virt_platform_customization_info{kind="ConfigMap",name="patched-cm",namespace="` + testNs + `",type="patch"} 1
			`
			Expect(testutil.CollectAndCompare(observability.CustomizationInfo, strings.NewReader(expected))).To(Succeed())
		})

		It("should track ignore-fields customization", func() {
			cm := &unstructured.Unstructured{}
			cm.SetKind("ConfigMap")
			cm.SetName("ignored-cm")
			cm.SetNamespace(testNs)
			cm.SetAnnotations(map[string]string{
				overrides.AnnotationIgnoreFields: "/data/userField",
			})

			By("setting ignore customization metric")
			observability.SetCustomization(cm, "ignore")

			By("verifying ignore customization metric")
			expected := `
				# HELP virt_platform_customization_info Tracks intentional customizations (always 1 when present). Type: patch/ignore/unmanaged
				# TYPE virt_platform_customization_info gauge
				virt_platform_customization_info{kind="ConfigMap",name="ignored-cm",namespace="` + testNs + `",type="ignore"} 1
			`
			Expect(testutil.CollectAndCompare(observability.CustomizationInfo, strings.NewReader(expected))).To(Succeed())
		})

		It("should track unmanaged mode customization", func() {
			cm := &unstructured.Unstructured{}
			cm.SetKind("ConfigMap")
			cm.SetName("unmanaged-cm")
			cm.SetNamespace(testNs)
			cm.SetAnnotations(map[string]string{
				overrides.AnnotationMode: overrides.ModeUnmanaged,
			})

			By("setting unmanaged customization metric")
			observability.SetCustomization(cm, "unmanaged")

			By("verifying unmanaged customization metric")
			expected := `
				# HELP virt_platform_customization_info Tracks intentional customizations (always 1 when present). Type: patch/ignore/unmanaged
				# TYPE virt_platform_customization_info gauge
				virt_platform_customization_info{kind="ConfigMap",name="unmanaged-cm",namespace="` + testNs + `",type="unmanaged"} 1
			`
			Expect(testutil.CollectAndCompare(observability.CustomizationInfo, strings.NewReader(expected))).To(Succeed())
		})

		It("should track multiple customization types on same resource", func() {
			cm := &unstructured.Unstructured{}
			cm.SetKind("ConfigMap")
			cm.SetName("multi-custom-cm")
			cm.SetNamespace(testNs)

			By("setting both patch and ignore customizations")
			observability.SetCustomization(cm, "patch")
			observability.SetCustomization(cm, "ignore")

			By("verifying both customization metrics exist")
			expected := `
				# HELP virt_platform_customization_info Tracks intentional customizations (always 1 when present). Type: patch/ignore/unmanaged
				# TYPE virt_platform_customization_info gauge
				virt_platform_customization_info{kind="ConfigMap",name="multi-custom-cm",namespace="` + testNs + `",type="ignore"} 1
				virt_platform_customization_info{kind="ConfigMap",name="multi-custom-cm",namespace="` + testNs + `",type="patch"} 1
			`
			Expect(testutil.CollectAndCompare(observability.CustomizationInfo, strings.NewReader(expected))).To(Succeed())
		})

		It("should clear customization metric when annotation removed", func() {
			cm := &unstructured.Unstructured{}
			cm.SetKind("ConfigMap")
			cm.SetName("cleared-cm")
			cm.SetNamespace(testNs)

			By("setting customization metric")
			observability.SetCustomization(cm, "patch")

			By("verifying metric exists")
			count := testutil.CollectAndCount(observability.CustomizationInfo)
			Expect(count).To(Equal(1))

			By("clearing customization metric")
			observability.ClearCustomization(cm, "patch")

			By("verifying metric is removed")
			count = testutil.CollectAndCount(observability.CustomizationInfo)
			Expect(count).To(Equal(0))
		})
	})

	Context("Reconcile Duration Metrics", func() {
		It("should record reconciliation duration", func() {
			cm := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "v1",
					"kind":       "ConfigMap",
					"metadata": map[string]interface{}{
						"name":      "timed-cm",
						"namespace": testNs,
					},
					"data": map[string]interface{}{"k": "v"},
				},
			}

			By("observing a reconciliation duration")
			duration := 150 * time.Millisecond
			observability.ObserveReconcileDuration(cm, duration)

			By("verifying histogram recorded the observation")
			// For histograms, we can't check exact values, but we can verify count
			count := testutil.CollectAndCount(observability.ReconcileDuration)
			Expect(count).To(Equal(1))
		})

		It("should record duration using timer pattern", func() {
			cm := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "v1",
					"kind":       "ConfigMap",
					"metadata": map[string]interface{}{
						"name":      "timer-cm",
						"namespace": testNs,
					},
				},
			}

			By("using ReconcileDurationTimer")
			timer := observability.ReconcileDurationTimer(cm)
			time.Sleep(10 * time.Millisecond) // Simulate work
			timer.ObserveDuration()

			By("verifying histogram recorded the observation")
			count := testutil.CollectAndCount(observability.ReconcileDuration)
			Expect(count).To(Equal(1))
		})

		It("should track duration for multiple resources independently", func() {
			cm1 := &unstructured.Unstructured{}
			cm1.SetKind("ConfigMap")
			cm1.SetName("cm1")
			cm1.SetNamespace(testNs)

			cm2 := &unstructured.Unstructured{}
			cm2.SetKind("ConfigMap")
			cm2.SetName("cm2")
			cm2.SetNamespace(testNs)

			By("observing durations for both resources")
			observability.ObserveReconcileDuration(cm1, 100*time.Millisecond)
			observability.ObserveReconcileDuration(cm2, 200*time.Millisecond)

			By("verifying both observations recorded")
			count := testutil.CollectAndCount(observability.ReconcileDuration)
			Expect(count).To(Equal(2))
		})
	})

	Context("End-to-End Metric Emission", func() {
		It("should emit all relevant metrics during successful reconciliation", func() {
			cm := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "v1",
					"kind":       "ConfigMap",
					"metadata": map[string]interface{}{
						"name":      "e2e-cm",
						"namespace": testNs,
						"annotations": map[string]interface{}{
							overrides.PatchAnnotation: `[{"op":"add","path":"/data/patched","value":"true"}]`,
						},
					},
					"data": map[string]interface{}{
						"original": "value",
					},
				},
			}

			By("simulating full reconciliation")
			// 1. Apply the asset
			applied, err := applier.Apply(ctx, cm, true)
			Expect(err).NotTo(HaveOccurred())
			Expect(applied).To(BeTrue())

			// 2. Set compliance (success)
			observability.SetCompliance(cm, 1)

			// 3. Track patch customization
			observability.SetCustomization(cm, "patch")

			// 4. Record duration
			observability.ObserveReconcileDuration(cm, 50*time.Millisecond)

			By("verifying compliance metric is 1 (synced)")
			expectedCompliance := `
				# HELP virt_platform_compliance_status Compliance status of managed resources (1=synced, 0=drifted/failed)
				# TYPE virt_platform_compliance_status gauge
				virt_platform_compliance_status{kind="ConfigMap",name="e2e-cm",namespace="` + testNs + `"} 1
			`
			Expect(testutil.CollectAndCompare(observability.ComplianceStatus, strings.NewReader(expectedCompliance))).To(Succeed())

			By("verifying customization metric")
			expectedCustom := `
				# HELP virt_platform_customization_info Tracks intentional customizations (always 1 when present). Type: patch/ignore/unmanaged
				# TYPE virt_platform_customization_info gauge
				virt_platform_customization_info{kind="ConfigMap",name="e2e-cm",namespace="` + testNs + `",type="patch"} 1
			`
			Expect(testutil.CollectAndCompare(observability.CustomizationInfo, strings.NewReader(expectedCustom))).To(Succeed())

			By("verifying duration metric was recorded")
			count := testutil.CollectAndCount(observability.ReconcileDuration)
			Expect(count).To(BeNumerically(">", 0))
		})

		It("should handle failed reconciliation with correct metrics", func() {
			invalidCM := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "v1",
					"kind":       "ConfigMap",
					"metadata": map[string]interface{}{
						"namespace": testNs,
						// Missing name - will fail
					},
				},
			}

			By("attempting to apply invalid asset")
			_, err := applier.Apply(ctx, invalidCM, true)
			Expect(err).To(HaveOccurred())

			By("setting compliance to failed")
			invalidCM.SetName("failed-cm") // Set for metrics
			observability.SetCompliance(invalidCM, 0)

			By("recording duration even for failures")
			observability.ObserveReconcileDuration(invalidCM, 30*time.Millisecond)

			By("verifying compliance metric shows failure (0)")
			expectedFailed := `
				# HELP virt_platform_compliance_status Compliance status of managed resources (1=synced, 0=drifted/failed)
				# TYPE virt_platform_compliance_status gauge
				virt_platform_compliance_status{kind="ConfigMap",name="failed-cm",namespace="` + testNs + `"} 0
			`
			Expect(testutil.CollectAndCompare(observability.ComplianceStatus, strings.NewReader(expectedFailed))).To(Succeed())

			By("verifying duration was still recorded")
			count := testutil.CollectAndCount(observability.ReconcileDuration)
			Expect(count).To(BeNumerically(">", 0))
		})
	})
})
