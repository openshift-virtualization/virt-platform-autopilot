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
	"crypto/tls"
	"testing"

	configv1 "github.com/openshift/api/config/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// resetCaches clears the package-level caches so tests are independent.
func resetCaches(t *testing.T) {
	t.Helper()
	apiServerProfile.set(nil)
	hyperConvergedProfile.set(nil)
}

func modernProfile() *configv1.TLSSecurityProfile {
	return &configv1.TLSSecurityProfile{
		Type:   configv1.TLSProfileModernType,
		Modern: &configv1.ModernTLSProfile{},
	}
}

func oldProfile() *configv1.TLSSecurityProfile {
	return &configv1.TLSSecurityProfile{
		Type: configv1.TLSProfileOldType,
		Old:  &configv1.OldTLSProfile{},
	}
}

func TestResolvePriority(t *testing.T) {
	resetCaches(t)

	// Nothing set anywhere → Intermediate default.
	if got := Resolve(nil); got.Type != configv1.TLSProfileIntermediateType {
		t.Fatalf("expected Intermediate default, got %q", got.Type)
	}

	// APIServer set, no HCO override → APIServer profile wins.
	apiServerProfile.set(oldProfile())
	if got := Resolve(nil); got.Type != configv1.TLSProfileOldType {
		t.Fatalf("expected Old from APIServer, got %q", got.Type)
	}

	// HCO override present → wins over APIServer.
	if got := Resolve(modernProfile()); got.Type != configv1.TLSProfileModernType {
		t.Fatalf("expected Modern from HCO override, got %q", got.Type)
	}
}

func TestResolveCustomWithoutCiphersFallsBack(t *testing.T) {
	resetCaches(t)
	custom := &configv1.TLSSecurityProfile{
		Type:   configv1.TLSProfileCustomType,
		Custom: &configv1.CustomTLSProfile{}, // no ciphers
	}
	if got := Resolve(custom); got.Type != configv1.TLSProfileIntermediateType {
		t.Fatalf("expected fallback to Intermediate, got %q", got.Type)
	}
}

func TestGoCipherSuitesAndMinVersion(t *testing.T) {
	resetCaches(t)

	tests := []struct {
		name       string
		profile    *configv1.TLSSecurityProfile
		wantMin    uint16
		wantCipher bool // whether cipher suites should be non-empty
	}{
		{"intermediate", nil, tls.VersionTLS12, true},
		{"old", oldProfile(), tls.VersionTLS10, true},
		{"modern", modernProfile(), tls.VersionTLS13, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ciphers, minVer := GoCipherSuitesAndMinVersion(tc.profile)
			if minVer != tc.wantMin {
				t.Errorf("min version: got %#x want %#x", minVer, tc.wantMin)
			}
			if tc.wantCipher && len(ciphers) == 0 {
				t.Error("expected non-empty cipher list")
			}
		})
	}
}

func TestCustomProfileGoConfig(t *testing.T) {
	resetCaches(t)
	custom := &configv1.TLSSecurityProfile{
		Type: configv1.TLSProfileCustomType,
		Custom: &configv1.CustomTLSProfile{
			TLSProfileSpec: configv1.TLSProfileSpec{
				Ciphers:       []string{"ECDHE-ECDSA-AES256-GCM-SHA384"},
				MinTLSVersion: configv1.VersionTLS12,
			},
		},
	}
	ciphers, minVer := GoCipherSuitesAndMinVersion(custom)
	if minVer != tls.VersionTLS12 {
		t.Errorf("min version: got %#x want %#x", minVer, tls.VersionTLS12)
	}
	if len(ciphers) != 1 {
		t.Errorf("expected 1 cipher, got %d", len(ciphers))
	}
}

func TestMutateTLSConfig(t *testing.T) {
	resetCaches(t)
	hyperConvergedProfile.set(modernProfile())

	cfg := &tls.Config{}
	MutateTLSConfig(cfg)
	if cfg.GetConfigForClient == nil {
		t.Fatal("GetConfigForClient not installed")
	}
	got, err := cfg.GetConfigForClient(nil)
	if err != nil {
		t.Fatalf("GetConfigForClient error: %v", err)
	}
	if got.MinVersion != tls.VersionTLS13 {
		t.Errorf("min version: got %#x want %#x", got.MinVersion, tls.VersionTLS13)
	}
	// Modern is TLS 1.3 → cipher suites must not be pinned.
	if len(got.CipherSuites) != 0 {
		t.Errorf("expected no pinned ciphers for TLS 1.3, got %d", len(got.CipherSuites))
	}
}

func TestSetHyperConvergedProfileFromUnstructured(t *testing.T) {
	resetCaches(t)
	hco := &unstructured.Unstructured{Object: map[string]any{
		"spec": map[string]any{
			"security": map[string]any{
				"tlsSecurityProfile": map[string]any{
					"type":   "Modern",
					"modern": map[string]any{},
				},
			},
		},
	}}
	changed, err := SetHyperConvergedProfileFromUnstructured(hco)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !changed {
		t.Error("expected changed=true on first set")
	}
	if got := hyperConvergedProfile.get(); got == nil || got.Type != configv1.TLSProfileModernType {
		t.Fatalf("expected Modern cached, got %+v", got)
	}

	// Absent profile → nil, and a second identical set reports no change.
	changed, err = SetHyperConvergedProfileFromUnstructured(hco)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if changed {
		t.Error("expected changed=false on identical set")
	}

	empty := &unstructured.Unstructured{Object: map[string]any{}}
	changed, err = SetHyperConvergedProfileFromUnstructured(empty)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !changed {
		t.Error("expected changed=true when profile removed")
	}
	if got := hyperConvergedProfile.get(); got != nil {
		t.Errorf("expected nil cached profile, got %+v", got)
	}
}
