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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const (
	machineConfigCRDName = "machineconfigs.machineconfiguration.openshift.io"
	machineConfigCRDFile = "test/crds/openshift/machineconfig-crd.yaml"
)

var machineConfigDepLabels = map[string]string{
	"group": "machineconfiguration.openshift.io",
	"kind":  "Machineconfig",
}

var _ = Describe("CRD Lifecycle Tests", Ordered, func() {

	BeforeAll(func() {
		if isOpenShiftCluster() {
			Skip("CRD lifecycle tests only run on Kind — on OCP, CRDs are managed by operators")
		}

		ensureHCOExists()
		patchAutopilotAndWait(autopilotEnabled)

		By("removing MachineConfig CRD so we can test creation lifecycle")
		prevCount := getManagerRestartCount()
		removeCRD(machineConfigCRDName)
		waitForOperatorRestart(prevCount)
		waitForOperatorHealthy()
	})

	It("should restart when managed CRD is created and create the swap-enable resource", func() {
		By("verifying CRDMissing event was emitted after CRD removal")
		Eventually(func() int {
			return captureAutopilotEvents().CRDMissing
		}, timeout, interval).Should(BeNumerically(">", 0),
			"CRDMissing event should be emitted when CRD is absent")

		By("verifying missing_dependency=1 and dependency_opted_in=1 while CRD is absent")
		Eventually(func() float64 {
			return findMetricValue("kubevirt_autopilot_missing_dependency", machineConfigDepLabels)
		}, timeout, interval).Should(Equal(1.0),
			"missing_dependency metric should be 1 when CRD is missing")
		Eventually(func() float64 {
			return findMetricValue("kubevirt_autopilot_dependency_opted_in", machineConfigDepLabels)
		}, timeout, interval).Should(Equal(1.0),
			"dependency_opted_in should be 1 — MachineConfig is install:always")

		prevCount := getManagerRestartCount()
		installCRDFromFile(machineConfigCRDFile)
		waitForCRDEstablished(machineConfigCRDName)
		waitForOperatorRestart(prevCount)
		waitForOperatorHealthy()

		Eventually(func() error {
			_, err := getUnstructuredResource(machineConfigGVK, "90-worker-swap-online", "")
			return err
		}, timeout, interval).Should(Succeed(),
			"Operator should create the 90-worker-swap-online MachineConfig after CRD installation")

		By("verifying missing_dependency metric is 0 after CRD is installed")
		Eventually(func() float64 {
			return findMetricValue("kubevirt_autopilot_missing_dependency", machineConfigDepLabels)
		}, timeout, interval).Should(Equal(0.0),
			"missing_dependency metric should be 0 when CRD is present")
	})

	It("should restart when managed CRD is deleted", func() {
		crdMissingBefore := captureAutopilotEvents().CRDMissing
		deleteTime := time.Now()

		prevCount := getManagerRestartCount()
		removeCRD(machineConfigCRDName)
		waitForOperatorRestart(prevCount)

		By("verifying CRDMissing event was emitted after CRD deletion")
		Eventually(func() int {
			return captureAutopilotEvents(deleteTime).CRDMissing
		}, timeout, interval).Should(BeNumerically(">", 0),
			"CRDMissing event should be emitted after CRD deletion")
		Expect(captureAutopilotEvents().CRDMissing).To(BeNumerically(">", crdMissingBefore),
			"Total CRDMissing event count should increase")

		By("verifying missing_dependency=1 and dependency_opted_in=1 after CRD deletion")
		Eventually(func() float64 {
			return findMetricValue("kubevirt_autopilot_missing_dependency", machineConfigDepLabels)
		}, timeout, interval).Should(Equal(1.0),
			"missing_dependency metric should be 1 when CRD is deleted")
		Eventually(func() float64 {
			return findMetricValue("kubevirt_autopilot_dependency_opted_in", machineConfigDepLabels)
		}, timeout, interval).Should(Equal(1.0),
			"dependency_opted_in should be 1 — MachineConfig is install:always")
	})

	AfterAll(func() {
		if isOpenShiftCluster() {
			return
		}

		// Restore the full CRD from test/crds/ so other tests still find it.
		// Installing a managed CRD triggers a graceful operator restart to
		// reconfigure watches, so we must wait for that restart before
		// checking health.
		if !crdInstalled(machineConfigCRDName) {
			prevCount := getManagerRestartCount()
			installCRDFromFile(machineConfigCRDFile)
			waitForCRDEstablished(machineConfigCRDName)
			waitForOperatorRestart(prevCount)
		} else {
			waitForOperatorHealthy()
		}
	})
})

var _ = Describe("Dependency Opt-In Metric Tests", Ordered, func() {
	// Verifies that dependency_opted_in tracks the opt-in annotation state independently
	// of CRD presence. Uses NodeHealthCheck (nodehealthchecks.remediation.medik8s.io)
	// as the canonical opt-in + gate_crd scenario — an optional medik8s operator CRD
	// that is absent on Kind by default, matching real-world opt-in semantics.
	// Runs only on Kind (no Prometheus needed — checks /metrics directly).
	// Complements the OCP alert tests in alert_e2e_test.go that verify the alert behavior.
	const (
		nhcCRDName = "nodehealthchecks.remediation.medik8s.io"
		nhcCRDFile = "test/crds/remediation/nodehealthchecks.remediation.medik8s.io.yaml"
		nhcAnnot   = "platform.kubevirt.io/enable-node-remediation"
	)

	var nhcDepLabels = map[string]string{
		"group": "remediation.medik8s.io",
		"kind":  "Nodehealthcheck",
	}

	// tracks whether BeforeAll removed the CRD (so AfterAll can restore it)
	// and whether the third It installed it (so AfterAll can remove it if it
	// was absent at suite entry).
	nhcWasInstalled := false
	nhcInstalledByTest := false

	BeforeAll(func() {
		if isOpenShiftCluster() {
			Skip("Dependency opt-in metric tests only run on Kind")
		}

		ensureHCOExists()
		patchAutopilotAndWait(autopilotEnabled)

		By("ensuring NodeHealthCheck opt-in annotation is absent on HCO")
		removeAnnotation(hcoGVK, hcoName, operatorNamespace, nhcAnnot)

		if crdInstalled(nhcCRDName) {
			By("removing NodeHealthCheck CRD to start from a known absent state")
			nhcWasInstalled = true
			prevCount := getManagerRestartCount()
			removeCRD(nhcCRDName)
			waitForOperatorRestart(prevCount)
			waitForOperatorHealthy()
		}

		reconcileStart := time.Now()
		touchHCO()
		waitForReconcileSucceeded(reconcileStart)
	})

	AfterAll(func() {
		if isOpenShiftCluster() {
			return
		}
		By("removing NodeHealthCheck opt-in annotation from HCO")
		removeAnnotation(hcoGVK, hcoName, operatorNamespace, nhcAnnot)
		if nhcWasInstalled && !crdInstalled(nhcCRDName) {
			By("restoring NodeHealthCheck CRD")
			prevCount := getManagerRestartCount()
			installCRDFromFile(nhcCRDFile)
			waitForCRDEstablished(nhcCRDName)
			waitForOperatorRestart(prevCount)
			waitForOperatorHealthy()
		} else if nhcInstalledByTest && !nhcWasInstalled && crdInstalled(nhcCRDName) {
			By("removing NodeHealthCheck CRD installed by test (was absent at suite entry)")
			prevCount := getManagerRestartCount()
			removeCRD(nhcCRDName)
			waitForOperatorRestart(prevCount)
			waitForOperatorHealthy()
		}
	})

	It("should emit missing_dependency=1 and dependency_opted_in=0 when gate CRD is absent and annotation not set", func() {
		Eventually(func() float64 {
			return findMetricValue("kubevirt_autopilot_missing_dependency", nhcDepLabels)
		}, timeout, interval).Should(Equal(1.0),
			"missing_dependency should be 1 when NodeHealthCheck CRD is absent")

		Eventually(func() float64 {
			return findMetricValue("kubevirt_autopilot_dependency_opted_in", nhcDepLabels)
		}, timeout, interval).Should(Equal(0.0),
			"dependency_opted_in should be 0 when opt-in annotation is not set")
	})

	It("should emit dependency_opted_in=1 when opt-in annotation is set, missing_dependency stays 1", func() {
		By("setting NodeHealthCheck opt-in annotation on HCO")
		reconcileStart := time.Now()
		setAnnotation(hcoGVK, hcoName, operatorNamespace, nhcAnnot, "true")
		touchHCO()
		waitForReconcileSucceeded(reconcileStart)

		Eventually(func() float64 {
			return findMetricValue("kubevirt_autopilot_dependency_opted_in", nhcDepLabels)
		}, timeout, interval).Should(Equal(1.0),
			"dependency_opted_in should be 1 after opt-in annotation is set")

		Eventually(func() float64 {
			return findMetricValue("kubevirt_autopilot_missing_dependency", nhcDepLabels)
		}, timeout, interval).Should(Equal(1.0),
			"missing_dependency must still be 1 — CRD is still absent")
	})

	It("should emit missing_dependency=0 and dependency_opted_in=1 after gate CRD is installed", func() {
		By("installing NodeHealthCheck CRD")
		nhcInstalledByTest = true
		prevCount := getManagerRestartCount()
		installCRDFromFile(nhcCRDFile)
		waitForCRDEstablished(nhcCRDName)
		waitForOperatorRestart(prevCount)
		waitForOperatorHealthy()
		reconcileStart := time.Now()
		touchHCO()
		waitForReconcileSucceeded(reconcileStart)

		Eventually(func() float64 {
			return findMetricValue("kubevirt_autopilot_missing_dependency", nhcDepLabels)
		}, timeout, interval).Should(Equal(0.0),
			"missing_dependency should be 0 after NodeHealthCheck CRD is installed")

		Eventually(func() float64 {
			return findMetricValue("kubevirt_autopilot_dependency_opted_in", nhcDepLabels)
		}, timeout, interval).Should(Equal(1.0),
			"dependency_opted_in must remain 1 — annotation is still set")
	})
})
