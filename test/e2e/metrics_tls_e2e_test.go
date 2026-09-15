package e2e

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
)

const (
	metricsServiceName           = "virt-platform-autopilot-metrics"
	metricsServingCertAnnotation = "service.beta.openshift.io/serving-cert-secret-name"
	metricsPortNumber            = int32(8443)
)

// These tests validate that the operator metrics endpoint is served over HTTPS
// with native mTLS (CNV-96287): only clients presenting the in-cluster Prometheus
// client certificate may scrape it. The positive mTLS path is exercised on both
// Kind and OpenShift (via a Prometheus-equivalent client cert over port-forward);
// the OpenShift-only checks additionally confirm the service-ca serving cert and
// that the real in-cluster Prometheus scrapes the target successfully.
var _ = Describe("Metrics HTTPS/mTLS endpoint (CNV-96287)", Ordered, func() {
	BeforeAll(func() {
		ensureHCOExists()
		patchAutopilotAndWait(autopilotEnabled)
	})

	Context("mTLS enforcement", func() {
		It("serves metrics over HTTPS to a client presenting the Prometheus client certificate", func() {
			Eventually(fetchMetricsBody, timeout, interval).
				Should(ContainSubstring("kubevirt_autopilot_"),
					"the mTLS metrics endpoint should return operator metrics to an authorized client")
		})

		It("rejects scrape attempts that present no client certificate", func() {
			localPort, err := ensureMetricsPortForward()
			Expect(err).NotTo(HaveOccurred())

			// No client certificate: RequireAndVerifyClientCert must abort the handshake.
			noCertClient := &http.Client{
				Timeout: 15 * time.Second,
				Transport: &http.Transport{
					TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
				},
			}
			_, err = noCertClient.Get(fmt.Sprintf("https://127.0.0.1:%d/metrics", localPort))
			Expect(err).To(HaveOccurred(),
				"server must require a client certificate (RequireAndVerifyClientCert)")
		})
	})

	Context("OpenShift service-ca and Prometheus integration", func() {
		BeforeEach(func() {
			if !isOpenShiftCluster() {
				Skip("service-ca and Prometheus integration only run on OpenShift")
			}
		})

		It("exposes the metrics Service on HTTPS port 8443 with a service-ca serving-cert annotation", func() {
			svc := &corev1.Service{}
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{
					Name: metricsServiceName, Namespace: operatorNamespace,
				}, svc)
			}, timeout, interval).Should(Succeed(), "metrics Service should exist")

			Expect(svc.Annotations).To(HaveKeyWithValue(metricsServingCertAnnotation, metricsServingCertSecret),
				"metrics Service should request a service-ca serving certificate")

			var hasPort bool
			for _, p := range svc.Spec.Ports {
				if p.Port == metricsPortNumber {
					hasPort = true
				}
			}
			Expect(hasPort).To(BeTrue(), "metrics Service should expose port 8443")
		})

		It("has the service-ca serving certificate secret minted", func() {
			secret := &corev1.Secret{}
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{
					Name: metricsServingCertSecret, Namespace: operatorNamespace,
				}, secret)
			}, timeout, interval).Should(Succeed(),
				"service-ca should mint the serving cert secret once the metrics Service exists")
			Expect(secret.Data).To(HaveKey("tls.crt"))
			Expect(secret.Data).To(HaveKey("tls.key"))
		})

		It("is scraped successfully by in-cluster Prometheus over mTLS (up == 1)", func() {
			// A successful scrape is the end-to-end proof of the mTLS path: Prometheus
			// presents its client cert, the operator verifies it against the cluster
			// CA and authorizes the prometheus-k8s CommonName. If mTLS were broken the
			// target would report up == 0.
			Eventually(func() (float64, error) {
				return queryPrometheusScalar(fmt.Sprintf(
					`up{namespace=%q,service=%q}`, operatorNamespace, metricsServiceName))
			}, 5*time.Minute, 10*time.Second).Should(Equal(1.0),
				"Prometheus target for the metrics endpoint should be UP")
		})

		It("ingests operator metrics scraped over the mTLS endpoint", func() {
			Eventually(func() (float64, error) {
				return queryPrometheusScalar(`count(kubevirt_autopilot_compliance_status)`)
			}, 5*time.Minute, 10*time.Second).Should(BeNumerically(">", 0),
				"operator metrics scraped over mTLS should be queryable in Prometheus")
		})
	})
})
