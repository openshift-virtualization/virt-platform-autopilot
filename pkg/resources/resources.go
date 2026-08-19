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

// Package resources processes resources
package resources

import "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

// ExtractFeatureGates extracts feature gates from HCO v1 spec.
// In v1 the field is an array of {name: string, state: "Enabled"|"Disabled"} objects.
func ExtractFeatureGates(hco *unstructured.Unstructured) map[string]bool {
	gates := make(map[string]bool)

	featureGates, found, err := unstructured.NestedSlice(hco.Object, "spec", "featureGates")
	if err != nil || !found {
		return gates
	}

	for _, item := range featureGates {
		gate, ok := item.(map[string]any)
		if !ok {
			continue
		}
		name, _ := gate["name"].(string)
		state, _ := gate["state"].(string)
		if name != "" {
			gates[name] = (state != "Disabled")
		}
	}

	return gates
}

// ExtractKVFeatureGate extracts feature gates from KubeVirt v1 spec.
func ExtractKVFeatureGate(kv *unstructured.Unstructured) []string {
	if kv == nil || kv.Object == nil {
		return nil
	}

	featureGates, found, err := unstructured.NestedStringSlice(kv.Object, "spec", "configuration", "developerConfiguration", "featureGates")
	if err != nil || !found {
		return nil
	}

	return featureGates
}
