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
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	pkgcontext "github.com/kubevirt/virt-platform-autopilot/pkg/context"
	"github.com/kubevirt/virt-platform-autopilot/pkg/observability"
)

func TestInactiveAutopilotClearsRenderFailures(t *testing.T) {
	for _, missingHCO := range []bool{false, true} {
		t.Run(fmt.Sprintf("missingHCO=%t", missingHCO), func(t *testing.T) {
			observability.RenderFailed.Reset()
			t.Cleanup(observability.RenderFailed.Reset)
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
