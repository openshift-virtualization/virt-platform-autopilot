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

package context

import "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

// RenderContext contains all data needed for rendering asset templates
type RenderContext struct {
	HCO      *unstructured.Unstructured // Full HCO object, templates access directly
	Hardware *HardwareContext           // Cluster-discovered hardware info
}

// HardwareContext contains cluster hardware detection results
type HardwareContext struct {
	PCIDevicesPresent bool // For PCI passthrough
	NUMANodesPresent  bool // For NUMA topology
	VFIOCapable       bool // For VFIO device assignment
	USBDevicesPresent bool // For USB passthrough
	GPUPresent        bool // For GPU operator
}

// AsMap converts HardwareContext to map for condition evaluation
func (h *HardwareContext) AsMap() map[string]bool {
	return map[string]bool{
		"pciDevicesPresent": h.PCIDevicesPresent,
		"numaNodesPresent":  h.NUMANodesPresent,
		"vfioCapable":       h.VFIOCapable,
		"usbDevicesPresent": h.USBDevicesPresent,
		"gpuPresent":        h.GPUPresent,
	}
}
