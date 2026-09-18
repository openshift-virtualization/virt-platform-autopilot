/*
Copyright 2026 The Virt Platform Autopilot Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	pkgcontext "github.com/kubevirt/virt-platform-autopilot/pkg/context"
	"github.com/kubevirt/virt-platform-autopilot/pkg/observability"
)

const (
	// MachineConfigCoalescingBypassAnnotation opts one managed MachineConfig out
	// of staging. It is deliberately read from (and preserved on) the live MC.
	MachineConfigCoalescingBypassAnnotation = "platform.kubevirt.io/bypass-mcp-rollout-coalescing"
	machineConfigStagingConfigMap           = "virt-platform-autopilot-mc-staging"
	machineConfigGroup                      = "machineconfiguration.openshift.io"
	machineConfigKind                       = "MachineConfig"
)

var machineConfigPoolGVK = schema.GroupVersionKind{Group: machineConfigGroup, Version: "v1", Kind: "MachineConfigPool"}

// MachineConfigPoolRolloutConditionTypes are the MachineConfigPool status
// conditions that decide whether a staged MachineConfig update is released.
// The controller's MCP watch filters on exactly these, so the two stay in sync.
//
// Degraded is deliberately absent: it does not gate the release (see
// machineConfigPoolReleasesStagedUpdate) and it flaps throughout a rollout, so
// watching it would only add reconcile churn.
var MachineConfigPoolRolloutConditionTypes = []string{"RenderDegraded", "Updating"}

type machineConfigStage struct {
	StagedAt      time.Time `json:"stagedAt"`
	DesiredHash   string    `json:"desiredHash"`
	MatchingPools []string  `json:"matchingPools"`
}

// machineConfigStagingStore is an in-memory mirror of the staging ConfigMap.
// The operator is the ConfigMap's only writer and leader election guarantees a
// single active instance, so the mirror is authoritative once loaded: staged
// state can be consulted for every MachineConfig on every reconcile without a
// per-asset API read.
type machineConfigStagingStore struct {
	mu       sync.Mutex
	entries  map[string]machineConfigStage
	loaded   bool
	hasOwner bool
}

func isMachineConfig(obj *unstructured.Unstructured) bool {
	return obj != nil && obj.GetKind() == machineConfigKind && obj.GroupVersionKind().Group == machineConfigGroup
}

// coalesceMachineConfigUpdate returns true when an existing MC update may be
// applied. Creation is never staged. The first matching MCP that releases the
// update wins; this intentionally avoids waiting forever for every MCP which can
// select the same MachineConfig.
//
// desiredHash identifies the update being staged. It is computed by the caller
// from the rendered desired state *before* ignore-field masking: masking folds in
// values read from the live object, so hashing the masked result would let drift
// on the live side reset the staging timestamp.
func (p *Patcher) coalesceMachineConfigUpdate(ctx context.Context, desired, live *unstructured.Unstructured, desiredHash string, renderCtx *pkgcontext.RenderContext) (bool, error) {
	if !isMachineConfig(desired) || live == nil {
		return true, nil
	}
	namespace := renderCtx.HCO.GetNamespace()
	if namespace == "" {
		return true, nil
	}

	p.copyMachineConfigBypassAnnotation(desired, live)
	if machineConfigCoalescingBypassed(live) {
		return true, p.unstageMachineConfigUpdate(ctx, namespace, desired.GetName())
	}

	// Include pools selected by the live labels as well as by the proposed
	// labels. Changing an MC label can remove configuration from an old pool,
	// which is itself a pool-affecting update and must be coalesced too.
	pools, err := p.matchingMachineConfigPools(ctx, desired, live)
	if err != nil {
		// The MCO is optional outside OpenShift. Absence must retain normal MC reconciliation.
		if apierrors.IsNotFound(err) || meta.IsNoMatchError(err) {
			return true, nil
		}
		return false, err
	}
	if len(pools) == 0 {
		return true, p.unstageMachineConfigUpdate(ctx, namespace, desired.GetName())
	}
	for _, pool := range pools {
		if machineConfigPoolReleasesStagedUpdate(pool) {
			// The staged entry is deliberately left in place here. The apply can
			// still be refused downstream by the namespace guard or the token
			// bucket, and dropping the entry now would lose the original staging
			// timestamp on every refusal. ReconcileAsset clears it once the apply
			// has actually reached the API server.
			return true, nil
		}
	}

	names := make([]string, 0, len(pools))
	for _, pool := range pools {
		names = append(names, pool.GetName())
	}
	slices.Sort(names)

	stage, err := p.machineConfigStage(ctx, namespace, desired.GetName())
	if err != nil {
		return false, err
	}
	switch {
	case stage == nil || stage.DesiredHash != desiredHash:
		stage = &machineConfigStage{StagedAt: time.Now().UTC(), DesiredHash: desiredHash, MatchingPools: names}
		if err := p.stageMachineConfigUpdate(ctx, namespace, desired.GetName(), *stage, renderCtx.HCO); err != nil {
			return false, err
		}
		log.Log.Info("Staged MachineConfig update", "machineconfig", desired.GetName(), "matchingPools", names, "stagedAt", stage.StagedAt)
		if p.eventRecorder != nil && renderCtx.HCO != nil {
			p.eventRecorder.MachineConfigUpdateStaged(renderCtx.HCO, desired.GetName(), names)
		}
	case !slices.Equal(stage.MatchingPools, names):
		stage.MatchingPools = names
		if err := p.stageMachineConfigUpdate(ctx, namespace, desired.GetName(), *stage, renderCtx.HCO); err != nil {
			return false, err
		}
	}
	// Clear first: the set of matching pools shrinks when a selector changes, and
	// the obsolete pool series would otherwise linger.
	observability.ClearMachineConfigUpdateStaged(desired.GetName())
	observability.SetMachineConfigUpdateStaged(desired.GetName(), names, stage.StagedAt)
	return false, nil
}

// clearMachineConfigStaging drops any staged entry for a MachineConfig asset. It
// is a no-op for every other kind, and needs no API call unless something is
// actually staged.
func (p *Patcher) clearMachineConfigStaging(ctx context.Context, desired *unstructured.Unstructured, renderCtx *pkgcontext.RenderContext) (*machineConfigStage, error) {
	if !isMachineConfig(desired) || renderCtx == nil || renderCtx.HCO == nil {
		return nil, nil
	}
	namespace := renderCtx.HCO.GetNamespace()
	if namespace == "" {
		return nil, nil
	}
	stage, err := p.machineConfigStage(ctx, namespace, desired.GetName())
	if err != nil || stage == nil {
		return stage, err
	}
	return stage, p.unstageMachineConfigUpdate(ctx, namespace, desired.GetName())
}

// machineConfigCoalescingBypassed reports whether the live MachineConfig opts out
// of staging. An unparseable value is treated as "no bypass": staging is the safe
// default, and a typo must not silently start a node-rebooting rollout.
func machineConfigCoalescingBypassed(live *unstructured.Unstructured) bool {
	value, ok := live.GetAnnotations()[MachineConfigCoalescingBypassAnnotation]
	if !ok {
		return false
	}
	bypass, err := strconv.ParseBool(value)
	return err == nil && bypass
}

func (p *Patcher) copyMachineConfigBypassAnnotation(desired, live *unstructured.Unstructured) {
	if value, ok := live.GetAnnotations()[MachineConfigCoalescingBypassAnnotation]; ok {
		annotations := desired.GetAnnotations()
		if annotations == nil {
			annotations = map[string]string{}
		}
		annotations[MachineConfigCoalescingBypassAnnotation] = value
		desired.SetAnnotations(annotations)
	}
}

func (p *Patcher) matchingMachineConfigPools(ctx context.Context, objects ...*unstructured.Unstructured) ([]unstructured.Unstructured, error) {
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(machineConfigPoolGVK.GroupVersion().WithKind("MachineConfigPoolList"))
	// Served from the manager cache: cmd/main.go exempts MachineConfigPool from the
	// managed-by DefaultLabelSelector precisely so this needs no API round trip per
	// MachineConfig per reconcile. Where the MCP CRD is absent the cache cannot map
	// the kind and returns a no-match error, which the caller treats as "no MCO".
	if err := p.client.List(ctx, list); err != nil {
		return nil, err
	}
	matched := map[string]unstructured.Unstructured{}
	for _, pool := range list.Items {
		selector, err := machineConfigPoolSelector(&pool)
		if err != nil {
			// Guessing is unsafe in both directions: skipping the pool can release
			// an update early, and falling back to an empty selector would match
			// every MachineConfig. Fail the reconcile instead.
			return nil, err
		}
		if selector == nil {
			continue
		}
		for _, obj := range objects {
			if obj != nil && selector.Matches(labels.Set(obj.GetLabels())) {
				matched[pool.GetName()] = pool
				break
			}
		}
	}
	result := make([]unstructured.Unstructured, 0, len(matched))
	for _, pool := range matched {
		result = append(result, pool)
	}
	return result, nil
}

// machineConfigPoolSelector builds the MachineConfig selector of a pool. A nil
// selector with a nil error means the pool selects no MachineConfig at all.
func machineConfigPoolSelector(pool *unstructured.Unstructured) (labels.Selector, error) {
	raw, found, err := unstructured.NestedMap(pool.Object, "spec", "machineConfigSelector")
	if err != nil {
		return nil, fmt.Errorf("malformed spec.machineConfigSelector on MachineConfigPool %s: %w", pool.GetName(), err)
	}
	if !found {
		return nil, nil
	}
	matchLabels, _, err := unstructured.NestedStringMap(raw, "matchLabels")
	if err != nil {
		return nil, fmt.Errorf("malformed spec.machineConfigSelector.matchLabels on MachineConfigPool %s: %w", pool.GetName(), err)
	}
	matchExpressions, err := machineConfigPoolSelectorExpressions(raw, pool.GetName())
	if err != nil {
		return nil, err
	}
	selector, err := metav1.LabelSelectorAsSelector(&metav1.LabelSelector{MatchLabels: matchLabels, MatchExpressions: matchExpressions})
	if err != nil {
		return nil, fmt.Errorf("invalid spec.machineConfigSelector on MachineConfigPool %s: %w", pool.GetName(), err)
	}
	return selector, nil
}

func machineConfigPoolSelectorExpressions(raw map[string]any, pool string) ([]metav1.LabelSelectorRequirement, error) {
	items, found, err := unstructured.NestedSlice(raw, "matchExpressions")
	if err != nil {
		return nil, fmt.Errorf("malformed spec.machineConfigSelector.matchExpressions on MachineConfigPool %s: %w", pool, err)
	}
	if !found {
		return nil, nil
	}
	result := make([]metav1.LabelSelectorRequirement, 0, len(items))
	for _, item := range items {
		encoded, err := json.Marshal(item)
		if err != nil {
			return nil, fmt.Errorf("malformed spec.machineConfigSelector.matchExpressions on MachineConfigPool %s: %w", pool, err)
		}
		var requirement metav1.LabelSelectorRequirement
		if err := json.Unmarshal(encoded, &requirement); err != nil {
			return nil, fmt.Errorf("malformed spec.machineConfigSelector.matchExpressions on MachineConfigPool %s: %w", pool, err)
		}
		result = append(result, requirement)
	}
	return result, nil
}

// MachineConfigPoolRolloutConditions extracts the release-relevant conditions of a
// MachineConfigPool as a type→status map, for both the release decision and the
// controller's watch predicate.
func MachineConfigPoolRolloutConditions(pool *unstructured.Unstructured) map[string]string {
	if pool == nil {
		return nil
	}
	conditions, _, _ := unstructured.NestedSlice(pool.Object, "status", "conditions")
	result := make(map[string]string, len(MachineConfigPoolRolloutConditionTypes))
	for _, item := range conditions {
		condition, ok := item.(map[string]any)
		if !ok {
			continue
		}
		conditionType, _ := condition["type"].(string)
		if !slices.Contains(MachineConfigPoolRolloutConditionTypes, conditionType) {
			continue
		}
		status, _ := condition["status"].(string)
		result[conditionType] = status
	}
	return result
}

// machineConfigPoolReleasesStagedUpdate reports whether a pool is in a state that
// lets a staged MachineConfig update through.
//
// Degraded does not veto the release. A pool that is Degraded *and* Updating is
// already disrupting nodes, which is exactly the rollout worth joining, and the
// degraded condition flaps whenever a single node fails mid-rollout. Vetoing on it
// would skip that window and hold the update until some later rollout.
//
// A degraded pool that is not updating simply keeps the update staged, as any other
// stable pool would: no rollout is running, so there is nothing to coalesce into.
// When the pool recovers and resumes, Updating=True releases the update then.
func machineConfigPoolReleasesStagedUpdate(pool unstructured.Unstructured) bool {
	conditions := MachineConfigPoolRolloutConditions(&pool)
	// RenderDegraded means MCO cannot turn this pool's MachineConfigs into a
	// rendered config at all, so no rollout — and therefore no reboot — can follow
	// an apply. Staging here would be an indefinite hold with no upside, and the
	// staged update may well be the change that fixes the render.
	if conditions["RenderDegraded"] == "True" {
		return true
	}
	return conditions["Updating"] == "True"
}

func machineConfigDesiredHash(obj *unstructured.Unstructured) (string, error) {
	// The rendered desired state carries no server-populated metadata, but strip it
	// defensively so the hash can never churn on resourceVersion alone.
	normalized := obj.DeepCopy()
	normalized.SetResourceVersion("")
	normalized.SetManagedFields(nil)
	normalized.SetUID("")
	normalized.SetGeneration(0)
	normalized.SetCreationTimestamp(metav1.Time{})
	// encoding/json sorts map keys, so this is stable across reconciles.
	encoded, err := json.Marshal(normalized.Object)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

// loadMachineConfigStaging populates the in-memory mirror on first use. The
// caller must hold p.staging.mu.
func (p *Patcher) loadMachineConfigStaging(ctx context.Context, namespace string) error {
	if p.staging.loaded {
		return nil
	}
	p.staging.entries = map[string]machineConfigStage{}
	cm := &corev1.ConfigMap{}
	// Read past the cache: the manager's ConfigMap informer is narrowed by field
	// selector to the metrics client-CA ConfigMap and never sees this one.
	err := p.applier.GetDirectObject(ctx, client.ObjectKey{Namespace: namespace, Name: machineConfigStagingConfigMap}, cm)
	switch {
	case apierrors.IsNotFound(err):
	case err != nil:
		return err
	default:
		for name, raw := range cm.Data {
			var stage machineConfigStage
			if err := json.Unmarshal([]byte(raw), &stage); err != nil {
				// A corrupt entry must not wedge reconciliation of every MachineConfig.
				// Drop it; the next staging decision rewrites it from scratch.
				log.Log.V(1).Info("Discarding unparseable staged MachineConfig state", "machineconfig", name, "error", err.Error())
				continue
			}
			p.staging.entries[name] = stage
		}
		p.staging.hasOwner = len(cm.OwnerReferences) > 0
	}
	p.staging.loaded = true
	return nil
}

func (p *Patcher) machineConfigStage(ctx context.Context, namespace, name string) (*machineConfigStage, error) {
	p.staging.mu.Lock()
	defer p.staging.mu.Unlock()
	if err := p.loadMachineConfigStaging(ctx, namespace); err != nil {
		return nil, err
	}
	stage, ok := p.staging.entries[name]
	if !ok {
		return nil, nil
	}
	return &stage, nil
}

func (p *Patcher) stageMachineConfigUpdate(ctx context.Context, namespace, name string, stage machineConfigStage, owner *unstructured.Unstructured) error {
	p.staging.mu.Lock()
	defer p.staging.mu.Unlock()
	if err := p.loadMachineConfigStaging(ctx, namespace); err != nil {
		return err
	}
	raw, err := json.Marshal(stage)
	if err != nil {
		return err
	}
	encoded := string(raw)
	if err := p.writeStagingEntry(ctx, namespace, name, &encoded, owner); err != nil {
		return err
	}
	p.staging.entries[name] = stage
	return nil
}

func (p *Patcher) unstageMachineConfigUpdate(ctx context.Context, namespace, name string) error {
	p.staging.mu.Lock()
	defer p.staging.mu.Unlock()
	if err := p.loadMachineConfigStaging(ctx, namespace); err != nil {
		return err
	}
	if _, ok := p.staging.entries[name]; !ok {
		observability.ClearMachineConfigUpdateStaged(name)
		return nil
	}
	if err := p.writeStagingEntry(ctx, namespace, name, nil, nil); err != nil {
		return err
	}
	delete(p.staging.entries, name)
	observability.ClearMachineConfigUpdateStaged(name)
	return nil
}

// writeStagingEntry patches a single key of the staging ConfigMap; a nil value
// deletes the key. A merge patch touches only the key at hand, so there is no
// read-modify-write window and no resource version to track. The caller must hold
// p.staging.mu.
func (p *Patcher) writeStagingEntry(ctx context.Context, namespace, name string, value *string, owner *unstructured.Unstructured) error {
	patch := map[string]any{"data": map[string]any{name: nil}}
	if value != nil {
		patch["data"] = map[string]any{name: *value}
	}
	// The ConfigMap is operator state, not user configuration. Give it the HCO
	// lifecycle where the HCO has a usable identity. The guard keeps fake-client
	// unit tests and early object-adoption paths free of invalid owner refs.
	ownerRefs := stagingOwnerReferences(owner)
	if ownerRefs != nil && !p.staging.hasOwner {
		patch["metadata"] = map[string]any{"ownerReferences": ownerRefs}
	}
	raw, err := json.Marshal(patch)
	if err != nil {
		return err
	}

	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: machineConfigStagingConfigMap}}
	err = p.client.Patch(ctx, cm, client.RawPatch(types.MergePatchType, raw))
	if apierrors.IsNotFound(err) {
		if value == nil {
			return nil
		}
		created := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:       namespace,
				Name:            machineConfigStagingConfigMap,
				Labels:          map[string]string{ManagedByLabel: ManagedByValue},
				OwnerReferences: ownerRefs,
			},
			Data: map[string]string{name: *value},
		}
		if err := p.client.Create(ctx, created); err != nil {
			return err
		}
		p.staging.hasOwner = ownerRefs != nil
		return nil
	}
	if err != nil {
		return err
	}
	if ownerRefs != nil {
		p.staging.hasOwner = true
	}
	return nil
}

func stagingOwnerReferences(owner *unstructured.Unstructured) []metav1.OwnerReference {
	if owner == nil || owner.GetUID() == "" || owner.GetAPIVersion() == "" || owner.GetKind() == "" {
		return nil
	}
	ownerRef := metav1.NewControllerRef(owner, owner.GroupVersionKind())
	// BlockOwnerDeletion requires permission to update the owner's finalizers.
	// The operator does not need that permission to give the ConfigMap an HCO
	// owner, so leave it unset.
	ownerRef.BlockOwnerDeletion = nil
	return []metav1.OwnerReference{*ownerRef}
}
