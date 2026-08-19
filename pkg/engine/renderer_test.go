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

	t.Run("handles topology context", func(t *testing.T) {
		ctx := &pkgcontext.RenderContext{
			HCO: &unstructured.Unstructured{
				Object: map[string]any{},
			},
			Topology: &pkgcontext.TopologyContext{
				IsCompact: true,
			},
		}

		template := "{{ if .Topology.IsCompact }}compact{{ else }}regular{{ end }}"
		rendered, err := renderer.renderTemplate("test", template, ctx)
		if err != nil {
			t.Fatalf("renderTemplate() error = %v", err)
		}

		if !strings.Contains(string(rendered), "compact") {
			t.Errorf("renderTemplate() did not handle topology context: %s", string(rendered))
		}
	})

	t.Run("errors on missing template param key", func(t *testing.T) {
		ctx := pkgcontext.NewRenderContext(&unstructured.Unstructured{Object: map[string]any{}})

		template := "role: {{ .Params.role }}"
		_, err := renderer.renderTemplate("test", template, ctx)
		if err == nil {
			t.Error("renderTemplate() should return error when Params.role is missing")
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

func TestTemplateParamsPassedToContext(t *testing.T) {
	loader := assets.NewLoader()
	renderer := NewRenderer(loader)

	meta := &assets.AssetMetadata{
		Name:           "psi-enable",
		Path:           "active/machine-config/04-psi-enable.yaml.tpl",
		TemplateParams: map[string]string{"role": "worker"},
	}

	ctx := pkgcontext.NewRenderContext(&unstructured.Unstructured{Object: map[string]any{}})

	obj, err := renderer.RenderAsset(meta, ctx)
	if err != nil {
		t.Fatalf("RenderAsset() error = %v", err)
	}
	if obj == nil {
		t.Fatal("RenderAsset() returned nil")
	}

	if ctx.Params == nil {
		t.Fatal("ctx.Params should be set from TemplateParams")
	}
	if got := ctx.Params["role"]; got != "worker" {
		t.Errorf("ctx.Params[\"role\"] = %q, want %q", got, "worker")
	}
	if got := obj.GetName(); got != "99-openshift-machineconfig-worker-psi-karg" {
		t.Errorf("name = %q, want %q", got, "99-openshift-machineconfig-worker-psi-karg")
	}
}

func TestMetricsExporterEnvOverrides(t *testing.T) {
	loader := assets.NewLoader()
	renderer := NewRenderer(loader)

	t.Run("no annotation uses all defaults", func(t *testing.T) {
		ctx := &pkgcontext.RenderContext{
			HCO: &unstructured.Unstructured{
				Object: map[string]any{
					"metadata": map[string]any{
						"name":      "kubevirt-hyperconverged",
						"namespace": "openshift-cnv",
					},
				},
			},
			Images: map[string]string{
				"kubevirt-metrics-exporter": "quay.io/kubevirt/metrics-exporter:latest",
			},
		}

		assetMeta := &assets.AssetMetadata{
			Name: "metrics-exporter",
			Path: "active/metrics-exporter/metrics-exporter.yaml.tpl",
		}

		obj, err := renderer.RenderAsset(assetMeta, ctx)
		if err != nil {
			t.Fatalf("RenderAsset() error = %v", err)
		}
		if obj == nil {
			t.Fatal("RenderAsset() returned nil")
		}

		envVars := extractEnvVars(t, obj)
		assertEnvValue(t, envVars, "ENABLE_QMP", "true")
		assertEnvValue(t, envVars, "ENABLE_QGA", "true")
		assertEnvValue(t, envVars, "QGA_POLL_INTERVAL", "1m")
		assertEnvValue(t, envVars, "ENABLE_EBPF", "true")
		assertEnvValue(t, envVars, "ENABLE_EBPF_BLOCK", "true")
		assertEnvValue(t, envVars, "ENABLE_EBPF_NFS", "true")
		assertEnvValue(t, envVars, "ENABLE_EBPF_NFS_KPROBE", "false")
		assertEnvValue(t, envVars, "QMP_POLL_INTERVAL", "1m")
		assertEnvValue(t, envVars, "EBPF_SCAN_INTERVAL", "30")
		assertEnvValue(t, envVars, "LOG_LEVEL", "info")
	})

	t.Run("partial override merges with defaults", func(t *testing.T) {
		ctx := &pkgcontext.RenderContext{
			HCO: &unstructured.Unstructured{
				Object: map[string]any{
					"metadata": map[string]any{
						"name":      "kubevirt-hyperconverged",
						"namespace": "openshift-cnv",
						"annotations": map[string]any{
							"platform.kubevirt.io/metrics-exporter-env": `{"LOG_LEVEL":"debug","ENABLE_QMP":"false","EBPF_SCAN_INTERVAL":"60"}`,
						},
					},
				},
			},
			Images: map[string]string{
				"kubevirt-metrics-exporter": "quay.io/kubevirt/metrics-exporter:latest",
			},
		}

		assetMeta := &assets.AssetMetadata{
			Name: "metrics-exporter",
			Path: "active/metrics-exporter/metrics-exporter.yaml.tpl",
		}

		obj, err := renderer.RenderAsset(assetMeta, ctx)
		if err != nil {
			t.Fatalf("RenderAsset() error = %v", err)
		}
		if obj == nil {
			t.Fatal("RenderAsset() returned nil")
		}

		envVars := extractEnvVars(t, obj)

		// Overridden values
		assertEnvValue(t, envVars, "LOG_LEVEL", "debug")
		assertEnvValue(t, envVars, "ENABLE_QMP", "false")
		assertEnvValue(t, envVars, "EBPF_SCAN_INTERVAL", "60")

		// Non-overridden values keep defaults
		assertEnvValue(t, envVars, "ENABLE_QGA", "true")
		assertEnvValue(t, envVars, "QGA_POLL_INTERVAL", "1m")
		assertEnvValue(t, envVars, "ENABLE_EBPF", "true")
		assertEnvValue(t, envVars, "ENABLE_EBPF_BLOCK", "true")
		assertEnvValue(t, envVars, "ENABLE_EBPF_NFS", "true")
		assertEnvValue(t, envVars, "ENABLE_EBPF_NFS_KPROBE", "false")
		assertEnvValue(t, envVars, "QMP_POLL_INTERVAL", "1m")
	})

	t.Run("empty JSON object uses all defaults", func(t *testing.T) {
		ctx := &pkgcontext.RenderContext{
			HCO: &unstructured.Unstructured{
				Object: map[string]any{
					"metadata": map[string]any{
						"name":      "kubevirt-hyperconverged",
						"namespace": "openshift-cnv",
						"annotations": map[string]any{
							"platform.kubevirt.io/metrics-exporter-env": `{}`,
						},
					},
				},
			},
			Images: map[string]string{
				"kubevirt-metrics-exporter": "quay.io/kubevirt/metrics-exporter:latest",
			},
		}

		assetMeta := &assets.AssetMetadata{
			Name: "metrics-exporter",
			Path: "active/metrics-exporter/metrics-exporter.yaml.tpl",
		}

		obj, err := renderer.RenderAsset(assetMeta, ctx)
		if err != nil {
			t.Fatalf("RenderAsset() error = %v", err)
		}
		if obj == nil {
			t.Fatal("RenderAsset() returned nil")
		}

		envVars := extractEnvVars(t, obj)
		assertEnvValue(t, envVars, "LOG_LEVEL", "info")
		assertEnvValue(t, envVars, "ENABLE_QMP", "true")
		assertEnvValue(t, envVars, "EBPF_SCAN_INTERVAL", "30")
	})
}

// extractEnvVars pulls env vars from the rendered DaemonSet into a name->value map.
func extractEnvVars(t *testing.T, obj *unstructured.Unstructured) map[string]string {
	t.Helper()
	containers, found, err := unstructured.NestedSlice(obj.Object, "spec", "template", "spec", "containers")
	if err != nil || !found || len(containers) == 0 {
		t.Fatal("could not find containers in rendered DaemonSet")
	}

	container, ok := containers[0].(map[string]any)
	if !ok {
		t.Fatal("container is not a map")
	}

	envList, ok := container["env"].([]any)
	if !ok {
		t.Fatal("env is not a list")
	}

	result := make(map[string]string)
	for _, item := range envList {
		envMap, ok := item.(map[string]any)
		if !ok {
			continue
		}
		name, _ := envMap["name"].(string)
		value, _ := envMap["value"].(string)
		if name != "" {
			result[name] = value
		}
	}
	return result
}

func assertEnvValue(t *testing.T, envVars map[string]string, name, expected string) {
	t.Helper()
	got, exists := envVars[name]
	if !exists {
		t.Errorf("env var %s not found in rendered output", name)
		return
	}
	if got != expected {
		t.Errorf("env var %s = %q, want %q", name, got, expected)
	}
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
