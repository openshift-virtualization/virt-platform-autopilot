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

package engine

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/kubevirt/virt-platform-autopilot/pkg/assets"
	pkgcontext "github.com/kubevirt/virt-platform-autopilot/pkg/context"
)

func renderLoggingAsset(t *testing.T, assetName string, topology *pkgcontext.TopologyContext, annotations map[string]string) *unstructured.Unstructured {
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
		HCO:      hco,
		Topology: topology,
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

func TestLokiStackSizing(t *testing.T) {
	tests := []struct {
		name     string
		topology *pkgcontext.TopologyContext
		wantSize string
	}{
		{
			name:     "HCP cluster gets extra-small",
			topology: &pkgcontext.TopologyContext{IsHCP: true, TotalNodeCount: 50},
			wantSize: "1x.extra-small",
		},
		{
			name:     "SNO (1 node) gets pico",
			topology: &pkgcontext.TopologyContext{TotalNodeCount: 1},
			wantSize: "1x.pico",
		},
		{
			name:     "3-node compact gets pico",
			topology: &pkgcontext.TopologyContext{TotalNodeCount: 3, IsCompact: true},
			wantSize: "1x.pico",
		},
		{
			name:     "5-node cluster gets extra-small",
			topology: &pkgcontext.TopologyContext{TotalNodeCount: 5},
			wantSize: "1x.extra-small",
		},
		{
			name:     "10-node cluster gets extra-small",
			topology: &pkgcontext.TopologyContext{TotalNodeCount: 10},
			wantSize: "1x.extra-small",
		},
		{
			name:     "11-node cluster gets medium",
			topology: &pkgcontext.TopologyContext{TotalNodeCount: 11},
			wantSize: "1x.medium",
		},
		{
			name:     "50-node cluster gets medium",
			topology: &pkgcontext.TopologyContext{TotalNodeCount: 50},
			wantSize: "1x.medium",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rendered := renderLoggingAsset(t, "logging-lokistack", tt.topology, nil)

			size, found, err := unstructured.NestedString(rendered.Object, "spec", "size")
			if err != nil {
				t.Fatalf("Error accessing spec.size: %v", err)
			}
			if !found {
				t.Fatal("spec.size not found in rendered LokiStack")
			}
			if size != tt.wantSize {
				t.Errorf("spec.size = %q, want %q", size, tt.wantSize)
			}
		})
	}
}

func TestLokiStackStorageType(t *testing.T) {
	tests := []struct {
		name     string
		topology *pkgcontext.TopologyContext
		wantType string
	}{
		{
			name:     "AWS uses s3",
			topology: &pkgcontext.TopologyContext{IsAWS: true, TotalNodeCount: 5},
			wantType: "s3",
		},
		{
			name:     "Azure uses azure",
			topology: &pkgcontext.TopologyContext{IsAzure: true, TotalNodeCount: 5},
			wantType: "azure",
		},
		{
			name:     "GCP uses gcs",
			topology: &pkgcontext.TopologyContext{IsGCP: true, TotalNodeCount: 5},
			wantType: "gcs",
		},
		{
			name:     "BareMetal uses s3",
			topology: &pkgcontext.TopologyContext{IsBareMetal: true, TotalNodeCount: 5},
			wantType: "s3",
		},
		{
			name:     "vSphere uses s3",
			topology: &pkgcontext.TopologyContext{IsVSphere: true, TotalNodeCount: 5},
			wantType: "s3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rendered := renderLoggingAsset(t, "logging-lokistack", tt.topology, nil)

			storageType, found, err := unstructured.NestedString(rendered.Object, "spec", "storage", "secret", "type")
			if err != nil {
				t.Fatalf("Error accessing spec.storage.secret.type: %v", err)
			}
			if !found {
				t.Fatal("spec.storage.secret.type not found")
			}
			if storageType != tt.wantType {
				t.Errorf("spec.storage.secret.type = %q, want %q", storageType, tt.wantType)
			}
		})
	}
}

func TestLokiStackStorageClassName(t *testing.T) {
	tests := []struct {
		name           string
		topology       *pkgcontext.TopologyContext
		wantClass      string
		wantClassFound bool
	}{
		{
			name:           "AWS uses gp3-csi",
			topology:       &pkgcontext.TopologyContext{IsAWS: true, TotalNodeCount: 5},
			wantClass:      "gp3-csi",
			wantClassFound: true,
		},
		{
			name:           "Azure uses managed-csi",
			topology:       &pkgcontext.TopologyContext{IsAzure: true, TotalNodeCount: 5},
			wantClass:      "managed-csi",
			wantClassFound: true,
		},
		{
			name:           "GCP uses standard-csi",
			topology:       &pkgcontext.TopologyContext{IsGCP: true, TotalNodeCount: 5},
			wantClass:      "standard-csi",
			wantClassFound: true,
		},
		{
			name:           "BareMetal uses lvms-vg1",
			topology:       &pkgcontext.TopologyContext{IsBareMetal: true, TotalNodeCount: 5},
			wantClass:      "lvms-vg1",
			wantClassFound: true,
		},
		{
			name:           "vSphere uses thin-csi",
			topology:       &pkgcontext.TopologyContext{IsVSphere: true, TotalNodeCount: 5},
			wantClass:      "thin-csi",
			wantClassFound: true,
		},
		{
			name:           "unknown platform omits storageClassName",
			topology:       &pkgcontext.TopologyContext{TotalNodeCount: 5},
			wantClass:      "",
			wantClassFound: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rendered := renderLoggingAsset(t, "logging-lokistack", tt.topology, nil)

			class, found, err := unstructured.NestedString(rendered.Object, "spec", "storageClassName")
			if err != nil {
				t.Fatalf("Error accessing spec.storageClassName: %v", err)
			}
			if found != tt.wantClassFound {
				t.Errorf("storageClassName present = %v, want %v", found, tt.wantClassFound)
			}
			if found && class != tt.wantClass {
				t.Errorf("storageClassName = %q, want %q", class, tt.wantClass)
			}
		})
	}
}

func TestLokiStackSecretName(t *testing.T) {
	rendered := renderLoggingAsset(t, "logging-lokistack", &pkgcontext.TopologyContext{IsAWS: true, TotalNodeCount: 5}, nil)

	name, found, err := unstructured.NestedString(rendered.Object, "spec", "storage", "secret", "name")
	if err != nil {
		t.Fatalf("Error accessing spec.storage.secret.name: %v", err)
	}
	if !found {
		t.Fatal("spec.storage.secret.name not found")
	}
	if name != "logging-loki-storage" {
		t.Errorf("secret name = %q, want %q", name, "logging-loki-storage")
	}
}

func TestLokiStackRetention(t *testing.T) {
	rendered := renderLoggingAsset(t, "logging-lokistack", &pkgcontext.TopologyContext{IsAWS: true, TotalNodeCount: 5}, nil)

	days, found, err := unstructured.NestedInt64(rendered.Object, "spec", "limits", "global", "retention", "days")
	if err != nil {
		t.Fatalf("Error accessing retention days: %v", err)
	}
	if !found {
		t.Fatal("retention days not found")
	}
	if days != 7 {
		t.Errorf("retention days = %d, want 7", days)
	}
}

func TestClusterLogForwarderWithoutAudit(t *testing.T) {
	rendered := renderLoggingAsset(t, "logging-collector", &pkgcontext.TopologyContext{TotalNodeCount: 5}, nil)

	if rendered.GetKind() != "ClusterLogForwarder" {
		t.Errorf("Kind = %q, want ClusterLogForwarder", rendered.GetKind())
	}

	pipelines, found, err := unstructured.NestedSlice(rendered.Object, "spec", "pipelines")
	if err != nil {
		t.Fatalf("Error accessing pipelines: %v", err)
	}
	if !found {
		t.Fatal("pipelines not found")
	}

	for _, p := range pipelines {
		pipeline := p.(map[string]any)
		if pipeline["name"] == "audit-logs" {
			t.Error("audit-logs pipeline should NOT be present without audit annotation")
		}
	}

	filters, found, err := unstructured.NestedSlice(rendered.Object, "spec", "filters")
	if err != nil {
		t.Fatalf("Error accessing filters: %v", err)
	}
	if !found {
		t.Fatal("filters not found")
	}

	for _, f := range filters {
		filter := f.(map[string]any)
		name := filter["name"].(string)
		if name == "audit-drop-non-complete" || name == "audit-drop-system-users" || name == "audit-drop-noisy-verbs" {
			t.Errorf("audit filter %q should NOT be present without audit annotation", name)
		}
	}
}

func TestClusterLogForwarderWithAudit(t *testing.T) {
	annotations := map[string]string{
		"platform.kubevirt.io/enable-audit-logging": "true",
	}
	rendered := renderLoggingAsset(t, "logging-collector", &pkgcontext.TopologyContext{TotalNodeCount: 5}, annotations)

	pipelines, _, _ := unstructured.NestedSlice(rendered.Object, "spec", "pipelines")

	foundAuditPipeline := false
	for _, p := range pipelines {
		pipeline := p.(map[string]any)
		if pipeline["name"] == "audit-logs" {
			foundAuditPipeline = true
		}
	}
	if !foundAuditPipeline {
		t.Error("audit-logs pipeline should be present with audit annotation")
	}

	filters, _, _ := unstructured.NestedSlice(rendered.Object, "spec", "filters")
	auditFilters := map[string]bool{
		"audit-drop-non-complete": false,
		"audit-drop-system-users": false,
		"audit-drop-noisy-verbs":  false,
	}
	for _, f := range filters {
		filter := f.(map[string]any)
		name := filter["name"].(string)
		if _, ok := auditFilters[name]; ok {
			auditFilters[name] = true
		}
	}
	for name, found := range auditFilters {
		if !found {
			t.Errorf("audit filter %q should be present with audit annotation", name)
		}
	}
}

func TestClusterLogForwarderBasePipelines(t *testing.T) {
	rendered := renderLoggingAsset(t, "logging-collector", &pkgcontext.TopologyContext{TotalNodeCount: 5}, nil)

	pipelines, _, _ := unstructured.NestedSlice(rendered.Object, "spec", "pipelines")

	expectedPipelines := map[string]bool{"infra-logs": false, "app-logs": false}
	for _, p := range pipelines {
		pipeline := p.(map[string]any)
		name := pipeline["name"].(string)
		if _, ok := expectedPipelines[name]; ok {
			expectedPipelines[name] = true
		}
	}
	for name, found := range expectedPipelines {
		if !found {
			t.Errorf("pipeline %q should always be present", name)
		}
	}
}
