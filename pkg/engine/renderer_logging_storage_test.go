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
	"testing"
	"time"

	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/kubevirt/virt-platform-autopilot/pkg/assets"
	pkgcontext "github.com/kubevirt/virt-platform-autopilot/pkg/context"
)

func loggingTestStorageClass(name string, isDefault bool, created int64) *storagev1.StorageClass {
	class := &storagev1.StorageClass{ObjectMeta: metav1.ObjectMeta{Name: name, CreationTimestamp: metav1.NewTime(time.Unix(created, 0))}}
	if isDefault {
		class.Annotations = map[string]string{"storageclass.kubernetes.io/is-default-class": "true"}
	}
	return class
}

func loggingTestStack(class string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "loki.grafana.com/v1", "kind": "LokiStack",
		"metadata": map[string]any{"name": "logging-loki", "namespace": "openshift-logging"},
		"spec":     map[string]any{"storageClassName": class},
	}}
}

func TestLoggingStorageClass(t *testing.T) {
	legacyDefault := loggingTestStorageClass("legacy-default", false, 1)
	legacyDefault.Annotations = map[string]string{"storageclass.beta.kubernetes.io/is-default-class": "true"}
	tests := []struct {
		name      string
		objects   []client.Object
		override  string
		want      string
		wantError string
	}{
		{name: "bare metal with ODF and no LVMS", objects: []client.Object{loggingTestStorageClass("ocs-storagecluster-ceph-rbd", true, 1)}, want: "ocs-storagecluster-ceph-rbd"},
		{name: "preserves existing class after default changes", objects: []client.Object{loggingTestStorageClass("gp3-csi", false, 1), loggingTestStorageClass("new-default", true, 2), loggingTestStack("gp3-csi")}, want: "gp3-csi"},
		{name: "explicit configuration overrides existing class", objects: []client.Object{loggingTestStorageClass("lvms-vg1", true, 1), loggingTestStorageClass("ocs-storagecluster-ceph-rbd", false, 2), loggingTestStack("lvms-vg1")}, override: "ocs-storagecluster-ceph-rbd", want: "ocs-storagecluster-ceph-rbd"},
		{name: "newest default wins", objects: []client.Object{loggingTestStorageClass("old", true, 1), loggingTestStorageClass("new", true, 2)}, want: "new"},
		{name: "equal timestamps are deterministic", objects: []client.Object{loggingTestStorageClass("z", true, 1), loggingTestStorageClass("a", true, 1)}, want: "a"},
		{name: "legacy default annotation", objects: []client.Object{legacyDefault}, want: "legacy-default"},
		{name: "no default fails instead of guessing", objects: []client.Object{loggingTestStorageClass("local-block-ocs", false, 1)}, wantError: "no default StorageClass for logging; run oc get storageclass and set platform.kubevirt.io/logging-storage-class on HyperConverged to an existing class, or configure a cluster default; see docs/logging.md"},
		{name: "removed existing class does not trigger a volume migration", objects: []client.Object{loggingTestStorageClass("odf", true, 1), loggingTestStack("lvms-vg1")}, want: "lvms-vg1"},
		{name: "missing explicit class fails", override: "missing", wantError: `logging StorageClass "missing" does not exist; run oc get storageclass and set platform.kubevirt.io/logging-storage-class on HyperConverged to an existing class, or remove the annotation to use existing/default storage; see docs/logging.md`},
		{name: "existing stack without storage class uses default", objects: []client.Object{loggingTestStorageClass("default", true, 1), loggingTestStack("")}, want: "default"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			if err := storagev1.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			reader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(tt.objects...).Build()
			renderer := NewRenderer(assets.NewLoader())
			renderer.SetClient(reader)
			rendered, err := renderLoggingStorage(renderer, tt.override)
			if tt.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantError) {
					t.Fatalf("error = %v, want %q", err, tt.wantError)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			got, _, err := unstructured.NestedString(rendered.Object, "spec", "storageClassName")
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("storage class = %q, want %q", got, tt.want)
			}
		})
	}
}

func renderLoggingStorage(renderer *Renderer, override string) (*unstructured.Unstructured, error) {
	registry, err := assets.NewRegistry(assets.NewLoader())
	if err != nil {
		return nil, err
	}
	asset, err := registry.GetAsset("logging-lokistack")
	if err != nil {
		return nil, err
	}
	hco := &unstructured.Unstructured{}
	hco.SetAnnotations(map[string]string{"platform.kubevirt.io/logging-storage-class": override})
	return renderer.RenderAsset(asset, &pkgcontext.RenderContext{
		HCO: hco, Topology: &pkgcontext.TopologyContext{IsBareMetal: true, TotalNodeCount: 3},
	})
}

type loggingFailingReader struct {
	client.Reader
	operation string
}

func (r loggingFailingReader) List(ctx context.Context, list client.ObjectList, options ...client.ListOption) error {
	if r.operation == "list" {
		return fmt.Errorf("storage discovery denied")
	}
	return r.Reader.List(ctx, list, options...)
}

func (r loggingFailingReader) Get(ctx context.Context, key client.ObjectKey, object client.Object, options ...client.GetOption) error {
	if r.operation == "get" {
		return fmt.Errorf("LokiStack read denied")
	}
	return r.Reader.Get(ctx, key, object, options...)
}

func TestLoggingStorageDiscoveryErrors(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := storagev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	reader := fake.NewClientBuilder().WithScheme(scheme).Build()
	for _, tt := range []struct{ operation, override, wantError string }{
		{"list", "", "list StorageClasses: storage discovery denied"},
		{"get", "", "read LokiStack openshift-logging/logging-loki: LokiStack read denied"},
		{"get", "custom", `read StorageClass "custom": LokiStack read denied`},
	} {
		t.Run(tt.operation, func(t *testing.T) {
			renderer := NewRenderer(assets.NewLoader())
			renderer.SetClient(loggingFailingReader{Reader: reader, operation: tt.operation})
			_, err := renderLoggingStorage(renderer, tt.override)
			if err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("error = %v, want %q", err, tt.wantError)
			}
		})
	}
}

func TestLoggingStorageRenderingUsesCachedReader(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := storagev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	cached := fake.NewClientBuilder().WithScheme(scheme).WithObjects(loggingTestStorageClass("ocs-storagecluster-ceph-rbd", true, 1)).Build()
	direct := loggingFailingReader{Reader: fake.NewClientBuilder().WithScheme(scheme).Build(), operation: "list"}
	loader := assets.NewLoader()
	registry, err := assets.NewRegistry(loader)
	if err != nil {
		t.Fatal(err)
	}
	asset, err := registry.GetAsset("logging-lokistack")
	if err != nil {
		t.Fatal(err)
	}
	patcher := NewPatcher(cached, direct, loader)
	rendered, err := patcher.renderer.RenderAsset(asset, &pkgcontext.RenderContext{HCO: &unstructured.Unstructured{}, Topology: &pkgcontext.TopologyContext{IsBareMetal: true, TotalNodeCount: 3}})
	if err != nil {
		t.Fatal(err)
	}
	class, _, err := unstructured.NestedString(rendered.Object, "spec", "storageClassName")
	if err != nil {
		t.Fatal(err)
	}
	if class != "ocs-storagecluster-ceph-rbd" {
		t.Fatalf("rendered storage class = %q, want ocs-storagecluster-ceph-rbd", class)
	}
}

func TestLoggingStorageRenderingPreservesUnlabeledStack(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := storagev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	cached := fake.NewClientBuilder().WithScheme(scheme).WithObjects(loggingTestStorageClass("new-default", true, 2)).Build()
	direct := fake.NewClientBuilder().WithScheme(scheme).WithObjects(loggingTestStack("existing-class")).Build()
	patcher := NewPatcher(cached, direct, assets.NewLoader())
	if patcher.renderer.client != cached {
		t.Fatal("template introspection must keep using the cached client")
	}
	rendered, err := renderLoggingStorage(patcher.renderer, "")
	if err != nil {
		t.Fatal(err)
	}
	class, _, err := unstructured.NestedString(rendered.Object, "spec", "storageClassName")
	if err != nil || class != "existing-class" {
		t.Fatalf("class = %q, error = %v; want existing-class", class, err)
	}
}

func TestLoggingStorageClassOfflineConfiguration(t *testing.T) {
	renderer := NewRenderer(assets.NewLoader())
	rendered, err := renderLoggingStorage(renderer, "custom-storage")
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := unstructured.NestedString(rendered.Object, "spec", "storageClassName")
	if err != nil {
		t.Fatal(err)
	}
	if got != "custom-storage" {
		t.Fatalf("offline storage class = %q, want custom-storage", got)
	}
	rendered, err = renderLoggingStorage(renderer, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, found, err := unstructured.NestedString(rendered.Object, "spec", "storageClassName"); found || err != nil {
		t.Fatalf("offline discovery must omit storageClassName: found = %v, error = %v", found, err)
	}
}
