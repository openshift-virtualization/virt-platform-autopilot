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
	"context"
	"fmt"
	"reflect"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kubevirt/virt-platform-autopilot/pkg/assets"
	pkgcontext "github.com/kubevirt/virt-platform-autopilot/pkg/context"
)

const (
	machineConfigurationAPIVersion = "operator.openshift.io/v1"
	machineConfigurationKind       = "MachineConfiguration"
	machineConfigurationName       = "cluster"
)

// prepareNodeDisruptionPolicyDependentMachineConfig waits for the policy
// dependency and, when effective, marks the MachineConfig as safe to bypass
// rollout coalescing.
func (p *Patcher) prepareNodeDisruptionPolicyDependentMachineConfig(ctx context.Context, desired *unstructured.Unstructured, policyAsset *assets.AssetMetadata, renderCtx *pkgcontext.RenderContext) (bool, error) {
	if !isMachineConfig(desired) {
		return false, fmt.Errorf("node disruption policy dependency did not render a MachineConfig")
	}
	want, err := p.renderNodeDisruptionPolicy(policyAsset, renderCtx)
	if err != nil {
		return false, err
	}
	if err := nodeDisruptionPolicyCoversMachineConfig(want, desired); err != nil {
		return false, err
	}
	ready, err := p.nodeDisruptionPolicyEffective(ctx, want)
	if err != nil || !ready {
		return ready, err
	}
	annotations := desired.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}
	annotations[MachineConfigCoalescingBypassAnnotation] = "true"
	desired.SetAnnotations(annotations)
	return true, nil
}

// nodeDisruptionPolicyReady returns true only after MCO has published the
// exact file, unit, and SSH-key actions declared by policyAsset. This makes an
// opt-in MachineConfig safe to apply without rollout coalescing; it does not
// attempt to judge whether a feature's actions themselves are safe.
func (p *Patcher) nodeDisruptionPolicyReady(ctx context.Context, policyAsset *assets.AssetMetadata, renderCtx *pkgcontext.RenderContext) (bool, error) {
	want, err := p.renderNodeDisruptionPolicy(policyAsset, renderCtx)
	if err != nil {
		return false, err
	}
	return p.nodeDisruptionPolicyEffective(ctx, want)
}

func (p *Patcher) renderNodeDisruptionPolicy(policyAsset *assets.AssetMetadata, renderCtx *pkgcontext.RenderContext) (map[string]any, error) {
	if policyAsset == nil {
		return nil, fmt.Errorf("node disruption policy asset is not resolved")
	}

	// Rendering assigns Params on RenderContext. Keep the dependent asset's
	// parameter set intact when the policy asset uses different parameters.
	policyRenderCtx := *renderCtx
	policy, err := p.renderer.RenderAsset(policyAsset, &policyRenderCtx)
	if err != nil {
		return nil, fmt.Errorf("render node disruption policy asset %q: %w", policyAsset.Name, err)
	}
	if policy == nil {
		return nil, nil
	}
	if policy.GetAPIVersion() != machineConfigurationAPIVersion || policy.GetKind() != machineConfigurationKind || policy.GetName() != machineConfigurationName {
		return nil, fmt.Errorf("node disruption policy asset %q must render %s %s/%s", policyAsset.Name, machineConfigurationAPIVersion, machineConfigurationKind, machineConfigurationName)
	}

	want, found, err := unstructured.NestedMap(policy.Object, "spec", "nodeDisruptionPolicy")
	if err != nil {
		return nil, fmt.Errorf("read node disruption policy asset %q: %w", policyAsset.Name, err)
	}
	if !found {
		return nil, fmt.Errorf("node disruption policy asset %q has no spec.nodeDisruptionPolicy", policyAsset.Name)
	}
	return want, nil
}

func (p *Patcher) nodeDisruptionPolicyEffective(ctx context.Context, want map[string]any) (bool, error) {
	if want == nil {
		return false, nil
	}
	effectivePolicy := &unstructured.Unstructured{}
	effectivePolicy.SetAPIVersion(machineConfigurationAPIVersion)
	effectivePolicy.SetKind(machineConfigurationKind)
	if err := p.client.Get(ctx, client.ObjectKey{Name: machineConfigurationName}, effectivePolicy); err != nil {
		if apierrors.IsNotFound(err) || meta.IsNoMatchError(err) {
			return false, nil
		}
		return false, fmt.Errorf("read MachineConfiguration %q: %w", machineConfigurationName, err)
	}

	status, found, err := unstructured.NestedMap(effectivePolicy.Object, "status", "nodeDisruptionPolicyStatus", "clusterPolicies")
	if err != nil || !found {
		return false, err
	}
	return nodeDisruptionPolicyEntriesMatch(status, want)
}

// nodeDisruptionPolicyCoversMachineConfig makes opting out of rollout
// coalescing deliberately narrow. MCO node disruption policies cover only
// Ignition files, systemd units, and SSH keys; any other MachineConfig change
// can require a reboot and is rejected here.
func nodeDisruptionPolicyCoversMachineConfig(policy map[string]any, machineConfig *unstructured.Unstructured) error {
	spec, found, err := unstructured.NestedMap(machineConfig.Object, "spec")
	if err != nil || !found {
		return err
	}
	for field := range spec {
		if field != "config" {
			return fmt.Errorf("node disruption policy cannot cover MachineConfig spec.%s", field)
		}
	}

	config, found, err := unstructured.NestedMap(spec, "config")
	if err != nil || !found {
		return err
	}
	for field := range config {
		switch field {
		case "ignition", "storage", "systemd", "passwd":
		default:
			return fmt.Errorf("node disruption policy cannot cover MachineConfig spec.config.%s", field)
		}
	}
	if err := requireOnlyFields(config, "ignition", "version"); err != nil {
		return err
	}
	if err := nodeDisruptionPolicyCoversNamedEntries(policy, config, "storage", "files", "path"); err != nil {
		return err
	}
	if err := nodeDisruptionPolicyCoversNamedEntries(policy, config, "systemd", "units", "name"); err != nil {
		return err
	}
	return nodeDisruptionPolicyCoversSSHKeys(policy, config)
}

func requireOnlyFields(parent map[string]any, field, allowedChild string) error {
	child, found, err := unstructured.NestedMap(parent, field)
	if err != nil || !found {
		return err
	}
	for key := range child {
		if key != allowedChild {
			return fmt.Errorf("node disruption policy cannot cover MachineConfig spec.config.%s.%s", field, key)
		}
	}
	return nil
}

func nodeDisruptionPolicyCoversNamedEntries(policy, config map[string]any, configSection, entryField, identifier string) error {
	section, found, err := unstructured.NestedMap(config, configSection)
	if err != nil || !found {
		return err
	}
	for field := range section {
		if field != entryField {
			return fmt.Errorf("node disruption policy cannot cover MachineConfig spec.config.%s.%s", configSection, field)
		}
	}
	entries, found, err := unstructured.NestedSlice(section, entryField)
	if err != nil || !found {
		return err
	}
	policyEntries, policyEntriesFound, err := unstructured.NestedSlice(policy, entryField)
	if err != nil {
		return fmt.Errorf("read node disruption policy %s: %w", entryField, err)
	}
	for _, entry := range entries {
		entryMap, ok := entry.(map[string]any)
		if !ok {
			return fmt.Errorf("MachineConfig spec.config.%s.%s entry is not an object", configSection, entryField)
		}
		name, found, err := unstructured.NestedString(entryMap, identifier)
		if err != nil || !found || name == "" {
			return fmt.Errorf("MachineConfig spec.config.%s.%s entry has no %s", configSection, entryField, identifier)
		}
		if !policyEntriesFound || !nodeDisruptionPolicyHasNamedEntry(policyEntries, identifier, name) {
			return fmt.Errorf("node disruption policy does not cover MachineConfig %s %q", entryField, name)
		}
	}
	return nil
}

func nodeDisruptionPolicyHasNamedEntry(entries []any, identifier, name string) bool {
	for _, entry := range entries {
		entryMap, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		entryName, found, err := unstructured.NestedString(entryMap, identifier)
		if err == nil && found && entryName == name {
			return true
		}
	}
	return false
}

func nodeDisruptionPolicyCoversSSHKeys(policy, config map[string]any) error {
	passwd, found, err := unstructured.NestedMap(config, "passwd")
	if err != nil || !found {
		return err
	}
	for field := range passwd {
		if field != "users" {
			return fmt.Errorf("node disruption policy cannot cover MachineConfig spec.config.passwd.%s", field)
		}
	}
	users, found, err := unstructured.NestedSlice(passwd, "users")
	if err != nil || !found {
		return err
	}
	usesSSHKeys := false
	for _, user := range users {
		userMap, ok := user.(map[string]any)
		if !ok {
			return fmt.Errorf("MachineConfig spec.config.passwd.users entry is not an object")
		}
		for field := range userMap {
			if field != "name" && field != "sshAuthorizedKeys" {
				return fmt.Errorf("node disruption policy cannot cover MachineConfig spec.config.passwd.users.%s", field)
			}
		}
		keys, found, err := unstructured.NestedStringSlice(userMap, "sshAuthorizedKeys")
		if err != nil {
			return err
		}
		usesSSHKeys = usesSSHKeys || found && len(keys) > 0
	}
	if !usesSSHKeys {
		return nil
	}
	sshKey, found, err := unstructured.NestedMap(policy, "sshkey")
	if err != nil || !found {
		return fmt.Errorf("node disruption policy does not cover MachineConfig SSH keys")
	}
	if _, found, err := unstructured.NestedSlice(sshKey, "actions"); err != nil || !found {
		return fmt.Errorf("node disruption policy does not cover MachineConfig SSH keys")
	}
	return nil
}

func nodeDisruptionPolicyEntriesMatch(actual, expected map[string]any) (bool, error) {
	hasRequestedEntries := false
	for _, policyType := range []struct {
		field, identifier string
	}{
		{field: "files", identifier: "path"},
		{field: "units", identifier: "name"},
	} {
		want, found, err := unstructured.NestedSlice(expected, policyType.field)
		if err != nil {
			return false, fmt.Errorf("read requested node disruption policy %s: %w", policyType.field, err)
		}
		if !found {
			continue
		}
		if len(want) == 0 {
			return false, fmt.Errorf("requested node disruption policy %s is empty", policyType.field)
		}
		hasRequestedEntries = true
		got, found, err := unstructured.NestedSlice(actual, policyType.field)
		if err != nil || !found || !nodeDisruptionPolicyNamedEntriesMatch(got, want, policyType.identifier) {
			return false, err
		}
	}

	sshKey, found, err := unstructured.NestedMap(expected, "sshkey")
	if err != nil {
		return false, fmt.Errorf("read requested node disruption policy sshkey: %w", err)
	}
	if found {
		hasRequestedEntries = true
		actualSSHKey, found, err := unstructured.NestedMap(actual, "sshkey")
		if err != nil || !found || !nodeDisruptionPolicyActionsMatch(actualSSHKey, sshKey) {
			return false, err
		}
	}

	if !hasRequestedEntries {
		return false, fmt.Errorf("requested node disruption policy has neither files, units, nor sshkey")
	}
	return true, nil
}

func nodeDisruptionPolicyNamedEntriesMatch(actual, expected []any, identifierField string) bool {
	for _, expectedEntry := range expected {
		expectedMap, ok := expectedEntry.(map[string]any)
		if !ok {
			return false
		}
		identifier, found, err := unstructured.NestedString(expectedMap, identifierField)
		if err != nil || !found {
			return false
		}

		matched := false
		for _, actualEntry := range actual {
			actualMap, ok := actualEntry.(map[string]any)
			if !ok {
				continue
			}
			actualIdentifier, _, err := unstructured.NestedString(actualMap, identifierField)
			if err != nil || actualIdentifier != identifier {
				continue
			}
			matched = nodeDisruptionPolicyActionsMatch(actualMap, expectedMap)
			break
		}
		if !matched {
			return false
		}
	}
	return true
}

func nodeDisruptionPolicyActionsMatch(actual, expected map[string]any) bool {
	expectedActions, found, err := unstructured.NestedSlice(expected, "actions")
	if err != nil || !found {
		return false
	}
	actualActions, found, err := unstructured.NestedSlice(actual, "actions")
	return err == nil && found && reflect.DeepEqual(actualActions, expectedActions)
}
