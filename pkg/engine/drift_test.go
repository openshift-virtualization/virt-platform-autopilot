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
	"context"
	"fmt"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// errApplyClient wraps a fake client and overrides Apply to simulate webhook/TLS failures.
type errApplyClient struct {
	client.Client
	applyErr error
}

func (c *errApplyClient) Apply(_ context.Context, _ runtime.ApplyConfiguration, _ ...client.ApplyOption) error {
	return c.applyErr
}

func makeObj(labels map[string]string, spec map[string]interface{}) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata": map[string]interface{}{
				"name":      "test",
				"namespace": "default",
			},
		},
	}
	if labels != nil {
		obj.SetLabels(labels)
	}
	if spec != nil {
		obj.Object["spec"] = spec
	}
	return obj
}

func TestSimpleDriftCheck(t *testing.T) {
	dd := &DriftDetector{} // no client needed for SimpleDriftCheck

	tests := []struct {
		name    string
		desired *unstructured.Unstructured
		live    *unstructured.Unstructured
		want    bool // true = drift detected
	}{
		{
			name:    "identical objects, no labels",
			desired: makeObj(nil, map[string]interface{}{"key": "value"}),
			live:    makeObj(nil, map[string]interface{}{"key": "value"}),
			want:    false,
		},
		{
			name:    "both have managed-by label, spec equal",
			desired: makeObj(map[string]string{ManagedByLabel: ManagedByValue}, map[string]interface{}{"key": "value"}),
			live:    makeObj(map[string]string{ManagedByLabel: ManagedByValue}, map[string]interface{}{"key": "value"}),
			want:    false,
		},
		{
			name:    "spec differs",
			desired: makeObj(map[string]string{ManagedByLabel: ManagedByValue}, map[string]interface{}{"key": "a"}),
			live:    makeObj(map[string]string{ManagedByLabel: ManagedByValue}, map[string]interface{}{"key": "b"}),
			want:    true,
		},
		{
			// Regression: templates do not emit the managed-by label; Applier.Apply()
			// injects it just before the SSA call. If ReconcileAsset does not call
			// ensureManagedByLabel(desired) before drift detection, the label that is
			// always present on the live object will show up as spurious drift on
			// every reconciliation cycle, causing unnecessary applies and events.
			name:    "regression: managed-by label only on live causes false drift when desired not labeled",
			desired: makeObj(nil, map[string]interface{}{"key": "value"}),
			live:    makeObj(map[string]string{ManagedByLabel: ManagedByValue}, map[string]interface{}{"key": "value"}),
			want:    true, // this is the bug: drift is wrongly detected
		},
		{
			// Mirror of the regression case above: after ensureManagedByLabel(desired)
			// is called in ReconcileAsset (the fix), the comparison is fair and no
			// spurious drift is reported.
			name: "fix: no spurious drift when desired is labeled before comparison",
			desired: func() *unstructured.Unstructured {
				obj := makeObj(nil, map[string]interface{}{"key": "value"})
				ensureManagedByLabel(obj) // what ReconcileAsset now does before drift check
				return obj
			}(),
			live: makeObj(map[string]string{ManagedByLabel: ManagedByValue}, map[string]interface{}{"key": "value"}),
			want: false,
		},
		{
			name:    "nil desired",
			desired: nil,
			live:    makeObj(nil, nil),
			want:    true,
		},
		{
			name:    "nil live",
			desired: makeObj(nil, nil),
			live:    nil,
			want:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := dd.SimpleDriftCheck(tt.desired, tt.live)
			if got != tt.want {
				t.Errorf("SimpleDriftCheck() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCompareSpecs(t *testing.T) {
	tests := []struct {
		name string
		obj1 *unstructured.Unstructured
		obj2 *unstructured.Unstructured
		want bool
	}{
		{
			name: "both nil",
			obj1: nil,
			obj2: nil,
			want: true,
		},
		{
			name: "first nil",
			obj1: nil,
			obj2: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"spec": map[string]interface{}{"replicas": 3},
				},
			},
			want: false,
		},
		{
			name: "second nil",
			obj1: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"spec": map[string]interface{}{"replicas": 3},
				},
			},
			obj2: nil,
			want: false,
		},
		{
			name: "identical specs",
			obj1: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"spec": map[string]interface{}{
						"replicas": int64(3),
						"selector": map[string]interface{}{"app": "test"},
					},
				},
			},
			obj2: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"spec": map[string]interface{}{
						"replicas": int64(3),
						"selector": map[string]interface{}{"app": "test"},
					},
				},
			},
			want: true,
		},
		{
			name: "different specs",
			obj1: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"spec": map[string]interface{}{"replicas": int64(3)},
				},
			},
			obj2: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"spec": map[string]interface{}{"replicas": int64(5)},
				},
			},
			want: false,
		},
		{
			name: "both have no spec",
			obj1: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{"name": "test"},
				},
			},
			obj2: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{"name": "test"},
				},
			},
			want: true,
		},
		{
			name: "only first has spec",
			obj1: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"spec": map[string]interface{}{"replicas": int64(3)},
				},
			},
			obj2: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{"name": "test"},
				},
			},
			want: false,
		},
		{
			name: "only second has spec",
			obj1: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{"name": "test"},
				},
			},
			obj2: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"spec": map[string]interface{}{"replicas": int64(3)},
				},
			},
			want: false,
		},
		{
			name: "metadata differs but specs identical",
			obj1: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{"name": "test1"},
					"spec":     map[string]interface{}{"replicas": int64(3)},
				},
			},
			obj2: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{"name": "test2"},
					"spec":     map[string]interface{}{"replicas": int64(3)},
				},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CompareSpecs(tt.obj1, tt.obj2)
			if got != tt.want {
				t.Errorf("CompareSpecs() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestDetectDriftPropagatesClientError verifies that DetectDrift surfaces errors from
// the underlying client (e.g. a webhook TLS failure) rather than silently swallowing
// them. This is important because the Patcher must propagate the error so that
// controller-runtime backs off and retries — not fall back to SimpleDriftCheck,
// which would produce a false-positive drift for any object with webhook-defaulted fields.
func TestDetectDriftPropagatesClientError(t *testing.T) {
	applyErr := fmt.Errorf("failed to perform dry-run apply: Internal error occurred: " +
		"failed calling webhook \"mutate-hyperconverged-hco-v1beta1.kubevirt.io\": " +
		"tls: failed to verify certificate: x509: certificate signed by unknown authority")

	c := &errApplyClient{
		Client:   fake.NewClientBuilder().Build(),
		applyErr: applyErr,
	}
	dd := NewDriftDetector(c)

	desired := makeObj(nil, map[string]interface{}{"key": "value"})
	live := makeObj(nil, map[string]interface{}{"key": "value"})

	_, err := dd.DetectDrift(context.Background(), desired, live)
	if err == nil {
		t.Fatal("DetectDrift() should propagate client errors, got nil")
	}
}
