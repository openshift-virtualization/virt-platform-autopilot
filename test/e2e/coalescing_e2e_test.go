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

package e2e

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// coalescingAsset describes a MachineConfig whose coalescing behaviour is
// exercised by the Kind coalescing suite. Add entries here to extend coverage to more MachineConfigs.
type coalescingAsset struct {
	Name     string
	TamperFn func() // creates detectable spec drift without relying on MCO
}

var coalescingAssetsUnderTest = []coalescingAsset{
	{Name: swapMcName, TamperFn: tamperSwapMCIgnitionVersion},
	{Name: psiWorkerMCName, TamperFn: tamperPSIWorkerMCKernelArg},
}

const (
	mcCoalescingBypassAnnotation = "platform.kubevirt.io/bypass-mcp-rollout-coalescing"
	machineConfigPoolCRDName     = "machineconfigpools.machineconfiguration.openshift.io"
	machineConfigPoolCRDFile     = "test/crds/openshift/machineconfigpool-crd.yaml"
	// workerMCPName is the test MachineConfigPool created in each coalescing context.
	// It uses a test-specific prefix to avoid collision with any pre-existing pools.
	workerMCPName   = "test-coalescing-worker"
	infraMCPName    = "test-coalescing-infra"
	psiWorkerMCName = "99-openshift-machineconfig-worker-psi-karg"
)

// Coalescing tests manipulate MachineConfigPool status directly via a fake CRD.
// They are incompatible with real OpenShift clusters where MCO owns MCP state.
var _ = Describe("Kind: MachineConfig Rollout Coalescing", Ordered, func() {

	BeforeAll(func() {
		if isOpenShiftCluster() {
			Skip("coalescing tests run on Kind only — MCP status manipulation conflicts with real MCO")
		}
		ensureHCOExists()
		patchAutopilotAndWait(autopilotEnabled)

		By("installing MachineConfigPool CRD for coalescing tests")
		installCRDFromFile(machineConfigPoolCRDFile)
		waitForCRDEstablished(machineConfigPoolCRDName)

		By("waiting for all coalescing assets to be compliant")
		for _, asset := range coalescingAssetsUnderTest {
			waitForMCComplianceStatus(asset.Name, 1.0)
		}
	})

	AfterAll(func() {
		deleteStagingConfigMap()
		removeCRD(machineConfigPoolCRDName)
	})

	// All coalescing assets are tampered in parallel; each must reach compliance_status=2,
	// get a staging ConfigMap entry with correct fields, and emit the staged metric.
	// Drift must NOT be corrected while MCP remains stable.
	Context("MC update staged while MCP stable (Updating=False)", Ordered, func() {
		var stagedSince time.Time

		BeforeAll(func() {
			By("creating test worker MCP with Updating=False")
			createTestMCP(workerMCPName)
			By("tampering all coalescing assets in parallel to create concurrent drift")
			tamperAllAssetsParallel(coalescingAssetsUnderTest)
			stagedSince = time.Now()
			touchHCO()
		})

		AfterAll(func() {
			cleanupCoalescingContext(workerMCPName)
		})

		for _, asset := range coalescingAssetsUnderTest {
			asset := asset
			It(fmt.Sprintf("should set compliance_status=2 (staged/deferred) for %s", asset.Name), func() {
				Eventually(func() float64 {
					return captureAssetMetrics("MachineConfig", asset.Name, "").ComplianceStatus
				}, timeout, interval).Should(Equal(2.0),
					"compliance_status should be 2 (deferred) while MCP is stable")
			})

			It(fmt.Sprintf("should create a staging ConfigMap entry with correct fields for %s", asset.Name), func() {
				var entry *stagingEntry
				Eventually(func() bool {
					entry = getStagingEntry(asset.Name)
					return entry != nil
				}, timeout, interval).Should(BeTrue(),
					"staging ConfigMap should have an entry for "+asset.Name)
				Expect(entry.DesiredHash).NotTo(BeEmpty(),
					"staging entry must have a non-empty desiredHash")
				Expect(entry.StagedAt).NotTo(BeZero(),
					"staging entry must have a non-zero stagedAt")
				Expect(entry.MatchingPools).To(ContainElement(workerMCPName),
					"staging entry must list the worker pool")
			})

			It(fmt.Sprintf("should emit machineconfig_update_staged=1 for %s in the worker pool", asset.Name), func() {
				Eventually(func() float64 {
					return findMCStagedMetric(asset.Name, workerMCPName, "kubevirt_autopilot_machineconfig_update_staged")
				}, timeout, interval).Should(Equal(1.0),
					"machineconfig_update_staged metric should be 1 for "+workerMCPName)
			})

			It(fmt.Sprintf("should emit a MachineConfigUpdateStaged event for %s", asset.Name), func() {
				Eventually(func() int {
					return len(findEvents(EventFilter{Reason: "MachineConfigUpdateStaged", Since: stagedSince, Name: asset.Name}))
				}, timeout, interval).Should(BeNumerically(">", 0),
					"MachineConfigUpdateStaged event must be emitted when "+asset.Name+" is staged")
			})
		}

		It("should have staging ConfigMap entries for all assets simultaneously", func() {
			Eventually(func(g Gomega) {
				cm := &corev1.ConfigMap{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Namespace: operatorNamespace,
					Name:      "virt-platform-autopilot-mc-staging",
				}, cm)).To(Succeed())
				g.Expect(cm.Data).To(And(
					HaveKey(swapMcName),
					HaveKey(psiWorkerMCName),
					HaveLen(len(coalescingAssetsUnderTest)),
				))
			}, timeout, interval).Should(Succeed(),
				"staging ConfigMap must contain one entry per coalescing asset simultaneously")
		})

		It("should keep all assets at compliance_status=2 across subsequent reconciles (drift not corrected)", func() {
			touchHCO()
			for _, asset := range coalescingAssetsUnderTest {
				asset := asset
				Consistently(func() float64 {
					return captureAssetMetrics("MachineConfig", asset.Name, "").ComplianceStatus
				}, consistentlyDuration, consistentlyInterval).Should(Equal(2.0),
					"drift must remain uncorrected while MCP is not Updating")
			}
		})
	})

	// All assets are staged first; transitioning the MCP releases all of them.
	Context("Released when MCP transitions to Updating=True", Ordered, func() {
		var releasedSince time.Time

		BeforeAll(func() {
			By("creating test worker MCP with Updating=False")
			createTestMCP(workerMCPName)
			By("tampering all coalescing assets in parallel to create concurrent drift")
			tamperAllAssetsParallel(coalescingAssetsUnderTest)
			touchHCO()
			for _, asset := range coalescingAssetsUnderTest {
				waitForMCComplianceStatus(asset.Name, 2.0)
			}

			By("setting MCP Updating=True to trigger release")
			releasedSince = time.Now()
			setMCPUpdating(workerMCPName, true)
		})

		AfterAll(func() {
			cleanupCoalescingContext(workerMCPName)
		})

		for _, asset := range coalescingAssetsUnderTest {
			asset := asset
			It(fmt.Sprintf("should correct the drift for %s (compliance_status=1)", asset.Name), func() {
				Eventually(func() float64 {
					return captureAssetMetrics("MachineConfig", asset.Name, "").ComplianceStatus
				}, 2*timeout, interval).Should(Equal(1.0),
					"compliance_status should return to 1 after MCP transitions to Updating")
			})

			It(fmt.Sprintf("should clear the machineconfig_update_staged metric for %s", asset.Name), func() {
				Eventually(func() float64 {
					return findMCStagedMetric(asset.Name, workerMCPName, "kubevirt_autopilot_machineconfig_update_staged")
				}, timeout, interval).Should(Equal(-1.0),
					"machineconfig_update_staged metric should be absent after release")
			})

			It(fmt.Sprintf("should emit a MachineConfigUpdateReleased event for %s", asset.Name), func() {
				Eventually(func() int {
					return len(findEvents(EventFilter{Reason: "MachineConfigUpdateReleased", Since: releasedSince, Name: asset.Name}))
				}, timeout, interval).Should(BeNumerically(">", 0),
					"MachineConfigUpdateReleased event must be emitted when "+asset.Name+" is released")
			})
		}

		It("should remove all staging ConfigMap entries simultaneously", func() {
			Eventually(func(g Gomega) {
				cm := &corev1.ConfigMap{}
				if err := k8sClient.Get(ctx, types.NamespacedName{
					Namespace: operatorNamespace,
					Name:      "virt-platform-autopilot-mc-staging",
				}, cm); err != nil {
					return // CM fully deleted also satisfies no-entries
				}
				g.Expect(cm.Data).To(BeEmpty())
			}, timeout, interval).Should(Succeed(),
				"staging ConfigMap must be empty after all updates are released")
		})
	})

	// The bypass annotation must be preserved on the live MC after the apply.
	Context("Bypass annotation causes immediate apply", Ordered, func() {
		BeforeAll(func() {
			By("creating test worker MCP with Updating=False")
			createTestMCP(workerMCPName)

			By("setting bypass annotation on all coalescing assets")
			for _, asset := range coalescingAssetsUnderTest {
				setAnnotation(machineConfigGVK, asset.Name, "", mcCoalescingBypassAnnotation, "true")
			}

			By("tampering all coalescing assets in parallel to create drift")
			tamperAllAssetsParallel(coalescingAssetsUnderTest)
			touchHCO()
		})

		AfterAll(func() {
			for _, asset := range coalescingAssetsUnderTest {
				removeAnnotation(machineConfigGVK, asset.Name, "", mcCoalescingBypassAnnotation)
			}
			cleanupCoalescingContext(workerMCPName)
		})

		for _, asset := range coalescingAssetsUnderTest {
			asset := asset
			It(fmt.Sprintf("should immediately correct the drift for %s (compliance_status=1)", asset.Name), func() {
				Eventually(func() float64 {
					return captureAssetMetrics("MachineConfig", asset.Name, "").ComplianceStatus
				}, timeout, interval).Should(Equal(1.0),
					"bypass annotation must cause immediate apply even when MCP is stable")
			})

			It(fmt.Sprintf("should preserve the bypass annotation on %s after apply", asset.Name), func() {
				Eventually(func() string {
					mc, err := getUnstructuredResource(machineConfigGVK, asset.Name, "")
					if err != nil {
						return ""
					}
					return mc.GetAnnotations()[mcCoalescingBypassAnnotation]
				}, timeout, interval).Should(Equal("true"),
					"bypass annotation must survive the SSA apply and remain on the live MC")
			})
		}

		It("should NOT create any staging ConfigMap entries for any asset", func() {
			Consistently(func(g Gomega) {
				cm := &corev1.ConfigMap{}
				if err := k8sClient.Get(ctx, types.NamespacedName{
					Namespace: operatorNamespace,
					Name:      "virt-platform-autopilot-mc-staging",
				}, cm); err != nil {
					return // CM absent also satisfies no-entries
				}
				g.Expect(cm.Data).To(BeEmpty())
			}, consistentlyDuration, consistentlyInterval).Should(Succeed(),
				"staging ConfigMap must remain empty when bypass annotation is set on all assets")
		})
	})

	// The staging ConfigMap is the durable source; the in-memory mirror is rebuilt
	// on the first reconcile after restart and all metrics are re-emitted with the
	// original stagedAt timestamps.
	Context("Staging survives operator restart", Ordered, func() {
		BeforeAll(func() {
			By("creating test worker MCP with Updating=False and staging all assets")
			createTestMCP(workerMCPName)
			tamperAllAssetsParallel(coalescingAssetsUnderTest)
			touchHCO()
			for _, asset := range coalescingAssetsUnderTest {
				waitForMCComplianceStatus(asset.Name, 2.0)
			}

			By("restarting the operator pod")
			restartOperatorPod()
		})

		AfterAll(func() {
			cleanupCoalescingContext(workerMCPName)
		})

		It("should keep staging ConfigMap entries for all assets simultaneously after restart", func() {
			cm := &corev1.ConfigMap{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Namespace: operatorNamespace,
				Name:      "virt-platform-autopilot-mc-staging",
			}, cm)).To(Succeed())
			Expect(cm.Data).To(And(
				HaveKey(swapMcName),
				HaveKey(psiWorkerMCName),
				HaveLen(len(coalescingAssetsUnderTest)),
			))
		})

		for _, asset := range coalescingAssetsUnderTest {
			asset := asset
			It(fmt.Sprintf("should re-emit machineconfig_update_staged=1 for %s after the first reconcile", asset.Name), func() {
				Eventually(func() float64 {
					return findMCStagedMetric(asset.Name, workerMCPName, "kubevirt_autopilot_machineconfig_update_staged")
				}, timeout, interval).Should(Equal(1.0),
					"staging metric must be re-emitted from ConfigMap state after restart")
			})

			It(fmt.Sprintf("should preserve the original stagedAt in staged_since_seconds for %s after restart", asset.Name), func() {
				entry := getStagingEntry(asset.Name)
				Expect(entry).NotTo(BeNil(), "staging entry must exist in ConfigMap to verify timestamp")
				expectedSecs := float64(entry.StagedAt.Unix())
				Eventually(func() float64 {
					return findMCStagedMetric(asset.Name, workerMCPName, "kubevirt_autopilot_machineconfig_update_staged_since_seconds")
				}, timeout, interval).Should(BeNumerically("==", expectedSecs),
					"staged_since_seconds must reflect original StagedAt from ConfigMap, not the restart time")
			})
		}

		It("should keep all assets at compliance_status=2 after restart (drift not corrected)", func() {
			for _, asset := range coalescingAssetsUnderTest {
				asset := asset
				Consistently(func() float64 {
					return captureAssetMetrics("MachineConfig", asset.Name, "").ComplianceStatus
				}, consistentlyDuration, consistentlyInterval).Should(Equal(2.0),
					"drift must not be corrected after restart while MCP remains stable")
			}
		})
	})

	// to transition to Updating=True releases all staged updates immediately.
	Context("First updating pool releases staged updates (multi-pool)", Ordered, func() {
		BeforeAll(func() {
			By("creating worker and infra MCPs, both Updating=False")
			createTestMCP(workerMCPName)
			createTestMCP(infraMCPName)
			By("tampering all coalescing assets in parallel to create concurrent drift")
			tamperAllAssetsParallel(coalescingAssetsUnderTest)
			touchHCO()
			for _, asset := range coalescingAssetsUnderTest {
				waitForMCComplianceStatus(asset.Name, 2.0)
			}

			By("verifying machineconfig_update_staged=1 for both pools before release")
			for _, asset := range coalescingAssetsUnderTest {
				asset := asset
				EventuallyWithOffset(1, func() bool {
					return findMCStagedMetric(asset.Name, workerMCPName, "kubevirt_autopilot_machineconfig_update_staged") == 1.0 ||
						findMCStagedMetric(asset.Name, infraMCPName, "kubevirt_autopilot_machineconfig_update_staged") == 1.0
				}, timeout, interval).Should(BeTrue(),
					"at least one pool staging metric must be 1 for "+asset.Name+" before release")
			}

			By("transitioning infra pool to Updating=True — this should release all staged updates")
			setMCPUpdating(infraMCPName, true)
		})

		AfterAll(func() {
			cleanupCoalescingContext(workerMCPName, infraMCPName)
		})

		for _, asset := range coalescingAssetsUnderTest {
			asset := asset
			It(fmt.Sprintf("should correct the drift for %s when infra pool transitions to Updating=True", asset.Name), func() {
				Eventually(func() float64 {
					return captureAssetMetrics("MachineConfig", asset.Name, "").ComplianceStatus
				}, 2*timeout, interval).Should(Equal(1.0),
					"the first updating pool must release the staged update regardless of other pools' state")
			})

			It(fmt.Sprintf("should clear all staging metrics for %s after release", asset.Name), func() {
				Eventually(func() bool {
					workerGone := findMCStagedMetric(asset.Name, workerMCPName, "kubevirt_autopilot_machineconfig_update_staged") == -1.0
					infraGone := findMCStagedMetric(asset.Name, infraMCPName, "kubevirt_autopilot_machineconfig_update_staged") == -1.0
					return workerGone && infraGone
				}, timeout, interval).Should(BeTrue(),
					"machineconfig_update_staged metrics must be cleared for all pools after release")
			})
		}
	})
})

// MachineConfigPool is paused. While staged, VirtPlatformAutopilotSyncFailed must
// NOT fire (compliance_status=2 is intentional) and
// VirtPlatformAutopilotMachineConfigUpdateStaged (severity=info) must fire for each
// staged MC. Unpausing the pool releases both corrections in a single rollout window,
// after which VirtPlatformAutopilotMachineConfigUpdateStaged must clear.
var _ = Describe("OCP: two MachineConfig corrections staged while MCP is paused", Ordered, func() {
	var realWorkerMCPName string
	var stagedSince time.Time

	BeforeAll(func() {
		if !isOpenShiftCluster() {
			Skip("requires real OCP cluster with MCO and Prometheus — skipping on Kind")
		}
		ensureHCOExists()
		waitForMCPStable()

		realWorkerMCPName = findRealWorkerMCPName()
		Expect(realWorkerMCPName).NotTo(BeEmpty(), "a worker MachineConfigPool must exist on OCP")

		if !workerMCPHasMachines(realWorkerMCPName) {
			Skip("worker MCP has no machines — skipping on compact cluster")
		}

		By("setting PrometheusRule to unmanaged so alert for-durations can be patched")
		setAnnotation(prometheusRuleGVK, prometheusRuleName, operatorNamespace, modeAnnotation, modeUnmanaged)

		By("reducing alert for-durations to 15s for faster test feedback")
		patchAlertForDurations()

		By("ensuring baseline: PSI MC and swap MC at compliance_status=1")
		Eventually(func() float64 {
			return captureAssetMetrics("MachineConfig", psiWorkerMCName, "").ComplianceStatus
		}, timeout, interval).Should(Equal(1.0), psiWorkerMCName+" must be synced before staging")
		Eventually(func() float64 {
			return captureAssetMetrics("MachineConfig", swapMcName, "").ComplianceStatus
		}, timeout, interval).Should(Equal(1.0), swapMcName+" must be synced before staging")

		// Capture stagedSince before any tamper so the watch-triggered reconcile
		// that emits MachineConfigUpdateStaged is not excluded by the Since filter.
		stagedSince = time.Now()

		By("pausing worker MCP — MCO will re-render but not drain nodes")
		setRealMCPPaused(realWorkerMCPName, true)

		By("tampering PSI MC: removing psi=1 kernel argument")
		tamperPSIWorkerMCKernelArg()

		By("tampering swap MC: downgrading ignition version to 3.4.0")
		tamperSwapMCIgnitionVersion()

		By("triggering reconcile so the operator detects drift")
		touchHCO()

		By("waiting for both MCs to be staged while MCP is paused (Updating=False)")
		waitForMCComplianceStatus(psiWorkerMCName, 2.0)
		waitForMCComplianceStatus(swapMcName, 2.0)
	})

	AfterAll(func() {
		if !isOpenShiftCluster() {
			return
		}
		By("restoring PrometheusRule to managed mode")
		removeAnnotation(prometheusRuleGVK, prometheusRuleName, operatorNamespace, modeAnnotation)

		if realWorkerMCPName != "" {
			setRealMCPPaused(realWorkerMCPName, false)
		}
		// Force-apply via bypass so both MCs return to clean state regardless of
		// where the test failed.
		for _, mcName := range []string{psiWorkerMCName, swapMcName} {
			setAnnotation(machineConfigGVK, mcName, "", mcCoalescingBypassAnnotation, "true")
		}
		touchHCO()
		for _, mcName := range []string{psiWorkerMCName, swapMcName} {
			waitForMCComplianceStatus(mcName, 1.0)
			removeAnnotation(machineConfigGVK, mcName, "", mcCoalescingBypassAnnotation)
		}
		touchHCO()
		deleteStagingConfigMap()
		waitForMCPStable()
	})

	It("should have both MCs staged with CM entries and metrics", func() {
		for _, mcName := range []string{psiWorkerMCName, swapMcName} {
			mcName := mcName
			entry := getStagingEntry(mcName)
			Expect(entry).NotTo(BeNil(),
				"staging CM must have an entry for "+mcName)
			Expect(entry.DesiredHash).NotTo(BeEmpty(),
				"staging entry for "+mcName+" must have a non-empty desiredHash")
			Expect(entry.StagedAt).NotTo(BeZero(),
				"staging entry for "+mcName+" must have a non-zero stagedAt")
			Expect(entry.MatchingPools).To(ContainElement(realWorkerMCPName),
				"staging entry for "+mcName+" must list the worker pool")
			Expect(findMCStagedMetric(mcName, realWorkerMCPName, "kubevirt_autopilot_machineconfig_update_staged")).To(Equal(1.0),
				"machineconfig_update_staged must be 1 for "+mcName)
		}
	})

	It("should emit MachineConfigUpdateStaged events for both MCs", func() {
		for _, mcName := range []string{psiWorkerMCName, swapMcName} {
			mcName := mcName
			Eventually(func(g Gomega) {
				events := findEvents(EventFilter{Reason: "MachineConfigUpdateStaged", Since: stagedSince, Name: mcName})
				g.Expect(events).NotTo(BeEmpty(),
					"MachineConfigUpdateStaged event must be emitted for "+mcName)
			}, timeout, interval).Should(Succeed())
		}
	})

	It("should NOT fire VirtPlatformAutopilotSyncFailed while both MCs are staged", func() {
		Consistently(func() bool {
			psiSafe := queryAlertNotFiring("VirtPlatformAutopilotSyncFailed", 1, 1,
				"kind", "MachineConfig", "name", psiWorkerMCName)
			swapSafe := queryAlertNotFiring("VirtPlatformAutopilotSyncFailed", 1, 1,
				"kind", "MachineConfig", "name", swapMcName)
			return psiSafe && swapSafe
		}, time.Minute, 10*time.Second).Should(BeTrue(),
			"VirtPlatformAutopilotSyncFailed must NOT fire while updates are staged (compliance_status=2 is expected)")
	})

	It("should fire VirtPlatformAutopilotMachineConfigUpdateStaged for both staged MCs", func() {
		for _, mcName := range []string{swapMcName, psiWorkerMCName} {
			mcName := mcName
			Eventually(func() map[string]string {
				return queryFiringAlert("VirtPlatformAutopilotMachineConfigUpdateStaged", 1, 1,
					"machineconfig", mcName,
					"pool", realWorkerMCPName)
			}, 2*time.Minute, 10*time.Second).Should(And(
				Not(BeNil()),
				HaveKeyWithValue("machineconfig", mcName),
				HaveKeyWithValue("pool", realWorkerMCPName),
			), "VirtPlatformAutopilotMachineConfigUpdateStaged must fire with correct labels for "+mcName)
		}
	})

	It("should release both staged corrections in one rollout window when MCP is unpaused", func() {
		By("unpausing worker MCP — MCO sees pending spec changes and sets Updating=True")
		releasedSince := time.Now()
		setRealMCPPaused(realWorkerMCPName, false)
		touchHCO()

		By("verifying PSI MC correction released and applied (compliance_status=1)")
		Eventually(func() float64 {
			return captureAssetMetrics("MachineConfig", psiWorkerMCName, "").ComplianceStatus
		}, 2*timeout, interval).Should(Equal(1.0),
			psiWorkerMCName+" must be corrected once MCP transitions to Updating")

		By("verifying swap MC correction released and applied (compliance_status=1)")
		Eventually(func() float64 {
			return captureAssetMetrics("MachineConfig", swapMcName, "").ComplianceStatus
		}, 2*timeout, interval).Should(Equal(1.0),
			swapMcName+" must be corrected once MCP transitions to Updating")

		By("verifying staging CM entries cleared for both MCs")
		Eventually(stagingEntryExists(psiWorkerMCName), timeout, interval).Should(BeFalse(),
			"staging CM entry for "+psiWorkerMCName+" must be cleared after release")
		Eventually(stagingEntryExists(swapMcName), timeout, interval).Should(BeFalse(),
			"staging CM entry for "+swapMcName+" must be cleared after release")

		By("verifying machineconfig_update_staged metrics cleared for both MCs")
		Eventually(func() bool {
			psiGone := findMCStagedMetric(psiWorkerMCName, realWorkerMCPName, "kubevirt_autopilot_machineconfig_update_staged") == -1.0
			swapGone := findMCStagedMetric(swapMcName, realWorkerMCPName, "kubevirt_autopilot_machineconfig_update_staged") == -1.0
			return psiGone && swapGone
		}, timeout, interval).Should(BeTrue(),
			"machineconfig_update_staged must be cleared for both MCs after rollout window")

		By("verifying MachineConfigUpdateReleased events emitted for both MCs")
		for _, mcName := range []string{psiWorkerMCName, swapMcName} {
			mcName := mcName
			Eventually(func() int {
				return len(findEvents(EventFilter{Reason: "MachineConfigUpdateReleased", Since: releasedSince, Name: mcName}))
			}, timeout, interval).Should(BeNumerically(">", 0),
				"MachineConfigUpdateReleased event must be emitted for "+mcName)
		}
	})

	It("should clear VirtPlatformAutopilotMachineConfigUpdateStaged alert after rollout", func() {
		Eventually(func() bool {
			psiGone := queryAlertNotFiring("VirtPlatformAutopilotMachineConfigUpdateStaged", 1, 1,
				"machineconfig", psiWorkerMCName)
			swapGone := queryAlertNotFiring("VirtPlatformAutopilotMachineConfigUpdateStaged", 1, 1,
				"machineconfig", swapMcName)
			return psiGone && swapGone
		}, 2*time.Minute, 10*time.Second).Should(BeTrue(),
			"VirtPlatformAutopilotMachineConfigUpdateStaged must clear for both MCs after rollout")
	})

	It("should return to stable MCP state after rollout", func() {
		waitForMCPStable()
	})
})

// tamperPSIWorkerMCKernelArg patches spec.kernelArguments to empty on the PSI
// worker MachineConfig, removing the psi=1 kernel argument. The operator manages
// this field via SSA and will detect the drift on the next reconcile.
func tamperPSIWorkerMCKernelArg() {
	ref := unstructuredRef(machineConfigGVK, psiWorkerMCName, "")
	EventuallyWithOffset(1, func() error {
		return k8sClient.Patch(ctx, ref,
			client.RawPatch(types.MergePatchType, []byte(`{"spec":{"kernelArguments":[]}}`)))
	}, timeout, interval).Should(Succeed(),
		"should remove psi=1 kernel argument from "+psiWorkerMCName)
}

// tamperSwapMCIgnitionVersion patches spec.config.ignition.version on the swap MC
// from 3.5.0 to 3.4.0. The operator manages this field via SSA and will detect the
// drift on the next reconcile.
func tamperSwapMCIgnitionVersion() {
	ref := unstructuredRef(machineConfigGVK, swapMcName, "")
	EventuallyWithOffset(1, func() error {
		return k8sClient.Patch(ctx, ref,
			client.RawPatch(types.MergePatchType, []byte(`{"spec":{"config":{"ignition":{"version":"3.4.0"}}}}`)))
	}, timeout, interval).Should(Succeed(),
		"should tamper swap MC ignition version to 3.4.0")
}
