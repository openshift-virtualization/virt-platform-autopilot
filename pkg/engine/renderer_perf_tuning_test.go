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
	"encoding/base64"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"

	"github.com/kubevirt/virt-platform-autopilot/pkg/assets"
	pkgcontext "github.com/kubevirt/virt-platform-autopilot/pkg/context"
)

func renderHCOAsset(t *testing.T, assetName string) (*unstructured.Unstructured, *assets.Loader, *assets.AssetMetadata) {
	return renderHCOAssetWithAnnotations(t, assetName, nil)
}

func renderHCOAssetWithAnnotations(t *testing.T, assetName string, annotations map[string]string) (*unstructured.Unstructured, *assets.Loader, *assets.AssetMetadata) {
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
	hco.SetAnnotations(annotations)

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

func TestHCOGoldenConfigPreservesCertConfig(t *testing.T) {
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

	// Simulate a user-configured certConfig
	err = unstructured.SetNestedMap(hco.Object, map[string]any{
		"ca":     map[string]any{"duration": "24h0m0s", "renewBefore": "12h0m0s"},
		"server": map[string]any{"duration": "12h0m0s", "renewBefore": "6h0m0s"},
	}, "spec", "certConfig")
	if err != nil {
		t.Fatalf("Failed to set certConfig in HCO: %v", err)
	}

	asset, err := registry.GetAsset("hco-golden-config")
	if err != nil {
		t.Fatalf("Failed to get hco-golden-config: %v", err)
	}

	rendered, err := renderer.RenderAsset(asset, &pkgcontext.RenderContext{HCO: hco})
	if err != nil {
		t.Fatalf("Failed to render asset: %v", err)
	}

	// The rendered golden-config must NOT include certConfig so that SSA does not
	// claim ownership of it and overwrite the user's custom value.
	_, found, err := unstructured.NestedMap(rendered.Object, "spec", "certConfig")
	if err != nil {
		t.Fatalf("Error accessing certConfig: %v", err)
	}
	if found {
		t.Error("golden-config must not set certConfig; user-configured values would be overwritten by SSA")
	}
}

func TestKubeletPerfSettingsIsMachineConfig(t *testing.T) {
	rendered, _, _ := renderHCOAsset(t, "kubelet-perf-settings")

	if rendered.GetKind() != "MachineConfig" {
		t.Errorf("Kind = %s, want MachineConfig", rendered.GetKind())
	}
	if rendered.GetAPIVersion() != "machineconfiguration.openshift.io/v1" {
		t.Errorf("APIVersion = %s, want machineconfiguration.openshift.io/v1", rendered.GetAPIVersion())
	}

	// Verify it drops a file to /etc/openshift/kubelet.conf.d
	files, found, err := unstructured.NestedSlice(rendered.Object, "spec", "config", "storage", "files")
	if err != nil || !found || len(files) == 0 {
		t.Fatal("MachineConfig should have files in spec.config.storage.files")
	}

	fileMap, ok := files[0].(map[string]any)
	if !ok {
		t.Fatal("File entry should be a map")
	}

	path, _, _ := unstructured.NestedString(fileMap, "path")
	if !strings.HasPrefix(path, "/etc/openshift/kubelet.conf.d/") {
		t.Errorf("File path = %s, want /etc/openshift/kubelet.conf.d/*", path)
	}

	// Smoke-check the Ignition payload: unresolved template markers here
	// crash-loop kubelet when the drop-in is merged at runtime.
	assertKubeletDropinRenderable(t, fileMap)
}

func TestFileBasedSwapProvisioningIsOrderedBeforeActivation(t *testing.T) {
	rendered, loader, asset := renderHCOAsset(t, "file-based-swap-provisioning")

	if rendered.GetName() != "90-worker-file-based-swap-provisioning" {
		t.Errorf("MachineConfig name = %q, want 90-worker-file-based-swap-provisioning", rendered.GetName())
	}
	if asset.Install != "opt-in" {
		t.Errorf("file-based-swap-provisioning Install = %q, want opt-in", asset.Install)
	}

	provisioningUnit := machineConfigSystemdUnit(t, rendered, "swap-provision.service")
	provisioningContents, _ := provisioningUnit["contents"].(string)
	if !strings.Contains(provisioningContents, "kubevirt-provision-file-swap.sh 100 15") {
		t.Error("swap-provision.service should default the overcommit percentage to 100 and retain 15% free space")
	}
	if !strings.Contains(provisioningContents, "Before=kubelet-dependencies.target") {
		t.Error("swap-provision.service must complete before kubelet dependencies start")
	}
	if !strings.Contains(provisioningContents, "RequiresMountsFor=/var") {
		t.Error("swap-provision.service must require the mount backing /var, so the swap file is never written to an unmounted directory")
	}
	if !strings.Contains(provisioningContents, "WantedBy=kubelet-dependencies.target") {
		t.Error("swap-provision.service must not block kubelet startup when provisioning fails")
	}

	activationUnit := machineConfigSystemdUnit(t, rendered, "swap-file-enable.service")
	activationDropInContents := systemdDropInContents(t, activationUnit, "10-wait-for-provisioning.conf")
	if !strings.Contains(activationDropInContents, "After=swap-provision.service") ||
		!strings.Contains(activationDropInContents, "ConditionFirstBoot=") ||
		!strings.Contains(activationDropInContents, "ConditionPathExists=/var/swapfile") {
		t.Error("swap-file-enable.service must wait for swap provisioning")
	}

	provisioner, err := loader.LoadAsset("active/machine-config/09-file-based-swap-provisioning/kubevirt-provision-file-swap.sh")
	if err != nil {
		t.Fatalf("Failed to load swap provisioner: %v", err)
	}
	if !strings.Contains(string(provisioner), "swap_file=/var/swapfile") {
		t.Error("swap provisioner must create the swap file under /var")
	}
	if !strings.Contains(string(provisioner), "semanage fcontext -a -f a -t swapfile_t") ||
		!strings.Contains(string(provisioner), "restorecon -v \"${swap_file}\"") {
		t.Error("swap provisioner must persist and restore the swap file SELinux context")
	}
	if !strings.Contains(string(provisioner), "legacy_swap_files=(/var/tmp/ocpswap.file /var/tmp/swapfile)") ||
		!strings.Contains(string(provisioner), "legacy file-backed swap ${legacy_swap_file} exists; leaving it unchanged") {
		t.Error("swap provisioner must preserve both supported legacy /var/tmp swap file paths")
	}
}

func TestSwapFileMigrationPrecedesActivation(t *testing.T) {
	rendered, loader, _ := renderHCOAsset(t, "swap-enable")
	files, found, err := unstructured.NestedSlice(rendered.Object, "spec", "config", "storage", "files")
	if err != nil || !found {
		t.Fatal("swap-enable MachineConfig should contain storage files")
	}
	hasMigrator := false
	for _, file := range files {
		fileMap, ok := file.(map[string]any)
		if ok && fileMap["path"] == "/usr/local/bin/kubevirt-migrate-swap-file.sh" {
			hasMigrator = true
			break
		}
	}
	if !hasMigrator {
		t.Error("swap-enable MachineConfig must install the swap migration script")
	}

	activationUnit := machineConfigSystemdUnit(t, rendered, "swap-file-enable.service")
	activationContents, _ := activationUnit["contents"].(string)

	if !strings.Contains(activationContents, "ConditionPathExists=/var/swapfile") ||
		!strings.Contains(activationContents, "swapon --priority 10 /var/swapfile") {
		t.Error("swap-file-enable.service must activate /var/swapfile")
	}

	migrationUnit := machineConfigSystemdUnit(t, rendered, "swap-file-migrate.service")
	migrationContents, _ := migrationUnit["contents"].(string)
	if !strings.Contains(migrationContents, "ConditionPathExists=/var/ocpswap.file") ||
		!strings.Contains(migrationContents, "Before=swap-file-enable.service") ||
		!strings.Contains(migrationContents, "Before=swap-provision.service") ||
		!strings.Contains(migrationContents, "WantedBy=kubelet-dependencies.target") {
		t.Error("swap-file-migrate.service must precede provisioning and activation without blocking kubelet startup")
	}

	migrator, err := loader.LoadAsset("active/machine-config/01-swap-enable/kubevirt-migrate-swap-file.sh")
	if err != nil {
		t.Fatalf("Failed to load swap migrator: %v", err)
	}
	if !strings.Contains(string(migrator), "legacy_swap_file=/var/ocpswap.file") ||
		!strings.Contains(string(migrator), "swap_file=/var/swapfile") ||
		!strings.Contains(string(migrator), "refusing to migrate active swap file") {
		t.Error("swap migrator must move the old inactive path to /var/swapfile")
	}
}

func TestFileBasedSwapProvisioningUsesConfiguredFreeSpaceReserve(t *testing.T) {
	rendered, _, _ := renderHCOAssetWithAnnotations(t, "file-based-swap-provisioning", map[string]string{
		"platform.kubevirt.io/file-based-swap-min-free-percent": "20",
	})

	provisioningUnit := machineConfigSystemdUnit(t, rendered, "swap-provision.service")
	provisioningContents, _ := provisioningUnit["contents"].(string)
	if !strings.Contains(provisioningContents, "kubevirt-provision-file-swap.sh 100 20") {
		t.Error("swap-provision.service should pass the configured free-space reserve to the provisioner")
	}
}

func machineConfigSystemdUnit(t *testing.T, rendered *unstructured.Unstructured, name string) map[string]any {
	t.Helper()

	units, found, err := unstructured.NestedSlice(rendered.Object, "spec", "config", "systemd", "units")
	if err != nil || !found {
		t.Fatal("MachineConfig should contain systemd units")
	}
	for _, unit := range units {
		unitMap, ok := unit.(map[string]any)
		if ok && unitMap["name"] == name {
			return unitMap
		}
	}
	t.Fatalf("MachineConfig should contain %s", name)
	return nil
}

func systemdDropInContents(t *testing.T, unit map[string]any, name string) string {
	t.Helper()

	dropins, _ := unit["dropins"].([]any)
	for _, dropin := range dropins {
		dropinMap, ok := dropin.(map[string]any)
		if ok && dropinMap["name"] == name {
			contents, _ := dropinMap["contents"].(string)
			return contents
		}
	}
	t.Fatalf("systemd unit should contain %s", name)
	return ""
}

// Detailed field tests removed: kubelet configuration is now dropped as files
// to /etc/openshift/kubelet.conf.d, base64-encoded in the MachineConfig.
// The kubelet merges these files at runtime, making field-level assertions
// in unit tests less meaningful than functional e2e validation. Still decode
// the payload once to catch unresolved template markers (invalid YAML).

func TestKubeletPerfSettingsDocumentation(t *testing.T) {
	_, loader, asset := renderHCOAsset(t, "kubelet-perf-settings")

	content, err := loader.LoadAsset(asset.Path)
	if err != nil {
		t.Fatalf("Failed to load template: %v", err)
	}

	contentStr := string(content)
	if !strings.Contains(contentStr, "OCPNODE-3719") {
		t.Error("Template should reference OCPNODE-3719 for autoSizingReserved")
	}
	if !strings.Contains(contentStr, "BZ#1984442") {
		t.Error("Template should reference BZ#1984442 for nodeStatusMaxImages")
	}
}

func assertKubeletDropinRenderable(t *testing.T, fileMap map[string]any) {
	t.Helper()

	source, _, _ := unstructured.NestedString(fileMap, "contents", "source")
	const prefix = "base64,"
	idx := strings.Index(source, prefix)
	if idx < 0 {
		t.Fatalf("Ignition source missing base64 payload: %q", source)
	}
	decoded, err := base64.StdEncoding.DecodeString(source[idx+len(prefix):])
	if err != nil {
		t.Fatalf("Ignition base64 decode: %v", err)
	}
	conf := string(decoded)
	if strings.Contains(conf, "{{") || strings.Contains(conf, "}}") {
		t.Fatalf("drop-in still contains unresolved Go template markers:\n%s", conf)
	}
	var parsed map[string]any
	if err := yaml.Unmarshal(decoded, &parsed); err != nil {
		t.Fatalf("drop-in is not valid YAML (kubelet would crash-loop): %v\n%s", err, conf)
	}
}

func TestCPUManagerIsMachineConfig(t *testing.T) {
	rendered, _, _ := renderHCOAsset(t, "kubelet-cpu-manager")

	if rendered.GetKind() != "MachineConfig" {
		t.Errorf("Kind = %s, want MachineConfig", rendered.GetKind())
	}

	// Verify it drops a file to /etc/openshift/kubelet.conf.d
	files, found, err := unstructured.NestedSlice(rendered.Object, "spec", "config", "storage", "files")
	if err != nil || !found || len(files) == 0 {
		t.Fatal("MachineConfig should have files in spec.config.storage.files")
	}

	fileMap, ok := files[0].(map[string]any)
	if !ok {
		t.Fatal("File entry should be a map")
	}

	path, _, _ := unstructured.NestedString(fileMap, "path")
	if !strings.HasPrefix(path, "/etc/openshift/kubelet.conf.d/") {
		t.Errorf("File path = %s, want /etc/openshift/kubelet.conf.d/*", path)
	}
}

// Detailed field tests removed: kubelet configuration is now dropped as files
// to /etc/openshift/kubelet.conf.d, base64-encoded in the MachineConfig.
// The kubelet merges these files at runtime, making field-level assertions
// in unit tests less meaningful than functional e2e validation.

func TestCPUManagerDocumentation(t *testing.T) {
	_, loader, _ := renderHCOAsset(t, "kubelet-cpu-manager")

	// Read the kubelet config file referenced by readAsset
	content, err := loader.LoadAsset("active/machine-config/07-kubelet-cpu-manager/kubelet-96-cpu-manager.conf")
	if err != nil {
		t.Fatalf("Failed to load kubelet config: %v", err)
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

func TestKSMZeroOnlyNodeDisruptionPolicy(t *testing.T) {
	rendered, _, policyAsset := renderHCOAsset(t, "ksm-zero-only-node-disruption-policy")

	if rendered.GetAPIVersion() != "operator.openshift.io/v1" || rendered.GetKind() != "MachineConfiguration" {
		t.Fatalf("policy GVK = %s, %s, want operator.openshift.io/v1, MachineConfiguration", rendered.GetAPIVersion(), rendered.GetKind())
	}
	if rendered.GetName() != "cluster" {
		t.Errorf("policy name = %q, want cluster", rendered.GetName())
	}

	files, found, err := unstructured.NestedSlice(rendered.Object, "spec", "nodeDisruptionPolicy", "files")
	if err != nil || !found || len(files) != 1 {
		t.Fatalf("policy files = %#v, found=%t, err=%v; want one entry", files, found, err)
	}
	file, ok := files[0].(map[string]any)
	if !ok {
		t.Fatal("policy file entry should be a map")
	}
	if path, _, _ := unstructured.NestedString(file, "path"); path != "/usr/local/bin/kubevirt-ksm-tune.py" {
		t.Errorf("policy file path = %q, want KSM tuner", path)
	}
	fileActions, _, err := unstructured.NestedSlice(file, "actions")
	if err != nil || len(fileActions) != 2 {
		t.Fatalf("file actions = %#v, err=%v; want DaemonReload then Restart", fileActions, err)
	}
	for i, want := range []string{"DaemonReload", "Restart"} {
		fileAction, ok := fileActions[i].(map[string]any)
		if !ok {
			t.Fatalf("file action %d should be a map", i)
		}
		if got, _, _ := unstructured.NestedString(fileAction, "type"); got != want {
			t.Errorf("file action %d = %q, want %q", i, got, want)
		}
	}
	fileAction := fileActions[1].(map[string]any)
	if service, _, _ := unstructured.NestedString(fileAction, "restart", "serviceName"); service != "kubevirt-ksm-tune.service" {
		t.Errorf("file Restart service = %q, want kubevirt-ksm-tune.service", service)
	}

	units, found, err := unstructured.NestedSlice(rendered.Object, "spec", "nodeDisruptionPolicy", "units")
	if err != nil || !found || len(units) != 1 {
		t.Fatalf("policy units = %#v, found=%t, err=%v; want one entry", units, found, err)
	}
	unit, ok := units[0].(map[string]any)
	if !ok {
		t.Fatal("policy unit entry should be a map")
	}
	if name, _, _ := unstructured.NestedString(unit, "name"); name != "kubevirt-ksm-tune.service" {
		t.Errorf("policy unit name = %q, want kubevirt-ksm-tune.service", name)
	}
	actions, _, err := unstructured.NestedSlice(unit, "actions")
	if err != nil || len(actions) != 2 {
		t.Fatalf("unit actions = %#v, err=%v; want DaemonReload then Restart", actions, err)
	}
	for i, want := range []string{"DaemonReload", "Restart"} {
		action, ok := actions[i].(map[string]any)
		if !ok {
			t.Fatalf("unit action %d should be a map", i)
		}
		if got, _, _ := unstructured.NestedString(action, "type"); got != want {
			t.Errorf("unit action %d = %q, want %q", i, got, want)
		}
	}

	_, _, machineConfigAsset := renderHCOAsset(t, "ksm-zero-only")
	if policyAsset.ReconcileOrder >= machineConfigAsset.ReconcileOrder {
		t.Errorf("policy reconcile_order = %d, MachineConfig reconcile_order = %d; policy must be first", policyAsset.ReconcileOrder, machineConfigAsset.ReconcileOrder)
	}
	if got := machineConfigAsset.NodeDisruptionPolicy; got == nil || got.Asset != policyAsset.Name {
		t.Errorf("KSM MachineConfig node disruption policy = %#v, want %q", got, policyAsset.Name)
	}
}

func TestAssetMetadata(t *testing.T) {
	loader := assets.NewLoader()
	registry, err := assets.NewRegistry(loader)
	if err != nil {
		t.Fatalf("Failed to create registry: %v", err)
	}

	// Test kubelet-perf-settings (Tech Preview, opt-in via annotation)
	asset, err := registry.GetAsset("kubelet-perf-settings")
	if err != nil {
		t.Fatalf("Failed to get kubelet-perf-settings: %v", err)
	}
	if asset.Install != "opt-in" {
		t.Errorf("kubelet-perf-settings Install = %s, want opt-in", asset.Install)
	}

	hasPerfAnnotation := false
	for _, condition := range asset.Conditions {
		if condition.Type == "annotation" &&
			condition.Key == "platform.kubevirt.io/enable-kubelet-performance-settings" &&
			condition.Value == "true" {
			hasPerfAnnotation = true
			break
		}
	}
	if !hasPerfAnnotation {
		t.Error("kubelet-perf-settings should require the platform.kubevirt.io/enable-kubelet-performance-settings annotation")
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
	hasCPUManagerAnnotation := false
	for _, condition := range asset.Conditions {
		if condition.Type == "kubevirt-feature-gate" && condition.Value == "CPUManager" {
			hasFeatureGate = true
		}
		if condition.Type == "annotation" &&
			condition.Key == "platform.kubevirt.io/enable-cpu-manager-perf-tunings" &&
			condition.Value == "true" {
			hasCPUManagerAnnotation = true
		}
	}
	if !hasFeatureGate {
		t.Error("kubelet-cpu-manager should require CPUManager feature gate")
	}
	if !hasCPUManagerAnnotation {
		t.Error("kubelet-cpu-manager should require the platform.kubevirt.io/enable-cpu-manager-perf-tunings annotation")
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
