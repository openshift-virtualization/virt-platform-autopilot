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
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/events"
	"k8s.io/klog/v2"
)

// FakeRecorder implements events.EventRecorder for testing
type FakeRecorder struct {
	Events []Event
}

type Event struct {
	EventType string
	Reason    string
	Action    string
	Message   string
}

func (f *FakeRecorder) Eventf(regarding runtime.Object, related runtime.Object, eventtype, reason, action, note string, args ...any) {
	message := fmt.Sprintf(note, args...)
	f.Events = append(f.Events, Event{
		EventType: eventtype,
		Reason:    reason,
		Action:    action,
		Message:   message,
	})
}

func (f *FakeRecorder) Reset() {
	f.Events = []Event{}
}

func (f *FakeRecorder) LastEvent() *Event {
	if len(f.Events) == 0 {
		return nil
	}
	return &f.Events[len(f.Events)-1]
}

// WithLogger returns the same recorder (not needed for testing)
func (f *FakeRecorder) WithLogger(logger klog.Logger) events.EventRecorderLogger {
	return f
}

// Ensure FakeRecorder implements EventRecorderLogger interface
var _ events.EventRecorderLogger = &FakeRecorder{}

func TestEventRecorder_AssetApplied(t *testing.T) {
	fake := &FakeRecorder{}
	recorder := NewEventRecorder(fake)

	obj := &unstructured.Unstructured{}
	obj.SetName("test-obj")

	recorder.AssetApplied(obj, "test-asset", "ConfigMap", "default", "my-config")

	if len(fake.Events) != 1 {
		t.Fatalf("Expected 1 event, got %d", len(fake.Events))
	}

	event := fake.Events[0]
	if event.EventType != EventTypeNormal {
		t.Errorf("Expected EventType=%s, got %s", EventTypeNormal, event.EventType)
	}
	if event.Reason != EventReasonAssetApplied {
		t.Errorf("Expected Reason=%s, got %s", EventReasonAssetApplied, event.Reason)
	}
	if event.Message == "" {
		t.Error("Expected non-empty message")
	}
	if expected := "AssetApplied ConfigMap/default/my-config"; event.Action != expected {
		t.Errorf("Expected Action=%s, got %s", expected, event.Action)
	}
}

func TestEventRecorder_DriftDetected(t *testing.T) {
	fake := &FakeRecorder{}
	recorder := NewEventRecorder(fake)

	obj := &unstructured.Unstructured{}
	recorder.DriftDetected(obj, "Deployment", "default", "nginx")

	event := fake.LastEvent()
	if event == nil {
		t.Fatal("Expected event to be recorded")
	}

	if event.EventType != EventTypeWarning {
		t.Errorf("Expected warning event, got %s", event.EventType)
	}
	if event.Reason != EventReasonDriftDetected {
		t.Errorf("Expected Reason=%s, got %s", EventReasonDriftDetected, event.Reason)
	}
	if expected := "DriftDetected Deployment/default/nginx"; event.Action != expected {
		t.Errorf("Expected Action=%s, got %s", expected, event.Action)
	}
}

func TestEventRecorder_DriftCorrected(t *testing.T) {
	fake := &FakeRecorder{}
	recorder := NewEventRecorder(fake)

	obj := &unstructured.Unstructured{}
	recorder.DriftCorrected(obj, "ConfigMap", "default", "config")

	event := fake.LastEvent()
	if event == nil {
		t.Fatal("Expected event to be recorded")
	}

	if event.EventType != EventTypeNormal {
		t.Errorf("Expected normal event, got %s", event.EventType)
	}
	if event.Reason != EventReasonDriftCorrected {
		t.Errorf("Expected Reason=%s, got %s", EventReasonDriftCorrected, event.Reason)
	}
	if expected := "DriftCorrected ConfigMap/default/config"; event.Action != expected {
		t.Errorf("Expected Action=%s, got %s", expected, event.Action)
	}
}

func TestEventRecorder_PatchApplied(t *testing.T) {
	fake := &FakeRecorder{}
	recorder := NewEventRecorder(fake)

	obj := &unstructured.Unstructured{}
	recorder.PatchApplied(obj, "Deployment", "default", "app", 3)

	event := fake.LastEvent()
	if event == nil {
		t.Fatal("Expected event to be recorded")
	}

	if event.EventType != EventTypeNormal {
		t.Errorf("Expected normal event, got %s", event.EventType)
	}
	if event.Reason != EventReasonPatchApplied {
		t.Errorf("Expected Reason=%s, got %s", EventReasonPatchApplied, event.Reason)
	}
	if expected := "PatchApplied Deployment/default/app"; event.Action != expected {
		t.Errorf("Expected Action=%s, got %s", expected, event.Action)
	}
}

func TestEventRecorder_InvalidPatch(t *testing.T) {
	fake := &FakeRecorder{}
	recorder := NewEventRecorder(fake)

	obj := &unstructured.Unstructured{}
	recorder.InvalidPatch(obj, "ConfigMap", "default", "config", "invalid JSON syntax")

	event := fake.LastEvent()
	if event == nil {
		t.Fatal("Expected event to be recorded")
	}

	if event.EventType != EventTypeWarning {
		t.Errorf("Expected warning event, got %s", event.EventType)
	}
	if event.Reason != EventReasonInvalidPatch {
		t.Errorf("Expected Reason=%s, got %s", EventReasonInvalidPatch, event.Reason)
	}
	if expected := "InvalidPatch ConfigMap/default/config"; event.Action != expected {
		t.Errorf("Expected Action=%s, got %s", expected, event.Action)
	}
}

func TestEventRecorder_Throttled(t *testing.T) {
	fake := &FakeRecorder{}
	recorder := NewEventRecorder(fake)

	obj := &unstructured.Unstructured{}
	recorder.Throttled(obj, "Deployment", "default", "app", 5, "1m")

	event := fake.LastEvent()
	if event == nil {
		t.Fatal("Expected event to be recorded")
	}

	if event.EventType != EventTypeWarning {
		t.Errorf("Expected warning event, got %s", event.EventType)
	}
	if event.Reason != EventReasonThrottled {
		t.Errorf("Expected Reason=%s, got %s", EventReasonThrottled, event.Reason)
	}
	if expected := "Throttled Deployment/default/app"; event.Action != expected {
		t.Errorf("Expected Action=%s, got %s", expected, event.Action)
	}
}

func TestEventRecorder_UnmanagedMode(t *testing.T) {
	fake := &FakeRecorder{}
	recorder := NewEventRecorder(fake)

	obj := &unstructured.Unstructured{}
	recorder.UnmanagedMode(obj, "ConfigMap", "default", "config")

	event := fake.LastEvent()
	if event == nil {
		t.Fatal("Expected event to be recorded")
	}

	if event.EventType != EventTypeNormal {
		t.Errorf("Expected normal event, got %s", event.EventType)
	}
	if event.Reason != EventReasonUnmanagedMode {
		t.Errorf("Expected Reason=%s, got %s", EventReasonUnmanagedMode, event.Reason)
	}
	if expected := "UnmanagedMode ConfigMap/default/config"; event.Action != expected {
		t.Errorf("Expected Action=%s, got %s", expected, event.Action)
	}
}

func TestEventRecorder_CRDMissing(t *testing.T) {
	fake := &FakeRecorder{}
	recorder := NewEventRecorder(fake)

	obj := &unstructured.Unstructured{}
	recorder.CRDMissing(obj, "MetalLB", "metallbs.metallb.io")

	event := fake.LastEvent()
	if event == nil {
		t.Fatal("Expected event to be recorded")
	}

	if event.EventType != EventTypeWarning {
		t.Errorf("Expected warning event, got %s", event.EventType)
	}
	if event.Reason != EventReasonCRDMissing {
		t.Errorf("Expected Reason=%s, got %s", EventReasonCRDMissing, event.Reason)
	}
	if expected := "CRDMissing metallbs.metallb.io"; event.Action != expected {
		t.Errorf("Expected Action=%s, got %s", expected, event.Action)
	}
}

func TestEventRecorder_CRDDiscovered(t *testing.T) {
	fake := &FakeRecorder{}
	recorder := NewEventRecorder(fake)

	obj := &unstructured.Unstructured{}
	recorder.CRDDiscovered(obj, "MetalLB", "metallbs.metallb.io")

	event := fake.LastEvent()
	if event == nil {
		t.Fatal("Expected event to be recorded")
	}

	if event.EventType != EventTypeNormal {
		t.Errorf("Expected normal event, got %s", event.EventType)
	}
	if event.Reason != EventReasonCRDDiscovered {
		t.Errorf("Expected Reason=%s, got %s", EventReasonCRDDiscovered, event.Reason)
	}
	if expected := "CRDDiscovered metallbs.metallb.io"; event.Action != expected {
		t.Errorf("Expected Action=%s, got %s", expected, event.Action)
	}
}

func TestEventRecorder_ApplyFailed(t *testing.T) {
	fake := &FakeRecorder{}
	recorder := NewEventRecorder(fake)

	obj := &unstructured.Unstructured{}
	recorder.ApplyFailed(obj, "test-asset", "validation error")

	event := fake.LastEvent()
	if event == nil {
		t.Fatal("Expected event to be recorded")
	}

	if event.EventType != EventTypeWarning {
		t.Errorf("Expected warning event, got %s", event.EventType)
	}
	if event.Reason != EventReasonApplyFailed {
		t.Errorf("Expected Reason=%s, got %s", EventReasonApplyFailed, event.Reason)
	}
	if expected := "ApplyFailed test-asset"; event.Action != expected {
		t.Errorf("Expected Action=%s, got %s", expected, event.Action)
	}
}

func TestEventRecorder_RenderFailed(t *testing.T) {
	fake := &FakeRecorder{}
	recorder := NewEventRecorder(fake)

	obj := &unstructured.Unstructured{}
	recorder.RenderFailed(obj, "test-asset", "template error")

	event := fake.LastEvent()
	if event == nil {
		t.Fatal("Expected event to be recorded")
	}

	if event.EventType != EventTypeWarning {
		t.Errorf("Expected warning event, got %s", event.EventType)
	}
	if event.Reason != EventReasonRenderFailed {
		t.Errorf("Expected Reason=%s, got %s", EventReasonRenderFailed, event.Reason)
	}
	if expected := "RenderFailed test-asset"; event.Action != expected {
		t.Errorf("Expected Action=%s, got %s", expected, event.Action)
	}
}

func TestEventRecorder_ReconcileSucceeded(t *testing.T) {
	fake := &FakeRecorder{}
	recorder := NewEventRecorder(fake)

	obj := &unstructured.Unstructured{}
	recorder.ReconcileSucceeded(obj, 5, 8)

	event := fake.LastEvent()
	if event == nil {
		t.Fatal("Expected event to be recorded")
	}

	if event.EventType != EventTypeNormal {
		t.Errorf("Expected normal event, got %s", event.EventType)
	}
	if event.Reason != EventReasonReconcileSucceeded {
		t.Errorf("Expected Reason=%s, got %s", EventReasonReconcileSucceeded, event.Reason)
	}
}

func TestEventRecorder_AssetSkipped(t *testing.T) {
	fake := &FakeRecorder{}
	recorder := NewEventRecorder(fake)

	obj := &unstructured.Unstructured{}
	recorder.AssetSkipped(obj, "test-asset", "conditions not met")

	event := fake.LastEvent()
	if event == nil {
		t.Fatal("Expected event to be recorded")
	}

	if event.EventType != EventTypeNormal {
		t.Errorf("Expected normal event, got %s", event.EventType)
	}
	if event.Reason != EventReasonAssetSkipped {
		t.Errorf("Expected Reason=%s, got %s", EventReasonAssetSkipped, event.Reason)
	}
	if expected := "AssetSkipped test-asset"; event.Action != expected {
		t.Errorf("Expected Action=%s, got %s", expected, event.Action)
	}
}

func TestEventRecorder_MultipleEvents(t *testing.T) {
	fake := &FakeRecorder{}
	recorder := NewEventRecorder(fake)

	obj := &unstructured.Unstructured{}

	// Record multiple events
	recorder.DriftDetected(obj, "ConfigMap", "default", "config")
	recorder.AssetApplied(obj, "asset1", "ConfigMap", "default", "config")
	recorder.DriftCorrected(obj, "ConfigMap", "default", "config")

	if len(fake.Events) != 3 {
		t.Fatalf("Expected 3 events, got %d", len(fake.Events))
	}

	// Verify event order
	if fake.Events[0].Reason != EventReasonDriftDetected {
		t.Errorf("First event should be DriftDetected, got %s", fake.Events[0].Reason)
	}
	if fake.Events[1].Reason != EventReasonAssetApplied {
		t.Errorf("Second event should be AssetApplied, got %s", fake.Events[1].Reason)
	}
	if fake.Events[2].Reason != EventReasonDriftCorrected {
		t.Errorf("Third event should be DriftCorrected, got %s", fake.Events[2].Reason)
	}
}

func TestEventRecorder_InvalidIgnoreFields(t *testing.T) {
	fake := &FakeRecorder{}
	recorder := NewEventRecorder(fake)

	obj := &unstructured.Unstructured{}
	obj.SetName("test-obj")

	recorder.InvalidIgnoreFields(obj, "ConfigMap", "default", "my-config", "invalid pointer")

	if len(fake.Events) != 1 {
		t.Fatalf("Expected 1 event, got %d", len(fake.Events))
	}

	event := fake.Events[0]
	if event.EventType != EventTypeWarning {
		t.Errorf("Expected EventType=%s, got %s", EventTypeWarning, event.EventType)
	}
	if event.Reason != EventReasonInvalidIgnoreFields {
		t.Errorf("Expected Reason=%s, got %s", EventReasonInvalidIgnoreFields, event.Reason)
	}
	if expected := "InvalidIgnoreFields ConfigMap/default/my-config"; event.Action != expected {
		t.Errorf("Expected Action=%s, got %s", expected, event.Action)
	}
}

func TestEventRecorder_NoDriftDetected(t *testing.T) {
	fake := &FakeRecorder{}
	recorder := NewEventRecorder(fake)

	obj := &unstructured.Unstructured{}
	obj.SetName("test-obj")

	recorder.NoDriftDetected(obj, "ConfigMap", "default", "my-config")

	if len(fake.Events) != 1 {
		t.Fatalf("Expected 1 event, got %d", len(fake.Events))
	}

	event := fake.Events[0]
	if event.EventType != EventTypeNormal {
		t.Errorf("Expected EventType=%s, got %s", EventTypeNormal, event.EventType)
	}
	if event.Reason != EventReasonNoDriftDetected {
		t.Errorf("Expected Reason=%s, got %s", EventReasonNoDriftDetected, event.Reason)
	}
	if expected := "NoDriftDetected ConfigMap/default/my-config"; event.Action != expected {
		t.Errorf("Expected Action=%s, got %s", expected, event.Action)
	}
}

func TestAssetAction(t *testing.T) {
	result := assetAction("DriftDetected", "ConfigMap", "default", "my-config")
	expected := "DriftDetected ConfigMap/default/my-config"
	if result != expected {
		t.Errorf("Expected %q, got %q", expected, result)
	}
}

func TestAssetNameAction(t *testing.T) {
	result := assetNameAction("ApplyFailed", "my-asset")
	expected := "ApplyFailed my-asset"
	if result != expected {
		t.Errorf("Expected %q, got %q", expected, result)
	}
}

func TestEventRecorder_DeduplicationKeys(t *testing.T) {
	fake := &FakeRecorder{}
	recorder := NewEventRecorder(fake)
	obj := &unstructured.Unstructured{}

	recorder.DriftDetected(obj, "ConfigMap", "default", "config-a")
	recorder.DriftDetected(obj, "ConfigMap", "default", "config-b")

	if len(fake.Events) != 2 {
		t.Fatalf("Expected 2 events, got %d", len(fake.Events))
	}

	if fake.Events[0].Action == fake.Events[1].Action {
		t.Errorf("Actions should differ for different assets: both are %q", fake.Events[0].Action)
	}

	if fake.Events[0].Reason != fake.Events[1].Reason {
		t.Errorf("Reasons should be the same: got %q and %q", fake.Events[0].Reason, fake.Events[1].Reason)
	}
}
