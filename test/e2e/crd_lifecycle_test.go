package e2e

import (
	"time"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// lifecycleMachineConfigCRDName is the fully-qualified CRD name for MachineConfig used in lifecycle tests.
const (
	lifecycleMachineConfigCRDName = "machineconfigs.machineconfiguration.openshift.io"
	lifecycleManagedByLabel       = "platform.kubevirt.io/managed-by"
	lifecycleManagedByValue       = "virt-platform-autopilot"
)

var _ = Describe("CRD Lifecycle Tests", Ordered, func() {

	// lifecycleMachineConfigCRD is a minimal CRD used to test operator restart behaviour.
	// MachineConfig is a managed CRD with an "always applied" asset (swap-enable),
	// ensuring a watch is established after installation.
	var (
		lifecycleMachineConfigCRD *apiextensionsv1.CustomResourceDefinition
		hco                       *unstructured.Unstructured
	)

	var (
		lifecycleMachineConfigGVK = schema.GroupVersionKind{
			Group:   "machineconfiguration.openshift.io",
			Version: "v1",
			Kind:    "MachineConfig",
		}
	)

	BeforeAll(func() {
		By("ensuring clean HCO instance for lifecycle tests")
		hco = &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "hco.kubevirt.io/v1beta1",
				"kind":       "HyperConverged",
				"metadata": map[string]interface{}{
					"name":      hcoName,
					"namespace": operatorNamespace,
					"labels": map[string]interface{}{
						lifecycleManagedByLabel: lifecycleManagedByValue,
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

		lifecycleMachineConfigCRD = buildMinimalCRD(
			"machineconfiguration.openshift.io",
			"MachineConfig",
			"machineconfigs",
			"v1",
			apiextensionsv1.ClusterScoped,
		)
	})

	It("should restart when managed CRD is created and create the swap-enable resource", func() {
		prevCount := getManagerRestartCount()
		installCRD(lifecycleMachineConfigCRD)
		waitForOperatorRestart(prevCount)

		Eventually(func() error {
			_, err := getUnstructuredResource(lifecycleMachineConfigGVK, "90-worker-swap-online", "")
			return err
		}, timeout, interval).Should(Succeed(),
			"Operator should create the 90-worker-swap-online MachineConfig after CRD installation")
	})

	It("should restart when managed CRD is deleted", func() {
		prevCount := getManagerRestartCount()
		removeCRD(lifecycleMachineConfigCRDName)
		waitForOperatorRestart(prevCount)
	})

	AfterAll(func() {
		// Clean up HCO instance
		By("cleaning up: removing HCO instance")
		_ = k8sClient.Delete(ctx, hco)

		// Ensure CRD is cleaned up in case of test failure
		removeCRD(lifecycleMachineConfigCRDName)

		// Give the autopilot time to stabilize after potential restart
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
