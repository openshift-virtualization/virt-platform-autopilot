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
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	pkgassets "github.com/kubevirt/virt-platform-autopilot/pkg/assets"
	pkgcontext "github.com/kubevirt/virt-platform-autopilot/pkg/context"
	"github.com/kubevirt/virt-platform-autopilot/pkg/observability"
	"github.com/kubevirt/virt-platform-autopilot/pkg/throttling"
)

// failingDriftChecker always returns an error simulating a broken webhook or TLS failure.
type failingDriftChecker struct{ err error }

func (f *failingDriftChecker) DetectDrift(_ context.Context, _, _ *unstructured.Unstructured) (bool, error) {
	return false, f.err
}

// TestDriftDetectionFailureSetsComplianceToZero verifies that when the SSA dry-run used
// for drift detection fails persistently (e.g. webhook down), compliance_status is set to 0
// so that VirtPlatformSyncFailed can fire. Before the fix, the error path returned without
// updating the metric, leaving it stuck at 1 (synced) indefinitely.
func TestDriftDetectionFailureSetsComplianceToZero(t *testing.T) {
	observability.ComplianceStatus.Reset()

	loader := pkgassets.NewLoader()
	renderer := NewRenderer(loader)

	// 04-psi-enable.yaml is a static (non-template) MachineConfig — no HCO fields needed.
	assetMeta := &pkgassets.AssetMetadata{
		Name:      "psi-enable",
		Path:      "active/machine-config/04-psi-enable.yaml",
		Component: "MachineConfig",
	}

	hco := pkgcontext.NewMockHCO("kubevirt-hyperconverged", "kubevirt-hyperconverged")
	renderCtx := pkgcontext.NewRenderContext(hco)

	// Render once to get the exact object shape the patcher will use.
	desired, err := renderer.RenderAsset(assetMeta, renderCtx)
	if err != nil {
		t.Fatalf("failed to render asset: %v", err)
	}

	// Create the live object pre-existing in the cluster so liveExists=true and
	// the drift-detection branch is reached.
	live := desired.DeepCopy()
	fakeClient := fake.NewClientBuilder().WithObjects(live).Build()

	p := &Patcher{
		renderer:          renderer,
		applier:           NewApplier(fakeClient, nil),
		driftDetector:     &failingDriftChecker{err: fmt.Errorf("webhook TLS failure")},
		throttle:          throttling.NewTokenBucket(),
		thrashingDetector: throttling.NewThrashingDetector(),
		client:            fakeClient,
	}

	_, reconcileErr := p.ReconcileAsset(context.Background(), assetMeta, renderCtx)
	if reconcileErr == nil {
		t.Fatal("expected ReconcileAsset to return an error when drift detection fails")
	}
	if !strings.Contains(reconcileErr.Error(), "drift detection failed") {
		t.Errorf("unexpected error message: %v", reconcileErr)
	}

	// compliance_status must be 0: the operator cannot guarantee compliance while
	// drift detection is broken and must signal that to the monitoring stack.
	gauge := observability.ComplianceStatus.WithLabelValues(
		desired.GetKind(),
		desired.GetName(),
		desired.GetNamespace(),
	)
	if val := testutil.ToFloat64(gauge); val != 0 {
		t.Errorf("compliance_status = %v, want 0 after drift detection failure", val)
	}
}

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

// TestNamespaceNotFoundDoesNotConsumeTokens verifies that when the target namespace
// does not exist the patcher performs a cost-free soft skip: no rate-limit token is
// consumed and no error is returned. The API server returns only resource-not-found
// (never namespace-not-found) for GET operations, so the namespace must be checked
// explicitly before the token-consumption step (Pre-Step 6).
func TestNamespaceNotFoundDoesNotConsumeTokens(t *testing.T) {
	observability.ComplianceStatus.Reset()

	loader := pkgassets.NewLoader()
	renderer := NewRenderer(loader)

	// node-health-check is a static asset in namespace openshift-operators.
	assetMeta := &pkgassets.AssetMetadata{
		Name:      "node-health-check",
		Path:      "active/node-health/standard-remediation.yaml",
		Component: "NodeHealthCheck",
	}

	hco := pkgcontext.NewMockHCO("kubevirt-hyperconverged", "kubevirt-hyperconverged")
	renderCtx := pkgcontext.NewRenderContext(hco)

	// Render once to confirm the asset is namespaced — the guard under test is only
	// reachable for namespaced resources.
	desired, err := renderer.RenderAsset(assetMeta, renderCtx)
	if err != nil {
		t.Fatalf("failed to render asset: %v", err)
	}
	if desired.GetNamespace() == "" {
		t.Fatal("asset must be namespaced for this test to exercise the namespace guard")
	}

	// Empty fake cluster — the namespace does not exist.
	fakeClient := fake.NewClientBuilder().Build()

	// Tiny token bucket: capacity 3, 1-minute window.
	// If any token is consumed, calls 4–10 below will be throttled.
	smallBucket := throttling.NewTokenBucketWithSettings(3, time.Minute)

	p := &Patcher{
		renderer:          renderer,
		applier:           NewApplier(fakeClient, nil),
		driftDetector:     NewDriftDetector(fakeClient),
		throttle:          smallBucket,
		thrashingDetector: throttling.NewThrashingDetector(),
		client:            fakeClient,
	}

	// 10 calls far exceeds the bucket capacity of 3.
	// Pre-Step 6 must short-circuit every call before any token is touched.
	for i := 0; i < 10; i++ {
		applied, err := p.ReconcileAsset(context.Background(), assetMeta, renderCtx)
		if err != nil {
			t.Fatalf("call %d: got error %v; namespace-not-found must be a free soft skip, not an error", i+1, err)
		}
		if applied {
			t.Fatalf("call %d: got applied=true; want false (namespace is missing)", i+1)
		}
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
