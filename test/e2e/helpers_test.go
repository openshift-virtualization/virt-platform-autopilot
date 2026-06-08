package e2e

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"k8s.io/client-go/kubernetes"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	eventsv1 "k8s.io/api/events/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// buildMinimalCRD constructs a minimal CRD with x-kubernetes-preserve-unknown-fields
// suitable for testing without requiring a full schema.
func buildMinimalCRD(group, kind, plural, version string, scope apiextensionsv1.ResourceScope) *apiextensionsv1.CustomResourceDefinition { //nolint:unparam
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

// getOperatorPod returns the autopilot pod by app label.
func getOperatorPod() *corev1.Pod {
	podList := &corev1.PodList{}
	ExpectWithOffset(1, k8sClient.List(ctx, podList,
		client.InNamespace(operatorNamespace),
		client.MatchingLabels{"app": operatorAppLabel},
	)).To(Succeed())
	ExpectWithOffset(1, podList.Items).NotTo(BeEmpty(), "Operator pod should exist")
	return &podList.Items[0]
}

// getManagerRestartCount returns the restart count for the "manager" container in the autopilot pod.
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
	Fail("manager container not found in autopilot pod")
	return -1
}

// waitForOperatorRestart polls until the autopilot container restart count
// exceeds prevCount, then waits for the pod to become healthy.
func waitForOperatorRestart(prevCount int32) {
	By(fmt.Sprintf("waiting for operator restart count to exceed %d", prevCount))
	Eventually(func() int32 {
		return getManagerRestartCount()
	}, 3*time.Minute, 2*time.Second).Should(BeNumerically(">", prevCount),
		"Operator container restart count should increase")

	waitForOperatorHealthy()
}

// isOperatorReady returns true if the autopilot pod is Running with its manager container Ready.
func isOperatorReady() bool {
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
}

// waitForOperatorHealthy waits for the autopilot pod to become Running and Ready,
// then verifies it remains stable (no crash-loop) for a short observation window.
func waitForOperatorHealthy() {
	By("waiting for autopilot pod to become healthy")
	Eventually(isOperatorReady, 3*time.Minute, 2*time.Second).Should(BeTrue(),
		"Operator pod should be Running and Ready")

	By("verifying autopilot pod remains healthy")
	Consistently(isOperatorReady, 2*time.Second, 500*time.Millisecond).Should(BeTrue(),
		"Operator pod should remain Running and Ready")
}

// installCRD creates a CRD and waits for it to reach the Established condition.
func installCRD(crd *apiextensionsv1.CustomResourceDefinition) {
	By(fmt.Sprintf("installing CRD %s", crd.Name))
	ExpectWithOffset(1, k8sClient.Create(ctx, crd)).To(Succeed())

	waitForCRDEstablished(crd.Name)
}

// ensureCRDInstalled installs a CRD only if it does not already exist.
// Returns true if the CRD was newly installed, false if it already existed.
func ensureCRDInstalled(crd *apiextensionsv1.CustomResourceDefinition) bool {
	existing := &apiextensionsv1.CustomResourceDefinition{}
	err := k8sClient.Get(ctx, types.NamespacedName{Name: crd.Name}, existing)
	if err == nil {
		return false
	}
	ExpectWithOffset(1, apierrors.IsNotFound(err)).To(BeTrue(),
		fmt.Sprintf("unexpected error checking CRD %s: %v", crd.Name, err))
	installCRD(crd)
	return true
}

func waitForCRDEstablished(name string) {
	By(fmt.Sprintf("waiting for CRD %s to become Established", name))
	EventuallyWithOffset(2, func() bool {
		fetched := &apiextensionsv1.CustomResourceDefinition{}
		if err := k8sClient.Get(ctx, types.NamespacedName{Name: name}, fetched); err != nil {
			return false
		}
		for _, c := range fetched.Status.Conditions {
			if c.Type == apiextensionsv1.Established {
				return c.Status == apiextensionsv1.ConditionTrue
			}
		}
		return false
	}, 30*time.Second, 1*time.Second).Should(BeTrue(),
		fmt.Sprintf("CRD %s should become Established", name))
}

func newMachineConfigCRD() *apiextensionsv1.CustomResourceDefinition {
	return buildMinimalCRD(
		"machineconfiguration.openshift.io",
		"MachineConfig",
		"machineconfigs",
		"v1",
		apiextensionsv1.ClusterScoped,
	)
}

func newPrometheusRuleCRD() *apiextensionsv1.CustomResourceDefinition {
	return buildMinimalCRD(
		"monitoring.coreos.com",
		"PrometheusRule",
		"prometheusrules",
		"v1",
		apiextensionsv1.NamespaceScoped,
	)
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

// findEventsWithReason returns all events in the operator namespace matching the given reason.
func findEventsWithReason(reason string) []eventsv1.Event {
	eventList := &eventsv1.EventList{}
	ExpectWithOffset(1, k8sClient.List(ctx, eventList, client.InNamespace(operatorNamespace))).To(Succeed())

	var matched []eventsv1.Event
	for _, event := range eventList.Items {
		if event.Reason == reason {
			matched = append(matched, event)
		}
	}
	return matched
}

// AutopilotEvents captures event counts by reason from the operator namespace.
type AutopilotEvents struct {
	ReconcileSucceeded      int
	AssetApplied            int
	DriftDetected           int
	DriftCorrected          int
	CRDMissing              int
	CRDDiscovered           int
	PatchApplied            int
	InvalidPatch            int
	InvalidIgnoreFields     int
	Throttled               int
	ThrashingDetected       int
	AssetSkipped            int
	UnmanagedMode           int
	ApplyFailed             int
	RenderFailed            int
	NoDriftDetected         int
	HardwareDetectionFailed int
	TombstoneDeleted        int
	TombstoneFailed         int
	TombstoneSkipped        int
}

// eventFirings returns the total number of times an event has fired.
// Series.Count already includes the initial firing (starts at 2 on the
// second occurrence), so we return it directly when present.
func eventFirings(event eventsv1.Event) int {
	if event.Series != nil {
		return int(event.Series.Count)
	}
	return 1
}

// captureAutopilotEvents counts all autopilot event firings in the operator namespace.
func captureAutopilotEvents() AutopilotEvents {
	eventList := &eventsv1.EventList{}
	ExpectWithOffset(1, k8sClient.List(ctx, eventList, client.InNamespace(operatorNamespace))).To(Succeed())

	var e AutopilotEvents
	for _, event := range eventList.Items {
		n := eventFirings(event)
		switch event.Reason {
		case "ReconcileSucceeded":
			e.ReconcileSucceeded += n
		case "AssetApplied":
			e.AssetApplied += n
		case "DriftDetected":
			e.DriftDetected += n
		case "DriftCorrected":
			e.DriftCorrected += n
		case "CRDMissing":
			e.CRDMissing += n
		case "CRDDiscovered":
			e.CRDDiscovered += n
		case "PatchApplied":
			e.PatchApplied += n
		case "InvalidPatch":
			e.InvalidPatch += n
		case "InvalidIgnoreFields":
			e.InvalidIgnoreFields += n
		case "Throttled":
			e.Throttled += n
		case "ThrashingDetected":
			e.ThrashingDetected += n
		case "AssetSkipped":
			e.AssetSkipped += n
		case "UnmanagedMode":
			e.UnmanagedMode += n
		case "ApplyFailed":
			e.ApplyFailed += n
		case "RenderFailed":
			e.RenderFailed += n
		case "NoDriftDetected":
			e.NoDriftDetected += n
		case "HardwareDetectionFailed":
			e.HardwareDetectionFailed += n
		case "TombstoneDeleted":
			e.TombstoneDeleted += n
		case "TombstoneFailed":
			e.TombstoneFailed += n
		case "TombstoneSkipped":
			e.TombstoneSkipped += n
		}
	}
	return e
}

// AssetMetrics captures all Prometheus metrics for a specific managed asset.
// Values are -1 when the metric is not found (not yet emitted by the operator).
type AssetMetrics struct {
	ComplianceStatus       float64 // 1=synced, 0=drifted/failed, -1=not found
	ReconcileDurationCount int     // how many times this asset was reconciled
	ReconcileDurationSum   float64 // total reconciliation time in seconds
	ThrashingTotal         int     // anti-thrashing gate hits
	PausedResources        float64 // 1=paused, 0=active, -1=not found
	CustomizationInfo      float64 // 1=customized, -1=not found
	MissingDependency      float64 // 1=missing, 0=present, -1=not found
	TombstoneStatus        float64 // 1=exists, 0=deleted, -1=error, -2=skipped, or -1=not found
}

// fetchMetricsBody returns the raw metrics output from the operator's /metrics endpoint.
func fetchMetricsBody() string {
	pod := getOperatorPod()
	clientset, err := kubernetes.NewForConfig(cfg)
	ExpectWithOffset(2, err).NotTo(HaveOccurred())

	body, err := clientset.CoreV1().Pods(operatorNamespace).
		ProxyGet("http", pod.Name, "8080", "/metrics", nil).
		DoRaw(context.Background())
	ExpectWithOffset(2, err).NotTo(HaveOccurred())

	return string(body)
}

// captureAssetMetrics fetches all metrics for a specific asset from the operator's /metrics endpoint.
// Labels are matched by kind/name/namespace. For missing_dependency the labels are group/version/kind
// so it uses the kind parameter only.
func captureAssetMetrics(kind, name, namespace string) AssetMetrics { //nolint:unparam
	body := fetchMetricsBody()
	labelFilter := fmt.Sprintf(`kind="%s",name="%s",namespace="%s"`, kind, name, namespace)

	m := AssetMetrics{
		ComplianceStatus:       -1,
		ReconcileDurationCount: 0,
		ReconcileDurationSum:   -1,
		ThrashingTotal:         0,
		PausedResources:        -1,
		CustomizationInfo:      -1,
		MissingDependency:      -1,
		TombstoneStatus:        -1,
	}

	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "#") || line == "" {
			continue
		}

		if strings.Contains(line, labelFilter) {
			val := parseMetricValue(line)
			switch {
			case strings.HasPrefix(line, "kubevirt_autopilot_compliance_status"):
				m.ComplianceStatus = val
			case strings.HasPrefix(line, "kubevirt_autopilot_reconcile_duration_seconds_count"):
				m.ReconcileDurationCount = int(val)
			case strings.HasPrefix(line, "kubevirt_autopilot_reconcile_duration_seconds_sum"):
				m.ReconcileDurationSum = val
			case strings.HasPrefix(line, "kubevirt_autopilot_thrashing_total"):
				m.ThrashingTotal = int(val)
			case strings.HasPrefix(line, "kubevirt_autopilot_paused_resources"):
				m.PausedResources = val
			case strings.HasPrefix(line, "kubevirt_autopilot_customization_info"):
				m.CustomizationInfo = val
			case strings.HasPrefix(line, "kubevirt_autopilot_tombstone_status"):
				m.TombstoneStatus = val
			}
		}

		// missing_dependency uses group/version/kind labels instead of kind/name/namespace
		if strings.HasPrefix(line, "kubevirt_autopilot_missing_dependency") &&
			strings.Contains(line, fmt.Sprintf(`kind="%s"`, kind)) {
			m.MissingDependency = parseMetricValue(line)
		}
	}

	return m
}

func parseMetricValue(line string) float64 {
	parts := strings.Fields(line)
	if len(parts) == 2 {
		val, err := strconv.ParseFloat(parts[1], 64)
		if err == nil {
			return val
		}
	}
	return -1
}

// findEventsWithReasonAfter returns events matching the given reason that occurred after the specified time.
func findEventsWithReasonAfter(reason string, after time.Time) []eventsv1.Event {
	events := findEventsWithReason(reason)
	var filtered []eventsv1.Event
	for _, event := range events {
		if event.EventTime.After(after) {
			filtered = append(filtered, event)
		}
	}
	return filtered
}

// deleteResource deletes a resource by GVK, name, and namespace. Safe to call if the resource doesn't exist.
func deleteResource(gvk schema.GroupVersionKind, name, namespace string) {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(gvk)
	obj.SetName(name)
	obj.SetNamespace(namespace)
	_ = k8sClient.Delete(ctx, obj)
}

// ensureHCOExists creates a minimal HCO instance if one doesn't already exist.
func ensureHCOExists() {
	hco := &unstructured.Unstructured{}
	hco.SetGroupVersionKind(schema.GroupVersionKind{
		Group: "hco.kubevirt.io", Version: "v1", Kind: "HyperConverged",
	})
	err := k8sClient.Get(ctx, client.ObjectKey{Name: hcoName, Namespace: operatorNamespace}, hco)
	if err == nil {
		return
	}
	ExpectWithOffset(1, apierrors.IsNotFound(err)).To(BeTrue(),
		fmt.Sprintf("unexpected error checking HCO: %v", err))
	hco = &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "hco.kubevirt.io/v1",
			"kind":       "HyperConverged",
			"metadata": map[string]any{
				"name":      hcoName,
				"namespace": operatorNamespace,
			},
			"spec": map[string]any{},
		},
	}
	ExpectWithOffset(1, k8sClient.Create(ctx, hco)).To(Succeed())
}

// removeManagedByLabel patches the HCO to remove the managed-by label.
// Used to set up the "unlabeled adoption" scenario.
func removeManagedByLabel(labelKey string) {
	patch := []byte(fmt.Sprintf(`{"metadata":{"labels":{"%s":null}}}`, labelKey))
	ref := hcoRef()
	EventuallyWithOffset(1, func() error {
		return k8sClient.Patch(ctx, ref, client.RawPatch(types.MergePatchType, patch))
	}, timeout, interval).Should(Succeed(), "managed-by label should be removed from HCO")
}

// hasLabel checks if an unstructured object has a specific label key-value pair.
func hasLabel(obj *unstructured.Unstructured, key, value string) bool { //nolint:unparam
	labels := obj.GetLabels()
	return labels != nil && labels[key] == value
}

// patchAutopilotAndWait patches the autopilot annotation on the HCO and waits for
// the triggered reconciliation to complete before returning.
// If the annotation already has the desired value,
// it returns immediately (no-op).
// For disable, it waits a short period since
// no ReconcileSucceeded event is emitted when the operator goes idle.
func patchAutopilotAndWait(value string) {
	ref := hcoRef()

	// Check current value — skip if already set
	current := &unstructured.Unstructured{}
	current.SetGroupVersionKind(schema.GroupVersionKind{
		Group: "hco.kubevirt.io", Version: "v1", Kind: "HyperConverged",
	})
	if err := k8sClient.Get(ctx, client.ObjectKey{Name: hcoName, Namespace: operatorNamespace}, current); err == nil {
		annotations := current.GetAnnotations()
		currentVal := annotations[autopilotAnnotation]
		isDisable := value == "" || value == "null"
		if isDisable && currentVal == "" {
			return
		}
		if !isDisable && currentVal == value {
			return
		}
	}

	if value == "" || value == "null" {
		EventuallyWithOffset(1, func() error {
			return k8sClient.Patch(ctx, ref, client.RawPatch(types.MergePatchType, autopilotPatch(value)))
		}, 2*time.Minute, 2*time.Second).Should(Succeed())

		EventuallyWithOffset(1, func() string {
			obj := &unstructured.Unstructured{}
			obj.SetGroupVersionKind(schema.GroupVersionKind{
				Group: "hco.kubevirt.io", Version: "v1", Kind: "HyperConverged",
			})
			if err := k8sClient.Get(ctx, client.ObjectKey{Name: hcoName, Namespace: operatorNamespace}, obj); err != nil {
				return "error"
			}
			return obj.GetAnnotations()[autopilotAnnotation]
		}, timeout, interval).Should(BeEmpty(), "Autopilot annotation should be removed from HCO")

		var prev AutopilotEvents
		EventuallyWithOffset(1, func() bool {
			current := captureAutopilotEvents()
			stable := prev == current
			prev = current
			return stable
		}, timeout, 2*time.Second).Should(BeTrue(),
			"Autopilot events should stabilize after disabling")
		waitForOperatorHealthy()
		return
	}

	patchTime := time.Now()
	EventuallyWithOffset(1, func() error {
		return k8sClient.Patch(ctx, ref, client.RawPatch(types.MergePatchType, autopilotPatch(value)))
	}, 2*time.Minute, 2*time.Second).Should(Succeed())

	EventuallyWithOffset(1, func() bool {
		for _, event := range findEventsWithReasonAfter("ReconcileSucceeded", patchTime) {
			if event.Regarding.Name == hcoName {
				return true
			}
		}
		return false
	}, 2*time.Minute, 2*time.Second).Should(BeTrue(), "Reconciliation should complete after patching autopilot")
	waitForOperatorHealthy()
}

// autopilotPatch returns a JSON merge patch that sets the autopilot annotation.
// Pass "" or "null" to remove the annotation, or a value like "true" or "swap-enable,prometheus-alerts".
func autopilotPatch(value string) []byte {
	if value == "" || value == "null" {
		return []byte(fmt.Sprintf(`{"metadata":{"annotations":{"%s":null}}}`, autopilotAnnotation))
	}
	return []byte(fmt.Sprintf(`{"metadata":{"annotations":{"%s":"%s"}}}`, autopilotAnnotation, value))
}

// hcoRef returns an unstructured reference to the HCO CR for use with Patch calls.
func hcoRef() *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "hco.kubevirt.io",
		Version: "v1",
		Kind:    "HyperConverged",
	})
	obj.SetName(hcoName)
	obj.SetNamespace(operatorNamespace)
	return obj
}

// isOpenShiftCluster returns true when the ClusterVersion CRD exists,
// which is always present on OCP but never on Kind.
func isOpenShiftCluster() bool {
	crd := &apiextensionsv1.CustomResourceDefinition{}
	err := k8sClient.Get(ctx, types.NamespacedName{Name: "clusterversions.config.openshift.io"}, crd)
	return err == nil
}
