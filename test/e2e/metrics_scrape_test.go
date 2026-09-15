package e2e

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"time"

	. "github.com/onsi/gomega"
	certificatesv1 "k8s.io/api/certificates/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"

	"github.com/kubevirt/virt-platform-autopilot/pkg/metricstls"
)

// The operator metrics endpoint is served over HTTPS with native mTLS on :8443
// (CNV-96287). The kube-apiserver pod proxy terminates TLS to the pod but does
// not present a metrics client certificate, so scraping through the proxy is
// rejected by RequireAndVerifyClientCert. These helpers instead port-forward to
// the pod and scrape with a client certificate whose CommonName matches the
// in-cluster Prometheus service account, exactly as the real scraper does.
//
//   - OpenShift: reuse the real prometheus-k8s client cert from the
//     openshift-monitoring/metrics-client-certs secret.
//   - Kind: mint a client cert via the Kubernetes CSR API (signed by the cluster
//     client CA that the operator already trusts) with the same CommonName.
//
// A single port-forward is cached for the whole suite and transparently
// re-established when the operator pod restarts.

const (
	metricsServingCertSecret = metricstls.ServingCertSecretName
	metricsClientCertsSecret = "metrics-client-certs"
	monitoringNamespace      = "openshift-monitoring"
)

var (
	metricsClientCertCache *tls.Certificate

	metricsPF     *portforward.PortForwarder
	metricsPFStop chan struct{}
	metricsPFPort int
	metricsPFPod  types.UID
)

// fetchMetricsBody returns the raw Prometheus exposition text from the operator's
// mTLS metrics endpoint. It transparently reconnects if the cached port-forward
// has been broken by a pod restart.
func fetchMetricsBody() string {
	body, err := scrapeMetricsOnce()
	if err != nil {
		// A dropped port-forward (e.g. the operator pod restarted) surfaces here;
		// tear it down and try once more against the current pod.
		resetMetricsPortForward()
		body, err = scrapeMetricsOnce()
	}
	ExpectWithOffset(2, err).NotTo(HaveOccurred(), "should scrape the mTLS metrics endpoint")
	return body
}

// scrapeMetricsOnce performs a single mTLS GET of /metrics over the cached
// port-forward, establishing the port-forward and client cert on first use.
func scrapeMetricsOnce() (string, error) {
	cert := metricsClientCert()
	localPort, err := ensureMetricsPortForward()
	if err != nil {
		return "", fmt.Errorf("port-forward to metrics endpoint: %w", err)
	}

	httpClient := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				Certificates: []tls.Certificate{*cert},
				// The serving cert is issued by service-ca (OCP) or a self-signed
				// test CA (Kind); the test does not need to verify the server, only
				// to prove the mTLS client-auth path works end to end.
				InsecureSkipVerify: true, //nolint:gosec
			},
		},
	}

	url := fmt.Sprintf("https://127.0.0.1:%d/metrics", localPort)
	resp, err := httpClient.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("metrics endpoint returned %d: %s", resp.StatusCode, string(body))
	}
	return string(body), nil
}

// metricsClientCert returns (and caches) a TLS client certificate whose
// CommonName is the in-cluster Prometheus service account, which the operator's
// authorization filter allows.
func metricsClientCert() *tls.Certificate {
	if metricsClientCertCache != nil {
		return metricsClientCertCache
	}

	var cert tls.Certificate
	if isOpenShiftCluster() {
		cert = readPrometheusClientCert()
	} else {
		cert = issueClientCertViaCSR()
	}
	metricsClientCertCache = &cert
	return metricsClientCertCache
}

// readPrometheusClientCert loads the real prometheus-k8s scraping client cert
// (OpenShift). Using it also validates that the operator authorizes the real
// scraper CommonName.
func readPrometheusClientCert() tls.Certificate {
	secret := &corev1.Secret{}
	ExpectWithOffset(2, k8sClient.Get(ctx, types.NamespacedName{
		Namespace: monitoringNamespace,
		Name:      metricsClientCertsSecret,
	}, secret)).To(Succeed(),
		fmt.Sprintf("secret %s/%s (prometheus metrics client cert) must exist", monitoringNamespace, metricsClientCertsSecret))

	cert, err := tls.X509KeyPair(secret.Data["tls.crt"], secret.Data["tls.key"])
	ExpectWithOffset(2, err).NotTo(HaveOccurred(), "prometheus metrics client cert should be a valid keypair")
	return cert
}

// issueClientCertViaCSR mints a client certificate through the Kubernetes CSR
// API (Kind). The kube-apiserver-client signer signs it with the cluster client
// CA, which is exactly the CA the operator seeds its client-CA pool from, and
// the CommonName is set to the Prometheus service account the operator allows.
func issueClientCertViaCSR() tls.Certificate {
	clientset, err := kubernetes.NewForConfig(cfg)
	ExpectWithOffset(2, err).NotTo(HaveOccurred())

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	ExpectWithOffset(2, err).NotTo(HaveOccurred())

	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: metricstls.PrometheusK8sCN},
	}, key)
	ExpectWithOffset(2, err).NotTo(HaveOccurred())
	csrPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})

	expiration := int32(3600)
	csr := &certificatesv1.CertificateSigningRequest{
		ObjectMeta: metav1.ObjectMeta{GenerateName: "vpa-metrics-e2e-"},
		Spec: certificatesv1.CertificateSigningRequestSpec{
			Request:           csrPEM,
			SignerName:        certificatesv1.KubeAPIServerClientSignerName,
			ExpirationSeconds: &expiration,
			Usages:            []certificatesv1.KeyUsage{certificatesv1.UsageClientAuth},
		},
	}
	created, err := clientset.CertificatesV1().CertificateSigningRequests().Create(ctx, csr, metav1.CreateOptions{})
	ExpectWithOffset(2, err).NotTo(HaveOccurred(), "should create CSR for metrics client cert")

	// Clean up the CSR object once we have the signed certificate.
	defer func() {
		_ = clientset.CertificatesV1().CertificateSigningRequests().Delete(ctx, created.Name, metav1.DeleteOptions{})
	}()

	created.Status.Conditions = append(created.Status.Conditions, certificatesv1.CertificateSigningRequestCondition{
		Type:    certificatesv1.CertificateApproved,
		Status:  corev1.ConditionTrue,
		Reason:  "E2EMetricsTest",
		Message: "Approved by virt-platform-autopilot e2e suite",
	})
	_, err = clientset.CertificatesV1().CertificateSigningRequests().
		UpdateApproval(ctx, created.Name, created, metav1.UpdateOptions{})
	ExpectWithOffset(2, err).NotTo(HaveOccurred(), "should approve metrics client CSR")

	var certPEM []byte
	ExpectWithOffset(2, wait.PollUntilContextTimeout(ctx, 500*time.Millisecond, 30*time.Second, true,
		func(pollCtx context.Context) (bool, error) {
			issued, getErr := clientset.CertificatesV1().CertificateSigningRequests().
				Get(pollCtx, created.Name, metav1.GetOptions{})
			if getErr != nil {
				return false, getErr
			}
			if len(issued.Status.Certificate) == 0 {
				return false, nil
			}
			certPEM = issued.Status.Certificate
			return true, nil
		})).To(Succeed(), "CSR should be signed within timeout")

	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	ExpectWithOffset(2, err).NotTo(HaveOccurred(), "issued client cert should be a valid keypair")
	return cert
}

// ensureMetricsPortForward returns a local port forwarded to the operator pod's
// 8443 metrics port, (re)establishing the forward if the target pod changed.
func ensureMetricsPortForward() (int, error) {
	pod := getOperatorPod()
	if metricsPF != nil && metricsPFPod == pod.UID {
		return metricsPFPort, nil
	}
	resetMetricsPortForward()

	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return 0, err
	}
	roundTripper, upgrader, err := spdy.RoundTripperFor(cfg)
	if err != nil {
		return 0, err
	}

	req := clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Namespace(operatorNamespace).
		Name(pod.Name).
		SubResource("portforward")
	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: roundTripper}, http.MethodPost, req.URL())

	stopCh := make(chan struct{})
	readyCh := make(chan struct{})
	fw, err := portforward.NewOnAddresses(dialer,
		[]string{"127.0.0.1"}, []string{"0:8443"},
		stopCh, readyCh, io.Discard, io.Discard)
	if err != nil {
		close(stopCh)
		return 0, err
	}

	errCh := make(chan error, 1)
	go func() { errCh <- fw.ForwardPorts() }()

	select {
	case <-readyCh:
	case err := <-errCh:
		close(stopCh)
		return 0, fmt.Errorf("port-forward failed: %w", err)
	case <-time.After(30 * time.Second):
		close(stopCh)
		return 0, fmt.Errorf("timed out establishing port-forward to %s", pod.Name)
	}

	ports, err := fw.GetPorts()
	if err != nil || len(ports) == 0 {
		close(stopCh)
		return 0, fmt.Errorf("could not resolve forwarded port: %w", err)
	}

	metricsPF = fw
	metricsPFStop = stopCh
	metricsPFPort = int(ports[0].Local)
	metricsPFPod = pod.UID
	return metricsPFPort, nil
}

// resetMetricsPortForward tears down the cached port-forward, if any.
func resetMetricsPortForward() {
	if metricsPFStop != nil {
		close(metricsPFStop)
	}
	metricsPF = nil
	metricsPFStop = nil
	metricsPFPort = 0
	metricsPFPod = ""
}
