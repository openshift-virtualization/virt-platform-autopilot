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
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/kubevirt/virt-platform-operator/pkg/assets"
	pkgcontext "github.com/kubevirt/virt-platform-operator/pkg/context"
	"github.com/kubevirt/virt-platform-operator/pkg/overrides"
	"github.com/kubevirt/virt-platform-operator/pkg/throttling"
)

// Patcher implements the Patched Baseline algorithm
type Patcher struct {
	renderer      *Renderer
	applier       *Applier
	driftDetector *DriftDetector
	throttle      *throttling.TokenBucket
	client        client.Client
}

// NewPatcher creates a new patcher
func NewPatcher(c client.Client, loader *assets.Loader) *Patcher {
	return &Patcher{
		renderer:      NewRenderer(loader),
		applier:       NewApplier(c),
		driftDetector: NewDriftDetector(c),
		throttle:      throttling.NewTokenBucket(),
		client:        c,
	}
}

// ReconcileAsset performs the full Patched Baseline algorithm for an asset
// Returns true if the asset was applied, false if skipped/unchanged
func (p *Patcher) ReconcileAsset(ctx context.Context, assetMeta *assets.AssetMetadata, renderCtx *pkgcontext.RenderContext) (bool, error) {
	logger := log.FromContext(ctx)

	logger.V(1).Info("Reconciling asset",
		"name", assetMeta.Name,
		"path", assetMeta.Path,
		"component", assetMeta.Component,
	)

	// Step 1: Render asset template → Opinionated State
	desired, err := p.renderer.RenderAsset(assetMeta, renderCtx)
	if err != nil {
		return false, fmt.Errorf("failed to render asset %s: %w", assetMeta.Name, err)
	}

	// Handle conditional assets that don't apply (template rendered empty)
	if desired == nil {
		logger.V(1).Info("Asset not applicable (conditions not met)",
			"name", assetMeta.Name,
		)
		return false, nil
	}

	// Get live object from cluster
	live := &unstructured.Unstructured{}
	live.SetGroupVersionKind(desired.GroupVersionKind())
	objKey := client.ObjectKey{
		Namespace: desired.GetNamespace(),
		Name:      desired.GetName(),
	}

	err = p.applier.Get(ctx, objKey, live)
	if err != nil && !errors.IsNotFound(err) {
		return false, fmt.Errorf("failed to get live object: %w", err)
	}

	liveExists := err == nil

	// Step 2: Check opt-out annotation (mode: unmanaged)
	if liveExists && overrides.IsUnmanaged(live) {
		logger.V(1).Info("Asset is unmanaged, skipping",
			"name", assetMeta.Name,
			"kind", desired.GetKind(),
			"namespace", desired.GetNamespace(),
			"objectName", desired.GetName(),
		)
		return false, nil
	}

	// Step 3: Apply user patch (in-memory) → Modified State
	if liveExists {
		desired, err = overrides.ApplyJSONPatch(live)
		if err != nil {
			logger.Error(err, "Failed to apply JSON patch, using desired without patch",
				"name", assetMeta.Name,
			)
			// Continue with unpatch desired (don't fail reconciliation)
		}
	}

	// Step 4: Mask ignored fields → Effective Desired State
	if liveExists {
		desired, err = overrides.MaskIgnoredFields(desired, live)
		if err != nil {
			return false, fmt.Errorf("failed to mask ignored fields: %w", err)
		}
	}

	// Step 5: Drift detection
	hasDrift := false
	if liveExists {
		hasDrift, err = p.driftDetector.DetectDrift(ctx, desired, live)
		if err != nil {
			// Fall back to simple check if SSA dry-run fails
			logger.V(1).Info("SSA dry-run failed, using simple drift check",
				"error", err.Error(),
			)
			hasDrift = p.driftDetector.SimpleDriftCheck(desired, live)
		}
	} else {
		// Object doesn't exist - needs creation
		hasDrift = true
	}

	if !hasDrift {
		logger.V(1).Info("No drift detected, skipping apply",
			"name", assetMeta.Name,
		)
		return false, nil
	}

	// Step 6: Anti-thrashing gate
	resourceKey := throttling.MakeResourceKey(
		desired.GetNamespace(),
		desired.GetName(),
		desired.GetKind(),
	)

	if err := p.throttle.Record(resourceKey); err != nil {
		if throttling.IsThrottled(err) {
			logger.Info("Asset update throttled (anti-thrashing)",
				"name", assetMeta.Name,
				"key", resourceKey,
			)
			return false, err
		}
		return false, err
	}

	// Step 7: Apply via Server-Side Apply
	applied, err := p.applier.Apply(ctx, desired, true)
	if err != nil {
		return false, fmt.Errorf("failed to apply asset %s: %w", assetMeta.Name, err)
	}

	if applied {
		logger.Info("Successfully applied asset",
			"name", assetMeta.Name,
			"kind", desired.GetKind(),
			"namespace", desired.GetNamespace(),
			"objectName", desired.GetName(),
		)
	}

	return applied, nil
}

// ReconcileAssets reconciles multiple assets in order
func (p *Patcher) ReconcileAssets(ctx context.Context, assetMetas []assets.AssetMetadata, renderCtx *pkgcontext.RenderContext) (int, error) {
	appliedCount := 0
	var lastErr error

	for i := range assetMetas {
		applied, err := p.ReconcileAsset(ctx, &assetMetas[i], renderCtx)
		if err != nil {
			lastErr = err
			// Continue with other assets even if one fails
			log.FromContext(ctx).Error(err, "Failed to reconcile asset, continuing",
				"asset", assetMetas[i].Name,
			)
			continue
		}

		if applied {
			appliedCount++
		}
	}

	return appliedCount, lastErr
}
