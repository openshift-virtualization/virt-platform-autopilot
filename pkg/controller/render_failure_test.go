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

package controller

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	pkgcontext "github.com/kubevirt/virt-platform-autopilot/pkg/context"
	"github.com/kubevirt/virt-platform-autopilot/pkg/observability"
)

func TestInactiveAutopilotResetsRenderFailures(t *testing.T) {
	for _, missingHCO := range []bool{false, true} {
		t.Run(fmt.Sprintf("missingHCO=%t", missingHCO), func(t *testing.T) {
			observability.ResetRenderFailures()
			t.Cleanup(observability.ResetRenderFailures)
			observability.SetRenderFailure("logging-lokistack", "configuration", "NoDefaultStorageClass")
			hco := pkgcontext.NewMockHCO("hco", "openshift-cnv")
			hco.SetAnnotations(map[string]string{"platform.kubevirt.io/autopilot": "false"})
			var objects []client.Object
			if !missingHCO {
				objects = append(objects, hco)
			}
			reader := fake.NewClientBuilder().WithObjects(objects...).Build()
			reconciler, err := NewPlatformReconciler(reader, reader, hco.GetNamespace())
			if err != nil {
				t.Fatal(err)
			}
			_, err = reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: hco.GetName(), Namespace: hco.GetNamespace()}})
			if err != nil {
				t.Fatal(err)
			}
			if got := testutil.CollectAndCount(observability.RenderFailed); got != 0 {
				t.Fatalf("inactive operator retains %d failure series", got)
			}
		})
	}
}

// failingGetClient wraps a client whose reads always fail with a transient
// API error, to exercise the early-exit paths before asset reconciliation.
type failingGetClient struct {
	client.Client
}

func (c failingGetClient) Get(_ context.Context, _ client.ObjectKey, _ client.Object, _ ...client.GetOption) error {
	return fmt.Errorf("api unavailable")
}

func TestEarlyExitPreservesRenderFailures(t *testing.T) {
	observability.ResetRenderFailures()
	t.Cleanup(observability.ResetRenderFailures)
	observability.SetRenderFailure("logging-lokistack", "configuration", "NoDefaultStorageClass")
	hco := pkgcontext.NewMockHCO("hco", "openshift-cnv")
	base := fake.NewClientBuilder().WithObjects(hco).Build()
	reconciler, err := NewPlatformReconciler(failingGetClient{base}, base, hco.GetNamespace())
	if err != nil {
		t.Fatal(err)
	}
	_, err = reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: hco.GetName(), Namespace: hco.GetNamespace()}})
	if err == nil {
		t.Fatal("HCO read failure must fail reconciliation")
	}
	// The reconcile exits before assets re-evaluate their failures: the
	// inconclusive error must preserve the prior firing series instead of
	// falsely resolving it and restarting the alert's for: timer.
	expected := `# HELP kubevirt_autopilot_render_failed Asset rendering failures by reason and code (1=failed, 0=healthy or excluded; the series persists after recovery)
# TYPE kubevirt_autopilot_render_failed gauge
kubevirt_autopilot_render_failed{asset="logging-lokistack",code="NoDefaultStorageClass",reason="configuration"} 1
`
	if err := testutil.CollectAndCompare(observability.RenderFailed, strings.NewReader(expected)); err != nil {
		t.Fatal(err)
	}
}
