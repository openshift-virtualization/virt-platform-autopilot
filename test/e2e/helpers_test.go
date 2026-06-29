package e2e

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"k8s.io/client-go/kubernetes"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	authenticationv1 "k8s.io/api/authentication/v1"
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

// ensureCRDInstalled fails the test if the given CRD is not installed on the cluster.
func ensureCRDInstalled(name string) {
	By(fmt.Sprintf("ensuring CRD %s is installed", name))
	existing := &apiextensionsv1.CustomResourceDefinition{}
	ExpectWithOffset(1, k8sClient.Get(ctx, types.NamespacedName{Name: name}, existing)).To(Succeed(),
		fmt.Sprintf("CRD %s must be installed on the cluster", name)) // For kind, run kind-cluster.sh install-crds
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

// installCRDFromFile applies a CRD YAML file (relative to the project root)
// using server-side apply, matching what kind-cluster.sh install-crds does.
func installCRDFromFile(relativePath string) {
	// test/e2e/*.go → project root is ../../
	_, thisFile, _, _ := runtime.Caller(0)
	absPath := filepath.Join(filepath.Dir(thisFile), "..", "..", relativePath)
	By(fmt.Sprintf("installing CRD from %s", absPath))
	cmd := exec.CommandContext(ctx, "kubectl", "apply", "--server-side", "-f", absPath)
	output, err := cmd.CombinedOutput()
	ExpectWithOffset(1, err).NotTo(HaveOccurred(),
		fmt.Sprintf("kubectl apply failed for %s: %s", absPath, string(output)))
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

// EventFilter specifies criteria for finding events in the operator namespace.
// Zero-value fields are not applied (match everything).
type EventFilter struct {
	Reason string
	Since  time.Time
	Kind   string
	Name   string
}

// findEvents returns events in the operator namespace matching all non-zero filter fields.
// Kind and Name are matched as substrings in event.Note.
// When Since is set, an event matches if it was first observed at/after Since,
// or if it has a Series whose last firing is at/after Since.
func findEvents(filter EventFilter) []eventsv1.Event {
	eventList := &eventsv1.EventList{}
	ExpectWithOffset(1, k8sClient.List(ctx, eventList, client.InNamespace(operatorNamespace))).To(Succeed())

	var matched []eventsv1.Event
	for _, event := range eventList.Items {
		if filter.Reason != "" && event.Reason != filter.Reason {
			continue
		}
		if !filter.Since.IsZero() {
			firstOK := !event.EventTime.Time.Before(filter.Since)
			seriesOK := event.Series != nil && !event.Series.LastObservedTime.Time.Before(filter.Since)
			if !firstOK && !seriesOK {
				continue
			}
		}
		if filter.Kind != "" && !strings.Contains(event.Note, filter.Kind) {
			continue
		}
		if filter.Name != "" && !strings.Contains(event.Note, filter.Name) {
			continue
		}
		matched = append(matched, event)
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

// captureAutopilotEvents counts autopilot event firings in the operator namespace.
// When since is non-zero, only events fired at or after that time are counted.
func captureAutopilotEvents(since ...time.Time) AutopilotEvents {
	var filter EventFilter
	if len(since) > 0 {
		filter.Since = since[0]
	}
	events := findEvents(filter)

	var e AutopilotEvents
	for _, event := range events {
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
func captureAssetMetrics(kind, name, namespace string) AssetMetrics {
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

// deleteResource deletes a resource by GVK, name, and namespace. Safe to call if the resource doesn't exist.
func deleteResource(gvk schema.GroupVersionKind, name, namespace string) {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(gvk)
	obj.SetName(name)
	obj.SetNamespace(namespace)
	_ = k8sClient.Delete(ctx, obj)

	EventuallyWithOffset(1, func() bool {
		err := k8sClient.Get(ctx, client.ObjectKey{Name: name, Namespace: namespace}, obj)
		return apierrors.IsNotFound(err)
	}, 2*time.Minute, 10*time.Second).Should(BeTrue(),
		fmt.Sprintf("%s/%s should be fully deleted", gvk.Kind, name))
}

// ensureHCOExists fails the test if the HCO instance does not exist on the cluster.
func ensureHCOExists() {
	By("ensuring HCO instance exists")
	hco := &unstructured.Unstructured{}
	hco.SetGroupVersionKind(schema.GroupVersionKind{
		Group: "hco.kubevirt.io", Version: "v1", Kind: "HyperConverged",
	})
	ExpectWithOffset(1, k8sClient.Get(ctx, client.ObjectKey{Name: hcoName, Namespace: operatorNamespace}, hco)).To(Succeed(),
		fmt.Sprintf("HCO %s/%s must exist on the cluster", operatorNamespace, hcoName))
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
		for _, event := range findEvents(EventFilter{Reason: "ReconcileSucceeded", Since: patchTime}) {
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

func crdInstalled(name string) bool {
	crd := &apiextensionsv1.CustomResourceDefinition{}
	return k8sClient.Get(ctx, types.NamespacedName{Name: name}, crd) == nil
}

// --- Alert test helpers (OCP-only) ---

func touchHCO() {
	patch := []byte(fmt.Sprintf(`{"metadata":{"annotations":{"e2e.test/touch":"%d"}}}`, time.Now().UnixNano()))
	ref := hcoRef()
	ExpectWithOffset(1, k8sClient.Patch(ctx, ref, client.RawPatch(types.MergePatchType, patch))).To(Succeed())
}

func createBlockingWebhook(asset testAsset) {
	plural := asset.Plural
	failurePolicy := admissionregistrationv1.Fail
	sideEffects := admissionregistrationv1.SideEffectClassNone
	port := int32(443)

	webhook := &admissionregistrationv1.ValidatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name: asset.webhookName(),
		},
		Webhooks: []admissionregistrationv1.ValidatingWebhook{
			{
				Name: "block." + plural + ".e2e.test",
				ClientConfig: admissionregistrationv1.WebhookClientConfig{
					Service: &admissionregistrationv1.ServiceReference{
						Namespace: operatorNamespace,
						Name:      "e2e-nonexistent-webhook",
						Port:      &port,
					},
				},
				Rules: []admissionregistrationv1.RuleWithOperations{
					{
						Operations: []admissionregistrationv1.OperationType{
							admissionregistrationv1.Create,
							admissionregistrationv1.Update,
						},
						Rule: admissionregistrationv1.Rule{
							APIGroups:   []string{asset.GVK.Group},
							APIVersions: []string{asset.GVK.Version},
							Resources:   []string{plural},
						},
					},
				},
				FailurePolicy: &failurePolicy,
				SideEffects:   &sideEffects,
				ObjectSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{
						managedByLabel: managedByValue,
					},
				},
				AdmissionReviewVersions: []string{"v1"},
			},
		},
	}

	By(fmt.Sprintf("creating blocking webhook %s", asset.webhookName()))
	ExpectWithOffset(1, k8sClient.Create(ctx, webhook)).To(Succeed())
}

func deleteBlockingWebhook(asset testAsset) {
	By(fmt.Sprintf("deleting blocking webhook %s", asset.webhookName()))
	webhook := &admissionregistrationv1.ValidatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: asset.webhookName()},
	}
	err := k8sClient.Delete(ctx, webhook)
	if err != nil && !apierrors.IsNotFound(err) {
		ExpectWithOffset(1, err).NotTo(HaveOccurred())
	}
}

func setPrometheusRuleUnmanaged() {
	By("setting PrometheusRule to unmanaged mode")
	patch := []byte(`{"metadata":{"annotations":{"platform.kubevirt.io/mode":"unmanaged"}}}`)
	ref := &unstructured.Unstructured{}
	ref.SetGroupVersionKind(prometheusRuleGVK)
	ref.SetName(prometheusRuleName)
	ref.SetNamespace(operatorNamespace)
	ExpectWithOffset(1, k8sClient.Patch(ctx, ref, client.RawPatch(types.MergePatchType, patch))).To(Succeed())
}

func removePrometheusRuleUnmanaged() {
	By("removing unmanaged mode from PrometheusRule")
	patch := []byte(`{"metadata":{"annotations":{"platform.kubevirt.io/mode":null}}}`)
	ref := &unstructured.Unstructured{}
	ref.SetGroupVersionKind(prometheusRuleGVK)
	ref.SetName(prometheusRuleName)
	ref.SetNamespace(operatorNamespace)
	ExpectWithOffset(1, k8sClient.Patch(ctx, ref, client.RawPatch(types.MergePatchType, patch))).To(Succeed())
}

func patchAlertForDurations(targetFor string) {
	By(fmt.Sprintf("patching all alert 'for' durations to %s", targetFor))
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(prometheusRuleGVK)
	ExpectWithOffset(1, k8sClient.Get(ctx, types.NamespacedName{
		Name:      prometheusRuleName,
		Namespace: operatorNamespace,
	}, obj)).To(Succeed())

	groups, found, err := unstructured.NestedSlice(obj.Object, "spec", "groups")
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	ExpectWithOffset(1, found).To(BeTrue(), "PrometheusRule should have spec.groups")

	for _, group := range groups {
		groupMap, ok := group.(map[string]any)
		ExpectWithOffset(1, ok).To(BeTrue())
		rules, ok := groupMap["rules"].([]any)
		ExpectWithOffset(1, ok).To(BeTrue())
		for _, rule := range rules {
			ruleMap, ok := rule.(map[string]any)
			ExpectWithOffset(1, ok).To(BeTrue())
			if _, hasFor := ruleMap["for"]; hasFor {
				ruleMap["for"] = targetFor
			}
		}
	}

	ExpectWithOffset(1, unstructured.SetNestedSlice(obj.Object, groups, "spec", "groups")).To(Succeed())
	ExpectWithOffset(1, k8sClient.Update(ctx, obj)).To(Succeed())
}

type prometheusQueryResponse struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string `json:"resultType"`
		Result     []any  `json:"result"`
	} `json:"data"`
}

func queryPrometheus(promQL string) *prometheusQueryResponse {
	route := &unstructured.Unstructured{}
	route.SetGroupVersionKind(schema.GroupVersionKind{
		Group: "route.openshift.io", Version: "v1", Kind: "Route",
	})
	if err := k8sClient.Get(ctx, types.NamespacedName{
		Name: "thanos-querier", Namespace: "openshift-monitoring",
	}, route); err != nil {
		GinkgoWriter.Printf("queryPrometheus: cannot get thanos-querier route: %v\n", err)
		return nil
	}
	host, _, _ := unstructured.NestedString(route.Object, "spec", "host")
	if host == "" {
		GinkgoWriter.Println("queryPrometheus: route has no host")
		return nil
	}

	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		GinkgoWriter.Printf("queryPrometheus: cannot create clientset: %v\n", err)
		return nil
	}

	expSeconds := int64(600)
	tokenReq, err := clientset.CoreV1().ServiceAccounts("openshift-monitoring").
		CreateToken(ctx, "prometheus-k8s", &authenticationv1.TokenRequest{
			Spec: authenticationv1.TokenRequestSpec{
				ExpirationSeconds: &expSeconds,
			},
		}, metav1.CreateOptions{})
	if err != nil {
		GinkgoWriter.Printf("queryPrometheus: cannot create SA token: %v\n", err)
		return nil
	}

	reqURL := fmt.Sprintf("https://%s/api/v1/query", host)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		GinkgoWriter.Printf("queryPrometheus: cannot build request: %v\n", err)
		return nil
	}
	q := httpReq.URL.Query()
	q.Set("query", promQL)
	httpReq.URL.RawQuery = q.Encode()
	httpReq.Header.Set("Authorization", "Bearer "+tokenReq.Status.Token)

	httpClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
		},
	}
	httpResp, err := httpClient.Do(httpReq)
	if err != nil {
		GinkgoWriter.Printf("queryPrometheus: HTTP error: %v\n", err)
		return nil
	}
	defer httpResp.Body.Close()

	body, err := io.ReadAll(httpResp.Body)
	if err != nil {
		GinkgoWriter.Printf("queryPrometheus: cannot read body: %v\n", err)
		return nil
	}

	var resp prometheusQueryResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		GinkgoWriter.Printf("queryPrometheus: unmarshal error: %v, body: %s\n", err, string(body))
		return nil
	}

	return &resp
}

func queryFiringAlert(alertName string, attempt, maxAttempts int, labelFilters ...string) map[string]string {
	promQL := fmt.Sprintf(`ALERTS{alertname="%s",alertstate="firing"`, alertName)
	for i := 0; i+1 < len(labelFilters); i += 2 {
		promQL += fmt.Sprintf(`,%s="%s"`, labelFilters[i], labelFilters[i+1])
	}
	promQL += "}"
	resp := queryPrometheus(promQL)
	if resp == nil {
		GinkgoWriter.Printf("queryFiringAlert(%s) [%d/%d]: no response\n", alertName, attempt, maxAttempts)
		return nil
	}
	if resp.Status != "success" || len(resp.Data.Result) == 0 {
		GinkgoWriter.Printf("queryFiringAlert(%s) [%d/%d]: not firing yet\n", alertName, attempt, maxAttempts)
		return nil
	}

	resultMap, ok := resp.Data.Result[0].(map[string]any)
	if !ok {
		GinkgoWriter.Printf("queryFiringAlert(%s) [%d/%d]: unexpected result format\n", alertName, attempt, maxAttempts)
		return nil
	}
	metricRaw, ok := resultMap["metric"].(map[string]any)
	if !ok {
		GinkgoWriter.Printf("queryFiringAlert(%s) [%d/%d]: missing metric labels\n", alertName, attempt, maxAttempts)
		return nil
	}

	labels := make(map[string]string, len(metricRaw))
	for k, v := range metricRaw {
		labels[k] = fmt.Sprint(v)
	}

	GinkgoWriter.Printf("queryFiringAlert(%s) [%d/%d]: firing — kind=%s name=%s severity=%s\n",
		alertName, attempt, maxAttempts, labels["kind"], labels["name"], labels["severity"])
	return labels
}

type missingDependency struct {
	Kind    string
	Group   string
	Version string
}

func getMissingDependenciesFromMetrics() []missingDependency {
	var deps []missingDependency
	body := fetchMetricsBody()
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "#") || line == "" {
			continue
		}
		if strings.HasPrefix(line, "kubevirt_autopilot_missing_dependency") && parseMetricValue(line) == 1 {
			deps = append(deps, missingDependency{
				Kind:    parseMetricLabel(line, "kind"),
				Group:   parseMetricLabel(line, "group"),
				Version: parseMetricLabel(line, "version"),
			})
		}
	}
	return deps
}

func parseMetricLabel(line, key string) string {
	search := key + `="`
	idx := strings.Index(line, search)
	if idx < 0 {
		return ""
	}
	start := idx + len(search)
	end := strings.Index(line[start:], `"`)
	if end < 0 {
		return ""
	}
	return line[start : start+end]
}

// --- Anti-thrashing test helpers ---

// triggerEditWar repeatedly modifies the managed-by label to trigger drift
// detection until the operator exhausts its token-bucket budget and sets the
// pause annotation. We modify managed-by (not add a new label) because SSA
// drift detection only sees changes to fields owned by the operator's field
// manager — an extra unmanaged label would be invisible to the dry-run diff.
func triggerEditWar(gvk schema.GroupVersionKind, name, namespace string) {
	driftPatch := []byte(fmt.Sprintf(`{"metadata":{"labels":{"%s":"tampered"}}}`, managedByLabel))
	ref := &unstructured.Unstructured{}
	ref.SetGroupVersionKind(gvk)
	ref.SetName(name)
	ref.SetNamespace(namespace)

	Eventually(func() bool {
		obj, err := getUnstructuredResource(gvk, name, namespace)
		if err != nil {
			return false
		}
		if ann := obj.GetAnnotations(); ann != nil && ann[pauseAnnotation] == "true" {
			return true
		}
		_ = k8sClient.Patch(ctx, ref, client.RawPatch(types.MergePatchType, driftPatch))
		touchHCO()
		return false
	}, 5*time.Minute, 2*time.Second).Should(BeTrue(),
		fmt.Sprintf("Operator should set pause annotation on %s/%s after detecting edit war", gvk.Kind, name))
}

// removePauseAnnotation removes the reconcile-paused annotation from a managed resource.
func removePauseAnnotation(gvk schema.GroupVersionKind, name, namespace string) {
	patch := []byte(fmt.Sprintf(`{"metadata":{"annotations":{"%s":null}}}`, pauseAnnotation))
	ref := &unstructured.Unstructured{}
	ref.SetGroupVersionKind(gvk)
	ref.SetName(name)
	ref.SetNamespace(namespace)

	EventuallyWithOffset(1, func() error {
		return k8sClient.Patch(ctx, ref, client.RawPatch(types.MergePatchType, patch))
	}, timeout, interval).Should(Succeed(),
		fmt.Sprintf("Should remove pause annotation from %s/%s", gvk.Kind, name))
}

// queryAlertNotFiring returns true when the given alert is NOT firing in Prometheus.
func queryAlertNotFiring(alertName string, attempt, maxAttempts int, labelFilters ...string) bool {
	labels := queryFiringAlert(alertName, attempt, maxAttempts, labelFilters...)
	return labels == nil
}

// getOperatorLogs returns the operator pod logs since the given time.
// Uses SinceSeconds (relative, kubelet-evaluated) instead of SinceTime
// to avoid clock-skew issues between the test runner and the Kind node.
func getOperatorLogs(since time.Time) string {
	pod := getOperatorPod()
	clientset, err := kubernetes.NewForConfig(cfg)
	ExpectWithOffset(1, err).NotTo(HaveOccurred())

	container := "manager"
	if len(pod.Spec.Containers) == 1 {
		container = pod.Spec.Containers[0].Name
	}

	sinceSeconds := int64(time.Since(since).Seconds()) + 120
	logs, err := clientset.CoreV1().Pods(operatorNamespace).
		GetLogs(pod.Name, &corev1.PodLogOptions{
			Container:    container,
			SinceSeconds: &sinceSeconds,
		}).DoRaw(context.Background())
	ExpectWithOffset(1, err).NotTo(HaveOccurred())

	return string(logs)
}
