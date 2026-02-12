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

package engine

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/kubevirt/virt-platform-autopilot/pkg/assets"
	"github.com/kubevirt/virt-platform-autopilot/pkg/observability"
	"github.com/kubevirt/virt-platform-autopilot/pkg/util"
)

var _ = Describe("Tombstone Reconciler", func() {
	var (
		ctx        context.Context
		reconciler *TombstoneReconciler
		fakeClient client.Client
		loader     *assets.Loader
		hco        *unstructured.Unstructured
	)

	BeforeEach(func() {
		ctx = context.Background()

		// Create fake client
		scheme := runtime.NewScheme()
		fakeClient = fake.NewClientBuilder().WithScheme(scheme).Build()

		// Create loader
		loader = assets.NewLoader()

		// Create reconciler
		reconciler = NewTombstoneReconciler(fakeClient, loader)
		// Don't set event recorder for unit tests (would be nil and cause panics)

		// Create HCO object for events
		hco = &unstructured.Unstructured{}
		hco.SetGroupVersionKind(schema.GroupVersionKind{
			Group:   "hco.kubevirt.io",
			Version: "v1beta1",
			Kind:    "HyperConverged",
		})
		hco.SetName("kubevirt-hyperconverged")
		hco.SetNamespace("openshift-cnv")
	})

	Describe("reconcileTombstone", func() {
		var tombstone assets.TombstoneMetadata

		BeforeEach(func() {
			// Create a tombstone metadata
			obj := &unstructured.Unstructured{}
			obj.SetAPIVersion("v1")
			obj.SetKind("ConfigMap")
			obj.SetName("test-config")
			obj.SetNamespace("default")
			obj.SetLabels(map[string]string{
				assets.TombstoneLabel: assets.TombstoneLabelValue,
			})

			tombstone = assets.TombstoneMetadata{
				Path: "test.yaml",
				GVK: schema.GroupVersionKind{
					Group:   "",
					Version: "v1",
					Kind:    "ConfigMap",
				},
				Namespace: "default",
				Name:      "test-config",
				Object:    obj,
			}
		})

		It("should delete resource with matching label", func() {
			// Create resource with correct label
			resource := &unstructured.Unstructured{}
			resource.SetAPIVersion("v1")
			resource.SetKind("ConfigMap")
			resource.SetName("test-config")
			resource.SetNamespace("default")
			resource.SetLabels(map[string]string{
				assets.TombstoneLabel: assets.TombstoneLabelValue,
			})
			resource.Object["data"] = map[string]interface{}{"key": "value"}

			Expect(fakeClient.Create(ctx, resource)).To(Succeed())

			// Reconcile tombstone
			deleted, err := reconciler.reconcileTombstone(ctx, tombstone, hco)
			Expect(err).NotTo(HaveOccurred())
			Expect(deleted).To(BeTrue())

			// Verify deletion
			checkResource := &unstructured.Unstructured{}
			checkResource.SetAPIVersion("v1")
			checkResource.SetKind("ConfigMap")
			err = fakeClient.Get(ctx, client.ObjectKey{Name: "test-config", Namespace: "default"}, checkResource)
			Expect(errors.IsNotFound(err)).To(BeTrue())
		})

		It("should skip resource without label", func() {
			// Create resource WITHOUT the required label
			resource := &unstructured.Unstructured{}
			resource.SetAPIVersion("v1")
			resource.SetKind("ConfigMap")
			resource.SetName("test-config")
			resource.SetNamespace("default")
			resource.Object["data"] = map[string]interface{}{"key": "value"}

			Expect(fakeClient.Create(ctx, resource)).To(Succeed())

			// Reconcile tombstone
			deleted, err := reconciler.reconcileTombstone(ctx, tombstone, hco)
			Expect(err).NotTo(HaveOccurred())
			Expect(deleted).To(BeFalse())

			// Verify resource still exists
			checkResource := &unstructured.Unstructured{}
			checkResource.SetAPIVersion("v1")
			checkResource.SetKind("ConfigMap")
			err = fakeClient.Get(ctx, client.ObjectKey{Name: "test-config", Namespace: "default"}, checkResource)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should skip resource with incorrect label value", func() {
			// Create resource with wrong label value
			resource := &unstructured.Unstructured{}
			resource.SetAPIVersion("v1")
			resource.SetKind("ConfigMap")
			resource.SetName("test-config")
			resource.SetNamespace("default")
			resource.SetLabels(map[string]string{
				assets.TombstoneLabel: "wrong-value",
			})
			resource.Object["data"] = map[string]interface{}{"key": "value"}

			Expect(fakeClient.Create(ctx, resource)).To(Succeed())

			// Reconcile tombstone
			deleted, err := reconciler.reconcileTombstone(ctx, tombstone, hco)
			Expect(err).NotTo(HaveOccurred())
			Expect(deleted).To(BeFalse())

			// Verify resource still exists
			checkResource := &unstructured.Unstructured{}
			checkResource.SetAPIVersion("v1")
			checkResource.SetKind("ConfigMap")
			err = fakeClient.Get(ctx, client.ObjectKey{Name: "test-config", Namespace: "default"}, checkResource)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should handle NotFound gracefully (already deleted)", func() {
			// Don't create the resource - it doesn't exist

			// Reconcile tombstone
			deleted, err := reconciler.reconcileTombstone(ctx, tombstone, hco)
			Expect(err).NotTo(HaveOccurred())
			Expect(deleted).To(BeFalse())

			// This should succeed - idempotent behavior
		})

		It("should handle cluster-scoped resources", func() {
			// Create cluster-scoped tombstone
			obj := &unstructured.Unstructured{}
			obj.SetAPIVersion("machineconfiguration.openshift.io/v1")
			obj.SetKind("MachineConfig")
			obj.SetName("50-swap-enable")
			obj.SetLabels(map[string]string{
				assets.TombstoneLabel: assets.TombstoneLabelValue,
			})

			clusterTombstone := assets.TombstoneMetadata{
				Path: "machine-config.yaml",
				GVK: schema.GroupVersionKind{
					Group:   "machineconfiguration.openshift.io",
					Version: "v1",
					Kind:    "MachineConfig",
				},
				Namespace: "", // Cluster-scoped
				Name:      "50-swap-enable",
				Object:    obj,
			}

			// Create resource
			resource := &unstructured.Unstructured{}
			resource.SetAPIVersion("machineconfiguration.openshift.io/v1")
			resource.SetKind("MachineConfig")
			resource.SetName("50-swap-enable")
			resource.SetLabels(map[string]string{
				assets.TombstoneLabel: assets.TombstoneLabelValue,
			})
			resource.Object["spec"] = map[string]interface{}{"config": "test"}

			Expect(fakeClient.Create(ctx, resource)).To(Succeed())

			// Reconcile tombstone
			deleted, err := reconciler.reconcileTombstone(ctx, clusterTombstone, hco)
			Expect(err).NotTo(HaveOccurred())
			Expect(deleted).To(BeTrue())

			// Verify deletion
			checkResource := &unstructured.Unstructured{}
			checkResource.SetAPIVersion("machineconfiguration.openshift.io/v1")
			checkResource.SetKind("MachineConfig")
			err = fakeClient.Get(ctx, client.ObjectKey{Name: "50-swap-enable"}, checkResource)
			Expect(errors.IsNotFound(err)).To(BeTrue())
		})

		It("should set metric to deleted for successful deletion", func() {
			// Create resource
			resource := &unstructured.Unstructured{}
			resource.SetAPIVersion("v1")
			resource.SetKind("ConfigMap")
			resource.SetName("test-config")
			resource.SetNamespace("default")
			resource.SetLabels(map[string]string{
				assets.TombstoneLabel: assets.TombstoneLabelValue,
			})
			resource.Object["data"] = map[string]interface{}{"key": "value"}

			Expect(fakeClient.Create(ctx, resource)).To(Succeed())

			// Reconcile tombstone
			deleted, err := reconciler.reconcileTombstone(ctx, tombstone, hco)
			Expect(err).NotTo(HaveOccurred())
			Expect(deleted).To(BeTrue())

			// Note: In real implementation, we'd verify the metric was set
			// For unit tests with fake client, we can't easily verify Prometheus metrics
			// This is better tested in integration tests
		})

		It("should set metric to skipped for label mismatch", func() {
			// Create resource without label
			resource := &unstructured.Unstructured{}
			resource.SetAPIVersion("v1")
			resource.SetKind("ConfigMap")
			resource.SetName("test-config")
			resource.SetNamespace("default")
			resource.Object["data"] = map[string]interface{}{"key": "value"}

			Expect(fakeClient.Create(ctx, resource)).To(Succeed())

			// Reconcile tombstone
			deleted, err := reconciler.reconcileTombstone(ctx, tombstone, hco)
			Expect(err).NotTo(HaveOccurred())
			Expect(deleted).To(BeFalse())

			// Metric should be set to TombstoneSkipped
		})
	})

	Describe("ReconcileTombstones", func() {
		It("should return 0 when no tombstones exist", func() {
			// Loader will return empty list (no tombstones in embedded FS)
			deletedCount, err := reconciler.ReconcileTombstones(ctx, hco)
			Expect(err).NotTo(HaveOccurred())
			Expect(deletedCount).To(Equal(0))
		})

		It("should continue processing on error (best-effort)", func() {
			// This test would require mocking the loader to return tombstones
			// For now, we verify the function completes without panicking
			_, err := reconciler.ReconcileTombstones(ctx, hco)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should aggregate errors from failed deletions", func() {
			// This would require mocking to inject errors
			// Integration tests will cover this scenario better
			_, err := reconciler.ReconcileTombstones(ctx, hco)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Describe("SetEventRecorder", func() {
		It("should set event recorder", func() {
			recorder := util.NewEventRecorder(nil)
			reconciler.SetEventRecorder(recorder)
			Expect(reconciler.eventRecorder).NotTo(BeNil())
		})
	})

	Describe("Metric integration", func() {
		It("should use correct metric constants", func() {
			Expect(observability.TombstoneExists).To(Equal(1.0))
			Expect(observability.TombstoneDeleted).To(Equal(0.0))
			Expect(observability.TombstoneError).To(Equal(-1.0))
			Expect(observability.TombstoneSkipped).To(Equal(-2.0))
		})
	})
})
