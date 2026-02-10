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

package util

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/events"
)

// Event types for the operator
const (
	// EventTypeNormal represents normal, informational events
	EventTypeNormal = corev1.EventTypeNormal

	// EventTypeWarning represents warning events that may require attention
	EventTypeWarning = corev1.EventTypeWarning
)

// Event reasons - these appear in kubectl get events
const (
	// Successful operations
	EventReasonAssetApplied       = "AssetApplied"
	EventReasonDriftCorrected     = "DriftCorrected"
	EventReasonPatchApplied       = "PatchApplied"
	EventReasonReconcileSucceeded = "ReconcileSucceeded"
	EventReasonCRDDiscovered      = "CRDDiscovered"

	// Informational events
	EventReasonAssetSkipped    = "AssetSkipped"
	EventReasonNoDriftDetected = "NoDriftDetected"
	EventReasonUnmanagedMode   = "UnmanagedMode"

	// Warning events
	EventReasonDriftDetected       = "DriftDetected"
	EventReasonThrottled           = "Throttled"
	EventReasonThrashingDetected   = "ThrashingDetected"
	EventReasonInvalidPatch        = "InvalidPatch"
	EventReasonInvalidIgnoreFields = "InvalidIgnoreFields"
	EventReasonCRDMissing          = "CRDMissing"
	EventReasonApplyFailed         = "ApplyFailed"
	EventReasonRenderFailed        = "RenderFailed"
)

// EventRecorder wraps the Kubernetes event recorder with helper methods
type EventRecorder struct {
	recorder events.EventRecorder
}

// NewEventRecorder creates a new event recorder
func NewEventRecorder(recorder events.EventRecorder) *EventRecorder {
	return &EventRecorder{
		recorder: recorder,
	}
}

// AssetApplied records that an asset was successfully applied
func (e *EventRecorder) AssetApplied(object runtime.Object, assetName, kind, namespace, name string) {
	e.recorder.Eventf(object, nil, EventTypeNormal, EventReasonAssetApplied, "AssetApplied",
		"Applied asset %s: %s/%s/%s", assetName, kind, namespace, name)
}

// DriftCorrected records that drift was detected and corrected
func (e *EventRecorder) DriftCorrected(object runtime.Object, kind, namespace, name string) {
	e.recorder.Eventf(object, nil, EventTypeNormal, EventReasonDriftCorrected, "DriftCorrected",
		"Corrected drift for %s/%s/%s", kind, namespace, name)
}

// DriftDetected records that drift was detected (warning)
func (e *EventRecorder) DriftDetected(object runtime.Object, kind, namespace, name string) {
	e.recorder.Eventf(object, nil, EventTypeWarning, EventReasonDriftDetected, "DriftDetected",
		"Drift detected for %s/%s/%s", kind, namespace, name)
}

// PatchApplied records that a user JSON patch was applied
func (e *EventRecorder) PatchApplied(object runtime.Object, kind, namespace, name string, operations int) {
	e.recorder.Eventf(object, nil, EventTypeNormal, EventReasonPatchApplied, "PatchApplied",
		"Applied %d JSON patch operation(s) to %s/%s/%s", operations, kind, namespace, name)
}

// InvalidPatch records that a user's JSON patch was invalid
func (e *EventRecorder) InvalidPatch(object runtime.Object, kind, namespace, name, reason string) {
	e.recorder.Eventf(object, nil, EventTypeWarning, EventReasonInvalidPatch, "InvalidPatch",
		"Invalid JSON patch for %s/%s/%s: %s", kind, namespace, name, reason)
}

// InvalidIgnoreFields records that ignore-fields annotation was invalid
func (e *EventRecorder) InvalidIgnoreFields(object runtime.Object, kind, namespace, name, reason string) {
	e.recorder.Eventf(object, nil, EventTypeWarning, EventReasonInvalidIgnoreFields, "InvalidIgnoreFields",
		"Invalid ignore-fields annotation for %s/%s/%s: %s", kind, namespace, name, reason)
}

// Throttled records that an update was throttled (anti-thrashing)
func (e *EventRecorder) Throttled(object runtime.Object, kind, namespace, name string, capacity int, window string) {
	e.recorder.Eventf(object, nil, EventTypeWarning, EventReasonThrottled, "Throttled",
		"Update throttled for %s/%s/%s (limit: %d updates per %s)", kind, namespace, name, capacity, window)
}

// ThrashingDetected records that an edit war was detected and reconciliation was paused
func (e *EventRecorder) ThrashingDetected(object runtime.Object, kind, namespace, name string, attempts int) {
	e.recorder.Eventf(object, nil, EventTypeWarning, EventReasonThrashingDetected, "ThrashingDetected",
		"Edit war detected for %s/%s/%s after %d consecutive throttles. "+
			"Reconciliation paused. Another actor is modifying this resource, "+
			"conflicting with operator management. Remove annotation '%s=true' "+
			"to resume, or set '%s=unmanaged' if external management is intentional.",
		kind, namespace, name, attempts,
		"platform.kubevirt.io/reconcile-paused",
		"platform.kubevirt.io/mode")
}

// AssetSkipped records that an asset was skipped (conditions not met)
func (e *EventRecorder) AssetSkipped(object runtime.Object, assetName, reason string) {
	e.recorder.Eventf(object, nil, EventTypeNormal, EventReasonAssetSkipped, "AssetSkipped",
		"Skipped asset %s: %s", assetName, reason)
}

// UnmanagedMode records that a resource is in unmanaged mode
func (e *EventRecorder) UnmanagedMode(object runtime.Object, kind, namespace, name string) {
	e.recorder.Eventf(object, nil, EventTypeNormal, EventReasonUnmanagedMode, "UnmanagedMode",
		"Resource %s/%s/%s is in unmanaged mode, skipping reconciliation", kind, namespace, name)
}

// CRDMissing records that a required CRD is missing (soft dependency)
func (e *EventRecorder) CRDMissing(object runtime.Object, component, crdName string) {
	e.recorder.Eventf(object, nil, EventTypeWarning, EventReasonCRDMissing, "CRDMissing",
		"CRD %s not installed, skipping %s assets (soft dependency)", crdName, component)
}

// CRDDiscovered records that a previously missing CRD was discovered
func (e *EventRecorder) CRDDiscovered(object runtime.Object, component, crdName string) {
	e.recorder.Eventf(object, nil, EventTypeNormal, EventReasonCRDDiscovered, "CRDDiscovered",
		"CRD %s discovered, %s assets can now be reconciled", crdName, component)
}

// ApplyFailed records that applying an asset failed
func (e *EventRecorder) ApplyFailed(object runtime.Object, assetName, reason string) {
	e.recorder.Eventf(object, nil, EventTypeWarning, EventReasonApplyFailed, "ApplyFailed",
		"Failed to apply asset %s: %s", assetName, reason)
}

// RenderFailed records that rendering an asset template failed
func (e *EventRecorder) RenderFailed(object runtime.Object, assetName, reason string) {
	e.recorder.Eventf(object, nil, EventTypeWarning, EventReasonRenderFailed, "RenderFailed",
		"Failed to render asset %s: %s", assetName, reason)
}

// ReconcileSucceeded records successful reconciliation
func (e *EventRecorder) ReconcileSucceeded(object runtime.Object, appliedCount, totalCount int) {
	e.recorder.Eventf(object, nil, EventTypeNormal, EventReasonReconcileSucceeded, "ReconcileSucceeded",
		"Reconciliation succeeded: %d/%d assets applied", appliedCount, totalCount)
}

// NoDriftDetected records that no drift was detected (informational)
func (e *EventRecorder) NoDriftDetected(object runtime.Object, kind, namespace, name string) {
	e.recorder.Eventf(object, nil, EventTypeNormal, EventReasonNoDriftDetected, "NoDriftDetected",
		"No drift detected for %s/%s/%s", kind, namespace, name)
}
