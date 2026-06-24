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
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	pauseAnnotation = "platform.kubevirt.io/reconcile-paused"
	managedByLabel  = "platform.kubevirt.io/managed-by"
)

var _ = Describe("Anti-Thrashing E2E Tests", Ordered, ContinueOnFailure, func() {

	BeforeAll(func() {
		By("ensuring HCO exists with autopilot enabled")
		ensureHCOExists()
		patchAutopilotAndWait(autopilotEnabled)

		if isOpenShiftCluster() {
			By("setting PrometheusRule to unmanaged so operator won't revert our changes")
			setPrometheusRuleUnmanaged()

			By("reducing alert 'for' durations to 15s for faster test feedback")
			patchAlertForDurations("15s")

			By("touching HCO to trigger reconciliation and ensure metrics are emitted")
			touchHCO()
		}
	})

	AfterAll(func() {
		if isOpenShiftCluster() {
			By("restoring PrometheusRule to managed mode")
			removePrometheusRuleUnmanaged()
		}

		waitForOperatorHealthy()
	})

	for _, asset := range assetsUnderTest {
		asset := asset
		Context(fmt.Sprintf("Asset %s/%s", asset.GVK.Kind, asset.Name), Ordered, func() {
			var (
				baselineMetrics  AssetMetrics
				testStartTime    time.Time
				editWarSucceeded bool
			)

			BeforeAll(func() {
				if asset.GateCRD != "" && !crdInstalled(asset.GateCRD) {
					Skip(fmt.Sprintf("gate CRD %s not installed", asset.GateCRD))
				}

				By("verifying the resource exists before starting")
				Eventually(func() error {
					_, err := getUnstructuredResource(asset.GVK, asset.Name, asset.Namespace)
					return err
				}, timeout, interval).Should(Succeed(),
					fmt.Sprintf("%s/%s should exist", asset.GVK.Kind, asset.Name))

				testStartTime = time.Now()
				baselineMetrics = captureAssetMetrics(asset.GVK.Kind, asset.Name, asset.Namespace)
				GinkgoWriter.Printf("baseline metrics for %s/%s: thrashing_total=%d, paused_resources=%.0f\n",
					asset.GVK.Kind, asset.Name, baselineMetrics.ThrashingTotal, baselineMetrics.PausedResources)
			})

			AfterAll(func() {
				By("removing pause annotation if still present")
				obj, err := getUnstructuredResource(asset.GVK, asset.Name, asset.Namespace)
				if err == nil {
					if ann := obj.GetAnnotations(); ann != nil && ann[pauseAnnotation] == "true" {
						removePauseAnnotation(asset.GVK, asset.Name, asset.Namespace)
					}
				}

				By("restoring managed-by label if still tampered")
				restorePatch := []byte(fmt.Sprintf(`{"metadata":{"labels":{"%s":"%s"}}}`, managedByLabel, managedByValue))
				if obj != nil {
					_ = k8sClient.Patch(ctx, obj, client.RawPatch(types.MergePatchType, restorePatch))
				}

				By("touching HCO to trigger reconciliation after cleanup")
				touchHCO()

				By("waiting for operator to stabilize after cleanup")
				waitForOperatorHealthy()
			})

			// --- Test 1 ---
			It("should detect edit war and pause reconciliation", func() {
				By("triggering edit war — repeatedly tampering managed-by label until operator pauses")
				triggerEditWar(asset.GVK, asset.Name, asset.Namespace)

				By("verifying pause annotation is set")
				obj, err := getUnstructuredResource(asset.GVK, asset.Name, asset.Namespace)
				Expect(err).NotTo(HaveOccurred())
				Expect(obj.GetAnnotations()).To(HaveKeyWithValue(pauseAnnotation, "true"))

				By("verifying paused_resources metric is 1")
				Eventually(func() float64 {
					m := captureAssetMetrics(asset.GVK.Kind, asset.Name, asset.Namespace)
					return m.PausedResources
				}, 30*time.Second, 2*time.Second).Should(Equal(1.0),
					"paused_resources should be 1 after edit war")

				editWarSucceeded = true
			})

			// --- Test 2 ---
			It("should fire VirtPlatformThrashingDetected alert", func() {
				if !editWarSucceeded {
					Skip("edit war did not succeed — skipping dependent test")
				}
				if !isOpenShiftCluster() {
					Skip("Alert tests only run on OCP — Kind has no Prometheus")
				}

				By("waiting for VirtPlatformThrashingDetected alert to fire")
				attempt := 0
				maxAttempts := int((2 * time.Minute) / (10 * time.Second))
				var alertLabels map[string]string
				Eventually(func() bool {
					attempt++
					alertLabels = queryFiringAlert("VirtPlatformThrashingDetected", attempt, maxAttempts,
						"kind", asset.GVK.Kind, "name", asset.Name)
					return alertLabels != nil
				}, 2*time.Minute, 10*time.Second).Should(BeTrue(),
					"VirtPlatformThrashingDetected alert should fire when resource is paused")

				Expect(alertLabels).To(HaveKeyWithValue("severity", "warning"))
				Expect(alertLabels).To(HaveKeyWithValue("operator", "virt-platform-autopilot"))
			})

			// --- Test 3 ---
			It("should resume reconciliation when pause annotation is removed", func() {
				if !editWarSucceeded {
					Skip("edit war did not succeed — skipping dependent test")
				}

				// Workaround for CNV-89796: on Kind, test 2 is skipped so the token
				// bucket is still empty when we reach here. Wait for at least one
				// token to refill before removing the annotation, otherwise the
				// operator's tight reconciliation loop catches it with 0 tokens.
				if !isOpenShiftCluster() {
					time.Sleep(6 * time.Second)
				}

				By("removing pause annotation")
				removePauseAnnotation(asset.GVK, asset.Name, asset.Namespace)

				By("touching HCO to trigger reconciliation")
				touchHCO()

				By("waiting for operator to restore managed-by label via SSA")
				Eventually(func() string {
					obj, err := getUnstructuredResource(asset.GVK, asset.Name, asset.Namespace)
					if err != nil {
						return ""
					}
					return obj.GetLabels()[managedByLabel]
				}, 3*time.Minute, 5*time.Second).Should(Equal(managedByValue),
					"Operator should restore managed-by label after resuming reconciliation")

				By("verifying paused_resources metric is 0")
				Eventually(func() float64 {
					m := captureAssetMetrics(asset.GVK.Kind, asset.Name, asset.Namespace)
					return m.PausedResources
				}, 30*time.Second, 2*time.Second).Should(Equal(0.0),
					"paused_resources should be 0 after resume")
			})

			// --- Test 4 ---
			It("should clear VirtPlatformThrashingDetected alert after resume", func() {
				if !editWarSucceeded {
					Skip("edit war did not succeed — skipping dependent test")
				}
				if !isOpenShiftCluster() {
					Skip("Alert tests only run on OCP — Kind has no Prometheus")
				}

				By("waiting for VirtPlatformThrashingDetected alert to resolve")
				attempt := 0
				maxAttempts := int((2 * time.Minute) / (10 * time.Second))
				Eventually(func() bool {
					attempt++
					return queryAlertNotFiring("VirtPlatformThrashingDetected", attempt, maxAttempts,
						"kind", asset.GVK.Kind, "name", asset.Name)
				}, 2*time.Minute, 10*time.Second).Should(BeTrue(),
					"VirtPlatformThrashingDetected alert should resolve after resume")
			})

			// --- Test 5 ---
			It("should record Throttled and ThrashingDetected events", func() {
				if !editWarSucceeded {
					Skip("edit war did not succeed — skipping dependent test")
				}

				By("checking for Throttled events")
				throttledEvents := findEvents(EventFilter{
					Reason: "Throttled", Since: testStartTime,
					Kind: asset.GVK.Kind, Name: asset.Name,
				})
				GinkgoWriter.Printf("found %d Throttled events for %s/%s\n",
					len(throttledEvents), asset.GVK.Kind, asset.Name)
				Expect(throttledEvents).NotTo(BeEmpty(),
					"At least one Throttled event should exist for this asset")

				By("checking for ThrashingDetected event")
				thrashingEvents := findEvents(EventFilter{
					Reason: "ThrashingDetected", Since: testStartTime,
					Kind: asset.GVK.Kind, Name: asset.Name,
				})
				GinkgoWriter.Printf("found %d ThrashingDetected events for %s/%s\n",
					len(thrashingEvents), asset.GVK.Kind, asset.Name)
				Expect(thrashingEvents).To(HaveLen(1),
					"Exactly one ThrashingDetected event should exist for this asset")

				By("verifying ThrashingDetected event contains recovery instructions")
				Expect(thrashingEvents[0].Note).To(ContainSubstring(pauseAnnotation),
					"ThrashingDetected event should mention the pause annotation for recovery")
			})

			// --- Test 6 ---
			It("should emit thrashing metric only once per episode", func() {
				if !editWarSucceeded {
					Skip("edit war did not succeed — skipping dependent test")
				}

				By("capturing thrashing_total metric after edit war")
				currentMetrics := captureAssetMetrics(asset.GVK.Kind, asset.Name, asset.Namespace)

				By("verifying thrashing_total incremented by exactly 1")
				increment := currentMetrics.ThrashingTotal - baselineMetrics.ThrashingTotal
				GinkgoWriter.Printf("thrashing_total: baseline=%d, current=%d, increment=%d\n",
					baselineMetrics.ThrashingTotal, currentMetrics.ThrashingTotal, increment)
				Expect(increment).To(Equal(1),
					"thrashing_total should increment by exactly 1 per edit war episode")
			})
		})
	}
})

var _ = Describe("Namespace Guard E2E Tests (CNV-89799, CNV-89801)", Ordered, ContinueOnFailure, func() {

	BeforeAll(func() {
		if isOpenShiftCluster() {
			Skip("Namespace guard tests only run on Kind — on OCP, namespaces are managed by operators")
		}

		ensureHCOExists()
		patchAutopilotAndWait(autopilotEnabled)
	})

	AfterAll(func() {
		if isOpenShiftCluster() {
			return
		}
		waitForOperatorHealthy()
	})

	for _, asset := range assetsUnderTest {
		asset := asset
		if asset.Namespace == "" {
			continue
		}

		Context(fmt.Sprintf("Asset %s/%s (namespace %s)", asset.GVK.Kind, asset.Name, asset.Namespace), Ordered, func() {
			var (
				testStartTime     time.Time
				baselineThrashing int
			)

			BeforeAll(func() {
				if asset.GateCRD != "" && !crdInstalled(asset.GateCRD) {
					Skip(fmt.Sprintf("gate CRD %s not installed", asset.GateCRD))
				}

				By("ensuring gate CRD is installed")
				if asset.GateCRD != "" {
					ensureCRDInstalled(asset.GateCRD)
				}

				By(fmt.Sprintf("deleting namespace %s if it exists", asset.Namespace))
				ns := &corev1.Namespace{}
				if err := k8sClient.Get(ctx, client.ObjectKey{Name: asset.Namespace}, ns); err == nil {
					Expect(k8sClient.Delete(ctx, ns)).To(Succeed())
					Eventually(func() bool {
						return apierrors.IsNotFound(
							k8sClient.Get(ctx, client.ObjectKey{Name: asset.Namespace}, &corev1.Namespace{}))
					}, 2*time.Minute, 2*time.Second).Should(BeTrue(),
						fmt.Sprintf("Namespace %s should be fully deleted", asset.Namespace))
				}

				if asset.ClusterScoped {
					By(fmt.Sprintf("deleting cluster-scoped %s/%s if it survived namespace deletion", asset.GVK.Kind, asset.Name))
					obj := &unstructured.Unstructured{}
					obj.SetGroupVersionKind(asset.GVK)
					if err := k8sClient.Get(ctx, client.ObjectKey{Name: asset.Name}, obj); err == nil {
						Expect(k8sClient.Delete(ctx, obj)).To(Succeed())
						Eventually(func() bool {
							return apierrors.IsNotFound(
								k8sClient.Get(ctx, client.ObjectKey{Name: asset.Name}, obj))
						}, 30*time.Second, 2*time.Second).Should(BeTrue(),
							fmt.Sprintf("%s/%s should be deleted", asset.GVK.Kind, asset.Name))
					}
				}

				testStartTime = time.Now()
				baselineThrashing = captureAssetMetrics(asset.GVK.Kind, asset.Name, asset.Namespace).ThrashingTotal
			})

			It("should not generate ThrashingDetected or Throttled events across multiple reconciliations", func() {
				By("triggering 12 reconciliation cycles (above token-bucket capacity of 10)")
				for i := 0; i < 12; i++ {
					touchHCO()
					time.Sleep(1 * time.Second)
				}
				waitForOperatorHealthy()

				By("verifying reconciliations did occur during the test window")
				Expect(findEvents(EventFilter{
					Reason: "ReconcileSucceeded", Since: testStartTime,
				})).NotTo(BeEmpty(),
					"At least one ReconcileSucceeded event should exist, proving the operator was active")

				By(fmt.Sprintf("verifying no ThrashingDetected events for %s/%s", asset.GVK.Kind, asset.Name))
				Expect(findEvents(EventFilter{
					Reason: "ThrashingDetected", Since: testStartTime,
					Kind: asset.GVK.Kind, Name: asset.Name,
				})).To(BeEmpty(),
					"No ThrashingDetected events should be generated when target namespace is missing")

				By(fmt.Sprintf("verifying no Throttled events for %s/%s", asset.GVK.Kind, asset.Name))
				Expect(findEvents(EventFilter{
					Reason: "Throttled", Since: testStartTime,
					Kind: asset.GVK.Kind, Name: asset.Name,
				})).To(BeEmpty(),
					"No Throttled events should be generated when target namespace is missing")
			})

			It("should not increment the thrashing metric", func() {
				metrics := captureAssetMetrics(asset.GVK.Kind, asset.Name, asset.Namespace)

				Expect(metrics.ThrashingTotal).To(Equal(baselineThrashing),
					"thrashing_total should not increase when target namespace is missing")

				Expect(metrics.PausedResources).NotTo(Equal(1.0),
					"resource should not be marked as paused when target namespace is missing")
			})

			It("should log the namespace-not-found skip message in the operator pod", func() {
				Eventually(func() string {
					return getOperatorLogs(testStartTime)
				}, 30*time.Second, 5*time.Second).Should(
					And(
						ContainSubstring("Target namespace not found, skipping asset"),
						ContainSubstring(asset.Namespace),
					),
					fmt.Sprintf("Operator logs should contain the namespace guard message for %s", asset.Namespace))
			})

			AfterAll(func() {
				By(fmt.Sprintf("ensuring namespace %s exists", asset.Namespace))
				ns := &corev1.Namespace{
					ObjectMeta: metav1.ObjectMeta{Name: asset.Namespace},
				}
				if err := k8sClient.Create(ctx, ns); err != nil && !apierrors.IsAlreadyExists(err) {
					Expect(err).NotTo(HaveOccurred())
				}

				By("touching HCO to trigger reconciliation after restoring namespace")
				touchHCO()

				waitForOperatorHealthy()
			})
		})
	}
})
