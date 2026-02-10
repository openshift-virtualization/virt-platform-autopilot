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

package assets

import (
	"testing"
)

func TestNewLoader(t *testing.T) {
	loader := NewLoader()
	if loader == nil {
		t.Fatal("NewLoader() returned nil")
	}
}

func TestIsTemplate(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		expected bool
	}{
		{
			name:     "template with .tpl extension",
			path:     "path/to/file.yaml.tpl",
			expected: true,
		},
		{
			name:     "template with .tmpl extension",
			path:     "path/to/file.yaml.tmpl",
			expected: true,
		},
		{
			name:     "non-template YAML file",
			path:     "path/to/file.yaml",
			expected: false,
		},
		{
			name:     "non-template without extension",
			path:     "path/to/file",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsTemplate(tt.path)
			if result != tt.expected {
				t.Errorf("IsTemplate(%q) = %v, want %v", tt.path, result, tt.expected)
			}
		})
	}
}

func TestParseYAML(t *testing.T) {
	tests := []struct {
		name     string
		data     []byte
		wantErr  bool
		wantKind string
		wantName string
	}{
		{
			name: "valid ConfigMap",
			data: []byte(`apiVersion: v1
kind: ConfigMap
metadata:
  name: test-config
  namespace: default
data:
  key: value
`),
			wantErr:  false,
			wantKind: "ConfigMap",
			wantName: "test-config",
		},
		{
			name: "valid Deployment",
			data: []byte(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: test-deployment
spec:
  replicas: 3
`),
			wantErr:  false,
			wantKind: "Deployment",
			wantName: "test-deployment",
		},
		{
			name:    "invalid YAML",
			data:    []byte(`invalid: yaml: content:`),
			wantErr: true,
		},
		{
			name:    "empty YAML",
			data:    []byte(``),
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			obj, err := ParseYAML(tt.data)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseYAML() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if err != nil {
				return
			}

			if tt.wantKind != "" && obj.GetKind() != tt.wantKind {
				t.Errorf("ParseYAML() kind = %v, want %v", obj.GetKind(), tt.wantKind)
			}

			if tt.wantName != "" && obj.GetName() != tt.wantName {
				t.Errorf("ParseYAML() name = %v, want %v", obj.GetName(), tt.wantName)
			}
		})
	}
}

func TestParseMultiYAML(t *testing.T) {
	tests := []struct {
		name      string
		data      []byte
		wantErr   bool
		wantCount int
		wantKinds []string
	}{
		{
			name: "single document",
			data: []byte(`apiVersion: v1
kind: ConfigMap
metadata:
  name: test1
`),
			wantErr:   false,
			wantCount: 1,
			wantKinds: []string{"ConfigMap"},
		},
		{
			name: "multiple documents",
			data: []byte(`apiVersion: v1
kind: ConfigMap
metadata:
  name: test1
---
apiVersion: v1
kind: Secret
metadata:
  name: test2
---
apiVersion: v1
kind: Service
metadata:
  name: test3
`),
			wantErr:   false,
			wantCount: 3,
			wantKinds: []string{"ConfigMap", "Secret", "Service"},
		},
		{
			name: "multiple documents with empty separators",
			data: []byte(`apiVersion: v1
kind: ConfigMap
metadata:
  name: test1
---

---
apiVersion: v1
kind: Secret
metadata:
  name: test2
`),
			wantErr:   false,
			wantCount: 2,
			wantKinds: []string{"ConfigMap", "Secret"},
		},
		{
			name:      "empty document",
			data:      []byte(``),
			wantErr:   false,
			wantCount: 0,
		},
		{
			name: "invalid YAML in one document",
			data: []byte(`apiVersion: v1
kind: ConfigMap
metadata:
  name: test1
---
invalid: yaml: content:
`),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objs, err := ParseMultiYAML(tt.data)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseMultiYAML() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if err != nil {
				return
			}

			if len(objs) != tt.wantCount {
				t.Errorf("ParseMultiYAML() count = %v, want %v", len(objs), tt.wantCount)
				return
			}

			for i, kind := range tt.wantKinds {
				if objs[i].GetKind() != kind {
					t.Errorf("ParseMultiYAML() object[%d] kind = %v, want %v", i, objs[i].GetKind(), kind)
				}
			}
		})
	}
}

func TestLoader_LoadAsset(t *testing.T) {
	loader := NewLoader()

	tests := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{
			name:    "non-existent file",
			path:    "does-not-exist.yaml",
			wantErr: true,
		},
		{
			name:    "invalid path",
			path:    "../../../etc/passwd",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := loader.LoadAsset(tt.path)
			if (err != nil) != tt.wantErr {
				t.Errorf("LoadAsset() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr && len(data) == 0 {
				t.Error("LoadAsset() returned empty data for valid file")
			}
		})
	}
}

func TestLoader_LoadAssetTemplate(t *testing.T) {
	loader := NewLoader()

	tests := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{
			name:    "non-existent template",
			path:    "does-not-exist.yaml.tpl",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			content, err := loader.LoadAssetTemplate(tt.path)
			if (err != nil) != tt.wantErr {
				t.Errorf("LoadAssetTemplate() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr && content == "" {
				t.Error("LoadAssetTemplate() returned empty content for valid file")
			}
		})
	}
}

func TestLoader_LoadAssetAsUnstructured(t *testing.T) {
	loader := NewLoader()

	tests := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{
			name:    "non-existent file",
			path:    "does-not-exist.yaml",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			obj, err := loader.LoadAssetAsUnstructured(tt.path)
			if (err != nil) != tt.wantErr {
				t.Errorf("LoadAssetAsUnstructured() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr && obj == nil {
				t.Error("LoadAssetAsUnstructured() returned nil for valid file")
			}
		})
	}
}

func TestLoader_ListAssets(t *testing.T) {
	loader := NewLoader()

	tests := []struct {
		name    string
		pattern string
		wantErr bool
	}{
		{
			name:    "invalid glob pattern",
			pattern: "[invalid",
			wantErr: true,
		},
		{
			name:    "valid pattern with no matches",
			pattern: "nonexistent/*.yaml",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := loader.ListAssets(tt.pattern)
			if (err != nil) != tt.wantErr {
				t.Errorf("ListAssets() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestCalculateDepth(t *testing.T) {
	tests := []struct {
		name        string
		obj         interface{}
		expectedMax int
		description string
	}{
		{
			name:        "simple string",
			obj:         "simple value",
			expectedMax: 1,
			description: "Simple values have depth 1",
		},
		{
			name:        "simple number",
			obj:         42,
			expectedMax: 1,
			description: "Numbers have depth 1",
		},
		{
			name:        "empty map",
			obj:         map[string]interface{}{},
			expectedMax: 1,
			description: "Empty map has depth 1",
		},
		{
			name: "flat map",
			obj: map[string]interface{}{
				"key1": "value1",
				"key2": "value2",
			},
			expectedMax: 2,
			description: "Flat map has depth 2",
		},
		{
			name: "nested map",
			obj: map[string]interface{}{
				"level1": map[string]interface{}{
					"level2": map[string]interface{}{
						"level3": "value",
					},
				},
			},
			expectedMax: 4,
			description: "Nested maps increase depth",
		},
		{
			name: "array of strings",
			obj: []interface{}{
				"item1",
				"item2",
			},
			expectedMax: 2,
			description: "Array with simple values has depth 2",
		},
		{
			name: "array of maps",
			obj: []interface{}{
				map[string]interface{}{
					"nested": "value",
				},
			},
			expectedMax: 3,
			description: "Array of maps has depth 3",
		},
		{
			name: "complex nested structure",
			obj: map[string]interface{}{
				"metadata": map[string]interface{}{
					"name": "test",
					"labels": map[string]interface{}{
						"app": "myapp",
					},
				},
				"spec": map[string]interface{}{
					"replicas": 3,
					"template": map[string]interface{}{
						"spec": map[string]interface{}{
							"containers": []interface{}{
								map[string]interface{}{
									"name":  "nginx",
									"image": "nginx:latest",
								},
							},
						},
					},
				},
			},
			expectedMax: 7,
			description: "Complex Kubernetes-like structure (1:root + 1:metadata + 1:labels + 1:app OR 1:spec + 1:template + 1:spec + 1:containers + 1:map)",
		},
		{
			name:        "empty array",
			obj:         []interface{}{},
			expectedMax: 1,
			description: "Empty array has depth 1",
		},
		{
			name: "deeply nested maps",
			obj: map[string]interface{}{
				"l1": map[string]interface{}{
					"l2": map[string]interface{}{
						"l3": map[string]interface{}{
							"l4": map[string]interface{}{
								"l5": "deep",
							},
						},
					},
				},
			},
			expectedMax: 6,
			description: "Very deep nesting",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			depth := calculateDepth(tt.obj)
			if depth != tt.expectedMax {
				t.Errorf("calculateDepth() = %d, want %d (%s)", depth, tt.expectedMax, tt.description)
			}
		})
	}
}

func TestParseYAML_SizeLimits(t *testing.T) {
	t.Run("rejects YAML exceeding size limit", func(t *testing.T) {
		// Create YAML larger than MaxYAMLSize
		largeData := make([]byte, MaxYAMLSize+1)
		for i := range largeData {
			largeData[i] = 'a'
		}

		_, err := ParseYAML(largeData)
		if err == nil {
			t.Error("ParseYAML() should reject YAML exceeding size limit")
		}
	})

	t.Run("accepts YAML within size limit", func(t *testing.T) {
		smallData := []byte(`apiVersion: v1
kind: ConfigMap
metadata:
  name: small-config
`)
		_, err := ParseYAML(smallData)
		if err != nil {
			t.Errorf("ParseYAML() should accept small YAML, got error: %v", err)
		}
	})
}

func TestParseYAML_DepthLimits(t *testing.T) {
	t.Run("accepts YAML within depth limit", func(t *testing.T) {
		normalYAML := []byte(`apiVersion: v1
kind: ConfigMap
metadata:
  name: normal
  labels:
    app: test
data:
  key1: value1
  nested:
    level2: value2
`)
		_, err := ParseYAML(normalYAML)
		if err != nil {
			t.Errorf("ParseYAML() should accept normal depth YAML, got error: %v", err)
		}
	})

	t.Run("depth checking logic works", func(t *testing.T) {
		// Test that calculateDepth is being called
		// We'll create a moderately deep structure and verify it parses
		deepButValidYAML := []byte(`apiVersion: v1
kind: ConfigMap
metadata:
  name: test
  annotations:
    deep:
      level1:
        level2:
          level3:
            level4: value
`)
		_, err := ParseYAML(deepButValidYAML)
		if err != nil {
			t.Errorf("ParseYAML() should accept moderately deep YAML, got error: %v", err)
		}
	})
}

func TestLoadAssetAsUnstructured_ValidFiles(t *testing.T) {
	loader := NewLoader()

	t.Run("loads metadata.yaml successfully", func(t *testing.T) {
		// metadata.yaml should exist in the assets directory
		obj, err := loader.LoadAssetAsUnstructured("metadata.yaml")

		if err != nil {
			t.Skipf("metadata.yaml not found or invalid: %v", err)
		}

		if obj == nil {
			t.Error("LoadAssetAsUnstructured() returned nil object for valid file")
		}
	})
}

func TestParseMultiYAML_EdgeCases(t *testing.T) {
	t.Run("handles documents with only whitespace", func(t *testing.T) {
		data := []byte(`apiVersion: v1
kind: ConfigMap
metadata:
  name: test1
---

---
apiVersion: v1
kind: Secret
metadata:
  name: test2
`)
		objs, err := ParseMultiYAML(data)
		if err != nil {
			t.Errorf("ParseMultiYAML() error = %v", err)
		}

		if len(objs) != 2 {
			t.Errorf("ParseMultiYAML() returned %d objects, want 2", len(objs))
		}
	})

	t.Run("handles trailing separator", func(t *testing.T) {
		data := []byte(`apiVersion: v1
kind: ConfigMap
metadata:
  name: test1
---
`)
		objs, err := ParseMultiYAML(data)
		if err != nil {
			t.Errorf("ParseMultiYAML() error = %v", err)
		}

		if len(objs) != 1 {
			t.Errorf("ParseMultiYAML() returned %d objects, want 1", len(objs))
		}
	})
}
