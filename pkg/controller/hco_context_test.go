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
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	pkgcontext "github.com/kubevirt/virt-platform-autopilot/pkg/context"
	"github.com/kubevirt/virt-platform-autopilot/pkg/util"
)

func TestHasPCIDevices(t *testing.T) {
	tests := []struct {
		name string
		node *corev1.Node
		want bool
	}{
		{
			name: "has PCI device label",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"feature.node.kubernetes.io/pci-present": "true",
					},
				},
			},
			want: true,
		},
		{
			name: "has GPU device in capacity",
			node: &corev1.Node{
				Status: corev1.NodeStatus{
					Capacity: corev1.ResourceList{
						"nvidia.com/gpu": resource.MustParse("1"),
					},
				},
			},
			want: true,
		},
		{
			name: "has custom device plugin",
			node: &corev1.Node{
				Status: corev1.NodeStatus{
					Capacity: corev1.ResourceList{
						"intel.com/qat": resource.MustParse("2"),
					},
				},
			},
			want: true,
		},
		{
			name: "only standard resources",
			node: &corev1.Node{
				Status: corev1.NodeStatus{
					Capacity: corev1.ResourceList{
						"cpu":               resource.MustParse("4"),
						"memory":            resource.MustParse("8Gi"),
						"pods":              resource.MustParse("110"),
						"ephemeral-storage": resource.MustParse("100Gi"),
						"hugepages-1Gi":     resource.MustParse("0"),
						"hugepages-2Mi":     resource.MustParse("0"),
					},
				},
			},
			want: false,
		},
		{
			name: "no labels or capacity",
			node: &corev1.Node{},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hasPCIDevices(tt.node)
			if got != tt.want {
				t.Errorf("hasPCIDevices() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHasNUMATopology(t *testing.T) {
	tests := []struct {
		name string
		node *corev1.Node
		want bool
	}{
		{
			name: "has CPU multithreading label",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"feature.node.kubernetes.io/cpu-hardware_multithreading": "true",
					},
				},
			},
			want: true,
		},
		{
			name: "has topology manager annotation",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"kubevirt.io/topology-manager-policy": "single-numa-node",
					},
				},
			},
			want: true,
		},
		{
			name: "empty topology manager annotation",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"kubevirt.io/topology-manager-policy": "",
					},
				},
			},
			want: false,
		},
		{
			name: "no NUMA indicators",
			node: &corev1.Node{},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hasNUMATopology(tt.node)
			if got != tt.want {
				t.Errorf("hasNUMATopology() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHasVFIOCapability(t *testing.T) {
	tests := []struct {
		name string
		node *corev1.Node
		want bool
	}{
		{
			name: "IOMMU enabled",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"feature.node.kubernetes.io/iommu-enabled": "true",
					},
				},
			},
			want: true,
		},
		{
			name: "IOMMU explicitly disabled",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"feature.node.kubernetes.io/iommu-enabled": "false",
					},
				},
			},
			want: false,
		},
		{
			name: "no IOMMU label",
			node: &corev1.Node{},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hasVFIOCapability(tt.node)
			if got != tt.want {
				t.Errorf("hasVFIOCapability() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHasUSBDevices(t *testing.T) {
	tests := []struct {
		name string
		node *corev1.Node
		want bool
	}{
		{
			name: "has USB present label",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"feature.node.kubernetes.io/usb-present": "true",
					},
				},
			},
			want: true,
		},
		{
			name: "no USB label",
			node: &corev1.Node{},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hasUSBDevices(tt.node)
			if got != tt.want {
				t.Errorf("hasUSBDevices() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHasGPU(t *testing.T) {
	tests := []struct {
		name string
		node *corev1.Node
		want bool
	}{
		{
			name: "has NVIDIA GPU",
			node: &corev1.Node{
				Status: corev1.NodeStatus{
					Capacity: corev1.ResourceList{
						"nvidia.com/gpu": resource.MustParse("2"),
					},
				},
			},
			want: true,
		},
		{
			name: "has AMD GPU",
			node: &corev1.Node{
				Status: corev1.NodeStatus{
					Capacity: corev1.ResourceList{
						"amd.com/gpu": resource.MustParse("1"),
					},
				},
			},
			want: true,
		},
		{
			name: "has Intel GPU",
			node: &corev1.Node{
				Status: corev1.NodeStatus{
					Capacity: corev1.ResourceList{
						"gpu.intel.com/i915": resource.MustParse("1"),
					},
				},
			},
			want: true,
		},
		{
			name: "has multiple GPU types",
			node: &corev1.Node{
				Status: corev1.NodeStatus{
					Capacity: corev1.ResourceList{
						"nvidia.com/gpu":     resource.MustParse("2"),
						"gpu.intel.com/i915": resource.MustParse("1"),
					},
				},
			},
			want: true,
		},
		{
			name: "no GPU",
			node: &corev1.Node{
				Status: corev1.NodeStatus{
					Capacity: corev1.ResourceList{
						"cpu":    resource.MustParse("4"),
						"memory": resource.MustParse("8Gi"),
					},
				},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hasGPU(tt.node)
			if got != tt.want {
				t.Errorf("hasGPU() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHasMasterRole(t *testing.T) {
	tests := []struct {
		name string
		node *corev1.Node
		want bool
	}{
		{
			name: "has master label",
			node: &corev1.Node{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{
				"node-role.kubernetes.io/master": "",
			}}},
			want: true,
		},
		{
			name: "has control-plane label",
			node: &corev1.Node{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{
				"node-role.kubernetes.io/control-plane": "",
			}}},
			want: true,
		},
		{
			name: "worker only",
			node: &corev1.Node{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{
				"node-role.kubernetes.io/worker": "",
			}}},
			want: false,
		},
		{name: "no labels", node: &corev1.Node{}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasMasterRole(tt.node); got != tt.want {
				t.Errorf("hasMasterRole() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHasWorkerRole(t *testing.T) {
	tests := []struct {
		name string
		node *corev1.Node
		want bool
	}{
		{
			name: "has worker label",
			node: &corev1.Node{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{
				"node-role.kubernetes.io/worker": "",
			}}},
			want: true,
		},
		{
			name: "master only",
			node: &corev1.Node{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{
				"node-role.kubernetes.io/master": "",
			}}},
			want: false,
		},
		{name: "no labels", node: &corev1.Node{}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasWorkerRole(tt.node); got != tt.want {
				t.Errorf("hasWorkerRole() = %v, want %v", got, tt.want)
			}
		})
	}
}

// node builders for topology tests

func masterWorkerNode(name string) corev1.Node {
	return corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name, Labels: map[string]string{
		"node-role.kubernetes.io/master": "",
		"node-role.kubernetes.io/worker": "",
	}}}
}

func masterOnlyNode(name string) corev1.Node {
	return corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name, Labels: map[string]string{
		"node-role.kubernetes.io/master": "",
	}}}
}

func workerOnlyNode(name string) corev1.Node {
	return corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name, Labels: map[string]string{
		"node-role.kubernetes.io/worker": "",
	}}}
}

func infraCR(cpTopology, platformType string) *unstructured.Unstructured {
	status := map[string]interface{}{"controlPlaneTopology": cpTopology}
	if platformType != "" {
		status["platformStatus"] = map[string]interface{}{"type": platformType}
	}
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "config.openshift.io/v1",
		"kind":       "Infrastructure",
		"metadata":   map[string]interface{}{"name": "cluster"},
		"status":     status,
	}}
}

func fakeBuilderWith(objects ...client.Object) *RenderContextBuilder {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	return NewRenderContextBuilder(
		fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build(),
	)
}

func TestDetectTopology_CompactCluster(t *testing.T) {
	nodes := []corev1.Node{
		masterWorkerNode("node-0"),
		masterWorkerNode("node-1"),
		masterWorkerNode("node-2"),
	}

	topo, err := fakeBuilderWith().detectTopology(context.Background(), nodes)
	if err != nil {
		t.Fatalf("detectTopology() error = %v", err)
	}
	if !topo.IsCompact {
		t.Error("expected IsCompact=true for 3 master+worker nodes")
	}
	if topo.IsHCP {
		t.Error("expected IsHCP=false")
	}
	if topo.MasterCount != 3 {
		t.Errorf("MasterCount = %d, want 3", topo.MasterCount)
	}
	if topo.WorkerCount != 0 {
		t.Errorf("WorkerCount = %d, want 0", topo.WorkerCount)
	}
	if topo.TotalNodeCount != 3 {
		t.Errorf("TotalNodeCount = %d, want 3", topo.TotalNodeCount)
	}
}

func TestDetectTopology_RegularCluster(t *testing.T) {
	nodes := []corev1.Node{
		masterOnlyNode("master-0"),
		masterOnlyNode("master-1"),
		masterOnlyNode("master-2"),
		workerOnlyNode("worker-0"),
		workerOnlyNode("worker-1"),
	}

	topo, err := fakeBuilderWith().detectTopology(context.Background(), nodes)
	if err != nil {
		t.Fatalf("detectTopology() error = %v", err)
	}
	if topo.IsCompact {
		t.Error("expected IsCompact=false for dedicated masters/workers")
	}
	if topo.IsHCP {
		t.Error("expected IsHCP=false")
	}
	if topo.MasterCount != 3 {
		t.Errorf("MasterCount = %d, want 3", topo.MasterCount)
	}
	if topo.WorkerCount != 2 {
		t.Errorf("WorkerCount = %d, want 2", topo.WorkerCount)
	}
}

func TestDetectTopology_HCP(t *testing.T) {
	// HCP clusters expose only worker nodes (control plane is external)
	nodes := []corev1.Node{workerOnlyNode("worker-0"), workerOnlyNode("worker-1")}

	topo, err := fakeBuilderWith(infraCR("External", "")).detectTopology(context.Background(), nodes)
	if err != nil {
		t.Fatalf("detectTopology() error = %v", err)
	}
	if !topo.IsHCP {
		t.Error("expected IsHCP=true for controlPlaneTopology=External")
	}
	if topo.IsCompact {
		t.Error("expected IsCompact=false for HCP")
	}
	if topo.ControlPlaneTopology != "External" {
		t.Errorf("ControlPlaneTopology = %q, want External", topo.ControlPlaneTopology)
	}
	if topo.MasterCount != 0 {
		t.Errorf("MasterCount = %d, want 0", topo.MasterCount)
	}
	if topo.WorkerCount != 2 {
		t.Errorf("WorkerCount = %d, want 2", topo.WorkerCount)
	}
}

func TestDetectTopology_HighlyAvailable(t *testing.T) {
	nodes := []corev1.Node{masterOnlyNode("master-0"), workerOnlyNode("worker-0")}

	topo, err := fakeBuilderWith(infraCR("HighlyAvailable", "")).detectTopology(context.Background(), nodes)
	if err != nil {
		t.Fatalf("detectTopology() error = %v", err)
	}
	if topo.IsHCP {
		t.Error("expected IsHCP=false for HighlyAvailable")
	}
	if topo.ControlPlaneTopology != "HighlyAvailable" {
		t.Errorf("ControlPlaneTopology = %q, want HighlyAvailable", topo.ControlPlaneTopology)
	}
}

func TestDetectTopology_NoInfraCR(t *testing.T) {
	nodes := []corev1.Node{workerOnlyNode("worker-0")}

	topo, err := fakeBuilderWith().detectTopology(context.Background(), nodes)
	if err != nil {
		t.Fatalf("detectTopology() error = %v", err)
	}
	if topo.IsHCP {
		t.Error("expected IsHCP=false when Infrastructure CR absent")
	}
	if topo.ControlPlaneTopology != "" {
		t.Errorf("ControlPlaneTopology = %q, want empty", topo.ControlPlaneTopology)
	}
}

func TestDetectTopology_CloudProviders(t *testing.T) {
	tests := []struct {
		platformType string
		checkFn      func(*pkgcontext.TopologyContext) bool
	}{
		{"AWS", func(t *pkgcontext.TopologyContext) bool { return t.IsAWS }},
		{"Azure", func(t *pkgcontext.TopologyContext) bool { return t.IsAzure }},
		{"GCP", func(t *pkgcontext.TopologyContext) bool { return t.IsGCP }},
		{"BareMetal", func(t *pkgcontext.TopologyContext) bool { return t.IsBareMetal }},
		{"VSphere", func(t *pkgcontext.TopologyContext) bool { return t.IsVSphere }},
		{"OpenStack", func(t *pkgcontext.TopologyContext) bool { return t.IsOpenStack }},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.platformType, func(t *testing.T) {
			topo, err := fakeBuilderWith(infraCR("HighlyAvailable", tt.platformType)).
				detectTopology(context.Background(), nil)
			if err != nil {
				t.Fatalf("detectTopology() error = %v", err)
			}
			if topo.CloudProvider != tt.platformType {
				t.Errorf("CloudProvider = %q, want %q", topo.CloudProvider, tt.platformType)
			}
			if !tt.checkFn(topo) {
				t.Errorf("Is%s = false, want true", tt.platformType)
			}
			allFlags := []bool{topo.IsAWS, topo.IsAzure, topo.IsGCP, topo.IsBareMetal, topo.IsVSphere, topo.IsOpenStack}
			trueCount := 0
			for _, v := range allFlags {
				if v {
					trueCount++
				}
			}
			if trueCount != 1 {
				t.Errorf("expected exactly one cloud provider boolean true, got %d", trueCount)
			}
		})
	}
}

func TestNewRenderContextBuilder(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	t.Run("creates builder successfully", func(t *testing.T) {
		builder := NewRenderContextBuilder(fakeClient)

		if builder == nil {
			t.Fatal("NewRenderContextBuilder() returned nil")
		}

		if builder.client != fakeClient {
			t.Error("NewRenderContextBuilder() did not set client correctly")
		}
	})
}

func TestRenderContextBuilder_SetEventRecorder(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	builder := NewRenderContextBuilder(fakeClient)

	t.Run("sets event recorder", func(t *testing.T) {
		recorder := &util.EventRecorder{}
		builder.SetEventRecorder(recorder)

		if builder.eventRecorder != recorder {
			t.Error("SetEventRecorder() did not set event recorder")
		}
	})
}

func TestRenderContextBuilder_Build(t *testing.T) {
	ctx := context.Background()

	t.Run("builds context successfully with hardware detection", func(t *testing.T) {
		scheme := runtime.NewScheme()
		_ = corev1.AddToScheme(scheme)

		node := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-node",
				Labels: map[string]string{
					"feature.node.kubernetes.io/pci-present": "true",
				},
			},
			Status: corev1.NodeStatus{
				Capacity: corev1.ResourceList{
					"nvidia.com/gpu": resource.MustParse("1"),
				},
			},
		}

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(node).
			Build()

		builder := NewRenderContextBuilder(fakeClient)

		hco := &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "hco.kubevirt.io/v1beta1",
				"kind":       "HyperConverged",
				"metadata": map[string]interface{}{
					"name":      "test-hco",
					"namespace": "test-namespace",
				},
			},
		}

		renderCtx, err := builder.Build(ctx, hco)

		if err != nil {
			t.Fatalf("Build() error = %v", err)
		}

		if renderCtx == nil {
			t.Fatal("Build() returned nil context")
		}

		if renderCtx.HCO != hco {
			t.Error("Build() did not set HCO correctly")
		}

		if renderCtx.Hardware == nil {
			t.Fatal("Build() did not set hardware context")
		}

		if !renderCtx.Hardware.GPUPresent {
			t.Error("Build() did not detect GPU hardware")
		}

		if !renderCtx.Hardware.PCIDevicesPresent {
			t.Error("Build() did not detect PCI devices")
		}

		if renderCtx.Topology == nil {
			t.Error("Build() did not set topology context")
		}
	})

	t.Run("returns error when HCO is nil", func(t *testing.T) {
		scheme := runtime.NewScheme()
		_ = corev1.AddToScheme(scheme)
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

		builder := NewRenderContextBuilder(fakeClient)

		_, err := builder.Build(ctx, nil)

		if err == nil {
			t.Error("Build() should return error when HCO is nil")
		}
	})
}
