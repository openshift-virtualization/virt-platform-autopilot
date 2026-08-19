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

package resources_test

import (
	"slices"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/kubevirt/virt-platform-autopilot/pkg/resources"
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
				Object: map[string]any{
					"spec": map[string]any{
						"featureGates": []any{
							map[string]any{"name": "FeatureGate1"},
							map[string]any{"name": "FeatureGate2"},
							map[string]any{"name": "ExperimentalFeature"},
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
			name: "disabled feature gate",
			hco: &unstructured.Unstructured{
				Object: map[string]any{
					"spec": map[string]any{
						"featureGates": []any{
							map[string]any{"name": "EnabledGate"},
							map[string]any{"name": "DisabledGate", "state": "Disabled"},
						},
					},
				},
			},
			want: map[string]bool{
				"EnabledGate":  true,
				"DisabledGate": false,
			},
		},
		{
			name: "feature gate with no state defaults to enabled",
			hco: &unstructured.Unstructured{
				Object: map[string]any{
					"spec": map[string]any{
						"featureGates": []any{
							map[string]any{"name": "NoStateGate"},
						},
					},
				},
			},
			want: map[string]bool{
				"NoStateGate": true,
			},
		},
		{
			name: "empty feature gates",
			hco: &unstructured.Unstructured{
				Object: map[string]any{
					"spec": map[string]any{
						"featureGates": []any{},
					},
				},
			},
			want: map[string]bool{},
		},
		{
			name: "no feature gates field",
			hco: &unstructured.Unstructured{
				Object: map[string]any{
					"spec": map[string]any{},
				},
			},
			want: map[string]bool{},
		},
		{
			name: "no spec field",
			hco: &unstructured.Unstructured{
				Object: map[string]any{},
			},
			want: map[string]bool{},
		},
		{
			name: "single feature gate",
			hco: &unstructured.Unstructured{
				Object: map[string]any{
					"spec": map[string]any{
						"featureGates": []any{
							map[string]any{"name": "SingleFeature"},
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
			got := resources.ExtractFeatureGates(tt.hco)

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

func TestExtractKVFeatureGate(t *testing.T) {
	tests := []struct {
		name string
		kv   *unstructured.Unstructured
		want []string
	}{
		{
			name: "with feature gates",
			kv: &unstructured.Unstructured{
				Object: map[string]any{
					"spec": map[string]any{
						"configuration": map[string]any{
							"developerConfiguration": map[string]any{
								"featureGates": []any{
									"FeatureGate1",
									"FeatureGate2",
									"ExperimentalFeature",
								},
							},
						},
					},
				},
			},
			want: []string{
				"FeatureGate1",
				"FeatureGate2",
				"ExperimentalFeature",
			},
		},
		{
			name: "single feature gate",
			kv: &unstructured.Unstructured{
				Object: map[string]any{
					"spec": map[string]any{
						"configuration": map[string]any{
							"developerConfiguration": map[string]any{
								"featureGates": []any{"SingleFeature"},
							},
						},
					},
				},
			},
			want: []string{"SingleFeature"},
		},
		{
			name: "empty feature gates",
			kv: &unstructured.Unstructured{
				Object: map[string]any{
					"spec": map[string]any{
						"configuration": map[string]any{
							"developerConfiguration": map[string]any{
								"featureGates": []any{},
							},
						},
					},
				},
			},
			want: nil,
		},
		{
			name: "no developerConfiguration field",
			kv: &unstructured.Unstructured{
				Object: map[string]any{
					"spec": map[string]any{
						"configuration": map[string]any{},
					},
				},
			},
			want: nil,
		},
		{
			name: "no configuration field",
			kv: &unstructured.Unstructured{
				Object: map[string]any{
					"spec": map[string]any{},
				},
			},
			want: nil,
		},
		{
			name: "no spec field",
			kv: &unstructured.Unstructured{
				Object: map[string]any{},
			},
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resources.ExtractKVFeatureGate(tt.kv)

			if !slices.Equal(got, tt.want) {
				t.Errorf("ExtractKVFeatureGate() returned %#v gates, want %#v", got, tt.want)
			}
		})
	}
}
