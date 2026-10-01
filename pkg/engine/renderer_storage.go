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

package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"

	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func (r *Renderer) objectField(apiVersion, kind, namespace, name, fieldPath string) (string, error) {
	if r.client == nil {
		return "", nil
	}
	object := &unstructured.Unstructured{}
	object.SetAPIVersion(apiVersion)
	object.SetKind(kind)
	key := client.ObjectKey{Namespace: namespace, Name: name}
	err := r.client.Get(context.Background(), key, object)
	if apierrors.IsNotFound(err) && r.objectReader != nil {
		err = r.objectReader.Get(context.Background(), key, object)
	}
	if apierrors.IsNotFound(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read %s %s: %w", kind, key, err)
	}
	value, _, err := unstructured.NestedString(object.Object, strings.Split(fieldPath, ".")...)
	if err != nil {
		return "", fmt.Errorf("read %s %s field %s: %w", kind, key, fieldPath, err)
	}
	return value, nil
}

func (r *Renderer) storageClassExists(name string) (bool, error) {
	if r.client == nil {
		return true, nil
	}
	err := r.client.Get(context.Background(), client.ObjectKey{Name: name}, &storagev1.StorageClass{})
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read StorageClass %q: %w", name, err)
	}
	return true, nil
}

func (r *Renderer) defaultStorageClass(noDefaultMessage ...string) (string, error) {
	if r.client == nil {
		return "", nil
	}
	classes := &storagev1.StorageClassList{}
	if err := r.client.List(context.Background(), classes); err != nil {
		return "", fmt.Errorf("list StorageClasses: %w", err)
	}

	if class := defaultStorageClass(classes.Items); class != "" {
		return class, nil
	}
	if len(noDefaultMessage) > 0 {
		return "", errors.New(noDefaultMessage[0])
	}
	return "", errors.New("no default StorageClass found; configure a cluster default or an explicit storage class")
}

// Kubernetes chooses the newest default when multiple defaults exist.
func defaultStorageClass(classes []storagev1.StorageClass) string {
	var selected *storagev1.StorageClass
	for _, class := range classes {
		annotations := class.Annotations
		if annotations["storageclass.kubernetes.io/is-default-class"] != "true" && annotations["storageclass.beta.kubernetes.io/is-default-class"] != "true" {
			continue
		}
		if selected == nil || class.CreationTimestamp.After(selected.CreationTimestamp.Time) ||
			(class.CreationTimestamp.Equal(&selected.CreationTimestamp) && class.Name < selected.Name) {
			selected = &class
		}
	}
	if selected == nil {
		return ""
	}
	return selected.Name
}
