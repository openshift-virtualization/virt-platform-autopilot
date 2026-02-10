package main

import (
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"sigs.k8s.io/yaml"
)

const (
	assetsDir  = "assets"
	outputFile = "config/rbac/role.yaml"
)

// Resource represents a Kubernetes resource GVK
type Resource struct {
	APIVersion string
	Kind       string
}

// RBACRule represents a ClusterRole rule
type RBACRule struct {
	APIGroups []string `yaml:"apiGroups"`
	Resources []string `yaml:"resources"`
	Verbs     []string `yaml:"verbs"`
}

// ClusterRole represents the RBAC ClusterRole structure
type ClusterRole struct {
	APIVersion string     `yaml:"apiVersion"`
	Kind       string     `yaml:"kind"`
	Metadata   Metadata   `yaml:"metadata"`
	Rules      []RBACRule `yaml:"rules"`
}

type Metadata struct {
	Name string `yaml:"name"`
}

// staticRules returns the operator infrastructure RBAC rules
// IMPORTANT: Order must be stable for deterministic output
func staticRules() []RBACRule {
	return []RBACRule{
		// Rule 1: Nodes (for hardware detection)
		{
			APIGroups: []string{""},
			Resources: []string{"nodes"},
			Verbs:     []string{"get", "list", "watch"},
		},
		// Rule 2: Events (for observability - legacy core/v1 API)
		{
			APIGroups: []string{""},
			Resources: []string{"events"},
			Verbs:     []string{"create", "patch"},
		},
		// Rule 3: Events (for observability - modern events.k8s.io/v1 API)
		{
			APIGroups: []string{"events.k8s.io"},
			Resources: []string{"events"},
			Verbs:     []string{"create", "patch"},
		},
		// Rule 4: Leader Election
		{
			APIGroups: []string{"coordination.k8s.io"},
			Resources: []string{"leases"},
			Verbs:     []string{"create", "delete", "get", "list", "patch", "update", "watch"},
		},
		// Rule 5: CRD Discovery (for soft dependency detection)
		{
			APIGroups: []string{"apiextensions.k8s.io"},
			Resources: []string{"customresourcedefinitions"},
			Verbs:     []string{"get", "list", "watch"},
		},
	}
}

// parseGVK extracts Group, Version, Kind from apiVersion and kind fields
func parseGVK(apiVersion, kind string) (group, version, resource string) {
	// apiVersion can be "v1" (core) or "group/version"
	parts := strings.SplitN(apiVersion, "/", 2)
	if len(parts) == 1 {
		// Core API (e.g., "v1")
		return "", parts[0], pluralize(kind)
	}
	// Group API (e.g., "hco.kubevirt.io/v1beta1")
	return parts[0], parts[1], pluralize(kind)
}

// pluralize converts Kind to resource name (simple heuristic)
func pluralize(kind string) string {
	kind = strings.ToLower(kind)

	// Handle common special cases
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
		// Simple pluralization: add 's'
		if strings.HasSuffix(kind, "s") || strings.HasSuffix(kind, "x") || strings.HasSuffix(kind, "ch") {
			return kind + "es"
		}
		return kind + "s"
	}
}

// preprocessTemplate replaces template variables with dummy values for parsing
func preprocessTemplate(content []byte) []byte {
	// Replace {{ .* }} with "dummy-value" for parsing
	re := regexp.MustCompile(`\{\{[^}]+\}\}`)
	return re.ReplaceAll(content, []byte(`"dummy-value"`))
}

// processAssetFile extracts resources from a single asset file
func processAssetFile(content []byte, seen map[string]bool, resources *[]Resource) {
	// Parse YAML - sigs.k8s.io/yaml doesn't support streaming, so split on ---
	docs := strings.Split(string(content), "\n---\n")
	for _, docStr := range docs {
		docStr = strings.TrimSpace(docStr)
		if docStr == "" {
			continue
		}

		var doc map[string]interface{}
		err := yaml.Unmarshal([]byte(docStr), &doc)
		if err != nil {
			continue // Parse error (template remnants), skip
		}

		// Extract apiVersion and kind
		apiVersion, ok1 := doc["apiVersion"].(string)
		kind, ok2 := doc["kind"].(string)

		if ok1 && ok2 && apiVersion != "" && kind != "" {
			// Skip non-resource types
			if kind == "List" || kind == "CustomResourceDefinition" {
				continue
			}

			key := apiVersion + "/" + kind
			if !seen[key] {
				seen[key] = true
				*resources = append(*resources, Resource{
					APIVersion: apiVersion,
					Kind:       kind,
				})
			}
		}
	}
}

// extractResources scans asset files and extracts GVKs
func extractResources(assetsPath string) ([]Resource, error) {
	var resources []Resource
	seen := make(map[string]bool)

	err := filepath.WalkDir(assetsPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Skip directories and non-YAML files
		if d.IsDir() {
			// Skip assets/crds directory entirely
			if d.Name() == "crds" && filepath.Dir(path) == assetsPath {
				return filepath.SkipDir
			}
			return nil
		}

		// Only process .yaml and .yaml.tpl files
		if !strings.HasSuffix(path, ".yaml") && !strings.HasSuffix(path, ".yaml.tpl") {
			return nil
		}

		// Read file
		content, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("failed to read %s: %w", path, err)
		}

		// Preprocess templates
		if strings.HasSuffix(path, ".tpl") {
			content = preprocessTemplate(content)
		}

		// Process file content
		processAssetFile(content, seen, &resources)

		return nil
	})

	return resources, err
}

// generateDynamicRules creates RBAC rules from discovered resources
// IMPORTANT: Output must be deterministic for CI verification
func generateDynamicRules(resources []Resource) []RBACRule {
	// Group resources by API group
	groupedResources := make(map[string][]string)

	for _, res := range resources {
		group, _, resource := parseGVK(res.APIVersion, res.Kind)
		groupedResources[group] = append(groupedResources[group], resource)
	}

	// Sort groups alphabetically for deterministic output
	var groups []string
	for group := range groupedResources {
		groups = append(groups, group)
	}
	sort.Strings(groups)

	// Generate rules with deterministic ordering
	var rules []RBACRule
	// Verbs in alphabetical order for consistency
	standardVerbs := []string{"create", "get", "list", "patch", "update", "watch"}

	for _, group := range groups {
		resourceList := groupedResources[group]

		// Deduplicate and sort resources alphabetically for deterministic output
		resourceMap := make(map[string]bool)
		for _, r := range resourceList {
			resourceMap[r] = true
		}

		var uniqueResources []string
		for r := range resourceMap {
			uniqueResources = append(uniqueResources, r)
		}
		sort.Strings(uniqueResources) // Critical for deterministic output

		// Create rule
		apiGroup := group
		if group == "" {
			apiGroup = "" // Core API uses empty string
		}

		rules = append(rules, RBACRule{
			APIGroups: []string{apiGroup},
			Resources: uniqueResources,
			Verbs:     standardVerbs,
		})
	}

	return rules
}

// addComments adds comments to rules for readability
func formatRulesWithComments(rules []RBACRule) string {
	var builder strings.Builder

	// Static infrastructure rules
	builder.WriteString("  # ========================================\n")
	builder.WriteString("  # Operator Infrastructure (Static)\n")
	builder.WriteString("  # ========================================\n")
	builder.WriteString("  # Nodes (for hardware detection)\n")
	writeRule(&builder, &rules[0])
	builder.WriteString("  # Events (for observability - legacy core/v1 API)\n")
	writeRule(&builder, &rules[1])
	builder.WriteString("  # Events (for observability - modern events.k8s.io/v1 API)\n")
	writeRule(&builder, &rules[2])
	builder.WriteString("  # Leader Election\n")
	writeRule(&builder, &rules[3])
	builder.WriteString("  # CRD Discovery (for soft dependency detection)\n")
	writeRule(&builder, &rules[4])

	// Dynamic rules from assets
	builder.WriteString("  # ========================================\n")
	builder.WriteString("  # Managed Resources (Dynamic - from assets/)\n")
	builder.WriteString("  # ========================================\n")

	for i := 5; i < len(rules); i++ {
		// Add comment based on API group
		rule := &rules[i]
		comment := getCommentForAPIGroup(rule.APIGroups[0])
		if comment != "" {
			builder.WriteString(fmt.Sprintf("  # %s\n", comment))
		}
		writeRule(&builder, rule)
	}

	return builder.String()
}

func writeRule(builder *strings.Builder, rule *RBACRule) {
	builder.WriteString("  - apiGroups:\n")
	for _, group := range rule.APIGroups {
		if group == "" {
			builder.WriteString("      - \"\"\n")
		} else {
			fmt.Fprintf(builder, "      - %s\n", group)
		}
	}
	builder.WriteString("    resources:\n")
	for _, resource := range rule.Resources {
		fmt.Fprintf(builder, "      - %s\n", resource)
	}
	builder.WriteString("    verbs:\n")
	for _, verb := range rule.Verbs {
		fmt.Fprintf(builder, "      - %s\n", verb)
	}
}

func getCommentForAPIGroup(group string) string {
	switch group {
	case "hco.kubevirt.io":
		return "HyperConverged"
	case "machineconfiguration.openshift.io":
		return "MachineConfig & KubeletConfig"
	case "operator.openshift.io":
		return "KubeDescheduler"
	case "remediation.medik8s.io":
		return "NodeHealthCheck"
	case "self-node-remediation.medik8s.io":
		return "Self Node Remediation"
	case "fence-agents-remediation.medik8s.io":
		return "Fence Agents Remediation"
	case "forklift.konveyor.io":
		return "Migration Toolkit for Virtualization (MTV)"
	case "metallb.io":
		return "MetalLB"
	case "observability.openshift.io":
		return "Cluster Observability"
	default:
		return ""
	}
}

func main() {
	dryRun := flag.Bool("dry-run", false, "Print generated RBAC to stdout instead of writing to file")
	flag.Parse()

	// Extract resources from assets
	if !*dryRun {
		fmt.Println("Scanning assets for resources...")
	}
	resources, err := extractResources(assetsDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error scanning assets: %v\n", err)
		os.Exit(1)
	}

	if !*dryRun {
		fmt.Printf("Found %d unique resource types\n", len(resources))
	}

	// Generate dynamic rules
	dynamicRules := generateDynamicRules(resources)
	if !*dryRun {
		fmt.Printf("Generated %d dynamic RBAC rules\n", len(dynamicRules))
	}

	// Combine static + dynamic rules
	allRules := append(staticRules(), dynamicRules...)

	// Generate YAML header
	header := `# AUTO-GENERATED by 'make generate-rbac'
# DO NOT EDIT MANUALLY - your changes will be overwritten
# To modify RBAC:
#   1. Add/remove assets in assets/ directory
#   2. Run 'make generate-rbac'
#   3. Commit the updated config/rbac/role.yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: virt-platform-operator-role
rules:
`

	// Format rules with comments
	rulesYAML := formatRulesWithComments(allRules)
	output := header + rulesYAML

	if *dryRun {
		fmt.Print(output)
		return
	}

	// Write to file
	if err := os.WriteFile(outputFile, []byte(output), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing to %s: %v\n", outputFile, err)
		os.Exit(1)
	}

	fmt.Printf("✓ RBAC ClusterRole written to %s\n", outputFile)
	fmt.Printf("  Total rules: %d (5 static + %d dynamic)\n", len(allRules), len(dynamicRules))
}
