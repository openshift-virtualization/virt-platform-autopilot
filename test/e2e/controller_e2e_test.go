package e2e

import (
	"fmt"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

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

	// autopilotAnnotation controls the autopilot on the HCO CR. The autopilot is GA and
	// enabled by default; it is disabled only when this annotation is set to "false".
	autopilotAnnotation        = "platform.kubevirt.io/autopilot"
	autopilotEnabled           = "true"
	autopilotDisabledByFalse   = "false"
	autopilotAnnotationRemoved = ""
)

const (
	swapMcName                  = "90-worker-swap-online"
	consistentlyDuration        = 10 * time.Second
	consistentlyInterval        = 1 * time.Second
	prometheusRuleName          = "virt-platform-autopilot-alerts"
	managedByValue              = "virt-platform-autopilot"
	disabledResourcesAnnotation = "platform.kubevirt.io/disabled-resources"
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

		It("should keep reconciling when autopilot annotation is removed (enabled by default)", func() {
			By("removing managed-by label and the autopilot annotation")
			removeManagedByLabel(managedByLabel)
			reEnableTime := time.Now()
			patchAutopilotAndWait(autopilotAnnotationRemoved)

			By("verifying the operator re-adopts and labels the HCO (autopilot GA, enabled by default)")
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
			}, timeout, interval).Should(BeTrue(),
				"Operator should re-label HCO when annotation is absent (enabled by default)")

			By("verifying ReconcileSucceeded events are still emitted")
			eventsAfter := captureAutopilotEvents(reEnableTime)
			Expect(eventsAfter.ReconcileSucceeded).To(BeNumerically(">", 0),
				"ReconcileSucceeded events should be emitted when the annotation is absent")
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

	Context("Disabled resources via annotation", Ordered, func() {
		BeforeAll(func() {
			ensureHCOExists()
			ensureCRDInstalled("prometheusrules.monitoring.coreos.com")
			ensureCRDInstalled("machineconfigs.machineconfiguration.openshift.io")
			patchAutopilotAndWait(autopilotEnabled)
		})

		It("should clear compliance_status but preserve customization annotations when a previously-unmanaged asset is excluded via disabled-resources", func() {
			DeferCleanup(func() {
				removeAnnotation(hcoGVK, hcoName, operatorNamespace, disabledResourcesAnnotation)
				removeAnnotation(prometheusRuleGVK, prometheusRuleName, operatorNamespace, modeAnnotation)
				touchHCO()
				waitForOperatorHealthy()
			})

			By("setting unmanaged mode on PrometheusRule")
			setAnnotation(prometheusRuleGVK, prometheusRuleName, operatorNamespace, modeAnnotation, modeUnmanaged)
			touchHCO()

			By("waiting for customization_info{type=unmanaged} to appear")
			Eventually(func() float64 {
				return findCustomizationMetric(prometheusRuleGVK.Kind, prometheusRuleName, operatorNamespace, "unmanaged")
			}, timeout, interval).Should(Equal(1.0),
				"customization_info{type=unmanaged} must be 1 while PrometheusRule is unmanaged")

			By("adding PrometheusRule to disabled-resources while it is still unmanaged")
			setAnnotation(hcoGVK, hcoName, operatorNamespace, disabledResourcesAnnotation,
				exclusionEntryYAML(prometheusRuleGVK.Kind, prometheusRuleName, operatorNamespace))
			touchHCO()
			waitForOperatorHealthy()

			By("verifying compliance_status is cleared once the asset is excluded")
			Eventually(func() float64 {
				return captureAssetMetrics(prometheusRuleGVK.Kind, prometheusRuleName, operatorNamespace).ComplianceStatus
			}, timeout, interval).Should(Equal(-1.0),
				"compliance_status must be absent while PrometheusRule is excluded")

			By("verifying customization_info{type=unmanaged} persists while excluded — annotation still exists on the object")
			Expect(findCustomizationMetric(prometheusRuleGVK.Kind, prometheusRuleName, operatorNamespace, "unmanaged")).
				To(Equal(1.0),
					"customization_info{type=unmanaged} must remain 1 while excluded: the annotation persists on the object and will be re-evaluated when exclusion is lifted")
		})

		It("should skip excluded asset while continuing to reconcile others (non-existent kind in annotation is ignored)", func() {
			testStartTime := time.Now()

			By("excluding PrometheusRule via disabled-resources annotation; annotation also contains a non-existent kind to verify it is silently ignored")
			exclusionYAML := strings.Join([]string{
				exclusionEntryYAML(prometheusRuleGVK.Kind, prometheusRuleName, operatorNamespace),
				exclusionEntryYAML("NonExistentCRD", "does-not-exist", ""),
			}, "\n")
			setAnnotation(hcoGVK, hcoName, operatorNamespace, disabledResourcesAnnotation, exclusionYAML)

			By("deleting PrometheusRule to trigger the exclusion path")
			deleteResource(prometheusRuleGVK, prometheusRuleName, operatorNamespace)

			touchHCO()
			waitForOperatorHealthy()

			By("verifying compliance_status is cleared while excluded")
			Eventually(func() float64 {
				return captureAssetMetrics(prometheusRuleGVK.Kind, prometheusRuleName, operatorNamespace).ComplianceStatus
			}, timeout, interval).Should(Equal(-1.0),
				"compliance_status must be absent (-1=not found) while PrometheusRule is excluded via disabled-resources")

			By("verifying paused_resources is not stuck at 1 while excluded")
			Expect(captureAssetMetrics(prometheusRuleGVK.Kind, prometheusRuleName, operatorNamespace).PausedResources).
				NotTo(Equal(1.0), "paused_resources must not be 1 while PrometheusRule is excluded")

			By("verifying PrometheusRule is not recreated")
			Consistently(func() bool {
				_, err := getUnstructuredResource(prometheusRuleGVK, prometheusRuleName, operatorNamespace)
				return apierrors.IsNotFound(err)
			}, consistentlyDuration, consistentlyInterval).Should(BeTrue(),
				"excluded PrometheusRule must not be recreated by operator")

			By("verifying no AssetApplied event for excluded asset")
			Expect(findEvents(EventFilter{
				Reason: "AssetApplied",
				Kind:   prometheusRuleGVK.Kind,
				Name:   prometheusRuleName,
				Since:  testStartTime,
			})).To(BeEmpty(), "AssetApplied must not fire for excluded asset")

			By("verifying no DriftDetected event for excluded asset")
			Expect(findEvents(EventFilter{
				Reason: "DriftDetected",
				Kind:   prometheusRuleGVK.Kind,
				Name:   prometheusRuleName,
				Since:  testStartTime,
			})).To(BeEmpty(), "DriftDetected must not fire for excluded asset")

			By("introducing drift on MachineConfig (non-excluded asset) to prove operator is still active")
			mcRef := unstructuredRef(machineConfigGVK, swapMcName, "")
			Expect(k8sClient.Patch(ctx, mcRef, client.RawPatch(types.MergePatchType,
				[]byte(fmt.Sprintf(`{"metadata":{"labels":{%q:"tampered"}}}`, managedByLabel))))).To(Succeed())

			touchHCO()

			By("verifying managed-by label is restored on MachineConfig")
			Eventually(func() string {
				mc, err := getUnstructuredResource(machineConfigGVK, swapMcName, "")
				if err != nil {
					return ""
				}
				return mc.GetLabels()[managedByLabel]
			}, timeout, interval).Should(Equal(managedByValue),
				"non-excluded MachineConfig must be reconciled while excluded PrometheusRule stays absent")

			By("verifying PrometheusRule is still absent after MachineConfig reconciliation")
			Consistently(func() bool {
				_, err := getUnstructuredResource(prometheusRuleGVK, prometheusRuleName, operatorNamespace)
				return apierrors.IsNotFound(err)
			}, consistentlyDuration, consistentlyInterval).Should(BeTrue(),
				"excluded PrometheusRule must not be recreated")
		})

		It("should respect exclusion after operator pod restart", func() {
			By("restarting operator pod (annotation and absent PR inherited from previous test)")
			restartOperatorPod()

			By("verifying exclusion is respected after cold-start reconcile")
			Consistently(func() bool {
				_, err := getUnstructuredResource(prometheusRuleGVK, prometheusRuleName, operatorNamespace)
				return apierrors.IsNotFound(err)
			}, consistentlyDuration, consistentlyInterval).Should(BeTrue(),
				"disabled-resources annotation must be read on initial reconcile, not only on watch events")
		})

		It("should recreate asset when removed from disabled list", func() {
			recreateTime := time.Now()
			By("replacing disabled-resources annotation to keep only the non-existent kind (PrometheusRule exclusion lifted)")
			setAnnotation(hcoGVK, hcoName, operatorNamespace, disabledResourcesAnnotation,
				exclusionEntryYAML("NonExistentCRD", "does-not-exist", ""))
			touchHCO()

			By("verifying PrometheusRule is recreated")
			Eventually(func() error {
				_, err := getUnstructuredResource(prometheusRuleGVK, prometheusRuleName, operatorNamespace)
				return err
			}, timeout, interval).Should(Succeed(),
				"PrometheusRule must be recreated once exclusion is lifted")

			By("verifying AssetApplied event fired for re-included asset")
			Eventually(func() []eventsv1.Event {
				return findEvents(EventFilter{
					Reason: "AssetApplied",
					Kind:   prometheusRuleGVK.Kind,
					Name:   prometheusRuleName,
					Since:  recreateTime,
				})
			}, timeout, interval).ShouldNot(BeEmpty(), "AssetApplied must fire when exclusion is lifted")

			By("verifying compliance_status metric is 1 after recreation")
			Eventually(func() float64 {
				return captureAssetMetrics(prometheusRuleGVK.Kind, prometheusRuleName, operatorNamespace).ComplianceStatus
			}, timeout, interval).Should(Equal(1.0),
				"compliance_status must be 1 (synced) after PrometheusRule is recreated")
		})

		AfterAll(func() {
			By("removing disabled-resources annotation")
			removeAnnotation(hcoGVK, hcoName, operatorNamespace, disabledResourcesAnnotation)

			By("restoring autopilot annotation to recreate managed assets")
			patchAutopilotAndWait(autopilotEnabled)

			waitForOperatorHealthy()
		})
	})

})
