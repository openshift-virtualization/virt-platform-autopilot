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

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// TestMetricLabelConsistency ensures all metrics define and use labels consistently
func TestMetricLabelConsistency(t *testing.T) {
	tests := []struct {
		name           string
		metric         prometheus.Collector
		expectedLabels []string
		setupFunc      func() // Function that emits the metric with all labels
	}{
		{
			name:           "ComplianceStatus has correct labels",
			metric:         ComplianceStatus,
			expectedLabels: []string{"kind", "name", "namespace"},
			setupFunc:      setupComplianceMetric,
		},
		{
			name:           "ThrashingTotal has correct labels",
			metric:         ThrashingTotal,
			expectedLabels: []string{"kind", "name", "namespace"},
			setupFunc:      setupThrashingMetric,
		},
		{
			name:           "CustomizationInfo has correct labels",
			metric:         CustomizationInfo,
			expectedLabels: []string{"kind", "name", "namespace", "type"},
			setupFunc:      setupCustomizationMetric,
		},
		{
			name:           "MissingDependency has correct labels",
			metric:         MissingDependency,
			expectedLabels: []string{"group", "kind", "version"},
			setupFunc:      setupMissingDependencyMetric,
		},
		{
			name:           "ReconcileDuration has correct labels",
			metric:         ReconcileDuration,
			expectedLabels: []string{"kind", "name", "namespace"},
			setupFunc:      setupReconcileDurationMetric,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			verifyMetricLabels(t, tt.metric, tt.expectedLabels, tt.setupFunc)
		})
	}
}

// verifyMetricLabels is extracted to reduce cognitive complexity
func verifyMetricLabels(t *testing.T, metric prometheus.Collector, expectedLabels []string, setupFunc func()) {
	t.Helper()

	// Reset metric before test
	resetMetric(metric)

	// Emit metric using the setup function
	setupFunc()

	// Collect metrics
	metricFamilies, err := collectMetrics(metric)
	if err != nil {
		t.Fatalf("failed to collect metrics: %v", err)
	}

	if len(metricFamilies) == 0 {
		t.Fatal("no metrics collected")
	}

	// Get first data point
	mf := metricFamilies[0]
	if len(mf.Metric) == 0 {
		t.Fatal("metric family has no data points")
	}

	// Extract and verify labels
	actualLabels := extractLabelNames(mf.Metric[0])
	verifyLabelsMatch(t, expectedLabels, actualLabels)
}

// extractLabelNames extracts label names from a metric
func extractLabelNames(metric *dto.Metric) []string {
	var labels []string
	for _, label := range metric.Label {
		labels = append(labels, *label.Name)
	}
	return labels
}

// verifyLabelsMatch checks that expected labels match actual labels
func verifyLabelsMatch(t *testing.T, expected, actual []string) {
	t.Helper()

	if len(actual) != len(expected) {
		t.Errorf("expected %d labels, got %d\nExpected: %v\nActual: %v",
			len(expected), len(actual), expected, actual)
	}

	for _, expectedLabel := range expected {
		if !contains(actual, expectedLabel) {
			t.Errorf("expected label %q not found in actual labels %v", expectedLabel, actual)
		}
	}
}

// contains checks if a slice contains a string
func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

// Setup functions for each metric type

func setupComplianceMetric() {
	obj := &unstructured.Unstructured{}
	obj.SetKind("TestKind")
	obj.SetName("test-name")
	obj.SetNamespace("test-ns")
	SetCompliance(obj, 1)
}

func setupThrashingMetric() {
	obj := &unstructured.Unstructured{}
	obj.SetKind("TestKind")
	obj.SetName("test-name")
	obj.SetNamespace("test-ns")
	IncThrashing(obj)
}

func setupCustomizationMetric() {
	obj := &unstructured.Unstructured{}
	obj.SetKind("TestKind")
	obj.SetName("test-name")
	obj.SetNamespace("test-ns")
	SetCustomization(obj, "patch")
}

func setupMissingDependencyMetric() {
	SetMissingDependency("test.io", "v1", "TestKind", true)
}

func setupReconcileDurationMetric() {
	obj := &unstructured.Unstructured{}
	obj.SetKind("TestKind")
	obj.SetName("test-name")
	obj.SetNamespace("test-ns")
	ObserveReconcileDuration(obj, 100*time.Millisecond)
}

// TestCustomizationTypes verifies all three customization types emit metrics with correct labels
func TestCustomizationTypes(t *testing.T) {
	CustomizationInfo.Reset()

	obj := &unstructured.Unstructured{}
	obj.SetKind("HyperConverged")
	obj.SetName("test-hco")
	obj.SetNamespace("test-ns")

	types := []string{"patch", "ignore", "unmanaged"}

	for _, custType := range types {
		SetCustomization(obj, custType)
	}

	// Collect metrics
	metricFamilies, err := collectMetrics(CustomizationInfo)
	if err != nil {
		t.Fatalf("failed to collect metrics: %v", err)
	}

	if len(metricFamilies) == 0 {
		t.Fatal("no metrics collected")
	}

	mf := metricFamilies[0]

	// Should have 3 data points (one for each customization type)
	if len(mf.Metric) != 3 {
		t.Errorf("expected 3 customization metrics, got %d", len(mf.Metric))
	}

	// Verify each metric has all required labels with correct values
	for _, metric := range mf.Metric {
		labels := make(map[string]string)
		for _, label := range metric.Label {
			labels[*label.Name] = *label.Value
		}

		// Check all expected labels exist
		expectedLabels := []string{"kind", "name", "namespace", "type"}
		for _, expectedLabel := range expectedLabels {
			if _, exists := labels[expectedLabel]; !exists {
				t.Errorf("missing label %q in metric", expectedLabel)
			}
		}

		// Verify label values are correct
		if labels["kind"] != "HyperConverged" {
			t.Errorf("expected kind=HyperConverged, got %q", labels["kind"])
		}
		if labels["name"] != "test-hco" {
			t.Errorf("expected name=test-hco, got %q", labels["name"])
		}
		if labels["namespace"] != "test-ns" {
			t.Errorf("expected namespace=test-ns, got %q", labels["namespace"])
		}

		// Verify type is one of the expected values
		custType := labels["type"]
		validType := false
		for _, validValue := range types {
			if custType == validValue {
				validType = true
				break
			}
		}
		if !validType {
			t.Errorf("unexpected customization type %q, expected one of %v", custType, types)
		}
	}
}

// TestLabelOrder verifies that WithLabelValues calls match metric definition order
// This catches bugs where labels are passed in wrong order
func TestLabelOrder(t *testing.T) {
	ComplianceStatus.Reset()

	obj := &unstructured.Unstructured{}
	obj.SetKind("ConfigMap")
	obj.SetName("my-config")
	obj.SetNamespace("my-namespace")

	SetCompliance(obj, 1)

	// Collect and verify label order
	metricFamilies, err := collectMetrics(ComplianceStatus)
	if err != nil {
		t.Fatalf("failed to collect metrics: %v", err)
	}

	metric := metricFamilies[0].Metric[0]
	labels := metric.Label

	// ComplianceStatus labels should be in this order: kind, name, namespace
	expectedOrder := []struct {
		name  string
		value string
	}{
		{"kind", "ConfigMap"},
		{"name", "my-config"},
		{"namespace", "my-namespace"},
	}

	// Note: Prometheus may sort labels alphabetically, so we check values not order
	labelMap := make(map[string]string)
	for _, label := range labels {
		labelMap[*label.Name] = *label.Value
	}

	for _, expected := range expectedOrder {
		if labelMap[expected.name] != expected.value {
			t.Errorf("label %q: expected %q, got %q",
				expected.name, expected.value, labelMap[expected.name])
		}
	}
}

// Helper functions

func resetMetric(metric prometheus.Collector) {
	switch m := metric.(type) {
	case *prometheus.GaugeVec:
		m.Reset()
	case *prometheus.CounterVec:
		m.Reset()
	case *prometheus.HistogramVec:
		m.Reset()
	}
}

func collectMetrics(metric prometheus.Collector) ([]*dto.MetricFamily, error) {
	// Create a temporary registry
	registry := prometheus.NewRegistry()
	registry.MustRegister(metric)

	// Gather metrics
	return registry.Gather()
}

// TestAllMetricsHaveSubsystem ensures all metrics use the correct subsystem
func TestAllMetricsHaveSubsystem(t *testing.T) {
	metrics := []prometheus.Collector{
		ComplianceStatus,
		ThrashingTotal,
		CustomizationInfo,
		MissingDependency,
		ReconcileDuration,
	}

	expectedSubsystem := "virt_platform"

	for i, metric := range metrics {
		metricFamilies, err := collectMetrics(metric)
		if err != nil {
			t.Fatalf("metric %d: failed to collect: %v", i, err)
		}

		if len(metricFamilies) == 0 {
			// Metric might not have data yet, skip
			continue
		}

		name := *metricFamilies[0].Name
		if !strings.HasPrefix(name, expectedSubsystem+"_") {
			t.Errorf("metric %q does not have subsystem prefix %q", name, expectedSubsystem)
		}
	}
}
