package engine

import (
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
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
	renderer.objectReader = loggingFailingReader{Reader: reader, operation: "get"}
	for _, testCase := range []struct {
		name, field, want, wantError string
	}{
		{"reads cached field without querying API reader", "spec.value", "cached", ""},
		{"missing field", "spec.missing", "", ""},
		{"non-string field fails", "spec.count", "", "field spec.count"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			value, err := renderer.objectField("example.com/v1", "Example", "test", "instance", testCase.field)
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
	_, err := renderer.objectField("example.com/v1", "Example", "test", "missing", "spec.value")
	if err == nil || !strings.Contains(err.Error(), "read denied") {
		t.Fatalf("uncached fallback failure must be reported: %v", err)
	}
}
