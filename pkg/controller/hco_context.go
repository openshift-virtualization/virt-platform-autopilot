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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	pkgcontext "github.com/kubevirt/virt-platform-autopilot/pkg/context"
	"github.com/kubevirt/virt-platform-autopilot/pkg/util"
)

const (
	// Node role labels
	nodeMasterRoleLabel       = "node-role.kubernetes.io/master"
	nodeControlPlaneRoleLabel = "node-role.kubernetes.io/control-plane"
	nodeWorkerRoleLabel       = "node-role.kubernetes.io/worker"

	// infrastructureResourceName is the singleton Infrastructure CR name on OpenShift.
	infrastructureResourceName = "cluster"

	// controlPlaneTopologyExternal is the Infrastructure CR value that indicates HCP.
	controlPlaneTopologyExternal = "External"
)

// RenderContextBuilder builds RenderContext from cluster state
type RenderContextBuilder struct {
	client        client.Client
	eventRecorder *util.EventRecorder
}

// NewRenderContextBuilder creates a new RenderContext builder
func NewRenderContextBuilder(c client.Client) *RenderContextBuilder {
	return &RenderContextBuilder{
		client: c,
	}
}

// SetEventRecorder sets the event recorder for hardware detection events
func (b *RenderContextBuilder) SetEventRecorder(recorder *util.EventRecorder) {
	b.eventRecorder = recorder
}

// Build constructs a RenderContext from the current HCO state
func (b *RenderContextBuilder) Build(ctx context.Context, hco *unstructured.Unstructured) (*pkgcontext.RenderContext, error) {
	logger := log.FromContext(ctx)

	if hco == nil {
		return nil, fmt.Errorf("HCO object is nil")
	}

	// List nodes once — used by both hardware and topology detection.
	nodeList := &corev1.NodeList{}
	if err := b.client.List(ctx, nodeList); err != nil {
		return nil, fmt.Errorf("failed to list nodes: %w", err)
	}
	nodes := nodeList.Items

	// Detect hardware capabilities from nodes.
	hardware := detectHardware(nodes)

	// Detect cluster topology from nodes and Infrastructure CR.
	topology, err := b.detectTopology(ctx, nodes)
	if err != nil {
		logger.Error(err, "Topology detection failed, using defaults",
			"hco", hco.GetName())

		if b.eventRecorder != nil {
			b.eventRecorder.HardwareDetectionFailed(hco, err.Error())
		}

		topology = &pkgcontext.TopologyContext{}
	}

	return &pkgcontext.RenderContext{
		HCO:      hco,
		Hardware: hardware,
		Topology: topology,
	}, nil
}

// detectHardware scans the provided node list for hardware capabilities.
func detectHardware(nodes []corev1.Node) *pkgcontext.HardwareContext {
	hardware := &pkgcontext.HardwareContext{}

	for i := range nodes {
		node := &nodes[i]

		if hasPCIDevices(node) {
			hardware.PCIDevicesPresent = true
		}
		if hasNUMATopology(node) {
			hardware.NUMANodesPresent = true
		}
		if hasVFIOCapability(node) {
			hardware.VFIOCapable = true
		}
		if hasUSBDevices(node) {
			hardware.USBDevicesPresent = true
		}
		if hasGPU(node) {
			hardware.GPUPresent = true
		}
	}

	return hardware
}

// detectTopology derives cluster topology from node roles and the OpenShift
// Infrastructure CR.  All errors are non-fatal: the caller falls back to an
// empty TopologyContext so that reconciliation is never blocked.
func (b *RenderContextBuilder) detectTopology(ctx context.Context, nodes []corev1.Node) (*pkgcontext.TopologyContext, error) {
	topology := &pkgcontext.TopologyContext{
		TotalNodeCount: len(nodes),
	}

	// Count master and dedicated-worker nodes.
	masterCount := 0
	mastersWithWorkerRole := 0
	dedicatedWorkerCount := 0

	for i := range nodes {
		node := &nodes[i]
		isMaster := hasMasterRole(node)
		isWorker := hasWorkerRole(node)

		switch {
		case isMaster && isWorker:
			masterCount++
			mastersWithWorkerRole++
		case isMaster:
			masterCount++
		case isWorker:
			dedicatedWorkerCount++
		}
	}

	topology.MasterCount = masterCount
	topology.WorkerCount = dedicatedWorkerCount

	// Compact cluster: every visible master also carries the worker role and
	// there are no dedicated worker nodes.
	if masterCount > 0 && masterCount == mastersWithWorkerRole && dedicatedWorkerCount == 0 {
		topology.IsCompact = true
	}

	// Fetch the OpenShift Infrastructure CR (singleton, cluster-scoped).
	// Gracefully skip on non-OpenShift environments where the CR won't exist.
	infra := &unstructured.Unstructured{}
	infra.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "config.openshift.io",
		Version: "v1",
		Kind:    "Infrastructure",
	})

	err := b.client.Get(ctx, types.NamespacedName{Name: infrastructureResourceName}, infra)
	switch {
	case err == nil:
		cpTopology, _, _ := unstructured.NestedString(infra.Object, "status", "controlPlaneTopology")
		topology.ControlPlaneTopology = cpTopology
		topology.IsHCP = cpTopology == controlPlaneTopologyExternal

		provider, _, _ := unstructured.NestedString(infra.Object, "status", "platformStatus", "type")
		topology.CloudProvider = provider
		topology.IsAWS = provider == "AWS"
		topology.IsAzure = provider == "Azure"
		topology.IsGCP = provider == "GCP"
		topology.IsBareMetal = provider == "BareMetal"
		topology.IsVSphere = provider == "VSphere"
		topology.IsOpenStack = provider == "OpenStack"
	case apierrors.IsNotFound(err):
		// Non-OpenShift cluster — topology fields remain at zero values.
	default:
		return topology, fmt.Errorf("failed to fetch Infrastructure CR: %w", err)
	}

	return topology, nil
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

// hasMasterRole returns true when the node carries the master or control-plane role label.
func hasMasterRole(node *corev1.Node) bool {
	_, master := node.Labels[nodeMasterRoleLabel]
	_, controlPlane := node.Labels[nodeControlPlaneRoleLabel]
	return master || controlPlane
}

// hasWorkerRole returns true when the node carries the worker role label.
func hasWorkerRole(node *corev1.Node) bool {
	_, ok := node.Labels[nodeWorkerRoleLabel]
	return ok
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
