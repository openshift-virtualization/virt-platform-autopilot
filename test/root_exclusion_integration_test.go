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

package test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/kubevirt/virt-platform-autopilot/pkg/engine"
)

var _ = Describe("Root Exclusion Integration", func() {
	Describe("ParseDisabledResources", func() {
		It("should parse empty annotation", func() {
			result := engine.ParseDisabledResources("")
			Expect(result).To(BeEmpty())
		})

		It("should parse single resource", func() {
			result := engine.ParseDisabledResources("ConfigMap/test-config")
			Expect(result).To(HaveLen(1))
			Expect(result["ConfigMap/test-config"]).To(BeTrue())
		})

		It("should parse multiple resources with whitespace", func() {
			result := engine.ParseDisabledResources("ConfigMap/foo, Secret/bar, Deployment/baz")
			Expect(result).To(HaveLen(3))
			Expect(result["ConfigMap/foo"]).To(BeTrue())
			Expect(result["Secret/bar"]).To(BeTrue())
			Expect(result["Deployment/baz"]).To(BeTrue())
		})

		It("should handle extra whitespace and commas", func() {
			result := engine.ParseDisabledResources("  ConfigMap/foo  ,  ,  Secret/bar  , ")
			Expect(result).To(HaveLen(2))
			Expect(result["ConfigMap/foo"]).To(BeTrue())
			Expect(result["Secret/bar"]).To(BeTrue())
		})

		It("should handle complex resource names", func() {
			result := engine.ParseDisabledResources("KubeDescheduler/cluster, MachineConfig/50-swap-enable")
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
				createUnstructuredResource("ConfigMap", "config-1", "default"),
				createUnstructuredResource("ConfigMap", "config-2", "default"),
				createUnstructuredResource("Secret", "secret-1", "default"),
				createUnstructuredResource("Deployment", "deploy-1", "default"),
			}
		})

		It("should return all assets when disabled map is empty", func() {
			disabled := make(map[string]bool)
			result := engine.FilterExcludedAssets(assets, disabled)
			Expect(result).To(HaveLen(4))
		})

		It("should exclude specified resource", func() {
			disabled := engine.ParseDisabledResources("ConfigMap/config-1")
			result := engine.FilterExcludedAssets(assets, disabled)

			Expect(result).To(HaveLen(3))

			// Verify the right one was excluded
			names := make([]string, 0)
			for _, asset := range result {
				key := asset.GetKind() + "/" + asset.GetName()
				names = append(names, key)
			}

			Expect(names).To(ConsistOf(
				"ConfigMap/config-2",
				"Secret/secret-1",
				"Deployment/deploy-1",
			))
		})

		It("should exclude multiple resources", func() {
			disabled := engine.ParseDisabledResources("ConfigMap/config-1, Secret/secret-1")
			result := engine.FilterExcludedAssets(assets, disabled)

			Expect(result).To(HaveLen(2))

			names := make([]string, 0)
			for _, asset := range result {
				key := asset.GetKind() + "/" + asset.GetName()
				names = append(names, key)
			}

			Expect(names).To(ConsistOf(
				"ConfigMap/config-2",
				"Deployment/deploy-1",
			))
		})

		It("should exclude all when all are specified", func() {
			disabled := engine.ParseDisabledResources("ConfigMap/config-1, ConfigMap/config-2, Secret/secret-1, Deployment/deploy-1")
			result := engine.FilterExcludedAssets(assets, disabled)

			Expect(result).To(BeEmpty())
		})

		It("should keep all when none match", func() {
			disabled := engine.ParseDisabledResources("ConfigMap/nonexistent, Service/test")
			result := engine.FilterExcludedAssets(assets, disabled)

			Expect(result).To(HaveLen(4))
		})

		It("should handle nil disabled map", func() {
			result := engine.FilterExcludedAssets(assets, nil)
			Expect(result).To(HaveLen(4))
		})

		It("should be case-sensitive for Kind and Name", func() {
			disabled := engine.ParseDisabledResources("configmap/config-1") // lowercase kind
			result := engine.FilterExcludedAssets(assets, disabled)

			// Should NOT exclude because Kind is case-sensitive
			Expect(result).To(HaveLen(4))
		})
	})

	Describe("IsResourceExcluded", func() {
		It("should return false for empty disabled map", func() {
			disabled := make(map[string]bool)
			Expect(engine.IsResourceExcluded("ConfigMap", "test", disabled)).To(BeFalse())
		})

		It("should return true for excluded resource", func() {
			disabled := engine.ParseDisabledResources("ConfigMap/test")
			Expect(engine.IsResourceExcluded("ConfigMap", "test", disabled)).To(BeTrue())
		})

		It("should return false for non-excluded resource", func() {
			disabled := engine.ParseDisabledResources("ConfigMap/test")
			Expect(engine.IsResourceExcluded("ConfigMap", "other", disabled)).To(BeFalse())
			Expect(engine.IsResourceExcluded("Secret", "test", disabled)).To(BeFalse())
		})

		It("should be case-sensitive", func() {
			disabled := engine.ParseDisabledResources("ConfigMap/test")
			Expect(engine.IsResourceExcluded("configmap", "test", disabled)).To(BeFalse())
			Expect(engine.IsResourceExcluded("ConfigMap", "Test", disabled)).To(BeFalse())
		})

		It("should handle nil disabled map", func() {
			Expect(engine.IsResourceExcluded("ConfigMap", "test", nil)).To(BeFalse())
		})
	})

	Describe("Real-world scenarios", func() {
		It("should handle KubeDescheduler exclusion", func() {
			annotation := "KubeDescheduler/cluster"
			disabled := engine.ParseDisabledResources(annotation)

			assets := []*unstructured.Unstructured{
				createUnstructuredResource("KubeDescheduler", "cluster", ""),
				createUnstructuredResource("HyperConverged", "kubevirt-hyperconverged", "openshift-cnv"),
			}

			result := engine.FilterExcludedAssets(assets, disabled)

			Expect(result).To(HaveLen(1))
			Expect(result[0].GetKind()).To(Equal("HyperConverged"))
		})

		It("should handle MachineConfig exclusion", func() {
			annotation := "MachineConfig/50-swap-enable"
			disabled := engine.ParseDisabledResources(annotation)

			assets := []*unstructured.Unstructured{
				createUnstructuredResource("MachineConfig", "50-swap-enable", ""),
				createUnstructuredResource("MachineConfig", "51-pci-passthrough", ""),
			}

			result := engine.FilterExcludedAssets(assets, disabled)

			Expect(result).To(HaveLen(1))
			Expect(result[0].GetName()).To(Equal("51-pci-passthrough"))
		})

		It("should handle multiple feature exclusions", func() {
			annotation := "KubeDescheduler/cluster, MachineConfig/50-swap-enable, PersesDataSource/virt-metrics"
			disabled := engine.ParseDisabledResources(annotation)

			assets := []*unstructured.Unstructured{
				createUnstructuredResource("KubeDescheduler", "cluster", ""),
				createUnstructuredResource("MachineConfig", "50-swap-enable", ""),
				createUnstructuredResource("MachineConfig", "51-pci-passthrough", ""),
				createUnstructuredResource("PersesDataSource", "virt-metrics", "openshift-cnv"),
				createUnstructuredResource("HyperConverged", "kubevirt-hyperconverged", "openshift-cnv"),
			}

			result := engine.FilterExcludedAssets(assets, disabled)

			// Should have 2 resources left (MachineConfig and HyperConverged)
			Expect(result).To(HaveLen(2))

			names := make([]string, 0)
			for _, asset := range result {
				names = append(names, asset.GetName())
			}

			Expect(names).To(ConsistOf(
				"51-pci-passthrough",
				"kubevirt-hyperconverged",
			))
		})
	})
})

// Helper function to create unstructured resources for testing
func createUnstructuredResource(kind, name, namespace string) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetKind(kind)
	obj.SetName(name)
	if namespace != "" {
		obj.SetNamespace(namespace)
	}
	obj.SetAPIVersion("v1")
	return obj
}
