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

// Package render provides shared types and helpers used by both the render CLI
// subcommand and the debug HTTP endpoints.
package render

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"

	"github.com/kubevirt/virt-platform-autopilot/pkg/assets"
	pkgcontext "github.com/kubevirt/virt-platform-autopilot/pkg/context"
	"github.com/kubevirt/virt-platform-autopilot/pkg/engine"
)

// RenderOutput represents the rendering result for a single asset.
type RenderOutput struct {
	Asset      string                     `json:"asset" yaml:"asset"`
	Path       string                     `json:"path" yaml:"path"`
	Component  string                     `json:"component" yaml:"component"`
	Status     string                     `json:"status" yaml:"status"`
	Reason     string                     `json:"reason,omitempty" yaml:"reason,omitempty"`
	Conditions []assets.AssetCondition    `json:"conditions,omitempty" yaml:"conditions,omitempty"`
	Object     *unstructured.Unstructured `json:"object,omitempty" yaml:"object,omitempty"`
}

// CheckConditions reports whether all of an asset's conditions are satisfied.
// All conditions are evaluated with AND logic; an asset with no conditions is
// always considered satisfied.
func CheckConditions(assetMeta *assets.AssetMetadata, renderCtx *pkgcontext.RenderContext) bool {
	if len(assetMeta.Conditions) == 0 {
		return true
	}

	for _, condition := range assetMeta.Conditions {
		switch condition.Type {
		case assets.ConditionTypeAnnotation:
			if renderCtx.HCO.GetAnnotations()[condition.Key] != condition.Value {
				return false
			}
		case assets.ConditionTypeFeatureGate:
			featureGates := renderCtx.HCO.GetAnnotations()["platform.kubevirt.io/feature-gates"]
			if !strings.Contains(featureGates, condition.Value) {
				return false
			}
		case assets.ConditionTypeHardwareDetection:
			// Hardware detection requires node access which is not available here.
			return false
		}
	}

	return true
}

// BuildOutputs renders each asset in assetList and returns one RenderOutput per
// asset. Assets that are excluded or filtered are only included when
// showExcluded is true. Root-exclusion rules are parsed once before the loop;
// if the disabled-resources annotation is malformed the exclusion check is
// skipped (fail-open).
func BuildOutputs(
	assetList []assets.AssetMetadata,
	renderer *engine.Renderer,
	renderCtx *pkgcontext.RenderContext,
	showExcluded bool,
) []RenderOutput {
	// Parse root-exclusion rules once before iterating.
	var exclusionRules []engine.ExclusionRule
	if ann := renderCtx.HCO.GetAnnotations()[engine.DisabledResourcesAnnotation]; ann != "" {
		if rules, err := engine.ParseDisabledResources(ann); err == nil {
			exclusionRules = rules
		}
		// On parse error leave exclusionRules nil → no resources excluded (fail-open).
	}

	outputs := make([]RenderOutput, 0, len(assetList))
	for _, assetMeta := range assetList {
		output := RenderOutput{
			Asset:      assetMeta.Name,
			Path:       assetMeta.Path,
			Component:  assetMeta.Component,
			Conditions: assetMeta.Conditions,
		}

		if !CheckConditions(&assetMeta, renderCtx) {
			output.Status = "EXCLUDED"
			output.Reason = "Conditions not met"
			if showExcluded {
				outputs = append(outputs, output)
			}
			continue
		}

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

		if engine.IsResourceExcluded(rendered.GetKind(), rendered.GetNamespace(), rendered.GetName(), exclusionRules) {
			output.Status = "FILTERED"
			output.Reason = "Root exclusion (disabled-resources annotation)"
			if showExcluded {
				outputs = append(outputs, output)
			}
			continue
		}

		output.Status = "INCLUDED"
		output.Object = rendered
		outputs = append(outputs, output)
	}

	return outputs
}

// WriteYAML writes outputs as multi-document YAML with comment headers to w.
// The result is directly usable with kubectl apply.
func WriteYAML(w io.Writer, outputs []RenderOutput) error {
	for _, output := range outputs {
		fmt.Fprintf(w, "# Asset: %s\n", output.Asset)
		fmt.Fprintf(w, "# Path: %s\n", output.Path)
		fmt.Fprintf(w, "# Component: %s\n", output.Component)
		fmt.Fprintf(w, "# Status: %s\n", output.Status)
		if output.Reason != "" {
			fmt.Fprintf(w, "# Reason: %s\n", output.Reason)
		}
		if output.Object != nil {
			data, err := yaml.Marshal(output.Object.Object)
			if err != nil {
				return fmt.Errorf("failed to marshal %s: %w", output.Asset, err)
			}
			fmt.Fprint(w, string(data))
		}
		fmt.Fprintln(w, "---")
	}
	return nil
}

// WriteJSON writes outputs as a JSON array to w.
func WriteJSON(w io.Writer, outputs []RenderOutput) error {
	data, err := json.MarshalIndent(outputs, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal JSON: %w", err)
	}
	fmt.Fprintln(w, string(data))
	return nil
}
