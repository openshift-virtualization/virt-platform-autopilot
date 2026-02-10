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
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
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
	recorder record.EventRecorder
}

// NewEventRecorder creates a new event recorder
func NewEventRecorder(recorder record.EventRecorder) *EventRecorder {
	return &EventRecorder{
		recorder: recorder,
	}
}

// AssetApplied records that an asset was successfully applied
func (e *EventRecorder) AssetApplied(object runtime.Object, assetName, kind, namespace, name string) {
	msg := fmt.Sprintf("Applied asset %s: %s/%s/%s", assetName, kind, namespace, name)
	e.recorder.Event(object, EventTypeNormal, EventReasonAssetApplied, msg)
}

// DriftCorrected records that drift was detected and corrected
func (e *EventRecorder) DriftCorrected(object runtime.Object, kind, namespace, name string) {
	msg := fmt.Sprintf("Corrected drift for %s/%s/%s", kind, namespace, name)
	e.recorder.Event(object, EventTypeNormal, EventReasonDriftCorrected, msg)
}

// DriftDetected records that drift was detected (warning)
func (e *EventRecorder) DriftDetected(object runtime.Object, kind, namespace, name string) {
	msg := fmt.Sprintf("Drift detected for %s/%s/%s", kind, namespace, name)
	e.recorder.Event(object, EventTypeWarning, EventReasonDriftDetected, msg)
}

// PatchApplied records that a user JSON patch was applied
func (e *EventRecorder) PatchApplied(object runtime.Object, kind, namespace, name string, operations int) {
	msg := fmt.Sprintf("Applied %d JSON patch operation(s) to %s/%s/%s", operations, kind, namespace, name)
	e.recorder.Event(object, EventTypeNormal, EventReasonPatchApplied, msg)
}

// InvalidPatch records that a user's JSON patch was invalid
func (e *EventRecorder) InvalidPatch(object runtime.Object, kind, namespace, name, reason string) {
	msg := fmt.Sprintf("Invalid JSON patch for %s/%s/%s: %s", kind, namespace, name, reason)
	e.recorder.Event(object, EventTypeWarning, EventReasonInvalidPatch, msg)
}

// InvalidIgnoreFields records that ignore-fields annotation was invalid
func (e *EventRecorder) InvalidIgnoreFields(object runtime.Object, kind, namespace, name, reason string) {
	msg := fmt.Sprintf("Invalid ignore-fields annotation for %s/%s/%s: %s", kind, namespace, name, reason)
	e.recorder.Event(object, EventTypeWarning, EventReasonInvalidIgnoreFields, msg)
}

// Throttled records that an update was throttled (anti-thrashing)
func (e *EventRecorder) Throttled(object runtime.Object, kind, namespace, name string, capacity int, window string) {
	msg := fmt.Sprintf("Update throttled for %s/%s/%s (limit: %d updates per %s)", kind, namespace, name, capacity, window)
	e.recorder.Event(object, EventTypeWarning, EventReasonThrottled, msg)
}

// ThrashingDetected records that an edit war was detected and reconciliation was paused
func (e *EventRecorder) ThrashingDetected(object runtime.Object, kind, namespace, name string, attempts int) {
	msg := fmt.Sprintf(
		"Edit war detected for %s/%s/%s after %d consecutive throttles. "+
			"Reconciliation paused. Another actor is modifying this resource, "+
			"conflicting with operator management. Remove annotation '%s=true' "+
			"to resume, or set '%s=unmanaged' if external management is intentional.",
		kind, namespace, name, attempts,
		"platform.kubevirt.io/reconcile-paused",
		"platform.kubevirt.io/mode",
	)
	e.recorder.Event(object, EventTypeWarning, EventReasonThrashingDetected, msg)
}

// AssetSkipped records that an asset was skipped (conditions not met)
func (e *EventRecorder) AssetSkipped(object runtime.Object, assetName, reason string) {
	msg := fmt.Sprintf("Skipped asset %s: %s", assetName, reason)
	e.recorder.Event(object, EventTypeNormal, EventReasonAssetSkipped, msg)
}

// UnmanagedMode records that a resource is in unmanaged mode
func (e *EventRecorder) UnmanagedMode(object runtime.Object, kind, namespace, name string) {
	msg := fmt.Sprintf("Resource %s/%s/%s is in unmanaged mode, skipping reconciliation", kind, namespace, name)
	e.recorder.Event(object, EventTypeNormal, EventReasonUnmanagedMode, msg)
}

// CRDMissing records that a required CRD is missing (soft dependency)
func (e *EventRecorder) CRDMissing(object runtime.Object, component, crdName string) {
	msg := fmt.Sprintf("CRD %s not installed, skipping %s assets (soft dependency)", crdName, component)
	e.recorder.Event(object, EventTypeWarning, EventReasonCRDMissing, msg)
}

// CRDDiscovered records that a previously missing CRD was discovered
func (e *EventRecorder) CRDDiscovered(object runtime.Object, component, crdName string) {
	msg := fmt.Sprintf("CRD %s discovered, %s assets can now be reconciled", crdName, component)
	e.recorder.Event(object, EventTypeNormal, EventReasonCRDDiscovered, msg)
}

// ApplyFailed records that applying an asset failed
func (e *EventRecorder) ApplyFailed(object runtime.Object, assetName, reason string) {
	msg := fmt.Sprintf("Failed to apply asset %s: %s", assetName, reason)
	e.recorder.Event(object, EventTypeWarning, EventReasonApplyFailed, msg)
}

// RenderFailed records that rendering an asset template failed
func (e *EventRecorder) RenderFailed(object runtime.Object, assetName, reason string) {
	msg := fmt.Sprintf("Failed to render asset %s: %s", assetName, reason)
	e.recorder.Event(object, EventTypeWarning, EventReasonRenderFailed, msg)
}

// ReconcileSucceeded records successful reconciliation
func (e *EventRecorder) ReconcileSucceeded(object runtime.Object, appliedCount, totalCount int) {
	msg := fmt.Sprintf("Reconciliation succeeded: %d/%d assets applied", appliedCount, totalCount)
	e.recorder.Event(object, EventTypeNormal, EventReasonReconcileSucceeded, msg)
}

// NoDriftDetected records that no drift was detected (informational)
func (e *EventRecorder) NoDriftDetected(object runtime.Object, kind, namespace, name string) {
	msg := fmt.Sprintf("No drift detected for %s/%s/%s", kind, namespace, name)
	e.recorder.Event(object, EventTypeNormal, EventReasonNoDriftDetected, msg)
}

// Eventf records a formatted event
func (e *EventRecorder) Eventf(object runtime.Object, eventType, reason, messageFmt string, args ...interface{}) {
	e.recorder.Eventf(object, eventType, reason, messageFmt, args...)
}

// AnnotatedEventf records an event with annotations
func (e *EventRecorder) AnnotatedEventf(object runtime.Object, annotations map[string]string, eventType, reason, messageFmt string, args ...interface{}) {
	e.recorder.AnnotatedEventf(object, annotations, eventType, reason, messageFmt, args...)
}
