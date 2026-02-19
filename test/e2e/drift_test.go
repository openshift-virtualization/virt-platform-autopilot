package e2e

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	driftMachineConfigCRDName = "machineconfigs.machineconfiguration.openshift.io"

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

	var (
		machineConfigCRD *apiextensionsv1.CustomResourceDefinition
		hco              *unstructured.Unstructured
	)

	BeforeAll(func() {
		By("ensuring clean HCO instance for drift tests")
		hco = &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "hco.kubevirt.io/v1beta1",
				"kind":       "HyperConverged",
				"metadata": map[string]interface{}{
					"name":      hcoName,
					"namespace": operatorNamespace,
					"labels": map[string]interface{}{
						driftManagedByLabel: driftManagedByValue,
					},
				},
				"spec": map[string]interface{}{},
			},
		}

		// Delete any existing HCO from previous tests and wait for deletion
		_ = k8sClient.Delete(ctx, hco)
		Eventually(func() bool {
			err := k8sClient.Get(ctx, client.ObjectKey{
				Name:      hcoName,
				Namespace: operatorNamespace,
			}, hco)
			return err != nil // true when NotFound
		}, 30*time.Second, 500*time.Millisecond).Should(BeTrue(),
			"Previous HCO instance should be deleted before creating new one")

		// Create fresh HCO instance
		Expect(k8sClient.Create(ctx, hco)).To(Succeed())

		machineConfigCRD = buildMinimalCRD(
			"machineconfiguration.openshift.io",
			"MachineConfig",
			"machineconfigs",
			"v1",
			apiextensionsv1.ClusterScoped,
		)

		By("installing MachineConfig CRD and waiting for operator restart")
		prevCount := getManagerRestartCount()
		installCRD(machineConfigCRD)
		waitForOperatorRestart(prevCount)
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
		}, 30*time.Second, 2*time.Second).Should(BeNumerically(">=", 1),
			"At least one DriftCorrected event should exist for MachineConfig")
	})

	AfterAll(func() {
		By("cleaning up: removing HCO instance")
		_ = k8sClient.Delete(ctx, hco)

		By("cleaning up: removing MachineConfig CRD")
		removeCRD(driftMachineConfigCRDName)

		By("waiting for operator to stabilize after cleanup")
		Eventually(func() bool {
			pod := getOperatorPod()
			for _, cs := range pod.Status.ContainerStatuses {
				if cs.Name == "manager" || len(pod.Status.ContainerStatuses) == 1 {
					return cs.Ready
				}
			}
			return false
		}, 3*time.Minute, 2*time.Second).Should(BeTrue())
	})
})

// setNestedField sets a value in an unstructured object at the given field path.
func setNestedField(obj *unstructured.Unstructured, value interface{}, fields ...string) error {
	return unstructured.SetNestedField(obj.Object, value, fields...)
}

// getNestedString reads a string value from an unstructured object at the given field path.
func getNestedString(obj *unstructured.Unstructured, fields ...string) (string, bool, error) {
	return unstructured.NestedString(obj.Object, fields...)
}
