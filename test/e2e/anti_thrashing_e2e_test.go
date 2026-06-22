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
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	pauseAnnotation = "platform.kubevirt.io/reconcile-paused"
	managedByLabel  = "platform.kubevirt.io/managed-by"
)

type thrashingTestAsset struct {
	GVK       schema.GroupVersionKind
	Name      string
	Namespace string
	GateCRD   string
}

// thrashingAssets lists all phase-1 "install: always" assets from metadata.yaml.
// Each generates 6 sub-tests automatically. Assets whose GateCRD is not
// installed on the cluster are skipped in BeforeAll.
var thrashingAssets = []thrashingTestAsset{
	{
		GVK:  schema.GroupVersionKind{Group: "machineconfiguration.openshift.io", Version: "v1", Kind: "MachineConfig"},
		Name: "90-worker-swap-online",
	},
	{
		GVK:     schema.GroupVersionKind{Group: "machineconfiguration.openshift.io", Version: "v1", Kind: "MachineConfig"},
		Name:    "99-openshift-machineconfig-worker-psi-karg",
		GateCRD: "kubedeschedulers.operator.openshift.io",
	},
	{
		GVK:     schema.GroupVersionKind{Group: "machineconfiguration.openshift.io", Version: "v1", Kind: "KubeletConfig"},
		Name:    "virt-perf-settings",
		GateCRD: "kubeletconfigs.machineconfiguration.openshift.io",
	},
	{
		GVK:       schema.GroupVersionKind{Group: "remediation.medik8s.io", Version: "v1alpha1", Kind: "NodeHealthCheck"},
		Name:      "virt-node-health-check",
		Namespace: "openshift-operators",
		GateCRD:   "nodehealthchecks.remediation.medik8s.io",
	},
	{
		GVK:     schema.GroupVersionKind{Group: "observability.openshift.io", Version: "v1alpha1", Kind: "UIPlugin"},
		Name:    "monitoring",
		GateCRD: "uiplugins.observability.openshift.io",
	},
	{
		GVK:       schema.GroupVersionKind{Group: "operator.openshift.io", Version: "v1", Kind: "KubeDescheduler"},
		Name:      "cluster",
		Namespace: "openshift-kube-descheduler-operator",
		GateCRD:   "kubedeschedulers.operator.openshift.io",
	},
}

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

	for _, asset := range thrashingAssets {
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
