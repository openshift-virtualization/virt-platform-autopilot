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
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const (
	// AnnotationIgnoreFields is the annotation key for RFC 6901 JSON Pointer field masking
	AnnotationIgnoreFields = "platform.kubevirt.io/ignore-fields"
)

// MaskIgnoredFields copies field values from live object to desired object
// for all fields specified in the ignore-fields annotation.
// This effectively yields control of those fields to the user.
func MaskIgnoredFields(desired, live *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	if desired == nil {
		return nil, fmt.Errorf("desired object is nil")
	}

	if live == nil {
		// No live object means nothing to mask
		return desired, nil
	}

	annotations := live.GetAnnotations()
	if annotations == nil {
		return desired, nil
	}

	ignoreFields, exists := annotations[AnnotationIgnoreFields]
	if !exists || ignoreFields == "" {
		return desired, nil
	}

	// Parse comma-separated JSON pointers
	pointers := parsePointers(ignoreFields)

	// Create a copy of desired to modify
	result := desired.DeepCopy()

	// For each pointer, copy the value from live to result
	for _, pointer := range pointers {
		if err := copyFieldByPointer(live, result, pointer); err != nil {
			return nil, fmt.Errorf("failed to mask field %s: %w", pointer, err)
		}
	}

	return result, nil
}

// parsePointers splits comma-separated JSON pointers and trims whitespace
func parsePointers(pointers string) []string {
	parts := strings.Split(pointers, ",")
	result := make([]string, 0, len(parts))

	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}

	return result
}

// copyFieldByPointer copies a field value from source to dest using a JSON pointer
func copyFieldByPointer(source, dest *unstructured.Unstructured, pointer string) error {
	// JSON pointer must start with /
	if !strings.HasPrefix(pointer, "/") {
		return fmt.Errorf("JSON pointer must start with /: %s", pointer)
	}

	// Split pointer into path segments
	path := strings.Split(strings.TrimPrefix(pointer, "/"), "/")

	// Get value from source
	value, found, err := unstructured.NestedFieldCopy(source.Object, path...)
	if err != nil {
		return fmt.Errorf("failed to get field from source: %w", err)
	}

	if !found {
		// Field doesn't exist in source, remove it from dest if present
		unstructured.RemoveNestedField(dest.Object, path...)
		return nil
	}

	// Set value in dest
	if err := unstructured.SetNestedField(dest.Object, value, path...); err != nil {
		return fmt.Errorf("failed to set field in dest: %w", err)
	}

	return nil
}

// ValidatePointers validates a comma-separated list of JSON pointers
func ValidatePointers(pointers string) error {
	if pointers == "" {
		return nil
	}

	for _, pointer := range parsePointers(pointers) {
		if !strings.HasPrefix(pointer, "/") {
			return fmt.Errorf("invalid JSON pointer (must start with /): %s", pointer)
		}

		// Check for invalid escape sequences
		if strings.Contains(pointer, "~") {
			// ~0 represents ~ and ~1 represents /
			if !strings.Contains(pointer, "~0") && !strings.Contains(pointer, "~1") {
				return fmt.Errorf("invalid escape sequence in JSON pointer: %s", pointer)
			}
		}
	}

	return nil
}
