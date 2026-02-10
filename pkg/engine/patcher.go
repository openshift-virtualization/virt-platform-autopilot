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
	"encoding/json"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/kubevirt/virt-platform-operator/pkg/assets"
	pkgcontext "github.com/kubevirt/virt-platform-operator/pkg/context"
	"github.com/kubevirt/virt-platform-operator/pkg/observability"
	"github.com/kubevirt/virt-platform-operator/pkg/overrides"
	"github.com/kubevirt/virt-platform-operator/pkg/throttling"
	"github.com/kubevirt/virt-platform-operator/pkg/util"
)

// Patcher implements the Patched Baseline algorithm
type Patcher struct {
	renderer      *Renderer
	applier       *Applier
	driftDetector *DriftDetector
	throttle      *throttling.TokenBucket
	client        client.Client
	eventRecorder *util.EventRecorder
}

// NewPatcher creates a new patcher
// The apiReader enables object adoption (detecting and labeling unlabeled objects)
// For tests with fake clients, pass nil for apiReader
func NewPatcher(c client.Client, apiReader client.Reader, loader *assets.Loader) *Patcher {
	return &Patcher{
		renderer:      NewRenderer(loader),
		applier:       NewApplier(c, apiReader),
		driftDetector: NewDriftDetector(c),
		throttle:      throttling.NewTokenBucket(),
		client:        c,
	}
}

// SetEventRecorder sets the event recorder for this patcher
func (p *Patcher) SetEventRecorder(recorder *util.EventRecorder) {
	p.eventRecorder = recorder
}

// ReconcileAsset performs the full Patched Baseline algorithm for an asset
// Returns true if the asset was applied, false if skipped/unchanged
//
//nolint:gocognit // This function implements the 7-step Patched Baseline Algorithm which is inherently complex
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

	// Start reconciliation duration timer (will be observed at function exit)
	timer := observability.ReconcileDurationTimer(desired)
	defer timer.ObserveDuration()

	// Get live object from cluster
	// First try cached Get, then fall back to direct API call for adoption scenarios
	live := &unstructured.Unstructured{}
	live.SetGroupVersionKind(desired.GroupVersionKind())
	objKey := client.ObjectKey{
		Namespace: desired.GetNamespace(),
		Name:      desired.GetName(),
	}

	err = p.applier.Get(ctx, objKey, live)
	liveExists := err == nil

	if errors.IsNotFound(err) {
		// Object not found in cache - might be unlabeled and filtered out
		// Try direct API call to check if it exists but lacks managed-by label
		directLive := &unstructured.Unstructured{}
		directLive.SetGroupVersionKind(desired.GroupVersionKind())
		directErr := p.applier.GetDirect(ctx, objKey, directLive)

		if directErr == nil {
			// Object exists but was not in cache (probably unlabeled)
			// We'll adopt it by adding the label during Apply
			logger.V(1).Info("Adopting unlabeled object",
				"kind", desired.GetKind(),
				"namespace", desired.GetNamespace(),
				"name", desired.GetName(),
			)
			live = directLive
			liveExists = true
		} else if !errors.IsNotFound(directErr) {
			// Some other error occurred
			return false, fmt.Errorf("failed to get live object: %w", directErr)
		}
		// If directErr is NotFound, object truly doesn't exist
	} else if err != nil {
		// Some other error occurred during cached Get
		return false, fmt.Errorf("failed to get live object: %w", err)
	}

	// Log if we need to re-label an existing object
	if liveExists && !HasManagedByLabel(live) {
		logger.V(1).Info("Re-labeling object with managed-by label",
			"kind", desired.GetKind(),
			"namespace", desired.GetNamespace(),
			"name", desired.GetName(),
		)
	}

	// Step 2: Check opt-out annotation (mode: unmanaged)
	if liveExists && overrides.IsUnmanaged(live) {
		logger.V(1).Info("Asset is unmanaged, skipping",
			"name", assetMeta.Name,
			"kind", desired.GetKind(),
			"namespace", desired.GetNamespace(),
			"objectName", desired.GetName(),
		)
		// Track unmanaged customization
		observability.SetCustomization(desired, "unmanaged")

		// Record event about unmanaged mode
		if p.eventRecorder != nil && renderCtx.HCO != nil {
			p.eventRecorder.UnmanagedMode(renderCtx.HCO, desired.GetKind(), desired.GetNamespace(), desired.GetName())
		}
		return false, nil
	}

	// Step 3: Apply user patch (in-memory) → Modified State
	// Copy patch annotation from live to desired, then apply it
	if liveExists {
		liveAnnotations := live.GetAnnotations()
		if patchStr, exists := liveAnnotations[overrides.PatchAnnotation]; exists && patchStr != "" {
			// Track patch customization
			observability.SetCustomization(desired, "patch")

			// Copy patch annotation to desired temporarily
			desiredAnnotations := desired.GetAnnotations()
			if desiredAnnotations == nil {
				desiredAnnotations = make(map[string]string)
			}
			desiredAnnotations[overrides.PatchAnnotation] = patchStr
			desired.SetAnnotations(desiredAnnotations)

			// Apply the patch (modifies desired in-place)
			patched, err := overrides.ApplyJSONPatch(desired)
			if err != nil {
				logger.Error(err, "Failed to apply JSON patch, using desired without patch",
					"name", assetMeta.Name,
				)
				// Record event about invalid patch
				if p.eventRecorder != nil && renderCtx.HCO != nil {
					p.eventRecorder.InvalidPatch(renderCtx.HCO, desired.GetKind(), desired.GetNamespace(), desired.GetName(), err.Error())
				}
				// Continue with unpatched desired (don't fail reconciliation)
			} else if patched && p.eventRecorder != nil && renderCtx.HCO != nil {
				// Record successful patch application
				// Count operations in the patch string
				operations := countJSONPatchOperations(patchStr)
				p.eventRecorder.PatchApplied(renderCtx.HCO, desired.GetKind(), desired.GetNamespace(), desired.GetName(), operations)
			}
		}
	}

	// Step 4: Mask ignored fields → Effective Desired State
	if liveExists {
		// Check if ignore-fields annotation exists
		liveAnnotations := live.GetAnnotations()
		if _, exists := liveAnnotations[overrides.AnnotationIgnoreFields]; exists {
			// Track ignore-fields customization
			observability.SetCustomization(desired, "ignore")
		}

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
		// Optionally record no-drift event (commented to avoid spam)
		// if p.eventRecorder != nil && renderCtx.HCO != nil {
		// 	p.eventRecorder.NoDriftDetected(renderCtx.HCO, desired.GetKind(), desired.GetNamespace(), desired.GetName())
		// }
		return false, nil
	}

	// Record drift detection (only when drift is found)
	if liveExists && p.eventRecorder != nil && renderCtx.HCO != nil {
		p.eventRecorder.DriftDetected(renderCtx.HCO, desired.GetKind(), desired.GetNamespace(), desired.GetName())
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
			// Increment thrashing counter metric
			observability.IncThrashing(desired)

			// Record throttling event
			if p.eventRecorder != nil && renderCtx.HCO != nil {
				throttledErr := err.(*throttling.ThrottledError)
				window := throttledErr.Window.String()
				p.eventRecorder.Throttled(renderCtx.HCO, desired.GetKind(), desired.GetNamespace(), desired.GetName(), throttledErr.Capacity, window)
			}
			return false, err
		}
		return false, err
	}

	// Step 7: Apply via Server-Side Apply
	applied, err := p.applier.Apply(ctx, desired, true)
	if err != nil {
		// Set compliance status to failed (0)
		observability.SetCompliance(desired, 0)

		// Record apply failure event
		if p.eventRecorder != nil && renderCtx.HCO != nil {
			p.eventRecorder.ApplyFailed(renderCtx.HCO, assetMeta.Name, err.Error())
		}
		return false, fmt.Errorf("failed to apply asset %s: %w", assetMeta.Name, err)
	}

	if applied {
		logger.Info("Successfully applied asset",
			"name", assetMeta.Name,
			"kind", desired.GetKind(),
			"namespace", desired.GetNamespace(),
			"objectName", desired.GetName(),
		)
		// Set compliance status to synced (1)
		observability.SetCompliance(desired, 1)

		// Record successful asset application
		if p.eventRecorder != nil && renderCtx.HCO != nil {
			p.eventRecorder.AssetApplied(renderCtx.HCO, assetMeta.Name, desired.GetKind(), desired.GetNamespace(), desired.GetName())
		}
		// Also record drift correction since we just fixed it
		if liveExists && p.eventRecorder != nil && renderCtx.HCO != nil {
			p.eventRecorder.DriftCorrected(renderCtx.HCO, desired.GetKind(), desired.GetNamespace(), desired.GetName())
		}
	} else {
		// No drift detected or skipped - still compliant
		observability.SetCompliance(desired, 1)
	}

	return applied, nil
}

// ReconcileAssets reconciles multiple assets in order
func (p *Patcher) ReconcileAssets(ctx context.Context, assetMetas []assets.AssetMetadata, renderCtx *pkgcontext.RenderContext) (int, error) {
	appliedCount := 0
	var failedAssets []string
	var errors []error

	for i := range assetMetas {
		applied, err := p.ReconcileAsset(ctx, &assetMetas[i], renderCtx)
		if err != nil {
			// Collect error and failed asset name
			errors = append(errors, err)
			failedAssets = append(failedAssets, assetMetas[i].Name)

			// Continue with other assets even if one fails
			log.FromContext(ctx).Error(err, "Failed to reconcile asset, continuing with others",
				"asset", assetMetas[i].Name,
				"failedSoFar", len(failedAssets),
			)
			continue
		}

		if applied {
			appliedCount++
		}
	}

	// Return aggregated error if any assets failed
	// This ensures reconciliation fails and retries, but only after attempting all assets
	if len(errors) > 0 {
		// Build detailed error message with all failures
		var errMsgs []string
		for i, err := range errors {
			errMsgs = append(errMsgs, fmt.Sprintf("[%s: %v]", failedAssets[i], err))
		}

		return appliedCount, fmt.Errorf("failed to reconcile %d/%d assets: %s",
			len(failedAssets),
			len(assetMetas),
			strings.Join(errMsgs, "; "),
		)
	}

	return appliedCount, nil
}

// countJSONPatchOperations counts the number of operations in a JSON patch string
// Returns the count or 0 if parsing fails
func countJSONPatchOperations(patchStr string) int {
	var patch []map[string]interface{}
	if err := json.Unmarshal([]byte(patchStr), &patch); err != nil {
		return 0
	}
	return len(patch)
}
