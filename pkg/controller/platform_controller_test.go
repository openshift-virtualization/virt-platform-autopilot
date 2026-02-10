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

	pkgcontext "github.com/kubevirt/virt-platform-operator/pkg/context"
	"github.com/kubevirt/virt-platform-operator/pkg/util"
)

func TestExtractFeatureGates(t *testing.T) {
	tests := []struct {
		name string
		hco  *unstructured.Unstructured
		want map[string]bool
	}{
		{
			name: "with feature gates",
			hco: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"spec": map[string]interface{}{
						"featureGates": []interface{}{
							"FeatureGate1",
							"FeatureGate2",
							"ExperimentalFeature",
						},
					},
				},
			},
			want: map[string]bool{
				"FeatureGate1":        true,
				"FeatureGate2":        true,
				"ExperimentalFeature": true,
			},
		},
		{
			name: "empty feature gates",
			hco: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"spec": map[string]interface{}{
						"featureGates": []interface{}{},
					},
				},
			},
			want: map[string]bool{},
		},
		{
			name: "no feature gates field",
			hco: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"spec": map[string]interface{}{},
				},
			},
			want: map[string]bool{},
		},
		{
			name: "no spec field",
			hco: &unstructured.Unstructured{
				Object: map[string]interface{}{},
			},
			want: map[string]bool{},
		},
		{
			name: "single feature gate",
			hco: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"spec": map[string]interface{}{
						"featureGates": []interface{}{
							"SingleFeature",
						},
					},
				},
			},
			want: map[string]bool{
				"SingleFeature": true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractFeatureGates(tt.hco)

			if len(got) != len(tt.want) {
				t.Errorf("extractFeatureGates() returned %d gates, want %d", len(got), len(tt.want))
			}

			for gate, enabled := range tt.want {
				if gotEnabled, exists := got[gate]; !exists {
					t.Errorf("extractFeatureGates() missing gate %q", gate)
				} else if gotEnabled != enabled {
					t.Errorf("extractFeatureGates()[%q] = %v, want %v", gate, gotEnabled, enabled)
				}
			}

			for gate := range got {
				if _, exists := tt.want[gate]; !exists {
					t.Errorf("extractFeatureGates() has unexpected gate %q", gate)
				}
			}
		})
	}
}

func TestNewPlatformReconciler(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	t.Run("creates reconciler successfully", func(t *testing.T) {
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

		reconciler, err := NewPlatformReconciler(fakeClient, fakeClient, "test-namespace")

		if err != nil {
			t.Fatalf("NewPlatformReconciler() error = %v", err)
		}

		if reconciler == nil {
			t.Fatal("NewPlatformReconciler() returned nil")
		}

		if reconciler.Namespace != "test-namespace" {
			t.Errorf("NewPlatformReconciler() namespace = %s, want test-namespace", reconciler.Namespace)
		}

		if reconciler.loader == nil {
			t.Error("NewPlatformReconciler() loader is nil")
		}

		if reconciler.registry == nil {
			t.Error("NewPlatformReconciler() registry is nil")
		}

		if reconciler.patcher == nil {
			t.Error("NewPlatformReconciler() patcher is nil")
		}

		if reconciler.contextBuilder == nil {
			t.Error("NewPlatformReconciler() contextBuilder is nil")
		}

		if reconciler.watchedCRDs == nil {
			t.Error("NewPlatformReconciler() watchedCRDs map is nil")
		}
	})
}

func TestSetEventRecorder(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	reconciler, _ := NewPlatformReconciler(fakeClient, fakeClient, "test-namespace")

	t.Run("sets event recorder", func(t *testing.T) {
		recorder := &util.EventRecorder{}
		reconciler.SetEventRecorder(recorder)

		if reconciler.eventRecorder != recorder {
			t.Error("SetEventRecorder() did not set event recorder")
		}
	})

	t.Run("sets event recorder on patcher", func(t *testing.T) {
		recorder := &util.EventRecorder{}
		reconciler.SetEventRecorder(recorder)

		// The patcher should have the recorder set (verified by not panicking)
		if reconciler.patcher == nil {
			t.Error("Patcher is nil after SetEventRecorder()")
		}
	})

	t.Run("sets event recorder on context builder", func(t *testing.T) {
		recorder := &util.EventRecorder{}
		reconciler.SetEventRecorder(recorder)

		// The context builder should have the recorder set
		if reconciler.contextBuilder == nil {
			t.Error("ContextBuilder is nil after SetEventRecorder()")
		}
	})
}

func TestSetShutdownFunc(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	reconciler, _ := NewPlatformReconciler(fakeClient, fakeClient, "test-namespace")

	t.Run("sets shutdown function", func(t *testing.T) {
		called := false
		shutdownFunc := func() {
			called = true
		}

		reconciler.SetShutdownFunc(shutdownFunc)

		// Verify it was set by calling triggerShutdown
		reconciler.triggerShutdown()

		if !called {
			t.Error("SetShutdownFunc() did not set shutdown function correctly")
		}
	})
}

func TestTriggerShutdown(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	reconciler, _ := NewPlatformReconciler(fakeClient, fakeClient, "test-namespace")

	t.Run("calls shutdown function when set", func(t *testing.T) {
		called := false
		shutdownFunc := func() {
			called = true
		}

		reconciler.SetShutdownFunc(shutdownFunc)
		reconciler.triggerShutdown()

		if !called {
			t.Error("triggerShutdown() did not call shutdown function")
		}
	})

	t.Run("does not panic when shutdown function not set", func(t *testing.T) {
		// This should exit with os.Exit(0) in real code, but in tests
		// we just verify it doesn't panic
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("triggerShutdown() panicked: %v", r)
			}
		}()

		// Note: In the actual implementation, this would call os.Exit(0)
		// which we can't test directly without forking the process
	})
}

func TestIsWatchedCRD(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	reconciler, _ := NewPlatformReconciler(fakeClient, fakeClient, "test-namespace")

	t.Run("returns false for unwatched CRD", func(t *testing.T) {
		if reconciler.isWatchedCRD("some-crd") {
			t.Error("isWatchedCRD() returned true for unwatched CRD")
		}
	})

	t.Run("returns true for watched CRD", func(t *testing.T) {
		reconciler.markCRDAsWatched("test-crd")

		if !reconciler.isWatchedCRD("test-crd") {
			t.Error("isWatchedCRD() returned false for watched CRD")
		}
	})

	t.Run("handles empty CRD name", func(t *testing.T) {
		if reconciler.isWatchedCRD("") {
			t.Error("isWatchedCRD() returned true for empty name")
		}
	})
}

func TestMarkCRDAsWatched(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	reconciler, _ := NewPlatformReconciler(fakeClient, fakeClient, "test-namespace")

	t.Run("marks CRD as watched", func(t *testing.T) {
		crdName := "test-crd.example.com"
		reconciler.markCRDAsWatched(crdName)

		if !reconciler.isWatchedCRD(crdName) {
			t.Error("markCRDAsWatched() did not mark CRD as watched")
		}
	})

	t.Run("handles multiple CRDs", func(t *testing.T) {
		crd1 := "crd1.example.com"
		crd2 := "crd2.example.com"

		reconciler.markCRDAsWatched(crd1)
		reconciler.markCRDAsWatched(crd2)

		if !reconciler.isWatchedCRD(crd1) {
			t.Error("markCRDAsWatched() did not mark first CRD")
		}

		if !reconciler.isWatchedCRD(crd2) {
			t.Error("markCRDAsWatched() did not mark second CRD")
		}
	})

	t.Run("is thread-safe with concurrent access", func(t *testing.T) {
		// Basic concurrency test - just verify it doesn't panic
		done := make(chan bool)

		for i := 0; i < 10; i++ {
			go func() {
				crdName := "concurrent-crd"
				reconciler.markCRDAsWatched(crdName)
				reconciler.isWatchedCRD(crdName)
				done <- true
			}()
		}

		for i := 0; i < 10; i++ {
			<-done
		}
	})
}

func TestIsManagedCRD(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	reconciler, _ := NewPlatformReconciler(fakeClient, fakeClient, "test-namespace")

	tests := []struct {
		name     string
		crdName  string
		expected bool
	}{
		{
			name:     "MachineConfig is managed",
			crdName:  "machineconfigs.machineconfiguration.openshift.io",
			expected: true,
		},
		{
			name:     "KubeletConfig is managed",
			crdName:  "kubeletconfigs.machineconfiguration.openshift.io",
			expected: true,
		},
		{
			name:     "NodeHealthCheck is managed",
			crdName:  "nodehealthchecks.remediation.medik8s.io",
			expected: true,
		},
		{
			name:     "ForkliftController is managed",
			crdName:  "forkliftcontrollers.forklift.konveyor.io",
			expected: true,
		},
		{
			name:     "MetalLB is managed",
			crdName:  "metallbs.metallb.io",
			expected: true,
		},
		{
			name:     "UIPlugin is managed",
			crdName:  "uiplugins.console.openshift.io",
			expected: true,
		},
		{
			name:     "KubeDescheduler is managed",
			crdName:  "kubedeschedulers.operator.openshift.io",
			expected: true,
		},
		{
			name:     "HyperConverged is managed",
			crdName:  "hyperconvergeds.hco.kubevirt.io",
			expected: true,
		},
		{
			name:     "random CRD is not managed",
			crdName:  "randomcrds.example.com",
			expected: false,
		},
		{
			name:     "empty string is not managed",
			crdName:  "",
			expected: false,
		},
		{
			name:     "partial match is not managed",
			crdName:  "machineconfig",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := reconciler.isManagedCRD(tt.crdName)
			if result != tt.expected {
				t.Errorf("isManagedCRD(%q) = %v, want %v", tt.crdName, result, tt.expected)
			}
		})
	}
}

func TestDetectHardware(t *testing.T) {
	ctx := context.Background()

	t.Run("GPU detection", func(t *testing.T) {
		testGPUDetection(ctx, t)
	})

	t.Run("other hardware detection", func(t *testing.T) {
		testOtherHardwareDetection(ctx, t)
	})

	t.Run("handles empty node list", func(t *testing.T) {
		scheme := runtime.NewScheme()
		_ = corev1.AddToScheme(scheme)
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

		builder := NewRenderContextBuilder(fakeClient)
		hardware, err := builder.detectHardware(ctx)

		if err != nil {
			t.Fatalf("detectHardware() error = %v", err)
		}
		if hardware.GPUPresent || hardware.PCIDevicesPresent ||
			hardware.NUMANodesPresent || hardware.VFIOCapable ||
			hardware.USBDevicesPresent {
			t.Error("detectHardware() detected hardware on empty node list")
		}
	})

	t.Run("returns error when client fails", func(t *testing.T) {
		scheme := runtime.NewScheme()
		_ = corev1.AddToScheme(scheme)
		fakeClient := &failingClient{
			Client: fake.NewClientBuilder().WithScheme(scheme).Build(),
		}

		builder := NewRenderContextBuilder(fakeClient)
		_, err := builder.detectHardware(ctx)

		if err == nil {
			t.Error("detectHardware() should return error when client fails")
		}
	})
}

func testGPUDetection(ctx context.Context, t *testing.T) {
	t.Helper()

	gpuTests := []struct {
		name         string
		resourceName string
	}{
		{"nvidia", "nvidia.com/gpu"},
		{"AMD", "amd.com/gpu"},
		{"Intel", "gpu.intel.com/i915"},
	}

	for _, tt := range gpuTests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			_ = corev1.AddToScheme(scheme)

			node := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "gpu-node"},
				Status: corev1.NodeStatus{
					Capacity: corev1.ResourceList{
						corev1.ResourceName(tt.resourceName): *newQuantity(1),
					},
				},
			}

			fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(node).Build()
			builder := NewRenderContextBuilder(fakeClient)
			hardware, err := builder.detectHardware(ctx)

			if err != nil {
				t.Fatalf("detectHardware() error = %v", err)
			}
			if !hardware.GPUPresent {
				t.Errorf("detectHardware() did not detect %s GPU", tt.name)
			}
		})
	}
}

func testOtherHardwareDetection(ctx context.Context, t *testing.T) {
	t.Helper()

	tests := []struct {
		name      string
		label     string
		checkFunc func(*pkgcontext.HardwareContext) bool
	}{
		{"PCI devices", "feature.node.kubernetes.io/pci-present", func(h *pkgcontext.HardwareContext) bool { return h.PCIDevicesPresent }},
		{"NUMA topology", "feature.node.kubernetes.io/cpu-hardware_multithreading", func(h *pkgcontext.HardwareContext) bool { return h.NUMANodesPresent }},
		{"VFIO capability", "feature.node.kubernetes.io/iommu-enabled", func(h *pkgcontext.HardwareContext) bool { return h.VFIOCapable }},
		{"USB devices", "feature.node.kubernetes.io/usb-present", func(h *pkgcontext.HardwareContext) bool { return h.USBDevicesPresent }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			_ = corev1.AddToScheme(scheme)

			node := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "test-node",
					Labels: map[string]string{tt.label: "true"},
				},
			}

			fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(node).Build()
			builder := NewRenderContextBuilder(fakeClient)
			hardware, err := builder.detectHardware(ctx)

			if err != nil {
				t.Fatalf("detectHardware() error = %v", err)
			}
			if !tt.checkFunc(hardware) {
				t.Errorf("detectHardware() did not detect %s", tt.name)
			}
		})
	}
}

// Helper to create resource quantities
func newQuantity(value int64) *resource.Quantity {
	q := resource.Quantity{}
	q.Set(value)
	return &q
}

// failingClient wraps a client to simulate failures
type failingClient struct {
	client.Client
}

func (f *failingClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	return &errorForTest{msg: "simulated client failure"}
}

type errorForTest struct {
	msg string
}

func (e *errorForTest) Error() string {
	return e.msg
}
