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
