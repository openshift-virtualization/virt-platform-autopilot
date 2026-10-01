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
	"fmt"

	storagev1 "k8s.io/api/storage/v1"
)

func (r *Renderer) defaultStorageClass() (string, error) {
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
	return "", &ConfigurationError{
		Code:    NoDefaultStorageClass,
		Message: "no default StorageClass found; configure a cluster default or an explicit storage class",
	}
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
