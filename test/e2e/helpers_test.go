package e2e

import (
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	eventsv1 "k8s.io/api/events/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// buildMinimalCRD constructs a minimal CRD with x-kubernetes-preserve-unknown-fields
// suitable for testing without requiring a full schema.
func buildMinimalCRD(group, kind, plural, version string, scope apiextensionsv1.ResourceScope) *apiextensionsv1.CustomResourceDefinition {
	return &apiextensionsv1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("%s.%s", plural, group),
		},
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: group,
			Names: apiextensionsv1.CustomResourceDefinitionNames{
				Kind:     kind,
				Plural:   plural,
				Singular: strings.ToLower(kind),
			},
			Scope: scope,
			Versions: []apiextensionsv1.CustomResourceDefinitionVersion{
				{
					Name:    version,
					Served:  true,
					Storage: true,
					Schema: &apiextensionsv1.CustomResourceValidation{
						OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
							Type:                   "object",
							XPreserveUnknownFields: boolPtr(true),
						},
					},
				},
			},
		},
	}
}

// boolPtr returns a pointer to the given bool value.
func boolPtr(b bool) *bool {
	return &b
}

// getOperatorPod returns the operator pod by app label.
func getOperatorPod() *corev1.Pod {
	podList := &corev1.PodList{}
	ExpectWithOffset(1, k8sClient.List(ctx, podList,
		client.InNamespace(operatorNamespace),
		client.MatchingLabels{"app": operatorAppLabel},
	)).To(Succeed())
	ExpectWithOffset(1, podList.Items).NotTo(BeEmpty(), "Operator pod should exist")
	return &podList.Items[0]
}

// getManagerRestartCount returns the restart count for the "manager" container in the operator pod.
func getManagerRestartCount() int32 {
	pod := getOperatorPod()
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.Name == "manager" {
			return cs.RestartCount
		}
	}
	// If there's only one container, use it regardless of name
	if len(pod.Status.ContainerStatuses) == 1 {
		return pod.Status.ContainerStatuses[0].RestartCount
	}
	Fail("manager container not found in operator pod")
	return -1
}

// waitForOperatorRestart polls until the operator container restart count
// exceeds prevCount, then waits for the pod to become Ready.
func waitForOperatorRestart(prevCount int32) {
	By(fmt.Sprintf("waiting for operator restart count to exceed %d", prevCount))
	Eventually(func() int32 {
		return getManagerRestartCount()
	}, 3*time.Minute, 2*time.Second).Should(BeNumerically(">", prevCount),
		"Operator container restart count should increase")

	waitForOperatorHealthy()
}

// waitForOperatorHealthy waits for the operator pod to be Running with container Ready.
func waitForOperatorHealthy() {
	By("waiting for operator pod to become healthy")
	Eventually(func() bool {
		pod := getOperatorPod()
		if pod.Status.Phase != corev1.PodRunning {
			return false
		}
		for _, cs := range pod.Status.ContainerStatuses {
			if cs.Name == "manager" || len(pod.Status.ContainerStatuses) == 1 {
				return cs.Ready
			}
		}
		return false
	}, 3*time.Minute, 2*time.Second).Should(BeTrue(), "Operator pod should be Running and Ready")
}

// installCRD creates a CRD and waits for it to reach the Established condition.
func installCRD(crd *apiextensionsv1.CustomResourceDefinition) {
	By(fmt.Sprintf("installing CRD %s", crd.Name))
	ExpectWithOffset(1, k8sClient.Create(ctx, crd)).To(Succeed())

	By(fmt.Sprintf("waiting for CRD %s to become Established", crd.Name))
	EventuallyWithOffset(1, func() bool {
		fetched := &apiextensionsv1.CustomResourceDefinition{}
		if err := k8sClient.Get(ctx, types.NamespacedName{Name: crd.Name}, fetched); err != nil {
			return false
		}
		for _, c := range fetched.Status.Conditions {
			if c.Type == apiextensionsv1.Established {
				return c.Status == apiextensionsv1.ConditionTrue
			}
		}
		return false
	}, 30*time.Second, 1*time.Second).Should(BeTrue(),
		fmt.Sprintf("CRD %s should become Established", crd.Name))
}

// removeCRD deletes a CRD and waits for it to be fully removed.
func removeCRD(name string) {
	By(fmt.Sprintf("removing CRD %s", name))
	crd := &apiextensionsv1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{Name: name},
	}
	// Ignore NotFound errors - CRD may already be gone
	_ = k8sClient.Delete(ctx, crd)

	By(fmt.Sprintf("waiting for CRD %s to be deleted", name))
	EventuallyWithOffset(1, func() bool {
		err := k8sClient.Get(ctx, types.NamespacedName{Name: name}, &apiextensionsv1.CustomResourceDefinition{})
		return err != nil // true when NotFound
	}, 60*time.Second, 1*time.Second).Should(BeTrue(),
		fmt.Sprintf("CRD %s should be deleted", name))
}

// getUnstructuredResource fetches a resource as an Unstructured object.
// Pass empty namespace for cluster-scoped resources.
//
//nolint:unparam // namespace is currently always empty but needed for namespaced resources in future tests
func getUnstructuredResource(gvk schema.GroupVersionKind, name, namespace string) (*unstructured.Unstructured, error) {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(gvk)
	key := types.NamespacedName{Name: name, Namespace: namespace}
	err := k8sClient.Get(ctx, key, obj)
	return obj, err
}

// findDriftCorrectedEvents returns all events with reason "DriftCorrected" whose message
// contains the given kind and resource name. Events are emitted on the HCO object.
func findDriftCorrectedEvents(kind, name string) []eventsv1.Event {
	// Use new events.k8s.io/v1 API
	events := &eventsv1.EventList{}
	ExpectWithOffset(1, k8sClient.List(ctx, events, client.InNamespace(operatorNamespace))).To(Succeed())

	var matched []eventsv1.Event
	for _, event := range events.Items {
		if event.Reason == "DriftCorrected" &&
			strings.Contains(event.Note, kind) &&
			strings.Contains(event.Note, name) {
			matched = append(matched, event)
		}
	}
	return matched
}
