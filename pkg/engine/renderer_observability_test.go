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

func renderObservabilityAsset(t *testing.T, assetName string) *unstructured.Unstructured {
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

func TestMonitoringUIPlugin(t *testing.T) {
	assertAlwaysInstallNoConditions(t, "monitoring-ui-plugin")
	rendered := renderObservabilityAsset(t, "monitoring-ui-plugin")

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

	incidentsEnabled, found, _ := unstructured.NestedBool(rendered.Object, "spec", "monitoring", "incidents", "enabled")
	if !found {
		t.Fatal("spec.monitoring.incidents.enabled not found")
	}
	if !incidentsEnabled {
		t.Error("spec.monitoring.incidents.enabled should be true")
	}
}

func TestTroubleshootingPanelUIPlugin(t *testing.T) {
	assertAlwaysInstallNoConditions(t, "troubleshooting-panel-ui-plugin")
	rendered := renderObservabilityAsset(t, "troubleshooting-panel-ui-plugin")

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
