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

package e2e

import (
	"encoding/json"
	"strconv"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/kubevirt/virt-platform-autopilot/pkg/status"
)

var _ = Describe("Console inspection ConfigMaps", func() {
	It("publishes owned summary and golden ConfigMaps with coherent content", func() {
		ensureHCOExists()
		patchAutopilotAndWait(autopilotEnabled)

		hco := hcoRef()
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: hcoName, Namespace: operatorNamespace}, hco)).To(Succeed())

		Eventually(func(g Gomega) {
			summary := &corev1.ConfigMap{}
			golden := &corev1.ConfigMap{}
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: operatorNamespace, Name: status.SummaryConfigMapName}, summary)).To(Succeed())
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: operatorNamespace, Name: status.GoldenConfigMapName}, golden)).To(Succeed())

			controller := true
			owner := metav1.OwnerReference{APIVersion: hco.GetAPIVersion(), Kind: hco.GetKind(), Name: hco.GetName(), UID: hco.GetUID(), Controller: &controller}
			for _, configMap := range []*corev1.ConfigMap{summary, golden} {
				g.Expect(configMap.Labels).To(HaveKeyWithValue(status.InspectionLabel, "true"))
				g.Expect(configMap.Labels).To(HaveKeyWithValue(status.ManagedByLabel, status.ManagedByValue))
				g.Expect(configMap.OwnerReferences).To(ContainElement(owner))
			}

			var snapshot status.Response
			g.Expect(json.Unmarshal([]byte(summary.Data["snapshot.json"]), &snapshot)).To(Succeed())
			g.Expect(snapshot.APIVersion).To(Equal("autopilot.kubevirt.io/status/v1beta1"))
			g.Expect(snapshot.State).To(Equal("ready"))
			g.Expect(snapshot.Ready).To(BeTrue())
			g.Expect(snapshot.Assets).NotTo(BeEmpty())

			var index status.GoldenIndex
			g.Expect(json.Unmarshal([]byte(summary.Data["index.json"]), &index)).To(Succeed())
			g.Expect(index.Generation).To(Equal(snapshot.Generation))
			g.Expect(golden.Data).To(HaveKeyWithValue(status.GoldenGenerationKey, strconv.FormatUint(snapshot.Generation, 10)))
		}, timeout, interval).Should(Succeed(), "inspection ConfigMaps should be published with current owner and generation")
	})
})
