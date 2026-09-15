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

// Package tlsprofile resolves the effective OpenShift TLS security profile for
// the operator's metrics endpoint and converts it into the Go tls.Config
// parameters (minimum version + cipher suites).
//
// It re-implements the resolution logic of HCO's internal tlssecprofile package
// (which is not importable), following the OpenShift API conventions: a
// component-level override (the HCO CR's spec.security.tlsSecurityProfile) takes
// precedence, otherwise the cluster APIServer CR's spec.tlsSecurityProfile is
// used, otherwise the Intermediate profile.
//
// Both source CRs are read as unstructured objects (matching the rest of this
// repo, which never registers external typed schemes); only the extracted
// tlsSecurityProfile sub-object is decoded into the typed configv1 struct so we
// can reuse the canonical configv1.TLSProfiles cipher tables and
// library-go/pkg/crypto for the Go conversion.
package tlsprofile

import (
	"cmp"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/library-go/pkg/crypto"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// apiServerName is the singleton name of the cluster APIServer config CR.
	apiServerName = "cluster"
)

// APIServerGVK is the GroupVersionKind of the cluster APIServer config CR
// (apiservers.config.openshift.io). Exposed so callers can set up an
// unstructured watch without importing configv1 themselves.
var APIServerGVK = schema.GroupVersionKind{
	Group:   "config.openshift.io",
	Version: "v1",
	Kind:    "APIServer",
}

// defaultProfile returns the Intermediate profile, used when neither the HCO CR
// nor the APIServer CR specify one.
func defaultProfile() *configv1.TLSSecurityProfile {
	return &configv1.TLSSecurityProfile{
		Type:         configv1.TLSProfileIntermediateType,
		Intermediate: &configv1.IntermediateTLSProfile{},
	}
}

// Resolve returns the effective TLS security profile, applying the precedence
// HCO override > cached APIServer profile > Intermediate default. A Custom
// profile that carries no usable ciphers is treated as unusable and falls back
// to the default (HCO parity).
func Resolve(fromHC *configv1.TLSSecurityProfile) *configv1.TLSSecurityProfile {
	profile := cmp.Or(fromHC, apiServerProfile.get(), defaultProfile())
	if profile.Type == configv1.TLSProfileCustomType &&
		(profile.Custom == nil || len(profile.Custom.Ciphers) == 0) {
		return defaultProfile()
	}
	return profile
}

// CipherSuitesAndMinTLSVersion returns the OpenSSL cipher names and minimum TLS
// version for the effective profile (see Resolve).
func CipherSuitesAndMinTLSVersion(fromHC *configv1.TLSSecurityProfile) ([]string, configv1.TLSProtocolVersion) {
	profile := Resolve(fromHC)
	if profile.Type == configv1.TLSProfileCustomType {
		return profile.Custom.Ciphers, profile.Custom.MinTLSVersion
	}
	spec := configv1.TLSProfiles[profile.Type]
	return spec.Ciphers, spec.MinTLSVersion
}

// GoCipherSuitesAndMinVersion converts the effective profile into the Go
// tls.Config representation: IANA cipher suite IDs and a crypto/tls version
// constant. Unknown OpenSSL cipher names are dropped by OpenSSLToIANACipherSuites,
// so an all-unknown Custom list degrades to the crypto defaults rather than
// panicking.
func GoCipherSuitesAndMinVersion(fromHC *configv1.TLSSecurityProfile) (ciphers []uint16, minVersion uint16) {
	cipherNames, minTLSVersion := CipherSuitesAndMinTLSVersion(fromHC)
	ciphers = crypto.CipherSuitesOrDie(crypto.OpenSSLToIANACipherSuites(cipherNames))
	minVersion = crypto.TLSVersionOrDie(string(minTLSVersion))
	return ciphers, minVersion
}

// ResolvedGoConfig converts the currently effective profile (using the cached
// HCO override, if any) into Go tls.Config parameters. This is the entry point
// used by the metrics TLS server on each new connection.
func ResolvedGoConfig() (ciphers []uint16, minVersion uint16) {
	return GoCipherSuitesAndMinVersion(hyperConvergedProfile.get())
}

// MutateTLSConfig installs a GetConfigForClient callback that applies the
// currently effective profile (min version, and cipher suites when the minimum
// is below TLS 1.3 — cipher selection is not configurable for TLS 1.3) to every
// new connection. It preserves any other settings already present on cfg
// (notably the certwatcher's GetCertificate, which controller-runtime installs
// after the TLSOpts mutators run).
func MutateTLSConfig(cfg *tls.Config) {
	base := cfg
	cfg.GetConfigForClient = func(*tls.ClientHelloInfo) (*tls.Config, error) {
		ciphers, minVersion := ResolvedGoConfig()
		c := base.Clone()
		c.MinVersion = minVersion
		if minVersion < tls.VersionTLS13 {
			c.CipherSuites = ciphers
		}
		return c, nil
	}
}

// SetHyperConvergedProfile updates the cached HCO override and reports whether it
// changed. Pass nil when the HCO CR carries no spec.security.tlsSecurityProfile.
func SetHyperConvergedProfile(p *configv1.TLSSecurityProfile) bool {
	return hyperConvergedProfile.set(p)
}

// SetHyperConvergedProfileFromUnstructured extracts
// spec.security.tlsSecurityProfile from the given HCO object and updates the
// cached override, reporting whether it changed.
func SetHyperConvergedProfileFromUnstructured(hco *unstructured.Unstructured) (bool, error) {
	profile, err := profileFromUnstructured(hco, "spec", "security", "tlsSecurityProfile")
	if err != nil {
		return false, err
	}
	return SetHyperConvergedProfile(profile), nil
}

// RefreshAPIServer reads the cluster APIServer CR (as unstructured) and updates
// the cached APIServer profile, reporting whether it changed. A NotFound error
// is returned to the caller, which may treat it as "no APIServer profile"
// (e.g. on non-OpenShift clusters).
func RefreshAPIServer(ctx context.Context, c client.Reader) (bool, error) {
	apiServer := &unstructured.Unstructured{}
	apiServer.SetGroupVersionKind(APIServerGVK)
	if err := c.Get(ctx, types.NamespacedName{Name: apiServerName}, apiServer); err != nil {
		return false, err
	}
	profile, err := profileFromUnstructured(apiServer, "spec", "tlsSecurityProfile")
	if err != nil {
		return false, err
	}
	return apiServerProfile.set(profile), nil
}

// profileFromUnstructured extracts the nested map at the given field path and
// decodes it into a typed TLSSecurityProfile via a JSON round-trip. It returns
// (nil, nil) when the field is absent, so an unset profile is represented
// uniformly as a nil pointer.
func profileFromUnstructured(obj *unstructured.Unstructured, fields ...string) (*configv1.TLSSecurityProfile, error) {
	raw, found, err := unstructured.NestedMap(obj.Object, fields...)
	if err != nil {
		return nil, fmt.Errorf("failed to read %v from %s: %w", fields, obj.GetKind(), err)
	}
	if !found || raw == nil {
		return nil, nil
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal tlsSecurityProfile: %w", err)
	}
	profile := &configv1.TLSSecurityProfile{}
	if err := json.Unmarshal(data, profile); err != nil {
		return nil, fmt.Errorf("failed to decode tlsSecurityProfile: %w", err)
	}
	return profile, nil
}
