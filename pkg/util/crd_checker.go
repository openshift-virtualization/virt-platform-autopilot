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
	"fmt"
	"sync"
	"time"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ComponentKindMapping maps asset components to their CRD names
// This defines the soft dependencies - which components require which CRDs
var ComponentKindMapping = map[string]string{
	"MachineConfig":          "machineconfigs.machineconfiguration.openshift.io",
	"KubeletConfig":          "kubeletconfigs.machineconfiguration.openshift.io",
	"NodeHealthCheck":        "nodehealthchecks.remediation.medik8s.io",
	"ForkliftController":     "forkliftcontrollers.forklift.konveyor.io",
	"MetalLB":                "metallbs.metallb.io",
	"UIPlugin":               "uiplugins.console.openshift.io",
	"KubeDescheduler":        "kubedeschedulers.operator.openshift.io",
	"SelfNodeRemediation":    "selfnoderemediations.self-node-remediation.medik8s.io",
	"FenceAgentsRemediation": "fenceagentsremediations.fence-agents-remediation.medik8s.io",
	"HyperConverged":         "hyperconvergeds.hco.kubevirt.io", // Always required
}

// CRDChecker provides CRD availability checking with caching
type CRDChecker struct {
	client client.Reader
	cache  *crdCache
}

// crdCache caches CRD existence checks to reduce API calls
type crdCache struct {
	mu      sync.RWMutex
	entries map[string]*cacheEntry
	ttl     time.Duration
}

type cacheEntry struct {
	exists    bool
	timestamp time.Time
}

// NewCRDChecker creates a new CRD checker
// Accepts a Reader (not Client) since it only needs to read CRDs
func NewCRDChecker(c client.Reader) *CRDChecker {
	return &CRDChecker{
		client: c,
		cache: &crdCache{
			entries: make(map[string]*cacheEntry),
			ttl:     30 * time.Second, // Cache for 30 seconds
		},
	}
}

// IsCRDInstalled checks if a CRD is installed in the cluster
func (c *CRDChecker) IsCRDInstalled(ctx context.Context, crdName string) (bool, error) {
	// Check cache first
	if exists, found := c.cache.get(crdName); found {
		return exists, nil
	}

	// Query API server
	crd := &apiextensionsv1.CustomResourceDefinition{}
	err := c.client.Get(ctx, types.NamespacedName{Name: crdName}, crd)

	if err != nil {
		if errors.IsNotFound(err) {
			// CRD not found - cache negative result
			c.cache.set(crdName, false)
			return false, nil
		}
		// Other error - don't cache, return error
		return false, fmt.Errorf("failed to check CRD %s: %w", crdName, err)
	}

	// CRD exists - cache positive result
	c.cache.set(crdName, true)
	return true, nil
}

// IsComponentSupported checks if a component's CRD is installed
// Returns (supported, crdName, error)
func (c *CRDChecker) IsComponentSupported(ctx context.Context, component string) (bool, string, error) {
	crdName, ok := ComponentKindMapping[component]
	if !ok {
		// Component not in mapping - assume it's a core Kubernetes resource
		// or a resource that doesn't require CRD checking
		return true, "", nil
	}

	installed, err := c.IsCRDInstalled(ctx, crdName)
	if err != nil {
		return false, crdName, err
	}

	return installed, crdName, nil
}

// IsGVKSupported checks if a GroupVersionKind is supported in the cluster
func (c *CRDChecker) IsGVKSupported(ctx context.Context, gvk schema.GroupVersionKind) (bool, error) {
	// For core Kubernetes types (empty group), always return true
	if gvk.Group == "" {
		return true, nil
	}

	// Construct CRD name: <plural>.<group>
	// Note: This is a heuristic. The actual CRD name might differ.
	// For production use, maintain a proper mapping or query discovery API
	crdName := fmt.Sprintf("%ss.%s", gvk.Kind, gvk.Group)

	return c.IsCRDInstalled(ctx, crdName)
}

// InvalidateCache clears the cache for a specific CRD or all CRDs
func (c *CRDChecker) InvalidateCache(crdName string) {
	if crdName == "" {
		// Clear entire cache
		c.cache.clear()
	} else {
		// Clear specific entry
		c.cache.delete(crdName)
	}
}

// crdCache methods

func (cc *crdCache) get(crdName string) (bool, bool) {
	cc.mu.RLock()
	defer cc.mu.RUnlock()

	entry, found := cc.entries[crdName]
	if !found {
		return false, false
	}

	// Check if entry is still valid
	if time.Since(entry.timestamp) > cc.ttl {
		// Entry expired
		return false, false
	}

	return entry.exists, true
}

func (cc *crdCache) set(crdName string, exists bool) {
	cc.mu.Lock()
	defer cc.mu.Unlock()

	cc.entries[crdName] = &cacheEntry{
		exists:    exists,
		timestamp: time.Now(),
	}
}

func (cc *crdCache) delete(crdName string) {
	cc.mu.Lock()
	defer cc.mu.Unlock()

	delete(cc.entries, crdName)
}

func (cc *crdCache) clear() {
	cc.mu.Lock()
	defer cc.mu.Unlock()

	cc.entries = make(map[string]*cacheEntry)
}

// GetMissingCRDs returns a list of CRD names that are not installed
// for the given components
func (c *CRDChecker) GetMissingCRDs(ctx context.Context, components []string) ([]string, error) {
	var missing []string

	for _, component := range components {
		supported, crdName, err := c.IsComponentSupported(ctx, component)
		if err != nil {
			return nil, fmt.Errorf("failed to check component %s: %w", component, err)
		}

		if !supported && crdName != "" {
			missing = append(missing, crdName)
		}
	}

	return missing, nil
}
