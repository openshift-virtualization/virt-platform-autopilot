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

package overrides

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestMaskIgnoredFields(t *testing.T) {
	tests := []struct {
		name        string
		desired     *unstructured.Unstructured
		live        *unstructured.Unstructured
		expectError bool
		validate    func(t *testing.T, result *unstructured.Unstructured)
	}{
		{
			name: "no ignore-fields annotation",
			desired: &unstructured.Unstructured{
				Object: map[string]any{
					"spec": map[string]any{
						"replicas": int64(3),
					},
				},
			},
			live: &unstructured.Unstructured{
				Object: map[string]any{
					"spec": map[string]any{
						"replicas": int64(5),
					},
				},
			},
			expectError: false,
			validate: func(t *testing.T, result *unstructured.Unstructured) {
				replicas, _, _ := unstructured.NestedInt64(result.Object, "spec", "replicas")
				if replicas != 3 {
					t.Errorf("Expected replicas=3 (from desired), got %d", replicas)
				}
			},
		},
		{
			name: "mask single field",
			desired: &unstructured.Unstructured{
				Object: map[string]any{
					"spec": map[string]any{
						"replicas": int64(3),
						"template": map[string]any{
							"spec": "desired",
						},
					},
				},
			},
			live: &unstructured.Unstructured{
				Object: map[string]any{
					"metadata": map[string]any{
						"annotations": map[string]any{
							AnnotationIgnoreFields: "/spec/replicas",
						},
					},
					"spec": map[string]any{
						"replicas": int64(5),
						"template": map[string]any{
							"spec": "live",
						},
					},
				},
			},
			expectError: false,
			validate: func(t *testing.T, result *unstructured.Unstructured) {
				replicas, _, _ := unstructured.NestedInt64(result.Object, "spec", "replicas")
				if replicas != 5 {
					t.Errorf("Expected replicas=5 (from live, masked), got %d", replicas)
				}
				template, _, _ := unstructured.NestedString(result.Object, "spec", "template", "spec")
				if template != "desired" {
					t.Errorf("Expected template=desired (not masked), got %s", template)
				}
			},
		},
		{
			name: "mask multiple fields",
			desired: &unstructured.Unstructured{
				Object: map[string]any{
					"spec": map[string]any{
						"replicas": int64(3),
						"image":    "desired:v1",
						"port":     int64(8080),
					},
				},
			},
			live: &unstructured.Unstructured{
				Object: map[string]any{
					"metadata": map[string]any{
						"annotations": map[string]any{
							AnnotationIgnoreFields: "/spec/replicas, /spec/image",
						},
					},
					"spec": map[string]any{
						"replicas": int64(5),
						"image":    "live:v2",
						"port":     int64(9090),
					},
				},
			},
			expectError: false,
			validate: func(t *testing.T, result *unstructured.Unstructured) {
				replicas, _, _ := unstructured.NestedInt64(result.Object, "spec", "replicas")
				if replicas != 5 {
					t.Errorf("Expected replicas=5 (masked), got %d", replicas)
				}
				image, _, _ := unstructured.NestedString(result.Object, "spec", "image")
				if image != "live:v2" {
					t.Errorf("Expected image=live:v2 (masked), got %s", image)
				}
				port, _, _ := unstructured.NestedInt64(result.Object, "spec", "port")
				if port != 8080 {
					t.Errorf("Expected port=8080 (not masked), got %d", port)
				}
			},
		},
		{
			name: "field exists in live but not in desired",
			desired: &unstructured.Unstructured{
				Object: map[string]any{
					"spec": map[string]any{
						"replicas": int64(3),
					},
				},
			},
			live: &unstructured.Unstructured{
				Object: map[string]any{
					"metadata": map[string]any{
						"annotations": map[string]any{
							AnnotationIgnoreFields: "/spec/image",
						},
					},
					"spec": map[string]any{
						"replicas": int64(5),
						"image":    "user-set:v1",
					},
				},
			},
			expectError: false,
			validate: func(t *testing.T, result *unstructured.Unstructured) {
				image, found, _ := unstructured.NestedString(result.Object, "spec", "image")
				if !found || image != "user-set:v1" {
					t.Errorf("Expected image=user-set:v1 (copied from live), got %s (found=%v)", image, found)
				}
			},
		},
		{
			name: "field doesn't exist in live",
			desired: &unstructured.Unstructured{
				Object: map[string]any{
					"spec": map[string]any{
						"replicas": int64(3),
						"image":    "desired:v1",
					},
				},
			},
			live: &unstructured.Unstructured{
				Object: map[string]any{
					"metadata": map[string]any{
						"annotations": map[string]any{
							AnnotationIgnoreFields: "/spec/image",
						},
					},
					"spec": map[string]any{
						"replicas": int64(5),
					},
				},
			},
			expectError: false,
			validate: func(t *testing.T, result *unstructured.Unstructured) {
				_, found, _ := unstructured.NestedString(result.Object, "spec", "image")
				if found {
					t.Errorf("Expected image to be removed (not in live)")
				}
			},
		},
		{
			name:        "nil desired object",
			desired:     nil,
			live:        &unstructured.Unstructured{},
			expectError: true,
		},
		{
			name: "nil live object",
			desired: &unstructured.Unstructured{
				Object: map[string]any{
					"spec": map[string]any{
						"replicas": int64(3),
					},
				},
			},
			live:        nil,
			expectError: false,
			validate: func(t *testing.T, result *unstructured.Unstructured) {
				replicas, _, _ := unstructured.NestedInt64(result.Object, "spec", "replicas")
				if replicas != 3 {
					t.Errorf("Expected original desired object unchanged")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := MaskIgnoredFields(tt.desired, tt.live)

			if (err != nil) != tt.expectError {
				t.Errorf("MaskIgnoredFields() error = %v, expectError %v", err, tt.expectError)
				return
			}

			if tt.validate != nil && !tt.expectError {
				tt.validate(t, result)
			}
		})
	}
}

func TestValidatePointers(t *testing.T) {
	tests := []struct {
		name        string
		pointers    string
		expectError bool
	}{
		{
			name:        "empty",
			pointers:    "",
			expectError: false,
		},
		{
			name:        "single valid pointer",
			pointers:    "/spec/replicas",
			expectError: false,
		},
		{
			name:        "multiple valid pointers",
			pointers:    "/spec/replicas, /metadata/labels, /status/ready",
			expectError: false,
		},
		{
			name:        "pointer with spaces",
			pointers:    "  /spec/replicas  ,  /metadata/labels  ",
			expectError: false,
		},
		{
			name:        "missing leading slash",
			pointers:    "spec/replicas",
			expectError: true,
		},
		{
			name:        "valid escaped characters",
			pointers:    "/spec/app~1name",
			expectError: false,
		},
		{
			name:        "valid tilde zero escape",
			pointers:    "/spec/~0field",
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePointers(tt.pointers)
			if (err != nil) != tt.expectError {
				t.Errorf("ValidatePointers() error = %v, expectError %v", err, tt.expectError)
			}
		})
	}
}
