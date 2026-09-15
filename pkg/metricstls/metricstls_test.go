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

package metricstls

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-logr/logr"
)

func selfSignedCAPEM(t *testing.T) []byte {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("key gen: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func TestClientCAPool(t *testing.T) {
	pool := &ClientCAPool{}
	if pool.Get() != nil {
		t.Fatal("expected nil pool before Set")
	}

	if err := pool.Set([]byte("not a certificate")); err == nil {
		t.Fatal("expected error for invalid PEM")
	}
	if pool.Get() != nil {
		t.Fatal("pool should remain nil after failed Set")
	}

	if err := pool.Set(selfSignedCAPEM(t)); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if pool.Get() == nil {
		t.Fatal("expected non-nil pool after successful Set")
	}
}

func TestConfigureServerTLSInstallsClientAuth(t *testing.T) {
	pool := &ClientCAPool{}
	if err := pool.Set(selfSignedCAPEM(t)); err != nil {
		t.Fatalf("Set: %v", err)
	}

	cfg := &tls.Config{}
	ConfigureServerTLS(pool.Get)(cfg)
	if cfg.GetConfigForClient == nil {
		t.Fatal("GetConfigForClient not installed")
	}
	got, err := cfg.GetConfigForClient(nil)
	if err != nil {
		t.Fatalf("GetConfigForClient: %v", err)
	}
	if got.ClientAuth != tls.RequireAndVerifyClientCert {
		t.Errorf("ClientAuth: got %v want RequireAndVerifyClientCert", got.ClientAuth)
	}
	if got.ClientCAs == nil {
		t.Error("expected ClientCAs to be set from the pool")
	}
	if got.MinVersion == 0 {
		t.Error("expected MinVersion to be set from the resolved profile")
	}
}

func TestAllowPrometheusK8s(t *testing.T) {
	filter, err := AllowPrometheusK8s(nil, nil)
	if err != nil {
		t.Fatalf("AllowPrometheusK8s: %v", err)
	}

	var served bool
	inner := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { served = true })
	handler, err := filter(logr.Discard(), inner)
	if err != nil {
		t.Fatalf("filter: %v", err)
	}

	tests := []struct {
		name       string
		peerCN     string // "" → no client cert
		wantStatus int
		wantServed bool
	}{
		{"no client cert", "", http.StatusUnauthorized, false},
		{"wrong CN", "system:serviceaccount:other:sa", http.StatusForbidden, false},
		{"prometheus-k8s", PrometheusK8sCN, http.StatusOK, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			served = false
			req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
			if tc.peerCN != "" {
				req.TLS = &tls.ConnectionState{
					PeerCertificates: []*x509.Certificate{
						{Subject: pkix.Name{CommonName: tc.peerCN}},
					},
				}
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Errorf("status: got %d want %d", rec.Code, tc.wantStatus)
			}
			if served != tc.wantServed {
				t.Errorf("served: got %v want %v", served, tc.wantServed)
			}
		})
	}
}
