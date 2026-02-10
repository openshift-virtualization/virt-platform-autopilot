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

package observability

import (
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestSetCompliance(t *testing.T) {
	// Reset metrics before test
	ComplianceStatus.Reset()

	obj := &unstructured.Unstructured{}
	obj.SetKind("ConfigMap")
	obj.SetName("test-cm")
	obj.SetNamespace("test-ns")

	// Set compliance to synced (1)
	SetCompliance(obj, 1)

	expected := `
		# HELP virt_platform_compliance_status Compliance status of managed resources (1=synced, 0=drifted/failed)
		# TYPE virt_platform_compliance_status gauge
		virt_platform_compliance_status{kind="ConfigMap",name="test-cm",namespace="test-ns"} 1
	`

	if err := testutil.CollectAndCompare(ComplianceStatus, strings.NewReader(expected)); err != nil {
		t.Errorf("unexpected metric value: %v", err)
	}

	// Set compliance to drifted (0)
	SetCompliance(obj, 0)

	expected = `
		# HELP virt_platform_compliance_status Compliance status of managed resources (1=synced, 0=drifted/failed)
		# TYPE virt_platform_compliance_status gauge
		virt_platform_compliance_status{kind="ConfigMap",name="test-cm",namespace="test-ns"} 0
	`

	if err := testutil.CollectAndCompare(ComplianceStatus, strings.NewReader(expected)); err != nil {
		t.Errorf("unexpected metric value after drift: %v", err)
	}
}

func TestIncThrashing(t *testing.T) {
	// Reset metrics before test
	ThrashingTotal.Reset()

	obj := &unstructured.Unstructured{}
	obj.SetKind("Deployment")
	obj.SetName("test-deploy")
	obj.SetNamespace("test-ns")

	// Increment thrashing counter 3 times
	IncThrashing(obj)
	IncThrashing(obj)
	IncThrashing(obj)

	expected := `
		# HELP virt_platform_thrashing_total Total number of reconciliation throttling events (anti-thrashing gate hits)
		# TYPE virt_platform_thrashing_total counter
		virt_platform_thrashing_total{kind="Deployment",name="test-deploy",namespace="test-ns"} 3
	`

	if err := testutil.CollectAndCompare(ThrashingTotal, strings.NewReader(expected)); err != nil {
		t.Errorf("unexpected metric value: %v", err)
	}
}

func TestSetCustomization(t *testing.T) {
	// Reset metrics before test
	CustomizationInfo.Reset()

	obj := &unstructured.Unstructured{}
	obj.SetKind("HyperConverged")
	obj.SetName("kubevirt-hyperconverged")
	obj.SetNamespace("kubevirt-hyperconverged")

	// Set patch customization
	SetCustomization(obj, "patch")

	expected := `
		# HELP virt_platform_customization_info Tracks intentional customizations (always 1 when present). Type: patch/ignore/unmanaged
		# TYPE virt_platform_customization_info gauge
		virt_platform_customization_info{kind="HyperConverged",name="kubevirt-hyperconverged",namespace="kubevirt-hyperconverged",type="patch"} 1
	`

	if err := testutil.CollectAndCompare(CustomizationInfo, strings.NewReader(expected)); err != nil {
		t.Errorf("unexpected metric value: %v", err)
	}
}

func TestClearCustomization(t *testing.T) {
	// Reset metrics before test
	CustomizationInfo.Reset()

	obj := &unstructured.Unstructured{}
	obj.SetKind("HyperConverged")
	obj.SetName("kubevirt-hyperconverged")
	obj.SetNamespace("kubevirt-hyperconverged")

	// Set and then clear customization
	SetCustomization(obj, "patch")
	ClearCustomization(obj, "patch")

	// After clearing, the metric should not exist
	count := testutil.CollectAndCount(CustomizationInfo)
	if count != 0 {
		t.Errorf("expected 0 metrics after clear, got %d", count)
	}
}

func TestMultipleCustomizationTypes(t *testing.T) {
	// Reset metrics before test
	CustomizationInfo.Reset()

	obj := &unstructured.Unstructured{}
	obj.SetKind("HyperConverged")
	obj.SetName("kubevirt-hyperconverged")
	obj.SetNamespace("kubevirt-hyperconverged")

	// Set multiple customization types (e.g., both patch and ignore-fields)
	SetCustomization(obj, "patch")
	SetCustomization(obj, "ignore")

	expected := `
		# HELP virt_platform_customization_info Tracks intentional customizations (always 1 when present). Type: patch/ignore/unmanaged
		# TYPE virt_platform_customization_info gauge
		virt_platform_customization_info{kind="HyperConverged",name="kubevirt-hyperconverged",namespace="kubevirt-hyperconverged",type="ignore"} 1
		virt_platform_customization_info{kind="HyperConverged",name="kubevirt-hyperconverged",namespace="kubevirt-hyperconverged",type="patch"} 1
	`

	if err := testutil.CollectAndCompare(CustomizationInfo, strings.NewReader(expected)); err != nil {
		t.Errorf("unexpected metric value: %v", err)
	}
}

func TestSetMissingDependency(t *testing.T) {
	// Reset metrics before test
	MissingDependency.Reset()

	// Mark MetalLB CRD as missing
	SetMissingDependency("metallb.io", "v1beta1", "MetalLB", true)

	expected := `
		# HELP virt_platform_missing_dependency Indicates missing optional CRDs (1=missing, 0=present)
		# TYPE virt_platform_missing_dependency gauge
		virt_platform_missing_dependency{group="metallb.io",kind="MetalLB",version="v1beta1"} 1
	`

	if err := testutil.CollectAndCompare(MissingDependency, strings.NewReader(expected)); err != nil {
		t.Errorf("unexpected metric value: %v", err)
	}

	// Mark as present
	SetMissingDependency("metallb.io", "v1beta1", "MetalLB", false)

	expected = `
		# HELP virt_platform_missing_dependency Indicates missing optional CRDs (1=missing, 0=present)
		# TYPE virt_platform_missing_dependency gauge
		virt_platform_missing_dependency{group="metallb.io",kind="MetalLB",version="v1beta1"} 0
	`

	if err := testutil.CollectAndCompare(MissingDependency, strings.NewReader(expected)); err != nil {
		t.Errorf("unexpected metric value after marking present: %v", err)
	}
}

func TestObserveReconcileDuration(t *testing.T) {
	// Reset metrics before test
	ReconcileDuration.Reset()

	obj := &unstructured.Unstructured{}
	obj.SetKind("ConfigMap")
	obj.SetName("test-cm")
	obj.SetNamespace("test-ns")

	// Observe a duration of 250ms
	duration := 250 * time.Millisecond
	ObserveReconcileDuration(obj, duration)

	// We can't use exact comparison for histograms, but we can check the count
	count := testutil.CollectAndCount(ReconcileDuration)
	if count != 1 {
		t.Errorf("expected 1 histogram metric, got %d", count)
	}
}

func TestReconcileDurationTimer(t *testing.T) {
	// Reset metrics before test
	ReconcileDuration.Reset()

	obj := &unstructured.Unstructured{}
	obj.SetKind("ConfigMap")
	obj.SetName("test-cm")
	obj.SetNamespace("test-ns")

	// Use timer pattern
	timer := ReconcileDurationTimer(obj)
	time.Sleep(10 * time.Millisecond) // Simulate some work
	timer.ObserveDuration()

	// Verify histogram was recorded
	count := testutil.CollectAndCount(ReconcileDuration)
	if count != 1 {
		t.Errorf("expected 1 histogram metric after timer, got %d", count)
	}
}

func TestMultipleResources(t *testing.T) {
	// Reset metrics before test
	ComplianceStatus.Reset()

	// Create multiple resources
	cm := &unstructured.Unstructured{}
	cm.SetKind("ConfigMap")
	cm.SetName("test-cm")
	cm.SetNamespace("test-ns")

	deploy := &unstructured.Unstructured{}
	deploy.SetKind("Deployment")
	deploy.SetName("test-deploy")
	deploy.SetNamespace("test-ns")

	// Set compliance for both
	SetCompliance(cm, 1)
	SetCompliance(deploy, 0)

	expected := `
		# HELP virt_platform_compliance_status Compliance status of managed resources (1=synced, 0=drifted/failed)
		# TYPE virt_platform_compliance_status gauge
		virt_platform_compliance_status{kind="ConfigMap",name="test-cm",namespace="test-ns"} 1
		virt_platform_compliance_status{kind="Deployment",name="test-deploy",namespace="test-ns"} 0
	`

	if err := testutil.CollectAndCompare(ComplianceStatus, strings.NewReader(expected)); err != nil {
		t.Errorf("unexpected metric value for multiple resources: %v", err)
	}
}

func TestClusterScopedResources(t *testing.T) {
	// Reset metrics before test
	ComplianceStatus.Reset()

	// Cluster-scoped resources have empty namespace
	obj := &unstructured.Unstructured{}
	obj.SetKind("ClusterRole")
	obj.SetName("test-role")
	obj.SetNamespace("") // Cluster-scoped

	SetCompliance(obj, 1)

	expected := `
		# HELP virt_platform_compliance_status Compliance status of managed resources (1=synced, 0=drifted/failed)
		# TYPE virt_platform_compliance_status gauge
		virt_platform_compliance_status{kind="ClusterRole",name="test-role",namespace=""} 1
	`

	if err := testutil.CollectAndCompare(ComplianceStatus, strings.NewReader(expected)); err != nil {
		t.Errorf("unexpected metric value for cluster-scoped resource: %v", err)
	}
}
