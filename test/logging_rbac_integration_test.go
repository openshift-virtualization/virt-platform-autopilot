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
	"os"
	"slices"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/yaml"

	"github.com/kubevirt/virt-platform-autopilot/pkg/assets"
	"github.com/kubevirt/virt-platform-autopilot/pkg/engine"
)

var _ = Describe("Logging collector RBAC admission", func() {
	It("allows the operator service account to apply collector bindings without granting unrelated roles", func() {
		namespace := "logging-rbac-" + randString()
		testNamespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
		Expect(k8sClient.Create(ctx, testNamespace)).To(Succeed())
		DeferCleanup(func() { Expect(k8sClient.Delete(ctx, testNamespace)).To(Succeed()) })
		account := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: "virt-platform-autopilot", Namespace: namespace}}
		Expect(k8sClient.Create(ctx, account)).To(Succeed())

		By("installing the generated operator permissions without bind to reproduce the regression")
		manifest, err := os.ReadFile("../config/rbac/role.yaml")
		Expect(err).NotTo(HaveOccurred())
		operatorRole := &rbacv1.ClusterRole{}
		Expect(yaml.Unmarshal(manifest, operatorRole)).To(Succeed())
		operatorRole.Name += "-" + namespace
		fullRules := slices.Clone(operatorRole.Rules)
		operatorRole.Rules = slices.DeleteFunc(slices.Clone(fullRules), func(rule rbacv1.PolicyRule) bool { return slices.Contains(rule.Verbs, "bind") })
		Expect(k8sClient.Create(ctx, operatorRole)).To(Succeed())
		DeferCleanup(func() { Expect(k8sClient.Delete(ctx, operatorRole)).To(Succeed()) })
		operatorBinding := &rbacv1.ClusterRoleBinding{
			ObjectMeta: metav1.ObjectMeta{Name: operatorRole.Name},
			RoleRef:    rbacv1.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "ClusterRole", Name: operatorRole.Name},
			Subjects:   []rbacv1.Subject{{Kind: "ServiceAccount", Name: account.Name, Namespace: namespace}},
		}
		Expect(k8sClient.Create(ctx, operatorBinding)).To(Succeed())
		DeferCleanup(func() { Expect(k8sClient.Delete(ctx, operatorBinding)).To(Succeed()) })

		user, err := testEnv.AddUser(envtest.User{Name: "system:serviceaccount:" + namespace + ":" + account.Name, Groups: []string{"system:serviceaccounts", "system:serviceaccounts:" + namespace, "system:authenticated"}}, cfg)
		Expect(err).NotTo(HaveOccurred())
		operatorClient, err := client.New(user.Config(), client.Options{Scheme: k8sClient.Scheme()})
		Expect(err).NotTo(HaveOccurred())
		loader := assets.NewLoader()
		paths := []string{"application", "infrastructure", "audit", "writer"}
		for _, suffix := range paths {
			binding, loadErr := loader.LoadAssetAsUnstructured("active/logging/collector-crb-" + suffix + ".yaml")
			Expect(loadErr).NotTo(HaveOccurred())
			role := &rbacv1.ClusterRole{
				ObjectMeta: metav1.ObjectMeta{Name: binding.Object["roleRef"].(map[string]any)["name"].(string)},
				Rules:      []rbacv1.PolicyRule{{APIGroups: []string{"observability.openshift.io"}, Resources: []string{"logs"}, ResourceNames: []string{suffix}, Verbs: []string{"collect"}}},
			}
			if suffix == "writer" {
				role.Rules = []rbacv1.PolicyRule{{APIGroups: []string{"loki.grafana.com"}, Resources: []string{"application", "infrastructure", "audit"}, ResourceNames: []string{"logs"}, Verbs: []string{"create"}}}
			}
			Expect(k8sClient.Create(ctx, role)).To(Succeed())
			DeferCleanup(func() { Expect(k8sClient.Delete(ctx, role)).To(Succeed()) })
			Eventually(func() bool {
				createErr := operatorClient.Create(ctx, binding.DeepCopy())
				return apierrors.IsForbidden(createErr) && strings.Contains(createErr.Error(), "attempting to grant RBAC permissions")
			}).Should(BeTrue(), "binding creation without bind must be rejected by privilege escalation admission")
		}

		By("restoring generated bind permissions and applying the actual managed assets")
		operatorRole.Rules = fullRules
		Expect(k8sClient.Update(ctx, operatorRole)).To(Succeed())
		applier := engine.NewApplier(operatorClient, operatorClient)
		for _, suffix := range paths {
			binding, loadErr := loader.LoadAssetAsUnstructured("active/logging/collector-crb-" + suffix + ".yaml")
			Expect(loadErr).NotTo(HaveOccurred())
			Eventually(func() error { _, applyErr := applier.Apply(ctx, binding, true); return applyErr }).Should(Succeed())
			DeferCleanup(func() { Expect(k8sClient.Delete(ctx, binding)).To(Succeed()) })
			installed := &rbacv1.ClusterRoleBinding{}
			Expect(k8sClient.Get(ctx, client.ObjectKey{Name: binding.GetName()}, installed)).To(Succeed())
			Expect(installed.Subjects).To(ContainElement(rbacv1.Subject{Kind: "ServiceAccount", Name: "collector", Namespace: "openshift-logging"}))
		}

		By("applying metrics-exporter bindings using owned permissions without extra bind grants")
		for _, prefix := range []string{"", "scc-"} {
			role, loadErr := loader.LoadAssetAsUnstructured("active/kubevirt-metrics-exporter/" + prefix + "clusterrole.yaml")
			Expect(loadErr).NotTo(HaveOccurred())
			Expect(k8sClient.Create(ctx, role)).To(Succeed())
			DeferCleanup(func() { Expect(k8sClient.Delete(ctx, role)).To(Succeed()) })
			binding, loadErr := loader.LoadAssetAsUnstructured("active/kubevirt-metrics-exporter/" + prefix + "clusterrolebinding.yaml")
			Expect(loadErr).NotTo(HaveOccurred())
			Eventually(func() error { _, applyErr := applier.Apply(ctx, binding, true); return applyErr }).Should(Succeed())
			DeferCleanup(func() { Expect(k8sClient.Delete(ctx, binding)).To(Succeed()) })
		}

		By("verifying bind is limited to the explicit collector allowlist")
		unrelatedRole := &rbacv1.ClusterRole{ObjectMeta: metav1.ObjectMeta{Name: "unrelated-" + namespace}, Rules: []rbacv1.PolicyRule{{APIGroups: []string{""}, Resources: []string{"secrets"}, Verbs: []string{"get"}}}}
		Expect(k8sClient.Create(ctx, unrelatedRole)).To(Succeed())
		DeferCleanup(func() { Expect(k8sClient.Delete(ctx, unrelatedRole)).To(Succeed()) })
		unrelatedBinding := &rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: unrelatedRole.Name}, RoleRef: rbacv1.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "ClusterRole", Name: unrelatedRole.Name}, Subjects: operatorBinding.Subjects}
		Expect(apierrors.IsForbidden(operatorClient.Create(ctx, unrelatedBinding))).To(BeTrue())
		privilegedBinding := unrelatedBinding.DeepCopy()
		privilegedBinding.Name = "privileged-" + namespace
		privilegedBinding.RoleRef.Name = "cluster-admin"
		Expect(apierrors.IsForbidden(operatorClient.Create(ctx, privilegedBinding))).To(BeTrue())
	})
})
