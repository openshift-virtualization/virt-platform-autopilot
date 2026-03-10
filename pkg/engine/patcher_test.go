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
	"testing"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestIsNamespaceNotFound(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "namespace 404 is recognized",
			err:  k8serrors.NewNotFound(schema.GroupResource{Resource: "namespaces"}, "openshift-kube-descheduler-operator"),
			want: true,
		},
		{
			name: "resource-level 404 is not a namespace 404",
			err:  k8serrors.NewNotFound(schema.GroupResource{Group: "operator.openshift.io", Resource: "kubedeschedulers"}, "cluster"),
			want: false,
		},
		{
			name: "configmap 404 is not a namespace 404",
			err:  k8serrors.NewNotFound(schema.GroupResource{Resource: "configmaps"}, "my-config"),
			want: false,
		},
		{
			name: "conflict error is not namespace 404",
			err:  k8serrors.NewConflict(schema.GroupResource{Resource: "kubedeschedulers"}, "cluster", fmt.Errorf("conflict")),
			want: false,
		},
		{
			name: "plain error with namespace-like message is not a namespace 404",
			err:  fmt.Errorf(`namespaces "foo" not found`),
			want: false, // IsNotFound returns false for plain errors
		},
		{
			name: "forbidden is not namespace 404",
			err:  k8serrors.NewForbidden(schema.GroupResource{Resource: "namespaces"}, "foo", fmt.Errorf("forbidden")),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isNamespaceNotFound(tt.err); got != tt.want {
				t.Errorf("isNamespaceNotFound() = %v, want %v (err: %v)", got, tt.want, tt.err)
			}
		})
	}
}

func TestCountJSONPatchOperations(t *testing.T) {
	tests := []struct {
		name     string
		patchStr string
		want     int
	}{
		{
			name:     "single operation",
			patchStr: `[{"op": "add", "path": "/spec/replicas", "value": 3}]`,
			want:     1,
		},
		{
			name: "multiple operations",
			patchStr: `[
				{"op": "add", "path": "/spec/replicas", "value": 3},
				{"op": "replace", "path": "/spec/image", "value": "nginx:latest"},
				{"op": "remove", "path": "/spec/nodeSelector"}
			]`,
			want: 3,
		},
		{
			name:     "empty patch array",
			patchStr: `[]`,
			want:     0,
		},
		{
			name:     "invalid JSON",
			patchStr: `invalid json`,
			want:     0,
		},
		{
			name:     "not an array",
			patchStr: `{"op": "add"}`,
			want:     0,
		},
		{
			name:     "empty string",
			patchStr: ``,
			want:     0,
		},
		{
			name: "complex patch with nested values",
			patchStr: `[
				{"op": "add", "path": "/spec/template/spec/containers/0", "value": {"name": "nginx", "image": "nginx:latest"}},
				{"op": "add", "path": "/metadata/labels/app", "value": "web"}
			]`,
			want: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := countJSONPatchOperations(tt.patchStr)
			if got != tt.want {
				t.Errorf("countJSONPatchOperations() = %v, want %v", got, tt.want)
			}
		})
	}
}
