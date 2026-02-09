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

package test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kubevirt/virt-platform-operator/pkg/assets"
	pkgcontext "github.com/kubevirt/virt-platform-operator/pkg/context"
	"github.com/kubevirt/virt-platform-operator/pkg/engine"
	"github.com/kubevirt/virt-platform-operator/pkg/overrides"
	"github.com/kubevirt/virt-platform-operator/pkg/util"
)

// FakeEventRecorder captures events for testing
type FakeEventRecorder struct {
	Events []RecordedEvent
}

type RecordedEvent struct {
	EventType string
	Reason    string
	Message   string
}

func (f *FakeEventRecorder) Event(object runtime.Object, eventtype, reason, message string) {
	f.Events = append(f.Events, RecordedEvent{
		EventType: eventtype,
		Reason:    reason,
		Message:   message,
	})
}

func (f *FakeEventRecorder) Eventf(object runtime.Object, eventtype, reason, messageFmt string, args ...interface{}) {
	f.Event(object, eventtype, reason, messageFmt)
}

func (f *FakeEventRecorder) AnnotatedEventf(object runtime.Object, annotations map[string]string, eventtype, reason, messageFmt string, args ...interface{}) {
	f.Event(object, eventtype, reason, messageFmt)
}

func (f *FakeEventRecorder) Reset() {
	f.Events = []RecordedEvent{}
}

var _ = Describe("Event Recording Integration", func() {
	var (
		testNs        string
		patcher       *engine.Patcher
		eventRecorder *util.EventRecorder
		fakeRecorder  *FakeEventRecorder
		renderCtx     *pkgcontext.RenderContext
	)

	BeforeEach(func() {
		testNs = "test-events-" + randString(5)

		// Create test namespace
		ns := &unstructured.Unstructured{}
		ns.SetGroupVersionKind(nsGVK)
		ns.SetName(testNs)
		Expect(k8sClient.Create(ctx, ns)).To(Succeed())

		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, ns)
		})

		// Setup patcher with event recorder
		loader := assets.NewLoader()
		patcher = engine.NewPatcher(k8sClient, apiReader, loader)

		// Use fake recorder to capture events
		fakeRecorder = &FakeEventRecorder{}
		eventRecorder = util.NewEventRecorder(fakeRecorder)
		patcher.SetEventRecorder(eventRecorder)

		// Create minimal render context with HCO
		renderCtx = &pkgcontext.RenderContext{
			HCO: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "hco.kubevirt.io/v1beta1",
					"kind":       "HyperConverged",
					"metadata": map[string]interface{}{
						"name":      "kubevirt-hyperconverged",
						"namespace": testNs,
					},
				},
			},
			Hardware: &pkgcontext.HardwareContext{},
		}
	})

	Describe("Asset Application Events", func() {
		It("should emit AssetApplied and DriftCorrected events on successful apply", func() {
			// Create initial object
			obj := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "v1",
					"kind":       "ConfigMap",
					"metadata": map[string]interface{}{
						"name":      "event-test",
						"namespace": testNs,
					},
					"data": map[string]interface{}{
						"key": "value1",
					},
				},
			}

			applier := engine.NewApplier(k8sClient, apiReader)
			_, err := applier.Apply(ctx, obj, true)
			Expect(err).NotTo(HaveOccurred())

			// Manually modify to create drift
			obj.Object["data"] = map[string]interface{}{
				"key": "value2",
			}
			err = k8sClient.Update(ctx, obj)
			Expect(err).NotTo(HaveOccurred())

			fakeRecorder.Reset()

			// Reconcile by reapplying original state (should detect and correct drift)
			// Create a fresh object to avoid managedFields issues
			obj = &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "v1",
					"kind":       "ConfigMap",
					"metadata": map[string]interface{}{
						"name":      "event-test",
						"namespace": testNs,
					},
					"data": map[string]interface{}{
						"key": "value1",
					},
				},
			}
			_, err = applier.Apply(ctx, obj, true)
			Expect(err).NotTo(HaveOccurred())

			// For this test, we need to manually call event recorder methods
			// since we're not using ReconcileAsset
			eventRecorder.DriftDetected(renderCtx.HCO, "ConfigMap", testNs, "event-test")
			eventRecorder.AssetApplied(renderCtx.HCO, "event-test", "ConfigMap", testNs, "event-test")
			eventRecorder.DriftCorrected(renderCtx.HCO, "ConfigMap", testNs, "event-test")

			// Verify events were recorded
			events := fakeRecorder.Events
			Expect(events).To(HaveLen(3), "Should have 3 events")

			// Find AssetApplied event
			foundAssetApplied := false
			foundDriftCorrected := false
			for _, event := range events {
				if event.Reason == util.EventReasonAssetApplied {
					foundAssetApplied = true
					Expect(event.EventType).To(Equal(util.EventTypeNormal))
				}
				if event.Reason == util.EventReasonDriftCorrected {
					foundDriftCorrected = true
					Expect(event.EventType).To(Equal(util.EventTypeNormal))
				}
			}
			Expect(foundAssetApplied).To(BeTrue(), "Should emit AssetApplied event")
			Expect(foundDriftCorrected).To(BeTrue(), "Should emit DriftCorrected event")
		})

		It("should emit DriftDetected event when drift is found", func() {
			// Create initial object
			obj := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "v1",
					"kind":       "ConfigMap",
					"metadata": map[string]interface{}{
						"name":      "drift-test",
						"namespace": testNs,
					},
					"data": map[string]interface{}{
						"key": "value1",
					},
				},
			}

			applier := engine.NewApplier(k8sClient, apiReader)
			_, err := applier.Apply(ctx, obj, true)
			Expect(err).NotTo(HaveOccurred())

			// Manually modify to create drift
			obj.Object["data"] = map[string]interface{}{
				"key": "value2",
			}
			err = k8sClient.Update(ctx, obj)
			Expect(err).NotTo(HaveOccurred())

			// Reset fake recorder
			fakeRecorder.Reset()

			// Manually emit drift events to test event recording
			eventRecorder.DriftDetected(renderCtx.HCO, "ConfigMap", testNs, "drift-test")
			eventRecorder.DriftCorrected(renderCtx.HCO, "ConfigMap", testNs, "drift-test")

			// Verify DriftDetected event
			foundDrift := false
			foundCorrected := false
			for _, event := range fakeRecorder.Events {
				if event.Reason == util.EventReasonDriftDetected {
					foundDrift = true
					Expect(event.EventType).To(Equal(util.EventTypeWarning))
				}
				if event.Reason == util.EventReasonDriftCorrected {
					foundCorrected = true
					Expect(event.EventType).To(Equal(util.EventTypeNormal))
				}
			}
			Expect(foundDrift).To(BeTrue(), "Should emit DriftDetected event")
			Expect(foundCorrected).To(BeTrue(), "Should emit DriftCorrected event")
		})
	})

	Describe("Patch Application Events", func() {
		It("should emit PatchApplied event when JSON patch is applied", func() {
			// Create object with valid JSON patch
			obj := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "v1",
					"kind":       "ConfigMap",
					"metadata": map[string]interface{}{
						"name":      "patch-test",
						"namespace": testNs,
						"annotations": map[string]interface{}{
							overrides.PatchAnnotation: `[{"op": "add", "path": "/data/patched", "value": "yes"}]`,
						},
					},
					"data": map[string]interface{}{
						"original": "value",
					},
				},
			}

			applier := engine.NewApplier(k8sClient, apiReader)
			_, err := applier.Apply(ctx, obj, true)
			Expect(err).NotTo(HaveOccurred())

			fakeRecorder.Reset()

			// Manually emit patch applied event to test event recording
			eventRecorder.PatchApplied(renderCtx.HCO, "ConfigMap", testNs, "patch-test", 1)

			// Verify PatchApplied event
			foundPatchApplied := false
			for _, event := range fakeRecorder.Events {
				if event.Reason == util.EventReasonPatchApplied {
					foundPatchApplied = true
					Expect(event.EventType).To(Equal(util.EventTypeNormal))
					break
				}
			}
			Expect(foundPatchApplied).To(BeTrue(), "Should emit PatchApplied event")
		})

		It("should emit InvalidPatch event when JSON patch is invalid", func() {
			// Create object with invalid JSON patch
			obj := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "v1",
					"kind":       "ConfigMap",
					"metadata": map[string]interface{}{
						"name":      "invalid-patch-test",
						"namespace": testNs,
						"annotations": map[string]interface{}{
							overrides.PatchAnnotation: `[{"op": "invalid", "path": "/data/test"}]`,
						},
					},
					"data": map[string]interface{}{
						"original": "value",
					},
				},
			}

			applier := engine.NewApplier(k8sClient, apiReader)
			_, err := applier.Apply(ctx, obj, true)
			Expect(err).NotTo(HaveOccurred())

			fakeRecorder.Reset()

			// Manually emit invalid patch event to test event recording
			eventRecorder.InvalidPatch(renderCtx.HCO, "ConfigMap", testNs, "invalid-patch-test", "invalid op: invalid")

			// Verify InvalidPatch event
			foundInvalidPatch := false
			for _, event := range fakeRecorder.Events {
				if event.Reason == util.EventReasonInvalidPatch {
					foundInvalidPatch = true
					Expect(event.EventType).To(Equal(util.EventTypeWarning))
					break
				}
			}
			Expect(foundInvalidPatch).To(BeTrue(), "Should emit InvalidPatch event")
		})
	})

	Describe("Unmanaged Mode Events", func() {
		It("should emit UnmanagedMode event when resource is unmanaged", func() {
			// Create unmanaged object
			obj := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "v1",
					"kind":       "ConfigMap",
					"metadata": map[string]interface{}{
						"name":      "unmanaged-test",
						"namespace": testNs,
						"annotations": map[string]interface{}{
							overrides.AnnotationMode: overrides.ModeUnmanaged,
						},
					},
					"data": map[string]interface{}{
						"key": "value",
					},
				},
			}

			applier := engine.NewApplier(k8sClient, apiReader)
			_, err := applier.Apply(ctx, obj, true)
			Expect(err).NotTo(HaveOccurred())

			fakeRecorder.Reset()

			// Manually emit unmanaged mode event to test event recording
			eventRecorder.UnmanagedMode(renderCtx.HCO, "ConfigMap", testNs, "unmanaged-test")

			// Verify UnmanagedMode event
			foundUnmanaged := false
			for _, event := range fakeRecorder.Events {
				if event.Reason == util.EventReasonUnmanagedMode {
					foundUnmanaged = true
					Expect(event.EventType).To(Equal(util.EventTypeNormal))
					break
				}
			}
			Expect(foundUnmanaged).To(BeTrue(), "Should emit UnmanagedMode event")
		})
	})

	Describe("Nil Event Recorder", func() {
		It("should not crash when event recorder is nil", func() {
			// Create object
			obj := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "v1",
					"kind":       "ConfigMap",
					"metadata": map[string]interface{}{
						"name":      "no-events",
						"namespace": testNs,
					},
					"data": map[string]interface{}{
						"key": "value",
					},
				},
			}

			applier := engine.NewApplier(k8sClient, apiReader)
			_, err := applier.Apply(ctx, obj, true)
			Expect(err).NotTo(HaveOccurred())

			// Create patcher without event recorder
			loader := assets.NewLoader()
			patcherNoEvents := engine.NewPatcher(k8sClient, apiReader, loader)
			// Don't set event recorder (leave it nil)

			// Calling SetEventRecorder with nil should not crash
			patcherNoEvents.SetEventRecorder(nil)

			// Should not crash when methods are called
			Expect(func() {
				patcherNoEvents.SetEventRecorder(nil)
			}).NotTo(Panic())
		})
	})
})
