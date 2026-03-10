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

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const (
	// HCOGroup is the API group for HyperConverged
	HCOGroup = "hco.kubevirt.io"

	// HCOVersion is the API version for HyperConverged
	HCOVersion = "v1beta1"

	// HCOKind is the kind for HyperConverged
	HCOKind = "HyperConverged"

	// HCOName is the expected name of the HCO instance
	HCOName = "kubevirt-hyperconverged"

	// DefaultHCONamespace is the default namespace for HCO
	DefaultHCONamespace = "openshift-cnv"
)

var (
	// HCOGVK is the GroupVersionKind for HyperConverged
	HCOGVK = schema.GroupVersionKind{
		Group:   HCOGroup,
		Version: HCOVersion,
		Kind:    HCOKind,
	}
)

// RenderContext contains all data needed for rendering asset templates
type RenderContext struct {
	HCO      *unstructured.Unstructured // Full HCO object, templates access directly
	Hardware *HardwareContext           // Cluster-discovered hardware info
	Topology *TopologyContext           // Cluster topology info (HCP, compact, node counts)
}

// HardwareContext contains cluster hardware detection results
type HardwareContext struct {
	PCIDevicesPresent bool // For PCI passthrough
	NUMANodesPresent  bool // For NUMA topology
	VFIOCapable       bool // For VFIO device assignment
	USBDevicesPresent bool // For USB passthrough
	GPUPresent        bool // For GPU operator
}

// TopologyContext contains cluster topology detection results.
// Available in templates as .Topology.
type TopologyContext struct {
	// IsHCP is true when the cluster uses a Hosted Control Plane (HyperShift).
	// Detected via Infrastructure CR status.controlPlaneTopology == "External".
	IsHCP bool

	// IsCompact is true when all visible master nodes also carry the worker role,
	// meaning control-plane nodes run regular workloads (typical 3-node clusters).
	IsCompact bool

	// ControlPlaneTopology is the raw value from Infrastructure CR
	// status.controlPlaneTopology: "HighlyAvailable", "SingleReplica", or "External".
	// Empty string when the Infrastructure CR is unavailable (non-OpenShift).
	ControlPlaneTopology string

	// CloudProvider is the raw platform type from Infrastructure CR
	// status.platformStatus.type.  Known values: "AWS", "Azure", "GCP",
	// "BareMetal", "VSphere", "OpenStack", "IBMCloud", "Nutanix", "PowerVS",
	// "External", "None".  Empty on non-OpenShift clusters.
	CloudProvider string

	// Convenience booleans derived from CloudProvider.
	IsAWS       bool
	IsAzure     bool
	IsGCP       bool
	IsBareMetal bool
	IsVSphere   bool
	IsOpenStack bool

	// MasterCount is the number of nodes with the master or control-plane role label.
	MasterCount int

	// WorkerCount is the number of nodes carrying the worker role but NOT the master
	// role (dedicated workers). Zero in compact clusters.
	WorkerCount int

	// TotalNodeCount is the total number of nodes visible to the operator.
	TotalNodeCount int
}

// AsMap converts TopologyContext to a flat map for condition evaluation.
func (t *TopologyContext) AsMap() map[string]interface{} {
	return map[string]interface{}{
		"isHCP":                t.IsHCP,
		"isCompact":            t.IsCompact,
		"controlPlaneTopology": t.ControlPlaneTopology,
		"cloudProvider":        t.CloudProvider,
		"isAWS":                t.IsAWS,
		"isAzure":              t.IsAzure,
		"isGCP":                t.IsGCP,
		"isBareMetal":          t.IsBareMetal,
		"isVSphere":            t.IsVSphere,
		"isOpenStack":          t.IsOpenStack,
		"masterCount":          t.MasterCount,
		"workerCount":          t.WorkerCount,
		"totalNodeCount":       t.TotalNodeCount,
	}
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

// NewRenderContext creates a new render context from an HCO object
func NewRenderContext(hco *unstructured.Unstructured) *RenderContext {
	return &RenderContext{
		HCO:      hco,
		Hardware: &HardwareContext{},
		Topology: &TopologyContext{},
	}
}

// NewMockHCO creates a mock HyperConverged object for testing
func NewMockHCO(name, namespace string) *unstructured.Unstructured {
	hco := &unstructured.Unstructured{}
	hco.SetGroupVersionKind(HCOGVK)
	hco.SetName(name)
	hco.SetNamespace(namespace)
	hco.SetLabels(map[string]string{
		"platform.kubevirt.io/managed-by": "virt-platform-autopilot",
	})
	return hco
}
