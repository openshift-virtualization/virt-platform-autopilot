package e2e

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// resilienceAsset is the fixed asset used for drift and pause resilience scenarios.
// Service is always present (no gate CRD), not Sensitive, and has an overridable field.
// activeAsset pairs a testAsset with its pre-restart metrics snapshot.
type activeAsset struct {
	asset   testAsset
	metrics AssetMetrics
}

var resilienceAsset = testAsset{
	GVK:       schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Service"},
	Plural:    "services",
	Name:      "virt-platform-autopilot-metrics",
	Namespace: operatorNamespace,
	Override: UserOverrideFieldSpec{
		JSONPointer: "/metadata/labels/app",
		Values:      [2]string{"e2e-tampered", "e2e-modified"},
	},
}

// resilience_e2e_test.go verifies that the operator recovers correctly after an
// unexpected pod restart. Tests cover three failure surfaces:
//
//  1. Metric repopulation — compliance_status and paused_resources gauges reset on restart;
//     the no-drift early return in patcher.go must still call SetCompliance()/SetPaused() (CNV-92583).
//  2. Drift correction — drift introduced just before restart must be corrected after restart.
//  3. Pause annotation preserved — anti-thrashing pause annotation lives in etcd;
//     after in-memory state reset the operator must re-read and honour it.

var _ = Describe("Operator Resilience After Restart", Ordered, ContinueOnFailure, func() {

	BeforeAll(func() {
		By("ensuring HCO exists with autopilot fully enabled")
		ensureHCOExists()
		patchAutopilotAndWait(autopilotEnabled)
	})

	AfterAll(func() {
		removePauseAnnotation(resilienceAsset.GVK, resilienceAsset.Name, resilienceAsset.Namespace)
		waitForOperatorHealthy()
	})

	// --- Scenario 1: metrics repopulated after restart with no drift ---

	Context("after operator restart with all assets in sync", func() {

		It("should repopulate compliance_status and paused_resources", func() {
			var active []activeAsset
			By("collecting baseline metrics for all managed assets")
			for _, asset := range assetsUnderTest {
				if asset.GateCRD != "" && !crdInstalled(asset.GateCRD) {
					GinkgoWriter.Printf("resilience: skipping %s/%s — gate CRD %s not installed\n",
						asset.GVK.Kind, asset.Name, asset.GateCRD)
					continue
				}
				m := captureAssetMetrics(asset.GVK.Kind, asset.Name, asset.Namespace)
				active = append(active, activeAsset{asset: asset, metrics: m})
				GinkgoWriter.Printf("resilience: baseline %s/%s compliance=%.0f paused=%.0f\n",
					asset.GVK.Kind, asset.Name, m.ComplianceStatus, m.PausedResources)
			}
			Expect(active).NotTo(BeEmpty(),
				"At least one managed asset must have metrics before the restart")
			for _, a := range active {
				Expect(a.metrics.ComplianceStatus).NotTo(Equal(-1.0),
					fmt.Sprintf("%s/%s must have compliance_status before restart",
						a.asset.GVK.Kind, a.asset.Name))
			}

			postRestartTime := time.Now()
			restartOperatorPod()
			waitForReconcileSucceeded(postRestartTime)

			By("verifying post-restart metrics match pre-restart snapshot")
			type metricsEntry struct{ Compliance, Paused float64 }
			preSnapshot := map[string]metricsEntry{}
			for _, a := range active {
				key := fmt.Sprintf("%s/%s", a.asset.GVK.Kind, a.asset.Name)
				preSnapshot[key] = metricsEntry{a.metrics.ComplianceStatus, a.metrics.PausedResources}
			}
			Eventually(func() map[string]metricsEntry {
				post := map[string]metricsEntry{}
				for _, a := range active {
					key := fmt.Sprintf("%s/%s", a.asset.GVK.Kind, a.asset.Name)
					m := captureAssetMetrics(a.asset.GVK.Kind, a.asset.Name, a.asset.Namespace)
					post[key] = metricsEntry{m.ComplianceStatus, m.PausedResources}
				}
				return post
			}, timeout, interval).Should(Equal(preSnapshot),
				"post-restart compliance_status and paused_resources must match pre-restart snapshot")
		})

	})

	// --- Scenario 2: drift introduced just before restart ---

	It("should correct drift introduced just before operator restart", func() {

		postRestartTime := time.Now()
		driftPatch := resilienceAsset.Override.MergePatch(0)
		By(fmt.Sprintf("tampering %s/%s to introduce drift (field: %s → %s)",
			resilienceAsset.GVK.Kind, resilienceAsset.Name, resilienceAsset.Override.JSONPointer, resilienceAsset.Override.Values[0]))
		tamperField(resilienceAsset, driftPatch)

		By("restarting operator immediately after introducing drift")
		restartOperatorPod()
		waitForReconcileSucceeded(postRestartTime)

		By(fmt.Sprintf("verifying %s/%s drift was corrected during reconcile",
			resilienceAsset.GVK.Kind, resilienceAsset.Name))
		obj, err := getUnstructuredResource(resilienceAsset.GVK, resilienceAsset.Name, resilienceAsset.Namespace)
		Expect(err).NotTo(HaveOccurred())
		Expect(readOverrideFieldValue(obj, resilienceAsset)).NotTo(Equal(resilienceAsset.Override.Values[0]),
			fmt.Sprintf("Operator must revert the tampered field on %s/%s after restart",
				resilienceAsset.GVK.Kind, resilienceAsset.Name))

		By("verifying DriftCorrected event was emitted during reconcile")
		Expect(captureAutopilotEvents(postRestartTime).DriftCorrected).To(BeNumerically(">", 0),
			"DriftCorrected event must be emitted after restart when drift is present")

		By(fmt.Sprintf("verifying compliance_status=1 for %s/%s after correction",
			resilienceAsset.GVK.Kind, resilienceAsset.Name))
		Expect(captureAssetMetrics(resilienceAsset.GVK.Kind, resilienceAsset.Name, resilienceAsset.Namespace).ComplianceStatus).To(Equal(1.0),
			"compliance_status must be 1 after drift is corrected post-restart")
	})

	// --- Scenario 3: anti-thrashing pause annotation preserved after in-memory reset ---

	It("should honour the pause annotation after in-memory thrashing state is reset by restart", func() {
		By(fmt.Sprintf("setting pause annotation directly on %s/%s",
			resilienceAsset.GVK.Kind, resilienceAsset.Name))
		setAnnotation(resilienceAsset.GVK, resilienceAsset.Name, resilienceAsset.Namespace,
			pauseAnnotation, "true")

		reconcileTime := time.Now()
		restartOperatorPod()
		waitForReconcileSucceeded(reconcileTime)

		By(fmt.Sprintf("verifying pause annotation still present on %s/%s after restart",
			resilienceAsset.GVK.Kind, resilienceAsset.Name))
		obj, err := getUnstructuredResource(resilienceAsset.GVK, resilienceAsset.Name, resilienceAsset.Namespace)
		Expect(err).NotTo(HaveOccurred())
		Expect(obj.GetAnnotations()).To(HaveKeyWithValue(pauseAnnotation, "true"),
			fmt.Sprintf("pause annotation must survive operator restart on %s/%s — it lives in etcd, not operator memory",
				resilienceAsset.GVK.Kind, resilienceAsset.Name))

		By(fmt.Sprintf("verifying paused_resources=1 repopulated for %s/%s after restart",
			resilienceAsset.GVK.Kind, resilienceAsset.Name))
		Eventually(func() float64 {
			return captureAssetMetrics(resilienceAsset.GVK.Kind, resilienceAsset.Name, resilienceAsset.Namespace).PausedResources
		}, timeout, interval).Should(Equal(1.0),
			fmt.Sprintf("paused_resources for %s/%s must be 1 after restart — operator must re-read pause annotation from cluster",
				resilienceAsset.GVK.Kind, resilienceAsset.Name))

		By(fmt.Sprintf("verifying operator does not reconcile %s/%s while paused",
			resilienceAsset.GVK.Kind, resilienceAsset.Name))
		tamperField(resilienceAsset, resilienceAsset.Override.MergePatch(0))
		touchHCO()
		Consistently(func() string {
			obj, err := getUnstructuredResource(resilienceAsset.GVK, resilienceAsset.Name, resilienceAsset.Namespace)
			if err != nil {
				return ""
			}
			return readOverrideFieldValue(obj, resilienceAsset)
		}, consistentlyDuration, consistentlyInterval).Should(Equal(resilienceAsset.Override.Values[0]),
			fmt.Sprintf("Operator must not correct drift on %s/%s while pause annotation is present",
				resilienceAsset.GVK.Kind, resilienceAsset.Name))
	})

})
