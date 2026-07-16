package e2e

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const (
	machineConfigCRDName = "machineconfigs.machineconfiguration.openshift.io"
	machineConfigCRDFile = "test/crds/openshift/machineconfig-crd.yaml"
)

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
	})

	It("should restart when managed CRD is deleted", func() {
		prevCount := getManagerRestartCount()
		removeCRD(machineConfigCRDName)
		waitForOperatorRestart(prevCount)
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
