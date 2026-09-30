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
