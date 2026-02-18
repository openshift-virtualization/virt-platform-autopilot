package test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kubevirt/virt-platform-autopilot/pkg/assets"
	pkgcontext "github.com/kubevirt/virt-platform-autopilot/pkg/context"
	"github.com/kubevirt/virt-platform-autopilot/pkg/engine"
)

var _ = Describe("Prometheus Alert Rules", func() {
	var prometheusRuleObj *unstructured.Unstructured
	var renderer *engine.Renderer

	BeforeEach(func() {
		// Install PrometheusRule CRD for validation
		err := InstallCRDs(ctx, k8sClient, CRDSetPrometheus)
		Expect(err).NotTo(HaveOccurred())

		// Cleanup after test
		DeferCleanup(func() {
			_ = UninstallCRDs(ctx, k8sClient, CRDSetPrometheus)
		})

		// Initialize renderer to actually process the template
		loader := assets.NewLoader()
		registry, err := assets.NewRegistry(loader)
		Expect(err).NotTo(HaveOccurred(), "Should create asset registry")

		renderer = engine.NewRenderer(loader)
		renderer.SetClient(k8sClient)

		// Get asset metadata from registry
		assetMeta, err := registry.GetAsset("prometheus-alerts")
		Expect(err).NotTo(HaveOccurred(), "Should find prometheus-alerts asset in registry")
		Expect(assetMeta).NotTo(BeNil())

		// Render the template through the engine (this catches template syntax errors)
		renderCtx := &pkgcontext.RenderContext{
			HCO: pkgcontext.NewMockHCO("kubevirt-hyperconverged", "openshift-cnv"),
		}

		rendered, err := renderer.RenderAsset(assetMeta, renderCtx)
		Expect(err).NotTo(HaveOccurred(), "PrometheusRule template should render without errors")

		// The renderer returns an unstructured object
		prometheusRuleObj = rendered
	})

	It("should have valid PrometheusRule YAML syntax", func() {
		By("verifying it's a PrometheusRule resource")
		Expect(prometheusRuleObj.GetKind()).To(Equal("PrometheusRule"))
		Expect(prometheusRuleObj.GetAPIVersion()).To(Equal("monitoring.coreos.com/v1"))
		Expect(prometheusRuleObj.GetName()).To(Equal("virt-platform-autopilot-alerts"))
		Expect(prometheusRuleObj.GetNamespace()).To(Equal("openshift-cnv"))
	})

	It("should contain all required alert rules", func() {
		By("extracting the spec.groups field")
		spec, found, err := unstructured.NestedFieldNoCopy(prometheusRuleObj.Object, "spec")
		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeTrue(), "spec field should exist")

		specMap, ok := spec.(map[string]interface{})
		Expect(ok).To(BeTrue(), "spec should be a map")

		groups, found, err := unstructured.NestedSlice(specMap, "groups")
		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeTrue(), "spec.groups should exist")
		Expect(groups).To(HaveLen(2), "should have 2 rule groups (critical + warning)")

		By("verifying critical alert group")
		criticalGroup := groups[0].(map[string]interface{})
		Expect(criticalGroup["name"]).To(Equal("virt-platform-autopilot.critical"))

		criticalRules := criticalGroup["rules"].([]interface{})
		Expect(criticalRules).To(HaveLen(1), "critical group should have 1 alert")

		syncFailedAlert := criticalRules[0].(map[string]interface{})
		Expect(syncFailedAlert["alert"]).To(Equal("VirtPlatformSyncFailed"))
		Expect(syncFailedAlert["expr"]).To(ContainSubstring("virt_platform_compliance_status == 0"))
		Expect(syncFailedAlert["for"]).To(Equal("15m"))

		labels := syncFailedAlert["labels"].(map[string]interface{})
		Expect(labels["severity"]).To(Equal("critical"))

		By("verifying warning alert group")
		warningGroup := groups[1].(map[string]interface{})
		Expect(warningGroup["name"]).To(Equal("virt-platform-autopilot.warning"))

		warningRules := warningGroup["rules"].([]interface{})
		Expect(warningRules).To(HaveLen(3), "warning group should have 3 alerts")

		// Verify thrashing alert
		thrashingAlert := warningRules[0].(map[string]interface{})
		Expect(thrashingAlert["alert"]).To(Equal("VirtPlatformThrashingDetected"))
		Expect(thrashingAlert["expr"]).To(ContainSubstring("virt_platform_paused_resources > 0"))

		// Verify dependency alert
		dependencyAlert := warningRules[1].(map[string]interface{})
		Expect(dependencyAlert["alert"]).To(Equal("VirtPlatformDependencyMissing"))
		Expect(dependencyAlert["expr"]).To(ContainSubstring("virt_platform_missing_dependency == 1"))
		Expect(dependencyAlert["for"]).To(Equal("5m"))

		// Verify tombstone alert
		tombstoneAlert := warningRules[2].(map[string]interface{})
		Expect(tombstoneAlert["alert"]).To(Equal("VirtPlatformTombstoneStuck"))
		Expect(tombstoneAlert["expr"]).To(ContainSubstring("virt_platform_tombstone_status < 0"))
		Expect(tombstoneAlert["for"]).To(Equal("30m"))
	})

	It("should have proper labels and annotations on all alerts", func() {
		By("extracting all rules from all groups")
		groups, _, _ := unstructured.NestedSlice(prometheusRuleObj.Object, "spec", "groups")

		allAlerts := []map[string]interface{}{}
		for _, group := range groups {
			groupMap := group.(map[string]interface{})
			rules := groupMap["rules"].([]interface{})
			for _, rule := range rules {
				allAlerts = append(allAlerts, rule.(map[string]interface{}))
			}
		}

		By("verifying each alert has required fields")
		for _, alert := range allAlerts {
			alertName := alert["alert"].(string)

			// Verify labels
			labels, labelsExist := alert["labels"].(map[string]interface{})
			Expect(labelsExist).To(BeTrue(), "Alert %s should have labels", alertName)
			Expect(labels["severity"]).ToNot(BeEmpty(), "Alert %s should have severity label", alertName)
			Expect(labels["operator"]).To(Equal("virt-platform-autopilot"), "Alert %s should have operator label", alertName)

			// Verify annotations
			annotations, annotationsExist := alert["annotations"].(map[string]interface{})
			Expect(annotationsExist).To(BeTrue(), "Alert %s should have annotations", alertName)
			Expect(annotations["summary"]).ToNot(BeEmpty(), "Alert %s should have summary annotation", alertName)
			Expect(annotations["description"]).ToNot(BeEmpty(), "Alert %s should have description annotation", alertName)
			Expect(annotations["runbook_url"]).To(ContainSubstring("github.com"), "Alert %s should have runbook_url", alertName)
		}
	})

	It("should be accepted by Kubernetes API (CRD validation)", func() {
		By("creating the openshift-cnv namespace")
		ns := &unstructured.Unstructured{}
		ns.SetGroupVersionKind(nsGVK)
		ns.SetName("openshift-cnv")
		err := k8sClient.Create(ctx, ns)
		Expect(err).NotTo(HaveOccurred())

		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, ns)
		})

		By("applying PrometheusRule to envtest cluster")
		// Use a deep copy to avoid modifying the shared cached object
		obj := prometheusRuleObj.DeepCopy()
		// This validates the YAML structure against the PrometheusRule CRD schema
		err = k8sClient.Create(ctx, obj)
		Expect(err).NotTo(HaveOccurred(), "PrometheusRule should pass CRD validation")

		// Cleanup
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, obj)
		})

		By("verifying the resource was created successfully")
		created := &unstructured.Unstructured{}
		created.SetGroupVersionKind(obj.GroupVersionKind())
		err = k8sClient.Get(ctx, client.ObjectKeyFromObject(obj), created)
		Expect(err).NotTo(HaveOccurred())
		Expect(created.GetName()).To(Equal("virt-platform-autopilot-alerts"))
	})

	It("should properly escape Prometheus template variables", func() {
		By("verifying annotations contain Prometheus template syntax (not Go template syntax)")
		groups, _, _ := unstructured.NestedSlice(prometheusRuleObj.Object, "spec", "groups")

		// Get VirtPlatformSyncFailed alert annotations
		criticalGroup := groups[0].(map[string]interface{})
		criticalRules := criticalGroup["rules"].([]interface{})
		syncFailedAlert := criticalRules[0].(map[string]interface{})
		annotations := syncFailedAlert["annotations"].(map[string]interface{})

		summary := annotations["summary"].(string)
		description := annotations["description"].(string)

		// Verify Prometheus template variables are present (not rendered by Go template)
		Expect(summary).To(ContainSubstring("{{ $labels.kind }}"), "Summary should contain Prometheus template variable for kind")
		Expect(summary).To(ContainSubstring("{{ $labels.name }}"), "Summary should contain Prometheus template variable for name")
		Expect(description).To(ContainSubstring("{{ $labels.namespace }}"), "Description should contain Prometheus template variable for namespace")
		Expect(description).To(ContainSubstring("{{ $value }}"), "Description should contain Prometheus template variable for value")

		By("verifying all alerts have properly escaped Prometheus variables")
		for _, group := range groups {
			groupMap := group.(map[string]interface{})
			rules := groupMap["rules"].([]interface{})
			for _, rule := range rules {
				alert := rule.(map[string]interface{})
				alertName := alert["alert"].(string)
				alertAnnotations := alert["annotations"].(map[string]interface{})

				alertSummary := alertAnnotations["summary"].(string)
				alertDescription := alertAnnotations["description"].(string)

				// All alerts should use Prometheus template variables, not Go template output
				Expect(alertSummary).To(MatchRegexp(`\{\{.*\$labels.*\}\}`),
					"Alert %s summary should contain Prometheus template variables", alertName)
				Expect(alertDescription).To(MatchRegexp(`\{\{.*\$labels.*\}\}`),
					"Alert %s description should contain Prometheus template variables", alertName)

				// Ensure no Go template artifacts (like <no value> or empty strings from failed rendering)
				Expect(alertSummary).NotTo(ContainSubstring("<no value>"),
					"Alert %s summary should not have Go template rendering errors", alertName)
				Expect(alertDescription).NotTo(ContainSubstring("<no value>"),
					"Alert %s description should not have Go template rendering errors", alertName)
			}
		}
	})

	It("should render successfully through the template engine", func() {
		By("verifying the template was processed by Go template engine")
		// This test ensures the template syntax is valid and rendering completes
		// The fact that prometheusRuleObj exists proves the template rendered successfully

		Expect(prometheusRuleObj).NotTo(BeNil(), "Template should render to a valid object")
		Expect(prometheusRuleObj.GetKind()).To(Equal("PrometheusRule"))

		By("verifying no template syntax errors occurred")
		// If there were template syntax errors, the BeforeEach would have failed
		// This test documents that expectation
		_, found, err := unstructured.NestedFieldNoCopy(prometheusRuleObj.Object, "spec", "groups")
		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeTrue(), "Template should render all fields correctly")
	})
})
