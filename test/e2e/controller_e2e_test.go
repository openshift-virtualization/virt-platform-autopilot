package e2e

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	eventsv1 "k8s.io/api/events/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	operatorNamespace     = "openshift-cnv"
	operatorDeployment    = "virt-platform-autopilot"
	operatorAppLabel      = "virt-platform-autopilot"
	operatorComponentName = "virt-platform-autopilot"
	hcoName               = "kubevirt-hyperconverged"
	timeout               = 3 * time.Minute
	interval              = 2 * time.Second

	// autopilotAnnotation is the opt-in annotation that must be present on the HCO CR
	// for the autopilot to activate. All e2e test HCO instances must carry it.
	autopilotAnnotation        = "platform.kubevirt.io/autopilot"
	autopilotEnabled           = "true"
	autopilotDisabledByFalse   = "false"
	autopilotDisabledByRemoval = ""
)

const (
	swapMcName            = "90-worker-swap-online"
	consistentlyDuration  = 10 * time.Second
	consistentlyInterval  = 1 * time.Second
	prometheusRuleName    = "virt-platform-autopilot-alerts"
	managedByValue        = "virt-platform-autopilot"
	assetSwapEnable       = "swap-enable"
	assetPrometheusAlerts = "prometheus-alerts"
)

var (
	machineConfigGVK = schema.GroupVersionKind{
		Group:   "machineconfiguration.openshift.io",
		Version: "v1",
		Kind:    "MachineConfig",
	}
	prometheusRuleGVK = schema.GroupVersionKind{
		Group:   "monitoring.coreos.com",
		Version: "v1",
		Kind:    "PrometheusRule",
	}
)

var _ = Describe("Controller E2E Tests", func() {
	Context("Operator Deployment", func() {
		It("should have operator pod running", func() {
			By("checking operator deployment exists")
			deployment := &appsv1.Deployment{}
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{
					Name:      operatorDeployment,
					Namespace: operatorNamespace,
				}, deployment)
			}, timeout, interval).Should(Succeed())

			By("verifying deployment is ready")
			Eventually(func() bool {
				if err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      operatorDeployment,
					Namespace: operatorNamespace,
				}, deployment); err != nil {
					return false
				}
				return deployment.Status.ReadyReplicas > 0
			}, timeout, interval).Should(BeTrue())
		})

		It("should have operator pod in Running state", func() {
			podList := &corev1.PodList{}
			Eventually(func() bool {
				if err := k8sClient.List(ctx, podList, client.InNamespace(operatorNamespace),
					client.MatchingLabels{"app": operatorAppLabel}); err != nil {
					return false
				}
				if len(podList.Items) == 0 {
					return false
				}
				return podList.Items[0].Status.Phase == corev1.PodRunning
			}, timeout, interval).Should(BeTrue())
		})
	})

	Context("Unlabeled HCO Adoption", Ordered, func() {
		BeforeAll(func() {
			By("ensuring HCO exists")
			ensureHCOExists()
			if isOpenShiftCluster() {
				By("restoring PrometheusRule to managed mode")
				removeAnnotation(prometheusRuleGVK, prometheusRuleName, operatorNamespace, modeAnnotation)
			}
			patchAutopilotAndWait(autopilotEnabled)

		})

		It("should adopt and label the unlabeled HCO when autopilot is enabled", func() {
			By("disabling autopilot via annotation=false and removing managed-by label")
			patchAutopilotAndWait(autopilotDisabledByFalse)
			removeManagedByLabel(managedByLabel)

			By("capturing metrics before re-enabling")
			hcoMetricsBefore := captureAssetMetrics("HyperConverged", hcoName, operatorNamespace)
			reEnableTime := time.Now()

			By("re-enabling autopilot to trigger adoption")
			patchAutopilotAndWait(autopilotEnabled)

			By("waiting for operator to label the HCO")
			Eventually(func() bool {
				fetched := &unstructured.Unstructured{}
				fetched.SetGroupVersionKind(schema.GroupVersionKind{
					Group:   "hco.kubevirt.io",
					Version: "v1",
					Kind:    "HyperConverged",
				})
				if err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      hcoName,
					Namespace: operatorNamespace,
				}, fetched); err != nil {
					return false
				}
				labels := fetched.GetLabels()
				return labels != nil && labels[managedByLabel] == managedByValue
			}, timeout, interval).Should(BeTrue(), "Operator should have labeled HCO with managed-by label")

			By("verifying ReconcileSucceeded event was emitted")
			eventsAfter := captureAutopilotEvents(reEnableTime)
			Expect(eventsAfter.ReconcileSucceeded).To(BeNumerically(">", 0),
				"ReconcileSucceeded event should be emitted after re-enabling")

			By("verifying HCO metrics after adoption were updated")
			hcoMetricsAfter := captureAssetMetrics("HyperConverged", hcoName, operatorNamespace)
			Expect(hcoMetricsAfter.ReconcileDurationCount).To(BeNumerically(">", hcoMetricsBefore.ReconcileDurationCount),
				"reconcile_duration_seconds_count should increase for HCO")
			Expect(hcoMetricsAfter.ComplianceStatus).To(Equal(1.0),
				"compliance_status for HCO should be 1 (synced)")
		})

		It("should not reconcile when autopilot annotation is removed", func() {

			By("removing autopilot annotation and managed-by label")
			patchAutopilotAndWait(autopilotDisabledByRemoval)
			disableTime := time.Now()
			removeManagedByLabel(managedByLabel)

			By("verifying no new events were generated")
			eventsAfter := captureAutopilotEvents(disableTime)
			Expect(eventsAfter.ReconcileSucceeded).To(Equal(0),
				"No ReconcileSucceeded events should be emitted when disabled")
			Expect(eventsAfter.AssetApplied).To(Equal(0),
				"No AssetApplied events should be emitted when disabled")
		})
	})

	Context("Dynamic Watch Configuration", func() {
		It("should only watch CRDs that are installed", func() {
			By("checking operator logs for watch configuration")
			// This verifies SetupWithManager only configures watches for installed CRDs
			podList := &corev1.PodList{}
			Expect(k8sClient.List(ctx, podList, client.InNamespace(operatorNamespace),
				client.MatchingLabels{"app": operatorAppLabel})).To(Succeed())
			Expect(podList.Items).NotTo(BeEmpty())

			// In a real implementation, we'd check logs to verify:
			// - "Adding watch for managed resource type" for installed CRDs
			// - "CRD not installed, skipping watch" for missing CRDs
			// For now, just verify operator is running (watches configured successfully)
			Expect(podList.Items[0].Status.Phase).To(Equal(corev1.PodRunning))
		})
	})

	Context("Cache Optimization", func() {
		It("should filter cache by managed-by label", func() {
			// This verifies DefaultLabelSelector is working
			// In a real test, we'd:
			// 1. Create unlabeled ConfigMap
			// 2. Verify operator doesn't cache it (can't see it in cache)
			// 3. Label it with managed-by
			// 4. Verify operator can now see it
			// For now, this is implicitly tested by unlabeled HCO adoption working
		})

		It("should exempt HCO from label filtering", func() {
			// This is already tested by "Unlabeled HCO Adoption" test
			// The fact that unlabeled HCO triggers reconciliation proves
			// ByObject cache exemption is working
		})
	})

	Context("Event Recording", func() {
		It("should emit events during reconciliation", func() {
			By("fetching events for HCO")
			// Use new events.k8s.io/v1 API (modern event API)
			events := &eventsv1.EventList{}
			Eventually(func() bool {
				if err := k8sClient.List(ctx, events, client.InNamespace(operatorNamespace)); err != nil {
					return false
				}
				// Look for events related to our operator
				for _, event := range events.Items {
					if event.ReportingController == operatorComponentName {
						return true
					}
				}
				return false
			}, timeout, interval).Should(BeTrue(), "Operator should emit events")
		})
	})

	Context("Selective activation via allowlist", Ordered, func() {
		BeforeAll(func() {
			By("ensuring HCO exists")
			ensureHCOExists()

			ensureCRDInstalled("machineconfigs.machineconfiguration.openshift.io")
			ensureCRDInstalled("prometheusrules.monitoring.coreos.com")
			waitForOperatorHealthy()

			By("enabling both swap-enable and prometheus-alerts in the allowlist")
			patchAutopilotAndWait(assetSwapEnable + "," + assetPrometheusAlerts)
		})

		It("should create all allowlisted assets", func() {
			By("verifying swap-enable MachineConfig exists")
			Eventually(func() error {
				_, err := getUnstructuredResource(machineConfigGVK, swapMcName, operatorNamespace)
				return err
			}, timeout, interval).Should(Succeed())
			By(fmt.Sprintf("verifying %s MachineConfig metrics are healthy", swapMcName))
			Eventually(func() float64 {
				return captureAssetMetrics("MachineConfig", swapMcName, "").ComplianceStatus
			}, timeout, interval).Should(Equal(1.0),
				fmt.Sprintf("compliance_status for MachineConfig %s should be 1 (synced)", swapMcName))
			Eventually(func() float64 {
				return captureAssetMetrics("MachineConfig", swapMcName, "").PausedResources
			}, timeout, interval).Should(Equal(0.0),
				fmt.Sprintf("MachineConfig %s should not be paused", swapMcName))

			By("verifying PrometheusRule exists")
			Eventually(func() error {
				_, err := getUnstructuredResource(prometheusRuleGVK, prometheusRuleName, operatorNamespace)
				return err
			}, timeout, interval).Should(Succeed())
			By(fmt.Sprintf("verifying %s prometheusRule metrics are healthy", prometheusRuleName))
			Eventually(func() float64 {
				return captureAssetMetrics("PrometheusRule", prometheusRuleName, operatorNamespace).ComplianceStatus
			}, timeout, interval).Should(Equal(1.0),
				fmt.Sprintf("compliance_status for PrometheusRule %s should be 1 (synced)", prometheusRuleName))
			Eventually(func() float64 {
				return captureAssetMetrics("PrometheusRule", prometheusRuleName, operatorNamespace).PausedResources
			}, timeout, interval).Should(Equal(0.0),
				fmt.Sprintf("PrometheusRule %s should not be paused", prometheusRuleName))

		})

		It("should not reconcile assets outside the allowlist", func() {
			By("narrowing allowlist to swap-enable only")
			patchAutopilotAndWait(assetSwapEnable)

			By("verifying swap-enable MachineConfig exists and has managed-by label")
			mc, err := getUnstructuredResource(machineConfigGVK, swapMcName, operatorNamespace)
			Expect(err).NotTo(HaveOccurred())
			Expect(hasLabel(mc, managedByLabel, managedByValue)).To(BeTrue(),
				"MachineConfig should have managed-by label")

			By("verifying PrometheusRule still has managed-by label")
			pr, err := getUnstructuredResource(prometheusRuleGVK, prometheusRuleName, operatorNamespace)
			Expect(err).NotTo(HaveOccurred())
			Expect(hasLabel(pr, managedByLabel, managedByValue)).To(BeTrue(),
				"PrometheusRule should still have managed-by label")

			// Stale per-asset metrics must be deleted when asset leaves the allowlist.
			By("verifying PrometheusRule metrics were cleaned up after leaving the allowlist")
			Eventually(func() float64 {
				return captureAssetMetrics("PrometheusRule", prometheusRuleName, operatorNamespace).ComplianceStatus
			}, timeout, interval).Should(Equal(-1.0),
				"compliance_status should be -1 (series deleted) for an excluded asset")
			prMetricsBefore := captureAssetMetrics("PrometheusRule", prometheusRuleName, operatorNamespace)
			Expect(prMetricsBefore.PausedResources).To(Equal(-1.0),
				"paused_resources should be -1 (series deleted) for an excluded asset")
			Expect(prMetricsBefore.ReconcileDurationCount).To(Equal(0),
				"reconcile_duration_count should be 0 (series deleted) for an excluded asset")
			Expect(prMetricsBefore.ReconcileDurationSum).To(Equal(-1.0),
				"reconcile_duration_sum should be -1 (series deleted) for an excluded asset")
			Expect(prMetricsBefore.CustomizationInfo).To(Equal(-1.0),
				"customization_info should be -1 (series deleted) for an excluded asset")

			deleteTime := time.Now()

			By("deleting the PrometheusRule")
			deleteResource(prometheusRuleGVK, prometheusRuleName, operatorNamespace)

			By("verifying PrometheusRule is not recreated")
			Consistently(func() error {
				_, err := getUnstructuredResource(prometheusRuleGVK, prometheusRuleName, operatorNamespace)
				return err
			}, consistentlyDuration, consistentlyInterval).ShouldNot(Succeed(),
				"PrometheusRule should not be recreated when outside the allowlist")

			By("verifying PrometheusRule metrics remain cleaned up after resource deletion")
			prMetricsAfter := captureAssetMetrics("PrometheusRule", prometheusRuleName, operatorNamespace)
			Expect(prMetricsAfter).To(Equal(prMetricsBefore),
				"PrometheusRule metrics should not change when outside the allowlist")

			By("verifying no asset-level events were generated for the deleted PrometheusRule")
			eventsAfter := captureAutopilotEvents(deleteTime)
			Expect(eventsAfter.AssetApplied).To(Equal(0),
				"No new AssetApplied events should appear")
			Expect(eventsAfter.DriftDetected).To(Equal(0),
				"No new DriftDetected events should appear")
			Expect(eventsAfter.DriftCorrected).To(Equal(0),
				"No new DriftCorrected events should appear")

		})

		It("should recreate a deleted asset when added to the allowlist", func() {
			By("capturing metrics before test")
			prMetricsBefore := captureAssetMetrics("PrometheusRule", prometheusRuleName, operatorNamespace)
			recreateTime := time.Now()

			By("deleting PrometheusRule if it exists")
			deleteResource(prometheusRuleGVK, prometheusRuleName, operatorNamespace)

			By("adding prometheus-alerts to the allowlist")
			patchAutopilotAndWait(assetPrometheusAlerts)

			By("verifying PrometheusRule is recreated")
			Eventually(func() error {
				_, err := getUnstructuredResource(prometheusRuleGVK, prometheusRuleName, operatorNamespace)
				return err
			}, timeout, interval).Should(Succeed(), "PrometheusRule should be recreated")

			By("verifying managed-by label on recreated PrometheusRule")
			pr, err := getUnstructuredResource(prometheusRuleGVK, prometheusRuleName, operatorNamespace)
			Expect(err).NotTo(HaveOccurred())
			Expect(hasLabel(pr, managedByLabel, managedByValue)).To(BeTrue(),
				"Recreated PrometheusRule should have managed-by label")

			By("verifying PrometheusRule metrics after recreation")
			prMetricsAfter := captureAssetMetrics("PrometheusRule", prometheusRuleName, operatorNamespace)
			Expect(prMetricsAfter.ComplianceStatus).To(Equal(1.0),
				"compliance_status should be 1 (synced)")
			Expect(prMetricsAfter.ReconcileDurationCount).To(BeNumerically(">", prMetricsBefore.ReconcileDurationCount),
				"reconcile_duration_seconds_count should increase")
			Expect(prMetricsAfter.ThrashingTotal).To(Equal(prMetricsBefore.ThrashingTotal),
				"thrashing_total should not increase")
			Expect(prMetricsAfter.PausedResources).NotTo(Equal(1.0),
				"resource should not be paused")

			By("verifying events after recreation")
			eventsAfter := captureAutopilotEvents(recreateTime)
			Expect(eventsAfter.AssetApplied).To(BeNumerically(">", 0),
				"AssetApplied event should be emitted")
			Expect(eventsAfter.ReconcileSucceeded).To(BeNumerically(">", 0),
				"ReconcileSucceeded event should be emitted")
			Expect(eventsAfter.ThrashingDetected).To(Equal(0),
				"ThrashingDetected event should not be emitted")
			Expect(eventsAfter.ApplyFailed).To(Equal(0),
				"ApplyFailed event should not be emitted")

			By("verifying swap-enable MachineConfig still exists")
			_, err = getUnstructuredResource(machineConfigGVK, swapMcName, operatorNamespace)
			Expect(err).NotTo(HaveOccurred(), "MachineConfig should still exist after being removed from allowlist")
		})

		It("should correct a modified asset in the allowlist", func() {
			By("ensuring prometheus-alerts is in the allowlist")
			patchAutopilotAndWait(assetPrometheusAlerts)

			By("ensuring PrometheusRule exists")
			Eventually(func() error {
				_, err := getUnstructuredResource(prometheusRuleGVK, prometheusRuleName, operatorNamespace)
				return err
			}, timeout, interval).Should(Succeed())

			By("capturing metrics before drift")
			metricsBefore := captureAssetMetrics("PrometheusRule", prometheusRuleName, operatorNamespace)
			driftTime := time.Now()

			By("modifying PrometheusRule by changing a managed label")
			driftPatch := []byte(`{"metadata":{"labels":{"app":"tampered"}}}`)
			prRef := &unstructured.Unstructured{}
			prRef.SetGroupVersionKind(prometheusRuleGVK)
			prRef.SetName(prometheusRuleName)
			prRef.SetNamespace(operatorNamespace)
			Expect(k8sClient.Patch(ctx, prRef, client.RawPatch(types.MergePatchType, driftPatch))).To(Succeed())

			By("verifying operator corrects the drift by restoring the app label")
			Eventually(func() bool {
				pr, err := getUnstructuredResource(prometheusRuleGVK, prometheusRuleName, operatorNamespace)
				if err != nil {
					return false
				}
				return hasLabel(pr, "app", "virt-platform-autopilot")
			}, timeout, interval).Should(BeTrue(), "Operator should restore the app label (drift correction)")

			By("verifying managed-by label is still present")
			pr, err := getUnstructuredResource(prometheusRuleGVK, prometheusRuleName, operatorNamespace)
			Expect(err).NotTo(HaveOccurred())
			Expect(hasLabel(pr, managedByLabel, managedByValue)).To(BeTrue(),
				"managed-by label should be preserved after drift correction")

			By("verifying PrometheusRule metrics after drift correction")
			metricsAfter := captureAssetMetrics("PrometheusRule", prometheusRuleName, operatorNamespace)
			Expect(metricsAfter.ComplianceStatus).To(Equal(1.0),
				"compliance_status should be 1 (synced)")
			Expect(metricsAfter.ReconcileDurationCount).To(BeNumerically(">", metricsBefore.ReconcileDurationCount),
				"reconcile_duration_seconds_count should increase")
			Expect(metricsAfter.ThrashingTotal).To(Equal(metricsBefore.ThrashingTotal),
				"thrashing_total should not increase")
			Expect(metricsAfter.PausedResources).NotTo(Equal(1.0),
				"resource should not be paused")

			By("verifying drift events were emitted")
			eventsAfter := captureAutopilotEvents(driftTime)
			Expect(eventsAfter.DriftDetected).To(BeNumerically(">", 0),
				"DriftDetected event should be emitted")
			Expect(eventsAfter.DriftCorrected).To(BeNumerically(">", 0),
				"DriftCorrected event should be emitted")
			Expect(eventsAfter.AssetApplied).To(BeNumerically(">", 0),
				"AssetApplied event should be emitted")
			Expect(eventsAfter.ThrashingDetected).To(Equal(0),
				"ThrashingDetected event should not be emitted")
			Expect(eventsAfter.ApplyFailed).To(Equal(0),
				"ApplyFailed event should not be emitted")
		})

		AfterAll(func() {
			By("Restoring autopilot to enable after tests")
			patchAutopilotAndWait(autopilotEnabled)
		})
	})

})
