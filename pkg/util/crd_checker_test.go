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

package util

import (
	"context"
	"testing"
	"time"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestCRDChecker_IsCRDInstalled(t *testing.T) {
	tests := []struct {
		name       string
		crdName    string
		crdExists  bool
		wantExists bool
		wantErr    bool
	}{
		{
			name:       "CRD exists",
			crdName:    "foos.example.com",
			crdExists:  true,
			wantExists: true,
			wantErr:    false,
		},
		{
			name:       "CRD does not exist",
			crdName:    "bars.example.com",
			crdExists:  false,
			wantExists: false,
			wantErr:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			_ = apiextensionsv1.AddToScheme(scheme)

			var objects []client.Object
			if tt.crdExists {
				crd := &apiextensionsv1.CustomResourceDefinition{}
				crd.SetName(tt.crdName)
				objects = append(objects, crd)
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(objects...).
				Build()

			checker := NewCRDChecker(fakeClient)
			ctx := context.Background()

			exists, err := checker.IsCRDInstalled(ctx, tt.crdName)

			if (err != nil) != tt.wantErr {
				t.Errorf("IsCRDInstalled() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if exists != tt.wantExists {
				t.Errorf("IsCRDInstalled() exists = %v, want %v", exists, tt.wantExists)
			}
		})
	}
}

func TestCRDChecker_IsComponentSupported(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = apiextensionsv1.AddToScheme(scheme)

	// Create fake CRDs for some components
	metallbCRD := &apiextensionsv1.CustomResourceDefinition{}
	metallbCRD.SetName("metallbs.metallb.io")

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(metallbCRD).
		Build()

	checker := NewCRDChecker(fakeClient)
	ctx := context.Background()

	tests := []struct {
		name          string
		component     string
		wantSupported bool
		wantCRDName   string
		wantErr       bool
	}{
		{
			name:          "supported component with CRD installed",
			component:     "MetalLB",
			wantSupported: true,
			wantCRDName:   "metallbs.metallb.io",
			wantErr:       false,
		},
		{
			name:          "supported component with CRD missing",
			component:     "NodeHealthCheck",
			wantSupported: false,
			wantCRDName:   "nodehealthchecks.remediation.medik8s.io",
			wantErr:       false,
		},
		{
			name:          "unknown component (assumes core resource)",
			component:     "UnknownComponent",
			wantSupported: true,
			wantCRDName:   "",
			wantErr:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			supported, crdName, err := checker.IsComponentSupported(ctx, tt.component)

			if (err != nil) != tt.wantErr {
				t.Errorf("IsComponentSupported() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if supported != tt.wantSupported {
				t.Errorf("IsComponentSupported() supported = %v, want %v", supported, tt.wantSupported)
			}

			if crdName != tt.wantCRDName {
				t.Errorf("IsComponentSupported() crdName = %v, want %v", crdName, tt.wantCRDName)
			}
		})
	}
}

func TestCRDChecker_IsGVKSupported(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = apiextensionsv1.AddToScheme(scheme)

	// Create a fake CRD for testing
	fooCRD := &apiextensionsv1.CustomResourceDefinition{}
	fooCRD.SetName("Foos.example.com")

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(fooCRD).
		Build()

	checker := NewCRDChecker(fakeClient)
	ctx := context.Background()

	tests := []struct {
		name       string
		gvk        schema.GroupVersionKind
		wantExists bool
	}{
		{
			name: "core Kubernetes type (empty group)",
			gvk: schema.GroupVersionKind{
				Group:   "",
				Version: "v1",
				Kind:    "Pod",
			},
			wantExists: true, // Core types always return true
		},
		{
			name: "custom resource with CRD installed",
			gvk: schema.GroupVersionKind{
				Group:   "example.com",
				Version: "v1",
				Kind:    "Foo",
			},
			wantExists: true,
		},
		{
			name: "custom resource without CRD",
			gvk: schema.GroupVersionKind{
				Group:   "missing.com",
				Version: "v1",
				Kind:    "Bar",
			},
			wantExists: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exists, err := checker.IsGVKSupported(ctx, tt.gvk)
			if err != nil {
				t.Errorf("IsGVKSupported() unexpected error: %v", err)
				return
			}

			if exists != tt.wantExists {
				t.Errorf("IsGVKSupported() = %v, want %v", exists, tt.wantExists)
			}
		})
	}
}

func TestCRDChecker_CacheFunctionality(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = apiextensionsv1.AddToScheme(scheme)

	crd := &apiextensionsv1.CustomResourceDefinition{}
	crd.SetName("foos.example.com")

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(crd).
		Build()

	checker := NewCRDChecker(fakeClient)
	ctx := context.Background()

	// First call - should query API and cache
	exists1, err := checker.IsCRDInstalled(ctx, "foos.example.com")
	if err != nil {
		t.Fatalf("First IsCRDInstalled() failed: %v", err)
	}
	if !exists1 {
		t.Fatal("Expected CRD to exist")
	}

	// Second call - should use cache
	exists2, err := checker.IsCRDInstalled(ctx, "foos.example.com")
	if err != nil {
		t.Fatalf("Second IsCRDInstalled() failed: %v", err)
	}
	if !exists2 {
		t.Fatal("Expected CRD to exist (from cache)")
	}

	// Verify cache hit
	if exists1 != exists2 {
		t.Error("Cache should return same result")
	}
}

func TestCRDChecker_CacheExpiration(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = apiextensionsv1.AddToScheme(scheme)

	crd := &apiextensionsv1.CustomResourceDefinition{}
	crd.SetName("foos.example.com")

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(crd).
		Build()

	checker := NewCRDChecker(fakeClient)
	// Set very short TTL for testing
	checker.cache.ttl = 10 * time.Millisecond

	ctx := context.Background()

	// First call - populate cache
	exists1, err := checker.IsCRDInstalled(ctx, "foos.example.com")
	if err != nil || !exists1 {
		t.Fatal("Initial check failed")
	}

	// Wait for cache to expire
	time.Sleep(20 * time.Millisecond)

	// Cache should have expired, will query API again
	exists2, err := checker.IsCRDInstalled(ctx, "foos.example.com")
	if err != nil || !exists2 {
		t.Fatal("Check after expiration failed")
	}
}

func TestCRDChecker_InvalidateCache(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = apiextensionsv1.AddToScheme(scheme)

	crd := &apiextensionsv1.CustomResourceDefinition{}
	crd.SetName("foos.example.com")

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(crd).
		Build()

	checker := NewCRDChecker(fakeClient)
	ctx := context.Background()

	// Populate cache
	_, _ = checker.IsCRDInstalled(ctx, "foos.example.com")
	_, _ = checker.IsCRDInstalled(ctx, "bars.example.com")

	// Check cache has entries
	if len(checker.cache.entries) != 2 {
		t.Fatalf("Expected 2 cache entries, got %d", len(checker.cache.entries))
	}

	// Invalidate specific entry
	checker.InvalidateCache("foos.example.com")

	if len(checker.cache.entries) != 1 {
		t.Errorf("Expected 1 cache entry after specific invalidation, got %d", len(checker.cache.entries))
	}

	// Invalidate all
	checker.InvalidateCache("")

	if len(checker.cache.entries) != 0 {
		t.Errorf("Expected 0 cache entries after full invalidation, got %d", len(checker.cache.entries))
	}
}

func TestCRDChecker_GetMissingCRDs(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = apiextensionsv1.AddToScheme(scheme)

	// Install only MetalLB CRD
	metallbCRD := &apiextensionsv1.CustomResourceDefinition{}
	metallbCRD.SetName("metallbs.metallb.io")

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(metallbCRD).
		Build()

	checker := NewCRDChecker(fakeClient)
	ctx := context.Background()

	components := []string{
		"MetalLB",            // Installed
		"NodeHealthCheck",    // Missing
		"ForkliftController", // Missing
		"UnknownComponent",   // Not in mapping (ignored)
	}

	missing, err := checker.GetMissingCRDs(ctx, components)
	if err != nil {
		t.Fatalf("GetMissingCRDs() failed: %v", err)
	}

	expectedMissing := []string{
		"nodehealthchecks.remediation.medik8s.io",
		"forkliftcontrollers.forklift.konveyor.io",
	}

	if len(missing) != len(expectedMissing) {
		t.Fatalf("Expected %d missing CRDs, got %d", len(expectedMissing), len(missing))
	}

	for i, crdName := range expectedMissing {
		if missing[i] != crdName {
			t.Errorf("Expected missing CRD %s, got %s", crdName, missing[i])
		}
	}
}

func TestCRDChecker_ConcurrentAccess(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = apiextensionsv1.AddToScheme(scheme)

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		Build()

	checker := NewCRDChecker(fakeClient)
	ctx := context.Background()

	// Test concurrent access to cache (should not race)
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func() {
			_, _ = checker.IsCRDInstalled(ctx, "test.example.com")
			done <- true
		}()
	}

	for i := 0; i < 10; i++ {
		<-done
	}

	// If we got here without a race detector panic, test passed
}

// Test that cache methods are thread-safe
func TestCRDCache_ThreadSafety(t *testing.T) {
	cache := &crdCache{
		entries: make(map[string]*cacheEntry),
		ttl:     30 * time.Second,
	}

	done := make(chan bool)

	// Concurrent writers
	for i := 0; i < 5; i++ {
		go func(n int) {
			cache.set(string(rune('A'+n)), true)
			done <- true
		}(i)
	}

	// Concurrent readers
	for i := 0; i < 5; i++ {
		go func() {
			_, _ = cache.get("A")
			done <- true
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}

	// If no race detector panic, test passed
}

func TestComponentKindMapping(t *testing.T) {
	// Verify critical components are mapped
	critical := []string{
		"HyperConverged",
		"MachineConfig",
		"NodeHealthCheck",
		"MetalLB",
	}

	for _, component := range critical {
		if _, ok := ComponentKindMapping[component]; !ok {
			t.Errorf("Critical component %s not found in ComponentKindMapping", component)
		}
	}

	// Verify HyperConverged is mapped (always required)
	hcoCRD, ok := ComponentKindMapping["HyperConverged"]
	if !ok || hcoCRD != "hyperconvergeds.hco.kubevirt.io" {
		t.Error("HyperConverged mapping incorrect")
	}
}
