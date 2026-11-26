/*
Copyright 2026 The Virt Platform Autopilot Authors.

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
	"testing"
)

func TestDeriveFeatureOptInIncludesUserFacingConditionTypes(t *testing.T) {
	resolved := []*AssetMetadata{
		{
			Name: "pci-passthrough",
			Conditions: []AssetCondition{
				{Type: ConditionTypeAnnotation, Key: "platform.kubevirt.io/openshift", Value: "true"},
				{Type: ConditionTypeHardwareDetection, Detector: "pciDevicesPresent"},
			},
		},
		{
			Name: "ksm-zero-only-master",
			Conditions: []AssetCondition{
				{Type: ConditionTypeAnnotation, Key: "platform.kubevirt.io/enable-ksm-zero-only", Value: "true"},
				{Type: ConditionTypeHCOFieldUnconfigured, Path: "spec.virtualization.ksmConfiguration"},
				{Type: ConditionTypeTopology, Field: "hasSchedulableMasters"},
			},
		},
	}

	got := deriveFeatureOptIn(resolved)
	if got == nil {
		t.Fatal("deriveFeatureOptIn() = nil, want conditions")
	}

	want := "platform.kubevirt.io/openshift=true, platform.kubevirt.io/enable-ksm-zero-only=true, hcoUnconfigured:spec.virtualization.ksmConfiguration"
	if *got != want {
		t.Errorf("deriveFeatureOptIn() = %q, want %q", *got, want)
	}
}

func TestDeriveFeatureOptInSkipsInternalConditionTypes(t *testing.T) {
	resolved := []*AssetMetadata{
		{
			Name: "psi-enable-master",
			Conditions: []AssetCondition{
				{Type: ConditionTypeTopology, Field: "hasSchedulableMasters"},
			},
		},
		{
			Name: "kubevirt-metrics-exporter",
			Conditions: []AssetCondition{
				{Type: ConditionTypeImage, Key: "kubevirt-metrics-exporter"},
			},
		},
	}

	if got := deriveFeatureOptIn(resolved); got != nil {
		t.Errorf("deriveFeatureOptIn() = %q, want nil", *got)
	}
}

func TestSortFeatureStatuses(t *testing.T) {
	statuses := []FeatureStatus{
		{Name: "Zebra", Maturity: "DP"},
		{Name: "Beta", Maturity: "GA"},
		{Name: "Alpha", Maturity: "GA"},
		{Name: "Charlie", Maturity: "TP"},
	}

	sortFeatureStatuses(statuses)

	want := []string{"Alpha", "Beta", "Charlie", "Zebra"}
	for i, name := range want {
		if statuses[i].Name != name {
			t.Fatalf("statuses[%d].Name = %q, want %q", i, statuses[i].Name, name)
		}
	}
}

func TestDeriveFeatureStatusesIncludesRecommended(t *testing.T) {
	catalog := &AssetCatalog{
		Assets: []AssetMetadata{
			{Name: "kubevirt-metrics-exporter", Install: InstallModeOptIn},
		},
		Features: []FeatureMetadata{
			{
				ID:          "kubevirt-metrics-exporter",
				Name:        "KubeVirt Metrics Exporter",
				Description: "Per-node metrics exporter",
				Groups:      []string{},
				Assets:      []string{"kubevirt-metrics-exporter"},
				Recommended: []string{"Cluster Observability Operator"},
			},
		},
	}

	statuses, err := deriveFeatureStatuses(catalog)
	if err != nil {
		t.Fatalf("deriveFeatureStatuses() error = %v", err)
	}
	if len(statuses) != 1 {
		t.Fatalf("deriveFeatureStatuses() returned %d statuses, want 1", len(statuses))
	}
	if len(statuses[0].Recommended) != 1 || statuses[0].Recommended[0] != "Cluster Observability Operator" {
		t.Fatalf("status.Recommended = %v, want [Cluster Observability Operator]", statuses[0].Recommended)
	}
}

func TestValidateFeatureCoverageRequiresUniqueStableIDs(t *testing.T) {
	catalog := &AssetCatalog{
		Assets: []AssetMetadata{{Name: "asset-a"}, {Name: "asset-b"}},
		Features: []FeatureMetadata{
			{ID: "duplicate", Name: "A", Assets: []string{"asset-a"}},
			{ID: "duplicate", Name: "B", Assets: []string{"asset-b"}},
		},
	}
	if err := ValidateFeatureCoverage(catalog); err == nil {
		t.Fatal("expected duplicate feature IDs to be rejected")
	}
}

func TestDeriveFeatureStatusIncludesControlsAndRollout(t *testing.T) {
	catalog := &AssetCatalog{
		Assets: []AssetMetadata{
			{Name: "policy", Component: "MachineConfiguration"},
			{Name: "machine-config", Component: "MachineConfig", NodeDisruptionPolicy: &NodeDisruptionPolicyDependency{Asset: "policy"}},
		},
		Features: []FeatureMetadata{{
			ID: "example", Name: "Example", Assets: []string{"policy", "machine-config"},
			Enablement: []FeatureEnablementControl{{ID: "example", Label: "Enable example", Annotation: "platform.kubevirt.io/enable-example", Value: "true"}},
			Rollout:    &FeatureRollout{ExpectedDisruption: "serviceRestart"},
		}},
	}
	statuses, err := deriveFeatureStatuses(catalog)
	if err != nil {
		t.Fatal(err)
	}
	got := statuses[0]
	if got.ID != "example" || len(got.Assets) != 2 || len(got.Enablement) != 1 || got.Rollout == nil || got.Rollout.Coverage != "full" || got.Rollout.PolicyAssetIDs[0] != "policy" {
		t.Fatalf("unexpected derived feature status: %#v", got)
	}
}

func TestDeriveFeatureStatusRequiresRolloutForMachineConfig(t *testing.T) {
	_, err := deriveFeatureStatuses(&AssetCatalog{Assets: []AssetMetadata{{Name: "machine-config", Component: "MachineConfig"}}, Features: []FeatureMetadata{{ID: "example", Name: "Example", Assets: []string{"machine-config"}}}})
	if err == nil {
		t.Fatal("expected MachineConfig rollout declaration to be required")
	}
}

func TestValidateFeatureControlsMatchAssetConditions(t *testing.T) {
	asset := AssetMetadata{Name: "asset", Conditions: []AssetCondition{{Type: ConditionTypeAnnotation, Key: "platform.kubevirt.io/enable-example", Value: "true"}}}
	base := FeatureMetadata{ID: "example", Name: "Example", Assets: []string{"asset"}, Enablement: []FeatureEnablementControl{{ID: "example", Label: "Enable example", Annotation: "platform.kubevirt.io/enable-example", Value: "true", Assets: []string{"asset"}}}}
	if err := validateFeatureControls(base, []AssetMetadata{asset}); err != nil {
		t.Fatalf("valid control rejected: %v", err)
	}

	missing := base
	missing.Enablement = nil
	if err := validateFeatureControls(missing, []AssetMetadata{asset}); err == nil {
		t.Fatal("missing control for enable annotation was accepted")
	}

	mismatched := base
	mismatched.Enablement = []FeatureEnablementControl{{ID: "example", Label: "Enable example", Annotation: "platform.kubevirt.io/enable-typo", Value: "true"}}
	if err := validateFeatureControls(mismatched, []AssetMetadata{asset}); err == nil {
		t.Fatal("control without matching asset condition was accepted")
	}

	omitted := base
	omitted.Assets = []string{"asset", "other"}
	omitted.Enablement = []FeatureEnablementControl{{ID: "example", Label: "Enable example", Annotation: "platform.kubevirt.io/enable-example", Value: "true", Assets: []string{"other"}}}
	if err := validateFeatureControls(omitted, []AssetMetadata{asset, {Name: "other"}}); err == nil {
		t.Fatal("control that omits its affected asset was accepted")
	}
}

func TestDeriveFeatureRolloutCoverage(t *testing.T) {
	policy := AssetMetadata{Name: "policy", Component: "MachineConfiguration"}
	protected := AssetMetadata{Name: "protected", Component: "MachineConfig", NodeDisruptionPolicy: &NodeDisruptionPolicyDependency{Asset: "policy"}}
	unprotected := AssetMetadata{Name: "unprotected", Component: "MachineConfig"}
	feature := FeatureMetadata{Rollout: &FeatureRollout{ExpectedDisruption: "nodeRebootPossible"}}
	for _, testCase := range []struct {
		name     string
		assets   []*AssetMetadata
		coverage string
	}{
		{name: "full", assets: []*AssetMetadata{&policy, &protected}, coverage: "full"},
		{name: "partial", assets: []*AssetMetadata{&policy, &protected, &unprotected}, coverage: "partial"},
		{name: "none", assets: []*AssetMetadata{&unprotected}, coverage: "none"},
		{name: "not applicable", assets: []*AssetMetadata{{Name: "config", Component: "ConfigMap"}}, coverage: "notApplicable"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			got := deriveFeatureRollout(feature, testCase.assets)
			if got.Coverage != testCase.coverage {
				t.Fatalf("coverage = %q, want %q", got.Coverage, testCase.coverage)
			}
		})
	}
}

func TestRegistryFeatureCatalog(t *testing.T) {
	loader := NewLoader()
	registry, err := NewRegistry(loader)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	catalog, err := registry.FeatureCatalog()
	if err != nil {
		t.Fatalf("FeatureCatalog() error = %v", err)
	}

	if catalog.Framework.Maturity == "" {
		t.Fatal("framework maturity is empty")
	}
	if len(catalog.Features) == 0 {
		t.Fatal("expected non-empty feature list")
	}

	for i := 1; i < len(catalog.Features); i++ {
		prev := catalog.Features[i-1]
		curr := catalog.Features[i]
		prevRank := maturityRank(prev.Maturity)
		currRank := maturityRank(curr.Maturity)
		if prevRank > currRank {
			t.Fatalf("features not sorted by maturity: %q before %q", prev.Name, curr.Name)
		}
		if prevRank == currRank && prev.Name > curr.Name {
			t.Fatalf("features not sorted by name within maturity: %q before %q", prev.Name, curr.Name)
		}
	}
}
