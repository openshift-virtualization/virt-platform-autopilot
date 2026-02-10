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
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

const (
	// Metric subsystem name
	subsystem = "virt_platform"
)

var (
	// ComplianceStatus tracks whether each managed resource is in sync with desired state.
	// 1 = Synced (Golden State matches Live), 0 = Drifted/Sync Failed
	// This is the core health indicator used by the VirtPlatformSyncFailed alert.
	ComplianceStatus = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Subsystem: subsystem,
			Name:      "compliance_status",
			Help:      "Compliance status of managed resources (1=synced, 0=drifted/failed)",
		},
		[]string{"kind", "name", "namespace"},
	)

	// ThrashingTotal counts reconciliation throttling events (token bucket exhaustion).
	// Increments when the "Reconcile Gate" is hit (update budget exhausted).
	// Indicates an active "Edit War" between the operator and external changes.
	ThrashingTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Subsystem: subsystem,
			Name:      "thrashing_total",
			Help:      "Total number of reconciliation throttling events (anti-thrashing gate hits)",
		},
		[]string{"kind", "name", "namespace"},
	)

	// CustomizationInfo tracks intentional deviations from the Golden State.
	// Always set to 1 when customization exists. Type indicates: patch, ignore, or unmanaged.
	// Useful for Support to see "Is this cluster stock or customized?" without digging into YAML.
	CustomizationInfo = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Subsystem: subsystem,
			Name:      "customization_info",
			Help:      "Tracks intentional customizations (always 1 when present). Type: patch/ignore/unmanaged",
		},
		[]string{"kind", "name", "namespace", "type"},
	)

	// MissingDependency tracks missing optional CRDs (soft dependencies).
	// 1 if a managed CRD (e.g., KubeDescheduler) is missing, 0 otherwise.
	// Distinguishes "Broken" (asset failed) from "Not Installed" (CRD missing).
	MissingDependency = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Subsystem: subsystem,
			Name:      "missing_dependency",
			Help:      "Indicates missing optional CRDs (1=missing, 0=present)",
		},
		[]string{"group", "version", "kind"},
	)

	// ReconcileDuration tracks how long asset reconciliation takes.
	// Measures SSA apply and rendering logic duration.
	// High latency implies API server stress or complex asset rendering.
	ReconcileDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Subsystem: subsystem,
			Name:      "reconcile_duration_seconds",
			Help:      "Duration of asset reconciliation operations (rendering + SSA apply)",
			Buckets:   []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0},
		},
		[]string{"kind", "name", "namespace"},
	)
)

func init() {
	// Register all metrics with controller-runtime's metrics registry
	// This registry is automatically exposed on :8080/metrics by the manager
	metrics.Registry.MustRegister(
		ComplianceStatus,
		ThrashingTotal,
		CustomizationInfo,
		MissingDependency,
		ReconcileDuration,
	)
}

// SetCompliance sets the compliance status for a managed resource.
// status: 1 = synced, 0 = drifted/failed
func SetCompliance(obj *unstructured.Unstructured, status float64) {
	ComplianceStatus.WithLabelValues(
		obj.GetKind(),
		obj.GetName(),
		obj.GetNamespace(),
	).Set(status)
}

// IncThrashing increments the thrashing counter for a managed resource.
// Called when token bucket is exhausted (anti-thrashing gate triggered).
func IncThrashing(obj *unstructured.Unstructured) {
	ThrashingTotal.WithLabelValues(
		obj.GetKind(),
		obj.GetName(),
		obj.GetNamespace(),
	).Inc()
}

// SetCustomization records an intentional customization on a managed resource.
// customizationType: "patch", "ignore", or "unmanaged"
func SetCustomization(obj *unstructured.Unstructured, customizationType string) {
	CustomizationInfo.WithLabelValues(
		obj.GetKind(),
		obj.GetName(),
		obj.GetNamespace(),
		customizationType,
	).Set(1)
}

// ClearCustomization removes a customization metric when the annotation is removed.
func ClearCustomization(obj *unstructured.Unstructured, customizationType string) {
	CustomizationInfo.DeleteLabelValues(
		obj.GetKind(),
		obj.GetName(),
		obj.GetNamespace(),
		customizationType,
	)
}

// SetMissingDependency marks a CRD as missing (1) or present (0).
// group, version, kind: The GVK of the missing CRD
func SetMissingDependency(group, version, kind string, missing bool) {
	value := 0.0
	if missing {
		value = 1.0
	}
	MissingDependency.WithLabelValues(group, version, kind).Set(value)
}

// ObserveReconcileDuration records the duration of a reconciliation operation.
// Use with prometheus.NewTimer() for automatic duration tracking.
func ObserveReconcileDuration(obj *unstructured.Unstructured, duration time.Duration) {
	ReconcileDuration.WithLabelValues(
		obj.GetKind(),
		obj.GetName(),
		obj.GetNamespace(),
	).Observe(duration.Seconds())
}

// ReconcileDurationTimer returns a prometheus.Timer for measuring reconciliation duration.
// Usage:
//
//	timer := ReconcileDurationTimer(obj)
//	defer timer.ObserveDuration()
func ReconcileDurationTimer(obj *unstructured.Unstructured) *prometheus.Timer {
	return prometheus.NewTimer(ReconcileDuration.WithLabelValues(
		obj.GetKind(),
		obj.GetName(),
		obj.GetNamespace(),
	))
}
