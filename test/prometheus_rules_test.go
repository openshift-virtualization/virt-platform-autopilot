package test

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"
)

var _ = Describe("Prometheus Alert Rules", func() {
	BeforeEach(func() {
		// Install PrometheusRule CRD for validation
		err := InstallCRDs(ctx, k8sClient, CRDSetPrometheus)
		Expect(err).NotTo(HaveOccurred())

		// Cleanup after test
		DeferCleanup(func() {
			_ = UninstallCRDs(ctx, k8sClient, CRDSetPrometheus)
		})
	})

	It("should have valid PrometheusRule YAML syntax", func() {
		By("reading the PrometheusRule template")
		templatePath := filepath.Join("..", "assets", "observability", "prometheus-rules.yaml.tpl")
		data, err := os.ReadFile(templatePath)
		Expect(err).NotTo(HaveOccurred(), "PrometheusRule template should exist")

		By("parsing YAML to unstructured object")
		obj := &unstructured.Unstructured{}
		err = yaml.Unmarshal(data, obj)
		Expect(err).NotTo(HaveOccurred(), "PrometheusRule YAML should be valid")

		By("verifying it's a PrometheusRule resource")
		Expect(obj.GetKind()).To(Equal("PrometheusRule"))
		Expect(obj.GetAPIVersion()).To(Equal("monitoring.coreos.com/v1"))
		Expect(obj.GetName()).To(Equal("virt-platform-autopilot-alerts"))
		Expect(obj.GetNamespace()).To(Equal("openshift-cnv"))
	})

	It("should contain all required alert rules", func() {
		By("loading the PrometheusRule template")
		templatePath := filepath.Join("..", "assets", "observability", "prometheus-rules.yaml.tpl")
		data, err := os.ReadFile(templatePath)
		Expect(err).NotTo(HaveOccurred())

		obj := &unstructured.Unstructured{}
		err = yaml.Unmarshal(data, obj)
		Expect(err).NotTo(HaveOccurred())

		By("extracting the spec.groups field")
		spec, found, err := unstructured.NestedFieldNoCopy(obj.Object, "spec")
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
		Expect(warningRules).To(HaveLen(2), "warning group should have 2 alerts")

		// Verify thrashing alert
		thrashingAlert := warningRules[0].(map[string]interface{})
		Expect(thrashingAlert["alert"]).To(Equal("VirtPlatformThrashingDetected"))
		Expect(thrashingAlert["expr"]).To(ContainSubstring("increase(virt_platform_thrashing_total[10m]) > 5"))

		// Verify dependency alert
		dependencyAlert := warningRules[1].(map[string]interface{})
		Expect(dependencyAlert["alert"]).To(Equal("VirtPlatformDependencyMissing"))
		Expect(dependencyAlert["expr"]).To(ContainSubstring("virt_platform_missing_dependency == 1"))
		Expect(dependencyAlert["for"]).To(Equal("5m"))
	})

	It("should have proper labels and annotations on all alerts", func() {
		By("loading the PrometheusRule template")
		templatePath := filepath.Join("..", "assets", "observability", "prometheus-rules.yaml.tpl")
		data, err := os.ReadFile(templatePath)
		Expect(err).NotTo(HaveOccurred())

		obj := &unstructured.Unstructured{}
		err = yaml.Unmarshal(data, obj)
		Expect(err).NotTo(HaveOccurred())

		By("extracting all rules from all groups")
		groups, _, _ := unstructured.NestedSlice(obj.Object, "spec", "groups")

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

		By("loading the PrometheusRule template")
		templatePath := filepath.Join("..", "assets", "observability", "prometheus-rules.yaml.tpl")
		data, err := os.ReadFile(templatePath)
		Expect(err).NotTo(HaveOccurred())

		obj := &unstructured.Unstructured{}
		err = yaml.Unmarshal(data, obj)
		Expect(err).NotTo(HaveOccurred())

		By("applying PrometheusRule to envtest cluster")
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
})
