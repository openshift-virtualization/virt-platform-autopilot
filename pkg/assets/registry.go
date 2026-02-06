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

package assets

import (
	"context"
	"fmt"
	"sort"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"
)

// InstallMode defines when an asset should be installed
type InstallMode string

const (
	InstallModeAlways InstallMode = "always"
	InstallModeOptIn  InstallMode = "opt-in"
)

// ConditionType defines the type of condition for asset activation
type ConditionType string

const (
	ConditionTypeHardwareDetection ConditionType = "hardware-detection"
	ConditionTypeFeatureGate       ConditionType = "feature-gate"
	ConditionTypeAnnotation        ConditionType = "annotation"
)

// AssetCondition defines a condition that must be met for an asset to be applied
type AssetCondition struct {
	Type     ConditionType `json:"type"`
	Detector string        `json:"detector,omitempty"` // For hardware-detection
	Key      string        `json:"key,omitempty"`      // For annotation
	Value    string        `json:"value,omitempty"`    // For annotation/feature-gate
}

// AssetMetadata defines the metadata for a managed asset
type AssetMetadata struct {
	Name            string                     `json:"name"`
	Path            string                     `json:"path"`
	Phase           int                        `json:"phase"`
	Install         InstallMode                `json:"install"`
	Component       string                     `json:"component"`
	ReconcileOrder  int                        `json:"reconcile_order"`
	Conditions      []AssetCondition           `json:"conditions,omitempty"`
	RenderedContent *unstructured.Unstructured `json:"-"` // Cached rendered content
}

// AssetCatalog contains all asset metadata
type AssetCatalog struct {
	Assets []AssetMetadata `json:"assets"`
}

// Registry manages the asset catalog and provides querying capabilities
type Registry struct {
	catalog *AssetCatalog
	loader  *Loader
}

// NewRegistry creates a new asset registry
func NewRegistry(loader *Loader) (*Registry, error) {
	// Load metadata.yaml
	data, err := loader.LoadAsset("metadata.yaml")
	if err != nil {
		return nil, fmt.Errorf("failed to load asset catalog: %w", err)
	}

	catalog := &AssetCatalog{}
	if err := yaml.Unmarshal(data, catalog); err != nil {
		return nil, fmt.Errorf("failed to parse asset catalog: %w", err)
	}

	return &Registry{
		catalog: catalog,
		loader:  loader,
	}, nil
}

// GetAsset returns asset metadata by name
func (r *Registry) GetAsset(name string) (*AssetMetadata, error) {
	for i := range r.catalog.Assets {
		if r.catalog.Assets[i].Name == name {
			return &r.catalog.Assets[i], nil
		}
	}
	return nil, fmt.Errorf("asset %s not found", name)
}

// ListAssets returns all assets, optionally filtered by phase
func (r *Registry) ListAssets(phase *int) []AssetMetadata {
	if phase == nil {
		return r.catalog.Assets
	}

	var filtered []AssetMetadata
	for _, asset := range r.catalog.Assets {
		if asset.Phase == *phase {
			filtered = append(filtered, asset)
		}
	}
	return filtered
}

// ListAssetsByReconcileOrder returns assets sorted by reconcile_order
func (r *Registry) ListAssetsByReconcileOrder() []AssetMetadata {
	sorted := make([]AssetMetadata, len(r.catalog.Assets))
	copy(sorted, r.catalog.Assets)

	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].ReconcileOrder < sorted[j].ReconcileOrder
	})

	return sorted
}

// ShouldApply determines if an asset should be applied based on its conditions
func (r *Registry) ShouldApply(ctx context.Context, asset *AssetMetadata, evalContext ConditionEvaluator) (bool, error) {
	// Always apply if install mode is "always" and no conditions
	if asset.Install == InstallModeAlways && len(asset.Conditions) == 0 {
		return true, nil
	}

	// For opt-in assets, check if explicitly enabled
	if asset.Install == InstallModeOptIn && len(asset.Conditions) == 0 {
		return false, nil // Opt-in requires explicit condition
	}

	// Evaluate all conditions (AND logic - all must be true)
	for _, condition := range asset.Conditions {
		satisfied, err := evalContext.EvaluateCondition(ctx, condition)
		if err != nil {
			return false, fmt.Errorf("failed to evaluate condition %v for asset %s: %w", condition, asset.Name, err)
		}

		if !satisfied {
			return false, nil
		}
	}

	return true, nil
}

// ConditionEvaluator defines the interface for evaluating asset conditions
type ConditionEvaluator interface {
	EvaluateCondition(ctx context.Context, condition AssetCondition) (bool, error)
}

// DefaultConditionEvaluator provides default condition evaluation logic
type DefaultConditionEvaluator struct {
	HardwareContext map[string]bool   // Hardware detection results
	FeatureGates    map[string]bool   // Feature gate states
	Annotations     map[string]string // Annotation values
}

// EvaluateCondition evaluates a single condition
func (e *DefaultConditionEvaluator) EvaluateCondition(ctx context.Context, condition AssetCondition) (bool, error) {
	switch condition.Type {
	case ConditionTypeHardwareDetection:
		if condition.Detector == "" {
			return false, fmt.Errorf("hardware-detection condition requires detector field")
		}
		detected, ok := e.HardwareContext[condition.Detector]
		return ok && detected, nil

	case ConditionTypeFeatureGate:
		if condition.Value == "" {
			return false, fmt.Errorf("feature-gate condition requires value field")
		}
		enabled, ok := e.FeatureGates[condition.Value]
		return ok && enabled, nil

	case ConditionTypeAnnotation:
		if condition.Key == "" {
			return false, fmt.Errorf("annotation condition requires key field")
		}
		actualValue, ok := e.Annotations[condition.Key]
		if !ok {
			return false, nil
		}
		// If no value specified, just check existence
		if condition.Value == "" {
			return true, nil
		}
		return actualValue == condition.Value, nil

	default:
		return false, fmt.Errorf("unknown condition type: %s", condition.Type)
	}
}
