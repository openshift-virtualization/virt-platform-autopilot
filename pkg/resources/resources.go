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
