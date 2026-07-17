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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type testTombstone struct {
	GVK       schema.GroupVersionKind
	Plural    string
	Name      string
	Namespace string
	CRDName   string
}

func (t testTombstone) label() string {
	if t.Namespace != "" {
		return fmt.Sprintf("%s/%s/%s", t.GVK.Kind, t.Namespace, t.Name)
	}
	return fmt.Sprintf("%s/%s", t.GVK.Kind, t.Name)
}

func (t testTombstone) webhookName() string {
	return fmt.Sprintf("autopilot-e2e-block-tombstone-%s", t.Plural)
}

var tombstonesUnderTest = []testTombstone{
	{
		GVK:       schema.GroupVersionKind{Group: "remediation.medik8s.io", Version: "v1alpha1", Kind: "NodeHealthCheck"},
		Plural:    "nodehealthchecks",
		Name:      "virt-node-health-check",
		Namespace: "openshift-operators",
		CRDName:   "nodehealthchecks.remediation.medik8s.io",
	},
}

var _ = Describe("Tombstone Lifecycle Tests", Ordered, ContinueOnFailure, func() {
	var suiteBeforeTime time.Time

	BeforeAll(func() {
		By("ensuring HCO exists with autopilot enabled")
		ensureHCOExists()
		patchAutopilotAndWait(autopilotEnabled)

		waitForOperatorHealthy()

		if isOpenShiftCluster() {
			By("setting PrometheusRule to unmanaged so operator won't revert our changes")
			setAnnotation(prometheusRuleGVK, prometheusRuleName, operatorNamespace, modeAnnotation, modeUnmanaged)

			By("reducing alert 'for' durations to 1m for faster test feedback")
			patchAlertForDurations("15s")
		}

		By("triggering initial reconciliation to establish baseline metrics")
		touchHCO()
		suiteBeforeTime = time.Now()
	})

	AfterAll(func() {
		By("cleaning up any leftover tombstone target resources")
		for _, ts := range tombstonesUnderTest {
			obj := unstructuredRef(ts.GVK, ts.Name, ts.Namespace)
			_ = k8sClient.Delete(ctx, obj)
			deleteTombstoneBlockingWebhook(ts)
		}

		if isOpenShiftCluster() {
			By("restoring PrometheusRule to managed mode")
			removeAnnotation(prometheusRuleGVK, prometheusRuleName, operatorNamespace, modeAnnotation)
		}

		touchHCO()
		waitForOperatorHealthy()
	})

	for _, ts := range tombstonesUnderTest {
		ts := ts

		Context(ts.label(), Ordered, func() {
			var beforeTime time.Time

			BeforeAll(func() {
				if !crdInstalled(ts.CRDName) {
					Skip(fmt.Sprintf("CRD %s not installed on this cluster", ts.CRDName))
				}
			})

			BeforeEach(func() {
				beforeTime = time.Now()
			})

			It("should be idempotent when resource does not exist", func() {
				By("verifying target resource does not exist")
				err := k8sClient.Get(ctx, client.ObjectKey{Name: ts.Name, Namespace: ts.Namespace},
					unstructuredRef(ts.GVK, ts.Name, ts.Namespace))
				Expect(err).To(HaveOccurred(), "tombstone asset should not exist")

				By("verifying metric is TombstoneDeleted (0)")
				Expect(captureAssetMetrics(ts.GVK.Kind, ts.Name, ts.Namespace).TombstoneStatus).
					To(BeNumerically("==", 0), "TombstoneStatus should be 0 (deleted/absent)")

				By("verifying no TombstoneFailed events")
				Expect(findEvents(EventFilter{
					Reason: "TombstoneFailed",
					Since:  suiteBeforeTime,
					Name:   ts.Name,
				})).To(BeEmpty(), "No TombstoneFailed events should be emitted")

				By("verifying no TombstoneSkipped events")
				Expect(findEvents(EventFilter{
					Reason: "TombstoneSkipped",
					Since:  suiteBeforeTime,
					Name:   ts.Name,
				})).To(BeEmpty(), "No TombstoneSkipped events should be emitted")
			})

			It("should delete resource with correct managed-by label (TombstoneDeleted)", func() {
				By("creating target resource with managed-by label")
				createTombstoneResource(ts, true)

				By("triggering reconciliation")
				touchHCO()

				By("verifying resource is deleted by the operator")
				Eventually(func() bool {
					err := k8sClient.Get(ctx, client.ObjectKey{Name: ts.Name, Namespace: ts.Namespace},
						unstructuredRef(ts.GVK, ts.Name, ts.Namespace))
					return apierrors.IsNotFound(err)
				}, timeout, interval).Should(BeTrue(),
					fmt.Sprintf("%s should be deleted by tombstone reconciler", ts.label()))

				By("verifying TombstoneDeleted event")
				Eventually(func() []string {
					var notes []string
					for _, e := range findEvents(EventFilter{Reason: "TombstoneDeleted", Since: beforeTime, Name: ts.Name}) {
						notes = append(notes, e.Note)
					}
					return notes
				}, timeout, interval).ShouldNot(BeEmpty(),
					"At least one TombstoneDeleted event should be emitted")

				By("verifying metric is TombstoneDeleted (0)")
				Eventually(func() float64 {
					return captureAssetMetrics(ts.GVK.Kind, ts.Name, ts.Namespace).TombstoneStatus
				}, timeout, interval).Should(BeNumerically("==", 0),
					"TombstoneStatus should be 0 after successful deletion")
			})

			It("should skip resource without managed-by label (TombstoneSkipped)", func() {
				By("creating target resource WITHOUT managed-by label")
				createTombstoneResource(ts, false)

				By("triggering reconciliation")
				touchHCO()

				By("verifying resource is NOT deleted (safety check)")
				Consistently(func() error {
					return k8sClient.Get(ctx, client.ObjectKey{Name: ts.Name, Namespace: ts.Namespace},
						unstructuredRef(ts.GVK, ts.Name, ts.Namespace))
				}, consistentlyDuration, consistentlyInterval).Should(Succeed(),
					fmt.Sprintf("%s should NOT be deleted — label mismatch", ts.label()))

				By("verifying TombstoneSkipped event")
				Eventually(func() []string {
					var notes []string
					for _, e := range findEvents(EventFilter{Reason: "TombstoneSkipped", Since: beforeTime, Name: ts.Name}) {
						notes = append(notes, e.Note)
					}
					return notes
				}, timeout, interval).ShouldNot(BeEmpty(),
					"At least one TombstoneSkipped event should be emitted")

				By("verifying metric is TombstoneSkipped (-2)")
				Eventually(func() float64 {
					return captureAssetMetrics(ts.GVK.Kind, ts.Name, ts.Namespace).TombstoneStatus
				}, timeout, interval).Should(BeNumerically("==", -2),
					"TombstoneStatus should be -2 (skipped due to label mismatch)")

				By("cleaning up: deleting the target resource")
				deleteResource(ts.GVK, ts.Name, ts.Namespace)
			})

			It("should emit TombstoneFailed when deletion is blocked", func() {
				By("creating webhook that blocks DELETE operations BEFORE creating the resource")
				createTombstoneBlockingWebhook(ts)

				By("creating target resource with managed-by label")
				createTombstoneResource(ts, true)

				By("verifying resource survives (webhook blocks any stale reconciliation deletes)")
				Consistently(func() error {
					return k8sClient.Get(ctx, client.ObjectKey{Name: ts.Name, Namespace: ts.Namespace},
						unstructuredRef(ts.GVK, ts.Name, ts.Namespace))
				}, 5*time.Second, 1*time.Second).Should(Succeed(),
					"resource should persist — webhook must block deletes")

				By("triggering reconciliation")
				touchHCO()

				By("verifying TombstoneFailed event")
				Eventually(func() []string {
					var notes []string
					for _, e := range findEvents(EventFilter{Reason: "TombstoneFailed", Since: beforeTime, Name: ts.Name}) {
						notes = append(notes, e.Note)
					}
					return notes
				}, timeout, interval).ShouldNot(BeEmpty(),
					"At least one TombstoneFailed event should be emitted")

				By("verifying metric is TombstoneError (-1)")
				Eventually(func() float64 {
					return captureAssetMetrics(ts.GVK.Kind, ts.Name, ts.Namespace).TombstoneStatus
				}, timeout, interval).Should(BeNumerically("==", -1),
					"TombstoneStatus should be -1 (deletion error)")

				By("cleaning up: removing webhook and deleting the target resource")
				deleteTombstoneBlockingWebhook(ts)
				deleteResource(ts.GVK, ts.Name, ts.Namespace)
			})
		})
	}

	Context("VirtPlatformTombstoneStuck alert (OCP only)", func() {
		ts := tombstonesUnderTest[0]

		It("should fire warning alert when tombstone status is negative", func() {
			if !isOpenShiftCluster() {
				Skip("Alert tests only run on OCP — Kind has no Prometheus")
			}
			if !crdInstalled(ts.CRDName) {
				Skip(fmt.Sprintf("CRD %s not installed on this cluster", ts.CRDName))
			}

			By("creating target resource WITHOUT managed-by label to trigger TombstoneSkipped")
			createTombstoneResource(ts, false)

			By("triggering reconciliation")
			touchHCO()

			By("verifying metric is negative (TombstoneSkipped = -2)")
			Eventually(func() float64 {
				return captureAssetMetrics(ts.GVK.Kind, ts.Name, ts.Namespace).TombstoneStatus
			}, timeout, interval).Should(BeNumerically("<", 0),
				"TombstoneStatus should be negative")

			By("waiting for VirtPlatformTombstoneStuck alert to fire (for: 1m)")
			attempt := 0
			maxAttempts := int((3 * time.Minute) / (10 * time.Second))
			var alertLabels map[string]string
			Eventually(func() bool {
				attempt++
				alertLabels = queryFiringAlert("VirtPlatformTombstoneStuck", attempt, maxAttempts)
				return alertLabels != nil
			}, 3*time.Minute, 10*time.Second).Should(BeTrue(),
				"VirtPlatformTombstoneStuck alert should fire when tombstone_status < 0")

			Expect(alertLabels).To(HaveKeyWithValue("severity", "warning"))
			Expect(alertLabels).To(HaveKeyWithValue("operator", "virt-platform-autopilot"))

			By("cleaning up: deleting the target resource")
			deleteResource(ts.GVK, ts.Name, ts.Namespace)

			By("verifying alert stops firing after cleanup")
			touchHCO()
			alertAttempt := 0
			alertMaxAttempts := int((3 * time.Minute) / (10 * time.Second))
			Eventually(func() bool {
				alertAttempt++
				return queryAlertNotFiring("VirtPlatformTombstoneStuck", alertAttempt, alertMaxAttempts)
			}, 3*time.Minute, 10*time.Second).Should(BeTrue(),
				"VirtPlatformTombstoneStuck alert should stop firing after cleanup")
		})
	})

	_ = suiteBeforeTime // used implicitly via closure
})
