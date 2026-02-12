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
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const (
	// DisabledResourcesAnnotation is the annotation key for root exclusion
	DisabledResourcesAnnotation = "platform.kubevirt.io/disabled-resources"
)

// ParseDisabledResources parses the disabled-resources annotation
// Format: "Kind/Name, Kind/Name, ..."
// Returns: map["Kind/Name"]bool for O(1) lookup
func ParseDisabledResources(annotation string) map[string]bool {
	disabled := make(map[string]bool)

	if annotation == "" {
		return disabled
	}

	// Split by comma and trim whitespace
	pairs := strings.Split(annotation, ",")
	for _, pair := range pairs {
		trimmed := strings.TrimSpace(pair)
		if trimmed != "" {
			disabled[trimmed] = true
		}
	}

	return disabled
}

// FilterExcludedAssets removes disabled resources from asset list
// Returns a new slice with excluded assets removed
func FilterExcludedAssets(assets []*unstructured.Unstructured, disabledMap map[string]bool) []*unstructured.Unstructured {
	if len(disabledMap) == 0 {
		return assets // No filtering needed
	}

	filtered := make([]*unstructured.Unstructured, 0, len(assets))

	for _, asset := range assets {
		key := fmt.Sprintf("%s/%s", asset.GetKind(), asset.GetName())

		if disabledMap[key] {
			// Skip this asset - it's disabled
			continue
		}

		filtered = append(filtered, asset)
	}

	return filtered
}

// IsResourceExcluded checks if a specific resource is in the disabled map
func IsResourceExcluded(kind, name string, disabledMap map[string]bool) bool {
	if len(disabledMap) == 0 {
		return false
	}

	key := fmt.Sprintf("%s/%s", kind, name)
	return disabledMap[key]
}
