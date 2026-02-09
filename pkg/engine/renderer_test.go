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
