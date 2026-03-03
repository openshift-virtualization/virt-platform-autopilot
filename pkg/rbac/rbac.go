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
// The order is stable and must not be changed without updating callers that rely on
// index-based access (e.g. the comment formatter in cmd/rbac-gen).
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
	}
}

// AllRules returns the complete set of RBAC rules (static infrastructure rules plus
// dynamic rules derived from assets) for the given asset FS.
//
// fsys must be rooted at the assets directory: it should contain active/ and tombstones/
// as immediate subdirectories. Both os.DirFS("assets") and the embedded assets.EmbeddedFS
// satisfy this contract.
func AllRules(fsys fs.FS) ([]Rule, error) {
	resources, err := extractResources(fsys)
	if err != nil {
		return nil, err
	}
	dynamic := generateDynamicRules(resources)
	return append(StaticRules(), dynamic...), nil
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
	case "selfnoderemediation":
		return "selfnoderemediations"
	case "fenceagentsremediation":
		return "fenceagentsremediations"
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

		var doc map[string]interface{}
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
			verbs = append([]string{"delete"}, verbs...)
		}

		rules = append(rules, Rule{
			APIGroups: []string{group},
			Resources: unique,
			Verbs:     verbs,
		})
	}

	return rules
}
