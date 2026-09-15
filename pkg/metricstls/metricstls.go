/*
Copyright 2026 The KubeVirt Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package metricstls provides the native (no kube-rbac-proxy sidecar) mTLS
// building blocks for the operator's HTTPS metrics endpoint:
//
//   - a hot-swappable client-CA pool used to verify in-cluster Prometheus's
//     client certificate (tls.RequireAndVerifyClientCert),
//   - ConfigureServerTLS, a single controller-runtime TLSOpts mutator that wires
//     the resolved TLS security profile (via pkg/tlsprofile) together with client
//     certificate verification, and
//   - AllowPrometheusK8s, a metrics-server FilterProvider that authorizes only
//     the in-cluster Prometheus service account by client-certificate CN.
//
// This mirrors the approach OpenShift's cluster-version-operator uses to serve
// metrics natively over mTLS.
package metricstls

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"sync/atomic"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/kubevirt/virt-platform-autopilot/pkg/tlsprofile"
)

const (
	// ServingCertDir is where the service-ca serving-cert secret is mounted into
	// the operator pod. controller-runtime's certwatcher reads tls.crt/tls.key
	// from here and hot-reloads them on service-ca rotation.
	ServingCertDir = "/etc/tls/private"

	// ServingCertSecretName is the secret requested from the OpenShift service-ca
	// operator via the metrics Service annotation
	// service.beta.openshift.io/serving-cert-secret-name.
	ServingCertSecretName = "virt-platform-autopilot-metrics-tls"

	// The authoritative client CA used to verify metrics scrapers lives in the
	// kube-system extension-apiserver-authentication ConfigMap under
	// client-ca-file. (metrics-client-ca in openshift-monitoring is a
	// CMO-derived copy of the same CA.)
	ClientCAConfigMapNamespace = "kube-system"
	ClientCAConfigMapName      = "extension-apiserver-authentication"
	ClientCAConfigMapKey       = "client-ca-file"

	// PrometheusK8sCN is the Common Name of the client certificate presented by
	// the in-cluster Prometheus (prometheus-k8s) when it scrapes metrics.
	PrometheusK8sCN = "system:serviceaccount:openshift-monitoring:prometheus-k8s"
)

// ClientCAPool holds the CA pool used to verify metrics client certificates.
// It is safe for concurrent use: the TLS server reads it once per new connection
// while a watch/reconciler swaps it atomically on ConfigMap changes.
type ClientCAPool struct {
	pool atomic.Pointer[x509.CertPool]
}

// Get returns the current pool, or nil if none has been loaded yet.
func (c *ClientCAPool) Get() *x509.CertPool {
	return c.pool.Load()
}

// Set parses the PEM-encoded CA bundle and atomically replaces the pool.
// It returns an error (leaving the previous pool intact) if no certificate can
// be parsed, so a malformed update never drops verification to an empty pool.
func (c *ClientCAPool) Set(caPEM []byte) error {
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return fmt.Errorf("no valid certificates found in client CA bundle")
	}
	c.pool.Store(pool)
	return nil
}

// RefreshClientCA reads the authoritative metrics client CA from the kube-system
// extension-apiserver-authentication ConfigMap and atomically updates the pool.
// It is used both to seed the pool at startup and to refresh it when a watch
// observes the ConfigMap change (CA rotation). Reads should use an authoritative
// (uncached) reader so a stale cache never narrows the trusted client set.
func RefreshClientCA(ctx context.Context, c client.Reader, pool *ClientCAPool) error {
	cm := &corev1.ConfigMap{}
	key := types.NamespacedName{
		Namespace: ClientCAConfigMapNamespace,
		Name:      ClientCAConfigMapName,
	}
	if err := c.Get(ctx, key, cm); err != nil {
		return err
	}
	caPEM, ok := cm.Data[ClientCAConfigMapKey]
	if !ok || caPEM == "" {
		return fmt.Errorf("configmap %s missing key %q", key, ClientCAConfigMapKey)
	}
	return pool.Set([]byte(caPEM))
}

// ConfigureServerTLS returns a controller-runtime metrics-server TLSOpts mutator
// that installs a single GetConfigForClient. On every new connection it applies:
//
//   - MinVersion and (below TLS 1.3) CipherSuites from the resolved TLS security
//     profile, so cluster TLS-policy changes take effect without a restart;
//   - ClientAuth = RequireAndVerifyClientCert with ClientCAs from getPool(), so
//     only holders of a cluster-issued client certificate can connect.
//
// It must be the only mutator that sets GetConfigForClient (a second would
// overwrite it). The cloned config inherits the certwatcher's GetCertificate,
// which controller-runtime sets on the same tls.Config after the mutators run.
func ConfigureServerTLS(getPool func() *x509.CertPool) func(*tls.Config) {
	return func(cfg *tls.Config) {
		base := cfg
		cfg.GetConfigForClient = func(*tls.ClientHelloInfo) (*tls.Config, error) {
			ciphers, minVersion := tlsprofile.ResolvedGoConfig()
			c := base.Clone()
			c.MinVersion = minVersion
			if minVersion < tls.VersionTLS13 {
				c.CipherSuites = ciphers
			}
			c.ClientAuth = tls.RequireAndVerifyClientCert
			c.ClientCAs = getPool()
			return c, nil
		}
	}
}

// AllowPrometheusK8s is a metrics-server FilterProvider that authorizes requests
// only from the in-cluster Prometheus service account, identified by its
// client-certificate CN. The certificate itself is already cryptographically
// verified by RequireAndVerifyClientCert; this adds application-layer authz so
// that any other cluster-trusted client is still rejected.
func AllowPrometheusK8s(_ *rest.Config, _ *http.Client) (metricsserver.Filter, error) {
	return func(_ logr.Logger, handler http.Handler) (http.Handler, error) {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if req.TLS == nil || len(req.TLS.PeerCertificates) == 0 {
				http.Error(w, "client certificate required", http.StatusUnauthorized)
				return
			}
			if cn := req.TLS.PeerCertificates[0].Subject.CommonName; cn != PrometheusK8sCN {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			handler.ServeHTTP(w, req)
		}), nil
	}, nil
}
