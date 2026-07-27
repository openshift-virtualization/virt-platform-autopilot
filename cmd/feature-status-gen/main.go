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

// feature-status-gen reads the features catalog from assets/active/metadata.yaml,
// derives maturity levels, and generates:
//   - docs/generated/feature-status.json  (structured data for CI)
//   - README.md section between sentinel comments (human-readable table)
//
// Run via 'make generate-feature-status'.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"sigs.k8s.io/yaml"

	"github.com/kubevirt/virt-platform-autopilot/pkg/assets"
)

const (
	metadataPath = "assets/active/metadata.yaml"
	jsonOutput   = "docs/generated/feature-status.json"
	readmePath   = "README.md"
	beginMarker  = "<!-- BEGIN FEATURE STATUS -->"
	endMarker    = "<!-- END FEATURE STATUS -->"
)

type FrameworkStatus struct {
	Maturity string  `json:"maturity"`
	OptIn    *string `json:"opt_in"`
}

type FeatureStatus struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Maturity    string   `json:"maturity"`
	Install     string   `json:"install"`
	OptIn       *string  `json:"opt_in"`
	Requires    []string `json:"requires,omitempty"`
}

type Output struct {
	Framework FrameworkStatus `json:"framework"`
	Features  []FeatureStatus `json:"features"`
}

func main() {
	dryRun := flag.Bool("dry-run", false, "Write to /tmp and diff against committed files instead of writing in place")
	flag.Parse()

	catalog, err := loadCatalog()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading metadata: %v\n", err)
		os.Exit(1)
	}

	if err := validateCoverage(catalog); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	statuses, err := deriveStatuses(catalog)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error deriving feature statuses: %v\n", err)
		os.Exit(1)
	}

	fw := deriveFramework(catalog)
	output := Output{Framework: fw, Features: statuses}

	jsonData, err := json.MarshalIndent(output, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error marshalling JSON: %v\n", err)
		os.Exit(1)
	}
	jsonData = append(jsonData, '\n')

	table := renderTable(fw, statuses)

	if *dryRun {
		failed := false

		tmpJSON := "/tmp/generated-feature-status.json"
		if err := os.WriteFile(tmpJSON, jsonData, 0644); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing %s: %v\n", tmpJSON, err)
			os.Exit(1)
		}
		defer os.Remove(tmpJSON)

		if diffErr := runDiff(jsonOutput, tmpJSON); diffErr != nil {
			failed = true
		}

		tmpReadme := "/tmp/generated-readme-features.md"
		if err := os.WriteFile(tmpReadme, []byte(table), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing %s: %v\n", tmpReadme, err)
			os.Exit(1)
		}
		defer os.Remove(tmpReadme)

		currentSection, err := extractSentinelSection(readmePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error extracting README section: %v\n", err)
			failed = true
		} else {
			tmpCurrent := "/tmp/current-readme-features.md"
			if err := os.WriteFile(tmpCurrent, []byte(currentSection), 0644); err != nil {
				fmt.Fprintf(os.Stderr, "Error writing %s: %v\n", tmpCurrent, err)
				os.Exit(1)
			}
			defer os.Remove(tmpCurrent)

			if diffErr := runDiff(tmpCurrent, tmpReadme); diffErr != nil {
				failed = true
			}
		}

		if failed {
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Feature status is out of date.")
			fmt.Fprintln(os.Stderr, "  1. Run: make generate-feature-status")
			fmt.Fprintln(os.Stderr, "  2. Commit the updated files")
			os.Exit(1)
		}

		fmt.Println("Feature status is up-to-date.")
		return
	}

	if err := os.MkdirAll(filepath.Dir(jsonOutput), 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating directory: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(jsonOutput, jsonData, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing %s: %v\n", jsonOutput, err)
		os.Exit(1)
	}
	fmt.Printf("Wrote %s\n", jsonOutput)

	if err := injectIntoReadme(readmePath, table); err != nil {
		fmt.Fprintf(os.Stderr, "Error updating %s: %v\n", readmePath, err)
		os.Exit(1)
	}
	fmt.Printf("Updated %s\n", readmePath)
}

func loadCatalog() (*assets.AssetCatalog, error) {
	data, err := os.ReadFile(metadataPath)
	if err != nil {
		return nil, err
	}
	catalog := &assets.AssetCatalog{}
	if err := yaml.Unmarshal(data, catalog); err != nil {
		return nil, err
	}
	return catalog, nil
}

func validateCoverage(catalog *assets.AssetCatalog) error {
	covered := make(map[string]bool)

	for _, excl := range catalog.ExcludedAssets {
		covered[excl] = true
	}

	for _, feature := range catalog.Features {
		for _, name := range feature.Assets {
			covered[name] = true
		}
		for _, group := range feature.Groups {
			for _, a := range catalog.Assets {
				if a.Group == group {
					covered[a.Name] = true
				}
			}
		}
	}

	var uncovered []string
	for _, a := range catalog.Assets {
		if !covered[a.Name] {
			uncovered = append(uncovered, a.Name)
		}
	}

	if len(uncovered) > 0 {
		return fmt.Errorf("assets not covered by any feature or excluded_assets: %s\n"+
			"  Add them to a features entry or to excluded_assets in metadata.yaml",
			strings.Join(uncovered, ", "))
	}
	return nil
}

func deriveStatuses(catalog *assets.AssetCatalog) ([]FeatureStatus, error) {
	assetByName := make(map[string]*assets.AssetMetadata, len(catalog.Assets))
	for i := range catalog.Assets {
		assetByName[catalog.Assets[i].Name] = &catalog.Assets[i]
	}

	var statuses []FeatureStatus
	for _, feature := range catalog.Features {
		resolved := resolveAssets(feature, catalog.Assets, assetByName)
		if len(resolved) == 0 {
			return nil, fmt.Errorf("feature %q resolves to zero assets", feature.Name)
		}

		maturity := deriveMaturity(feature, resolved)
		install := deriveInstall(resolved)
		optIn := deriveOptIn(resolved)

		statuses = append(statuses, FeatureStatus{
			Name:        feature.Name,
			Description: feature.Description,
			Maturity:    maturity,
			Install:     install,
			OptIn:       optIn,
			Requires:    feature.Requires,
		})
	}
	return statuses, nil
}

func resolveAssets(feature assets.FeatureMetadata, allAssets []assets.AssetMetadata, byName map[string]*assets.AssetMetadata) []*assets.AssetMetadata {
	var resolved []*assets.AssetMetadata
	seen := make(map[string]bool)

	for _, name := range feature.Assets {
		if a, ok := byName[name]; ok && !seen[name] {
			resolved = append(resolved, a)
			seen[name] = true
		}
	}

	for _, group := range feature.Groups {
		for i := range allAssets {
			if allAssets[i].Group == group && !seen[allAssets[i].Name] {
				resolved = append(resolved, &allAssets[i])
				seen[allAssets[i].Name] = true
			}
		}
	}

	return resolved
}

func deriveMaturity(feature assets.FeatureMetadata, resolved []*assets.AssetMetadata) string {
	if feature.Maturity != "" {
		return strings.ToUpper(feature.Maturity)
	}
	for _, a := range resolved {
		if a.Install == assets.InstallModeOptIn {
			return "DP"
		}
	}
	return "GA"
}

func deriveInstall(resolved []*assets.AssetMetadata) string {
	for _, a := range resolved {
		if a.Install == assets.InstallModeOptIn {
			return "opt-in"
		}
	}
	return "always"
}

func deriveOptIn(resolved []*assets.AssetMetadata) *string {
	var conditions []string
	seen := make(map[string]bool)

	for _, a := range resolved {
		for _, c := range a.Conditions {
			if c.Type == assets.ConditionTypeAnnotation && c.Key != "" {
				entry := c.Key + "=" + c.Value
				if !seen[entry] {
					conditions = append(conditions, entry)
					seen[entry] = true
				}
			} else if c.Type == assets.ConditionTypeFeatureGate && c.Value != "" {
				entry := "featureGate:" + c.Value
				if !seen[entry] {
					conditions = append(conditions, entry)
					seen[entry] = true
				}
			}
		}
	}

	if len(conditions) == 0 {
		return nil
	}
	result := strings.Join(conditions, ", ")
	return &result
}

func deriveFramework(catalog *assets.AssetCatalog) FrameworkStatus {
	maturity := "GA"
	if catalog.Framework.Maturity != "" {
		maturity = strings.ToUpper(catalog.Framework.Maturity)
	}
	var optIn *string
	if catalog.Framework.OptIn != "" {
		s := catalog.Framework.OptIn
		optIn = &s
	}
	return FrameworkStatus{Maturity: maturity, OptIn: optIn}
}

func renderTable(fw FrameworkStatus, statuses []FeatureStatus) string {
	var b strings.Builder

	if fw.Maturity != "GA" && fw.OptIn != nil {
		fmt.Fprintf(&b, "> **Note:** The autopilot framework is currently **%s** and requires `%s` on the HCO CR. Feature maturity levels below are relative to an enabled autopilot.\n\n", fw.Maturity, *fw.OptIn)
	}

	b.WriteString("| Feature | Maturity | Install | Opt-in | Requires |\n")
	b.WriteString("|---------|----------|---------|--------|----------|\n")

	for _, s := range statuses {
		optIn := "-"
		if s.OptIn != nil {
			optIn = "`" + *s.OptIn + "`"
		}
		requires := "-"
		if len(s.Requires) > 0 {
			requires = strings.Join(s.Requires, ", ")
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s |\n", s.Name, s.Maturity, s.Install, optIn, requires)
	}

	return b.String()
}

func extractSentinelSection(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	content := string(data)

	beginIdx := strings.Index(content, beginMarker)
	endIdx := strings.Index(content, endMarker)
	if beginIdx < 0 || endIdx < 0 || endIdx <= beginIdx {
		return "", fmt.Errorf("sentinel markers not found in %s", path)
	}

	section := content[beginIdx+len(beginMarker) : endIdx]
	section = strings.TrimPrefix(section, "\n")
	section = strings.TrimSuffix(section, "\n")
	return section + "\n", nil
}

func injectIntoReadme(path, table string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	content := string(data)

	beginIdx := strings.Index(content, beginMarker)
	endIdx := strings.Index(content, endMarker)
	if beginIdx < 0 || endIdx < 0 || endIdx <= beginIdx {
		return fmt.Errorf("sentinel markers not found in %s", path)
	}

	var b strings.Builder
	b.WriteString(content[:beginIdx+len(beginMarker)])
	b.WriteString("\n")
	b.WriteString(table)
	b.WriteString(content[endIdx:])

	return os.WriteFile(path, []byte(b.String()), 0644)
}

func runDiff(fileA, fileB string) error {
	cmd := exec.Command("diff", "-u", fileA, fileB)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
