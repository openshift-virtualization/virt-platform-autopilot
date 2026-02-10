package test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

var _ = Describe("CRD Lifecycle Scenarios", func() {
	Context("when CRDs are missing", func() {
		It("should start successfully without optional CRDs", func() {
			By("verifying only core CRDs are installed")
			Expect(IsCRDInstalled(ctx, k8sClient, "hyperconvergeds.hco.kubevirt.io")).To(BeTrue())

			By("verifying optional CRDs are NOT installed")
			Expect(IsCRDInstalled(ctx, k8sClient, "machineconfigs.machineconfiguration.openshift.io")).To(BeFalse())
			Expect(IsCRDInstalled(ctx, k8sClient, "nodehealthchecks.remediation.medik8s.io")).To(BeFalse())
		})

		It("should handle missing CRDs gracefully when reconciling", func() {
			// This test would start the controller and verify it doesn't crash
			// when trying to create resources whose CRDs don't exist

			By("attempting to create a resource without its CRD")
			obj := &unstructured.Unstructured{}
			obj.SetAPIVersion("machineconfiguration.openshift.io/v1")
			obj.SetKind("MachineConfig")
			obj.SetName("test-mc")
			obj.SetNamespace("default")

			By("expecting the create to fail with 'no matches for kind' error")
			err := k8sClient.Create(ctx, obj)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("no matches for kind"))
		})
	})

	Context("when CRDs are dynamically installed", func() {
		It("should detect and use newly installed CRDs", func() {
			By("verifying MachineConfig CRD is not installed initially")
			ExpectCRDNotInstalled(ctx, k8sClient, "machineconfigs.machineconfiguration.openshift.io")

			By("dynamically installing OpenShift CRDs")
			err := InstallCRDs(ctx, k8sClient, CRDSetOpenShift)
			Expect(err).NotTo(HaveOccurred())

			// Cleanup CRDs after test to avoid pollution
			DeferCleanup(func() {
				_ = UninstallCRDs(ctx, k8sClient, CRDSetOpenShift)
			})

			By("waiting for CRD to be established")
			err = WaitForCRD(ctx, k8sClient, "machineconfigs.machineconfiguration.openshift.io", 10*time.Second)
			Expect(err).NotTo(HaveOccurred())

			By("verifying we can now create MachineConfig resources")
			obj := &unstructured.Unstructured{}
			obj.SetAPIVersion("machineconfiguration.openshift.io/v1")
			obj.SetKind("MachineConfig")
			obj.SetName("test-mc-dynamic")
			obj.SetNamespace("default")

			// Should succeed now that CRD is installed
			err = k8sClient.Create(ctx, obj)
			Expect(err).NotTo(HaveOccurred())

			// Cleanup
			err = k8sClient.Delete(ctx, obj)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should handle multiple CRD sets being installed over time", func() {
			By("installing remediation CRDs")
			err := InstallCRDs(ctx, k8sClient, CRDSetRemediation)
			Expect(err).NotTo(HaveOccurred())

			// Cleanup CRDs after test
			DeferCleanup(func() {
				_ = UninstallCRDs(ctx, k8sClient, CRDSetRemediation)
				_ = UninstallCRDs(ctx, k8sClient, CRDSetOperators)
			})

			By("verifying remediation CRDs are available")
			ExpectCRDInstalled(ctx, k8sClient, "nodehealthchecks.remediation.medik8s.io")
			ExpectCRDInstalled(ctx, k8sClient, "selfnoderemediations.self-node-remediation.medik8s.io")
			ExpectCRDInstalled(ctx, k8sClient, "fenceagentsremediations.fence-agents-remediation.medik8s.io")

			By("installing operator CRDs")
			err = InstallCRDs(ctx, k8sClient, CRDSetOperators)
			Expect(err).NotTo(HaveOccurred())

			By("verifying operator CRDs are available")
			ExpectCRDInstalled(ctx, k8sClient, "forkliftcontrollers.forklift.konveyor.io")
			ExpectCRDInstalled(ctx, k8sClient, "metallbs.metallb.io")
		})
	})

	Context("when CRDs are removed", func() {
		BeforeEach(func() {
			// Ensure operators CRDs are installed for this test
			// NOTE: This may take up to 2 minutes in CI due to rate limiting
			// and potential CRD cleanup from previous tests
			err := InstallCRDs(ctx, k8sClient, CRDSetOperators)
			Expect(err).NotTo(HaveOccurred())

			// Cleanup CRDs after each test in this context
			DeferCleanup(func() {
				_ = UninstallCRDs(ctx, k8sClient, CRDSetOperators)
			})
		})

		It("should handle CRD removal gracefully", func() {
			By("verifying CRD is installed")
			ExpectCRDInstalled(ctx, k8sClient, "metallbs.metallb.io")

			By("removing the CRD set")
			err := UninstallCRDs(ctx, k8sClient, CRDSetOperators)
			Expect(err).NotTo(HaveOccurred())

			By("verifying CRD is no longer available")
			Eventually(func() bool {
				return IsCRDInstalled(ctx, k8sClient, "metallbs.metallb.io")
			}, 10*time.Second, 250*time.Millisecond).Should(BeFalse())
		})
	})

	Context("soft dependency handling", func() {
		It("should continue operating when optional CRDs are missing", func() {
			// This test validates the core design principle of "Inform, Don't Crash"
			// The operator should:
			// 1. Log warnings about missing CRDs
			// 2. Skip assets that require those CRDs
			// 3. Continue managing other assets successfully
			// 4. Automatically start managing skipped assets when CRDs appear

			By("starting with minimal CRDs (only HCO)")
			ExpectCRDInstalled(ctx, k8sClient, "hyperconvergeds.hco.kubevirt.io")

			// TODO: Start controller and verify it reconciles successfully
			// even without optional CRDs

			By("adding optional CRDs after controller start")
			err := InstallCRDs(ctx, k8sClient, CRDSetRemediation)
			Expect(err).NotTo(HaveOccurred())

			// Cleanup CRDs after test
			DeferCleanup(func() {
				_ = UninstallCRDs(ctx, k8sClient, CRDSetRemediation)
			})

			// TODO: Verify controller detects new CRDs and starts managing
			// NodeHealthCheck resources automatically
		})
	})
})
