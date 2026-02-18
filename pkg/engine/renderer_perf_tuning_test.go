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
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/kubevirt/virt-platform-autopilot/pkg/assets"
	pkgcontext "github.com/kubevirt/virt-platform-autopilot/pkg/context"
)

func renderHCOAsset(t *testing.T, assetName string) (*unstructured.Unstructured, *assets.Loader, *assets.AssetMetadata) {
	t.Helper()

	loader := assets.NewLoader()
	registry, err := assets.NewRegistry(loader)
	if err != nil {
		t.Fatalf("Failed to create registry: %v", err)
	}

	renderer := NewRenderer(loader)

	hco := &unstructured.Unstructured{}
	hco.SetAPIVersion("hco.kubevirt.io/v1beta1")
	hco.SetKind("HyperConverged")
	hco.SetName("kubevirt-hyperconverged")
	hco.SetNamespace("openshift-cnv")

	renderCtx := &pkgcontext.RenderContext{HCO: hco}

	asset, err := registry.GetAsset(assetName)
	if err != nil {
		t.Fatalf("Failed to get asset: %v", err)
	}

	rendered, err := renderer.RenderAsset(asset, renderCtx)
	if err != nil {
		t.Fatalf("Failed to render asset: %v", err)
	}

	if rendered == nil {
		t.Fatal("Rendered asset is nil")
	}

	return rendered, loader, asset
}

func TestHCOGoldenConfigHighBurst(t *testing.T) {
	rendered, _, _ := renderHCOAsset(t, "hco-golden-config")

	tuningPolicy, found, err := unstructured.NestedString(rendered.Object, "spec", "tuningPolicy")
	if err != nil {
		t.Fatalf("Error accessing tuningPolicy: %v", err)
	}
	if !found {
		t.Error("tuningPolicy should be present in spec")
	}
	if tuningPolicy != "highBurst" {
		t.Errorf("tuningPolicy = %s, want highBurst", tuningPolicy)
	}
}

func TestHCOGoldenConfigCPUAllocationRatio(t *testing.T) {
	rendered, _, _ := renderHCOAsset(t, "hco-golden-config")

	ratio, found, err := unstructured.NestedInt64(rendered.Object, "spec", "resourceRequirements", "vmiCPUAllocationRatio")
	if err != nil {
		t.Fatalf("Error accessing vmiCPUAllocationRatio: %v", err)
	}
	if !found {
		t.Error("vmiCPUAllocationRatio should be present")
	}
	if ratio != 10 {
		t.Errorf("vmiCPUAllocationRatio = %d, want 10", ratio)
	}
}

func TestHCOGoldenConfigLiveMigration(t *testing.T) {
	rendered, _, _ := renderHCOAsset(t, "hco-golden-config")

	liveMigration, found, err := unstructured.NestedMap(rendered.Object, "spec", "liveMigrationConfig")
	if err != nil {
		t.Fatalf("Error accessing liveMigrationConfig: %v", err)
	}
	if !found {
		t.Error("liveMigrationConfig should be present")
	}
	if len(liveMigration) == 0 {
		t.Error("liveMigrationConfig should not be empty")
	}

	if allowAutoConverge, ok := liveMigration["allowAutoConverge"].(bool); !ok || allowAutoConverge {
		t.Error("allowAutoConverge should be false")
	}
	if allowPostCopy, ok := liveMigration["allowPostCopy"].(bool); !ok || allowPostCopy {
		t.Error("allowPostCopy should be false")
	}
	if parallel, ok := liveMigration["parallelMigrationsPerCluster"].(int64); !ok || parallel != 5 {
		t.Errorf("parallelMigrationsPerCluster = %v, want 5", liveMigration["parallelMigrationsPerCluster"])
	}
}

func TestHCOGoldenConfigDocumentation(t *testing.T) {
	_, loader, asset := renderHCOAsset(t, "hco-golden-config")

	content, err := loader.LoadAsset(asset.Path)
	if err != nil {
		t.Fatalf("Failed to load template: %v", err)
	}

	contentStr := string(content)
	if !strings.Contains(contentStr, "CNV-69442") {
		t.Error("Template should reference CNV-69442 for highBurst")
	}
	if !strings.Contains(contentStr, "VM-level performance") {
		t.Error("Template should note VM-level settings")
	}
}

func TestKubeletPerfSettingsIsKubeletConfig(t *testing.T) {
	rendered, _, _ := renderHCOAsset(t, "kubelet-perf-settings")

	if rendered.GetKind() != "KubeletConfig" {
		t.Errorf("Kind = %s, want KubeletConfig", rendered.GetKind())
	}
	if rendered.GetAPIVersion() != "machineconfiguration.openshift.io/v1" {
		t.Errorf("APIVersion = %s, want machineconfiguration.openshift.io/v1", rendered.GetAPIVersion())
	}
}

func TestKubeletPerfSettingsNodeStatusMaxImages(t *testing.T) {
	rendered, _, _ := renderHCOAsset(t, "kubelet-perf-settings")

	maxImages, found, err := unstructured.NestedInt64(rendered.Object, "spec", "kubeletConfig", "nodeStatusMaxImages")
	if err != nil {
		t.Fatalf("Error accessing nodeStatusMaxImages: %v", err)
	}
	if !found {
		t.Error("nodeStatusMaxImages should be present")
	}
	if maxImages != -1 {
		t.Errorf("nodeStatusMaxImages = %d, want -1", maxImages)
	}
}

func TestKubeletPerfSettingsAutoSizing(t *testing.T) {
	rendered, _, _ := renderHCOAsset(t, "kubelet-perf-settings")

	autoSizing, found, err := unstructured.NestedBool(rendered.Object, "spec", "kubeletConfig", "autoSizingReserved")
	if err != nil {
		t.Fatalf("Error accessing autoSizingReserved: %v", err)
	}
	if !found {
		t.Error("autoSizingReserved should be present")
	}
	if !autoSizing {
		t.Error("autoSizingReserved should be true")
	}
}

func TestKubeletPerfSettingsFailSwapOn(t *testing.T) {
	rendered, _, _ := renderHCOAsset(t, "kubelet-perf-settings")

	failSwap, found, err := unstructured.NestedBool(rendered.Object, "spec", "kubeletConfig", "failSwapOn")
	if err != nil {
		t.Fatalf("Error accessing failSwapOn: %v", err)
	}
	if !found {
		t.Error("failSwapOn should be present")
	}
	if failSwap {
		t.Error("failSwapOn should be false to allow future swap enablement")
	}
}

func TestKubeletPerfSettingsMaxPodsDefault(t *testing.T) {
	rendered, _, _ := renderHCOAsset(t, "kubelet-perf-settings")

	maxPods, found, err := unstructured.NestedInt64(rendered.Object, "spec", "kubeletConfig", "maxPods")
	if err != nil {
		t.Fatalf("Error accessing maxPods: %v", err)
	}
	if !found {
		t.Error("maxPods should be present")
	}
	if maxPods != 500 {
		t.Errorf("maxPods = %d, want 500 (default)", maxPods)
	}
}

func TestKubeletPerfSettingsMaxPodsCustom(t *testing.T) {
	loader := assets.NewLoader()
	registry, err := assets.NewRegistry(loader)
	if err != nil {
		t.Fatalf("Failed to create registry: %v", err)
	}

	renderer := NewRenderer(loader)

	hco := &unstructured.Unstructured{}
	hco.SetAPIVersion("hco.kubevirt.io/v1beta1")
	hco.SetKind("HyperConverged")
	hco.SetName("kubevirt-hyperconverged")
	hco.SetNamespace("openshift-cnv")

	// Set custom maxPods
	err = unstructured.SetNestedField(hco.Object, int64(250), "spec", "infra", "nodePlacement", "maxPods")
	if err != nil {
		t.Fatalf("Failed to set maxPods in HCO: %v", err)
	}

	asset, err := registry.GetAsset("kubelet-perf-settings")
	if err != nil {
		t.Fatalf("Failed to get asset: %v", err)
	}

	rendered, err := renderer.RenderAsset(asset, &pkgcontext.RenderContext{HCO: hco})
	if err != nil {
		t.Fatalf("Failed to render asset: %v", err)
	}

	maxPods, found, err := unstructured.NestedInt64(rendered.Object, "spec", "kubeletConfig", "maxPods")
	if err != nil {
		t.Fatalf("Error accessing maxPods: %v", err)
	}
	if !found {
		t.Error("maxPods should be present")
	}
	if maxPods != 250 {
		t.Errorf("maxPods = %d, want 250 (from HCO)", maxPods)
	}
}

func TestKubeletPerfSettingsDocumentation(t *testing.T) {
	_, loader, asset := renderHCOAsset(t, "kubelet-perf-settings")

	content, err := loader.LoadAsset(asset.Path)
	if err != nil {
		t.Fatalf("Failed to load template: %v", err)
	}

	contentStr := string(content)
	if !strings.Contains(contentStr, "RFE-8045") {
		t.Error("Template should reference RFE-8045 for autoSizingReserved")
	}
	if !strings.Contains(contentStr, "BZ#1984442") {
		t.Error("Template should reference BZ#1984442 for nodeStatusMaxImages")
	}
}

func TestCPUManagerIsKubeletConfig(t *testing.T) {
	rendered, _, _ := renderHCOAsset(t, "kubelet-cpu-manager")

	if rendered.GetKind() != "KubeletConfig" {
		t.Errorf("Kind = %s, want KubeletConfig", rendered.GetKind())
	}
}

func TestCPUManagerPolicyStatic(t *testing.T) {
	rendered, _, _ := renderHCOAsset(t, "kubelet-cpu-manager")

	cpuPolicy, found, err := unstructured.NestedString(rendered.Object, "spec", "kubeletConfig", "cpuManagerPolicy")
	if err != nil {
		t.Fatalf("Error accessing cpuManagerPolicy: %v", err)
	}
	if !found {
		t.Error("cpuManagerPolicy should be present")
	}
	if cpuPolicy != "static" {
		t.Errorf("cpuManagerPolicy = %s, want static", cpuPolicy)
	}
}

func TestCPUManagerTopologyPolicy(t *testing.T) {
	rendered, _, _ := renderHCOAsset(t, "kubelet-cpu-manager")

	topoPolicy, found, err := unstructured.NestedString(rendered.Object, "spec", "kubeletConfig", "topologyManagerPolicy")
	if err != nil {
		t.Fatalf("Error accessing topologyManagerPolicy: %v", err)
	}
	if !found {
		t.Error("topologyManagerPolicy should be present for VM pinning")
	}
	if topoPolicy != "best-effort" {
		t.Errorf("topologyManagerPolicy = %s, want best-effort", topoPolicy)
	}
}

func TestCPUManagerMemoryPolicy(t *testing.T) {
	rendered, _, _ := renderHCOAsset(t, "kubelet-cpu-manager")

	memPolicy, found, err := unstructured.NestedString(rendered.Object, "spec", "kubeletConfig", "memoryManagerPolicy")
	if err != nil {
		t.Fatalf("Error accessing memoryManagerPolicy: %v", err)
	}
	if !found {
		t.Error("memoryManagerPolicy should be present for VM pinning")
	}
	if memPolicy != "Static" {
		t.Errorf("memoryManagerPolicy = %s, want Static", memPolicy)
	}
}

func TestCPUManagerReservedMemory(t *testing.T) {
	rendered, _, _ := renderHCOAsset(t, "kubelet-cpu-manager")

	reservedMem, found, err := unstructured.NestedSlice(rendered.Object, "spec", "kubeletConfig", "reservedMemory")
	if err != nil {
		t.Fatalf("Error accessing reservedMemory: %v", err)
	}
	if !found {
		t.Error("reservedMemory should be present")
	}
	if len(reservedMem) != 1 {
		t.Errorf("reservedMemory length = %d, want 1", len(reservedMem))
	}

	node0, ok := reservedMem[0].(map[string]interface{})
	if !ok {
		t.Fatal("reservedMemory[0] is not a map")
	}
	if numa, ok := node0["numaNode"].(int64); !ok || numa != 0 {
		t.Errorf("numaNode = %v, want 0", node0["numaNode"])
	}
	limits, ok := node0["limits"].(map[string]interface{})
	if !ok {
		t.Fatal("limits is not a map")
	}
	if memory, ok := limits["memory"].(string); !ok || memory != "1124Mi" {
		t.Errorf("memory = %v, want 1124Mi", limits["memory"])
	}
}

func TestCPUManagerPolicyOptions(t *testing.T) {
	rendered, _, _ := renderHCOAsset(t, "kubelet-cpu-manager")

	policyOpts, found, err := unstructured.NestedMap(rendered.Object, "spec", "kubeletConfig", "cpuManagerPolicyOptions")
	if err != nil {
		t.Fatalf("Error accessing cpuManagerPolicyOptions: %v", err)
	}
	if !found {
		t.Error("cpuManagerPolicyOptions should be present")
	}
	if fullPCPUs, ok := policyOpts["full-pcpus-only"].(string); !ok || fullPCPUs != "true" {
		t.Errorf("full-pcpus-only = %v, want true", policyOpts["full-pcpus-only"])
	}
}

func TestCPUManagerReconcilePeriod(t *testing.T) {
	rendered, _, _ := renderHCOAsset(t, "kubelet-cpu-manager")

	reconcilePeriod, found, err := unstructured.NestedString(rendered.Object, "spec", "kubeletConfig", "cpuManagerReconcilePeriod")
	if err != nil {
		t.Fatalf("Error accessing cpuManagerReconcilePeriod: %v", err)
	}
	if !found {
		t.Error("cpuManagerReconcilePeriod should be present")
	}
	if reconcilePeriod != "5s" {
		t.Errorf("cpuManagerReconcilePeriod = %s, want 5s", reconcilePeriod)
	}
}

func TestCPUManagerReservedCPUs(t *testing.T) {
	rendered, _, _ := renderHCOAsset(t, "kubelet-cpu-manager")

	reservedCPUs, found, err := unstructured.NestedString(rendered.Object, "spec", "kubeletConfig", "reservedSystemCPUs")
	if err != nil {
		t.Fatalf("Error accessing reservedSystemCPUs: %v", err)
	}
	if !found {
		t.Error("reservedSystemCPUs should be present")
	}
	if reservedCPUs != "0-1" {
		t.Errorf("reservedSystemCPUs = %s, want 0-1", reservedCPUs)
	}
}

func TestCPUManagerDocumentation(t *testing.T) {
	_, loader, asset := renderHCOAsset(t, "kubelet-cpu-manager")

	content, err := loader.LoadAsset(asset.Path)
	if err != nil {
		t.Fatalf("Failed to load template: %v", err)
	}

	contentStr := string(content)
	if !strings.Contains(contentStr, "NUMA") && !strings.Contains(contentStr, "pinning") {
		t.Error("Template should mention NUMA or pinning")
	}
	if !strings.Contains(contentStr, "Topology Manager") {
		t.Error("Template should explain topology manager")
	}
	if !strings.Contains(contentStr, "Memory Manager") {
		t.Error("Template should explain memory manager")
	}
}

func TestAssetMetadata(t *testing.T) {
	loader := assets.NewLoader()
	registry, err := assets.NewRegistry(loader)
	if err != nil {
		t.Fatalf("Failed to create registry: %v", err)
	}

	// Test kubelet-perf-settings
	asset, err := registry.GetAsset("kubelet-perf-settings")
	if err != nil {
		t.Fatalf("Failed to get kubelet-perf-settings: %v", err)
	}
	if asset.Install != "opt-in" {
		t.Errorf("kubelet-perf-settings Install = %s, want opt-in", asset.Install)
	}

	// Test kubelet-cpu-manager
	asset, err = registry.GetAsset("kubelet-cpu-manager")
	if err != nil {
		t.Fatalf("Failed to get kubelet-cpu-manager: %v", err)
	}
	if asset.Install != "opt-in" {
		t.Errorf("kubelet-cpu-manager Install = %s, want opt-in", asset.Install)
	}

	hasFeatureGate := false
	for _, condition := range asset.Conditions {
		if condition.Type == "feature-gate" && condition.Value == "CPUManager" {
			hasFeatureGate = true
			break
		}
	}
	if !hasFeatureGate {
		t.Error("kubelet-cpu-manager should require CPUManager feature gate")
	}

	// Test hco-golden-config
	asset, err = registry.GetAsset("hco-golden-config")
	if err != nil {
		t.Fatalf("Failed to get hco-golden-config: %v", err)
	}
	if asset.ReconcileOrder != 0 {
		t.Errorf("hco-golden-config ReconcileOrder = %d, want 0", asset.ReconcileOrder)
	}
}
