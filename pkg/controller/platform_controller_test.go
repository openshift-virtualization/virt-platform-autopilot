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

package controller

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestExtractFeatureGates(t *testing.T) {
	tests := []struct {
		name string
		hco  *unstructured.Unstructured
		want map[string]bool
	}{
		{
			name: "with feature gates",
			hco: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"spec": map[string]interface{}{
						"featureGates": []interface{}{
							"FeatureGate1",
							"FeatureGate2",
							"ExperimentalFeature",
						},
					},
				},
			},
			want: map[string]bool{
				"FeatureGate1":        true,
				"FeatureGate2":        true,
				"ExperimentalFeature": true,
			},
		},
		{
			name: "empty feature gates",
			hco: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"spec": map[string]interface{}{
						"featureGates": []interface{}{},
					},
				},
			},
			want: map[string]bool{},
		},
		{
			name: "no feature gates field",
			hco: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"spec": map[string]interface{}{},
				},
			},
			want: map[string]bool{},
		},
		{
			name: "no spec field",
			hco: &unstructured.Unstructured{
				Object: map[string]interface{}{},
			},
			want: map[string]bool{},
		},
		{
			name: "single feature gate",
			hco: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"spec": map[string]interface{}{
						"featureGates": []interface{}{
							"SingleFeature",
						},
					},
				},
			},
			want: map[string]bool{
				"SingleFeature": true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractFeatureGates(tt.hco)

			if len(got) != len(tt.want) {
				t.Errorf("extractFeatureGates() returned %d gates, want %d", len(got), len(tt.want))
			}

			for gate, enabled := range tt.want {
				if gotEnabled, exists := got[gate]; !exists {
					t.Errorf("extractFeatureGates() missing gate %q", gate)
				} else if gotEnabled != enabled {
					t.Errorf("extractFeatureGates()[%q] = %v, want %v", gate, gotEnabled, enabled)
				}
			}

			for gate := range got {
				if _, exists := tt.want[gate]; !exists {
					t.Errorf("extractFeatureGates() has unexpected gate %q", gate)
				}
			}
		})
	}
}
