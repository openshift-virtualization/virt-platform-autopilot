/*
Copyright 2026 The Virt Platform Autopilot Authors.

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
	"context"
	"strings"
	"testing"
	"testing/fstest"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/kubevirt/virt-platform-autopilot/pkg/assets"
	pkgcontext "github.com/kubevirt/virt-platform-autopilot/pkg/context"
)

const testNodeDisruptionPolicy = `apiVersion: operator.openshift.io/v1
kind: MachineConfiguration
metadata:
  name: cluster
spec:
  nodeDisruptionPolicy:
    files:
    - path: /usr/local/bin/tune
      actions:
      - type: Restart
        restart:
          serviceName: tune.service
    units:
    - name: tune.service
      actions:
      - type: DaemonReload
      - type: Restart
        restart:
          serviceName: tune.service
    sshkey:
      actions:
      - type: None
`

func TestNodeDisruptionPolicyReady(t *testing.T) {
	policyAsset := &assets.AssetMetadata{Name: "test-policy", Path: "active/policy.yaml"}
	loader := assets.NewLoaderFromFS(fstest.MapFS{
		"active/policy.yaml": &fstest.MapFile{Data: []byte(testNodeDisruptionPolicy)},
	})
	renderCtx := pkgcontext.NewRenderContext(pkgcontext.NewMockHCO("kubevirt-hyperconverged", "openshift-cnv"))

	t.Run("waits for exact effective policy", func(t *testing.T) {
		p := NewPatcher(fake.NewClientBuilder().WithObjects(testMachineConfigurationPolicyStatus("other.service")).Build(), nil, loader)
		ready, err := p.nodeDisruptionPolicyReady(context.Background(), policyAsset, renderCtx)
		if err != nil {
			t.Fatal(err)
		}
		if ready {
			t.Fatal("policy should not be ready when an action differs")
		}
	})

	t.Run("accepts matching files units and sshkey", func(t *testing.T) {
		p := NewPatcher(fake.NewClientBuilder().WithObjects(testMachineConfigurationPolicyStatus("tune.service")).Build(), nil, loader)
		ready, err := p.nodeDisruptionPolicyReady(context.Background(), policyAsset, renderCtx)
		if err != nil {
			t.Fatal(err)
		}
		if !ready {
			t.Fatal("policy should be ready after MCO publishes every requested entry")
		}

		machineConfig := &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "machineconfiguration.openshift.io/v1",
			"kind":       "MachineConfig",
			"metadata":   map[string]any{"name": "test"},
		}}
		ready, err = p.prepareNodeDisruptionPolicyDependentMachineConfig(context.Background(), machineConfig, policyAsset, renderCtx)
		if err != nil || !ready {
			t.Fatalf("prepare dependent MachineConfig: ready=%t err=%v", ready, err)
		}
		if got := machineConfig.GetAnnotations()[MachineConfigCoalescingBypassAnnotation]; got != "true" {
			t.Errorf("bypass annotation = %q, want true", got)
		}
	})

	t.Run("waits while MachineConfiguration is absent", func(t *testing.T) {
		p := NewPatcher(fake.NewClientBuilder().Build(), nil, loader)
		ready, err := p.nodeDisruptionPolicyReady(context.Background(), policyAsset, renderCtx)
		if err != nil {
			t.Fatal(err)
		}
		if ready {
			t.Fatal("policy should not be ready before MachineConfiguration exists")
		}
	})

	t.Run("rejects a dependency on a non-MachineConfig", func(t *testing.T) {
		p := NewPatcher(fake.NewClientBuilder().Build(), nil, loader)
		notMachineConfig := &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata":   map[string]any{"name": "test"},
		}}
		ready, err := p.prepareNodeDisruptionPolicyDependentMachineConfig(context.Background(), notMachineConfig, policyAsset, renderCtx)
		if err == nil || ready {
			t.Fatalf("prepare non-MachineConfig: ready=%t err=%v, want false and error", ready, err)
		}
	})
}

func TestNodeDisruptionPolicyCoversMachineConfig(t *testing.T) {
	policy := map[string]any{
		"files":  []any{map[string]any{"path": "/usr/local/bin/tune"}},
		"units":  []any{map[string]any{"name": "tune.service"}},
		"sshkey": map[string]any{"actions": []any{map[string]any{"type": "None"}}},
	}
	validMachineConfig := func() *unstructured.Unstructured {
		return &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "machineconfiguration.openshift.io/v1",
			"kind":       "MachineConfig",
			"spec": map[string]any{
				"config": map[string]any{
					"ignition": map[string]any{"version": "3.5.0"},
					"storage":  map[string]any{"files": []any{map[string]any{"path": "/usr/local/bin/tune"}}},
					"systemd":  map[string]any{"units": []any{map[string]any{"name": "tune.service"}}},
					"passwd": map[string]any{"users": []any{map[string]any{
						"name":              "core",
						"sshAuthorizedKeys": []any{"ssh-ed25519 AAAA"},
					}}},
				},
			},
		}}
	}

	tests := []struct {
		name    string
		mutate  func(*unstructured.Unstructured, map[string]any)
		wantErr string
	}{
		{
			name: "covers files units and ssh keys",
		},
		{
			name: "rejects uncovered file",
			mutate: func(machineConfig *unstructured.Unstructured, _ map[string]any) {
				_ = unstructured.SetNestedField(machineConfig.Object, []any{map[string]any{"path": "/etc/not-covered"}}, "spec", "config", "storage", "files")
			},
			wantErr: `does not cover MachineConfig files "/etc/not-covered"`,
		},
		{
			name: "rejects uncovered unit",
			mutate: func(machineConfig *unstructured.Unstructured, _ map[string]any) {
				_ = unstructured.SetNestedField(machineConfig.Object, []any{map[string]any{"name": "not-covered.service"}}, "spec", "config", "systemd", "units")
			},
			wantErr: `does not cover MachineConfig units "not-covered.service"`,
		},
		{
			name: "rejects uncovered ssh keys",
			mutate: func(_ *unstructured.Unstructured, policy map[string]any) {
				delete(policy, "sshkey")
			},
			wantErr: "does not cover MachineConfig SSH keys",
		},
		{
			name: "rejects kernel arguments",
			mutate: func(machineConfig *unstructured.Unstructured, _ map[string]any) {
				_ = unstructured.SetNestedStringSlice(machineConfig.Object, []string{"intel_iommu=on"}, "spec", "kernelArguments")
			},
			wantErr: "cannot cover MachineConfig spec.kernelArguments",
		},
		{
			name: "rejects unsupported ignition content",
			mutate: func(machineConfig *unstructured.Unstructured, _ map[string]any) {
				_ = unstructured.SetNestedField(machineConfig.Object, map[string]any{"version": "3.5.0", "security": map[string]any{}}, "spec", "config", "ignition")
			},
			wantErr: "cannot cover MachineConfig spec.config.ignition.security",
		},
		{
			name: "rejects unsupported user change",
			mutate: func(machineConfig *unstructured.Unstructured, _ map[string]any) {
				_ = unstructured.SetNestedField(machineConfig.Object, []any{map[string]any{"name": "core", "passwordHash": "hash"}}, "spec", "config", "passwd", "users")
			},
			wantErr: "cannot cover MachineConfig spec.config.passwd.users.passwordHash",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			machineConfig := validMachineConfig()
			policyCopy := map[string]any{}
			for key, value := range policy {
				policyCopy[key] = value
			}
			if test.mutate != nil {
				test.mutate(machineConfig, policyCopy)
			}
			err := nodeDisruptionPolicyCoversMachineConfig(policyCopy, machineConfig)
			if test.wantErr == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("error = %v, want %q", err, test.wantErr)
			}
		})
	}
}

func testMachineConfigurationPolicyStatus(restartService string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": machineConfigurationAPIVersion,
		"kind":       machineConfigurationKind,
		"metadata":   map[string]any{"name": machineConfigurationName},
		"status": map[string]any{
			"nodeDisruptionPolicyStatus": map[string]any{
				"clusterPolicies": map[string]any{
					"files": []any{map[string]any{
						"path": "/usr/local/bin/tune",
						"actions": []any{map[string]any{
							"type":    "Restart",
							"restart": map[string]any{"serviceName": restartService},
						}},
					}},
					"units": []any{map[string]any{
						"name": "tune.service",
						"actions": []any{
							map[string]any{"type": "DaemonReload"},
							map[string]any{"type": "Restart", "restart": map[string]any{"serviceName": restartService}},
						},
					}},
					"sshkey": map[string]any{"actions": []any{map[string]any{"type": "None"}}},
				},
			},
		},
	}}
}
