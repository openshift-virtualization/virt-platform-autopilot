package e2e

import (
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("Pre-flight sanity gate", Label("sanity"), Ordered, SpecPriority(1000), func() {
	BeforeAll(func() {
		if !isOpenShiftCluster() {
			Skip("pre-flight sanity gate only runs against real OpenShift clusters")
		}
	})

	AfterEach(func() {
		report := CurrentSpecReport()
		if report.Failed() {
			AbortSuite(fmt.Sprintf(
				"pre-flight sanity gate failed — cluster not in a suitable state for testing\n\nDetails:\n%s",
				report.Failure.Message,
			))
		}
	})

	// MCP check runs first: node reboots during a rollout make subsequent API calls
	// unreliable. Waiting here ensures the cluster is stable before any other check.
	It("all MachineConfigPools are stable", func() {
		defer func() {
			if r := recover(); r != nil {
				Fail("CLUSTER NOT READY: MachineConfigPools did not stabilize — " +
					"a MachineConfig rollout may be in progress or degraded; " +
					"run 'oc get mcp' for details (self-resolving, no action needed unless Degraded=True)")
			}
		}()
		waitForMCPStable()
	})

	It("all cluster nodes are Ready and schedulable", func() {
		nodeList := &corev1.NodeList{}
		Expect(k8sClient.List(ctx, nodeList)).To(Succeed())
		Expect(nodeList.Items).NotTo(BeEmpty(), "CLUSTER NOT READY: no nodes found in cluster")

		var failures []string
		for _, node := range nodeList.Items {
			if node.Spec.Unschedulable {
				failures = append(failures, fmt.Sprintf(
					"CLUSTER NOT READY: node %s is cordoned (Unschedulable=true) — "+
						"uncordon the node before running tests",
					node.Name))
			}
			for _, cond := range node.Status.Conditions {
				switch cond.Type {
				case corev1.NodeReady:
					if cond.Status != corev1.ConditionTrue {
						failures = append(failures, fmt.Sprintf(
							"CLUSTER NOT READY: node %s is not Ready: %s",
							node.Name, cond.Message))
					}
				case corev1.NodeMemoryPressure:
					if cond.Status == corev1.ConditionTrue {
						failures = append(failures, fmt.Sprintf(
							"CLUSTER NOT READY: node %s has MemoryPressure: %s",
							node.Name, cond.Message))
					}
				case corev1.NodeDiskPressure:
					if cond.Status == corev1.ConditionTrue {
						failures = append(failures, fmt.Sprintf(
							"CLUSTER NOT READY: node %s has DiskPressure: %s",
							node.Name, cond.Message))
					}
				case corev1.NodePIDPressure:
					if cond.Status == corev1.ConditionTrue {
						failures = append(failures, fmt.Sprintf(
							"CLUSTER NOT READY: node %s has PIDPressure: %s",
							node.Name, cond.Message))
					}
				}
			}
		}
		if len(failures) > 0 {
			Fail(strings.Join(failures, "\n"))
		}
	})

	It("all ClusterOperators are available and not degraded", func() {
		coList := &unstructured.UnstructuredList{}
		coList.SetGroupVersionKind(schema.GroupVersionKind{
			Group:   "config.openshift.io",
			Version: "v1",
			Kind:    "ClusterOperatorList",
		})
		Expect(k8sClient.List(ctx, coList)).To(Succeed())

		var failures []string
		for _, co := range coList.Items {
			name := co.GetName()
			conditions, _, _ := unstructured.NestedSlice(co.Object, "status", "conditions")
			condMap := parseConditions(conditions)

			if condMap["Available"] != "True" {
				failures = append(failures, fmt.Sprintf(
					"CLUSTER NOT READY: ClusterOperator %s is not available — %s",
					name, condMap["Available_message"]))
			}
			if condMap["Degraded"] == "True" {
				failures = append(failures, fmt.Sprintf(
					"CLUSTER NOT READY: ClusterOperator %s is degraded — %s",
					name, condMap["Degraded_message"]))
			}
			if condMap["Progressing"] == "True" {
				failures = append(failures, fmt.Sprintf(
					"CLUSTER NOT READY: ClusterOperator %s is progressing (upgrade in flight?) — %s",
					name, condMap["Progressing_message"]))
			}
		}
		if len(failures) > 0 {
			Fail(strings.Join(failures, "\n"))
		}
	})

	It("HyperConverged operator is available and not degraded", func() {
		// HCO may be temporarily unavailable/progressing after a MCP rollout (node reboots
		// cause KubeVirt to redeploy). Wait up to 10 minutes — self-resolving unless Degraded.
		Eventually(func() string {
			hco := &unstructured.Unstructured{}
			hco.SetGroupVersionKind(hcoGVK)
			if err := k8sClient.Get(ctx, client.ObjectKey{Name: hcoName, Namespace: operatorNamespace}, hco); err != nil {
				return fmt.Sprintf("CLUSTER NOT READY: cannot get HyperConverged %s/%s — ensure CNV is installed: %v",
					operatorNamespace, hcoName, err)
			}
			conditions, _, _ := unstructured.NestedSlice(hco.Object, "status", "conditions")
			condMap := parseConditions(conditions)

			if condMap["Degraded"] == "True" {
				return fmt.Sprintf("CLUSTER NOT READY: HyperConverged is degraded — %s",
					condMap["Degraded_message"])
			}
			if condMap["Available"] != "True" && condMap["Progressing"] != "True" {
				return fmt.Sprintf("CLUSTER NOT READY: HyperConverged is not available and not progressing — %s",
					condMap["Available_message"])
			}
			if condMap["Available"] != "True" || condMap["Progressing"] == "True" {
				return fmt.Sprintf("waiting for HyperConverged to become available (Available=%s Progressing=%s)",
					condMap["Available"], condMap["Progressing"])
			}
			return ""
		}, 10*time.Minute, 10*time.Second).Should(BeEmpty(),
			"CLUSTER NOT READY: HyperConverged did not become available within 10 minutes — "+
				"check 'oc get hco -n openshift-cnv' for details")
	})

	It("virt-platform-autopilot deployment has ReadyReplicas > 0", func() {
		deployment := &appsv1.Deployment{}
		Eventually(func() int32 {
			if err := k8sClient.Get(ctx, types.NamespacedName{
				Name:      operatorDeployment,
				Namespace: operatorNamespace,
			}, deployment); err != nil {
				return 0
			}
			return deployment.Status.ReadyReplicas
		}, timeout, interval).Should(BeNumerically(">", 0),
			"CLUSTER NOT READY: virt-platform-autopilot deployment has no ready replicas — "+
				"ensure the operator is deployed and running before executing tests")
	})

	It("all managed assets are compliant, not paused, and have no active customizations", func() {
		assets := discoverActiveAssets()
		Expect(assets).NotTo(BeEmpty(),
			"CLUSTER NOT READY: no assets found in operator metrics — "+
				"ensure the operator is reconciling and emitting metrics")

		var failures []string
		for _, asset := range assets {
			m := captureAssetMetrics(asset.Kind, asset.Name, asset.Namespace)

			if m.ComplianceStatus != 1.0 && m.PausedResources != 1.0 {
				failures = append(failures, fmt.Sprintf(
					"CLUSTER NOT READY: %s has compliance_status=%.0f (expected 1=synced) — "+
						"the cluster has drift or the operator failed to apply this asset",
					asset.label(), m.ComplianceStatus))
			}
			if m.PausedResources == 1.0 {
				failures = append(failures, fmt.Sprintf(
					"CLUSTER NOT READY: %s is paused (platform.kubevirt.io/reconcile-paused=true) — "+
						"remove the pause annotation before running tests",
					asset.label()))
			}
			if m.CustomizationInfo == 1.0 {
				failures = append(failures, fmt.Sprintf(
					"CLUSTER NOT READY: %s has active user customizations — "+
						"remove all user override annotations before running tests to avoid non-deterministic results",
					asset.label()))
			}
		}
		if len(failures) > 0 {
			Fail(strings.Join(failures, "\n"))
		}
	})

	It("no error events in the last 2 minutes", func() {
		windowStart := time.Now().Add(-2 * time.Minute)
		e := captureAutopilotEvents(windowStart)

		var failures []string
		if e.ApplyFailed > 0 {
			failures = append(failures, fmt.Sprintf(
				"CLUSTER NOT READY: %d ApplyFailed event(s) in the last 2 minutes — "+
					"the operator failed to apply one or more managed assets",
				e.ApplyFailed))
		}
		if e.ThrashingDetected > 0 {
			failures = append(failures, fmt.Sprintf(
				"CLUSTER NOT READY: %d ThrashingDetected event(s) — "+
					"one or more assets are paused due to repeated conflicting changes; "+
					"remove platform.kubevirt.io/reconcile-paused before running tests",
				e.ThrashingDetected))
		}
		if e.InvalidPatch > 0 {
			failures = append(failures, fmt.Sprintf(
				"CLUSTER NOT READY: %d InvalidPatch event(s) — "+
					"a user patch annotation on one or more assets is invalid",
				e.InvalidPatch))
		}
		if e.TombstoneFailed > 0 {
			failures = append(failures, fmt.Sprintf(
				"CLUSTER NOT READY: %d TombstoneFailed event(s) — "+
					"the operator failed to delete one or more tombstoned resources",
				e.TombstoneFailed))
		}
		if len(failures) > 0 {
			Fail(strings.Join(failures, "\n"))
		}
	})

	It("no critical alerts are firing", func() {
		resp := queryPrometheus(fmt.Sprintf(
			`ALERTS{alertstate="firing",namespace="%s",severity="critical"}`,
			operatorNamespace,
		))
		if resp == nil || resp.Status != "success" {
			Skip("could not reach Prometheus — skipping alert check")
		}
		Expect(resp.Data.Result).To(BeEmpty(),
			"CLUSTER NOT READY: %d critical alert(s) are firing in %s — "+
				"resolve all critical alerts before running tests",
			len(resp.Data.Result), operatorNamespace)
	})
})

// parseConditions converts a slice of unstructured conditions into a flat map
// keyed by "Type" (status) and "Type_message" (message) for easy lookup.
func parseConditions(conditions []any) map[string]string {
	result := make(map[string]string)
	for _, c := range conditions {
		cm, ok := c.(map[string]any)
		if !ok {
			continue
		}
		cType, _ := cm["type"].(string)
		cStatus, _ := cm["status"].(string)
		cMessage, _ := cm["message"].(string)
		if cType != "" {
			result[cType] = cStatus
			result[cType+"_message"] = cMessage
		}
	}
	return result
}
