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
	"testing"
)

func TestNewRegistry(t *testing.T) {
	t.Run("creates registry successfully", func(t *testing.T) {
		loader := NewLoader()
		registry, err := NewRegistry(loader)

		if err != nil {
			t.Fatalf("NewRegistry() error = %v", err)
		}

		if registry == nil {
			t.Fatal("NewRegistry() returned nil registry")
		}

		if registry.catalog == nil {
			t.Error("Registry catalog is nil")
		}

		if registry.loader != loader {
			t.Error("Registry loader not set correctly")
		}
	})

	t.Run("panics with nil loader", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Error("NewRegistry() should panic with nil loader")
			}
		}()
		_, _ = NewRegistry(nil)
	})
}

func TestGetAsset(t *testing.T) {
	loader := NewLoader()
	registry, err := NewRegistry(loader)
	if err != nil {
		t.Fatalf("Failed to create registry: %v", err)
	}

	t.Run("returns asset when found", func(t *testing.T) {
		// The real metadata.yaml should have assets
		// Let's get the first asset from the catalog
		if len(registry.catalog.Assets) == 0 {
			t.Skip("No assets in catalog")
		}

		expectedName := registry.catalog.Assets[0].Name
		asset, err := registry.GetAsset(expectedName)

		if err != nil {
			t.Errorf("GetAsset() error = %v", err)
		}

		if asset == nil {
			t.Fatal("GetAsset() returned nil asset")
		}

		if asset.Name != expectedName {
			t.Errorf("GetAsset() name = %s, want %s", asset.Name, expectedName)
		}
	})

	t.Run("returns error when asset not found", func(t *testing.T) {
		_, err := registry.GetAsset("non-existent-asset-12345")
		if err == nil {
			t.Error("GetAsset() should return error for non-existent asset")
		}

		expectedError := "asset non-existent-asset-12345 not found"
		if err.Error() != expectedError {
			t.Errorf("GetAsset() error = %v, want %v", err.Error(), expectedError)
		}
	})

	t.Run("handles empty asset name", func(t *testing.T) {
		_, err := registry.GetAsset("")
		if err == nil {
			t.Error("GetAsset() should return error for empty name")
		}
	})
}

func TestListAssets(t *testing.T) {
	loader := NewLoader()
	registry, err := NewRegistry(loader)
	if err != nil {
		t.Fatalf("Failed to create registry: %v", err)
	}

	t.Run("returns all assets when phase is nil", func(t *testing.T) {
		assets := registry.ListAssets(nil)

		if len(assets) != len(registry.catalog.Assets) {
			t.Errorf("ListAssets(nil) returned %d assets, want %d",
				len(assets), len(registry.catalog.Assets))
		}
	})

	t.Run("filters assets by phase", func(t *testing.T) {
		if len(registry.catalog.Assets) == 0 {
			t.Skip("No assets in catalog")
		}

		// Get the phase of the first asset
		targetPhase := registry.catalog.Assets[0].Phase
		assets := registry.ListAssets(&targetPhase)

		// All returned assets should have the target phase
		for _, asset := range assets {
			if asset.Phase != targetPhase {
				t.Errorf("ListAssets() returned asset with phase %d, want %d",
					asset.Phase, targetPhase)
			}
		}

		// Count expected assets with this phase
		expectedCount := 0
		for _, asset := range registry.catalog.Assets {
			if asset.Phase == targetPhase {
				expectedCount++
			}
		}

		if len(assets) != expectedCount {
			t.Errorf("ListAssets() returned %d assets, want %d",
				len(assets), expectedCount)
		}
	})

	t.Run("returns empty list for non-existent phase", func(t *testing.T) {
		nonExistentPhase := 99999
		assets := registry.ListAssets(&nonExistentPhase)

		if len(assets) != 0 {
			t.Errorf("ListAssets() returned %d assets for non-existent phase, want 0",
				len(assets))
		}
	})
}

func TestListAssetsByReconcileOrder(t *testing.T) {
	loader := NewLoader()
	registry, err := NewRegistry(loader)
	if err != nil {
		t.Fatalf("Failed to create registry: %v", err)
	}

	t.Run("returns assets sorted by reconcile_order", func(t *testing.T) {
		assets := registry.ListAssetsByReconcileOrder()

		if len(assets) != len(registry.catalog.Assets) {
			t.Errorf("ListAssetsByReconcileOrder() returned %d assets, want %d",
				len(assets), len(registry.catalog.Assets))
		}

		// Verify sorting
		for i := 1; i < len(assets); i++ {
			if assets[i].ReconcileOrder < assets[i-1].ReconcileOrder {
				t.Errorf("Assets not sorted: asset[%d].ReconcileOrder=%d > asset[%d].ReconcileOrder=%d",
					i-1, assets[i-1].ReconcileOrder, i, assets[i].ReconcileOrder)
			}
		}
	})

	t.Run("does not modify original catalog", func(t *testing.T) {
		originalOrder := make([]string, len(registry.catalog.Assets))
		for i, asset := range registry.catalog.Assets {
			originalOrder[i] = asset.Name
		}

		_ = registry.ListAssetsByReconcileOrder()

		// Verify original catalog unchanged
		for i, asset := range registry.catalog.Assets {
			if asset.Name != originalOrder[i] {
				t.Error("ListAssetsByReconcileOrder() modified original catalog")
			}
		}
	})
}

func TestShouldApply(t *testing.T) {
	ctx := context.Background()

	t.Run("always apply asset with no conditions", func(t *testing.T) {
		asset := &AssetMetadata{
			Name:    "test-asset",
			Install: InstallModeAlways,
		}

		evaluator := &DefaultConditionEvaluator{}

		loader := NewLoader()
		registry, _ := NewRegistry(loader)

		shouldApply, err := registry.ShouldApply(ctx, asset, evaluator)
		if err != nil {
			t.Errorf("ShouldApply() error = %v", err)
		}

		if !shouldApply {
			t.Error("ShouldApply() = false, want true for always-install with no conditions")
		}
	})

	t.Run("opt-in asset with no conditions should not apply", func(t *testing.T) {
		asset := &AssetMetadata{
			Name:    "opt-in-asset",
			Install: InstallModeOptIn,
		}

		evaluator := &DefaultConditionEvaluator{}

		loader := NewLoader()
		registry, _ := NewRegistry(loader)

		shouldApply, err := registry.ShouldApply(ctx, asset, evaluator)
		if err != nil {
			t.Errorf("ShouldApply() error = %v", err)
		}

		if shouldApply {
			t.Error("ShouldApply() = true, want false for opt-in with no conditions")
		}
	})

	t.Run("applies when all conditions are satisfied", func(t *testing.T) {
		asset := &AssetMetadata{
			Name:    "conditional-asset",
			Install: InstallModeAlways,
			Conditions: []AssetCondition{
				{
					Type:     ConditionTypeHardwareDetection,
					Detector: "gpu",
				},
				{
					Type:  ConditionTypeFeatureGate,
					Value: "gpu-passthrough",
				},
			},
		}

		evaluator := &DefaultConditionEvaluator{
			HardwareContext: map[string]bool{
				"gpu": true,
			},
			FeatureGates: map[string]bool{
				"gpu-passthrough": true,
			},
		}

		loader := NewLoader()
		registry, _ := NewRegistry(loader)

		shouldApply, err := registry.ShouldApply(ctx, asset, evaluator)
		if err != nil {
			t.Errorf("ShouldApply() error = %v", err)
		}

		if !shouldApply {
			t.Error("ShouldApply() = false, want true when all conditions satisfied")
		}
	})

	t.Run("does not apply when one condition fails", func(t *testing.T) {
		asset := &AssetMetadata{
			Name:    "conditional-asset",
			Install: InstallModeAlways,
			Conditions: []AssetCondition{
				{
					Type:     ConditionTypeHardwareDetection,
					Detector: "gpu",
				},
				{
					Type:  ConditionTypeFeatureGate,
					Value: "gpu-passthrough",
				},
			},
		}

		evaluator := &DefaultConditionEvaluator{
			HardwareContext: map[string]bool{
				"gpu": true,
			},
			FeatureGates: map[string]bool{
				"gpu-passthrough": false, // This one fails
			},
		}

		loader := NewLoader()
		registry, _ := NewRegistry(loader)

		shouldApply, err := registry.ShouldApply(ctx, asset, evaluator)
		if err != nil {
			t.Errorf("ShouldApply() error = %v", err)
		}

		if shouldApply {
			t.Error("ShouldApply() = true, want false when condition not satisfied")
		}
	})

	t.Run("returns error when condition evaluation fails", func(t *testing.T) {
		asset := &AssetMetadata{
			Name:    "invalid-condition-asset",
			Install: InstallModeAlways,
			Conditions: []AssetCondition{
				{
					Type: ConditionType("invalid-type"),
				},
			},
		}

		evaluator := &DefaultConditionEvaluator{}

		loader := NewLoader()
		registry, _ := NewRegistry(loader)

		_, err := registry.ShouldApply(ctx, asset, evaluator)
		if err == nil {
			t.Error("ShouldApply() should return error for invalid condition")
		}
	})
}

func TestDefaultConditionEvaluator_EvaluateCondition(t *testing.T) {
	ctx := context.Background()

	t.Run("hardware detection conditions", func(t *testing.T) {
		testHardwareDetectionConditions(ctx, t)
	})

	t.Run("feature gate conditions", func(t *testing.T) {
		testFeatureGateConditions(ctx, t)
	})

	t.Run("annotation conditions", func(t *testing.T) {
		testAnnotationConditions(ctx, t)
	})

	t.Run("unknown condition type", func(t *testing.T) {
		evaluator := &DefaultConditionEvaluator{}
		condition := AssetCondition{Type: ConditionType("unknown-type")}

		_, err := evaluator.EvaluateCondition(ctx, condition)
		if err == nil {
			t.Error("EvaluateCondition() should return error for unknown condition type")
		}
	})
}

func testHardwareDetectionConditions(ctx context.Context, t *testing.T) {
	t.Helper()

	tests := []struct {
		name          string
		hardware      map[string]bool
		detector      string
		wantSatisfied bool
		wantErr       bool
	}{
		{"satisfied", map[string]bool{"gpu": true}, "gpu", true, false},
		{"not satisfied", map[string]bool{"gpu": false}, "gpu", false, false},
		{"missing detector", map[string]bool{}, "", false, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			evaluator := &DefaultConditionEvaluator{HardwareContext: tt.hardware}
			condition := AssetCondition{Type: ConditionTypeHardwareDetection, Detector: tt.detector}

			satisfied, err := evaluator.EvaluateCondition(ctx, condition)
			if (err != nil) != tt.wantErr {
				t.Errorf("EvaluateCondition() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && satisfied != tt.wantSatisfied {
				t.Errorf("EvaluateCondition() = %v, want %v", satisfied, tt.wantSatisfied)
			}
		})
	}
}

func testFeatureGateConditions(ctx context.Context, t *testing.T) {
	t.Helper()

	tests := []struct {
		name          string
		gates         map[string]bool
		value         string
		wantSatisfied bool
		wantErr       bool
	}{
		{"enabled", map[string]bool{"experimental-feature": true}, "experimental-feature", true, false},
		{"disabled", map[string]bool{"experimental-feature": false}, "experimental-feature", false, false},
		{"missing value", map[string]bool{}, "", false, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			evaluator := &DefaultConditionEvaluator{FeatureGates: tt.gates}
			condition := AssetCondition{Type: ConditionTypeFeatureGate, Value: tt.value}

			satisfied, err := evaluator.EvaluateCondition(ctx, condition)
			if (err != nil) != tt.wantErr {
				t.Errorf("EvaluateCondition() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && satisfied != tt.wantSatisfied {
				t.Errorf("EvaluateCondition() = %v, want %v", satisfied, tt.wantSatisfied)
			}
		})
	}
}

func testAnnotationConditions(ctx context.Context, t *testing.T) {
	t.Helper()

	tests := []struct {
		name          string
		annotations   map[string]string
		key           string
		value         string
		wantSatisfied bool
		wantErr       bool
	}{
		{"exact match", map[string]string{"enable-gpu": "true"}, "enable-gpu", "true", true, false},
		{"value mismatch", map[string]string{"enable-gpu": "false"}, "enable-gpu", "true", false, false},
		{"existence check", map[string]string{"enable-gpu": "anything"}, "enable-gpu", "", true, false},
		{"missing annotation", map[string]string{}, "missing-key", "", false, false},
		{"missing key", map[string]string{}, "", "", false, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			evaluator := &DefaultConditionEvaluator{Annotations: tt.annotations}
			condition := AssetCondition{Type: ConditionTypeAnnotation, Key: tt.key, Value: tt.value}

			satisfied, err := evaluator.EvaluateCondition(ctx, condition)
			if (err != nil) != tt.wantErr {
				t.Errorf("EvaluateCondition() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && satisfied != tt.wantSatisfied {
				t.Errorf("EvaluateCondition() = %v, want %v", satisfied, tt.wantSatisfied)
			}
		})
	}
}

// TestOptInAssetsHaveConditions validates that all opt-in assets in metadata.yaml
// have at least one condition. Opt-in assets without conditions will never be applied
// (see pkg/assets/registry.go:140-142), which is likely a configuration error.
func TestOptInAssetsHaveConditions(t *testing.T) {
	loader := NewLoader()
	registry, err := NewRegistry(loader)
	if err != nil {
		t.Fatalf("Failed to create registry: %v", err)
	}

	allAssets := registry.catalog.Assets
	var violations []string

	for _, asset := range allAssets {
		if asset.Install == InstallModeOptIn && len(asset.Conditions) == 0 {
			violations = append(violations, asset.Name)
		}
	}

	if len(violations) > 0 {
		t.Errorf("Found %d opt-in asset(s) without conditions (will never be applied): %v\n"+
			"Opt-in assets require at least one condition to be activated.\n"+
			"Either add a condition or change install mode to 'always'.",
			len(violations), violations)
	}
}
