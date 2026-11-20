/*
Copyright 2026 The Virt Platform Autopilot Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kubevirt/virt-platform-autopilot/pkg/status"
)

var _ = Describe("Inspection publication integration", func() {
	It("publishes through the API reader when the ConfigMap cache excludes its namespace", func() {
		namespace := "inspection-" + randString()
		Expect(k8sClient.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}})).To(Succeed())

		restrictedCache, err := cache.New(cfg, cache.Options{
			Scheme: clientgoscheme.Scheme,
			ByObject: map[client.Object]cache.ByObject{
				&corev1.ConfigMap{}: {Namespaces: map[string]cache.Config{"kube-system": {}}},
			},
		})
		Expect(err).NotTo(HaveOccurred())
		cacheCtx, cacheCancel := context.WithCancel(ctx)
		defer cacheCancel()
		go func() { _ = restrictedCache.Start(cacheCtx) }()
		Expect(restrictedCache.WaitForCacheSync(cacheCtx)).To(BeTrue())

		cachedClient, err := client.New(cfg, client.Options{
			Scheme: clientgoscheme.Scheme,
			Cache:  &client.CacheOptions{Reader: restrictedCache},
		})
		Expect(err).NotTo(HaveOccurred())

		// The manager's cache is deliberately unable to read ConfigMaps in the
		// Autopilot namespace. Publishing must use apiReader for reads while
		// keeping the cached client as the writer.
		Expect(cachedClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: status.SummaryConfigMapName}, &corev1.ConfigMap{})).To(HaveOccurred())

		owner := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "inspection-owner", Namespace: namespace}}
		Expect(k8sClient.Create(ctx, owner)).To(Succeed())
		ownerObject := &unstructured.Unstructured{}
		ownerObject.SetAPIVersion("v1")
		ownerObject.SetKind("ConfigMap")
		ownerObject.SetNamespace(namespace)
		ownerObject.SetName(owner.Name)
		ownerObject.SetUID(owner.UID)

		store := status.NewStore()
		store.SetOwner(ownerObject)
		Expect(store.Publish(ctx, cachedClient, apiReader, namespace)).To(Succeed())

		controller := true
		for _, name := range []string{status.SummaryConfigMapName, status.GoldenConfigMapName} {
			inspection := &corev1.ConfigMap{}
			Expect(apiReader.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, inspection)).To(Succeed())
			Expect(inspection.OwnerReferences).To(ConsistOf(metav1.OwnerReference{
				APIVersion: "v1", Kind: "ConfigMap", Name: owner.Name, UID: owner.UID, Controller: &controller,
			}))
			if name == status.SummaryConfigMapName {
				Expect(inspection.Data).To(HaveKey("snapshot.json"))
				Expect(inspection.Data).To(HaveKey("index.json"))
			}
		}
	})
})
