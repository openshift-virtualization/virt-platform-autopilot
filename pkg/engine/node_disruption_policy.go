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
	"slices"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kubevirt/virt-platform-autopilot/pkg/assets"
	pkgcontext "github.com/kubevirt/virt-platform-autopilot/pkg/context"
	"github.com/kubevirt/virt-platform-autopilot/pkg/overrides"
	"github.com/kubevirt/virt-platform-autopilot/pkg/status"
)

const (
	machineConfigurationAPIVersion = "operator.openshift.io/v1"
	machineConfigurationKind       = "MachineConfiguration"
	machineConfigurationName       = "cluster"
)

// ReconcileNodeDisruptionPolicies applies one aggregate policy for the
// MachineConfiguration singleton. The API models files and units as keyed
// map-lists, but all policy templates share one object and one SSA field
// manager. Aggregating prevents one active policy asset from removing entries
// contributed by another. Entries for inactive or root-excluded dependents are
// omitted, so their later manual MachineConfig deletion follows MCO's default
// reboot path rather than trying to restart a service that no longer exists.
// ReconcileNodeDisruptionPolicies returns whether Autopilot controls the
// singleton policy and how many file/unit entries changed.
func (p *Patcher) ReconcileNodeDisruptionPolicies(ctx context.Context, allAssets []assets.AssetMetadata, active map[string]bool, pruneTombstoneActions bool, renderCtx *pkgcontext.RenderContext) (bool, int, error) {
	desired, allowCreate, err := p.aggregateNodeDisruptionPolicies(allAssets, active, pruneTombstoneActions, renderCtx)
	if err != nil {
		p.observeNodeDisruptionPolicyAssets(nodeDisruptionPolicyAssets(allAssets), nodeDisruptionPolicyStatusObject(), "applyFailed", status.AssetReasonNodeDisruptionPolicyAggregationFail, renderCtx)
		return false, 0, err
	}
	if desired == nil {
		p.observeNodeDisruptionPolicyAssets(nodeDisruptionPolicyAssets(allAssets), nodeDisruptionPolicyStatusObject(), "excluded", status.AssetReasonRootExclusion, renderCtx)
		return false, 0, nil
	}
	managed, changes, err := p.reconcileAggregateNodeDisruptionPolicy(ctx, desired, allowCreate)
	state, reason := "synced", ""
	if err != nil {
		state, reason = "applyFailed", status.AssetReasonNodeDisruptionPolicyApplyFail
	} else if !managed {
		state, reason = "unmanaged", status.AssetReasonUserOptOut
	}
	p.observeNodeDisruptionPolicyAssets(activeNodeDisruptionPolicyAssets(allAssets, active), desired, state, reason, renderCtx)
	return managed, changes, err
}

func (p *Patcher) observeNodeDisruptionPolicyAssets(policyAssets []assets.AssetMetadata, object *unstructured.Unstructured, state, reason string, renderCtx *pkgcontext.RenderContext) {
	for _, asset := range policyAssets {
		if sidecar, err := p.renderNodeDisruptionPolicySidecar(&asset, renderCtx); err == nil && sidecar != nil {
			status.DefaultStore.SetGolden(asset.Name, sidecar)
		}
		status.DefaultStore.ObserveSharedAsset(asset.Name, object, state, reason, "spec.nodeDisruptionPolicy")
	}
}

func (p *Patcher) renderNodeDisruptionPolicySidecar(policyAsset *assets.AssetMetadata, renderCtx *pkgcontext.RenderContext) (*unstructured.Unstructured, error) {
	if renderCtx == nil {
		return nil, nil
	}
	want, err := p.renderNodeDisruptionPolicy(policyAsset, renderCtx)
	if err != nil || want == nil {
		return nil, err
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": machineConfigurationAPIVersion,
		"kind":       machineConfigurationKind,
		"metadata":   map[string]any{"name": machineConfigurationName},
		"spec":       map[string]any{"nodeDisruptionPolicy": want},
	}}, nil
}

func nodeDisruptionPolicyStatusObject() *unstructured.Unstructured {
	object := &unstructured.Unstructured{}
	object.SetAPIVersion(machineConfigurationAPIVersion)
	object.SetKind(machineConfigurationKind)
	object.SetName(machineConfigurationName)
	return object
}

func nodeDisruptionPolicyAssets(allAssets []assets.AssetMetadata) []assets.AssetMetadata {
	active := make(map[string]bool, len(allAssets))
	for _, asset := range allAssets {
		active[asset.Name] = true
	}
	return activeNodeDisruptionPolicyAssets(allAssets, active)
}

func activeNodeDisruptionPolicyAssets(allAssets []assets.AssetMetadata, active map[string]bool) []assets.AssetMetadata {
	seen := map[string]bool{}
	var out []assets.AssetMetadata
	for _, asset := range allAssets {
		if asset.NodeDisruptionPolicy == nil || asset.NodeDisruptionPolicy.PolicyAsset() == nil || !active[asset.Name] {
			continue
		}
		policy := asset.NodeDisruptionPolicy.PolicyAsset()
		if !seen[policy.Name] {
			seen[policy.Name] = true
			out = append(out, *policy)
		}
	}
	return out
}

// reconcileAggregateNodeDisruptionPolicy deliberately retains the live
// MachineConfiguration's normal user controls. The aggregate is not a
// template asset itself, but it is still the desired state for the same
// singleton, so mode: unmanaged and ignore-fields must have the same meaning
// as they do for ordinary assets. JSON patches are not allowed for this
// node-level API, just as they are not allowed for MachineConfig.
// The second return value is the number of file/unit entry changes so callers
// can log after other assets without changing apply order.
func (p *Patcher) reconcileAggregateNodeDisruptionPolicy(ctx context.Context, desired *unstructured.Unstructured, allowCreate bool) (bool, int, error) {
	live := &unstructured.Unstructured{}
	live.SetGroupVersionKind(desired.GroupVersionKind())
	err := p.applier.Get(ctx, client.ObjectKey{Name: desired.GetName()}, live)
	if apierrors.IsNotFound(err) {
		// An empty aggregate must prune entries previously owned by Autopilot,
		// but must not create the singleton on clusters that never enabled an
		// NDP-dependent feature.
		if !allowCreate {
			return false, 0, nil
		}
		changes := nodeDisruptionPolicyChangeCount(desired, nil)
		_, err = p.applier.Apply(ctx, desired, true)
		return err == nil, changes, err
	}
	if err != nil {
		return false, 0, fmt.Errorf("read aggregate node disruption policy: %w", err)
	}
	if !allowCreate && !HasManagedByLabel(live) {
		return false, 0, nil
	}
	if overrides.IsUnmanaged(live) {
		return false, 0, nil
	}

	annotations := live.GetAnnotations()
	// An ignored field can intentionally retain entries that the aggregate would
	// otherwise prune. Reconcile the singleton normally, but do not release a
	// MachineConfig tombstone until an administrator removes that override.
	tombstoneSafe := true
	if _, ignored := annotations[overrides.AnnotationIgnoreFields]; ignored {
		tombstoneSafe = false
	}

	var maskErr error
	desired, maskErr = overrides.MaskIgnoredFields(desired, live)
	if maskErr != nil {
		return false, 0, fmt.Errorf("mask aggregate node disruption policy ignored fields: %w", maskErr)
	}
	changes := nodeDisruptionPolicyChangeCount(desired, live)
	_, err = p.applier.Apply(ctx, desired, true)
	return tombstoneSafe && err == nil, changes, err
}

// nodeDisruptionPolicyChangeCount returns how many file/unit entries differ
// between desired and live. Each added, removed, or modified keyed entry
// counts as one change so a multi-entry aggregate update is visible as
// changes>1 rather than a single opaque apply.
func nodeDisruptionPolicyChangeCount(desired, live *unstructured.Unstructured) int {
	changes := nodeDisruptionPolicyKeyedEntryChanges(desired, live, "files", "path")
	changes += nodeDisruptionPolicyKeyedEntryChanges(desired, live, "units", "name")
	return changes
}

func nodeDisruptionPolicyKeyedEntryChanges(desired, live *unstructured.Unstructured, field, identifier string) int {
	desiredEntries := nodeDisruptionPolicyEntryIndex(desired, field, identifier)
	liveEntries := nodeDisruptionPolicyEntryIndex(live, field, identifier)
	changes := 0
	for key, want := range desiredEntries {
		got, found := liveEntries[key]
		if !found || !reflect.DeepEqual(want, got) {
			changes++
		}
	}
	for key := range liveEntries {
		if _, found := desiredEntries[key]; !found {
			changes++
		}
	}
	return changes
}

func nodeDisruptionPolicyEntryIndex(obj *unstructured.Unstructured, field, identifier string) map[string]map[string]any {
	out := map[string]map[string]any{}
	if obj == nil {
		return out
	}
	entries, found, err := unstructured.NestedSlice(obj.Object, "spec", "nodeDisruptionPolicy", field)
	if err != nil || !found {
		return out
	}
	for _, entry := range entries {
		entryMap, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		key, found, err := unstructured.NestedString(entryMap, identifier)
		if err != nil || !found || key == "" {
			continue
		}
		out[key] = entryMap
	}
	return out
}

// NodeDisruptionPolicyGenerationObserved reports whether MCO has processed the
// currently applied policy generation. Tombstone cleanup uses this as a barrier
// before checking whether the deleted MachineConfig's own files and units still
// have an effective action; unrelated administrator policy entries are allowed.
func (p *Patcher) NodeDisruptionPolicyGenerationObserved(ctx context.Context) (bool, error) {
	policy := &unstructured.Unstructured{}
	policy.SetAPIVersion(machineConfigurationAPIVersion)
	policy.SetKind(machineConfigurationKind)
	if err := p.client.Get(ctx, client.ObjectKey{Name: machineConfigurationName}, policy); err != nil {
		if apierrors.IsNotFound(err) || meta.IsNoMatchError(err) {
			return false, nil
		}
		return false, fmt.Errorf("read MachineConfiguration %q: %w", machineConfigurationName, err)
	}
	observed, found, err := unstructured.NestedInt64(policy.Object, "status", "observedGeneration")
	if err != nil || !found {
		return false, err
	}
	return observed >= policy.GetGeneration(), nil
}

func (p *Patcher) aggregateNodeDisruptionPolicies(allAssets []assets.AssetMetadata, active map[string]bool, pruneTombstoneActions bool, renderCtx *pkgcontext.RenderContext) (*unstructured.Unstructured, bool, error) {
	rules := nodeDisruptionPolicyExclusionRules(renderCtx)
	if IsResourceExcluded(machineConfigurationKind, "", machineConfigurationName, rules) {
		return nil, false, nil
	}
	aggregate, hasActiveDependent, err := p.nodeDisruptionPolicyAggregateEntries(allAssets, active, renderCtx, rules)
	if err != nil {
		return nil, false, err
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": machineConfigurationAPIVersion,
		"kind":       machineConfigurationKind,
		"metadata":   map[string]any{"name": machineConfigurationName},
		"spec":       map[string]any{"nodeDisruptionPolicy": aggregate},
	}}, hasActiveDependent || pruneTombstoneActions, nil
}

func nodeDisruptionPolicyExclusionRules(renderCtx *pkgcontext.RenderContext) []ExclusionRule {
	if renderCtx == nil || renderCtx.HCO == nil {
		return nil
	}
	rules, err := ParseDisabledResources(renderCtx.HCO.GetAnnotations()[DisabledResourcesAnnotation])
	if err != nil {
		// Match normal asset reconciliation: an invalid root-exclusion annotation
		// is ignored rather than silently changing managed resources.
		return nil
	}
	return rules
}

func (p *Patcher) nodeDisruptionPolicyAggregateEntries(allAssets []assets.AssetMetadata, active map[string]bool, renderCtx *pkgcontext.RenderContext, rules []ExclusionRule) (map[string]any, bool, error) {
	files := map[string]map[string]any{}
	units := map[string]map[string]any{}
	var sshKey map[string]any
	for i := range allAssets {
		asset := &allAssets[i]
		if asset.NodeDisruptionPolicy == nil || asset.NodeDisruptionPolicy.PolicyAsset() == nil || !active[asset.Name] {
			continue
		}
		candidate, included, err := p.nodeDisruptionPolicyEntriesForDependent(asset, renderCtx, rules)
		if err != nil {
			return nil, false, err
		}
		if !included {
			continue
		}
		if err := mergeNodeDisruptionPolicyEntries(files, candidate, "files", "path"); err != nil {
			return nil, false, err
		}
		if err := mergeNodeDisruptionPolicyEntries(units, candidate, "units", "name"); err != nil {
			return nil, false, err
		}
		sshKey, err = mergeNodeDisruptionPolicySSHKey(sshKey, candidate)
		if err != nil {
			return nil, false, err
		}
	}
	aggregate := map[string]any{
		"files": nodeDisruptionPolicySortedEntries(files),
		"units": nodeDisruptionPolicySortedEntries(units),
	}
	if sshKey != nil {
		aggregate["sshkey"] = sshKey
	}
	return aggregate, len(files) > 0 || len(units) > 0 || sshKey != nil, nil
}

func (p *Patcher) nodeDisruptionPolicyEntriesForDependent(asset *assets.AssetMetadata, renderCtx *pkgcontext.RenderContext, rules []ExclusionRule) (map[string]any, bool, error) {
	dependentCtx := *renderCtx
	dependent, err := p.renderer.RenderAsset(asset, &dependentCtx)
	if err != nil {
		return nil, false, fmt.Errorf("render node disruption policy dependent %q: %w", asset.Name, err)
	}
	if dependent == nil || IsResourceExcluded(dependent.GetKind(), dependent.GetNamespace(), dependent.GetName(), rules) {
		return nil, false, nil
	}
	want, err := p.renderNodeDisruptionPolicy(asset.NodeDisruptionPolicy.PolicyAsset(), renderCtx)
	if err != nil {
		return nil, false, err
	}
	if want == nil {
		return nil, false, fmt.Errorf("node disruption policy asset %q rendered empty for active dependent %q", asset.NodeDisruptionPolicy.Asset, asset.Name)
	}
	if err := nodeDisruptionPolicyCoversMachineConfig(want, dependent); err != nil {
		return nil, false, fmt.Errorf("node disruption policy asset %q does not cover dependent %q: %w", asset.NodeDisruptionPolicy.Asset, asset.Name, err)
	}
	return want, true, nil
}

func mergeNodeDisruptionPolicySSHKey(existing, policy map[string]any) (map[string]any, error) {
	candidate, found, err := unstructured.NestedMap(policy, "sshkey")
	if err != nil || !found {
		return existing, err
	}
	if existing != nil && !reflect.DeepEqual(existing, candidate) {
		return nil, fmt.Errorf("node disruption policy sshkey actions conflict between active assets")
	}
	return candidate, nil
}

func mergeNodeDisruptionPolicyEntries(destination map[string]map[string]any, policy map[string]any, field, identifier string) error {
	entries, found, err := unstructured.NestedSlice(policy, field)
	if err != nil || !found {
		return err
	}
	for _, entry := range entries {
		entryMap, ok := entry.(map[string]any)
		if !ok {
			return fmt.Errorf("node disruption policy %s entry is not an object", field)
		}
		key, found, err := unstructured.NestedString(entryMap, identifier)
		if err != nil || !found || key == "" {
			return fmt.Errorf("node disruption policy %s entry has no %s", field, identifier)
		}
		if existing, found := destination[key]; found && !reflect.DeepEqual(existing, entryMap) {
			return fmt.Errorf("node disruption policy %s entry %q conflicts between active assets", field, key)
		}
		destination[key] = entryMap
	}
	return nil
}

func nodeDisruptionPolicySortedEntries(entries map[string]map[string]any) []any {
	keys := make([]string, 0, len(entries))
	for key := range entries {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	result := make([]any, 0, len(keys))
	for _, key := range keys {
		result = append(result, entries[key])
	}
	return result
}

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
	annotations[NodeDisruptionPolicyCoalescingBypassAnnotation] = "true"
	desired.SetAnnotations(annotations)
	return true, nil
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
