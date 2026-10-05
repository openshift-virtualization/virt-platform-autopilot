package engine

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/kubevirt/virt-platform-autopilot/pkg/assets"
)

func TestObjectField(t *testing.T) {
	object := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "example.com/v1", "kind": "Example",
		"metadata": map[string]any{"name": "instance", "namespace": "test"},
		"spec":     map[string]any{"value": "cached", "count": int64(1)},
	}}
	reader := fake.NewClientBuilder().WithObjects(object).Build()
	renderer := NewRenderer(assets.NewLoader())
	renderer.SetClient(reader)
	for _, testCase := range []struct {
		name, field, want, wantError string
	}{
		{"reads string field", "spec.value", "cached", ""},
		{"missing field", "spec.missing", "", ""},
		{"non-string field fails", "spec.count", "", "field spec.count"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			value, err := renderer.objectField("example.com/v1", "Example", "test", "instance", strings.Split(testCase.field, ".")...)
			if testCase.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), testCase.wantError) {
					t.Fatalf("error = %v, want %q", err, testCase.wantError)
				}
				return
			}
			if err != nil || value != testCase.want {
				t.Fatalf("value = %q, error = %v; want %q", value, err, testCase.want)
			}
		})
	}
	value, err := renderer.objectField("example.com/v1", "Example", "test", "missing", "spec", "value")
	if err != nil || value != "" {
		t.Fatalf("missing object: value = %q, error = %v", value, err)
	}
}

// Use controller-runtime's real delegating client: fake clients alone do not
// exercise the distinction between unstructured API reads and typed cache reads.
func TestLoggingStorageWithDelegatingClient(t *testing.T) {
	var requests atomic.Int32
	var missing atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if request.URL.Path != "/apis/loki.grafana.com/v1/namespaces/openshift-logging/lokistacks/logging-loki" {
			t.Errorf("unexpected API request: %s", request.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if missing.Load() {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"apiVersion":"v1","kind":"Status","status":"Failure","reason":"NotFound","code":404}`))
			return
		}
		if err := json.NewEncoder(w).Encode(loggingTestStack("existing-class").Object); err != nil {
			t.Errorf("encode LokiStack: %v", err)
		}
	}))
	defer server.Close()

	scheme := runtime.NewScheme()
	if err := storagev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	mapper := meta.NewDefaultRESTMapper([]schema.GroupVersion{storagev1.SchemeGroupVersion, {Group: "loki.grafana.com", Version: "v1"}})
	mapper.Add(storagev1.SchemeGroupVersion.WithKind("StorageClass"), meta.RESTScopeRoot)
	mapper.Add(schema.GroupVersionKind{Group: "loki.grafana.com", Version: "v1", Kind: "LokiStack"}, meta.RESTScopeNamespace)
	cached := fake.NewClientBuilder().WithScheme(scheme).WithObjects(loggingTestStorageClass("new-default", true, 1)).Build()
	reader, err := client.New(&rest.Config{Host: server.URL}, client.Options{
		Scheme: scheme, Mapper: mapper,
		Cache: &client.CacheOptions{Reader: loggingFailingReader{Reader: cached, operation: "get"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	renderer := NewRenderer(assets.NewLoader())
	renderer.SetClient(reader)
	for _, testCase := range []struct {
		missing bool
		want    string
	}{
		{false, "existing-class"},
		{true, "new-default"},
	} {
		missing.Store(testCase.missing)
		rendered, err := renderLoggingStorage(renderer, "")
		if err != nil {
			t.Fatal(err)
		}
		class, _, err := unstructured.NestedString(rendered.Object, "spec", "storageClassName")
		if err != nil || class != testCase.want {
			t.Fatalf("class = %q, error = %v; want %q", class, err, testCase.want)
		}
	}
	if requests.Load() != 2 {
		t.Fatalf("API requests = %d; want one LokiStack read per render, with no fallback or StorageClass API list", requests.Load())
	}
}

func TestObjectFieldPropagatesReadErrors(t *testing.T) {
	renderer := NewRenderer(assets.NewLoader())
	renderer.SetClient(loggingFailingReader{Reader: fake.NewClientBuilder().Build(), operation: "get"})
	_, err := renderer.renderTemplate("config", `{{ objectField "v1" "ConfigMap" "openshift-monitoring" "cluster-monitoring-config" "data" "config.yaml" }}`, nil)
	if err == nil || !strings.Contains(err.Error(), "read ConfigMap openshift-monitoring/cluster-monitoring-config") {
		t.Fatalf("ConfigMap read failure must fail rendering: %v", err)
	}
}

func TestObjectFieldMissingAPIKind(t *testing.T) {
	reader, err := client.New(&rest.Config{Host: "http://unused.invalid"}, client.Options{
		Mapper: meta.NewDefaultRESTMapper([]schema.GroupVersion{{Group: "monitoring.coreos.com", Version: "v1"}}),
	})
	if err != nil {
		t.Fatal(err)
	}
	renderer := NewRenderer(assets.NewLoader())
	renderer.SetClient(reader)
	value, err := renderer.objectField("monitoring.coreos.com/v1", "PrometheusRule", "openshift-kube-descheduler-operator", "descheduler-rules", "metadata", "name")
	if err != nil || value != "" {
		t.Fatalf("missing API kind: value = %q, error = %v; want empty string and nil error", value, err)
	}
}
