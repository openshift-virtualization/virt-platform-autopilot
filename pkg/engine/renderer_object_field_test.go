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

func TestObjectFieldBool(t *testing.T) {
	newSriovConfig := func(enableInjector bool) *unstructured.Unstructured {
		obj := &unstructured.Unstructured{}
		obj.SetKind("SriovOperatorConfig")
		obj.SetAPIVersion("sriovnetwork.openshift.io/v1")
		obj.SetName("default")
		obj.SetNamespace("sriov-network-operator")
		_ = unstructured.SetNestedField(obj.Object, enableInjector, "spec", "enableInjector")
		return obj
	}

	call := func(t *testing.T, objs ...runtime.Object) bool {
		t.Helper()
		builder := fake.NewClientBuilder().WithScheme(runtime.NewScheme())
		if len(objs) > 0 {
			builder = builder.WithRuntimeObjects(objs...)
		}
		renderer := NewRenderer(assets.NewLoader())
		renderer.SetClient(builder.Build())
		fn := renderer.customFuncMap()["objectFieldBool"].(func(string, string, string, string, string) bool)
		return fn("sriovnetwork.openshift.io/v1", "SriovOperatorConfig", "sriov-network-operator", "default", "spec.enableInjector")
	}

	t.Run("returns true when field is true", func(t *testing.T) {
		if !call(t, newSriovConfig(true)) {
			t.Error("expected true when spec.enableInjector=true")
		}
	})

	t.Run("returns false when field is false", func(t *testing.T) {
		if call(t, newSriovConfig(false)) {
			t.Error("expected false when spec.enableInjector=false")
		}
	})

	t.Run("returns false when object is absent", func(t *testing.T) {
		if call(t) {
			t.Error("expected false when SriovOperatorConfig does not exist")
		}
	})

	t.Run("returns false when no client is set", func(t *testing.T) {
		renderer := NewRenderer(assets.NewLoader())
		fn := renderer.customFuncMap()["objectFieldBool"].(func(string, string, string, string, string) bool)
		if fn("sriovnetwork.openshift.io/v1", "SriovOperatorConfig", "sriov-network-operator", "default", "spec.enableInjector") {
			t.Error("expected false when client is nil")
		}
	})
}
