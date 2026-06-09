package e2e

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// assetsUnderTest lists all phase-1 "install: always" assets from metadata.yaml.
// Each generates a VirtPlatformSyncFailed sub-test. Assets whose CRD or gateCRD are not
// installed on the cluster are skipped automatically.
var assetsUnderTest = []assetUnderTest{
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
		GVK:  schema.GroupVersionKind{Group: "machineconfiguration.openshift.io", Version: "v1", Kind: "KubeletConfig"},
		Name: "virt-perf-settings",
	},
	{
		GVK:       schema.GroupVersionKind{Group: "remediation.medik8s.io", Version: "v1alpha1", Kind: "NodeHealthCheck"},
		Name:      "virt-node-health-check",
		Namespace: "openshift-operators",
		GateCRD:   "nodehealthchecks.remediation.medik8s.io",
	},
	{
		GVK:       schema.GroupVersionKind{Group: "observability.openshift.io", Version: "v1alpha1", Kind: "UIPlugin"},
		Name:      "monitoring",
		Namespace: "",
		GateCRD:   "uiplugins.observability.openshift.io",
	},
	{
		GVK:       schema.GroupVersionKind{Group: "operator.openshift.io", Version: "v1", Kind: "KubeDescheduler"},
		Name:      "cluster",
		Namespace: "openshift-kube-descheduler-operator",
		GateCRD:   "kubedeschedulers.operator.openshift.io",
	},
}

var _ = Describe("Prometheus Alert Tests", Ordered, ContinueOnFailure, func() {

	BeforeAll(func() {
		if !isOpenShiftCluster() {
			Skip("Alert tests only run on OCP — Kind has no Prometheus")
		}

		By("ensuring HCO exists with autopilot enabled")
		ensureHCOExists()
		patchAutopilotAndWait(autopilotEnabled)

		By("setting PrometheusRule to unmanaged so operator won't revert our changes")
		setPrometheusRuleUnmanaged()

		By("reducing alert 'for' durations to 15s for faster test feedback")
		patchAlertForDurations("15s")

		By("touching HCO to trigger reconciliation and ensure metrics are emitted")
		touchHCO()

		// CNV-89454: ServiceMonitor and metrics Service may be missing from the
		// cluster if the midstream build doesn't include them yet.
		By("waiting for Prometheus to scrape autopilot metrics (CNV-89454)")
		metricsAttempt := 0
		metricsMaxAttempts := int((10 * time.Minute) / (15 * time.Second))
		Eventually(func() bool {
			metricsAttempt++
			return queryMetricExists("kubevirt_autopilot_compliance_status", metricsAttempt, metricsMaxAttempts)
		}, 10*time.Minute, 15*time.Second).Should(BeTrue(),
			"Prometheus should be able to scrape autopilot metrics — ensure ServiceMonitor and metrics Service exist (CNV-89454)")
	})

	AfterAll(func() {
		if !isOpenShiftCluster() {
			return
		}

		By("restoring PrometheusRule to managed mode")
		removePrometheusRuleUnmanaged()

		waitForOperatorHealthy()
	})

	// --- Test 1: VirtPlatformSyncFailed (table-driven) ---

	for _, asset := range assetsUnderTest {
		asset := asset
		Context(fmt.Sprintf("VirtPlatformSyncFailed for %s/%s", asset.GVK.Kind, asset.Name), func() {
			webhookCreated := false

			AfterEach(func() {
				if !webhookCreated {
					return
				}

				deleteBlockingWebhook(asset)

				By(fmt.Sprintf("waiting for autopilot to recreate %s/%s", asset.GVK.Kind, asset.Name))
				Eventually(func() error {
					_, err := getUnstructuredResource(asset.GVK, asset.Name, asset.Namespace)
					return err
				}, 3*time.Minute, 5*time.Second).Should(Succeed(),
					fmt.Sprintf("%s/%s should be recreated by autopilot after webhook removal", asset.GVK.Kind, asset.Name))
			})

			It("should fire critical alert when asset drift cannot be restored", func() {
				if asset.GateCRD != "" && !crdInstalled(asset.GateCRD) {
					Skip(fmt.Sprintf("gate CRD %s not installed", asset.GateCRD))
				}

				By("verifying the resource exists before blocking")
				Eventually(func() error {
					_, err := getUnstructuredResource(asset.GVK, asset.Name, asset.Namespace)
					return err
				}, timeout, interval).Should(Succeed(),
					fmt.Sprintf("%s/%s should exist", asset.GVK.Kind, asset.Name))

				By("creating a webhook that blocks SSA for this resource")
				createBlockingWebhook(asset)
				webhookCreated = true

				// CNV-89450: the webhook also blocks the SSA dry-run used for drift detection,
				// so compliance_status never reaches 0. Deleting the resource forces the autopilot
				// through the Apply() path (liveExists=false → hasDrift=true, no dry-run).
				// Remove this step once CNV-89450 is fixed.
				By("deleting the resource to bypass dry-run drift detection (CNV-89450)")
				deleteResource(asset.GVK, asset.Name, asset.Namespace)

				By("touching HCO to trigger immediate reconciliation")
				touchHCO()

				By("waiting for VirtPlatformSyncFailed alert to fire (compliance_status=0 for 15s)")
				syncAttempt := 0
				syncMaxAttempts := int((3 * time.Minute) / (10 * time.Second))
				var alertLabels map[string]string
				Eventually(func() bool {
					syncAttempt++
					alertLabels = queryFiringAlert("VirtPlatformSyncFailed", syncAttempt, syncMaxAttempts,
						"kind", asset.GVK.Kind, "name", asset.Name)
					return alertLabels != nil
				}, 3*time.Minute, 10*time.Second).Should(BeTrue(),
					"VirtPlatformSyncFailed alert should fire when drift cannot be restored")

				Expect(alertLabels).To(HaveKeyWithValue("kind", asset.GVK.Kind))
				Expect(alertLabels).To(HaveKeyWithValue("name", asset.Name))
				Expect(alertLabels).To(HaveKeyWithValue("severity", "critical"))
				Expect(alertLabels).To(HaveKeyWithValue("operator", "virt-platform-autopilot"))
			})
		})
	}

	// --- Test 2: VirtPlatformDependencyMissing (passive) ---

	Context("VirtPlatformDependencyMissing", func() {
		It("should fire warning alert when an optional CRD is absent", func() {
			By("checking if any missing_dependency metric is already 1")
			missingDeps := getMissingDependenciesFromMetrics()
			if len(missingDeps) == 0 {
				Skip("No missing dependencies found — all optional CRDs are installed on this cluster")
			}
			GinkgoWriter.Printf("missing dependencies: %v\n", missingDeps)

			for _, dep := range missingDeps {
				By(fmt.Sprintf("waiting for VirtPlatformDependencyMissing alert for %s.%s", dep.Kind, dep.Group))
				depAttempt := 0
				depMaxAttempts := int(time.Minute / (10 * time.Second))
				var alertLabels map[string]string
				Eventually(func() bool {
					depAttempt++
					alertLabels = queryFiringAlert("VirtPlatformDependencyMissing", depAttempt, depMaxAttempts,
						"kind", dep.Kind, "group", dep.Group)
					return alertLabels != nil
				}, time.Minute, 10*time.Second).Should(BeTrue(),
					fmt.Sprintf("VirtPlatformDependencyMissing alert should fire for %s.%s", dep.Kind, dep.Group))

				Expect(alertLabels).To(HaveKeyWithValue("severity", "warning"))
				Expect(alertLabels).To(HaveKeyWithValue("operator", "virt-platform-autopilot"))
			}
		})
	})
})
