package e2e

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const (
	// Expected resource name created by operator asset
	driftMcName = "90-worker-swap-online"

	// Expected spec field value from the autopilot's asset
	driftExpectedIgnitionVersion = "3.5.0"

	// Managed-by label
	driftManagedByLabel = "platform.kubevirt.io/managed-by"
	driftManagedByValue = "virt-platform-autopilot"
)

var (
	driftMachineConfigGVK = schema.GroupVersionKind{
		Group:   "machineconfiguration.openshift.io",
		Version: "v1",
		Kind:    "MachineConfig",
	}
)

var _ = Describe("Drift Detection Tests", Ordered, func() {

	BeforeAll(func() {
		By("ensuring HCO instance exists")
		ensureHCOExists()
		patchAutopilotAndWait(autopilotEnabled)

		By("ensuring MachineConfig CRD is installed")
		prevCount := getManagerRestartCount()
		if ensureCRDInstalled(newMachineConfigCRD()) {
			waitForOperatorRestart(prevCount)
		}
		waitForOperatorHealthy()
	})

	It("should create the 90-worker-swap-online MachineConfig with managed-by label", func() {
		Eventually(func() error {
			_, err := getUnstructuredResource(driftMachineConfigGVK, driftMcName, "")
			return err
		}, timeout, interval).Should(Succeed(),
			"Operator should create the 90-worker-swap-online MachineConfig")

		mc, err := getUnstructuredResource(driftMachineConfigGVK, driftMcName, "")
		Expect(err).NotTo(HaveOccurred())
		labels := mc.GetLabels()
		Expect(labels).To(HaveKeyWithValue(driftManagedByLabel, driftManagedByValue),
			"MachineConfig should have managed-by label")
	})

	It("should correct drift on MachineConfig spec and emit DriftCorrected event", func() {
		By("modifying ignition.version to simulate drift")
		mc, err := getUnstructuredResource(driftMachineConfigGVK, driftMcName, "")
		Expect(err).NotTo(HaveOccurred())

		// Modify spec.config.ignition.version
		Expect(setNestedField(mc, "2.0.0", "spec", "config", "ignition", "version")).To(Succeed())
		Expect(k8sClient.Update(ctx, mc)).To(Succeed())

		By("verifying operator corrects the drift back to expected version")
		Eventually(func() string {
			obj, err := getUnstructuredResource(driftMachineConfigGVK, driftMcName, "")
			if err != nil {
				return ""
			}
			val, _, _ := getNestedString(obj, "spec", "config", "ignition", "version")
			return val
		}, timeout, interval).Should(Equal(driftExpectedIgnitionVersion),
			"Operator should restore ignition.version to expected version")

		By("checking for DriftCorrected event")
		Eventually(func() int {
			return len(findDriftCorrectedEvents("MachineConfig", driftMcName))
		}, timeout, interval).Should(BeNumerically(">=", 1),
			"At least one DriftCorrected event should exist for MachineConfig")
	})

	AfterAll(func() {
		if !isOpenShiftCluster() {
			removeCRD(newMachineConfigCRD().Name)
		}
		waitForOperatorHealthy()
	})
})

// setNestedField sets a value in an unstructured object at the given field path.
func setNestedField(obj *unstructured.Unstructured, value any, fields ...string) error {
	return unstructured.SetNestedField(obj.Object, value, fields...)
}

// getNestedString reads a string value from an unstructured object at the given field path.
func getNestedString(obj *unstructured.Unstructured, fields ...string) (string, bool, error) {
	return unstructured.NestedString(obj.Object, fields...)
}
