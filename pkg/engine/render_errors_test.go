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
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/kubevirt/virt-platform-autopilot/pkg/assets"
	pkgcontext "github.com/kubevirt/virt-platform-autopilot/pkg/context"
	"github.com/kubevirt/virt-platform-autopilot/pkg/observability"
	"github.com/kubevirt/virt-platform-autopilot/pkg/overrides"
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
	observability.ResetRenderFailures()
	t.Cleanup(observability.ResetRenderFailures)
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
	assertRenderFailureMetric(t, failureSeries{asset: asset.Name, reason: "configuration", code: NoDefaultStorageClass, value: 1})

	patcher.renderer.SetClient(loggingFailingReader{Reader: fakeClient, operation: "list"})
	_, err = patcher.ReconcileAsset(context.Background(), asset, renderCtx)
	if err == nil {
		t.Fatal("API failure must fail reconciliation")
	}
	// The superseded configuration series stays present at 0: a missing series
	// would reset the alert's for: timer mid-incident.
	assertRenderFailureMetric(t,
		failureSeries{asset: asset.Name, reason: "internal", value: 1},
		failureSeries{asset: asset.Name, reason: "configuration", code: NoDefaultStorageClass, value: 0},
	)

	// Correcting configuration clears the failure even if the target operator
	// namespace is not installed yet (the apply stage then soft-skips).
	if err := fakeClient.Create(context.Background(), loggingTestStorageClass("configured", true, 1)); err != nil {
		t.Fatal(err)
	}
	patcher.renderer.SetClient(fakeClient)
	if _, err := patcher.ReconcileAsset(context.Background(), asset, renderCtx); err != nil {
		t.Fatal(err)
	}
	// Recovery keeps the series present at 0 instead of deleting them.
	assertRenderFailureMetric(t,
		failureSeries{asset: asset.Name, reason: "internal", value: 0},
		failureSeries{asset: asset.Name, reason: "configuration", code: NoDefaultStorageClass, value: 0},
	)

	// Exclusion clears a failure even when discovery is still unavailable.
	patcher.renderer.SetClient(loggingFailingReader{Reader: fakeClient, operation: "list"})
	observability.SetRenderFailure(asset.Name, "internal", "")
	patcher.CleanupExcludedAsset(asset, renderCtx)
	assertRenderFailureMetric(t,
		failureSeries{asset: asset.Name, reason: "internal", value: 0},
		failureSeries{asset: asset.Name, reason: "configuration", code: NoDefaultStorageClass, value: 0},
	)

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
	assertRenderFailureMetric(t,
		failureSeries{asset: "pci-passthrough", reason: "internal", value: 0},
		failureSeries{asset: asset.Name, reason: "internal", value: 0},
		failureSeries{asset: asset.Name, reason: "configuration", code: NoDefaultStorageClass, value: 0},
	)
}

// TestRenderFailureSkipsOptedOutAssets covers render failures on assets the
// operator is not supposed to manage: the failure must not be recorded, must
// not emit an event, and must not fail reconciliation.
func TestRenderFailureSkipsOptedOutAssets(t *testing.T) {
	observability.ResetRenderFailures()
	t.Cleanup(observability.ResetRenderFailures)

	scheme := runtime.NewScheme()
	if err := storagev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	scheme.AddKnownTypeWithName(
		schema.GroupVersionKind{Group: "loki.grafana.com", Version: "v1", Kind: "LokiStack"},
		&unstructured.Unstructured{},
	)
	loader := assets.NewLoader()
	registry, err := assets.NewRegistry(loader)
	if err != nil {
		t.Fatal(err)
	}
	asset, err := registry.GetAsset("logging-lokistack")
	if err != nil {
		t.Fatal(err)
	}

	t.Run("root-excluded via disabled-resources", func(t *testing.T) {
		observability.ResetRenderFailures()
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
		patcher := NewPatcher(fakeClient, fakeClient, loader)
		recorder := &renderFailureRecorder{}
		patcher.SetEventRecorder(util.NewEventRecorder(recorder))

		hco := pkgcontext.NewMockHCO("hco", "openshift-cnv")
		hco.SetAnnotations(map[string]string{
			DisabledResourcesAnnotation: "- kind: LokiStack\n  namespace: openshift-logging\n  name: logging-loki",
		})
		renderCtx := pkgcontext.NewRenderContext(hco)

		applied, err := patcher.ReconcileAsset(context.Background(), asset, renderCtx)
		if err != nil {
			t.Fatalf("root-excluded asset must not fail reconciliation: %v", err)
		}
		if applied {
			t.Fatal("root-excluded asset must not be applied")
		}
		if recorder.message != "" {
			t.Fatalf("root-excluded asset must not emit RenderFailed event: %q", recorder.message)
		}
		if got := testutil.CollectAndCount(observability.RenderFailed); got != 0 {
			t.Fatalf("root-excluded asset recorded %d failure series", got)
		}
	})

	t.Run("unmanaged live object", func(t *testing.T) {
		observability.ResetRenderFailures()
		live := loggingTestStack("")
		live.SetAnnotations(map[string]string{overrides.AnnotationMode: overrides.ModeUnmanaged})
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(live).Build()
		patcher := NewPatcher(fakeClient, fakeClient, loader)
		recorder := &renderFailureRecorder{}
		patcher.SetEventRecorder(util.NewEventRecorder(recorder))
		renderCtx := pkgcontext.NewRenderContext(pkgcontext.NewMockHCO("hco", "openshift-cnv"))

		applied, err := patcher.ReconcileAsset(context.Background(), asset, renderCtx)
		if err != nil {
			t.Fatalf("unmanaged asset must not fail reconciliation: %v", err)
		}
		if applied {
			t.Fatal("unmanaged asset must not be applied")
		}
		if recorder.message != "" {
			t.Fatalf("unmanaged asset must not emit RenderFailed event: %q", recorder.message)
		}
		if got := testutil.CollectAndCount(observability.RenderFailed); got != 0 {
			t.Fatalf("unmanaged asset recorded %d failure series", got)
		}
	})
}

type renderFailureRecorder struct{ message string }

func (r *renderFailureRecorder) Eventf(_ runtime.Object, _ runtime.Object, _, reason, _, message string, args ...any) {
	if reason == util.EventReasonRenderFailed {
		r.message = fmt.Sprintf(message, args...)
	}
}

type failureSeries struct {
	asset  string
	reason string
	code   string
	value  int
}

func assertRenderFailureMetric(t *testing.T, series ...failureSeries) {
	t.Helper()
	var b strings.Builder
	b.WriteString("# HELP kubevirt_autopilot_render_failed Asset rendering failures by reason and code (1=failed, 0=healthy or excluded; the series persists after recovery)\n")
	b.WriteString("# TYPE kubevirt_autopilot_render_failed gauge\n")
	for _, s := range series {
		fmt.Fprintf(&b, "kubevirt_autopilot_render_failed{asset=%q,code=%q,reason=%q} %d\n", s.asset, s.code, s.reason, s.value)
	}
	if err := testutil.CollectAndCompare(observability.RenderFailed, strings.NewReader(b.String())); err != nil {
		t.Fatal(err)
	}
}
