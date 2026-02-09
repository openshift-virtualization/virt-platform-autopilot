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

package util

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestSetNestedFieldWithDefault(t *testing.T) {
	tests := []struct {
		name      string
		obj       map[string]interface{}
		value     interface{}
		fields    []string
		wantErr   bool
		wantValue interface{}
	}{
		{
			name: "set field when not present",
			obj: map[string]interface{}{
				"metadata": map[string]interface{}{},
			},
			value:     "test-value",
			fields:    []string{"metadata", "name"},
			wantErr:   false,
			wantValue: "test-value",
		},
		{
			name: "do not override existing field",
			obj: map[string]interface{}{
				"metadata": map[string]interface{}{
					"name": "existing-value",
				},
			},
			value:     "new-value",
			fields:    []string{"metadata", "name"},
			wantErr:   false,
			wantValue: "existing-value", // Should keep existing
		},
		{
			name:    "error on nil object",
			obj:     nil,
			value:   "test",
			fields:  []string{"field"},
			wantErr: true,
		},
		{
			name: "set nested field multiple levels",
			obj: map[string]interface{}{
				"spec": map[string]interface{}{},
			},
			value:     int64(3),
			fields:    []string{"spec", "replicas"},
			wantErr:   false,
			wantValue: int64(3),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := SetNestedFieldWithDefault(tt.obj, tt.value, tt.fields...)
			if (err != nil) != tt.wantErr {
				t.Errorf("SetNestedFieldWithDefault() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr && tt.obj != nil {
				got, found, _ := unstructured.NestedFieldCopy(tt.obj, tt.fields...)
				if !found {
					t.Errorf("SetNestedFieldWithDefault() field not found after setting")
					return
				}
				if got != tt.wantValue {
					t.Errorf("SetNestedFieldWithDefault() = %v, want %v", got, tt.wantValue)
				}
			}
		})
	}
}

func TestGetNestedString(t *testing.T) {
	tests := []struct {
		name      string
		obj       map[string]interface{}
		fields    []string
		wantValue string
		wantFound bool
	}{
		{
			name: "get existing string",
			obj: map[string]interface{}{
				"metadata": map[string]interface{}{
					"name": "test-name",
				},
			},
			fields:    []string{"metadata", "name"},
			wantValue: "test-name",
			wantFound: true,
		},
		{
			name: "field not found",
			obj: map[string]interface{}{
				"metadata": map[string]interface{}{},
			},
			fields:    []string{"metadata", "missing"},
			wantValue: "",
			wantFound: false,
		},
		{
			name: "wrong type returns not found",
			obj: map[string]interface{}{
				"count": 123,
			},
			fields:    []string{"count"},
			wantValue: "",
			wantFound: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, found := GetNestedString(tt.obj, tt.fields...)
			if found != tt.wantFound {
				t.Errorf("GetNestedString() found = %v, want %v", found, tt.wantFound)
			}
			if got != tt.wantValue {
				t.Errorf("GetNestedString() = %v, want %v", got, tt.wantValue)
			}
		})
	}
}

func TestGetNestedInt64(t *testing.T) {
	tests := []struct {
		name      string
		obj       map[string]interface{}
		fields    []string
		wantValue int64
		wantFound bool
	}{
		{
			name: "get existing int64",
			obj: map[string]interface{}{
				"spec": map[string]interface{}{
					"replicas": int64(5),
				},
			},
			fields:    []string{"spec", "replicas"},
			wantValue: 5,
			wantFound: true,
		},
		{
			name: "field not found",
			obj: map[string]interface{}{
				"spec": map[string]interface{}{},
			},
			fields:    []string{"spec", "missing"},
			wantValue: 0,
			wantFound: false,
		},
		{
			name: "wrong type returns not found",
			obj: map[string]interface{}{
				"name": "string-value",
			},
			fields:    []string{"name"},
			wantValue: 0,
			wantFound: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, found := GetNestedInt64(tt.obj, tt.fields...)
			if found != tt.wantFound {
				t.Errorf("GetNestedInt64() found = %v, want %v", found, tt.wantFound)
			}
			if got != tt.wantValue {
				t.Errorf("GetNestedInt64() = %v, want %v", got, tt.wantValue)
			}
		})
	}
}

func TestGetNestedBool(t *testing.T) {
	tests := []struct {
		name      string
		obj       map[string]interface{}
		fields    []string
		wantValue bool
		wantFound bool
	}{
		{
			name: "get existing bool true",
			obj: map[string]interface{}{
				"spec": map[string]interface{}{
					"enabled": true,
				},
			},
			fields:    []string{"spec", "enabled"},
			wantValue: true,
			wantFound: true,
		},
		{
			name: "get existing bool false",
			obj: map[string]interface{}{
				"spec": map[string]interface{}{
					"enabled": false,
				},
			},
			fields:    []string{"spec", "enabled"},
			wantValue: false,
			wantFound: true,
		},
		{
			name: "field not found",
			obj: map[string]interface{}{
				"spec": map[string]interface{}{},
			},
			fields:    []string{"spec", "missing"},
			wantValue: false,
			wantFound: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, found := GetNestedBool(tt.obj, tt.fields...)
			if found != tt.wantFound {
				t.Errorf("GetNestedBool() found = %v, want %v", found, tt.wantFound)
			}
			if got != tt.wantValue {
				t.Errorf("GetNestedBool() = %v, want %v", got, tt.wantValue)
			}
		})
	}
}

func TestGetNestedStringSlice(t *testing.T) {
	tests := []struct {
		name      string
		obj       map[string]interface{}
		fields    []string
		wantValue []string
		wantFound bool
	}{
		{
			name: "get existing string slice",
			obj: map[string]interface{}{
				"spec": map[string]interface{}{
					"args": []interface{}{"arg1", "arg2"},
				},
			},
			fields:    []string{"spec", "args"},
			wantValue: []string{"arg1", "arg2"},
			wantFound: true,
		},
		{
			name: "field not found",
			obj: map[string]interface{}{
				"spec": map[string]interface{}{},
			},
			fields:    []string{"spec", "missing"},
			wantValue: nil,
			wantFound: false,
		},
		{
			name: "empty slice",
			obj: map[string]interface{}{
				"spec": map[string]interface{}{
					"args": []interface{}{},
				},
			},
			fields:    []string{"spec", "args"},
			wantValue: []string{},
			wantFound: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, found := GetNestedStringSlice(tt.obj, tt.fields...)
			if found != tt.wantFound {
				t.Errorf("GetNestedStringSlice() found = %v, want %v", found, tt.wantFound)
			}
			if found {
				if len(got) != len(tt.wantValue) {
					t.Errorf("GetNestedStringSlice() length = %v, want %v", len(got), len(tt.wantValue))
					return
				}
				for i := range got {
					if got[i] != tt.wantValue[i] {
						t.Errorf("GetNestedStringSlice()[%d] = %v, want %v", i, got[i], tt.wantValue[i])
					}
				}
			}
		})
	}
}

func TestMakeGVK(t *testing.T) {
	tests := []struct {
		name    string
		group   string
		version string
		kind    string
		want    schema.GroupVersionKind
	}{
		{
			name:    "core Kubernetes resource",
			group:   "",
			version: "v1",
			kind:    "Pod",
			want: schema.GroupVersionKind{
				Group:   "",
				Version: "v1",
				Kind:    "Pod",
			},
		},
		{
			name:    "custom resource",
			group:   "example.com",
			version: "v1alpha1",
			kind:    "MyResource",
			want: schema.GroupVersionKind{
				Group:   "example.com",
				Version: "v1alpha1",
				Kind:    "MyResource",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MakeGVK(tt.group, tt.version, tt.kind)
			if got != tt.want {
				t.Errorf("MakeGVK() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMakeUnstructured(t *testing.T) {
	gvk := schema.GroupVersionKind{
		Group:   "apps",
		Version: "v1",
		Kind:    "Deployment",
	}

	obj := MakeUnstructured(gvk)

	if obj == nil {
		t.Fatal("MakeUnstructured() returned nil")
	}

	gotGVK := obj.GroupVersionKind()
	if gotGVK != gvk {
		t.Errorf("MakeUnstructured() GVK = %v, want %v", gotGVK, gvk)
	}
}

func TestCloneUnstructured(t *testing.T) {
	tests := []struct {
		name string
		obj  *unstructured.Unstructured
		want *unstructured.Unstructured
	}{
		{
			name: "clone non-nil object",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "v1",
					"kind":       "ConfigMap",
					"metadata": map[string]interface{}{
						"name": "test",
					},
				},
			},
			want: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "v1",
					"kind":       "ConfigMap",
					"metadata": map[string]interface{}{
						"name": "test",
					},
				},
			},
		},
		{
			name: "clone nil object",
			obj:  nil,
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CloneUnstructured(tt.obj)

			if tt.want == nil {
				if got != nil {
					t.Errorf("CloneUnstructured() = %v, want nil", got)
				}
				return
			}

			if got == nil {
				t.Fatal("CloneUnstructured() returned nil, want non-nil")
			}

			// Verify it's a deep copy (different pointers)
			if tt.obj != nil && got == tt.obj {
				t.Error("CloneUnstructured() returned same pointer, want deep copy")
			}

			// Verify content matches
			if got.GetKind() != tt.want.GetKind() {
				t.Errorf("CloneUnstructured() Kind = %v, want %v", got.GetKind(), tt.want.GetKind())
			}
			if got.GetName() != tt.want.GetName() {
				t.Errorf("CloneUnstructured() Name = %v, want %v", got.GetName(), tt.want.GetName())
			}
		})
	}
}
