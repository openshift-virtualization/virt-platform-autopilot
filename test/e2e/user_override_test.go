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

package e2e

import (
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	patchAnnotation        = "platform.kubevirt.io/patch"
	ignoreFieldsAnnotation = "platform.kubevirt.io/ignore-fields"
)

var _ = Describe("User Override E2E Tests: ", Ordered, ContinueOnFailure, func() {
	var (
		assetsUnderTestAvailable []testAsset
		patchableAssets          []testAsset // non-sensitive kinds (patch allowed)
		sensitiveAssets          []testAsset // MachineConfig, KubeletConfig (patch blocked)
	)

	BeforeAll(func() {
		By("ensuring HCO exists with autopilot enabled")
		ensureHCOExists()
		patchAutopilotAndWait(autopilotEnabled)

		By("triggering reconciliation and waiting for operator to stabilize")
		if isOpenShiftCluster() {
			By("restoring PrometheusRule to managed mode")
			removeAnnotation(prometheusRuleGVK, prometheusRuleName, operatorNamespace, modeAnnotation)
		}
		touchHCO()
		waitForOperatorHealthy()

		By("filtering assets by installed gate CRDs")
		for _, asset := range assetsUnderTest {
			if asset.GateCRD != "" && !crdInstalled(asset.GateCRD) {
				GinkgoWriter.Printf("skipping %s/%s: gate CRD %s not installed\n", asset.GVK.Kind, asset.Name, asset.GateCRD)
				continue
			}
			assetsUnderTestAvailable = append(assetsUnderTestAvailable, asset)
		}
		Expect(assetsUnderTestAvailable).NotTo(BeEmpty(), "at least one asset with gate CRD installed must exist")
		for _, asset := range assetsUnderTestAvailable {
			GinkgoWriter.Printf("available: %s/%s\n", asset.GVK.Kind, asset.Name)
		}

		By("splitting assets into patchable and sensitive")
		for _, asset := range assetsUnderTestAvailable {
			if asset.Sensitive {
				sensitiveAssets = append(sensitiveAssets, asset)
			} else {
				patchableAssets = append(patchableAssets, asset)
			}
		}
		GinkgoWriter.Printf("available=%d, patchable=%d, sensitive=%d\n",
			len(assetsUnderTestAvailable), len(patchableAssets), len(sensitiveAssets))
	})

	AfterAll(func() {
		By("cleaning up all test annotations and labels from all assets")
		for _, asset := range assetsUnderTestAvailable {
			obj, err := getUnstructuredResource(asset.GVK, asset.Name, asset.Namespace)
			if err != nil {
				continue
			}
			annotations := obj.GetAnnotations()
			needsPatch := false
			patchParts := []string{}

			for _, key := range []string{patchAnnotation, ignoreFieldsAnnotation, modeAnnotation, "e2e-patch-test"} {
				if _, exists := annotations[key]; exists {
					patchParts = append(patchParts, fmt.Sprintf("%q:null", key))
					needsPatch = true
				}
			}

			if needsPatch {
				cleanupPatch := []byte(fmt.Sprintf(`{"metadata":{"annotations":{%s}}}`, strings.Join(patchParts, ",")))
				ref := &unstructured.Unstructured{}
				ref.SetGroupVersionKind(asset.GVK)
				ref.SetName(asset.Name)
				ref.SetNamespace(asset.Namespace)
				_ = k8sClient.Patch(ctx, ref, client.RawPatch(types.MergePatchType, cleanupPatch))
			}
		}

		By("touching HCO for final cleanup reconciliation")
		touchHCO()
		waitForOperatorHealthy()
	})

	// --- Context 1: Concurrent valid JSON Patch ---
	Context("Concurrent valid JSON Patch on non-sensitive assets", Ordered, func() {
		const legacyPatchValue = `[{"op":"add","path":"/metadata/annotations/e2e-patch-test","value":"applied"}]`
		var testStartTime time.Time

		BeforeAll(func() {
			if len(patchableAssets) == 0 {
				Skip("no patchable (non-sensitive) assets available")
			}
			testStartTime = time.Now()

			By("applying patch annotation to ALL patchable assets concurrently")
			for _, asset := range patchableAssets {
				patchDoc := legacyPatchValue
				if asset.Override.JSONPointer != "" {
					patchDoc = asset.Override.PatchDoc()
					GinkgoWriter.Printf("patch: %s/%s → override %s\n", asset.GVK.Kind, asset.Name, asset.Override.JSONPointer)
				} else {
					GinkgoWriter.Printf("patch: %s/%s → legacy e2e-patch-test annotation (no Override, no Override spec)\n",
						asset.GVK.Kind, asset.Name)
				}
				setAnnotation(asset.GVK, asset.Name, asset.Namespace, patchAnnotation, patchDoc)
			}

			By("touching HCO to trigger reconciliation")
			touchHCO()
			waitForOperatorHealthy()
		})

		It("should emit PatchApplied events for all patchable assets", func() {
			Skip("Bug pending to fix: https://redhat.atlassian.net/browse/CNV-91395")
			for _, asset := range patchableAssets {
				events := findEvents(EventFilter{
					Reason: "PatchApplied",
					Since:  testStartTime,
					Kind:   asset.GVK.Kind,
					Name:   asset.Name,
				})
				Expect(events).NotTo(BeEmpty(),
					fmt.Sprintf("PatchApplied event should exist for %s/%s", asset.GVK.Kind, asset.Name))
			}
		})

		It("should report customization_info{type=patch} metric for all patchable assets", func() {
			for _, asset := range patchableAssets {
				Eventually(func() float64 {
					return findCustomizationMetric(asset.GVK.Kind, asset.Name, asset.Namespace, "patch")
				}, 30*time.Second, 2*time.Second).Should(Equal(1.0),
					fmt.Sprintf("customization_info{type=patch} should be 1 for %s/%s", asset.GVK.Kind, asset.Name))
			}
		})

		It("should apply the patch to all patchable assets", func() {
			for _, asset := range patchableAssets {
				asset := asset
				if asset.Override.JSONPointer != "" {
					Eventually(pollResourceField(asset, func(obj *unstructured.Unstructured) string {
						return readOverrideFieldValue(obj, asset)
					}), timeout, interval).Should(Equal(asset.Override.Values[0]),
						fmt.Sprintf("field %s should be %q on %s/%s",
							asset.Override.JSONPointer, asset.Override.Values[0], asset.GVK.Kind, asset.Name))
				} else {
					Eventually(pollResourceField(asset, func(obj *unstructured.Unstructured) string {
						return obj.GetAnnotations()["e2e-patch-test"]
					}), timeout, interval).Should(Equal("applied"),
						fmt.Sprintf("annotation e2e-patch-test=applied should be present on %s/%s", asset.GVK.Kind, asset.Name))
				}
			}
		})

		It("should correct drift and keep compliance_status=1", func() {
			By("tampering with the patched field")
			for _, asset := range patchableAssets {
				if asset.Override.JSONPointer != "" {
					tamperField(asset, asset.Override.MergePatch(1))
				} else {
					setAnnotation(asset.GVK, asset.Name, asset.Namespace, "e2e-patch-test", "tampered")
				}
			}

			By("triggering reconciliation")
			touchHCO()

			By("verifying the operator corrects the drift back to the patched value")
			for _, asset := range patchableAssets {
				asset := asset
				if asset.Override.JSONPointer != "" {
					Eventually(pollResourceField(asset, func(obj *unstructured.Unstructured) string {
						return readOverrideFieldValue(obj, asset)
					}), timeout, interval).Should(Equal(asset.Override.Values[0]),
						fmt.Sprintf("field %s should be reconciled back to %q on %s/%s",
							asset.Override.JSONPointer, asset.Override.Values[0], asset.GVK.Kind, asset.Name))
				} else {
					Eventually(pollResourceField(asset, func(obj *unstructured.Unstructured) string {
						return obj.GetAnnotations()["e2e-patch-test"]
					}), timeout, interval).Should(Equal("applied"),
						fmt.Sprintf("annotation e2e-patch-test should be reconciled back to 'applied' on %s/%s", asset.GVK.Kind, asset.Name))
				}
			}

			By("verifying compliance_status stays at 1 (synced)")
			for _, asset := range patchableAssets {
				asset := asset
				Eventually(func() float64 {
					return captureAssetMetrics(asset.GVK.Kind, asset.Name, asset.Namespace).ComplianceStatus
				}, timeout, interval).Should(Equal(1.0),
					fmt.Sprintf("compliance_status should be 1 for %s/%s after drift correction", asset.GVK.Kind, asset.Name))
			}
		})

		AfterAll(func() {
			By("removing patch annotation from all patchable assets")
			for _, asset := range patchableAssets {
				removeAnnotation(asset.GVK, asset.Name, asset.Namespace, patchAnnotation)
			}
			touchHCO()

			By("verifying operator restores original values after patch annotation removal")
			for _, asset := range patchableAssets {
				asset := asset
				if asset.Override.JSONPointer != "" {
					Eventually(pollResourceField(asset, func(obj *unstructured.Unstructured) string {
						return readOverrideFieldValue(obj, asset)
					}), timeout, interval).ShouldNot(Equal(asset.Override.Values[0]),
						fmt.Sprintf("field %s should revert to original on %s/%s after patch removal",
							asset.Override.JSONPointer, asset.GVK.Kind, asset.Name))
				} else {
					Eventually(pollResourceField(asset, func(obj *unstructured.Unstructured) string {
						return obj.GetAnnotations()["e2e-patch-test"]
					}), timeout, interval).Should(BeEmpty(),
						fmt.Sprintf("annotation e2e-patch-test should be removed from %s/%s after patch removal", asset.GVK.Kind, asset.Name))
				}
			}
			waitForOperatorHealthy()
		})
	})

	// --- Context 2: Concurrent patch security block on sensitive kinds ---
	Context("Concurrent patch security block on sensitive kinds", Ordered, func() {
		var testStartTime time.Time

		BeforeAll(func() {
			if len(sensitiveAssets) == 0 {
				Skip("no sensitive assets available (MachineConfig/KubeletConfig CRDs not installed)")
			}
			testStartTime = time.Now()

			By("applying patch annotation to ALL sensitive assets concurrently")
			patchValue := `[{"op":"add","path":"/metadata/labels/e2e-patch-test","value":"should-not-appear"}]`
			for _, asset := range sensitiveAssets {
				setAnnotation(asset.GVK, asset.Name, asset.Namespace, patchAnnotation, patchValue)
			}

			By("touching HCO to trigger reconciliation")
			touchHCO()
			waitForOperatorHealthy()
		})

		It("should emit InvalidPatch events mentioning sensitive resource kind", func() {
			Skip("https://redhat.atlassian.net/browse/CNV-91395")
			for _, asset := range sensitiveAssets {
				events := findEvents(EventFilter{
					Reason: "InvalidPatch",
					Since:  testStartTime,
					Kind:   asset.GVK.Kind,
					Name:   asset.Name,
				})
				Expect(events).NotTo(BeEmpty(),
					fmt.Sprintf("InvalidPatch event should exist for sensitive kind %s/%s", asset.GVK.Kind, asset.Name))
				Expect(events[0].Note).To(ContainSubstring("sensitive"),
					"InvalidPatch event should mention 'sensitive' for security block")
			}
		})

		It("should NOT apply the patch label on sensitive assets", func() {
			for _, asset := range sensitiveAssets {
				obj, err := getUnstructuredResource(asset.GVK, asset.Name, asset.Namespace)
				Expect(err).NotTo(HaveOccurred())
				Expect(obj.GetLabels()["e2e-patch-test"]).To(BeEmpty(),
					fmt.Sprintf("label e2e-patch-test should NOT appear on sensitive %s/%s", asset.GVK.Kind, asset.Name))
			}
		})

		AfterAll(func() {
			By("removing patch annotations from sensitive assets")
			for _, asset := range sensitiveAssets {
				removeAnnotation(asset.GVK, asset.Name, asset.Namespace, patchAnnotation)
			}
			touchHCO()
			waitForOperatorHealthy()
		})
	})

	// --- Context 3: Forbidden patch paths (CNV-89805) ---
	Context("Forbidden patch paths block (CNV-89805)", func() {
		type forbiddenPathCase struct {
			description string
			path        string
			op          string
			value       string
		}

		forbiddenCases := []forbiddenPathCase{
			{description: "/metadata/name", path: "/metadata/name", op: "replace", value: `"hijacked"`},
			{description: "/metadata/namespace", path: "/metadata/namespace", op: "replace", value: `"hijacked-ns"`},
			{description: "/status/conditions", path: "/status/conditions", op: "add", value: `[]`},
			{description: "/apiVersion", path: "/apiVersion", op: "replace", value: `"v2"`},
			{description: "/kind", path: "/kind", op: "replace", value: `"Fake"`},
			{description: "/metadata/managedFields", path: "/metadata/managedFields", op: "replace", value: `[]`},
		}

		for _, tc := range forbiddenCases {
			tc := tc
			It(fmt.Sprintf("should reject patch targeting %s", tc.description), func() {
				if len(patchableAssets) == 0 {
					Skip("no patchable assets available")
				}
				asset := patchableAssets[0]
				testStart := time.Now()

				patchValue := fmt.Sprintf(`[{"op":"%s","path":"%s","value":%s}]`, tc.op, tc.path, tc.value)
				setAnnotation(asset.GVK, asset.Name, asset.Namespace, patchAnnotation, patchValue)
				DeferCleanup(func() {
					By("cleaning up patch annotation")
					removeAnnotation(asset.GVK, asset.Name, asset.Namespace, patchAnnotation)
					touchHCO()
					waitForOperatorHealthy()
				})
				touchHCO()

				By("waiting for InvalidPatch event with forbidden message")
				Eventually(func() bool {
					events := findEvents(EventFilter{
						Reason: "InvalidPatch",
						Since:  testStart,
						Kind:   asset.GVK.Kind,
						Name:   asset.Name,
					})
					for _, e := range events {
						if strings.Contains(e.Note, "forbidden") {
							return true
						}
					}
					return false
				}, timeout, interval).Should(BeTrue(),
					fmt.Sprintf("InvalidPatch event mentioning 'forbidden' should exist for path %s", tc.path))
			})
		}
	})

	// --- Context 4: Invalid patch syntax ---
	Context("Invalid patch syntax", func() {
		It("should emit InvalidPatch event for malformed JSON", func() {
			if len(patchableAssets) == 0 {
				Skip("no patchable assets available")
			}
			asset := patchableAssets[0]
			testStart := time.Now()

			By("setting malformed JSON as patch annotation")
			setAnnotation(asset.GVK, asset.Name, asset.Namespace, patchAnnotation, `{not valid json}`)
			DeferCleanup(func() {
				By("cleaning up malformed patch annotation")
				removeAnnotation(asset.GVK, asset.Name, asset.Namespace, patchAnnotation)
				touchHCO()
				waitForOperatorHealthy()
			})
			touchHCO()

			By("verifying InvalidPatch event is emitted")
			Eventually(func() bool {
				events := findEvents(EventFilter{
					Reason: "InvalidPatch",
					Since:  testStart,
					Kind:   asset.GVK.Kind,
					Name:   asset.Name,
				})
				return len(events) > 0
			}, timeout, interval).Should(BeTrue(),
				fmt.Sprintf("InvalidPatch event should exist for malformed patch on %s/%s", asset.GVK.Kind, asset.Name))
		})
	})

	// --- Context 5: Concurrent Ignore Fields ---
	Context("Concurrent ignore-fields on assets with Override spec", Ordered, func() {
		var ignoreFieldAssets []testAsset

		BeforeAll(func() {
			for _, a := range assetsUnderTestAvailable {
				if a.Override.JSONPointer != "" {
					ignoreFieldAssets = append(ignoreFieldAssets, a)
				} else {
					GinkgoWriter.Printf("WARNING: %s/%s has no Override spec, skipping ignore-fields test (no Override spec)\n",
						a.GVK.Kind, a.Name)
				}
			}
			if len(ignoreFieldAssets) == 0 {
				Skip("no assets with Override spec available")
			}

			By("setting ignore-fields annotation pointing to each asset's operator-controlled field")
			for _, asset := range ignoreFieldAssets {
				setAnnotation(asset.GVK, asset.Name, asset.Namespace, ignoreFieldsAnnotation, asset.Override.JSONPointer)
			}

			By("tampering with the operator-controlled field on each asset")
			for _, asset := range ignoreFieldAssets {
				tamperField(asset, asset.Override.MergePatch(0))
			}

			By("touching HCO to trigger reconciliation")
			touchHCO()
			waitForOperatorHealthy()
		})

		It("should preserve the tampered value on all assets (operator does not revert)", func() {
			Consistently(func() string {
				for _, asset := range ignoreFieldAssets {
					obj, err := getUnstructuredResource(asset.GVK, asset.Name, asset.Namespace)
					if err != nil {
						return fmt.Sprintf("%s/%s: fetch error: %v", asset.GVK.Kind, asset.Name, err)
					}
					if v := readOverrideFieldValue(obj, asset); v != asset.Override.Values[0] {
						return fmt.Sprintf("%s/%s: field %s=%q, want %q",
							asset.GVK.Kind, asset.Name, asset.Override.JSONPointer, v, asset.Override.Values[0])
					}
				}
				return ""
			}, consistentlyDuration, consistentlyInterval).Should(BeEmpty())
		})

		It("should not revert user modifications to the ignored field after reconciliation", func() {
			By("modifying the ignored field to a different value")
			for _, asset := range ignoreFieldAssets {
				tamperField(asset, asset.Override.MergePatch(1))
			}

			By("triggering reconciliation")
			touchHCO()
			waitForOperatorHealthy()

			By("verifying the modified value is preserved (not reverted)")
			Consistently(func() string {
				for _, asset := range ignoreFieldAssets {
					obj, err := getUnstructuredResource(asset.GVK, asset.Name, asset.Namespace)
					if err != nil {
						return fmt.Sprintf("%s/%s: fetch error: %v", asset.GVK.Kind, asset.Name, err)
					}
					if v := readOverrideFieldValue(obj, asset); v != asset.Override.Values[1] {
						return fmt.Sprintf("%s/%s: field %s=%q, want %q",
							asset.GVK.Kind, asset.Name, asset.Override.JSONPointer, v, asset.Override.Values[1])
					}
				}
				return ""
			}, consistentlyDuration, consistentlyInterval).Should(BeEmpty())
		})

		It("should report customization_info{type=ignore} metric for all assets", func() {
			for _, asset := range ignoreFieldAssets {
				Eventually(func() float64 {
					return findCustomizationMetric(asset.GVK.Kind, asset.Name, asset.Namespace, "ignore")
				}, 30*time.Second, 2*time.Second).Should(Equal(1.0),
					fmt.Sprintf("customization_info{type=ignore} should be 1 for %s/%s", asset.GVK.Kind, asset.Name))
			}
		})

		It("should restore the operator-controlled field after removing ignore-fields", func() {
			By("removing ignore-fields annotation from all assets")
			for _, asset := range ignoreFieldAssets {
				removeAnnotation(asset.GVK, asset.Name, asset.Namespace, ignoreFieldsAnnotation)
			}

			By("touching HCO to trigger reconciliation")
			touchHCO()

			By("verifying operator restores the field via SSA (no longer the tampered value)")
			for _, asset := range ignoreFieldAssets {
				asset := asset
				Eventually(pollResourceField(asset, func(obj *unstructured.Unstructured) string {
					return readOverrideFieldValue(obj, asset)
				}), timeout, interval).ShouldNot(Equal(asset.Override.Values[1]),
					fmt.Sprintf("field %s should be restored on %s/%s after removing ignore-fields",
						asset.Override.JSONPointer, asset.GVK.Kind, asset.Name))
			}
		})

		AfterAll(func() {
			By("safety-net: removing ignore-fields annotation from all assets")
			for _, asset := range ignoreFieldAssets {
				removeAnnotation(asset.GVK, asset.Name, asset.Namespace, ignoreFieldsAnnotation)
			}
			touchHCO()
			waitForOperatorHealthy()
		})
	})

	// --- Context 6: Invalid ignore-fields ---
	Context("Invalid ignore-fields syntax", func() {
		It("should emit InvalidIgnoreFields event for invalid path (no leading slash)", func() {
			Skip("https://redhat.atlassian.net/browse/CNV-91395")
			if len(patchableAssets) == 0 {
				Skip("no patchable assets available")
			}
			asset := patchableAssets[0]
			testStart := time.Now()

			By("setting invalid ignore-fields value (no leading slash)")
			setAnnotation(asset.GVK, asset.Name, asset.Namespace, ignoreFieldsAnnotation, "no-leading-slash")
			DeferCleanup(func() {
				By("cleaning up invalid annotation")
				removeAnnotation(asset.GVK, asset.Name, asset.Namespace, ignoreFieldsAnnotation)
				touchHCO()
				waitForOperatorHealthy()
			})
			touchHCO()

			By("verifying InvalidIgnoreFields event is emitted")
			Eventually(func() bool {
				events := findEvents(EventFilter{
					Reason: "InvalidIgnoreFields",
					Since:  testStart,
					Kind:   asset.GVK.Kind,
					Name:   asset.Name,
				})
				return len(events) > 0
			}, timeout, interval).Should(BeTrue(),
				fmt.Sprintf("InvalidIgnoreFields event should exist for %s/%s with invalid path", asset.GVK.Kind, asset.Name))
		})
	})

	// --- Context 7: Concurrent Unmanaged Mode ---
	Context("Concurrent unmanaged mode on all available assets", Ordered, func() {
		var testStartTime time.Time

		BeforeAll(func() {
			Expect(assetsUnderTestAvailable).NotTo(BeEmpty(), "at least one asset must be available")
			testStartTime = time.Now()

			By("setting unmanaged mode on ALL available assets concurrently")
			for _, asset := range assetsUnderTestAvailable {
				setAnnotation(asset.GVK, asset.Name, asset.Namespace, modeAnnotation, modeUnmanaged)
			}

			By("tampering with an operator-controlled field on each asset (drift that should NOT be reverted while unmanaged)")
			for _, asset := range assetsUnderTestAvailable {
				if asset.Override.JSONPointer != "" {
					GinkgoWriter.Printf("unmanaged: %s/%s → override %s\n", asset.GVK.Kind, asset.Name, asset.Override.JSONPointer)
					tamperField(asset, asset.Override.MergePatch(0))
				} else {
					GinkgoWriter.Printf("unmanaged: %s/%s → fallback managed-by label (no Override, no Override spec)\n",
						asset.GVK.Kind, asset.Name)
					setLabel(asset.GVK, asset.Name, asset.Namespace, managedByLabel, "tampered")
				}
			}

			By("touching HCO to trigger reconciliation")
			touchHCO()
			waitForOperatorHealthy()
		})

		It("should NOT revert modifications on unmanaged assets", func() {
			Consistently(func() string {
				for _, asset := range assetsUnderTestAvailable {
					obj, err := getUnstructuredResource(asset.GVK, asset.Name, asset.Namespace)
					if err != nil {
						return fmt.Sprintf("%s/%s: fetch error: %v", asset.GVK.Kind, asset.Name, err)
					}
					if asset.Override.JSONPointer != "" {
						if v := readOverrideFieldValue(obj, asset); v != asset.Override.Values[0] {
							return fmt.Sprintf("%s/%s: field %s=%q, want %q",
								asset.GVK.Kind, asset.Name, asset.Override.JSONPointer, v, asset.Override.Values[0])
						}
					} else {
						if v := obj.GetLabels()[managedByLabel]; v != "tampered" {
							return fmt.Sprintf("%s/%s: managed-by=%q, want 'tampered'", asset.GVK.Kind, asset.Name, v)
						}
					}
				}
				return ""
			}, consistentlyDuration, consistentlyInterval).Should(BeEmpty())
		})

		It("should emit UnmanagedMode events for all assets", func() {
			Skip("https://redhat.atlassian.net/browse/CNV-91395")
			for _, asset := range assetsUnderTestAvailable {
				events := findEvents(EventFilter{
					Reason: "UnmanagedMode",
					Since:  testStartTime,
					Kind:   asset.GVK.Kind,
					Name:   asset.Name,
				})
				Expect(events).NotTo(BeEmpty(),
					fmt.Sprintf("UnmanagedMode event should exist for %s/%s", asset.GVK.Kind, asset.Name))
			}
		})

		It("should report customization_info{type=unmanaged} metric for all assets", func() {
			for _, asset := range assetsUnderTestAvailable {
				Eventually(func() float64 {
					return findCustomizationMetric(asset.GVK.Kind, asset.Name, asset.Namespace, "unmanaged")
				}, 30*time.Second, 2*time.Second).Should(Equal(1.0),
					fmt.Sprintf("customization_info{type=unmanaged} should be 1 for %s/%s", asset.GVK.Kind, asset.Name))
			}
		})

		It("should resume reconciliation and correct drift when unmanaged annotation is removed", func() {
			By("removing unmanaged annotation from all assets")
			for _, asset := range assetsUnderTestAvailable {
				removeAnnotation(asset.GVK, asset.Name, asset.Namespace, modeAnnotation)
			}

			By("touching HCO to trigger reconciliation")
			touchHCO()

			By("verifying operator restores tampered fields via SSA")
			for _, asset := range assetsUnderTestAvailable {
				asset := asset
				if asset.Override.JSONPointer != "" {
					Eventually(pollResourceField(asset, func(obj *unstructured.Unstructured) string {
						return readOverrideFieldValue(obj, asset)
					}), timeout, interval).ShouldNot(Equal(asset.Override.Values[0]),
						fmt.Sprintf("field %s should be restored on %s/%s after resuming managed mode",
							asset.Override.JSONPointer, asset.GVK.Kind, asset.Name))
				} else {
					Eventually(pollResourceField(asset, func(obj *unstructured.Unstructured) string {
						return obj.GetLabels()[managedByLabel]
					}), timeout, interval).Should(Equal(managedByValue),
						fmt.Sprintf("managed-by label should be restored to %q on %s/%s after resuming managed mode",
							managedByValue, asset.GVK.Kind, asset.Name))
				}
			}
		})

		AfterAll(func() {
			for _, asset := range assetsUnderTestAvailable {
				obj, err := getUnstructuredResource(asset.GVK, asset.Name, asset.Namespace)
				if err != nil {
					continue
				}
				if ann := obj.GetAnnotations(); ann != nil && ann[modeAnnotation] == modeUnmanaged {
					removeAnnotation(asset.GVK, asset.Name, asset.Namespace, modeAnnotation)
				}
			}
			touchHCO()
			waitForOperatorHealthy()
		})
	})
})
