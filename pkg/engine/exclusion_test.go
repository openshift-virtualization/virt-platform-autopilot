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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

var _ = Describe("Root Exclusion", func() {
	Describe("ParseDisabledResources", func() {
		It("should return empty map for empty annotation", func() {
			result := ParseDisabledResources("")
			Expect(result).To(BeEmpty())
		})

		It("should parse single resource", func() {
			result := ParseDisabledResources("ConfigMap/my-config")
			Expect(result).To(HaveLen(1))
			Expect(result["ConfigMap/my-config"]).To(BeTrue())
		})

		It("should parse multiple resources", func() {
			result := ParseDisabledResources("ConfigMap/foo, Secret/bar, Deployment/baz")
			Expect(result).To(HaveLen(3))
			Expect(result["ConfigMap/foo"]).To(BeTrue())
			Expect(result["Secret/bar"]).To(BeTrue())
			Expect(result["Deployment/baz"]).To(BeTrue())
		})

		It("should handle whitespace correctly", func() {
			result := ParseDisabledResources("  ConfigMap/foo  ,  Secret/bar  ")
			Expect(result).To(HaveLen(2))
			Expect(result["ConfigMap/foo"]).To(BeTrue())
			Expect(result["Secret/bar"]).To(BeTrue())
		})

		It("should ignore empty entries", func() {
			result := ParseDisabledResources("ConfigMap/foo,  , Secret/bar")
			Expect(result).To(HaveLen(2))
			Expect(result["ConfigMap/foo"]).To(BeTrue())
			Expect(result["Secret/bar"]).To(BeTrue())
		})

		It("should handle trailing comma", func() {
			result := ParseDisabledResources("ConfigMap/foo, Secret/bar,")
			Expect(result).To(HaveLen(2))
			Expect(result["ConfigMap/foo"]).To(BeTrue())
			Expect(result["Secret/bar"]).To(BeTrue())
		})

		It("should handle leading comma", func() {
			result := ParseDisabledResources(", ConfigMap/foo, Secret/bar")
			Expect(result).To(HaveLen(2))
			Expect(result["ConfigMap/foo"]).To(BeTrue())
			Expect(result["Secret/bar"]).To(BeTrue())
		})

		It("should handle complex resource names", func() {
			result := ParseDisabledResources("KubeDescheduler/cluster, MachineConfig/50-swap-enable")
			Expect(result).To(HaveLen(2))
			Expect(result["KubeDescheduler/cluster"]).To(BeTrue())
			Expect(result["MachineConfig/50-swap-enable"]).To(BeTrue())
		})
	})

	Describe("FilterExcludedAssets", func() {
		var assets []*unstructured.Unstructured

		BeforeEach(func() {
			// Create test assets
			assets = []*unstructured.Unstructured{
				createTestAsset("ConfigMap", "config-1"),
				createTestAsset("ConfigMap", "config-2"),
				createTestAsset("Secret", "secret-1"),
				createTestAsset("Deployment", "deploy-1"),
			}
		})

		It("should return all assets when disabled map is empty", func() {
			disabled := make(map[string]bool)
			result := FilterExcludedAssets(assets, disabled)
			Expect(result).To(HaveLen(4))
		})

		It("should exclude single resource", func() {
			disabled := ParseDisabledResources("ConfigMap/config-1")
			result := FilterExcludedAssets(assets, disabled)
			Expect(result).To(HaveLen(3))

			// Verify the right one was excluded
			for _, asset := range result {
				key := asset.GetKind() + "/" + asset.GetName()
				Expect(key).NotTo(Equal("ConfigMap/config-1"))
			}
		})

		It("should exclude multiple resources", func() {
			disabled := ParseDisabledResources("ConfigMap/config-1, Secret/secret-1")
			result := FilterExcludedAssets(assets, disabled)
			Expect(result).To(HaveLen(2))

			// Verify the right ones were kept
			Expect(result[0].GetKind() + "/" + result[0].GetName()).To(Equal("ConfigMap/config-2"))
			Expect(result[1].GetKind() + "/" + result[1].GetName()).To(Equal("Deployment/deploy-1"))
		})

		It("should exclude all resources when all are disabled", func() {
			disabled := ParseDisabledResources("ConfigMap/config-1, ConfigMap/config-2, Secret/secret-1, Deployment/deploy-1")
			result := FilterExcludedAssets(assets, disabled)
			Expect(result).To(BeEmpty())
		})

		It("should keep all resources when none match", func() {
			disabled := ParseDisabledResources("ConfigMap/nonexistent, Secret/nonexistent")
			result := FilterExcludedAssets(assets, disabled)
			Expect(result).To(HaveLen(4))
		})

		It("should handle nil disabled map", func() {
			result := FilterExcludedAssets(assets, nil)
			Expect(result).To(HaveLen(4))
		})
	})

	Describe("IsResourceExcluded", func() {
		It("should return false for empty disabled map", func() {
			disabled := make(map[string]bool)
			Expect(IsResourceExcluded("ConfigMap", "test", disabled)).To(BeFalse())
		})

		It("should return true for excluded resource", func() {
			disabled := ParseDisabledResources("ConfigMap/test")
			Expect(IsResourceExcluded("ConfigMap", "test", disabled)).To(BeTrue())
		})

		It("should return false for non-excluded resource", func() {
			disabled := ParseDisabledResources("ConfigMap/test")
			Expect(IsResourceExcluded("ConfigMap", "other", disabled)).To(BeFalse())
		})

		It("should be case-sensitive", func() {
			disabled := ParseDisabledResources("ConfigMap/test")
			Expect(IsResourceExcluded("configmap", "test", disabled)).To(BeFalse())
			Expect(IsResourceExcluded("ConfigMap", "Test", disabled)).To(BeFalse())
		})

		It("should return false for nil disabled map", func() {
			Expect(IsResourceExcluded("ConfigMap", "test", nil)).To(BeFalse())
		})
	})
})

// createTestAsset creates a test unstructured object
func createTestAsset(kind, name string) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetKind(kind)
	obj.SetName(name)
	obj.SetAPIVersion("v1")
	return obj
}
