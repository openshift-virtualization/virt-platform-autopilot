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
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestCompareSpecs(t *testing.T) {
	tests := []struct {
		name string
		obj1 *unstructured.Unstructured
		obj2 *unstructured.Unstructured
		want bool
	}{
		{
			name: "both nil",
			obj1: nil,
			obj2: nil,
			want: true,
		},
		{
			name: "first nil",
			obj1: nil,
			obj2: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"spec": map[string]interface{}{"replicas": 3},
				},
			},
			want: false,
		},
		{
			name: "second nil",
			obj1: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"spec": map[string]interface{}{"replicas": 3},
				},
			},
			obj2: nil,
			want: false,
		},
		{
			name: "identical specs",
			obj1: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"spec": map[string]interface{}{
						"replicas": int64(3),
						"selector": map[string]interface{}{"app": "test"},
					},
				},
			},
			obj2: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"spec": map[string]interface{}{
						"replicas": int64(3),
						"selector": map[string]interface{}{"app": "test"},
					},
				},
			},
			want: true,
		},
		{
			name: "different specs",
			obj1: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"spec": map[string]interface{}{"replicas": int64(3)},
				},
			},
			obj2: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"spec": map[string]interface{}{"replicas": int64(5)},
				},
			},
			want: false,
		},
		{
			name: "both have no spec",
			obj1: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{"name": "test"},
				},
			},
			obj2: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{"name": "test"},
				},
			},
			want: true,
		},
		{
			name: "only first has spec",
			obj1: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"spec": map[string]interface{}{"replicas": int64(3)},
				},
			},
			obj2: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{"name": "test"},
				},
			},
			want: false,
		},
		{
			name: "only second has spec",
			obj1: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{"name": "test"},
				},
			},
			obj2: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"spec": map[string]interface{}{"replicas": int64(3)},
				},
			},
			want: false,
		},
		{
			name: "metadata differs but specs identical",
			obj1: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{"name": "test1"},
					"spec":     map[string]interface{}{"replicas": int64(3)},
				},
			},
			obj2: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{"name": "test2"},
					"spec":     map[string]interface{}{"replicas": int64(3)},
				},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CompareSpecs(tt.obj1, tt.obj2)
			if got != tt.want {
				t.Errorf("CompareSpecs() = %v, want %v", got, tt.want)
			}
		})
	}
}
