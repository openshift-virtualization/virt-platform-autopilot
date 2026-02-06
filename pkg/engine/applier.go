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
)

const (
	// FieldManager is the field manager name used for SSA
	FieldManager = "virt-platform-operator"
)

// Applier handles Server-Side Apply operations
type Applier struct {
	client client.Client
}

// NewApplier creates a new SSA applier
func NewApplier(c client.Client) *Applier {
	return &Applier{
		client: c,
	}
}

// Apply applies an object using Server-Side Apply
// Returns true if the object was created/updated, false if unchanged
func (a *Applier) Apply(ctx context.Context, obj *unstructured.Unstructured, force bool) (bool, error) {
	logger := log.FromContext(ctx)

	if obj == nil {
		return false, fmt.Errorf("object is nil")
	}

	// Ensure object has required metadata
	if obj.GetName() == "" {
		return false, fmt.Errorf("object name is empty")
	}

	if obj.GetKind() == "" {
		return false, fmt.Errorf("object kind is empty")
	}

	// Create a copy to avoid modifying the input
	appliedObj := obj.DeepCopy()

	// Apply using SSA
	// Note: Using Patch with client.Apply for unstructured objects
	// The client.Apply() method requires typed ApplyConfiguration objects
	patchOptions := []client.PatchOption{
		client.ForceOwnership,
		client.FieldOwner(FieldManager),
	}

	logger.V(1).Info("Applying object",
		"kind", appliedObj.GetKind(),
		"namespace", appliedObj.GetNamespace(),
		"name", appliedObj.GetName(),
		"force", force,
	)

	//nolint:staticcheck // SA1019: client.Apply is deprecated but still needed for unstructured objects
	err := a.client.Patch(ctx, appliedObj, client.Apply, patchOptions...)
	if err != nil {
		if errors.IsConflict(err) {
			return false, fmt.Errorf("field ownership conflict (another controller owns fields): %w", err)
		}
		return false, fmt.Errorf("failed to apply object: %w", err)
	}

	logger.V(1).Info("Successfully applied object",
		"kind", appliedObj.GetKind(),
		"namespace", appliedObj.GetNamespace(),
		"name", appliedObj.GetName(),
	)

	return true, nil
}

// Delete deletes an object if it exists
func (a *Applier) Delete(ctx context.Context, obj *unstructured.Unstructured) error {
	logger := log.FromContext(ctx)

	if obj == nil {
		return fmt.Errorf("object is nil")
	}

	logger.V(1).Info("Deleting object",
		"kind", obj.GetKind(),
		"namespace", obj.GetNamespace(),
		"name", obj.GetName(),
	)

	err := a.client.Delete(ctx, obj)
	if err != nil {
		if errors.IsNotFound(err) {
			return nil // Already deleted
		}
		return fmt.Errorf("failed to delete object: %w", err)
	}

	logger.V(1).Info("Successfully deleted object",
		"kind", obj.GetKind(),
		"namespace", obj.GetNamespace(),
		"name", obj.GetName(),
	)

	return nil
}

// Get retrieves an object by key
func (a *Applier) Get(ctx context.Context, key client.ObjectKey, obj *unstructured.Unstructured) error {
	return a.client.Get(ctx, key, obj)
}
