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

package engine

import (
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/kubevirt/virt-platform-autopilot/pkg/assets"
	pkgcontext "github.com/kubevirt/virt-platform-autopilot/pkg/context"
)

func TestDig(t *testing.T) {
	tests := []struct {
		name string
		keys []interface{}
		want interface{}
	}{
		{
			name: "access nested field successfully",
			keys: []interface{}{
				"spec",
				"replicas",
				"default",
				map[string]interface{}{
					"spec": map[string]interface{}{
						"replicas": int64(5),
					},
				},
			},
			want: int64(5),
		},
		{
			name: "field not found returns default",
			keys: []interface{}{
				"spec",
				"missing",
				"default-value",
				map[string]interface{}{
					"spec": map[string]interface{}{
						"replicas": int64(5),
					},
				},
			},
			want: "default-value",
		},
		{
			name: "deep nesting",
			keys: []interface{}{
				"spec",
				"template",
				"spec",
				"containers",
				99,
				map[string]interface{}{
					"spec": map[string]interface{}{
						"template": map[string]interface{}{
							"spec": map[string]interface{}{
								"containers": "found",
							},
						},
					},
				},
			},
			want: "found",
		},
		{
			name: "less than 2 arguments returns nil",
			keys: []interface{}{
				map[string]interface{}{},
			},
			want: nil,
		},
		{
			name: "non-string key returns default",
			keys: []interface{}{
				123, // non-string key
				"default",
				map[string]interface{}{
					"field": "value",
				},
			},
			want: "default",
		},
		{
			name: "non-map object returns default",
			keys: []interface{}{
				"field",
				"default",
				"not-a-map",
			},
			want: "default",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := dig(tt.keys...)
			if got != tt.want {
				t.Errorf("dig() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHas(t *testing.T) {
	tests := []struct {
		name     string
		needle   interface{}
		haystack interface{}
		want     bool
	}{
		{
			name:     "string in string slice",
			needle:   "value2",
			haystack: []string{"value1", "value2", "value3"},
			want:     true,
		},
		{
			name:     "string not in string slice",
			needle:   "missing",
			haystack: []string{"value1", "value2", "value3"},
			want:     false,
		},
		{
			name:     "value in interface slice",
			needle:   "test",
			haystack: []interface{}{"test", "other"},
			want:     true,
		},
		{
			name:     "value not in interface slice",
			needle:   "missing",
			haystack: []interface{}{"test", "other"},
			want:     false,
		},
		{
			name:     "non-string needle with string slice",
			needle:   123,
			haystack: []string{"value1", "value2"},
			want:     false,
		},
		{
			name:     "empty string slice",
			needle:   "value",
			haystack: []string{},
			want:     false,
		},
		{
			name:     "empty interface slice",
			needle:   "value",
			haystack: []interface{}{},
			want:     false,
		},
		{
			name:     "non-slice haystack",
			needle:   "value",
			haystack: "not-a-slice",
			want:     false,
		},
		{
			name:     "nil haystack",
			needle:   "value",
			haystack: nil,
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := has(tt.needle, tt.haystack)
			if got != tt.want {
				t.Errorf("has() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNewRenderer(t *testing.T) {
	loader := assets.NewLoader()
	renderer := NewRenderer(loader)

	if renderer == nil {
		t.Fatal("NewRenderer() returned nil")
	}
	if renderer.loader != loader {
		t.Error("NewRenderer() did not set loader correctly")
	}
}

func TestSafeFuncMap(t *testing.T) {
	funcMap := safeFuncMap()

	t.Run("includes safe string functions", func(t *testing.T) {
		assertFunctionsExist(t, funcMap, []string{"upper", "lower", "trim", "replace", "contains"})
	})

	t.Run("includes safe logic functions", func(t *testing.T) {
		assertFunctionsExist(t, funcMap, []string{"default", "empty", "ternary"})
	})

	t.Run("includes safe type conversion", func(t *testing.T) {
		assertFunctionsExist(t, funcMap, []string{"toString", "toJson", "fromJson"})

		// Verify at least some type conversion functions exist
		typeConvCount := 0
		for name := range funcMap {
			if strings.HasPrefix(name, "to") || strings.HasPrefix(name, "from") {
				typeConvCount++
			}
		}
		if typeConvCount < 5 {
			t.Errorf("Expected at least 5 type conversion functions, got %d", typeConvCount)
		}
	})

	t.Run("includes safe list functions", func(t *testing.T) {
		assertFunctionsExist(t, funcMap, []string{"list", "append", "first", "last", "reverse"})
	})

	t.Run("includes safe math functions", func(t *testing.T) {
		assertFunctionsExist(t, funcMap, []string{"add", "sub", "mul", "div", "max", "min"})
	})

	t.Run("excludes dangerous functions", func(t *testing.T) {
		assertFunctionsNotExist(t, funcMap, []string{"env", "expandenv", "genPrivateKey", "genCertificate", "now", "date", "randAlpha", "uuid"})
	})
}

// assertFunctionsExist checks that all expected functions are present in the funcMap
func assertFunctionsExist(t *testing.T, funcMap map[string]interface{}, expectedFuncs []string) {
	t.Helper()
	for _, name := range expectedFuncs {
		if _, exists := funcMap[name]; !exists {
			t.Errorf("safeFuncMap() missing safe function: %s", name)
		}
	}
}

// assertFunctionsNotExist checks that dangerous functions are not present in the funcMap
func assertFunctionsNotExist(t *testing.T, funcMap map[string]interface{}, dangerousFuncs []string) {
	t.Helper()
	for _, name := range dangerousFuncs {
		if _, exists := funcMap[name]; exists {
			t.Errorf("safeFuncMap() includes dangerous function: %s", name)
		}
	}
}

func TestCustomFuncMap(t *testing.T) {
	loader := assets.NewLoader()
	renderer := NewRenderer(loader)
	funcMap := renderer.customFuncMap()

	t.Run("includes dig function", func(t *testing.T) {
		if _, exists := funcMap["dig"]; !exists {
			t.Error("customFuncMap() missing 'dig' function")
		}
	})

	t.Run("includes has function", func(t *testing.T) {
		if _, exists := funcMap["has"]; !exists {
			t.Error("customFuncMap() missing 'has' function")
		}
	})

	t.Run("includes crdEnum function", func(t *testing.T) {
		if _, exists := funcMap["crdEnum"]; !exists {
			t.Error("customFuncMap() missing 'crdEnum' function")
		}
	})

	t.Run("includes crdHasEnum function", func(t *testing.T) {
		if _, exists := funcMap["crdHasEnum"]; !exists {
			t.Error("customFuncMap() missing 'crdHasEnum' function")
		}
	})

	t.Run("includes objectExists function", func(t *testing.T) {
		if _, exists := funcMap["objectExists"]; !exists {
			t.Error("customFuncMap() missing 'objectExists' function")
		}
	})

	t.Run("includes prometheusRuleHasRecordingRule function", func(t *testing.T) {
		if _, exists := funcMap["prometheusRuleHasRecordingRule"]; !exists {
			t.Error("customFuncMap() missing 'prometheusRuleHasRecordingRule' function")
		}
	})
}

func TestRenderTemplate(t *testing.T) {
	loader := assets.NewLoader()
	renderer := NewRenderer(loader)

	t.Run("renders simple template", func(t *testing.T) {
		ctx := &pkgcontext.RenderContext{
			HCO: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{
						"name": "test-hco",
					},
				},
			},
		}

		template := "name: {{ .HCO.Object.metadata.name }}"
		rendered, err := renderer.renderTemplate("test", template, ctx)
		if err != nil {
			t.Fatalf("renderTemplate() error = %v", err)
		}

		expected := "name: test-hco"
		if strings.TrimSpace(string(rendered)) != expected {
			t.Errorf("renderTemplate() = %q, want %q", string(rendered), expected)
		}
	})

	t.Run("uses safe functions", func(t *testing.T) {
		ctx := &pkgcontext.RenderContext{
			HCO: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{
						"name": "test",
					},
				},
			},
		}

		template := "name: {{ .HCO.Object.metadata.name | upper }}"
		rendered, err := renderer.renderTemplate("test", template, ctx)
		if err != nil {
			t.Fatalf("renderTemplate() error = %v", err)
		}

		expected := "name: TEST"
		if strings.TrimSpace(string(rendered)) != expected {
			t.Errorf("renderTemplate() = %q, want %q", string(rendered), expected)
		}
	})

	t.Run("uses custom dig function", func(t *testing.T) {
		hcoObj := map[string]interface{}{
			"spec": map[string]interface{}{
				"nested": map[string]interface{}{
					"field": "value",
				},
			},
		}

		ctx := &pkgcontext.RenderContext{
			HCO: &unstructured.Unstructured{
				Object: hcoObj,
			},
		}

		template := `value: {{ dig "spec" "nested" "field" "default" .HCO.Object }}`
		rendered, err := renderer.renderTemplate("test", template, ctx)
		if err != nil {
			t.Fatalf("renderTemplate() error = %v", err)
		}

		if !strings.Contains(string(rendered), "value: value") {
			t.Errorf("renderTemplate() did not use dig correctly: %s", string(rendered))
		}
	})

	t.Run("handles hardware context", func(t *testing.T) {
		ctx := &pkgcontext.RenderContext{
			HCO: &unstructured.Unstructured{
				Object: map[string]interface{}{},
			},
			Hardware: &pkgcontext.HardwareContext{
				GPUPresent: true,
			},
		}

		template := "{{ if .Hardware.GPUPresent }}gpu-enabled{{ else }}no-gpu{{ end }}"
		rendered, err := renderer.renderTemplate("test", template, ctx)
		if err != nil {
			t.Fatalf("renderTemplate() error = %v", err)
		}

		if !strings.Contains(string(rendered), "gpu-enabled") {
			t.Errorf("renderTemplate() did not handle hardware context: %s", string(rendered))
		}
	})

	t.Run("returns error for invalid template", func(t *testing.T) {
		ctx := &pkgcontext.RenderContext{
			HCO: &unstructured.Unstructured{
				Object: map[string]interface{}{},
			},
		}

		template := "{{ .InvalidSyntax"
		_, err := renderer.renderTemplate("test", template, ctx)
		if err == nil {
			t.Error("renderTemplate() should return error for invalid template")
		}
	})

	t.Run("returns error for undefined function", func(t *testing.T) {
		ctx := &pkgcontext.RenderContext{
			HCO: &unstructured.Unstructured{
				Object: map[string]interface{}{},
			},
		}

		template := `{{ env "PATH" }}`
		_, err := renderer.renderTemplate("test", template, ctx)
		if err == nil {
			t.Error("renderTemplate() should return error for dangerous function 'env'")
		}
	})
}

func TestRenderAsset(t *testing.T) {
	t.Run("identifies template vs static files", func(t *testing.T) {
		loader := assets.NewLoader()
		renderer := NewRenderer(loader)

		// Test with a template file extension
		assetMeta := &assets.AssetMetadata{
			Name: "test",
			Path: "test.yaml.tpl",
		}

		ctx := &pkgcontext.RenderContext{
			HCO: &unstructured.Unstructured{
				Object: map[string]interface{}{},
			},
		}

		// This will fail because file doesn't exist, but we're testing the path logic
		_, err := renderer.RenderAsset(assetMeta, ctx)
		if err == nil {
			t.Error("Expected error for non-existent template file")
		}
		if !strings.Contains(err.Error(), "failed to load template") {
			t.Errorf("Expected template loading error, got: %v", err)
		}
	})

	t.Run("handles static YAML path", func(t *testing.T) {
		loader := assets.NewLoader()
		renderer := NewRenderer(loader)

		assetMeta := &assets.AssetMetadata{
			Name: "static",
			Path: "static.yaml",
		}

		ctx := &pkgcontext.RenderContext{
			HCO: &unstructured.Unstructured{
				Object: map[string]interface{}{},
			},
		}

		// This will fail because file doesn't exist, but we're testing the path logic
		_, err := renderer.RenderAsset(assetMeta, ctx)
		if err == nil {
			t.Error("Expected error for non-existent static file")
		}
		if !strings.Contains(err.Error(), "failed to read asset") {
			t.Errorf("Expected asset loading error, got: %v", err)
		}
	})
}

func TestRenderMultiAsset(t *testing.T) {
	t.Run("identifies template vs static files for multi-doc", func(t *testing.T) {
		loader := assets.NewLoader()
		renderer := NewRenderer(loader)

		assetMeta := &assets.AssetMetadata{
			Name: "multi",
			Path: "multi.yaml.tmpl",
		}

		ctx := &pkgcontext.RenderContext{
			HCO: &unstructured.Unstructured{
				Object: map[string]interface{}{},
			},
		}

		// This will fail because file doesn't exist, but we're testing the path logic
		_, err := renderer.RenderMultiAsset(assetMeta, ctx)
		if err == nil {
			t.Error("Expected error for non-existent template file")
		}
		if !strings.Contains(err.Error(), "failed to load template") {
			t.Errorf("Expected template loading error, got: %v", err)
		}
	})

	t.Run("handles static multi-doc YAML path", func(t *testing.T) {
		loader := assets.NewLoader()
		renderer := NewRenderer(loader)

		assetMeta := &assets.AssetMetadata{
			Name: "static-multi",
			Path: "static-multi.yaml",
		}

		ctx := &pkgcontext.RenderContext{
			HCO: &unstructured.Unstructured{
				Object: map[string]interface{}{},
			},
		}

		// This will fail because file doesn't exist, but we're testing the path logic
		_, err := renderer.RenderMultiAsset(assetMeta, ctx)
		if err == nil {
			t.Error("Expected error for non-existent static file")
		}
		if !strings.Contains(err.Error(), "failed to read asset") {
			t.Errorf("Expected asset loading error, got: %v", err)
		}
	})
}
