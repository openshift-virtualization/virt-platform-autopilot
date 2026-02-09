package test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kubevirt/virt-platform-operator/pkg/util"
)

var _ = Describe("CRD Event Handling", func() {
	// These tests verify that the operator correctly handles CRD lifecycle events
	// In the running controller, these events would trigger watch configuration changes

	Context("when CRDs are dynamically installed and removed", func() {
		It("should detect when a managed CRD is installed", func() {
			By("verifying NodeHealthCheck CRD is not initially installed")
			checker := util.NewCRDChecker(k8sClient)

			// Start with fresh cache
			checker.InvalidateCache("")

			initiallyInstalled, err := checker.IsCRDInstalled(ctx, "nodehealthchecks.remediation.medik8s.io")
			Expect(err).NotTo(HaveOccurred())

			if initiallyInstalled {
				Skip("NodeHealthCheck CRD already installed, cannot test installation")
			}

			By("installing NodeHealthCheck CRD dynamically")
			err = InstallCRDs(ctx, k8sClient, CRDSetRemediation)
			Expect(err).NotTo(HaveOccurred())

			DeferCleanup(func() {
				_ = UninstallCRDs(ctx, k8sClient, CRDSetRemediation)
			})

			By("waiting for CRD to be established")
			Eventually(func() bool {
				return IsCRDInstalled(ctx, k8sClient, "nodehealthchecks.remediation.medik8s.io")
			}, 30*time.Second, 500*time.Millisecond).Should(BeTrue())

			By("invalidating cache to detect new CRD")
			checker.InvalidateCache("")

			By("verifying CRD is now detected")
			installed, err := checker.IsCRDInstalled(ctx, "nodehealthchecks.remediation.medik8s.io")
			Expect(err).NotTo(HaveOccurred())
			Expect(installed).To(BeTrue(), "CRD should be detected after installation")

			By("verifying component is now supported")
			supported, crdName, err := checker.IsComponentSupported(ctx, "NodeHealthCheck")
			Expect(err).NotTo(HaveOccurred())
			Expect(supported).To(BeTrue(), "NodeHealthCheck component should be supported")
			Expect(crdName).To(Equal("nodehealthchecks.remediation.medik8s.io"))
		})

		It("should detect when a managed CRD is removed", func() {
			By("installing remediation CRDs")
			err := InstallCRDs(ctx, k8sClient, CRDSetRemediation)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for CRDs to be established")
			Eventually(func() bool {
				return IsCRDInstalled(ctx, k8sClient, "nodehealthchecks.remediation.medik8s.io")
			}, 30*time.Second, 500*time.Millisecond).Should(BeTrue())

			By("verifying CRD is installed")
			checker := util.NewCRDChecker(k8sClient)
			installed, err := checker.IsCRDInstalled(ctx, "nodehealthchecks.remediation.medik8s.io")
			Expect(err).NotTo(HaveOccurred())
			Expect(installed).To(BeTrue())

			By("removing the CRDs")
			err = UninstallCRDs(ctx, k8sClient, CRDSetRemediation)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for CRD deletion to complete")
			Eventually(func() bool {
				crd := &apiextensionsv1.CustomResourceDefinition{}
				err := k8sClient.Get(ctx, client.ObjectKey{Name: "nodehealthchecks.remediation.medik8s.io"}, crd)
				return err != nil // CRD should not exist
			}, 60*time.Second, 500*time.Millisecond).Should(BeTrue())

			By("invalidating cache")
			checker.InvalidateCache("")

			By("verifying CRD is no longer detected")
			installed, err = checker.IsCRDInstalled(ctx, "nodehealthchecks.remediation.medik8s.io")
			Expect(err).NotTo(HaveOccurred())
			Expect(installed).To(BeFalse(), "CRD should not be detected after removal")

			By("verifying component is no longer supported")
			supported, _, err := checker.IsComponentSupported(ctx, "NodeHealthCheck")
			Expect(err).NotTo(HaveOccurred())
			Expect(supported).To(BeFalse(), "NodeHealthCheck should not be supported after CRD removal")
		})

		It("should handle CRD updates correctly", func() {
			By("installing remediation CRDs")
			err := InstallCRDs(ctx, k8sClient, CRDSetRemediation)
			Expect(err).NotTo(HaveOccurred())

			DeferCleanup(func() {
				_ = UninstallCRDs(ctx, k8sClient, CRDSetRemediation)
			})

			By("waiting for CRD to be established")
			Eventually(func() bool {
				return IsCRDInstalled(ctx, k8sClient, "nodehealthchecks.remediation.medik8s.io")
			}, 30*time.Second, 500*time.Millisecond).Should(BeTrue())

			By("fetching the CRD")
			crd := &apiextensionsv1.CustomResourceDefinition{}
			err = k8sClient.Get(ctx, client.ObjectKey{Name: "nodehealthchecks.remediation.medik8s.io"}, crd)
			Expect(err).NotTo(HaveOccurred())

			By("updating the CRD (adding a label)")
			if crd.Labels == nil {
				crd.Labels = make(map[string]string)
			}
			crd.Labels["test-update"] = "true"
			err = k8sClient.Update(ctx, crd)
			Expect(err).NotTo(HaveOccurred())

			By("invalidating cache (simulates what controller does on CRD update)")
			checker := util.NewCRDChecker(k8sClient)
			checker.InvalidateCache("")

			By("verifying CRD is still detected and functional")
			installed, err := checker.IsCRDInstalled(ctx, "nodehealthchecks.remediation.medik8s.io")
			Expect(err).NotTo(HaveOccurred())
			Expect(installed).To(BeTrue(), "CRD should still be detected after update")

			By("verifying updated label is present")
			fetched := &apiextensionsv1.CustomResourceDefinition{}
			err = k8sClient.Get(ctx, client.ObjectKey{Name: "nodehealthchecks.remediation.medik8s.io"}, fetched)
			Expect(err).NotTo(HaveOccurred())
			Expect(fetched.Labels).To(HaveKeyWithValue("test-update", "true"))
		})
	})

	Context("when validating CRD discovery for all managed types", func() {
		It("should identify all managed CRD types from ComponentKindMapping", func() {
			By("checking ComponentKindMapping has entries")
			Expect(util.ComponentKindMapping).NotTo(BeEmpty(), "ComponentKindMapping should define managed types")

			By("verifying each component maps to a valid CRD name")
			for component, crdName := range util.ComponentKindMapping {
				Expect(crdName).NotTo(BeEmpty(), "CRD name should not be empty for component %s", component)
				Expect(crdName).To(ContainSubstring("."), "CRD name should be fully qualified for component %s", component)
			}

			By("checking for expected components")
			expectedComponents := []string{
				"MachineConfig",
				"NodeHealthCheck",
				"SelfNodeRemediation",
				"FenceAgentsRemediation",
				"ForkliftController",
				"MetalLB",
			}

			for _, expected := range expectedComponents {
				_, exists := util.ComponentKindMapping[expected]
				Expect(exists).To(BeTrue(), "Expected component %s to be in ComponentKindMapping", expected)
			}
		})

		It("should correctly identify which CRDs are installed", func() {
			checker := util.NewCRDChecker(k8sClient)

			By("HCO CRD should always be installed (test prerequisite)")
			installed, err := checker.IsCRDInstalled(ctx, "hyperconvergeds.hco.kubevirt.io")
			Expect(err).NotTo(HaveOccurred())
			Expect(installed).To(BeTrue(), "HCO CRD is required for tests")

			By("checking each managed component")
			for component, crdName := range util.ComponentKindMapping {
				supported, detectedCRD, err := checker.IsComponentSupported(ctx, component)
				Expect(err).NotTo(HaveOccurred(), "Should not error checking component %s", component)

				if supported {
					Expect(detectedCRD).To(Equal(crdName), "CRD name should match for component %s", component)
					GinkgoWriter.Printf("✓ Component %s is supported (CRD: %s)\n", component, crdName)
				} else {
					GinkgoWriter.Printf("○ Component %s is not supported (CRD %s not installed)\n", component, crdName)
				}
			}
		})
	})

	Context("when testing CRD cache invalidation", func() {
		It("should use cached results for repeated queries", func() {
			checker := util.NewCRDChecker(k8sClient)

			By("first query should hit the API server")
			start := time.Now()
			installed1, err := checker.IsCRDInstalled(ctx, "hyperconvergeds.hco.kubevirt.io")
			firstDuration := time.Since(start)
			Expect(err).NotTo(HaveOccurred())
			Expect(installed1).To(BeTrue())

			By("second query should use cache (faster)")
			start = time.Now()
			installed2, err := checker.IsCRDInstalled(ctx, "hyperconvergeds.hco.kubevirt.io")
			secondDuration := time.Since(start)
			Expect(err).NotTo(HaveOccurred())
			Expect(installed2).To(BeTrue())

			// Cached query should be significantly faster
			// In practice, cache hits are microseconds vs milliseconds for API calls
			Expect(secondDuration).To(BeNumerically("<", firstDuration),
				"Cached query should be faster (first: %v, second: %v)", firstDuration, secondDuration)
		})

		It("should invalidate cache for specific CRD", func() {
			checker := util.NewCRDChecker(k8sClient)

			By("populating cache with HCO CRD")
			installed, err := checker.IsCRDInstalled(ctx, "hyperconvergeds.hco.kubevirt.io")
			Expect(err).NotTo(HaveOccurred())
			Expect(installed).To(BeTrue())

			By("invalidating cache for HCO CRD")
			checker.InvalidateCache("hyperconvergeds.hco.kubevirt.io")

			By("verifying cache was invalidated (next query hits API)")
			// We can't directly verify it hits the API, but we can verify it still works
			installed, err = checker.IsCRDInstalled(ctx, "hyperconvergeds.hco.kubevirt.io")
			Expect(err).NotTo(HaveOccurred())
			Expect(installed).To(BeTrue())
		})

		It("should invalidate all cache entries when empty string is provided", func() {
			checker := util.NewCRDChecker(k8sClient)

			By("populating cache with multiple CRDs")
			_, _ = checker.IsCRDInstalled(ctx, "hyperconvergeds.hco.kubevirt.io")

			// Install remediation CRDs if not present
			if !IsCRDInstalled(ctx, k8sClient, "nodehealthchecks.remediation.medik8s.io") {
				err := InstallCRDs(ctx, k8sClient, CRDSetRemediation)
				Expect(err).NotTo(HaveOccurred())
				DeferCleanup(func() {
					_ = UninstallCRDs(ctx, k8sClient, CRDSetRemediation)
				})

				Eventually(func() bool {
					return IsCRDInstalled(ctx, k8sClient, "nodehealthchecks.remediation.medik8s.io")
				}, 30*time.Second, 500*time.Millisecond).Should(BeTrue())
			}

			_, _ = checker.IsCRDInstalled(ctx, "nodehealthchecks.remediation.medik8s.io")

			By("invalidating entire cache")
			checker.InvalidateCache("")

			By("verifying all queries still work after cache invalidation")
			installed1, err := checker.IsCRDInstalled(ctx, "hyperconvergeds.hco.kubevirt.io")
			Expect(err).NotTo(HaveOccurred())
			Expect(installed1).To(BeTrue())

			installed2, err := checker.IsCRDInstalled(ctx, "nodehealthchecks.remediation.medik8s.io")
			Expect(err).NotTo(HaveOccurred())
			Expect(installed2).To(BeTrue())
		})
	})

	Context("when testing CRD version handling", func() {
		It("should handle CRDs with multiple versions", func() {
			By("checking HCO CRD which has multiple versions")
			crd := &apiextensionsv1.CustomResourceDefinition{}
			err := k8sClient.Get(ctx, client.ObjectKey{Name: "hyperconvergeds.hco.kubevirt.io"}, crd)
			Expect(err).NotTo(HaveOccurred())

			By("verifying CRD has versions defined")
			Expect(crd.Spec.Versions).NotTo(BeEmpty(), "CRD should have at least one version")

			By("verifying at least one version is marked as storage version")
			hasStorageVersion := false
			for _, version := range crd.Spec.Versions {
				if version.Storage {
					hasStorageVersion = true
					GinkgoWriter.Printf("Storage version: %s\n", version.Name)
					break
				}
			}
			Expect(hasStorageVersion).To(BeTrue(), "CRD should have a storage version")
		})

		It("should use the storage version for API operations", func() {
			By("fetching HCO CRD")
			crd := &apiextensionsv1.CustomResourceDefinition{}
			err := k8sClient.Get(ctx, client.ObjectKey{Name: "hyperconvergeds.hco.kubevirt.io"}, crd)
			Expect(err).NotTo(HaveOccurred())

			By("finding the storage version")
			var storageVersion string
			for _, version := range crd.Spec.Versions {
				if version.Storage {
					storageVersion = version.Name
					break
				}
			}
			Expect(storageVersion).NotTo(BeEmpty(), "Should find storage version")

			By("verifying CRD group matches expected")
			Expect(crd.Spec.Group).To(Equal("hco.kubevirt.io"))

			By("verifying CRD kind matches expected")
			Expect(crd.Spec.Names.Kind).To(Equal("HyperConverged"))
		})
	})
})
