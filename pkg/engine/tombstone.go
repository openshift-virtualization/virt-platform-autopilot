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

	"github.com/kubevirt/virt-platform-autopilot/pkg/assets"
	"github.com/kubevirt/virt-platform-autopilot/pkg/observability"
	"github.com/kubevirt/virt-platform-autopilot/pkg/util"
)

// TombstoneReconciler handles cleanup of tombstoned resources
type TombstoneReconciler struct {
	client        client.Client
	loader        *assets.Loader
	eventRecorder *util.EventRecorder
}

// NewTombstoneReconciler creates a new tombstone reconciler
func NewTombstoneReconciler(client client.Client, loader *assets.Loader) *TombstoneReconciler {
	return &TombstoneReconciler{
		client: client,
		loader: loader,
	}
}

// SetEventRecorder sets the event recorder for tombstone events
func (r *TombstoneReconciler) SetEventRecorder(recorder *util.EventRecorder) {
	r.eventRecorder = recorder
}

// ReconcileTombstones processes all tombstones and deletes matching resources
// Returns the number of successfully deleted resources and any errors encountered
// Uses best-effort error handling - continues processing even if some deletions fail
func (r *TombstoneReconciler) ReconcileTombstones(ctx context.Context, hco *unstructured.Unstructured) (int, error) {
	logger := log.FromContext(ctx)

	// Load tombstones from embedded filesystem
	tombstones, err := r.loader.LoadTombstones()
	if err != nil {
		return 0, fmt.Errorf("failed to load tombstones: %w", err)
	}

	if len(tombstones) == 0 {
		logger.V(1).Info("No tombstones found - skipping cleanup phase")
		return 0, nil
	}

	logger.Info("Processing tombstones", "count", len(tombstones))

	deletedCount := 0
	var aggregatedErrors []error

	// Process each tombstone
	for _, ts := range tombstones {
		deleted, err := r.reconcileTombstone(ctx, ts, hco)
		if err != nil {
			// Log error but continue processing remaining tombstones (best-effort)
			logger.Error(err, "Failed to process tombstone",
				"kind", ts.GVK.Kind,
				"name", ts.Name,
				"namespace", ts.Namespace,
				"path", ts.Path)
			aggregatedErrors = append(aggregatedErrors, err)
			continue
		}

		if deleted {
			deletedCount++
		}
	}

	// Return aggregated errors if any occurred
	if len(aggregatedErrors) > 0 {
		return deletedCount, fmt.Errorf("tombstone processing completed with %d errors (see logs for details)", len(aggregatedErrors))
	}

	logger.Info("Tombstone processing completed", "deleted", deletedCount, "total", len(tombstones))
	return deletedCount, nil
}

// reconcileTombstone processes a single tombstone and attempts deletion
// Returns true if the resource was deleted, false if skipped (NotFound or label mismatch)
func (r *TombstoneReconciler) reconcileTombstone(ctx context.Context, ts assets.TombstoneMetadata, hco *unstructured.Unstructured) (bool, error) {
	logger := log.FromContext(ctx)

	// Create object key for lookup
	objKey := client.ObjectKey{
		Name:      ts.Name,
		Namespace: ts.Namespace,
	}

	// Get current state from cluster
	live := &unstructured.Unstructured{}
	live.SetGroupVersionKind(ts.GVK)

	err := r.client.Get(ctx, objKey, live)
	if err != nil {
		if errors.IsNotFound(err) {
			// Resource already deleted - success (idempotent)
			logger.V(1).Info("Tombstone resource already deleted",
				"kind", ts.GVK.Kind,
				"name", ts.Name,
				"namespace", ts.Namespace)

			// Set metric to deleted
			observability.SetTombstoneStatus(ts.Object, observability.TombstoneDeleted)

			return false, nil
		}

		// Other error (permission, API, etc.)
		observability.SetTombstoneStatus(ts.Object, observability.TombstoneError)

		if r.eventRecorder != nil {
			r.eventRecorder.TombstoneFailed(hco, ts.GVK.Kind, ts.Namespace, ts.Name,
				fmt.Sprintf("Failed to get resource: %v", err))
		}

		return false, fmt.Errorf("failed to get resource: %w", err)
	}

	// SAFETY CHECK: Verify ownership label
	labels := live.GetLabels()
	if labels == nil || labels[assets.TombstoneLabel] != assets.TombstoneLabelValue {
		// Resource exists but doesn't have our management label - skip deletion
		logger.Info("Skipping tombstone deletion - label mismatch (safety check)",
			"kind", ts.GVK.Kind,
			"name", ts.Name,
			"namespace", ts.Namespace,
			"expected_label", fmt.Sprintf("%s=%s", assets.TombstoneLabel, assets.TombstoneLabelValue),
			"actual_labels", labels)

		// Set metric to skipped
		observability.SetTombstoneStatus(ts.Object, observability.TombstoneSkipped)

		if r.eventRecorder != nil {
			r.eventRecorder.TombstoneSkipped(hco, ts.GVK.Kind, ts.Namespace, ts.Name,
				"Label mismatch - resource not managed by virt-platform-autopilot")
		}

		return false, nil
	}

	// Delete the resource
	logger.Info("Deleting tombstoned resource",
		"kind", ts.GVK.Kind,
		"name", ts.Name,
		"namespace", ts.Namespace,
		"path", ts.Path)

	err = r.client.Delete(ctx, live)
	if err != nil {
		// Deletion failed
		observability.SetTombstoneStatus(ts.Object, observability.TombstoneError)

		if r.eventRecorder != nil {
			r.eventRecorder.TombstoneFailed(hco, ts.GVK.Kind, ts.Namespace, ts.Name,
				fmt.Sprintf("Failed to delete: %v", err))
		}

		return false, fmt.Errorf("failed to delete resource: %w", err)
	}

	// Deletion succeeded
	observability.SetTombstoneStatus(ts.Object, observability.TombstoneDeleted)

	if r.eventRecorder != nil {
		r.eventRecorder.TombstoneDeleted(hco, ts.GVK.Kind, ts.Namespace, ts.Name, ts.Path)
	}

	logger.Info("Successfully deleted tombstoned resource",
		"kind", ts.GVK.Kind,
		"name", ts.Name,
		"namespace", ts.Namespace)

	return true, nil
}
