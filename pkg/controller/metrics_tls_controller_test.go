package controller

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"testing"
	"time"

	configv1 "github.com/openshift/api/config/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/kubevirt/virt-platform-autopilot/pkg/metricstls"
	"github.com/kubevirt/virt-platform-autopilot/pkg/tlsprofile"
)

func TestMetricsTLSReconcilerNotifiesAfterProfileRefresh(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	scheme.AddKnownTypeWithName(tlsprofile.APIServerGVK, &unstructured.Unstructured{})

	apiServer := &unstructured.Unstructured{Object: map[string]any{
		"spec": map[string]any{
			"tlsSecurityProfile": map[string]any{"type": "Modern"},
		},
	}}
	apiServer.SetGroupVersionKind(tlsprofile.APIServerGVK)
	apiServer.SetName("cluster")
	clientCA := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Namespace: metricstls.ClientCAConfigMapNamespace, Name: metricstls.ClientCAConfigMapName},
		Data:       map[string]string{metricstls.ClientCAConfigMapKey: testCAPEM(t)},
	}
	client := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(apiServer, clientCA).Build()
	events := make(chan event.GenericEvent, 1)
	reconciler := NewMetricsTLSReconciler(client, &metricstls.ClientCAPool{}, true, events)

	if _, err := reconciler.Reconcile(context.Background(), reconcile.Request{}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-events:
		// The event is emitted after RefreshAPIServer has committed the cache.
		_, minVersion := tlsprofile.CipherSuitesAndMinTLSVersion(nil)
		if minVersion != configv1.VersionTLS13 {
			t.Fatalf("cached minimum TLS version = %q, want %q", minVersion, configv1.VersionTLS13)
		}
	default:
		t.Fatal("expected TLS profile change event")
	}

	// A reconciliation with the same source profile must not roll KME again.
	if _, err := reconciler.Reconcile(context.Background(), reconcile.Request{}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-events:
		t.Fatal("unexpected event for unchanged TLS profile")
	default:
	}
}

func testCAPEM(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
	}, &x509.Certificate{IsCA: true}, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}
