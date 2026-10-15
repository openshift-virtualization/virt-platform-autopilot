/*
Copyright 2026 The Virt Platform Autopilot Authors.

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
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// objectField reads a string field using literal path segments, including keys with dots.
func (r *Renderer) objectField(apiVersion, kind, namespace, name string, fieldPath ...string) (string, error) {
	if r.client == nil {
		return "", nil
	}
	object := &unstructured.Unstructured{}
	object.SetAPIVersion(apiVersion)
	object.SetKind(kind)
	key := client.ObjectKey{Namespace: namespace, Name: name}
	err := r.client.Get(context.Background(), key, object)
	if apierrors.IsNotFound(err) || meta.IsNoMatchError(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read %s %s: %w", kind, key, err)
	}
	value, _, err := unstructured.NestedString(object.Object, fieldPath...)
	if err != nil {
		return "", fmt.Errorf("read %s %s field %s: %w", kind, key, strings.Join(fieldPath, "."), err)
	}
	return value, nil
}

// nodeDisruptionPolicyReady reports whether MCO has validated the node
// disruption policy declared by the referenced MachineConfiguration asset. A
// nil reader is used for offline rendering, where no live status is available
// and emitting the asset is still useful.
func (r *Renderer) nodeDisruptionPolicyReady(policyAsset map[string]any) (bool, error) {
	if r.client == nil {
		return true, nil
	}

	policy := &unstructured.Unstructured{}
	policy.SetAPIVersion("operator.openshift.io/v1")
	policy.SetKind("MachineConfiguration")
	if err := r.client.Get(context.Background(), client.ObjectKey{Name: "cluster"}, policy); err != nil {
		if apierrors.IsNotFound(err) || meta.IsNoMatchError(err) {
			return false, nil
		}
		return false, fmt.Errorf("read MachineConfiguration cluster: %w", err)
	}

	want, found, err := unstructured.NestedMap(policyAsset, "spec", "nodeDisruptionPolicy")
	if err != nil {
		return false, fmt.Errorf("read requested node disruption policy asset: %w", err)
	}
	if !found {
		return false, fmt.Errorf("requested MachineConfiguration asset has no spec.nodeDisruptionPolicy")
	}

	status, found, err := unstructured.NestedMap(policy.Object, "status", "nodeDisruptionPolicyStatus", "clusterPolicies")
	if err != nil || !found {
		return false, err
	}

	hasRequestedEntries := false
	for _, policyType := range []struct {
		field, identifier string
	}{
		{field: "files", identifier: "path"},
		{field: "units", identifier: "name"},
	} {
		expected, found, err := unstructured.NestedSlice(want, policyType.field)
		if err != nil {
			return false, fmt.Errorf("read requested node disruption policy %s: %w", policyType.field, err)
		}
		if !found {
			continue
		}
		if len(expected) == 0 {
			return false, fmt.Errorf("requested node disruption policy %s is empty", policyType.field)
		}
		hasRequestedEntries = true
		actual, found, err := unstructured.NestedSlice(status, policyType.field)
		if err != nil || !found || !nodeDisruptionPolicyEntriesMatch(actual, expected, policyType.identifier) {
			return false, err
		}
	}
	if !hasRequestedEntries {
		return false, fmt.Errorf("requested node disruption policy has neither files nor units")
	}

	return true, nil
}

func nodeDisruptionPolicyEntriesMatch(actual, expected []any, identifierField string) bool {
	for _, expectedEntry := range expected {
		expectedMap, ok := expectedEntry.(map[string]any)
		if !ok {
			return false
		}
		identifier, found, err := unstructured.NestedString(expectedMap, identifierField)
		if err != nil || !found {
			return false
		}
		expectedActions, found, err := unstructured.NestedSlice(expectedMap, "actions")
		if err != nil || !found {
			return false
		}

		matched := false
		for _, actualEntry := range actual {
			actualMap, ok := actualEntry.(map[string]any)
			if !ok {
				continue
			}
			actualIdentifier, _, err := unstructured.NestedString(actualMap, identifierField)
			if err != nil || actualIdentifier != identifier {
				continue
			}
			actualActions, found, err := unstructured.NestedSlice(actualMap, "actions")
			matched = err == nil && found && reflect.DeepEqual(actualActions, expectedActions)
			break
		}
		if !matched {
			return false
		}
	}
	return true
}
