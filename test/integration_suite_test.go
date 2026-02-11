package test

import (
	"context"
	"math/rand"
	"path/filepath"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

var (
	cfg       *rest.Config
	k8sClient client.Client
	apiReader client.Reader // Direct API reader for adoption scenarios
	testEnv   *envtest.Environment
	ctx       context.Context
	cancel    context.CancelFunc

	// Common GVKs for tests
	nsGVK = schema.GroupVersionKind{
		Group:   "",
		Version: "v1",
		Kind:    "Namespace",
	}
)

func TestIntegration(t *testing.T) {
	RegisterFailHandler(Fail)

	// Set generous timeouts for CI environments with rate limiting
	// CRD operations can take a long time when rate limited
	SetDefaultEventuallyTimeout(2 * time.Minute)
	SetDefaultEventuallyPollingInterval(20 * time.Millisecond) // Fast polling
	SetDefaultConsistentlyDuration(5 * time.Second)
	SetDefaultConsistentlyPollingInterval(20 * time.Millisecond) // Fast polling

	RunSpecs(t, "Integration Test Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	ctx, cancel = context.WithCancel(context.Background())

	By("bootstrapping test environment")

	// Start with minimal CRDs (HCO only - the essential one)
	// Tests can dynamically add more CRDs using InstallCRDs helper
	testEnv = &envtest.Environment{
		CRDDirectoryPaths: []string{
			filepath.Join("..", "assets", "crds", "kubevirt"),
		},
		ErrorIfCRDPathMissing: true,
		// AttachControlPlaneOutput: true, // Uncomment for debugging
	}

	var err error
	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	// Increase rate limits for CI environments with heavy CRD operations
	// Default QPS=5, Burst=10 is too restrictive for tests that install multiple CRD sets
	// Tests like "invalidate all cache entries" install multiple CRDs sequentially,
	// each requiring ~300 API calls (deletion wait + establishment wait)
	// With multiple CRDs, this can easily exceed 1000 API calls in a short period
	// In envtest we control the API server so we can be very aggressive with limits
	cfg.QPS = 500
	cfg.Burst = 1000

	// Create client
	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())

	// In tests without a manager, k8sClient is already a direct client
	// Use it as the API reader for adoption scenarios
	apiReader = k8sClient

	// Wait for API server to be ready
	Eventually(func() error {
		namespaces := &unstructured.UnstructuredList{}
		namespaces.SetGroupVersionKind(schema.GroupVersionKind{
			Group:   "",
			Version: "v1",
			Kind:    "NamespaceList",
		})
		return k8sClient.List(ctx, namespaces)
	}, 10*time.Second, 250*time.Millisecond).Should(Succeed())
})

var _ = AfterSuite(func() {
	cancel()
	By("tearing down the test environment")
	err := testEnv.Stop()
	Expect(err).NotTo(HaveOccurred())
})

// Test helpers

// randString generates a random 5-character string for test namespaces
func randString() string {
	const charset = "abcdefghijklmnopqrstuvwxyz0123456789"
	const length = 5
	b := make([]byte, length)
	for i := range b {
		b[i] = charset[rand.Intn(len(charset))]
	}
	return string(b)
}
