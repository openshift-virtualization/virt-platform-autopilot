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
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestValidatePatchSecurity(t *testing.T) {
	tests := []struct {
		name    string
		obj     *unstructured.Unstructured
		wantErr bool
		errMsg  string
	}{
		{
			name:    "nil object",
			obj:     nil,
			wantErr: true,
			errMsg:  "object is nil",
		},
		{
			name: "patch on allowed kind (ConfigMap)",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"kind": "ConfigMap",
					"metadata": map[string]interface{}{
						"annotations": map[string]interface{}{
							PatchAnnotation: `[{"op": "add", "path": "/data/key", "value": "val"}]`,
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "patch on sensitive kind (MachineConfig)",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"kind": "MachineConfig",
					"metadata": map[string]interface{}{
						"annotations": map[string]interface{}{
							PatchAnnotation: `[{"op": "add", "path": "/spec/config", "value": {}}]`,
						},
					},
				},
			},
			wantErr: true,
			errMsg:  "JSON patches are not allowed on sensitive resource kind: MachineConfig",
		},
		{
			name: "no patch on sensitive kind (MachineConfig)",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"kind": "MachineConfig",
					"metadata": map[string]interface{}{
						"annotations": map[string]interface{}{},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "object without annotations",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"kind": "ConfigMap",
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePatchSecurity(tt.obj)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidatePatchSecurity() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr && !strings.Contains(err.Error(), tt.errMsg) {
				t.Errorf("ValidatePatchSecurity() error = %v, want error containing %q", err, tt.errMsg)
			}
		})
	}
}

func TestIsUnmanaged(t *testing.T) {
	tests := []struct {
		name string
		obj  *unstructured.Unstructured
		want bool
	}{
		{
			name: "unmanaged annotation present",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{
						"annotations": map[string]interface{}{
							AnnotationMode: ModeUnmanaged,
						},
					},
				},
			},
			want: true,
		},
		{
			name: "unmanaged annotation with different value",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{
						"annotations": map[string]interface{}{
							AnnotationMode: "managed",
						},
					},
				},
			},
			want: false,
		},
		{
			name: "no mode annotation",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{
						"annotations": map[string]interface{}{},
					},
				},
			},
			want: false,
		},
		{
			name: "no annotations",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{},
				},
			},
			want: false,
		},
		{
			name: "nil object",
			obj:  nil,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsUnmanaged(tt.obj)
			if got != tt.want {
				t.Errorf("IsUnmanaged() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsAutopilotEnabled(t *testing.T) {
	tests := []struct {
		name string
		obj  *unstructured.Unstructured
		want bool
	}{
		{
			name: "annotation set to true",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{
						"annotations": map[string]interface{}{
							AnnotationAutopilotEnabled: "true",
						},
					},
				},
			},
			want: true,
		},
		{
			name: "annotation set to comma-separated asset names",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{
						"annotations": map[string]interface{}{
							AnnotationAutopilotEnabled: "swap-enable,descheduler-loadaware",
						},
					},
				},
			},
			want: true,
		},
		{
			name: "annotation absent",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{
						"annotations": map[string]interface{}{},
					},
				},
			},
			want: false,
		},
		{
			name: "annotation set to empty string",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{
						"annotations": map[string]interface{}{
							AnnotationAutopilotEnabled: "",
						},
					},
				},
			},
			want: false,
		},
		{
			name: "no annotations",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{},
				},
			},
			want: false,
		},
		{
			name: "nil object",
			obj:  nil,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsAutopilotEnabled(tt.obj)
			if got != tt.want {
				t.Errorf("IsAutopilotEnabled() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseAutopilotScope(t *testing.T) {
	hcoWith := func(val string) *unstructured.Unstructured {
		return &unstructured.Unstructured{
			Object: map[string]interface{}{
				"metadata": map[string]interface{}{
					"annotations": map[string]interface{}{
						AnnotationAutopilotEnabled: val,
					},
				},
			},
		}
	}
	noAnnotations := &unstructured.Unstructured{Object: map[string]interface{}{}}

	tests := []struct {
		name        string
		hco         *unstructured.Unstructured
		wantEnabled bool
		wantNilList bool // true = nil allowlist means "all assets"
		wantList    []string
	}{
		{name: "nil object", hco: nil, wantEnabled: false},
		{name: "annotation absent", hco: noAnnotations, wantEnabled: false},
		{name: "annotation empty", hco: hcoWith(""), wantEnabled: false},
		{name: "annotation true", hco: hcoWith("true"), wantEnabled: true, wantNilList: true},
		{name: "single asset name", hco: hcoWith("swap-enable"), wantEnabled: true, wantList: []string{"swap-enable"}},
		{name: "multiple asset names", hco: hcoWith("swap-enable,descheduler-loadaware,node-health-check"), wantEnabled: true, wantList: []string{"swap-enable", "descheduler-loadaware", "node-health-check"}},
		{name: "whitespace trimmed", hco: hcoWith("  swap-enable , descheduler-loadaware  "), wantEnabled: true, wantList: []string{"swap-enable", "descheduler-loadaware"}},
		{name: "hco-golden-config explicit", hco: hcoWith("hco-golden-config,swap-enable"), wantEnabled: true, wantList: []string{"hco-golden-config", "swap-enable"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			allowlist, enabled := ParseAutopilotScope(tt.hco)
			if enabled != tt.wantEnabled {
				t.Fatalf("enabled = %v, want %v", enabled, tt.wantEnabled)
			}
			if !tt.wantEnabled {
				return
			}
			if tt.wantNilList {
				if allowlist != nil {
					t.Errorf("expected nil allowlist (all assets), got %v", allowlist)
				}
				return
			}
			if len(allowlist) != len(tt.wantList) {
				t.Fatalf("allowlist len = %d, want %d: %v", len(allowlist), len(tt.wantList), allowlist)
			}
			for _, name := range tt.wantList {
				if !allowlist[name] {
					t.Errorf("%q missing from allowlist %v", name, allowlist)
				}
			}
		})
	}
}

func TestValidateAnnotations(t *testing.T) {
	tests := []struct {
		name    string
		obj     *unstructured.Unstructured
		wantErr bool
		errMsg  string
	}{
		{
			name:    "nil object",
			obj:     nil,
			wantErr: true,
			errMsg:  "object is nil",
		},
		{
			name: "valid patch annotation",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"kind": "ConfigMap",
					"metadata": map[string]interface{}{
						"annotations": map[string]interface{}{
							PatchAnnotation: `[{"op": "add", "path": "/data/key", "value": "val"}]`,
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "invalid patch annotation (malformed JSON)",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"kind": "ConfigMap",
					"metadata": map[string]interface{}{
						"annotations": map[string]interface{}{
							PatchAnnotation: `invalid json`,
						},
					},
				},
			},
			wantErr: true,
			errMsg:  "invalid patch annotation",
		},
		{
			name: "patch on sensitive kind",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"kind": "MachineConfig",
					"metadata": map[string]interface{}{
						"annotations": map[string]interface{}{
							PatchAnnotation: `[{"op": "add", "path": "/spec/config", "value": {}}]`,
						},
					},
				},
			},
			wantErr: true,
			errMsg:  "JSON patches are not allowed on sensitive resource kind",
		},
		{
			name: "valid ignore-fields annotation",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"kind": "ConfigMap",
					"metadata": map[string]interface{}{
						"annotations": map[string]interface{}{
							AnnotationIgnoreFields: "/spec/replicas,/metadata/labels",
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "invalid ignore-fields annotation",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"kind": "ConfigMap",
					"metadata": map[string]interface{}{
						"annotations": map[string]interface{}{
							AnnotationIgnoreFields: "not-a-pointer",
						},
					},
				},
			},
			wantErr: true,
			errMsg:  "invalid ignore-fields annotation",
		},
		{
			name: "valid unmanaged mode",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"kind": "ConfigMap",
					"metadata": map[string]interface{}{
						"annotations": map[string]interface{}{
							AnnotationMode: ModeUnmanaged,
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "invalid mode annotation",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"kind": "ConfigMap",
					"metadata": map[string]interface{}{
						"annotations": map[string]interface{}{
							AnnotationMode: "invalid-mode",
						},
					},
				},
			},
			wantErr: true,
			errMsg:  "invalid mode annotation",
		},
		{
			name: "no annotations",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"kind": "ConfigMap",
				},
			},
			wantErr: false,
		},
		{
			name: "all valid annotations together",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"kind": "Deployment",
					"metadata": map[string]interface{}{
						"annotations": map[string]interface{}{
							PatchAnnotation:        `[{"op": "replace", "path": "/spec/replicas", "value": 3}]`,
							AnnotationIgnoreFields: "/metadata/labels/app",
							AnnotationMode:         ModeUnmanaged,
						},
					},
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateAnnotations(tt.obj)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateAnnotations() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr && !strings.Contains(err.Error(), tt.errMsg) {
				t.Errorf("ValidateAnnotations() error = %v, want error containing %q", err, tt.errMsg)
			}
		})
	}
}
