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

package assets

import (
	"fmt"
	"io/fs"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const (
	// TombstoneLabel is the required label for tombstone deletion safety
	TombstoneLabel = "platform.kubevirt.io/managed-by"
	// TombstoneLabelValue is the expected value for the tombstone label
	TombstoneLabelValue = "virt-platform-autopilot"
	// TombstonesDir is the directory containing tombstone files
	TombstonesDir = "tombstones"
)

// TombstoneMetadata represents a tombstoned resource to be deleted
type TombstoneMetadata struct {
	Path      string                     // Relative path in tombstones directory
	GVK       schema.GroupVersionKind    // Resource GVK
	Namespace string                     // Resource namespace (empty for cluster-scoped)
	Name      string                     // Resource name
	Object    *unstructured.Unstructured // Full object definition
}

// LoadTombstones scans the tombstones directory and loads all tombstone definitions
// Returns a slice of TombstoneMetadata for resources to be deleted
func (l *Loader) LoadTombstones() ([]TombstoneMetadata, error) {
	var tombstones []TombstoneMetadata

	// Walk the tombstones directory
	err := fs.WalkDir(l.fs, TombstonesDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// If tombstones directory doesn't exist, return empty list (not an error)
			if strings.Contains(err.Error(), "no such file or directory") ||
				strings.Contains(err.Error(), "file does not exist") {
				return nil
			}
			return err
		}

		// Skip directories
		if d.IsDir() {
			return nil
		}

		// Only process .yaml and .yaml.tpl files
		if !strings.HasSuffix(path, ".yaml") && !strings.HasSuffix(path, ".yaml.tpl") {
			return nil
		}

		// Load and parse tombstone file
		data, err := l.fs.ReadFile(path)
		if err != nil {
			return fmt.Errorf("failed to read tombstone file %s: %w", path, err)
		}

		// Parse YAML (tombstones should not be templates, but handle .tpl extension for consistency)
		objects, err := ParseMultiYAML(data)
		if err != nil {
			return fmt.Errorf("failed to parse tombstone file %s: %w", path, err)
		}

		// Process each object in the file
		for _, obj := range objects {
			// Validate required fields
			if err := validateTombstone(obj, path); err != nil {
				return err
			}

			// Extract metadata
			gvk := obj.GroupVersionKind()
			namespace := obj.GetNamespace()
			name := obj.GetName()

			tombstones = append(tombstones, TombstoneMetadata{
				Path:      path,
				GVK:       gvk,
				Namespace: namespace,
				Name:      name,
				Object:    obj,
			})
		}

		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to load tombstones: %w", err)
	}

	return tombstones, nil
}

// validateTombstone validates that a tombstone object has all required fields
func validateTombstone(obj *unstructured.Unstructured, path string) error {
	// Check Kind
	if obj.GetKind() == "" {
		return fmt.Errorf("tombstone %s missing required field: kind", path)
	}

	// Check APIVersion
	if obj.GetAPIVersion() == "" {
		return fmt.Errorf("tombstone %s missing required field: apiVersion", path)
	}

	// Check Name
	if obj.GetName() == "" {
		return fmt.Errorf("tombstone %s missing required field: metadata.name", path)
	}

	// Check for required label (safety check)
	labels := obj.GetLabels()
	if labels == nil {
		return fmt.Errorf("tombstone %s missing required label %s=%s (safety check)",
			path, TombstoneLabel, TombstoneLabelValue)
	}

	if labels[TombstoneLabel] != TombstoneLabelValue {
		return fmt.Errorf("tombstone %s has incorrect label value for %s: expected %s, got %s (safety check)",
			path, TombstoneLabel, TombstoneLabelValue, labels[TombstoneLabel])
	}

	return nil
}
