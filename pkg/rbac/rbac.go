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

// Package rbac provides shared RBAC rule generation logic for virt-platform-autopilot.
// It derives ClusterRole rules by scanning asset files (via fs.FS) to discover the
// Kubernetes GVKs the operator manages, then combining them with static infrastructure rules.
//
// Both the rbac-gen tool (reads from the real filesystem) and the csv-generator
// (reads from the embedded asset FS) use this package to ensure generated RBAC is
// always consistent with the actual set of managed resources.
package rbac

import (
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strings"

	"sigs.k8s.io/yaml"
)

// Resource represents a Kubernetes resource GVK discovered from an asset file.
type Resource struct {
	APIVersion  string
	Kind        string
	NeedsDelete bool // true if found in tombstones (requires delete verb)
}

// Rule represents a single ClusterRole policy rule.
type Rule struct {
	APIGroups []string
	Resources []string
	Verbs     []string
}

// StaticRules returns the fixed infrastructure RBAC rules that every release of
// virt-platform-autopilot requires, regardless of which asset templates are active.
// The order is stable; the comment formatter in cmd/rbac-gen writes each rule by index.
func StaticRules() []Rule {
	return []Rule{
		// Rule 0: Nodes (for hardware detection)
		{
			APIGroups: []string{""},
			Resources: []string{"nodes"},
			Verbs:     []string{"get", "list", "watch"},
		},
		// Rule 1: Events (for observability - legacy core/v1 API)
		{
			APIGroups: []string{""},
			Resources: []string{"events"},
			Verbs:     []string{"create", "patch"},
		},
		// Rule 2: Events (for observability - modern events.k8s.io/v1 API)
		{
			APIGroups: []string{"events.k8s.io"},
			Resources: []string{"events"},
			Verbs:     []string{"create", "patch"},
		},
		// Rule 3: Leader Election
		{
			APIGroups: []string{"coordination.k8s.io"},
			Resources: []string{"leases"},
			Verbs:     []string{"create", "delete", "get", "list", "patch", "update", "watch"},
		},
		// Rule 4: CRD Discovery (for soft dependency detection and template introspection)
		{
			APIGroups: []string{"apiextensions.k8s.io"},
			Resources: []string{"customresourcedefinitions"},
			Verbs:     []string{"get", "list", "watch"},
		},
		// Rule 5: OpenShift Infrastructure CR (for cluster topology detection: HCP, compact)
		// The Infrastructure CR is a singleton (name="cluster") and is non-sensitive read-only.
		// Gracefully absent on non-OpenShift clusters — the operator handles NotFound.
		{
			APIGroups: []string{"config.openshift.io"},
			Resources: []string{"infrastructures"},
			Verbs:     []string{"get", "list", "watch"},
		},
		// Rule 6: Namespaces (for pre-apply guard: verify the target namespace exists before
		// consuming a rate-limit token; avoids spurious throttling when an operator component
		// is not yet installed and its namespace is absent).
		{
			APIGroups: []string{""},
			Resources: []string{"namespaces"},
			Verbs:     []string{"get"},
		},
	}
}

// policyRule mirrors the Kubernetes PolicyRule structure for YAML unmarshalling.
type policyRule struct {
	APIGroups []string `yaml:"apiGroups"`
	Resources []string `yaml:"resources"`
	Verbs     []string `yaml:"verbs"`
}

// TransitiveRules scans ClusterRole and Role objects in the active/ subtree of fsys
// and returns the union of their policy rules. This satisfies Kubernetes privilege
// escalation prevention: the operator must hold every permission it grants.
//
// Rules are merged per API group (resources and verbs are unioned within each group)
// and returned in deterministic order.
func TransitiveRules(fsys fs.FS) ([]Rule, error) {
	var collected []policyRule
	err := fs.WalkDir(fsys, "active", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if !strings.HasSuffix(path, ".yaml") && !strings.HasSuffix(path, ".yaml.tpl") {
			return nil
		}
		content, err := fs.ReadFile(fsys, path)
		if err != nil {
			return fmt.Errorf("failed to read %s: %w", path, err)
		}
		if strings.HasSuffix(path, ".tpl") {
			content = preprocessTemplate(content)
		}
		collectRoleRules(content, &collected)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to scan active directory for roles: %w", err)
	}
	return mergeTransitiveRules(collected), nil
}

// collectRoleRules appends policy rules from any ClusterRole or Role documents
// found in the given YAML content (supports multi-doc files).
func collectRoleRules(content []byte, rules *[]policyRule) {
	for _, docStr := range strings.Split(string(content), "\n---\n") {
		docStr = strings.TrimSpace(docStr)
		if docStr == "" {
			continue
		}
		var doc struct {
			Kind  string       `yaml:"kind"`
			Rules []policyRule `yaml:"rules"`
		}
		if err := yaml.Unmarshal([]byte(docStr), &doc); err != nil {
			continue
		}
		if doc.Kind != "ClusterRole" && doc.Kind != "Role" {
			continue
		}
		*rules = append(*rules, doc.Rules...)
	}
}

// mergeTransitiveRules groups policy rules by API group, unions resources and verbs
// within each group, and returns a deterministically sorted slice of Rules.
func mergeTransitiveRules(collected []policyRule) []Rule {
	type groupInfo struct {
		resources map[string]bool
		verbs     map[string]bool
	}
	groups := make(map[string]*groupInfo)

	for _, rule := range collected {
		for _, apiGroup := range rule.APIGroups {
			if groups[apiGroup] == nil {
				groups[apiGroup] = &groupInfo{
					resources: make(map[string]bool),
					verbs:     make(map[string]bool),
				}
			}
			for _, r := range rule.Resources {
				groups[apiGroup].resources[r] = true
			}
			for _, v := range rule.Verbs {
				groups[apiGroup].verbs[v] = true
			}
		}
	}

	apiGroupKeys := make([]string, 0, len(groups))
	for g := range groups {
		apiGroupKeys = append(apiGroupKeys, g)
	}
	sort.Strings(apiGroupKeys)

	var result []Rule
	for _, group := range apiGroupKeys {
		info := groups[group]

		resources := make([]string, 0, len(info.resources))
		for r := range info.resources {
			resources = append(resources, r)
		}
		sort.Strings(resources)

		verbs := make([]string, 0, len(info.verbs))
		for v := range info.verbs {
			verbs = append(verbs, v)
		}
		sort.Strings(verbs)

		result = append(result, Rule{
			APIGroups: []string{group},
			Resources: resources,
			Verbs:     verbs,
		})
	}
	return result
}

// DynamicRules returns only the rules derived from managed resource GVKs in the assets.
func DynamicRules(fsys fs.FS) ([]Rule, error) {
	resources, err := extractResources(fsys)
	if err != nil {
		return nil, err
	}
	return generateDynamicRules(resources), nil
}

// AllRules returns the complete set of RBAC rules for the given asset FS:
// static infrastructure rules, transitive rules from managed ClusterRole/Role assets,
// and dynamic rules derived from managed resource GVKs.
//
// fsys must be rooted at the assets directory: it should contain active/ and tombstones/
// as immediate subdirectories. Both os.DirFS("assets") and the embedded assets.EmbeddedFS
// satisfy this contract.
func AllRules(fsys fs.FS) ([]Rule, error) {
	transitive, err := TransitiveRules(fsys)
	if err != nil {
		return nil, err
	}
	dynamic, err := DynamicRules(fsys)
	if err != nil {
		return nil, err
	}
	rules := append(StaticRules(), transitive...)
	return append(rules, dynamic...), nil
}

// parseGVK extracts the API group, version, and plural resource name from an apiVersion
// string and a Kind string.
func parseGVK(apiVersion, kind string) (group, version, resource string) {
	parts := strings.SplitN(apiVersion, "/", 2)
	if len(parts) == 1 {
		// Core API group (e.g. "v1")
		return "", parts[0], pluralize(kind)
	}
	// Named API group (e.g. "hco.kubevirt.io/v1beta1")
	return parts[0], parts[1], pluralize(kind)
}

// pluralize converts a Kind to its lowercase plural resource name.
func pluralize(kind string) string {
	kind = strings.ToLower(kind)
	switch kind {
	case "nodehealthcheck":
		return "nodehealthchecks"
	case "kubeletconfig":
		return "kubeletconfigs"
	case "machineconfig":
		return "machineconfigs"
	case "kubedescheduler":
		return "kubedeschedulers"
	default:
		if strings.HasSuffix(kind, "s") || strings.HasSuffix(kind, "x") || strings.HasSuffix(kind, "ch") {
			return kind + "es"
		}
		return kind + "s"
	}
}

// preprocessTemplate strips Go template directives and replaces template expressions
// with dummy values so the resulting bytes can be parsed as valid YAML.
func preprocessTemplate(content []byte) []byte {
	// Remove lines that are purely template control-flow directives (e.g. {{- if ... }})
	lines := strings.Split(string(content), "\n")
	var filtered []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "{{") && strings.HasSuffix(trimmed, "}}") {
			continue
		}
		filtered = append(filtered, line)
	}
	content = []byte(strings.Join(filtered, "\n"))

	// Replace backtick raw strings (Prometheus template variables embedded in strings)
	backtickRe := regexp.MustCompile("\\{\\{`[^`]*`\\}\\}")
	content = backtickRe.ReplaceAll(content, []byte(`dummy-value`))

	// Replace remaining template expressions with a dummy YAML-safe value
	exprRe := regexp.MustCompile(`\{\{[^}]+\}\}`)
	return exprRe.ReplaceAll(content, []byte(`"dummy-value"`))
}

// processAssetFile extracts Kubernetes GVKs from YAML content (supports multi-doc files).
func processAssetFile(content []byte, seen map[string]bool, resources *[]Resource, needsDelete bool) {
	docs := strings.Split(string(content), "\n---\n")
	for _, docStr := range docs {
		docStr = strings.TrimSpace(docStr)
		if docStr == "" {
			continue
		}

		var doc map[string]any
		if err := yaml.Unmarshal([]byte(docStr), &doc); err != nil {
			continue // template remnants or invalid YAML — skip
		}

		apiVersion, ok1 := doc["apiVersion"].(string)
		kind, ok2 := doc["kind"].(string)
		if !ok1 || !ok2 || apiVersion == "" || kind == "" {
			continue
		}

		// Skip meta-types that don't require RBAC permissions
		if kind == "List" || kind == "CustomResourceDefinition" {
			continue
		}

		key := apiVersion + "/" + kind
		if !seen[key] {
			seen[key] = true
			*resources = append(*resources, Resource{
				APIVersion:  apiVersion,
				Kind:        kind,
				NeedsDelete: needsDelete,
			})
		} else if needsDelete {
			// Upgrade an already-seen resource to require delete permissions
			for i := range *resources {
				if (*resources)[i].APIVersion == apiVersion && (*resources)[i].Kind == kind {
					(*resources)[i].NeedsDelete = true
					break
				}
			}
		}
	}
}

// scanDirectory walks dir inside fsys and calls processAssetFile for every .yaml / .yaml.tpl.
func scanDirectory(fsys fs.FS, dir string, seen map[string]bool, resources *[]Resource, needsDelete bool) error {
	return fs.WalkDir(fsys, dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".yaml") && !strings.HasSuffix(path, ".yaml.tpl") {
			return nil
		}

		content, err := fs.ReadFile(fsys, path)
		if err != nil {
			return fmt.Errorf("failed to read %s: %w", path, err)
		}

		if strings.HasSuffix(path, ".tpl") {
			content = preprocessTemplate(content)
		}

		processAssetFile(content, seen, resources, needsDelete)
		return nil
	})
}

// extractResources scans active/ and tombstones/ inside fsys and returns all discovered GVKs.
func extractResources(fsys fs.FS) ([]Resource, error) {
	var resources []Resource
	seen := make(map[string]bool)

	if err := scanDirectory(fsys, "active", seen, &resources, false); err != nil {
		return nil, fmt.Errorf("failed to scan active directory: %w", err)
	}

	if err := scanDirectory(fsys, "tombstones", seen, &resources, true); err != nil {
		// Tolerate a missing tombstones directory (it may be empty or absent)
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("failed to scan tombstones directory: %w", err)
		}
	}

	return resources, nil
}

// generateDynamicRules groups discovered resources by API group and produces
// deterministically ordered RBAC rules.
func generateDynamicRules(resources []Resource) []Rule {
	type groupInfo struct {
		resources   []string
		needsDelete bool
	}
	grouped := make(map[string]*groupInfo)

	for _, res := range resources {
		group, _, resource := parseGVK(res.APIVersion, res.Kind)
		if grouped[group] == nil {
			grouped[group] = &groupInfo{}
		}
		grouped[group].resources = append(grouped[group].resources, resource)
		if res.NeedsDelete {
			grouped[group].needsDelete = true
		}
	}

	// Sort groups alphabetically for deterministic output
	var groups []string
	for g := range grouped {
		groups = append(groups, g)
	}
	sort.Strings(groups)

	var rules []Rule
	for _, group := range groups {
		info := grouped[group]

		// Deduplicate and sort resources
		resourceSet := make(map[string]bool)
		for _, r := range info.resources {
			resourceSet[r] = true
		}
		var unique []string
		for r := range resourceSet {
			unique = append(unique, r)
		}
		sort.Strings(unique)

		verbs := []string{"create", "get", "list", "patch", "update", "watch"}
		if info.needsDelete {
			verbs = append(verbs, "delete")
			sort.Strings(verbs)
		}

		rules = append(rules, Rule{
			APIGroups: []string{group},
			Resources: unique,
			Verbs:     verbs,
		})
	}

	return rules
}
