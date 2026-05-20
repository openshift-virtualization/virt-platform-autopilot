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

package overrides

import (
	"encoding/json"
	"fmt"

	jsonpatch "github.com/evanphx/json-patch/v5"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const (
	// PatchAnnotation is the annotation key for RFC 6902 JSON Patch
	PatchAnnotation = "platform.kubevirt.io/patch"
)

// ApplyJSONPatch applies a RFC 6902 JSON Patch from the object's annotation
// The patch is applied in-memory to the provided object
// Returns true if a patch was applied, false if no patch annotation exists
func ApplyJSONPatch(obj *unstructured.Unstructured) (bool, error) {
	if obj == nil {
		return false, fmt.Errorf("object is nil")
	}

	annotations := obj.GetAnnotations()
	if annotations == nil {
		return false, nil
	}

	patchStr, exists := annotations[PatchAnnotation]
	if !exists || patchStr == "" {
		return false, nil
	}

	// Marshal current object to JSON
	originalJSON, err := json.Marshal(obj.Object)
	if err != nil {
		return false, fmt.Errorf("failed to marshal object to JSON: %w", err)
	}

	// Parse the patch
	patch, err := jsonpatch.DecodePatch([]byte(patchStr))
	if err != nil {
		return false, fmt.Errorf("invalid JSON Patch in annotation: %w", err)
	}

	// Apply the patch
	patchedJSON, err := patch.Apply(originalJSON)
	if err != nil {
		return false, fmt.Errorf("failed to apply JSON Patch: %w", err)
	}

	// Unmarshal back into the object
	var patchedObj map[string]any
	if err := json.Unmarshal(patchedJSON, &patchedObj); err != nil {
		return false, fmt.Errorf("failed to unmarshal patched JSON: %w", err)
	}

	// Update the object in-place
	obj.Object = patchedObj

	return true, nil
}

// ValidateJSONPatch validates that a JSON Patch string is valid RFC 6902 format
func ValidateJSONPatch(patchStr string) error {
	if patchStr == "" {
		return nil
	}

	_, err := jsonpatch.DecodePatch([]byte(patchStr))
	if err != nil {
		return fmt.Errorf("invalid JSON Patch: %w", err)
	}

	return nil
}
