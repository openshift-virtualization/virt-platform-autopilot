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

package rbac

import (
	"testing"
	"testing/fstest"
)

// ---- StaticRules ----

func TestStaticRules_Count(t *testing.T) {
	rules := StaticRules()
	if len(rules) != 7 {
		t.Errorf("expected 7 static rules, got %d", len(rules))
	}
}

func TestStaticRules_NoDuplicateAPIGroups(t *testing.T) {
	seen := map[string]bool{}
	for _, r := range StaticRules() {
		for _, g := range r.APIGroups {
			key := g + "|" + r.Resources[0]
			if seen[key] {
				t.Errorf("duplicate apiGroup/resource in static rules: %s", key)
			}
			seen[key] = true
		}
	}
}

// ---- collectRoleRules ----

func TestCollectRoleRules_ClusterRole(t *testing.T) {
	yaml := `
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: test-role
rules:
- apiGroups: ["apps"]
  resources: ["deployments"]
  verbs: ["get", "list"]
`
	var collected []policyRule
	collectRoleRules([]byte(yaml), &collected)
	if len(collected) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(collected))
	}
	if collected[0].APIGroups[0] != "apps" {
		t.Errorf("expected apiGroup 'apps', got %q", collected[0].APIGroups[0])
	}
}

func TestCollectRoleRules_Role(t *testing.T) {
	yaml := `
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: test-role
rules:
- apiGroups: [""]
  resources: ["configmaps"]
  verbs: ["get"]
`
	var collected []policyRule
	collectRoleRules([]byte(yaml), &collected)
	if len(collected) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(collected))
	}
	if collected[0].Resources[0] != "configmaps" {
		t.Errorf("expected resource 'configmaps', got %q", collected[0].Resources[0])
	}
}

func TestCollectRoleRules_IgnoresNonRoleKinds(t *testing.T) {
	yaml := `
apiVersion: kubevirt.io/v1
kind: HyperConverged
metadata:
  name: kubevirt-hyperconverged
`
	var collected []policyRule
	collectRoleRules([]byte(yaml), &collected)
	if len(collected) != 0 {
		t.Errorf("expected no rules from non-role kind, got %d", len(collected))
	}
}

func TestCollectRoleRules_MultiDoc(t *testing.T) {
	yaml := `
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: role-a
rules:
- apiGroups: ["apps"]
  resources: ["deployments"]
  verbs: ["get"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: role-b
rules:
- apiGroups: [""]
  resources: ["pods"]
  verbs: ["list"]
`
	var collected []policyRule
	collectRoleRules([]byte(yaml), &collected)
	if len(collected) != 2 {
		t.Errorf("expected 2 rules from multi-doc, got %d", len(collected))
	}
}

func TestCollectRoleRules_InvalidYAMLSkipped(t *testing.T) {
	yaml := `not: valid: yaml: [{`
	var collected []policyRule
	collectRoleRules([]byte(yaml), &collected)
	if len(collected) != 0 {
		t.Errorf("expected 0 rules from invalid YAML, got %d", len(collected))
	}
}

// ---- mergeTransitiveRules ----

func TestMergeTransitiveRules_Empty(t *testing.T) {
	result := mergeTransitiveRules(nil)
	if len(result) != 0 {
		t.Errorf("expected empty result, got %d rules", len(result))
	}
}

func TestMergeTransitiveRules_SingleRule(t *testing.T) {
	input := []policyRule{
		{APIGroups: []string{"apps"}, Resources: []string{"deployments"}, Verbs: []string{"get", "list"}},
	}
	result := mergeTransitiveRules(input)
	if len(result) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(result))
	}
	if result[0].APIGroups[0] != "apps" {
		t.Errorf("expected apiGroup 'apps', got %q", result[0].APIGroups[0])
	}
}

func TestMergeTransitiveRules_MergesResourcesWithinSameAPIGroup(t *testing.T) {
	input := []policyRule{
		{APIGroups: []string{""}, Resources: []string{"pods"}, Verbs: []string{"get"}},
		{APIGroups: []string{""}, Resources: []string{"services"}, Verbs: []string{"list"}},
	}
	result := mergeTransitiveRules(input)
	if len(result) != 1 {
		t.Fatalf("expected 1 merged rule, got %d", len(result))
	}
	if len(result[0].Resources) != 2 {
		t.Errorf("expected 2 resources, got %d: %v", len(result[0].Resources), result[0].Resources)
	}
}

func TestMergeTransitiveRules_UnionsVerbs(t *testing.T) {
	input := []policyRule{
		{APIGroups: []string{""}, Resources: []string{"pods"}, Verbs: []string{"get", "list"}},
		{APIGroups: []string{""}, Resources: []string{"pods"}, Verbs: []string{"watch", "get"}},
	}
	result := mergeTransitiveRules(input)
	if len(result) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(result))
	}
	verbSet := map[string]bool{}
	for _, v := range result[0].Verbs {
		verbSet[v] = true
	}
	for _, expected := range []string{"get", "list", "watch"} {
		if !verbSet[expected] {
			t.Errorf("expected verb %q in merged result, got %v", expected, result[0].Verbs)
		}
	}
}

func TestMergeTransitiveRules_SeparateAPIGroups(t *testing.T) {
	input := []policyRule{
		{APIGroups: []string{""}, Resources: []string{"pods"}, Verbs: []string{"get"}},
		{APIGroups: []string{"apps"}, Resources: []string{"deployments"}, Verbs: []string{"get"}},
	}
	result := mergeTransitiveRules(input)
	if len(result) != 2 {
		t.Fatalf("expected 2 rules (one per apiGroup), got %d", len(result))
	}
}

func TestMergeTransitiveRules_DeterministicOrder(t *testing.T) {
	input := []policyRule{
		{APIGroups: []string{"z.io"}, Resources: []string{"zs"}, Verbs: []string{"get"}},
		{APIGroups: []string{"a.io"}, Resources: []string{"as"}, Verbs: []string{"get"}},
		{APIGroups: []string{"m.io"}, Resources: []string{"ms"}, Verbs: []string{"get"}},
	}
	r1 := mergeTransitiveRules(input)
	r2 := mergeTransitiveRules(input)
	for i := range r1 {
		if r1[i].APIGroups[0] != r2[i].APIGroups[0] {
			t.Errorf("non-deterministic order at index %d: %q vs %q", i, r1[i].APIGroups[0], r2[i].APIGroups[0])
		}
	}
	// Also verify alphabetical ordering
	if r1[0].APIGroups[0] != "a.io" || r1[1].APIGroups[0] != "m.io" || r1[2].APIGroups[0] != "z.io" {
		t.Errorf("expected alphabetical order [a.io, m.io, z.io], got %v", []string{r1[0].APIGroups[0], r1[1].APIGroups[0], r1[2].APIGroups[0]})
	}
}

func TestMergeTransitiveRules_MultiAPIGroupInSingleRule(t *testing.T) {
	input := []policyRule{
		{APIGroups: []string{"", "apps"}, Resources: []string{"pods", "deployments"}, Verbs: []string{"get"}},
	}
	result := mergeTransitiveRules(input)
	// Each apiGroup becomes its own rule
	if len(result) != 2 {
		t.Fatalf("expected 2 rules (one per apiGroup), got %d", len(result))
	}
}

// ---- TransitiveRules ----

func makeFS(files map[string]string) fstest.MapFS {
	fsys := fstest.MapFS{}
	for path, content := range files {
		fsys[path] = &fstest.MapFile{Data: []byte(content)}
	}
	return fsys
}

func TestTransitiveRules_EmptyAssets(t *testing.T) {
	fsys := makeFS(map[string]string{
		"active/.keep": "",
	})
	rules, err := TransitiveRules(fsys)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rules) != 0 {
		t.Errorf("expected no transitive rules for empty assets, got %d", len(rules))
	}
}

func TestTransitiveRules_ExtractsFromClusterRole(t *testing.T) {
	fsys := makeFS(map[string]string{
		"active/component/role.yaml": `
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: component-role
rules:
- apiGroups: ["apps"]
  resources: ["daemonsets", "deployments"]
  verbs: ["create", "get", "list", "patch", "update", "watch"]
`,
	})
	rules, err := TransitiveRules(fsys)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("expected 1 transitive rule, got %d", len(rules))
	}
	if rules[0].APIGroups[0] != "apps" {
		t.Errorf("expected apiGroup 'apps', got %q", rules[0].APIGroups[0])
	}
}

func TestTransitiveRules_IgnoresNonRoleAssets(t *testing.T) {
	fsys := makeFS(map[string]string{
		"active/hco/golden-config.yaml": `
apiVersion: hco.kubevirt.io/v1beta1
kind: HyperConverged
metadata:
  name: kubevirt-hyperconverged
`,
	})
	rules, err := TransitiveRules(fsys)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rules) != 0 {
		t.Errorf("expected no transitive rules from non-role assets, got %d", len(rules))
	}
}

func TestTransitiveRules_MergesAcrossMultipleFiles(t *testing.T) {
	fsys := makeFS(map[string]string{
		"active/comp-a/role.yaml": `
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: comp-a
rules:
- apiGroups: [""]
  resources: ["pods"]
  verbs: ["get", "list"]
`,
		"active/comp-b/role.yaml": `
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: comp-b
rules:
- apiGroups: [""]
  resources: ["services"]
  verbs: ["get"]
- apiGroups: ["apps"]
  resources: ["deployments"]
  verbs: ["get"]
`,
	})
	rules, err := TransitiveRules(fsys)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// "" group (pods + services) and "apps" group
	if len(rules) != 2 {
		t.Fatalf("expected 2 rules, got %d", len(rules))
	}
	coreRule := rules[0] // "" sorts before "apps"
	if len(coreRule.Resources) != 2 {
		t.Errorf("expected pods and services merged into core group, got %v", coreRule.Resources)
	}
}

func TestTransitiveRules_HandlesTemplateFiles(t *testing.T) {
	fsys := makeFS(map[string]string{
		"active/component/role.yaml.tpl": `
{{- if .SomeCondition }}
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: component-role
rules:
- apiGroups: ["apps"]
  resources: ["deployments"]
  verbs: ["get", "list"]
`,
	})
	rules, err := TransitiveRules(fsys)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("expected 1 transitive rule from template file, got %d", len(rules))
	}
}

// ---- AllRules ----

func TestAllRules_ContainsStaticRules(t *testing.T) {
	fsys := makeFS(map[string]string{"active/.keep": ""})
	all, err := AllRules(fsys)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	static := StaticRules()
	if len(all) < len(static) {
		t.Errorf("AllRules returned fewer rules (%d) than StaticRules (%d)", len(all), len(static))
	}
	for i, r := range static {
		if all[i].APIGroups[0] != r.APIGroups[0] {
			t.Errorf("static rule %d mismatch: got apiGroup %q, want %q", i, all[i].APIGroups[0], r.APIGroups[0])
		}
	}
}

func TestAllRules_TransitiveRulesInsertedAfterStatic(t *testing.T) {
	fsys := makeFS(map[string]string{
		"active/comp/role.yaml": `
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: comp-role
rules:
- apiGroups: ["apps"]
  resources: ["deployments"]
  verbs: ["get"]
`,
	})
	all, err := AllRules(fsys)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	static := StaticRules()
	// The rule right after the static section should be the transitive one
	if len(all) <= len(static) {
		t.Fatalf("expected more rules than just static, got %d", len(all))
	}
	transitiveRule := all[len(static)]
	if transitiveRule.APIGroups[0] != "apps" {
		t.Errorf("expected transitive rule with apiGroup 'apps' after static rules, got %q", transitiveRule.APIGroups[0])
	}
}
