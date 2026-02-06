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
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// SetNestedFieldWithDefault sets a nested field with a default value if not already set
func SetNestedFieldWithDefault(obj map[string]interface{}, value interface{}, fields ...string) error {
	if obj == nil {
		return fmt.Errorf("object is nil")
	}

	// Check if field already exists
	_, found, _ := unstructured.NestedFieldCopy(obj, fields...)
	if found {
		return nil // Don't override existing value
	}

	return unstructured.SetNestedField(obj, value, fields...)
}

// GetNestedString safely gets a nested string field
func GetNestedString(obj map[string]interface{}, fields ...string) (string, bool) {
	val, found, err := unstructured.NestedString(obj, fields...)
	if err != nil || !found {
		return "", false
	}
	return val, true
}

// GetNestedInt64 safely gets a nested int64 field
func GetNestedInt64(obj map[string]interface{}, fields ...string) (int64, bool) {
	val, found, err := unstructured.NestedInt64(obj, fields...)
	if err != nil || !found {
		return 0, false
	}
	return val, true
}

// GetNestedBool safely gets a nested bool field
func GetNestedBool(obj map[string]interface{}, fields ...string) (bool, bool) {
	val, found, err := unstructured.NestedBool(obj, fields...)
	if err != nil || !found {
		return false, false
	}
	return val, true
}

// GetNestedStringSlice safely gets a nested string slice field
func GetNestedStringSlice(obj map[string]interface{}, fields ...string) ([]string, bool) {
	val, found, err := unstructured.NestedStringSlice(obj, fields...)
	if err != nil || !found {
		return nil, false
	}
	return val, true
}

// MakeGVK creates a GroupVersionKind
func MakeGVK(group, version, kind string) schema.GroupVersionKind {
	return schema.GroupVersionKind{
		Group:   group,
		Version: version,
		Kind:    kind,
	}
}

// MakeUnstructured creates an unstructured object with GVK set
func MakeUnstructured(gvk schema.GroupVersionKind) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(gvk)
	return obj
}

// CloneUnstructured creates a deep copy of an unstructured object
func CloneUnstructured(obj *unstructured.Unstructured) *unstructured.Unstructured {
	if obj == nil {
		return nil
	}
	return obj.DeepCopy()
}
