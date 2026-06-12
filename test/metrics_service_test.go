package test

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kubevirt/virt-platform-autopilot/pkg/assets"
	pkgcontext "github.com/kubevirt/virt-platform-autopilot/pkg/context"
	"github.com/kubevirt/virt-platform-autopilot/pkg/engine"
)

var _ = Describe("Metrics Service", func() {
	var serviceObj *unstructured.Unstructured
	var renderer *engine.Renderer

	BeforeEach(func() {
		loader := assets.NewLoader()
		registry, err := assets.NewRegistry(loader)
		Expect(err).NotTo(HaveOccurred())

		renderer = engine.NewRenderer(loader)
		renderer.SetClient(k8sClient)

		assetMeta, err := registry.GetAsset("metrics-service")
		Expect(err).NotTo(HaveOccurred())
		Expect(assetMeta).NotTo(BeNil())

		renderCtx := &pkgcontext.RenderContext{
			HCO: pkgcontext.NewMockHCO("kubevirt-hyperconverged", "openshift-cnv"),
		}

		rendered, err := renderer.RenderAsset(assetMeta, renderCtx)
		Expect(err).NotTo(HaveOccurred())

		serviceObj = rendered
	})

	It("should render a valid Service resource", func() {
		Expect(serviceObj.GetKind()).To(Equal("Service"))
		Expect(serviceObj.GetAPIVersion()).To(Equal("v1"))
		Expect(serviceObj.GetName()).To(Equal("virt-platform-autopilot-metrics"))
		Expect(serviceObj.GetNamespace()).To(Equal("openshift-cnv"))
	})

	It("should have labels for ServiceMonitor discovery", func() {
		labels := serviceObj.GetLabels()
		Expect(labels).To(HaveKeyWithValue("app.kubernetes.io/name", "virt-platform-autopilot"))
		Expect(labels).To(HaveKeyWithValue("app.kubernetes.io/component", "autopilot"))
		Expect(labels).To(HaveKeyWithValue("app", "virt-platform-autopilot"))
	})

	It("should select operator pods", func() {
		selector, found, err := unstructured.NestedStringMap(serviceObj.Object, "spec", "selector")
		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeTrue())
		Expect(selector).To(HaveKeyWithValue("app", "virt-platform-autopilot"))
		Expect(selector).To(HaveKeyWithValue("control-plane", "controller-manager"))
	})

	It("should expose metrics port 8080", func() {
		ports, found, err := unstructured.NestedSlice(serviceObj.Object, "spec", "ports")
		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeTrue())
		Expect(ports).To(HaveLen(1))

		port := ports[0].(map[string]any)
		Expect(port["name"]).To(Equal("metrics"))
		Expect(port["port"]).To(BeNumerically("==", 8080))
		Expect(port["targetPort"]).To(BeNumerically("==", 8080))
		Expect(port["protocol"]).To(Equal("TCP"))
	})

	It("should be accepted by Kubernetes API", func() {
		testNS := fmt.Sprintf("test-svc-%s", randString())
		ns := &unstructured.Unstructured{}
		ns.SetGroupVersionKind(nsGVK)
		ns.SetName(testNS)
		Expect(k8sClient.Create(ctx, ns)).To(Succeed())

		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, ns)
		})

		obj := serviceObj.DeepCopy()
		obj.SetNamespace(testNS)
		Expect(k8sClient.Create(ctx, obj)).To(Succeed())

		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, obj)
		})

		created := &unstructured.Unstructured{}
		created.SetGroupVersionKind(obj.GroupVersionKind())
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(obj), created)).To(Succeed())
		Expect(created.GetName()).To(Equal("virt-platform-autopilot-metrics"))
	})
})

var _ = Describe("Metrics ServiceMonitor", Ordered, func() {
	var serviceMonitorObj *unstructured.Unstructured
	var renderer *engine.Renderer

	BeforeAll(func() {
		// Install Prometheus CRDs once for the entire block (idempotent).
		err := InstallCRDs(ctx, k8sClient, CRDSetPrometheus)
		Expect(err).NotTo(HaveOccurred())
	})

	AfterAll(func() {
		_ = UninstallCRDs(ctx, k8sClient, CRDSetPrometheus)
	})

	BeforeEach(func() {
		loader := assets.NewLoader()
		registry, err := assets.NewRegistry(loader)
		Expect(err).NotTo(HaveOccurred())

		renderer = engine.NewRenderer(loader)
		renderer.SetClient(k8sClient)

		assetMeta, err := registry.GetAsset("metrics-servicemonitor")
		Expect(err).NotTo(HaveOccurred())
		Expect(assetMeta).NotTo(BeNil())

		renderCtx := &pkgcontext.RenderContext{
			HCO: pkgcontext.NewMockHCO("kubevirt-hyperconverged", "openshift-cnv"),
		}

		rendered, err := renderer.RenderAsset(assetMeta, renderCtx)
		Expect(err).NotTo(HaveOccurred())

		serviceMonitorObj = rendered
	})

	It("should render a valid ServiceMonitor resource", func() {
		Expect(serviceMonitorObj.GetKind()).To(Equal("ServiceMonitor"))
		Expect(serviceMonitorObj.GetAPIVersion()).To(Equal("monitoring.coreos.com/v1"))
		Expect(serviceMonitorObj.GetName()).To(Equal("virt-platform-autopilot-metrics"))
		Expect(serviceMonitorObj.GetNamespace()).To(Equal("openshift-cnv"))
	})

	It("should have correct labels", func() {
		labels := serviceMonitorObj.GetLabels()
		Expect(labels).To(HaveKeyWithValue("app.kubernetes.io/name", "virt-platform-autopilot"))
		Expect(labels).To(HaveKeyWithValue("app.kubernetes.io/component", "autopilot"))
		Expect(labels).To(HaveKeyWithValue("app", "virt-platform-autopilot"))
	})

	It("should select Service by labels", func() {
		matchLabels, found, err := unstructured.NestedStringMap(serviceMonitorObj.Object, "spec", "selector", "matchLabels")
		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeTrue())
		Expect(matchLabels).To(HaveKeyWithValue("app.kubernetes.io/name", "virt-platform-autopilot"))
		Expect(matchLabels).To(HaveKeyWithValue("app.kubernetes.io/component", "autopilot"))
	})

	It("should scrape metrics endpoint correctly", func() {
		endpoints, found, err := unstructured.NestedSlice(serviceMonitorObj.Object, "spec", "endpoints")
		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeTrue())
		Expect(endpoints).To(HaveLen(1))

		endpoint := endpoints[0].(map[string]any)
		Expect(endpoint["port"]).To(Equal("metrics"))
		Expect(endpoint["interval"]).To(Equal("30s"))
		Expect(endpoint["path"]).To(Equal("/metrics"))
	})

	It("should be accepted by Kubernetes API (CRD validation)", func() {
		gvr := schema.GroupVersionResource{
			Group:    "monitoring.coreos.com",
			Version:  "v1",
			Resource: "servicemonitors",
		}
		// Use the dynamic client (no REST mapper caching) to bypass any stale
		// negative-cache entries accumulated from CRD churn in other tests.
		dynClient, err := dynamic.NewForConfig(cfg)
		Expect(err).NotTo(HaveOccurred())

		testNS := fmt.Sprintf("test-sm-%s", randString())
		ns := &unstructured.Unstructured{}
		ns.SetGroupVersionKind(nsGVK)
		ns.SetName(testNS)
		Expect(k8sClient.Create(ctx, ns)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, ns) })

		obj := serviceMonitorObj.DeepCopy()
		obj.SetNamespace(testNS)

		var created *unstructured.Unstructured
		Eventually(func() error {
			var createErr error
			created, createErr = dynClient.Resource(gvr).Namespace(testNS).Create(ctx, obj.DeepCopy(), metav1.CreateOptions{})
			if apierrors.IsAlreadyExists(createErr) {
				created, createErr = dynClient.Resource(gvr).Namespace(testNS).Get(ctx, obj.GetName(), metav1.GetOptions{})
			}
			return createErr
		}).Should(Succeed())
		DeferCleanup(func() {
			_ = dynClient.Resource(gvr).Namespace(testNS).Delete(ctx, obj.GetName(), metav1.DeleteOptions{})
		})
		Expect(created.GetName()).To(Equal("virt-platform-autopilot-metrics"))
	})
})
