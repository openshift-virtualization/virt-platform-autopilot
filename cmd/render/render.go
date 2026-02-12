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

package render

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	"github.com/kubevirt/virt-platform-autopilot/pkg/assets"
	pkgcontext "github.com/kubevirt/virt-platform-autopilot/pkg/context"
	"github.com/kubevirt/virt-platform-autopilot/pkg/engine"
)

var (
	kubeconfig   string
	hcoFile      string
	assetFilter  string
	showExcluded bool
	outputFormat string
)

// NewRenderCommand creates the render subcommand
func NewRenderCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "render",
		Short: "Render assets without applying them to the cluster",
		Long: `Render all platform assets based on HyperConverged configuration.

This command is useful for:
- Debugging template rendering
- Validating asset configurations
- Generating manifests for GitOps
- Testing changes without cluster deployment
- CI/CD integration

Examples:
  # Render all assets using HCO from cluster
  virt-platform-autopilot render --kubeconfig=/path/to/kubeconfig

  # Render specific asset
  virt-platform-autopilot render --asset=swap-enable --kubeconfig=/path/to/kubeconfig

  # Offline mode: provide HCO as input
  virt-platform-autopilot render --hco-file=hco.yaml

  # Show excluded assets with reasons
  virt-platform-autopilot render --show-excluded --hco-file=hco.yaml

  # JSON output
  virt-platform-autopilot render --output=json --hco-file=hco.yaml
`,
		RunE: runRender,
	}

	cmd.Flags().StringVar(&kubeconfig, "kubeconfig", "", "Path to kubeconfig file (for cluster mode)")
	cmd.Flags().StringVar(&hcoFile, "hco-file", "", "Path to HyperConverged YAML file (for offline mode)")
	cmd.Flags().StringVar(&assetFilter, "asset", "", "Render only this specific asset")
	cmd.Flags().BoolVar(&showExcluded, "show-excluded", false, "Include excluded/filtered assets in output")
	cmd.Flags().StringVar(&outputFormat, "output", "yaml", "Output format: yaml, json, or status")

	return cmd
}

// runRender executes the render command
//
//nolint:gocognit // This function handles all rendering logic which is inherently complex
func runRender(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	// Validate flags
	if kubeconfig == "" && hcoFile == "" {
		return fmt.Errorf("either --kubeconfig or --hco-file must be specified")
	}

	if kubeconfig != "" && hcoFile != "" {
		return fmt.Errorf("--kubeconfig and --hco-file are mutually exclusive")
	}

	// Load assets
	loader := assets.NewLoader()
	registry, err := assets.NewRegistry(loader)
	if err != nil {
		return fmt.Errorf("failed to load asset registry: %w", err)
	}

	renderer := engine.NewRenderer(loader)

	// Get HCO
	var hco *unstructured.Unstructured
	if hcoFile != "" {
		hco, err = loadHCOFromFile(hcoFile)
		if err != nil {
			return fmt.Errorf("failed to load HCO from file: %w", err)
		}
	} else {
		hco, err = loadHCOFromCluster(ctx, kubeconfig)
		if err != nil {
			return fmt.Errorf("failed to load HCO from cluster: %w", err)
		}
	}

	// Build render context
	renderCtx := pkgcontext.NewRenderContext(hco)

	// Get assets to render
	var assetsToRender []assets.AssetMetadata
	if assetFilter != "" {
		asset, err := registry.GetAsset(assetFilter)
		if err != nil {
			return fmt.Errorf("asset not found: %w", err)
		}
		assetsToRender = []assets.AssetMetadata{*asset}
	} else {
		assetsToRender = registry.ListAssetsByReconcileOrder()
	}

	// Render assets
	outputs := []RenderOutput{}
	for _, assetMeta := range assetsToRender {
		output := RenderOutput{
			Asset:      assetMeta.Name,
			Path:       assetMeta.Path,
			Component:  assetMeta.Component,
			Conditions: assetMeta.Conditions,
		}

		// Check conditions
		if !checkConditions(&assetMeta, renderCtx) {
			output.Status = "EXCLUDED"
			output.Reason = "Conditions not met"
			if showExcluded {
				outputs = append(outputs, output)
			}
			continue
		}

		// Render asset
		rendered, err := renderer.RenderAsset(&assetMeta, renderCtx)
		if err != nil {
			output.Status = "ERROR"
			output.Reason = err.Error()
			outputs = append(outputs, output)
			continue
		}

		if rendered == nil {
			output.Status = "EXCLUDED"
			output.Reason = "Conditional template rendered empty"
			if showExcluded {
				outputs = append(outputs, output)
			}
			continue
		}

		// Check root exclusion
		disabledAnnotation := renderCtx.HCO.GetAnnotations()[engine.DisabledResourcesAnnotation]
		if disabledAnnotation != "" {
			disabledMap := engine.ParseDisabledResources(disabledAnnotation)
			if engine.IsResourceExcluded(rendered.GetKind(), rendered.GetName(), disabledMap) {
				output.Status = "FILTERED"
				output.Reason = "Root exclusion (disabled-resources annotation)"
				if showExcluded {
					outputs = append(outputs, output)
				}
				continue
			}
		}

		output.Status = "INCLUDED"
		output.Object = rendered
		outputs = append(outputs, output)
	}

	// Write output
	return writeOutput(outputs, outputFormat)
}

// RenderOutput represents the output for a rendered asset
type RenderOutput struct {
	Asset      string                     `json:"asset" yaml:"asset"`
	Path       string                     `json:"path" yaml:"path"`
	Component  string                     `json:"component" yaml:"component"`
	Status     string                     `json:"status" yaml:"status"`
	Reason     string                     `json:"reason,omitempty" yaml:"reason,omitempty"`
	Conditions []assets.AssetCondition    `json:"conditions,omitempty" yaml:"conditions,omitempty"`
	Object     *unstructured.Unstructured `json:"object,omitempty" yaml:"object,omitempty"`
}

// loadHCOFromFile loads HCO from a YAML file
func loadHCOFromFile(path string) (*unstructured.Unstructured, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	hco := &unstructured.Unstructured{}
	if err := yaml.Unmarshal(data, hco); err != nil {
		return nil, err
	}

	// Validate it's an HCO
	if hco.GetKind() != "HyperConverged" {
		return nil, fmt.Errorf("expected kind HyperConverged, got %s", hco.GetKind())
	}

	return hco, nil
}

// loadHCOFromCluster loads HCO from the cluster
func loadHCOFromCluster(ctx context.Context, kubeconfigPath string) (*unstructured.Unstructured, error) {
	// Build config
	var config *rest.Config
	var err error

	if kubeconfigPath != "" {
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	} else {
		// In-cluster config
		config, err = rest.InClusterConfig()
	}

	if err != nil {
		return nil, fmt.Errorf("failed to build config: %w", err)
	}

	// Create client
	k8sClient, err := client.New(config, client.Options{})
	if err != nil {
		return nil, fmt.Errorf("failed to create client: %w", err)
	}

	// List HCO resources
	hcoList := &unstructured.UnstructuredList{}
	hcoList.SetGroupVersionKind(pkgcontext.HCOGVK)
	hcoList.SetAPIVersion("hco.kubevirt.io/v1beta1")

	if err := k8sClient.List(ctx, hcoList); err != nil {
		return nil, fmt.Errorf("failed to list HCO: %w", err)
	}

	if len(hcoList.Items) == 0 {
		return nil, fmt.Errorf("no HyperConverged resources found in cluster")
	}

	return &hcoList.Items[0], nil
}

// checkConditions evaluates if an asset's conditions are met
func checkConditions(assetMeta *assets.AssetMetadata, renderCtx *pkgcontext.RenderContext) bool {
	// If no conditions, always include
	if len(assetMeta.Conditions) == 0 {
		return true
	}

	// All conditions must be met (AND logic)
	for _, condition := range assetMeta.Conditions {
		switch condition.Type {
		case assets.ConditionTypeAnnotation:
			annotations := renderCtx.HCO.GetAnnotations()
			if annotations[condition.Key] != condition.Value {
				return false
			}
		case assets.ConditionTypeFeatureGate:
			// Simplified: check if feature gate is in annotations
			featureGates := renderCtx.HCO.GetAnnotations()["platform.kubevirt.io/feature-gates"]
			if !strings.Contains(featureGates, condition.Value) {
				return false
			}
		case assets.ConditionTypeHardwareDetection:
			// Hardware detection requires node access - cannot check in offline mode
			return false
		}
	}

	return true
}

// writeOutput writes the rendered assets in the requested format
func writeOutput(outputs []RenderOutput, format string) error {
	switch format {
	case "yaml":
		return writeYAMLOutput(outputs)
	case "json":
		return writeJSONOutput(outputs)
	case "status":
		return writeStatusOutput(outputs)
	default:
		return fmt.Errorf("unsupported output format: %s", format)
	}
}

// writeYAMLOutput writes multi-document YAML
func writeYAMLOutput(outputs []RenderOutput) error {
	for _, output := range outputs {
		// Write comment header
		fmt.Printf("# Asset: %s\n", output.Asset)
		fmt.Printf("# Path: %s\n", output.Path)
		fmt.Printf("# Component: %s\n", output.Component)
		fmt.Printf("# Status: %s\n", output.Status)
		if output.Reason != "" {
			fmt.Printf("# Reason: %s\n", output.Reason)
		}

		// Write object if included
		if output.Object != nil {
			yamlData, err := yaml.Marshal(output.Object.Object)
			if err != nil {
				return fmt.Errorf("failed to marshal %s: %w", output.Asset, err)
			}
			fmt.Print(string(yamlData))
		}

		fmt.Println("---")
	}
	return nil
}

// writeJSONOutput writes JSON array
func writeJSONOutput(outputs []RenderOutput) error {
	data, err := json.MarshalIndent(outputs, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal JSON: %w", err)
	}
	fmt.Println(string(data))
	return nil
}

// writeStatusOutput writes a status table
func writeStatusOutput(outputs []RenderOutput) error {
	fmt.Printf("%-30s %-15s %-20s %s\n", "ASSET", "STATUS", "COMPONENT", "REASON")
	fmt.Println(strings.Repeat("-", 100))

	for _, output := range outputs {
		reason := output.Reason
		if reason == "" {
			reason = "-"
		}
		fmt.Printf("%-30s %-15s %-20s %s\n",
			truncate(output.Asset, 30),
			output.Status,
			truncate(output.Component, 20),
			truncate(reason, 35))
	}

	// Print summary
	included := 0
	excluded := 0
	filtered := 0
	errors := 0

	for _, output := range outputs {
		switch output.Status {
		case "INCLUDED":
			included++
		case "EXCLUDED":
			excluded++
		case "FILTERED":
			filtered++
		case "ERROR":
			errors++
		}
	}

	fmt.Println(strings.Repeat("-", 100))
	fmt.Printf("Summary: %d included, %d excluded, %d filtered, %d errors\n", included, excluded, filtered, errors)

	return nil
}

// truncate truncates a string to maxLen characters
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
