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
	"reflect"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// DriftDetector detects configuration drift using SSA dry-run
type DriftDetector struct {
	client client.Client
}

// NewDriftDetector creates a new drift detector
func NewDriftDetector(c client.Client) *DriftDetector {
	return &DriftDetector{
		client: c,
	}
}

// DetectDrift checks if applying desired would change live object
// Uses SSA dry-run to accurately detect drift
func (d *DriftDetector) DetectDrift(ctx context.Context, desired, live *unstructured.Unstructured) (bool, error) {
	logger := log.FromContext(ctx)

	if desired == nil {
		return false, fmt.Errorf("desired object is nil")
	}

	// If live object doesn't exist, there's drift (needs creation)
	if live == nil {
		return true, nil
	}

	// Perform SSA dry-run to see what would change
	dryRunObj := desired.DeepCopy()

	// Note: Using Patch with client.Apply for unstructured objects
	// The client.Apply() method requires typed ApplyConfiguration objects
	patchOptions := []client.PatchOption{
		client.DryRunAll,
		client.ForceOwnership,
		client.FieldOwner(FieldManager),
	}

	//nolint:staticcheck // SA1019: client.Apply is deprecated but still needed for unstructured objects
	err := d.client.Patch(ctx, dryRunObj, client.Apply, patchOptions...)
	if err != nil {
		return false, fmt.Errorf("failed to perform dry-run apply: %w", err)
	}

	// Sanitize both objects for comparison (remove runtime fields)
	sanitizedDryRun := sanitizeObject(dryRunObj)
	sanitizedLive := sanitizeObject(live)

	// Compare the objects
	hasDrift := !equality.Semantic.DeepEqual(sanitizedDryRun, sanitizedLive)

	if hasDrift {
		logger.V(1).Info("Drift detected",
			"kind", desired.GetKind(),
			"namespace", desired.GetNamespace(),
			"name", desired.GetName(),
		)
	}

	return hasDrift, nil
}

// SimpleDriftCheck performs a simple comparison without SSA dry-run
// This is faster but less accurate than DetectDrift
func (d *DriftDetector) SimpleDriftCheck(desired, live *unstructured.Unstructured) bool {
	if desired == nil || live == nil {
		return true
	}

	// Sanitize both objects
	sanitizedDesired := sanitizeObject(desired)
	sanitizedLive := sanitizeObject(live)

	// Simple semantic equality check
	return !equality.Semantic.DeepEqual(sanitizedDesired, sanitizedLive)
}

// sanitizeObject removes fields that should not be compared for drift
func sanitizeObject(obj *unstructured.Unstructured) map[string]interface{} {
	if obj == nil {
		return nil
	}

	sanitized := obj.DeepCopy().Object

	// Remove metadata fields that change on every update
	if metadata, ok := sanitized["metadata"].(map[string]interface{}); ok {
		// Keep: name, namespace, labels, annotations
		// Remove: resourceVersion, generation, uid, creationTimestamp, managedFields, etc.
		fieldsToKeep := map[string]bool{
			"name":        true,
			"namespace":   true,
			"labels":      true,
			"annotations": true,
		}

		sanitizedMetadata := make(map[string]interface{})
		for key, value := range metadata {
			if fieldsToKeep[key] {
				sanitizedMetadata[key] = value
			}
		}

		sanitized["metadata"] = sanitizedMetadata
	}

	// Remove status field (managed by controllers, not declarative)
	delete(sanitized, "status")

	return sanitized
}

// CompareSpecs compares only the spec sections of two objects
func CompareSpecs(obj1, obj2 *unstructured.Unstructured) bool {
	if obj1 == nil || obj2 == nil {
		return obj1 == obj2
	}

	spec1, found1, _ := unstructured.NestedFieldCopy(obj1.Object, "spec")
	spec2, found2, _ := unstructured.NestedFieldCopy(obj2.Object, "spec")

	if found1 != found2 {
		return false
	}

	if !found1 {
		return true // Both have no spec
	}

	return reflect.DeepEqual(spec1, spec2)
}
