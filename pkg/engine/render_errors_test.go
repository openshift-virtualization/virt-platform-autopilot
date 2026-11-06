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
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/kubevirt/virt-platform-autopilot/pkg/assets"
	pkgcontext "github.com/kubevirt/virt-platform-autopilot/pkg/context"
	"github.com/kubevirt/virt-platform-autopilot/pkg/observability"
	"github.com/kubevirt/virt-platform-autopilot/pkg/util"
)

func TestRenderFailureClassification(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := storagev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	reader := fake.NewClientBuilder().WithScheme(scheme).Build()
	for _, tt := range []struct{ name, reason, code, operation, override string }{
		{name: "missing storage configuration", reason: "configuration", code: NoDefaultStorageClass},
		{name: "API list failure", reason: "internal", operation: "list"},
		{name: "API read failure", reason: "internal", operation: "get"},
		{name: "explicit class is passed through", override: "admin-choice"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			renderer := NewRenderer(assets.NewLoader())
			renderer.SetClient(loggingFailingReader{Reader: reader, operation: tt.operation})
			_, err := renderLoggingStorage(renderer, tt.override)
			reason, code := renderFailureLabels(err)
			if reason != tt.reason || code != tt.code {
				t.Fatalf("classification = (%q, %q), want (%q, %q): %v", reason, code, tt.reason, tt.code, err)
			}
			if tt.code != "" {
				var configurationError *ConfigurationError
				if !errors.As(err, &configurationError) {
					t.Fatal("template execution lost ConfigurationError")
				}
			}
		})
	}
	renderer := NewRenderer(assets.NewLoader())
	for _, template := range []string{`{{ .Missing }}`, `{{ broken syntax`} {
		_, err := renderer.renderTemplate("invalid", template, &pkgcontext.RenderContext{})
		reason, code := renderFailureLabels(err)
		if reason != "internal" || code != "" {
			t.Fatalf("template error classified as %q/%q: %v", reason, code, err)
		}
	}
}

func TestRenderFailureMetricAndEventLifecycle(t *testing.T) {
	observability.RenderFailed.Reset()
	t.Cleanup(observability.RenderFailed.Reset)
	scheme := runtime.NewScheme()
	if err := storagev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	loader := assets.NewLoader()
	registry, err := assets.NewRegistry(loader)
	if err != nil {
		t.Fatal(err)
	}
	asset, err := registry.GetAsset("logging-lokistack")
	if err != nil {
		t.Fatal(err)
	}
	patcher := NewPatcher(fakeClient, fakeClient, loader)
	recorder := &renderFailureRecorder{}
	patcher.SetEventRecorder(util.NewEventRecorder(recorder))
	renderCtx := pkgcontext.NewRenderContext(pkgcontext.NewMockHCO("hco", "openshift-cnv"))
	_, err = patcher.ReconcileAsset(context.Background(), asset, renderCtx)
	if err == nil {
		t.Fatal("missing storage must fail reconciliation")
	}
	if !strings.Contains(recorder.message, NoDefaultStorageClass) || !strings.Contains(recorder.message, asset.Name) {
		t.Fatalf("event does not identify configuration problem: %q", recorder.message)
	}
	assertRenderFailureMetric(t, "configuration", NoDefaultStorageClass)

	patcher.renderer.SetClient(loggingFailingReader{Reader: fakeClient, operation: "list"})
	_, err = patcher.ReconcileAsset(context.Background(), asset, renderCtx)
	if err == nil {
		t.Fatal("API failure must fail reconciliation")
	}
	assertRenderFailureMetric(t, "internal", "")

	// Correcting configuration clears the failure even if the target operator
	// namespace is not installed yet (the apply stage then soft-skips).
	if err := fakeClient.Create(context.Background(), loggingTestStorageClass("configured", true, 1)); err != nil {
		t.Fatal(err)
	}
	patcher.renderer.SetClient(fakeClient)
	if _, err := patcher.ReconcileAsset(context.Background(), asset, renderCtx); err != nil {
		t.Fatal(err)
	}
	if got := testutil.CollectAndCount(observability.RenderFailed); got != 0 {
		t.Fatalf("successful render retains %d metrics", got)
	}

	// Exclusion clears a failure even when discovery is still unavailable.
	patcher.renderer.SetClient(loggingFailingReader{Reader: fakeClient, operation: "list"})
	observability.SetRenderFailure(asset.Name, "internal", "")
	patcher.CleanupExcludedAsset(asset, renderCtx)
	if got := testutil.CollectAndCount(observability.RenderFailed); got != 0 {
		t.Fatalf("excluded asset retains %d metrics", got)
	}

	// A successfully rendered empty asset must clear an earlier failure too.
	observability.SetRenderFailure("pci-passthrough", "internal", "")
	patcher.renderer.SetClient(nil)
	emptyAsset, err := registry.GetAsset("pci-passthrough")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := patcher.ReconcileAsset(context.Background(), emptyAsset, renderCtx); err != nil {
		t.Fatal(err)
	}
	if got := testutil.CollectAndCount(observability.RenderFailed); got != 0 {
		t.Fatalf("empty render retains %d metrics", got)
	}
}

type renderFailureRecorder struct{ message string }

func (r *renderFailureRecorder) Eventf(_ runtime.Object, _ runtime.Object, _, reason, _, message string, args ...any) {
	if reason == util.EventReasonRenderFailed {
		r.message = fmt.Sprintf(message, args...)
	}
}

func assertRenderFailureMetric(t *testing.T, reason, code string) {
	t.Helper()
	expected := fmt.Sprintf(`# HELP kubevirt_autopilot_render_failed Asset rendering failures by reason and code (1=failed; absent after success or exclusion)
# TYPE kubevirt_autopilot_render_failed gauge
kubevirt_autopilot_render_failed{asset="logging-lokistack",code="%s",reason="%s"} 1
`, code, reason)
	if err := testutil.CollectAndCompare(observability.RenderFailed, strings.NewReader(expected)); err != nil {
		t.Fatal(err)
	}
}
