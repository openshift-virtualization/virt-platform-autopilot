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
	// AnnotationPatch is the annotation key for RFC 6902 JSON Patch
	AnnotationPatch = "platform.kubevirt.io/patch"
)

// ApplyJSONPatch applies a JSON Patch from annotation to an unstructured object
// Returns the patched object or the original if no patch annotation exists
func ApplyJSONPatch(obj *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	if obj == nil {
		return nil, fmt.Errorf("object is nil")
	}

	annotations := obj.GetAnnotations()
	if annotations == nil {
		return obj, nil
	}

	patchStr, exists := annotations[AnnotationPatch]
	if !exists || patchStr == "" {
		return obj, nil
	}

	// Validate patch is valid JSON
	if !json.Valid([]byte(patchStr)) {
		return nil, fmt.Errorf("invalid JSON in patch annotation: %s", patchStr)
	}

	// Convert object to JSON
	objJSON, err := json.Marshal(obj.Object)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal object: %w", err)
	}

	// Parse the patch
	patch, err := jsonpatch.DecodePatch([]byte(patchStr))
	if err != nil {
		return nil, fmt.Errorf("failed to decode JSON patch: %w", err)
	}

	// Apply the patch
	patchedJSON, err := patch.Apply(objJSON)
	if err != nil {
		return nil, fmt.Errorf("failed to apply JSON patch: %w", err)
	}

	// Unmarshal back to unstructured
	patchedObj := &unstructured.Unstructured{}
	if err := json.Unmarshal(patchedJSON, &patchedObj.Object); err != nil {
		return nil, fmt.Errorf("failed to unmarshal patched object: %w", err)
	}

	return patchedObj, nil
}

// ValidatePatch validates a JSON Patch string
func ValidatePatch(patchStr string) error {
	if patchStr == "" {
		return nil
	}

	if !json.Valid([]byte(patchStr)) {
		return fmt.Errorf("patch is not valid JSON")
	}

	_, err := jsonpatch.DecodePatch([]byte(patchStr))
	if err != nil {
		return fmt.Errorf("invalid JSON patch format: %w", err)
	}

	return nil
}
