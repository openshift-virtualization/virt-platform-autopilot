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

func TestApplyJSONPatch(t *testing.T) {
	tests := []struct {
		name          string
		obj           *unstructured.Unstructured
		patch         string
		expectApplied bool
		expectError   bool
		validate      func(t *testing.T, obj *unstructured.Unstructured)
	}{
		{
			name: "no patch annotation",
			obj: &unstructured.Unstructured{
				Object: map[string]any{
					"apiVersion": "v1",
					"kind":       "ConfigMap",
					"metadata": map[string]any{
						"name": "test",
					},
					"data": map[string]any{
						"key": "value",
					},
				},
			},
			expectApplied: false,
			expectError:   false,
		},
		{
			name: "add operation",
			obj: &unstructured.Unstructured{
				Object: map[string]any{
					"apiVersion": "v1",
					"kind":       "ConfigMap",
					"metadata": map[string]any{
						"name": "test",
						"annotations": map[string]any{
							PatchAnnotation: `[{"op": "add", "path": "/data/newKey", "value": "newValue"}]`,
						},
					},
					"data": map[string]any{
						"key": "value",
					},
				},
			},
			expectApplied: true,
			expectError:   false,
			validate: func(t *testing.T, obj *unstructured.Unstructured) {
				data, _, _ := unstructured.NestedStringMap(obj.Object, "data")
				if data["newKey"] != "newValue" {
					t.Errorf("Expected data.newKey=newValue, got %v", data["newKey"])
				}
				if data["key"] != "value" {
					t.Errorf("Expected original data.key=value, got %v", data["key"])
				}
			},
		},
		{
			name: "replace operation",
			obj: &unstructured.Unstructured{
				Object: map[string]any{
					"apiVersion": "v1",
					"kind":       "ConfigMap",
					"metadata": map[string]any{
						"name": "test",
						"annotations": map[string]any{
							PatchAnnotation: `[{"op": "replace", "path": "/data/key", "value": "newValue"}]`,
						},
					},
					"data": map[string]any{
						"key": "value",
					},
				},
			},
			expectApplied: true,
			expectError:   false,
			validate: func(t *testing.T, obj *unstructured.Unstructured) {
				data, _, _ := unstructured.NestedStringMap(obj.Object, "data")
				if data["key"] != "newValue" {
					t.Errorf("Expected data.key=newValue, got %v", data["key"])
				}
			},
		},
		{
			name: "remove operation",
			obj: &unstructured.Unstructured{
				Object: map[string]any{
					"apiVersion": "v1",
					"kind":       "ConfigMap",
					"metadata": map[string]any{
						"name": "test",
						"annotations": map[string]any{
							PatchAnnotation: `[{"op": "remove", "path": "/data/key"}]`,
						},
					},
					"data": map[string]any{
						"key":   "value",
						"other": "data",
					},
				},
			},
			expectApplied: true,
			expectError:   false,
			validate: func(t *testing.T, obj *unstructured.Unstructured) {
				data, _, _ := unstructured.NestedStringMap(obj.Object, "data")
				if _, exists := data["key"]; exists {
					t.Errorf("Expected data.key to be removed")
				}
				if data["other"] != "data" {
					t.Errorf("Expected other data to remain")
				}
			},
		},
		{
			name: "multiple operations",
			obj: &unstructured.Unstructured{
				Object: map[string]any{
					"apiVersion": "v1",
					"kind":       "ConfigMap",
					"metadata": map[string]any{
						"name": "test",
						"annotations": map[string]any{
							PatchAnnotation: `[
								{"op": "replace", "path": "/data/key", "value": "updated"},
								{"op": "add", "path": "/data/new", "value": "added"}
							]`,
						},
					},
					"data": map[string]any{
						"key": "value",
					},
				},
			},
			expectApplied: true,
			expectError:   false,
			validate: func(t *testing.T, obj *unstructured.Unstructured) {
				data, _, _ := unstructured.NestedStringMap(obj.Object, "data")
				if data["key"] != "updated" {
					t.Errorf("Expected data.key=updated, got %v", data["key"])
				}
				if data["new"] != "added" {
					t.Errorf("Expected data.new=added, got %v", data["new"])
				}
			},
		},
		{
			name: "invalid patch JSON",
			obj: &unstructured.Unstructured{
				Object: map[string]any{
					"apiVersion": "v1",
					"kind":       "ConfigMap",
					"metadata": map[string]any{
						"name": "test",
						"annotations": map[string]any{
							PatchAnnotation: `not valid json`,
						},
					},
				},
			},
			expectApplied: false,
			expectError:   true,
		},
		{
			name: "invalid patch operation",
			obj: &unstructured.Unstructured{
				Object: map[string]any{
					"apiVersion": "v1",
					"kind":       "ConfigMap",
					"metadata": map[string]any{
						"name": "test",
						"annotations": map[string]any{
							PatchAnnotation: `[{"op": "invalid", "path": "/data/key"}]`,
						},
					},
				},
			},
			expectApplied: false,
			expectError:   true,
		},
		{
			name:          "nil object",
			obj:           nil,
			expectApplied: false,
			expectError:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			applied, err := ApplyJSONPatch(tt.obj)

			if (err != nil) != tt.expectError {
				t.Errorf("ApplyJSONPatch() error = %v, expectError %v", err, tt.expectError)
				return
			}

			if applied != tt.expectApplied {
				t.Errorf("ApplyJSONPatch() applied = %v, expectApplied %v", applied, tt.expectApplied)
				return
			}

			if tt.validate != nil && !tt.expectError {
				tt.validate(t, tt.obj)
			}
		})
	}
}

func TestValidateJSONPatch(t *testing.T) {
	tests := []struct {
		name        string
		patch       string
		expectError bool
	}{
		{
			name:        "empty patch",
			patch:       "",
			expectError: false,
		},
		{
			name:        "valid add operation",
			patch:       `[{"op": "add", "path": "/data/key", "value": "value"}]`,
			expectError: false,
		},
		{
			name:        "valid multiple operations",
			patch:       `[{"op": "add", "path": "/a", "value": "1"}, {"op": "remove", "path": "/b"}]`,
			expectError: false,
		},
		{
			name:        "invalid JSON",
			patch:       `not json`,
			expectError: true,
		},
		{
			name:        "invalid operation",
			patch:       `[{"op": "invalid"}]`,
			expectError: true,
		},
		{
			name:        "missing required field",
			patch:       `[{"op": "add"}]`,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateJSONPatch(tt.patch)
			if (err != nil) != tt.expectError {
				t.Errorf("ValidateJSONPatch() error = %v, expectError %v", err, tt.expectError)
			}
		})
	}
}
