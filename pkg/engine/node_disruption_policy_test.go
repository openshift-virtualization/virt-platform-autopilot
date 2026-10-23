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
	"sigs.k8s.io/controller-runtime/pkg/client"
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

func TestNodeDisruptionPolicyEffective(t *testing.T) {
	policyAsset := &assets.AssetMetadata{Name: "test-policy", Path: "active/policy.yaml"}
	loader := assets.NewLoaderFromFS(fstest.MapFS{
		"active/policy.yaml": &fstest.MapFile{Data: []byte(testNodeDisruptionPolicy)},
	})
	renderCtx := pkgcontext.NewRenderContext(pkgcontext.NewMockHCO("kubevirt-hyperconverged", "openshift-cnv"))
	policy, err := NewPatcher(nil, nil, loader).renderNodeDisruptionPolicy(policyAsset, renderCtx)
	if err != nil {
		t.Fatalf("render node disruption policy: %v", err)
	}

	t.Run("waits for exact effective policy", func(t *testing.T) {
		p := NewPatcher(fake.NewClientBuilder().WithObjects(testMachineConfigurationPolicyStatus("other.service")).Build(), nil, loader)
		ready, err := p.nodeDisruptionPolicyEffective(context.Background(), policy)
		if err != nil {
			t.Fatal(err)
		}
		if ready {
			t.Fatal("policy should not be ready when an action differs")
		}
	})

	t.Run("accepts matching files units and sshkey", func(t *testing.T) {
		p := NewPatcher(fake.NewClientBuilder().WithObjects(testMachineConfigurationPolicyStatus("tune.service")).Build(), nil, loader)
		ready, err := p.nodeDisruptionPolicyEffective(context.Background(), policy)
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
		if got := machineConfig.GetAnnotations()[NodeDisruptionPolicyCoalescingBypassAnnotation]; got != "true" {
			t.Errorf("bypass annotation = %q, want true", got)
		}
		machineConfig.SetAnnotations(map[string]string{
			MachineConfigCoalescingBypassAnnotation:        "false",
			NodeDisruptionPolicyCoalescingBypassAnnotation: "true",
		})
		if !machineConfigCoalescingBypassed(machineConfig, renderCtx.HCO) {
			t.Fatal("effective NDP bypass must not be overridden by the administrator-controlled annotation")
		}
	})

	t.Run("waits while MachineConfiguration is absent", func(t *testing.T) {
		p := NewPatcher(fake.NewClientBuilder().Build(), nil, loader)
		ready, err := p.nodeDisruptionPolicyEffective(context.Background(), policy)
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

func TestAggregateNodeDisruptionPolicies(t *testing.T) {
	loader, allAssets := testNodeDisruptionPolicyCatalog(t, map[string]string{
		"active/policy.yaml": testNodeDisruptionPolicy,
		"active/worker.yaml": testNodeDisruptionPolicyMachineConfig("worker"),
		"active/master.yaml": testNodeDisruptionPolicyMachineConfig("master"),
	}, `
  - name: policy
    path: active/policy.yaml
    phase: 1
    install: always
    component: MachineConfiguration
    reconcile_order: 1
  - name: worker
    path: active/worker.yaml
    phase: 1
    install: always
    component: MachineConfig
    reconcile_order: 2
    node_disruption_policy:
      asset: policy
  - name: master
    path: active/master.yaml
    phase: 1
    install: always
    component: MachineConfig
    reconcile_order: 2
    node_disruption_policy:
      asset: policy`)
	p := NewPatcher(fake.NewClientBuilder().Build(), nil, loader)
	renderCtx := pkgcontext.NewRenderContext(pkgcontext.NewMockHCO("kubevirt-hyperconverged", "openshift-cnv"))

	assertEntries := func(t *testing.T, active map[string]bool, want int) {
		t.Helper()
		policy, _, err := p.aggregateNodeDisruptionPolicies(allAssets, active, false, renderCtx)
		if err != nil || policy == nil {
			t.Fatalf("aggregate policy: policy=%#v err=%v", policy, err)
		}
		for _, field := range []string{"files", "units"} {
			entries, found, err := unstructured.NestedSlice(policy.Object, "spec", "nodeDisruptionPolicy", field)
			if err != nil || !found || len(entries) != want {
				t.Errorf("%s = %#v, found=%t, err=%v; want %d entries", field, entries, found, err, want)
			}
		}
	}

	// Both MachineConfigs share the same map-list keys, so their identical
	// entries collapse to one rather than racing through separate SSA applies.
	assertEntries(t, map[string]bool{"worker": true, "master": true}, 1)
	// The entry remains while any active dependent still references it.
	assertEntries(t, map[string]bool{"worker": true}, 1)
	// Once the final dependent is inactive, empty map-lists remove the entries
	// owned by Autopilot without affecting entries owned by other managers.
	assertEntries(t, map[string]bool{}, 0)
}

func TestNodeDisruptionPolicyChangeCount(t *testing.T) {
	policy := func(files, units []any) *unstructured.Unstructured {
		return &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": machineConfigurationAPIVersion,
			"kind":       machineConfigurationKind,
			"metadata":   map[string]any{"name": machineConfigurationName},
			"spec": map[string]any{"nodeDisruptionPolicy": map[string]any{
				"files": files,
				"units": units,
			}},
		}}
	}
	file := func(path string, actions ...string) map[string]any {
		acts := make([]any, 0, len(actions))
		for _, action := range actions {
			acts = append(acts, map[string]any{"type": action})
		}
		return map[string]any{"path": path, "actions": acts}
	}
	unit := func(name string, actions ...string) map[string]any {
		acts := make([]any, 0, len(actions))
		for _, action := range actions {
			acts = append(acts, map[string]any{"type": action})
		}
		return map[string]any{"name": name, "actions": acts}
	}

	live := policy(
		[]any{file("/usr/local/bin/tune", "Restart")},
		[]any{unit("tune.service", "Restart"), unit("other.service", "Reload")},
	)
	desired := policy(
		[]any{file("/usr/local/bin/tune", "None")},
		[]any{unit("tune.service", "Restart")},
	)
	if got := nodeDisruptionPolicyChangeCount(desired, live); got != 2 {
		t.Fatalf("change count = %d, want 2", got)
	}
	empty := policy([]any{}, []any{})
	if got := nodeDisruptionPolicyChangeCount(empty, live); got != 3 {
		t.Fatalf("prune change count = %d, want 3", got)
	}
	if got := nodeDisruptionPolicyChangeCount(desired, nil); got != 2 {
		t.Fatalf("create change count = %d, want 2", got)
	}
	if got := nodeDisruptionPolicyChangeCount(desired, desired); got != 0 {
		t.Fatalf("identical change count = %d, want 0", got)
	}
}

func TestReconcileNodeDisruptionPoliciesDoesNotCreateEmptySingleton(t *testing.T) {
	p := NewPatcher(fake.NewClientBuilder().Build(), nil, assets.NewLoaderFromFS(fstest.MapFS{}))
	managed, _, err := p.ReconcileNodeDisruptionPolicies(
		context.Background(), nil, nil, false,
		pkgcontext.NewRenderContext(pkgcontext.NewMockHCO("kubevirt-hyperconverged", "openshift-cnv")),
	)
	if err != nil {
		t.Fatal(err)
	}
	if managed {
		t.Fatal("an empty aggregate must not create an unmanaged singleton")
	}
}

func TestReconcileNodeDisruptionPoliciesDoesNotPruneUnmanagedSingleton(t *testing.T) {
	live := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": machineConfigurationAPIVersion,
		"kind":       machineConfigurationKind,
		"metadata":   map[string]any{"name": machineConfigurationName},
		"spec": map[string]any{"nodeDisruptionPolicy": map[string]any{
			"files": []any{map[string]any{"path": "/etc/admin-owned"}},
			"units": []any{},
		}},
	}}
	p := NewPatcher(fake.NewClientBuilder().WithObjects(live).Build(), nil, assets.NewLoaderFromFS(fstest.MapFS{}))
	managed, _, err := p.ReconcileNodeDisruptionPolicies(
		context.Background(), nil, nil, false,
		pkgcontext.NewRenderContext(pkgcontext.NewMockHCO("kubevirt-hyperconverged", "openshift-cnv")),
	)
	if err != nil {
		t.Fatal(err)
	}
	if managed {
		t.Fatal("an empty aggregate must not prune an unmanaged singleton")
	}
	after := &unstructured.Unstructured{}
	after.SetAPIVersion(machineConfigurationAPIVersion)
	after.SetKind(machineConfigurationKind)
	if err := p.client.Get(context.Background(), client.ObjectKey{Name: machineConfigurationName}, after); err != nil {
		t.Fatal(err)
	}
	files, found, err := unstructured.NestedSlice(after.Object, "spec", "nodeDisruptionPolicy", "files")
	if err != nil || !found || len(files) != 1 {
		t.Fatalf("unmanaged policy files = %#v, found=%t, err=%v; want preserved entry", files, found, err)
	}
}

func TestAggregateNodeDisruptionPoliciesDropsRootExcludedDependent(t *testing.T) {
	loader, allAssets := testNodeDisruptionPolicyCatalog(t, map[string]string{
		"active/policy.yaml": testNodeDisruptionPolicy,
		"active/worker.yaml": testNodeDisruptionPolicyMachineConfig("worker"),
	}, `
  - name: policy
    path: active/policy.yaml
    phase: 1
    install: always
    component: MachineConfiguration
    reconcile_order: 1
  - name: worker
    path: active/worker.yaml
    phase: 1
    install: always
    component: MachineConfig
    reconcile_order: 2
    node_disruption_policy:
      asset: policy`)
	p := NewPatcher(fake.NewClientBuilder().Build(), nil, loader)
	hco := pkgcontext.NewMockHCO("kubevirt-hyperconverged", "openshift-cnv")
	hco.SetAnnotations(map[string]string{DisabledResourcesAnnotation: "- kind: MachineConfig\n  name: test-worker\n"})
	renderCtx := pkgcontext.NewRenderContext(hco)

	_, managed, err := p.aggregateNodeDisruptionPolicies(allAssets, map[string]bool{"worker": true}, false, renderCtx)
	if err != nil {
		t.Fatal(err)
	}
	if managed {
		t.Fatal("excluded-only dependents must not claim the MachineConfiguration singleton")
	}
}

func TestAggregateNodeDisruptionPoliciesPreservesRootExcludedSingleton(t *testing.T) {
	loader, allAssets := testNodeDisruptionPolicyCatalog(t, map[string]string{
		"active/policy.yaml": testNodeDisruptionPolicy,
		"active/worker.yaml": testNodeDisruptionPolicyMachineConfig("worker"),
	}, `
  - name: policy
    path: active/policy.yaml
    phase: 1
    install: always
    component: MachineConfiguration
    reconcile_order: 1
  - name: worker
    path: active/worker.yaml
    phase: 1
    install: always
    component: MachineConfig
    reconcile_order: 2
    node_disruption_policy:
      asset: policy`)
	p := NewPatcher(fake.NewClientBuilder().Build(), nil, loader)
	hco := pkgcontext.NewMockHCO("kubevirt-hyperconverged", "openshift-cnv")
	hco.SetAnnotations(map[string]string{DisabledResourcesAnnotation: "- kind: MachineConfiguration\n  name: cluster\n"})

	_, managed, err := p.aggregateNodeDisruptionPolicies(allAssets, map[string]bool{}, false, pkgcontext.NewRenderContext(hco))
	if err != nil {
		t.Fatal(err)
	}
	if managed {
		t.Fatal("root-excluded MachineConfiguration must not be pruned")
	}
}

func TestAggregateNodeDisruptionPoliciesRejectsConflictingEntries(t *testing.T) {
	loader, allAssets := testNodeDisruptionPolicyCatalog(t, map[string]string{
		"active/policy-a.yaml":   testNodeDisruptionPolicy,
		"active/policy-b.yaml":   strings.Replace(testNodeDisruptionPolicy, "type: Restart", "type: Drain", 1),
		"active/consumer-a.yaml": testNodeDisruptionPolicyMachineConfig("a"),
		"active/consumer-b.yaml": testNodeDisruptionPolicyMachineConfig("b"),
	}, `
  - name: policy-a
    path: active/policy-a.yaml
    phase: 1
    install: always
    component: MachineConfiguration
    reconcile_order: 1
  - name: policy-b
    path: active/policy-b.yaml
    phase: 1
    install: always
    component: MachineConfiguration
    reconcile_order: 1
  - name: consumer-a
    path: active/consumer-a.yaml
    phase: 1
    install: always
    component: MachineConfig
    reconcile_order: 2
    node_disruption_policy:
      asset: policy-a
  - name: consumer-b
    path: active/consumer-b.yaml
    phase: 1
    install: always
    component: MachineConfig
    reconcile_order: 2
    node_disruption_policy:
      asset: policy-b`)
	p := NewPatcher(fake.NewClientBuilder().Build(), nil, loader)
	renderCtx := pkgcontext.NewRenderContext(pkgcontext.NewMockHCO("kubevirt-hyperconverged", "openshift-cnv"))

	_, _, err := p.aggregateNodeDisruptionPolicies(allAssets, map[string]bool{"consumer-a": true, "consumer-b": true}, false, renderCtx)
	if err == nil || !strings.Contains(err.Error(), "conflicts between active assets") {
		t.Fatalf("aggregate conflicting entries error = %v, want conflict", err)
	}
}

func TestNodeDisruptionPolicyGenerationObserved(t *testing.T) {
	statusPolicy := map[string]any{"files": []any{}, "units": []any{}}
	policy := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": machineConfigurationAPIVersion,
		"kind":       machineConfigurationKind,
		"metadata": map[string]any{
			"name":       machineConfigurationName,
			"generation": int64(4),
		},
		"status": map[string]any{
			"observedGeneration":         int64(3),
			"nodeDisruptionPolicyStatus": map[string]any{"clusterPolicies": statusPolicy},
		},
	}}
	p := NewPatcher(fake.NewClientBuilder().WithObjects(policy).Build(), nil, assets.NewLoaderFromFS(fstest.MapFS{}))

	observed, err := p.NodeDisruptionPolicyGenerationObserved(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if observed {
		t.Fatal("MCO must observe the current generation before deletion can proceed")
	}

	_ = unstructured.SetNestedField(policy.Object, int64(4), "status", "observedGeneration")
	p = NewPatcher(fake.NewClientBuilder().WithObjects(policy).Build(), nil, assets.NewLoaderFromFS(fstest.MapFS{}))
	observed, err = p.NodeDisruptionPolicyGenerationObserved(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !observed {
		t.Fatal("matching observed generation must release deletion")
	}

}

func TestAggregateNodeDisruptionPolicyPrunesForTombstoneWithoutPolicyAsset(t *testing.T) {
	loader, allAssets := testNodeDisruptionPolicyCatalog(t, map[string]string{
		"active/policy.yaml": testNodeDisruptionPolicy,
	}, `
  - name: policy
    path: active/policy.yaml
    phase: 1
    install: always
    component: MachineConfiguration
    reconcile_order: 1`)
	p := NewPatcher(fake.NewClientBuilder().Build(), nil, loader)
	ctx := pkgcontext.NewRenderContext(pkgcontext.NewMockHCO("kubevirt-hyperconverged", "openshift-cnv"))

	aggregate, managed, err := p.aggregateNodeDisruptionPolicies(allAssets, nil, true, ctx)
	if err != nil || !managed {
		t.Fatalf("aggregate tombstone pruning policy: managed=%t err=%v", managed, err)
	}
	entries, found, err := unstructured.NestedSlice(aggregate.Object, "spec", "nodeDisruptionPolicy", "files")
	if err != nil || !found || len(entries) != 0 {
		t.Errorf("tombstone pruning files = %#v, found=%t, err=%v; want empty", entries, found, err)
	}
}

func TestAggregateNodeDisruptionPolicyOverridesBlockTombstoneRelease(t *testing.T) {
	desired := func() *unstructured.Unstructured {
		return &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": machineConfigurationAPIVersion,
			"kind":       machineConfigurationKind,
			"metadata":   map[string]any{"name": machineConfigurationName},
			"spec": map[string]any{"nodeDisruptionPolicy": map[string]any{
				"files": []any{}, "units": []any{},
			}},
		}}
	}
	for name, annotations := range map[string]map[string]string{
		"ignore": {"platform.kubevirt.io/ignore-fields": "/spec/nodeDisruptionPolicy/files"},
	} {
		t.Run(name, func(t *testing.T) {
			live := desired()
			live.SetAnnotations(annotations)
			p := NewPatcher(fake.NewClientBuilder().WithObjects(live).Build(), nil, assets.NewLoaderFromFS(fstest.MapFS{}))
			managed, _, err := p.reconcileAggregateNodeDisruptionPolicy(context.Background(), desired(), true)
			if err != nil {
				t.Fatal(err)
			}
			if managed {
				t.Fatal("a policy override must keep dependent tombstones pending")
			}
		})
	}
}

func testNodeDisruptionPolicyCatalog(t *testing.T, files map[string]string, metadata string) (*assets.Loader, []assets.AssetMetadata) {
	t.Helper()
	fs := fstest.MapFS{
		"active/metadata.yaml": &fstest.MapFile{Data: []byte("assets:\n" + metadata + "\nfeatures: []\n")},
	}
	for path, content := range files {
		fs[path] = &fstest.MapFile{Data: []byte(content)}
	}
	loader := assets.NewLoaderFromFS(fs)
	registry, err := assets.NewRegistry(loader)
	if err != nil {
		t.Fatalf("create test asset catalog: %v", err)
	}
	return loader, registry.ListAssetsByReconcileOrder()
}

func testNodeDisruptionPolicyMachineConfig(role string) string {
	return `apiVersion: machineconfiguration.openshift.io/v1
kind: MachineConfig
metadata:
  name: test-` + role + `
spec:
  config:
    ignition:
      version: 3.5.0
    storage:
      files:
      - path: /usr/local/bin/tune
    systemd:
      units:
      - name: tune.service
`
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

// TestCatalogNodeDisruptionPoliciesAreValid turns authoring errors into CI
// failures instead of allowing an invalid policy/dependent pair to stop the
// platform reconciliation loop on a customer cluster.
//
// Coverage is bidirectional: every MachineConfig file/unit must appear in its
// policy, and every policy file/unit must appear on at least one dependent so
// removed Ignition paths cannot leave stale Node Disruption Policy entries.
func TestCatalogNodeDisruptionPoliciesAreValid(t *testing.T) { //nolint:gocognit // catalog walk is inherently nested
	loader := assets.NewLoader()
	registry, err := assets.NewRegistry(loader)
	if err != nil {
		t.Fatal(err)
	}
	renderer := NewRenderer(loader)
	ctx := pkgcontext.NewRenderContext(pkgcontext.NewMockHCO("kubevirt-hyperconverged", "openshift-cnv"))

	type policyCoverage struct {
		policyAsset *assets.AssetMetadata
		files       map[string]struct{}
		units       map[string]struct{}
		dependents  int
	}
	byPolicy := map[string]*policyCoverage{}
	active := map[string]bool{}

	for _, asset := range registry.ListAssetsByReconcileOrder() {
		if asset.NodeDisruptionPolicy == nil {
			continue
		}
		active[asset.Name] = true
		policyMeta := asset.NodeDisruptionPolicy.PolicyAsset()
		cov, ok := byPolicy[policyMeta.Name]
		if !ok {
			cov = &policyCoverage{
				policyAsset: policyMeta,
				files:       map[string]struct{}{},
				units:       map[string]struct{}{},
			}
			byPolicy[policyMeta.Name] = cov
		}

		t.Run(asset.Name, func(t *testing.T) {
			dependentCtx := *ctx
			dependent, err := renderer.RenderAsset(&asset, &dependentCtx)
			if err != nil {
				t.Fatal(err)
			}
			if dependent == nil {
				t.Fatal("NDP dependent rendered empty")
			}
			policyCtx := *ctx
			policy, err := renderer.RenderAsset(policyMeta, &policyCtx)
			if err != nil {
				t.Fatal(err)
			}
			if policy == nil || policy.GetAPIVersion() != machineConfigurationAPIVersion || policy.GetKind() != machineConfigurationKind || policy.GetName() != machineConfigurationName {
				t.Fatalf("policy = %#v, want MachineConfiguration/cluster", policy)
			}
			want, found, err := unstructured.NestedMap(policy.Object, "spec", "nodeDisruptionPolicy")
			if err != nil || !found {
				t.Fatalf("policy has no spec.nodeDisruptionPolicy: found=%t err=%v", found, err)
			}
			if err := nodeDisruptionPolicyCoversMachineConfig(want, dependent); err != nil {
				t.Fatal(err)
			}

			files, units, err := machineConfigNodeDisruptionKeys(dependent)
			if err != nil {
				t.Fatal(err)
			}
			cov.dependents++
			for path := range files {
				cov.files[path] = struct{}{}
			}
			for name := range units {
				cov.units[name] = struct{}{}
			}
		})
	}

	for policyName, cov := range byPolicy {
		t.Run("policy/"+policyName, func(t *testing.T) {
			if cov.dependents == 0 {
				t.Fatal("node disruption policy has no MachineConfig dependents")
			}
			policyCtx := *ctx
			policy, err := renderer.RenderAsset(cov.policyAsset, &policyCtx)
			if err != nil {
				t.Fatal(err)
			}
			want, found, err := unstructured.NestedMap(policy.Object, "spec", "nodeDisruptionPolicy")
			if err != nil || !found {
				t.Fatalf("policy has no spec.nodeDisruptionPolicy: found=%t err=%v", found, err)
			}
			policyFiles, err := nodeDisruptionPolicyNamedKeys(want, "files", "path")
			if err != nil {
				t.Fatal(err)
			}
			policyUnits, err := nodeDisruptionPolicyNamedKeys(want, "units", "name")
			if err != nil {
				t.Fatal(err)
			}
			for path := range policyFiles {
				if _, ok := cov.files[path]; !ok {
					t.Errorf("policy files path %q is not present on any dependent MachineConfig", path)
				}
			}
			for name := range policyUnits {
				if _, ok := cov.units[name]; !ok {
					t.Errorf("policy units name %q is not present on any dependent MachineConfig", name)
				}
			}
		})
	}

	p := NewPatcher(fake.NewClientBuilder().Build(), nil, loader)
	if _, _, err := p.aggregateNodeDisruptionPolicies(registry.ListAssetsByReconcileOrder(), active, false, ctx); err != nil {
		t.Fatalf("catalog contains conflicting node disruption policy entries: %v", err)
	}
}

func machineConfigNodeDisruptionKeys(machineConfig *unstructured.Unstructured) (files, units map[string]struct{}, err error) {
	files = map[string]struct{}{}
	units = map[string]struct{}{}
	config, found, err := unstructured.NestedMap(machineConfig.Object, "spec", "config")
	if err != nil || !found {
		return nil, nil, err
	}
	fileEntries, found, err := unstructured.NestedSlice(config, "storage", "files")
	if err != nil {
		return nil, nil, err
	}
	if found {
		for _, entry := range fileEntries {
			entryMap, ok := entry.(map[string]any)
			if !ok {
				continue
			}
			path, found, err := unstructured.NestedString(entryMap, "path")
			if err != nil || !found || path == "" {
				continue
			}
			files[path] = struct{}{}
		}
	}
	unitEntries, found, err := unstructured.NestedSlice(config, "systemd", "units")
	if err != nil {
		return nil, nil, err
	}
	if found {
		for _, entry := range unitEntries {
			entryMap, ok := entry.(map[string]any)
			if !ok {
				continue
			}
			name, found, err := unstructured.NestedString(entryMap, "name")
			if err != nil || !found || name == "" {
				continue
			}
			units[name] = struct{}{}
		}
	}
	return files, units, nil
}

func nodeDisruptionPolicyNamedKeys(policy map[string]any, field, identifier string) (map[string]struct{}, error) {
	out := map[string]struct{}{}
	entries, found, err := unstructured.NestedSlice(policy, field)
	if err != nil || !found {
		return out, err
	}
	for _, entry := range entries {
		entryMap, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		key, found, err := unstructured.NestedString(entryMap, identifier)
		if err != nil || !found || key == "" {
			continue
		}
		out[key] = struct{}{}
	}
	return out, nil
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
