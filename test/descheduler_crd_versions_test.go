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
	"context"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	"github.com/kubevirt/virt-platform-autopilot/pkg/assets"
	pkgcontext "github.com/kubevirt/virt-platform-autopilot/pkg/context"
	"github.com/kubevirt/virt-platform-autopilot/pkg/engine"
)

var _ = Describe("Descheduler CRD Version Compatibility", func() {
	var (
		testCtx   context.Context
		ns        *unstructured.Unstructured
		hco       *unstructured.Unstructured
		renderer  *engine.Renderer
		assetMeta *assets.AssetMetadata
	)

	BeforeEach(func() {
		testCtx = context.Background()

		// Create test namespace
		ns = &unstructured.Unstructured{}
		ns.SetGroupVersionKind(nsGVK)
		ns.SetName("test-descheduler-" + randString())
		Expect(k8sClient.Create(testCtx, ns)).To(Succeed())

		// Create mock HCO with custom eviction limits
		hco = pkgcontext.NewMockHCO("kubevirt-hyperconverged", ns.GetName())
		// Set custom eviction limits to verify they're extracted correctly (v1 paths)
		hcoSpec := map[string]any{
			"virtualization": map[string]any{
				"liveMigrationConfig": map[string]any{
					"parallelMigrationsPerCluster":      int64(45),
					"parallelOutboundMigrationsPerNode": int64(20),
				},
			},
		}
		hco.Object["spec"] = hcoSpec
		hco.SetAnnotations(map[string]string{
			"platform.kubevirt.io/enable-loadaware": "true",
		})
		Expect(k8sClient.Create(testCtx, hco)).To(Succeed())

		// Initialize renderer with client for CRD introspection
		loader := assets.NewLoader()
		renderer = engine.NewRenderer(loader)
		renderer.SetClient(k8sClient) // Required for crdHasEnum and other CRD queries

		// Load descheduler asset metadata
		registry, err := assets.NewRegistry(loader)
		Expect(err).NotTo(HaveOccurred())
		assetMeta, err = registry.GetAsset("descheduler-loadaware")
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		// Cleanup namespace (cascade deletes HCO)
		if ns != nil {
			_ = k8sClient.Delete(testCtx, ns)
		}
	})

	// Test helper to install a specific CRD version
	installDeschedulerCRD := func(version string) {
		By("Installing KubeDescheduler CRD version " + version)
		crdPath := filepath.Join("testdata", "kube-descheduler-operator-"+version+".crd.yaml")

		// Read CRD file
		data, err := os.ReadFile(crdPath)
		Expect(err).NotTo(HaveOccurred(), "Failed to read CRD file: %s", crdPath)

		// Parse and install CRD
		crd := &unstructured.Unstructured{}
		Expect(yaml.Unmarshal(data, crd)).To(Succeed())
		Expect(crd.GetKind()).To(Equal("CustomResourceDefinition"))

		crdName := crd.GetName()

		// Delete existing CRD if present
		existing := &unstructured.Unstructured{}
		existing.SetGroupVersionKind(crd.GroupVersionKind())
		existing.SetName(crdName)
		_ = k8sClient.Delete(testCtx, existing)

		// Wait for deletion to complete
		Eventually(func() bool {
			err := k8sClient.Get(testCtx, client.ObjectKey{Name: crdName}, existing)
			return err != nil
		}, "30s", "500ms").Should(BeTrue(), "CRD should be deleted")

		// Install new CRD version
		Expect(k8sClient.Create(testCtx, crd)).To(Succeed())

		// Wait for CRD to be established
		ExpectCRDInstalled(testCtx, k8sClient, crdName)
	}

	// Test helper to render and validate the descheduler asset
	renderAndValidate := func(expectedProfile string, expectEvictionsInBackground bool) *unstructured.Unstructured {
		// Build render context
		renderCtx := &pkgcontext.RenderContext{
			HCO:      hco,
			Hardware: &pkgcontext.HardwareContext{},
		}

		// Render the descheduler asset
		rendered, err := renderer.RenderAsset(assetMeta, renderCtx)
		Expect(err).NotTo(HaveOccurred(), "Template rendering should succeed")
		Expect(rendered).NotTo(BeNil(), "Rendered asset should not be nil")

		// Validate basic structure
		Expect(rendered.GetKind()).To(Equal("KubeDescheduler"))
		Expect(rendered.GetName()).To(Equal("cluster"))
		Expect(rendered.GetNamespace()).To(Equal("openshift-kube-descheduler-operator"))

		// Validate spec fields
		spec, found, err := unstructured.NestedMap(rendered.Object, "spec")
		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeTrue(), "spec should exist")

		// Check profile selection
		profiles, found, err := unstructured.NestedStringSlice(rendered.Object, "spec", "profiles")
		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeTrue(), "spec.profiles should exist")
		Expect(profiles).To(HaveLen(1), "Should have exactly one profile")
		Expect(profiles[0]).To(Equal(expectedProfile), "Should use correct profile")

		// Check eviction limits from HCO spec
		totalLimit, found, err := unstructured.NestedInt64(rendered.Object, "spec", "evictionLimits", "total")
		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeTrue(), "spec.evictionLimits.total should exist")
		Expect(totalLimit).To(Equal(int64(45)), "Should use HCO parallelMigrationsPerCluster value")

		nodeLimit, found, err := unstructured.NestedInt64(rendered.Object, "spec", "evictionLimits", "node")
		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeTrue(), "spec.evictionLimits.node should exist")
		Expect(nodeLimit).To(Equal(int64(20)), "Should use HCO parallelOutboundMigrationsPerNode value")

		// Check management state
		mgmtState, found, err := unstructured.NestedString(rendered.Object, "spec", "managementState")
		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeTrue(), "spec.managementState should exist")
		Expect(mgmtState).To(Equal("Managed"))

		// Check profileCustomizations for evictionsInBackground
		if expectEvictionsInBackground {
			evictInBg, found, err := unstructured.NestedBool(rendered.Object, "spec", "profileCustomizations", "devEnableEvictionsInBackground")
			Expect(err).NotTo(HaveOccurred())
			Expect(found).To(BeTrue(), "spec.profileCustomizations.devEnableEvictionsInBackground should exist")
			Expect(evictInBg).To(BeTrue(), "devEnableEvictionsInBackground should be true for LongLifecycle")
		} else {
			// profileCustomizations might not exist or shouldn't have devEnableEvictionsInBackground
			_, found, _ := unstructured.NestedMap(spec, "profileCustomizations")
			if found {
				evictInBg, found, err := unstructured.NestedBool(rendered.Object, "spec", "profileCustomizations", "devEnableEvictionsInBackground")
				if found {
					Expect(err).NotTo(HaveOccurred())
					Expect(evictInBg).To(BeFalse(), "devEnableEvictionsInBackground should not be true")
				}
			}
		}

		return rendered
	}

	Describe("OpenShift 4.17 (Legacy profiles only)", func() {
		It("should use LongLifecycle profile and enable evictionsInBackground", func() {
			installDeschedulerCRD("4-17")
			renderAndValidate("LongLifecycle", true)
		})
	})

	Describe("OpenShift 4.19 (DevKubeVirtRelieveAndMigrate available)", func() {
		It("should use DevKubeVirtRelieveAndMigrate profile", func() {
			installDeschedulerCRD("4-19")
			renderAndValidate("DevKubeVirtRelieveAndMigrate", false)
		})
	})

	Describe("OpenShift 4.21 (KubeVirtRelieveAndMigrate available)", func() {
		It("should use KubeVirtRelieveAndMigrate profile (preferred over Dev variant)", func() {
			installDeschedulerCRD("4-21")
			renderAndValidate("KubeVirtRelieveAndMigrate", false)
		})
	})

	Describe("CRD version upgrade scenario", func() {
		It("should adapt profile selection when CRD is upgraded", func() {
			By("Starting with 4.17 CRD")
			installDeschedulerCRD("4-17")
			rendered417 := renderAndValidate("LongLifecycle", true)

			By("Upgrading to 4.19 CRD")
			installDeschedulerCRD("4-19")
			rendered419 := renderAndValidate("DevKubeVirtRelieveAndMigrate", false)

			// Verify the profile actually changed
			profiles417, _, _ := unstructured.NestedStringSlice(rendered417.Object, "spec", "profiles")
			profiles419, _, _ := unstructured.NestedStringSlice(rendered419.Object, "spec", "profiles")
			Expect(profiles417[0]).NotTo(Equal(profiles419[0]), "Profile should change after CRD upgrade")

			By("Upgrading to 4.21 CRD")
			installDeschedulerCRD("4-21")
			rendered421 := renderAndValidate("KubeVirtRelieveAndMigrate", false)

			// Verify we now use the GA profile
			profiles421, _, _ := unstructured.NestedStringSlice(rendered421.Object, "spec", "profiles")
			Expect(profiles421[0]).To(Equal("KubeVirtRelieveAndMigrate"))
			Expect(profiles421[0]).NotTo(Equal(profiles419[0]), "Should upgrade from Dev to GA profile")
		})
	})

	Describe("Eviction limits extraction from HCO", func() {
		It("should use default values when HCO spec is empty", func() {
			installDeschedulerCRD("4-21")

			// Create HCO without liveMigrationConfig
			emptyHCO := pkgcontext.NewMockHCO("test-hco", ns.GetName())
			emptyHCO.SetAnnotations(map[string]string{
				"platform.kubevirt.io/enable-loadaware": "true",
			})

			renderCtx := &pkgcontext.RenderContext{
				HCO:      emptyHCO,
				Hardware: &pkgcontext.HardwareContext{},
			}

			rendered, err := renderer.RenderAsset(assetMeta, renderCtx)
			Expect(err).NotTo(HaveOccurred())

			// Should use defaults (5 and 2)
			totalLimit, _, _ := unstructured.NestedInt64(rendered.Object, "spec", "evictionLimits", "total")
			nodeLimit, _, _ := unstructured.NestedInt64(rendered.Object, "spec", "evictionLimits", "node")
			Expect(totalLimit).To(Equal(int64(5)), "Should use default total limit")
			Expect(nodeLimit).To(Equal(int64(2)), "Should use default node limit")
		})

		It("should extract partial values with defaults for missing fields", func() {
			installDeschedulerCRD("4-21")

			// Create HCO with only total limit set
			partialHCO := pkgcontext.NewMockHCO("test-hco", ns.GetName())
			partialHCO.Object["spec"] = map[string]any{
				"virtualization": map[string]any{
					"liveMigrationConfig": map[string]any{
						"parallelMigrationsPerCluster": int64(100),
						// parallelOutboundMigrationsPerNode omitted
					},
				},
			}
			partialHCO.SetAnnotations(map[string]string{
				"platform.kubevirt.io/enable-loadaware": "true",
			})

			renderCtx := &pkgcontext.RenderContext{
				HCO:      partialHCO,
				Hardware: &pkgcontext.HardwareContext{},
			}

			rendered, err := renderer.RenderAsset(assetMeta, renderCtx)
			Expect(err).NotTo(HaveOccurred())

			totalLimit, _, _ := unstructured.NestedInt64(rendered.Object, "spec", "evictionLimits", "total")
			nodeLimit, _, _ := unstructured.NestedInt64(rendered.Object, "spec", "evictionLimits", "node")
			Expect(totalLimit).To(Equal(int64(100)), "Should use HCO total limit")
			Expect(nodeLimit).To(Equal(int64(2)), "Should use default node limit when not specified")
		})
	})
})
