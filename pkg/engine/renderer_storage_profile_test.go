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
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/kubevirt/virt-platform-autopilot/pkg/assets"
)

func TestStorageProfileRWXClass(t *testing.T) {
	scheme := runtime.NewScheme()

	t.Run("returns first RWX Filesystem StorageClass", func(t *testing.T) {
		cephfsProfile := &unstructured.Unstructured{}
		setStorageProfileGVK(cephfsProfile)
		cephfsProfile.SetName("ocs-storagecluster-cephfs")
		_ = unstructured.SetNestedSlice(cephfsProfile.Object, []any{
			map[string]any{
				"accessModes": []any{"ReadWriteOnce", "ReadWriteMany"},
				"volumeMode":  "Filesystem",
			},
		}, "status", "claimPropertySets")

		rbdProfile := &unstructured.Unstructured{}
		setStorageProfileGVK(rbdProfile)
		rbdProfile.SetName("ocs-storagecluster-ceph-rbd")
		_ = unstructured.SetNestedSlice(rbdProfile.Object, []any{
			map[string]any{
				"accessModes": []any{"ReadWriteOnce"},
				"volumeMode":  "Block",
			},
		}, "status", "claimPropertySets")

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(cephfsProfile, rbdProfile).
			Build()

		loader := assets.NewLoader()
		renderer := NewRenderer(loader)
		renderer.SetClient(fakeClient)

		funcMap := renderer.customFuncMap()
		fn := funcMap["storageProfileRWXClass"].(func() string)

		result := fn()
		if result != "ocs-storagecluster-cephfs" {
			t.Errorf("expected ocs-storagecluster-cephfs, got %q", result)
		}
	})

	t.Run("returns alphabetically first when multiple RWX classes exist", func(t *testing.T) {
		nfsProfile := &unstructured.Unstructured{}
		setStorageProfileGVK(nfsProfile)
		nfsProfile.SetName("nfs-client")
		_ = unstructured.SetNestedSlice(nfsProfile.Object, []any{
			map[string]any{
				"accessModes": []any{"ReadWriteMany"},
				"volumeMode":  "Filesystem",
			},
		}, "status", "claimPropertySets")

		cephfsProfile := &unstructured.Unstructured{}
		setStorageProfileGVK(cephfsProfile)
		cephfsProfile.SetName("ocs-storagecluster-cephfs")
		_ = unstructured.SetNestedSlice(cephfsProfile.Object, []any{
			map[string]any{
				"accessModes": []any{"ReadWriteMany"},
				"volumeMode":  "Filesystem",
			},
		}, "status", "claimPropertySets")

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(nfsProfile, cephfsProfile).
			Build()

		loader := assets.NewLoader()
		renderer := NewRenderer(loader)
		renderer.SetClient(fakeClient)

		funcMap := renderer.customFuncMap()
		fn := funcMap["storageProfileRWXClass"].(func() string)

		result := fn()
		if result != "nfs-client" {
			t.Errorf("expected nfs-client (alphabetically first), got %q", result)
		}
	})

	t.Run("returns empty string when no RWX Filesystem class exists", func(t *testing.T) {
		rbdProfile := &unstructured.Unstructured{}
		setStorageProfileGVK(rbdProfile)
		rbdProfile.SetName("ocs-storagecluster-ceph-rbd")
		_ = unstructured.SetNestedSlice(rbdProfile.Object, []any{
			map[string]any{
				"accessModes": []any{"ReadWriteOnce"},
				"volumeMode":  "Block",
			},
		}, "status", "claimPropertySets")

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(rbdProfile).
			Build()

		loader := assets.NewLoader()
		renderer := NewRenderer(loader)
		renderer.SetClient(fakeClient)

		funcMap := renderer.customFuncMap()
		fn := funcMap["storageProfileRWXClass"].(func() string)

		result := fn()
		if result != "" {
			t.Errorf("expected empty string, got %q", result)
		}
	})

	t.Run("returns empty string when no client is set", func(t *testing.T) {
		loader := assets.NewLoader()
		renderer := NewRenderer(loader)

		funcMap := renderer.customFuncMap()
		fn := funcMap["storageProfileRWXClass"].(func() string)

		result := fn()
		if result != "" {
			t.Errorf("expected empty string, got %q", result)
		}
	})
}

func setStorageProfileGVK(obj *unstructured.Unstructured) {
	obj.SetKind("StorageProfile")
	obj.SetAPIVersion("cdi.kubevirt.io/v1beta1")
}
