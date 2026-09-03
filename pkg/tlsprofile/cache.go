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

package tlsprofile

import (
	"reflect"
	"sync"

	configv1 "github.com/openshift/api/config/v1"
)

// profileCache holds a single TLSSecurityProfile behind an RWMutex. It is safe
// for concurrent use: the TLS server reads it once per new connection (via
// GetConfigForClient) while reconcilers/watches update it.
type profileCache struct {
	mu      sync.RWMutex
	profile *configv1.TLSSecurityProfile
}

// get returns the cached profile (may be nil when nothing has been observed yet).
func (c *profileCache) get() *configv1.TLSSecurityProfile {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.profile
}

// set stores p and reports whether it differs from the previous value. Callers
// use the returned bool to decide whether any downstream action is required.
func (c *profileCache) set(p *configv1.TLSSecurityProfile) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if reflect.DeepEqual(c.profile, p) {
		return false
	}
	c.profile = p
	return true
}

// The two authoritative sources of TLS policy, resolved with
// HCO override > APIServer cluster config > Intermediate default (see Resolve).
var (
	apiServerProfile      = &profileCache{}
	hyperConvergedProfile = &profileCache{}
)
