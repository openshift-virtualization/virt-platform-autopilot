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

package controller

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"

	pkgcontext "github.com/kubevirt/virt-platform-operator/pkg/context"
)

// RenderContextBuilder builds RenderContext from cluster state
type RenderContextBuilder struct {
	client client.Client
}

// NewRenderContextBuilder creates a new RenderContext builder
func NewRenderContextBuilder(c client.Client) *RenderContextBuilder {
	return &RenderContextBuilder{
		client: c,
	}
}

// Build constructs a RenderContext from the current HCO state
func (b *RenderContextBuilder) Build(ctx context.Context, hco *unstructured.Unstructured) (*pkgcontext.RenderContext, error) {
	if hco == nil {
		return nil, fmt.Errorf("HCO object is nil")
	}

	// Detect hardware capabilities
	hardware, err := b.detectHardware(ctx)
	if err != nil {
		// Log warning but don't fail - use defaults
		hardware = &pkgcontext.HardwareContext{}
	}

	return &pkgcontext.RenderContext{
		HCO:      hco,
		Hardware: hardware,
	}, nil
}

// detectHardware queries the cluster to detect hardware capabilities
func (b *RenderContextBuilder) detectHardware(ctx context.Context) (*pkgcontext.HardwareContext, error) {
	hardware := &pkgcontext.HardwareContext{}

	// List all nodes to examine hardware
	nodeList := &corev1.NodeList{}
	if err := b.client.List(ctx, nodeList); err != nil {
		return nil, fmt.Errorf("failed to list nodes: %w", err)
	}

	// Scan nodes for hardware capabilities
	for i := range nodeList.Items {
		node := &nodeList.Items[i]

		// Check for PCI devices (look for common vendor IDs in node labels/annotations)
		if hasPCIDevices(node) {
			hardware.PCIDevicesPresent = true
		}

		// Check for NUMA topology
		if hasNUMATopology(node) {
			hardware.NUMANodesPresent = true
		}

		// Check for VFIO capability (IOMMU support)
		if hasVFIOCapability(node) {
			hardware.VFIOCapable = true
		}

		// Check for USB devices
		if hasUSBDevices(node) {
			hardware.USBDevicesPresent = true
		}

		// Check for GPUs
		if hasGPU(node) {
			hardware.GPUPresent = true
		}
	}

	return hardware, nil
}

// hasPCIDevices checks if node has PCI devices suitable for passthrough
func hasPCIDevices(node *corev1.Node) bool {
	// Check for common PCI device labels/annotations
	// In real implementation, this would check for specific device labels
	// or examine node status for device plugins
	if _, exists := node.Labels["feature.node.kubernetes.io/pci-present"]; exists {
		return true
	}

	// Check capacity for device plugins (e.g., nvidia.com/gpu)
	for resource := range node.Status.Capacity {
		resourceName := string(resource)
		// Look for vendor-specific device plugins
		if resourceName != "cpu" && resourceName != "memory" && resourceName != "pods" &&
			resourceName != "ephemeral-storage" && resourceName != "hugepages-1Gi" &&
			resourceName != "hugepages-2Mi" {
			return true
		}
	}

	return false
}

// hasNUMATopology checks if node has NUMA topology
func hasNUMATopology(node *corev1.Node) bool {
	// Check for NUMA-related labels
	if _, exists := node.Labels["feature.node.kubernetes.io/cpu-hardware_multithreading"]; exists {
		return true
	}

	// Check annotations for topology manager policy
	if policy, exists := node.Annotations["kubevirt.io/topology-manager-policy"]; exists && policy != "" {
		return true
	}

	return false
}

// hasVFIOCapability checks if node supports VFIO (IOMMU enabled)
func hasVFIOCapability(node *corev1.Node) bool {
	// Check for IOMMU-related labels
	if iommu, exists := node.Labels["feature.node.kubernetes.io/iommu-enabled"]; exists && iommu == "true" {
		return true
	}

	return false
}

// hasUSBDevices checks if node has USB devices
func hasUSBDevices(node *corev1.Node) bool {
	// Check for USB device labels
	if _, exists := node.Labels["feature.node.kubernetes.io/usb-present"]; exists {
		return true
	}

	return false
}

// hasGPU checks if node has GPU devices
func hasGPU(node *corev1.Node) bool {
	// Check for NVIDIA GPUs
	if _, exists := node.Status.Capacity["nvidia.com/gpu"]; exists {
		return true
	}

	// Check for AMD GPUs
	if _, exists := node.Status.Capacity["amd.com/gpu"]; exists {
		return true
	}

	// Check for Intel GPUs
	if _, exists := node.Status.Capacity["gpu.intel.com/i915"]; exists {
		return true
	}

	return false
}
