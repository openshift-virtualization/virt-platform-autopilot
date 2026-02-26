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
	pkgrender "github.com/kubevirt/virt-platform-autopilot/pkg/render"
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

	if kubeconfig == "" && hcoFile == "" {
		return fmt.Errorf("either --kubeconfig or --hco-file must be specified")
	}
	if kubeconfig != "" && hcoFile != "" {
		return fmt.Errorf("--kubeconfig and --hco-file are mutually exclusive")
	}

	loader := assets.NewLoader()
	registry, err := assets.NewRegistry(loader)
	if err != nil {
		return fmt.Errorf("failed to load asset registry: %w", err)
	}

	renderer := engine.NewRenderer(loader)

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

	renderCtx := pkgcontext.NewRenderContext(hco)

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

	outputs := pkgrender.BuildOutputs(assetsToRender, renderer, renderCtx, showExcluded)

	return writeOutput(outputs, outputFormat)
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

	if hco.GetKind() != "HyperConverged" {
		return nil, fmt.Errorf("expected kind HyperConverged, got %s", hco.GetKind())
	}

	return hco, nil
}

// loadHCOFromCluster loads HCO from the cluster
func loadHCOFromCluster(ctx context.Context, kubeconfigPath string) (*unstructured.Unstructured, error) {
	var config *rest.Config
	var err error

	if kubeconfigPath != "" {
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	} else {
		config, err = rest.InClusterConfig()
	}
	if err != nil {
		return nil, fmt.Errorf("failed to build config: %w", err)
	}

	k8sClient, err := client.New(config, client.Options{})
	if err != nil {
		return nil, fmt.Errorf("failed to create client: %w", err)
	}

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

// writeOutput writes the rendered assets in the requested format
func writeOutput(outputs []pkgrender.RenderOutput, format string) error {
	switch format {
	case "yaml":
		return pkgrender.WriteYAML(os.Stdout, outputs)
	case "json":
		return pkgrender.WriteJSON(os.Stdout, outputs)
	case "status":
		return writeStatusOutput(outputs)
	default:
		return fmt.Errorf("unsupported output format: %s", format)
	}
}

// writeStatusOutput writes a status table (CLI-only format)
func writeStatusOutput(outputs []pkgrender.RenderOutput) error {
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

	included, excluded, filtered, errors := 0, 0, 0, 0
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
