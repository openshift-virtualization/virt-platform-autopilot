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
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

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
