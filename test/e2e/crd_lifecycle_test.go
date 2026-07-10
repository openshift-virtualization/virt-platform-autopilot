package e2e

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const (
	machineConfigCRDName = "machineconfigs.machineconfiguration.openshift.io"
	machineConfigCRDFile = "test/crds/openshift/machineconfig-crd.yaml"
)

var machineConfigDepLabels = map[string]string{
	"group": "machineconfiguration.openshift.io",
	"kind":  "Machineconfig",
}

var _ = Describe("CRD Lifecycle Tests", Ordered, func() {

	BeforeAll(func() {
		if isOpenShiftCluster() {
			Skip("CRD lifecycle tests only run on Kind — on OCP, CRDs are managed by operators")
		}

		ensureHCOExists()
		patchAutopilotAndWait(autopilotEnabled)

		By("removing MachineConfig CRD so we can test creation lifecycle")
		prevCount := getManagerRestartCount()
		removeCRD(machineConfigCRDName)
		waitForOperatorRestart(prevCount)
		waitForOperatorHealthy()
	})

	It("should restart when managed CRD is created and create the swap-enable resource", func() {
		By("verifying CRDMissing event was emitted after CRD removal")
		Eventually(func() int {
			return captureAutopilotEvents().CRDMissing
		}, timeout, interval).Should(BeNumerically(">", 0),
			"CRDMissing event should be emitted when CRD is absent")

		By("verifying missing_dependency metric is 1 while CRD is absent")
		Eventually(func() float64 {
			return findMetricValue("kubevirt_autopilot_missing_dependency", machineConfigDepLabels)
		}, timeout, interval).Should(Equal(1.0),
			"missing_dependency metric should be 1 when CRD is missing")

		prevCount := getManagerRestartCount()
		installCRDFromFile(machineConfigCRDFile)
		waitForCRDEstablished(machineConfigCRDName)
		waitForOperatorRestart(prevCount)
		waitForOperatorHealthy()

		Eventually(func() error {
			_, err := getUnstructuredResource(machineConfigGVK, "90-worker-swap-online", "")
			return err
		}, timeout, interval).Should(Succeed(),
			"Operator should create the 90-worker-swap-online MachineConfig after CRD installation")

		By("verifying missing_dependency metric is 0 after CRD is installed")
		Eventually(func() float64 {
			return findMetricValue("kubevirt_autopilot_missing_dependency", machineConfigDepLabels)
		}, timeout, interval).Should(Equal(0.0),
			"missing_dependency metric should be 0 when CRD is present")
	})

	It("should restart when managed CRD is deleted", func() {
		crdMissingBefore := captureAutopilotEvents().CRDMissing
		deleteTime := time.Now()

		prevCount := getManagerRestartCount()
		removeCRD(machineConfigCRDName)
		waitForOperatorRestart(prevCount)

		By("verifying CRDMissing event was emitted after CRD deletion")
		Eventually(func() int {
			return captureAutopilotEvents(deleteTime).CRDMissing
		}, timeout, interval).Should(BeNumerically(">", 0),
			"CRDMissing event should be emitted after CRD deletion")
		Expect(captureAutopilotEvents().CRDMissing).To(BeNumerically(">", crdMissingBefore),
			"Total CRDMissing event count should increase")

		By("verifying missing_dependency metric is 1 after CRD deletion")
		Eventually(func() float64 {
			return findMetricValue("kubevirt_autopilot_missing_dependency", machineConfigDepLabels)
		}, timeout, interval).Should(Equal(1.0),
			"missing_dependency metric should be 1 when CRD is deleted")
	})

	AfterAll(func() {
		if isOpenShiftCluster() {
			return
		}

		// Restore the full CRD from test/crds/ so other tests still find it.
		// Installing a managed CRD triggers a graceful operator restart to
		// reconfigure watches, so we must wait for that restart before
		// checking health.
		if !crdInstalled(machineConfigCRDName) {
			prevCount := getManagerRestartCount()
			installCRDFromFile(machineConfigCRDFile)
			waitForCRDEstablished(machineConfigCRDName)
			waitForOperatorRestart(prevCount)
		} else {
			waitForOperatorHealthy()
		}
	})
})
