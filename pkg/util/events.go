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
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
)

// Event reasons
const (
	EventReasonReconciliationSucceeded = "ReconciliationSucceeded"
	EventReasonReconciliationFailed    = "ReconciliationFailed"
	EventReasonAssetApplied            = "AssetApplied"
	EventReasonAssetSkipped            = "AssetSkipped"
	EventReasonInvalidPatch            = "InvalidPatch"
	EventReasonInvalidIgnoreFields     = "InvalidIgnoreFields"
	EventReasonPatchBlocked            = "PatchBlocked"
	EventReasonThrashingDetected       = "ThrashingDetected"
	EventReasonCRDMissing              = "CRDMissing"
	EventReasonDriftDetected           = "DriftDetected"
)

// RecordReconciliationSucceeded records a successful reconciliation event
func RecordReconciliationSucceeded(recorder record.EventRecorder, obj runtime.Object, message string) {
	recorder.Event(obj, "Normal", EventReasonReconciliationSucceeded, message)
}

// RecordReconciliationFailed records a failed reconciliation event
func RecordReconciliationFailed(recorder record.EventRecorder, obj runtime.Object, err error) {
	recorder.Event(obj, "Warning", EventReasonReconciliationFailed, err.Error())
}

// RecordAssetApplied records an asset application event
func RecordAssetApplied(recorder record.EventRecorder, obj runtime.Object, assetName string) {
	recorder.Eventf(obj, "Normal", EventReasonAssetApplied, "Applied asset: %s", assetName)
}

// RecordAssetSkipped records an asset skip event
func RecordAssetSkipped(recorder record.EventRecorder, obj runtime.Object, assetName, reason string) {
	recorder.Eventf(obj, "Normal", EventReasonAssetSkipped, "Skipped asset %s: %s", assetName, reason)
}

// RecordInvalidPatch records an invalid patch event
func RecordInvalidPatch(recorder record.EventRecorder, obj runtime.Object, err error) {
	recorder.Eventf(obj, "Warning", EventReasonInvalidPatch, "Invalid JSON patch: %v", err)
}

// RecordInvalidIgnoreFields records an invalid ignore-fields event
func RecordInvalidIgnoreFields(recorder record.EventRecorder, obj runtime.Object, err error) {
	recorder.Eventf(obj, "Warning", EventReasonInvalidIgnoreFields, "Invalid ignore-fields annotation: %v", err)
}

// RecordPatchBlocked records a blocked patch event
func RecordPatchBlocked(recorder record.EventRecorder, obj runtime.Object, kind string) {
	recorder.Eventf(obj, "Warning", EventReasonPatchBlocked, "JSON patch blocked on sensitive resource: %s", kind)
}

// RecordThrashingDetected records a thrashing detection event
func RecordThrashingDetected(recorder record.EventRecorder, obj runtime.Object, resourceKey string) {
	recorder.Eventf(obj, "Warning", EventReasonThrashingDetected, "Update throttled for resource: %s", resourceKey)
}

// RecordCRDMissing records a missing CRD event
func RecordCRDMissing(recorder record.EventRecorder, obj runtime.Object, crdName string) {
	recorder.Eventf(obj, "Warning", EventReasonCRDMissing, "CRD not found, skipping asset: %s", crdName)
}

// RecordDriftDetected records a drift detection event
func RecordDriftDetected(recorder record.EventRecorder, obj runtime.Object, resourceName string) {
	recorder.Eventf(obj, "Normal", EventReasonDriftDetected, "Configuration drift detected: %s", resourceName)
}
