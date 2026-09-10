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

package engine

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/kubevirt/virt-platform-autopilot/pkg/assets"
	pkgcontext "github.com/kubevirt/virt-platform-autopilot/pkg/context"
)

func renderObservabilityAsset(t *testing.T, assetName string, annotations map[string]string) *unstructured.Unstructured {
	t.Helper()

	loader := assets.NewLoader()
	registry, err := assets.NewRegistry(loader)
	if err != nil {
		t.Fatalf("Failed to create registry: %v", err)
	}

	renderer := NewRenderer(loader)

	hco := &unstructured.Unstructured{}
	hco.SetAPIVersion("hco.kubevirt.io/v1")
	hco.SetKind("HyperConverged")
	hco.SetName("kubevirt-hyperconverged")
	hco.SetNamespace("openshift-cnv")
	if annotations != nil {
		hco.SetAnnotations(annotations)
	}

	renderCtx := &pkgcontext.RenderContext{
		HCO: hco,
	}

	asset, err := registry.GetAsset(assetName)
	if err != nil {
		t.Fatalf("Failed to get asset %q: %v", assetName, err)
	}

	rendered, err := renderer.RenderAsset(asset, renderCtx)
	if err != nil {
		t.Fatalf("Failed to render asset %q: %v", assetName, err)
	}

	if rendered == nil {
		t.Fatalf("Rendered asset %q is nil", assetName)
	}

	return rendered
}

func assertAlwaysInstallNoConditions(t *testing.T, assetName string) {
	t.Helper()

	registry, err := assets.NewRegistry(assets.NewLoader())
	if err != nil {
		t.Fatalf("Failed to create registry: %v", err)
	}
	asset, err := registry.GetAsset(assetName)
	if err != nil {
		t.Fatalf("Failed to get asset %q: %v", assetName, err)
	}
	if asset.Install != assets.InstallModeAlways {
		t.Errorf("%s Install = %q, want %q", assetName, asset.Install, assets.InstallModeAlways)
	}
	if len(asset.Conditions) != 0 {
		t.Errorf("%s conditions = %#v, want none", assetName, asset.Conditions)
	}
}

func TestMonitoringUIPluginWithoutIncidents(t *testing.T) {
	rendered := renderObservabilityAsset(t, "monitoring-ui-plugin", nil)

	if rendered.GetKind() != "UIPlugin" {
		t.Errorf("Kind = %q, want UIPlugin", rendered.GetKind())
	}
	if rendered.GetName() != "monitoring" {
		t.Errorf("Name = %q, want monitoring", rendered.GetName())
	}

	pluginType, _, _ := unstructured.NestedString(rendered.Object, "spec", "type")
	if pluginType != "Monitoring" {
		t.Errorf("spec.type = %q, want Monitoring", pluginType)
	}

	persesEnabled, found, _ := unstructured.NestedBool(rendered.Object, "spec", "monitoring", "perses", "enabled")
	if !found {
		t.Fatal("spec.monitoring.perses.enabled not found")
	}
	if !persesEnabled {
		t.Error("spec.monitoring.perses.enabled should be true")
	}

	_, found, _ = unstructured.NestedBool(rendered.Object, "spec", "monitoring", "incidents", "enabled")
	if found {
		t.Error("spec.monitoring.incidents should NOT be present on the base monitoring UIPlugin")
	}
}

func TestMonitoringUIPluginIncidentsAsset(t *testing.T) {
	assertAlwaysInstallNoConditions(t, "monitoring-ui-plugin-incidents")

	baseRendered := renderObservabilityAsset(t, "monitoring-ui-plugin", nil)
	incidentsRendered := renderObservabilityAsset(t, "monitoring-ui-plugin-incidents", nil)

	persesEnabled, found, _ := unstructured.NestedBool(incidentsRendered.Object, "spec", "monitoring", "perses", "enabled")
	if !found {
		t.Fatal("incidents asset: spec.monitoring.perses.enabled not found")
	}
	if !persesEnabled {
		t.Error("incidents asset: spec.monitoring.perses.enabled should be true")
	}

	incidentsEnabled, found, _ := unstructured.NestedBool(incidentsRendered.Object, "spec", "monitoring", "incidents", "enabled")
	if !found {
		t.Fatal("incidents asset: spec.monitoring.incidents.enabled not found")
	}
	if !incidentsEnabled {
		t.Error("incidents asset: spec.monitoring.incidents.enabled should be true")
	}

	if baseRendered.GetName() != incidentsRendered.GetName() {
		t.Errorf("assets target different resources: base=%s, incidents=%s", baseRendered.GetName(), incidentsRendered.GetName())
	}
}

func TestTroubleshootingPanelUIPlugin(t *testing.T) {
	assertAlwaysInstallNoConditions(t, "troubleshooting-panel-ui-plugin")
	rendered := renderObservabilityAsset(t, "troubleshooting-panel-ui-plugin", nil)

	if rendered.GetKind() != "UIPlugin" {
		t.Errorf("Kind = %q, want UIPlugin", rendered.GetKind())
	}
	if rendered.GetName() != "troubleshooting-panel" {
		t.Errorf("Name = %q, want troubleshooting-panel", rendered.GetName())
	}

	pluginType, _, _ := unstructured.NestedString(rendered.Object, "spec", "type")
	if pluginType != "TroubleshootingPanel" {
		t.Errorf("spec.type = %q, want TroubleshootingPanel", pluginType)
	}
}
