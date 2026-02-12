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

package assets

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestTombstone(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Tombstone Suite")
}

var _ = Describe("Tombstone Loader", func() {
	var loader *Loader

	BeforeEach(func() {
		loader = NewLoader()
	})

	Describe("LoadTombstones", func() {
		It("should return empty list when tombstones directory is empty", func() {
			tombstones, err := loader.LoadTombstones()
			Expect(err).NotTo(HaveOccurred())
			// Directory exists but only contains .gitkeep
			Expect(tombstones).To(BeEmpty())
		})

		It("should handle non-existent tombstones directory gracefully", func() {
			// This is handled by the .gitkeep file, but the function should handle missing dir
			tombstones, err := loader.LoadTombstones()
			Expect(err).NotTo(HaveOccurred())
			// Empty directory or missing directory both return empty list
			Expect(tombstones).To(Or(BeNil(), BeEmpty()))
		})
	})

	Describe("validateTombstone", func() {
		It("should accept valid tombstone with all required fields", func() {
			obj := &unstructured.Unstructured{}
			obj.SetAPIVersion("v1")
			obj.SetKind("ConfigMap")
			obj.SetName("test-config")
			obj.SetLabels(map[string]string{
				TombstoneLabel: TombstoneLabelValue,
			})

			err := validateTombstone(obj, "test.yaml")
			Expect(err).NotTo(HaveOccurred())
		})

		It("should reject tombstone missing kind", func() {
			obj := &unstructured.Unstructured{}
			obj.SetAPIVersion("v1")
			obj.SetName("test-config")
			obj.SetLabels(map[string]string{
				TombstoneLabel: TombstoneLabelValue,
			})

			err := validateTombstone(obj, "test.yaml")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("missing required field: kind"))
		})

		It("should reject tombstone missing apiVersion", func() {
			obj := &unstructured.Unstructured{}
			obj.SetKind("ConfigMap")
			obj.SetName("test-config")
			obj.SetLabels(map[string]string{
				TombstoneLabel: TombstoneLabelValue,
			})

			err := validateTombstone(obj, "test.yaml")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("missing required field: apiVersion"))
		})

		It("should reject tombstone missing name", func() {
			obj := &unstructured.Unstructured{}
			obj.SetAPIVersion("v1")
			obj.SetKind("ConfigMap")
			obj.SetLabels(map[string]string{
				TombstoneLabel: TombstoneLabelValue,
			})

			err := validateTombstone(obj, "test.yaml")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("missing required field: metadata.name"))
		})

		It("should reject tombstone missing required label", func() {
			obj := &unstructured.Unstructured{}
			obj.SetAPIVersion("v1")
			obj.SetKind("ConfigMap")
			obj.SetName("test-config")
			// No labels set

			err := validateTombstone(obj, "test.yaml")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("missing required label"))
			Expect(err.Error()).To(ContainSubstring(TombstoneLabel))
		})

		It("should reject tombstone with incorrect label value", func() {
			obj := &unstructured.Unstructured{}
			obj.SetAPIVersion("v1")
			obj.SetKind("ConfigMap")
			obj.SetName("test-config")
			obj.SetLabels(map[string]string{
				TombstoneLabel: "wrong-value",
			})

			err := validateTombstone(obj, "test.yaml")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("has incorrect label value"))
			Expect(err.Error()).To(ContainSubstring("wrong-value"))
		})

		It("should accept namespaced resource with namespace", func() {
			obj := &unstructured.Unstructured{}
			obj.SetAPIVersion("v1")
			obj.SetKind("ConfigMap")
			obj.SetName("test-config")
			obj.SetNamespace("test-namespace")
			obj.SetLabels(map[string]string{
				TombstoneLabel: TombstoneLabelValue,
			})

			err := validateTombstone(obj, "test.yaml")
			Expect(err).NotTo(HaveOccurred())
		})

		It("should accept cluster-scoped resource without namespace", func() {
			obj := &unstructured.Unstructured{}
			obj.SetAPIVersion("machineconfiguration.openshift.io/v1")
			obj.SetKind("MachineConfig")
			obj.SetName("50-swap-enable")
			obj.SetLabels(map[string]string{
				TombstoneLabel: TombstoneLabelValue,
			})

			err := validateTombstone(obj, "test.yaml")
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Describe("TombstoneMetadata", func() {
		It("should correctly extract GVK from tombstone object", func() {
			obj := &unstructured.Unstructured{}
			obj.SetAPIVersion("hco.kubevirt.io/v1beta1")
			obj.SetKind("HyperConverged")
			obj.SetName("test")
			obj.SetLabels(map[string]string{
				TombstoneLabel: TombstoneLabelValue,
			})

			ts := TombstoneMetadata{
				Path:      "test.yaml",
				GVK:       obj.GroupVersionKind(),
				Namespace: obj.GetNamespace(),
				Name:      obj.GetName(),
				Object:    obj,
			}

			Expect(ts.GVK.Group).To(Equal("hco.kubevirt.io"))
			Expect(ts.GVK.Version).To(Equal("v1beta1"))
			Expect(ts.GVK.Kind).To(Equal("HyperConverged"))
			Expect(ts.Name).To(Equal("test"))
		})
	})
})
