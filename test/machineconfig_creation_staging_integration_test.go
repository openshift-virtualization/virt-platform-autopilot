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

package test

import (
	"fmt"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kubevirt/virt-platform-autopilot/pkg/assets"
	pkgcontext "github.com/kubevirt/virt-platform-autopilot/pkg/context"
	"github.com/kubevirt/virt-platform-autopilot/pkg/engine"
)

var _ = Describe("MachineConfig creation staging", func() {
	const (
		machineConfigName  = "99-openshift-machineconfig-worker-psi-karg"
		clusterVersionName = "version"
	)

	var (
		clusterVersionGVK    = schema.GroupVersionKind{Group: "config.openshift.io", Version: "v1", Kind: "ClusterVersion"}
		machineConfigGVK     = schema.GroupVersionKind{Group: "machineconfiguration.openshift.io", Version: "v1", Kind: "MachineConfig"}
		machineConfigPoolGVK = schema.GroupVersionKind{
			Group: "machineconfiguration.openshift.io", Version: "v1", Kind: "MachineConfigPool",
		}
		machineConfigExists          func() bool
		newMachineConfigPool         func(string, string) *unstructured.Unstructured
		setClusterVersionStatus      func(*unstructured.Unstructured, string, []map[string]any)
		setMachineConfigPoolUpdating func(*unstructured.Unstructured, string)
	)

	BeforeEach(func() {
		// machineConfigExists intentionally reads from the API server so this verifies
		// ReconcileAsset's actual server-side apply path rather than cache state.
		machineConfigExists = func() bool {
			machineConfig := &unstructured.Unstructured{}
			machineConfig.SetGroupVersionKind(machineConfigGVK)
			err := apiReader.Get(ctx, client.ObjectKey{Name: machineConfigName}, machineConfig)
			Expect(err == nil || apierrors.IsNotFound(err)).To(BeTrue(), fmt.Sprintf("get MachineConfig: %v", err))
			return err == nil
		}
		setClusterVersionStatus = func(clusterVersion *unstructured.Unstructured, desired string, history []map[string]any) {
			historyEntries := make([]any, 0, len(history))
			for _, entry := range history {
				historyEntries = append(historyEntries, entry)
			}
			Expect(unstructured.SetNestedField(clusterVersion.Object, desired, "status", "desired", "version")).To(Succeed())
			Expect(unstructured.SetNestedSlice(clusterVersion.Object, historyEntries, "status", "history")).To(Succeed())
		}
		setMachineConfigPoolUpdating = func(pool *unstructured.Unstructured, status string) {
			Expect(unstructured.SetNestedSlice(pool.Object, []any{map[string]any{
				"type": "Updating", "status": status,
			}}, "status", "conditions")).To(Succeed())
		}
		newMachineConfigPool = func(name, updating string) *unstructured.Unstructured {
			pool := &unstructured.Unstructured{Object: map[string]any{
				"apiVersion": "machineconfiguration.openshift.io/v1",
				"kind":       "MachineConfigPool",
				"metadata":   map[string]any{"name": name},
				"spec": map[string]any{
					"machineConfigSelector": map[string]any{
						"matchLabels": map[string]any{"machineconfiguration.openshift.io/role": "worker"},
					},
				},
			}}
			pool.SetGroupVersionKind(machineConfigPoolGVK)
			setMachineConfigPoolUpdating(pool, updating)
			return pool
		}
	})

	It("defers a new MachineConfig after a minor upgrade until its pool is updating", func() {
		Expect(InstallCRDs(ctx, k8sClient, CRDSetOpenShift)).To(Succeed())
		DeferCleanup(func() { _ = UninstallCRDs(ctx, k8sClient, CRDSetOpenShift) })
		Expect(installCRDFile(ctx, k8sClient, filepath.Join("testdata", "clusterversion-crd.yaml"))).To(Succeed())
		DeferCleanup(func() {
			crd := &apiextensionsv1.CustomResourceDefinition{}
			crd.SetName("clusterversions.config.openshift.io")
			_ = k8sClient.Delete(ctx, crd)
			_ = waitForCRDDeletion(ctx, k8sClient, crd.GetName())
		})

		namespace := &unstructured.Unstructured{}
		namespace.SetGroupVersionKind(nsGVK)
		namespace.SetName("mc-creation-staging-" + randString())
		Expect(k8sClient.Create(ctx, namespace)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, namespace) })

		hco := pkgcontext.NewMockHCO(pkgcontext.HCOName, namespace.GetName())
		Expect(k8sClient.Create(ctx, hco)).To(Succeed())

		clusterVersion := &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "config.openshift.io/v1",
			"kind":       "ClusterVersion",
			"metadata":   map[string]any{"name": clusterVersionName},
		}}
		clusterVersion.SetGroupVersionKind(clusterVersionGVK)
		Expect(k8sClient.Create(ctx, clusterVersion)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, clusterVersion) })

		setClusterVersionStatus(clusterVersion, "4.23.1", []map[string]any{
			{"state": "Completed", "version": "4.23.1"},
			{"state": "Completed", "version": "4.22.15"},
		})
		Expect(k8sClient.Status().Update(ctx, clusterVersion)).To(Succeed())
		Eventually(func(g Gomega) {
			stored := &unstructured.Unstructured{}
			stored.SetGroupVersionKind(clusterVersionGVK)
			g.Expect(apiReader.Get(ctx, client.ObjectKey{Name: clusterVersionName}, stored)).To(Succeed())
			target, found, err := unstructured.NestedString(stored.Object, "status", "desired", "version")
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(found).To(BeTrue())
			g.Expect(target).To(Equal("4.23.1"))
			history, found, err := unstructured.NestedSlice(stored.Object, "status", "history")
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(found).To(BeTrue())
			g.Expect(history).To(HaveLen(2))
		}).Should(Succeed())

		pool := newMachineConfigPool("creation-staging-worker-"+randString(), "False")
		Expect(k8sClient.Create(ctx, pool)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, pool) })
		Expect(k8sClient.Status().Update(ctx, pool)).To(Succeed())

		patcher := engine.NewPatcher(k8sClient, apiReader, assets.NewLoader())
		asset := assets.AssetMetadata{
			Name:           "machineconfig-creation-staging",
			Path:           "active/machine-config/04-psi-enable.yaml.tpl",
			Install:        assets.InstallModeAlways,
			Component:      "MachineConfig",
			TemplateParams: map[string]string{"role": "worker"},
		}
		renderCtx := pkgcontext.NewRenderContext(hco)
		machineConfig := &unstructured.Unstructured{}
		machineConfig.SetGroupVersionKind(machineConfigGVK)
		machineConfig.SetName(machineConfigName)
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, machineConfig) })

		By("reconciling while the matching pool is stable")
		applied, err := patcher.ReconcileAsset(ctx, &asset, renderCtx)
		Expect(err).NotTo(HaveOccurred())
		Expect(applied).To(BeFalse())
		Expect(machineConfigExists()).To(BeFalse(), "creation must be staged until a matching pool rolls out")

		By("starting a rollout for the matching pool")
		setMachineConfigPoolUpdating(pool, "True")
		Expect(k8sClient.Status().Update(ctx, pool)).To(Succeed())

		By("reconciling after the pool enters Updating")
		applied, err = patcher.ReconcileAsset(ctx, &asset, renderCtx)
		Expect(err).NotTo(HaveOccurred())
		Expect(applied).To(BeTrue())
		Expect(machineConfigExists()).To(BeTrue())
	})

})
