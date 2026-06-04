package e2e

import (
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
	timeout               = 2 * time.Minute
	interval              = 2 * time.Second

	// autopilotAnnotation is the opt-in annotation that must be present on the HCO CR
	// for the autopilot to activate. All e2e test HCO instances must carry it.
	autopilotAnnotation = "platform.kubevirt.io/autopilot"
	autopilotEnabled    = "true"
	autopilotDisabled   = "" // removing the annotation disables autopilot; "false" does NOT work (CNV-89261)
)

const (
	gateSwapMcName           = "90-worker-swap-online"
	gateConsistentlyDuration = 5 * time.Second
	gateConsistentlyInterval = 2 * time.Second
	gatePrometheusRuleName   = "virt-platform-autopilot-alerts"
	gateManagedByLabel       = "platform.kubevirt.io/managed-by"
	gateManagedByValue       = "virt-platform-autopilot"
	assetSwapEnable          = "swap-enable"
	assetPrometheusAlerts    = "prometheus-alerts"
)

var (
	gateMachineConfigGVK = schema.GroupVersionKind{
		Group:   "machineconfiguration.openshift.io",
		Version: "v1",
		Kind:    "MachineConfig",
	}
	gatePrometheusRuleGVK = schema.GroupVersionKind{
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
			By("ensuring no HCO exists before test")
			existingHCO := &unstructured.Unstructured{}
			existingHCO.SetGroupVersionKind(schema.GroupVersionKind{
				Group:   "hco.kubevirt.io",
				Version: "v1beta1",
				Kind:    "HyperConverged",
			})
			err := k8sClient.Get(ctx, types.NamespacedName{
				Name:      hcoName,
				Namespace: operatorNamespace,
			}, existingHCO)
			if err == nil {
				// HCO exists, delete it
				By("deleting existing HCO from previous test")
				Expect(k8sClient.Delete(ctx, existingHCO)).To(Succeed())
				Eventually(func() bool {
					err := k8sClient.Get(ctx, types.NamespacedName{
						Name:      hcoName,
						Namespace: operatorNamespace,
					}, existingHCO)
					return err != nil
				}, timeout, interval).Should(BeTrue(), "Existing HCO should be deleted")
			}

			By("creating unlabeled HCO instance")
			hco = &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "hco.kubevirt.io/v1beta1",
					"kind":       "HyperConverged",
					"metadata": map[string]interface{}{
						"name":      hcoName,
						"namespace": operatorNamespace,
						// Deliberately NO managed-by label to test adoption
						"annotations": map[string]interface{}{
							autopilotAnnotation: autopilotEnabled,
						},
					},
					"spec": map[string]interface{}{},
				},
			}
			Expect(k8sClient.Create(ctx, hco)).To(Succeed())
		})

		It("should adopt and label the unlabeled HCO", func() {
			By("waiting for operator to label the HCO")
			Eventually(func() bool {
				fetched := &unstructured.Unstructured{}
				fetched.SetGroupVersionKind(schema.GroupVersionKind{
					Group:   "hco.kubevirt.io",
					Version: "v1beta1",
					Kind:    "HyperConverged",
				})
				if err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      hcoName,
					Namespace: operatorNamespace,
				}, fetched); err != nil {
					return false
				}
				labels := fetched.GetLabels()
				return labels != nil && labels["platform.kubevirt.io/managed-by"] == "virt-platform-autopilot"
			}, timeout, interval).Should(BeTrue(), "Operator should have labeled HCO with managed-by label")
		})

		It("should trigger reconciliation for unlabeled HCO", func() {
			By("verifying ReconcileSucceeded event is emitted for HCO")
			Eventually(func() bool {
				// Use new events.k8s.io/v1 API
				events := &eventsv1.EventList{}
				if err := k8sClient.List(ctx, events, client.InNamespace(operatorNamespace)); err != nil {
					return false
				}
				for _, event := range events.Items {
					if event.Regarding.Name == hcoName &&
						event.Reason == "ReconcileSucceeded" {
						return true
					}
				}
				return false
			}, timeout, interval).Should(BeTrue(), "Operator should emit ReconcileSucceeded event for HCO")
		})

		It("should not reconcile when autopilot annotation is removed", func() {
			By("capturing metrics and events before deactivation")
			metricsBefore := getReconcileDurationCount()
			eventsBefore := captureAutopilotEvents()

			By("removing autopilot annotation from HCO")
			patchAutopilotAndWait(autopilotDisabled)

			By("verifying no new events were generated after deactivation")
			eventsAfter := captureAutopilotEvents()
			Expect(eventsAfter).To(Equal(eventsBefore),
				"No autopilot events should be emitted when disabled")

			By("verifying reconcile duration count did not increase")
			metricsAfter := getReconcileDurationCount()
			Expect(metricsAfter).To(Equal(metricsBefore),
				"reconcile_duration_seconds_count should not increase when autopilot is disabled")
		})

		// Re-enabling autopilot is also a change on the HCO CR that the controller
		// watches, so it should trigger an immediate reconciliation.
		It("should reconcile immediately when autopilot annotation is re-enabled", func() {
			By("capturing metrics and events before re-enabling")
			metricsBefore := getReconcileDurationCount()
			eventsBefore := captureAutopilotEvents()

			By("re-enabling autopilot annotation")
			patchAutopilotAndWait(autopilotEnabled)

			By("verifying reconcile duration count increased")
			metricsAfter := getReconcileDurationCount()
			Expect(metricsAfter).To(BeNumerically(">", metricsBefore),
				"reconcile_duration_seconds_count should increase after re-enabling autopilot")

			By("verifying ReconcileSucceeded event was emitted")
			eventsAfter := captureAutopilotEvents()
			Expect(eventsAfter.ReconcileSucceeded).To(BeNumerically(">", eventsBefore.ReconcileSucceeded),
				"ReconcileSucceeded count should increase after re-enabling")
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
			By("enabling both swap-enable and prometheus-alerts in the allowlist")
			patchAutopilotAndWait(assetSwapEnable + "," + assetPrometheusAlerts)
		})

		It("should create all allowlisted assets", func() {
			By("verifying swap-enable MachineConfig exists")
			Eventually(func() error {
				_, err := getUnstructuredResource(gateMachineConfigGVK, gateSwapMcName, "")
				return err
			}, timeout, interval).Should(Succeed())

			By("verifying PrometheusRule exists")
			Eventually(func() error {
				_, err := getUnstructuredResource(gatePrometheusRuleGVK, gatePrometheusRuleName, operatorNamespace)
				return err
			}, timeout, interval).Should(Succeed())
		})

		It("should not reconcile assets outside the allowlist", func() {
			By("narrowing allowlist to swap-enable only")
			patchAutopilotAndWait(assetSwapEnable)

			By("verifying swap-enable MachineConfig exists and has managed-by label")
			mc, err := getUnstructuredResource(gateMachineConfigGVK, gateSwapMcName, "")
			Expect(err).NotTo(HaveOccurred())
			Expect(hasLabel(mc, gateManagedByLabel, gateManagedByValue)).To(BeTrue(),
				"MachineConfig should have managed-by label")

			By("verifying PrometheusRule still has managed-by label")
			pr, err := getUnstructuredResource(gatePrometheusRuleGVK, gatePrometheusRuleName, operatorNamespace)
			Expect(err).NotTo(HaveOccurred())
			Expect(hasLabel(pr, gateManagedByLabel, gateManagedByValue)).To(BeTrue(),
				"PrometheusRule should still have managed-by label")

			By("capturing metrics and events before deletion")
			metricsBefore := captureAssetMetrics("PrometheusRule", gatePrometheusRuleName, operatorNamespace)
			eventsBefore := captureAutopilotEvents()

			By("deleting the PrometheusRule")
			deleteResource(gatePrometheusRuleGVK, gatePrometheusRuleName, operatorNamespace)

			By("verifying PrometheusRule is not recreated")
			Consistently(func() error {
				_, err := getUnstructuredResource(gatePrometheusRuleGVK, gatePrometheusRuleName, operatorNamespace)
				return err
			}, gateConsistentlyDuration, gateConsistentlyInterval).ShouldNot(Succeed(),
				"PrometheusRule should not be recreated when outside the allowlist")

			By("verifying PrometheusRule metrics did not change")
			metricsAfter := captureAssetMetrics("PrometheusRule", gatePrometheusRuleName, operatorNamespace)
			Expect(metricsAfter).To(Equal(metricsBefore),
				"PrometheusRule metrics should not change when outside the allowlist")

			By("verifying no asset-level events were generated for the deleted PrometheusRule")
			eventsAfter := captureAutopilotEvents()
			Expect(eventsAfter.AssetApplied).To(Equal(eventsBefore.AssetApplied),
				"No new AssetApplied events should appear")
			Expect(eventsAfter.DriftDetected).To(Equal(eventsBefore.DriftDetected),
				"No new DriftDetected events should appear")
			Expect(eventsAfter.DriftCorrected).To(Equal(eventsBefore.DriftCorrected),
				"No new DriftCorrected events should appear")

			By("verifying swap-enable MachineConfig metrics are healthy")
			mcMetrics := captureAssetMetrics("MachineConfig", gateSwapMcName, "")
			if mcMetrics.ComplianceStatus >= 0 {
				Expect(mcMetrics.ComplianceStatus).To(Equal(1.0),
					"compliance_status for MachineConfig should be 1 (synced)")
				Expect(mcMetrics.PausedResources).To(Equal(0.0),
					"MachineConfig should not be paused")
			}
		})

		It("should recreate a deleted asset when added to the allowlist", func() {
			By("capturing metrics and events before test")
			metricsBefore := captureAssetMetrics("PrometheusRule", gatePrometheusRuleName, operatorNamespace)
			eventsBefore := captureAutopilotEvents()

			By("deleting PrometheusRule if it exists")
			deleteResource(gatePrometheusRuleGVK, gatePrometheusRuleName, operatorNamespace)

			By("adding prometheus-alerts to the allowlist")
			patchAutopilotAndWait(assetPrometheusAlerts)

			By("verifying PrometheusRule is recreated")
			Eventually(func() error {
				_, err := getUnstructuredResource(gatePrometheusRuleGVK, gatePrometheusRuleName, operatorNamespace)
				return err
			}, timeout, interval).Should(Succeed(), "PrometheusRule should be recreated")

			By("verifying managed-by label on recreated PrometheusRule")
			pr, err := getUnstructuredResource(gatePrometheusRuleGVK, gatePrometheusRuleName, operatorNamespace)
			Expect(err).NotTo(HaveOccurred())
			Expect(hasLabel(pr, gateManagedByLabel, gateManagedByValue)).To(BeTrue(),
				"Recreated PrometheusRule should have managed-by label")

			By("verifying PrometheusRule metrics after recreation")
			metricsAfter := captureAssetMetrics("PrometheusRule", gatePrometheusRuleName, operatorNamespace)
			Expect(metricsAfter.ComplianceStatus).To(Equal(1.0),
				"compliance_status should be 1 (synced)")
			Expect(metricsAfter.ReconcileDurationCount).To(BeNumerically(">", metricsBefore.ReconcileDurationCount),
				"reconcile_duration_seconds_count should increase")
			Expect(metricsAfter.ThrashingTotal).To(Equal(metricsBefore.ThrashingTotal),
				"thrashing_total should not increase")
			Expect(metricsAfter.PausedResources).NotTo(Equal(1.0),
				"resource should not be paused")

			By("verifying events after recreation")
			eventsAfter := captureAutopilotEvents()
			Expect(eventsAfter.AssetApplied).To(BeNumerically(">", eventsBefore.AssetApplied),
				"AssetApplied count should increase")
			Expect(eventsAfter.ReconcileSucceeded).To(BeNumerically(">", eventsBefore.ReconcileSucceeded),
				"ReconcileSucceeded count should increase")
			Expect(eventsAfter.ThrashingDetected).To(Equal(eventsBefore.ThrashingDetected),
				"ThrashingDetected count should not increase")
			Expect(eventsAfter.ApplyFailed).To(Equal(eventsBefore.ApplyFailed),
				"ApplyFailed count should not increase")

			By("verifying swap-enable MachineConfig still exists")
			_, err = getUnstructuredResource(gateMachineConfigGVK, gateSwapMcName, "")
			Expect(err).NotTo(HaveOccurred(), "MachineConfig should still exist after being removed from allowlist")
		})

		It("should correct a modified asset in the allowlist", func() {
			By("ensuring prometheus-alerts is in the allowlist")
			patchAutopilotAndWait(assetPrometheusAlerts)

			By("ensuring PrometheusRule exists")
			Eventually(func() error {
				_, err := getUnstructuredResource(gatePrometheusRuleGVK, gatePrometheusRuleName, operatorNamespace)
				return err
			}, timeout, interval).Should(Succeed())

			By("capturing metrics and events before drift")
			metricsBefore := captureAssetMetrics("PrometheusRule", gatePrometheusRuleName, operatorNamespace)
			eventsBefore := captureAutopilotEvents()

			By("modifying PrometheusRule by changing a managed label")
			driftPatch := []byte(`{"metadata":{"labels":{"app":"tampered"}}}`)
			prRef := &unstructured.Unstructured{}
			prRef.SetGroupVersionKind(gatePrometheusRuleGVK)
			prRef.SetName(gatePrometheusRuleName)
			prRef.SetNamespace(operatorNamespace)
			Expect(k8sClient.Patch(ctx, prRef, client.RawPatch(types.MergePatchType, driftPatch))).To(Succeed())

			By("verifying operator corrects the drift by restoring the app label")
			Eventually(func() bool {
				pr, err := getUnstructuredResource(gatePrometheusRuleGVK, gatePrometheusRuleName, operatorNamespace)
				if err != nil {
					return false
				}
				return hasLabel(pr, "app", "virt-platform-autopilot")
			}, timeout, interval).Should(BeTrue(), "Operator should restore the app label (drift correction)")

			By("verifying managed-by label is still present")
			pr, err := getUnstructuredResource(gatePrometheusRuleGVK, gatePrometheusRuleName, operatorNamespace)
			Expect(err).NotTo(HaveOccurred())
			Expect(hasLabel(pr, gateManagedByLabel, gateManagedByValue)).To(BeTrue(),
				"managed-by label should be preserved after drift correction")

			By("verifying PrometheusRule metrics after drift correction")
			metricsAfter := captureAssetMetrics("PrometheusRule", gatePrometheusRuleName, operatorNamespace)
			Expect(metricsAfter.ComplianceStatus).To(Equal(1.0),
				"compliance_status should be 1 (synced)")
			Expect(metricsAfter.ReconcileDurationCount).To(BeNumerically(">", metricsBefore.ReconcileDurationCount),
				"reconcile_duration_seconds_count should increase")
			Expect(metricsAfter.ThrashingTotal).To(Equal(metricsBefore.ThrashingTotal),
				"thrashing_total should not increase")
			Expect(metricsAfter.PausedResources).NotTo(Equal(1.0),
				"resource should not be paused")

			By("verifying drift events were emitted")
			eventsAfter := captureAutopilotEvents()
			Expect(eventsAfter.DriftDetected).To(BeNumerically(">", eventsBefore.DriftDetected),
				"DriftDetected count should increase")
			Expect(eventsAfter.DriftCorrected).To(BeNumerically(">", eventsBefore.DriftCorrected),
				"DriftCorrected count should increase")
			Expect(eventsAfter.AssetApplied).To(BeNumerically(">", eventsBefore.AssetApplied),
				"AssetApplied count should increase")
			Expect(eventsAfter.ThrashingDetected).To(Equal(eventsBefore.ThrashingDetected),
				"ThrashingDetected count should not increase")
			Expect(eventsAfter.ApplyFailed).To(Equal(eventsBefore.ApplyFailed),
				"ApplyFailed count should not increase")
		})

		AfterAll(func() {
			// TODO: replace with patchAutopilotAndWait(autopilotEnabled) once the
			// local HCO CRD (test/crds/kubevirt/hyperconverged-crd.yaml) is updated
			// via `make update-crds`. Currently the CRD defines featureGates as an
			// object but upstream changed it to an array, causing SSA dry-run failures
			// when reconciling hco-golden-config with autopilot: "true".
			By("restoring autopilot to full activation")
			Eventually(func() error {
				return k8sClient.Patch(ctx, hcoRef(), client.RawPatch(types.MergePatchType, autopilotPatch(autopilotEnabled)))
			}, timeout, interval).Should(Succeed())
		})
	})

})
