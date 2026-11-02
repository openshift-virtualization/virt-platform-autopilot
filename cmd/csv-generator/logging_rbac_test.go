/*
Copyright 2026 The Virt Platform Autopilot Authors.

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

package main

import (
	"reflect"
	"slices"
	"testing"

	embeddedassets "github.com/kubevirt/virt-platform-autopilot/assets"
	"github.com/kubevirt/virt-platform-autopilot/pkg/rbac"
)

func TestCSVIncludesScopedCollectorBindPermissions(t *testing.T) {
	rules, err := rbac.AllRules(embeddedassets.EmbeddedFS)
	if err != nil {
		t.Fatal(err)
	}

	tstConfig := configuration{
		csvVersion:          "1.0.0",
		namespace:           "openshift-cnv",
		operatorImage:       "quay.io/test/autopilot:test",
		operatorVersion:     "1.0.0",
		pullPolicy:          "IfNotPresent",
		dumpNetworkPolicies: false,
	}

	csv := buildCSV(tstConfig, nil, rules)
	var names []string
	for _, permissions := range csv.Spec.Install.Spec.ClusterPermissions {
		for _, rule := range permissions.Rules {
			if !slices.Contains(rule.Verbs, "bind") {
				continue
			}
			if !reflect.DeepEqual(rule.APIGroups, []string{"rbac.authorization.k8s.io"}) || !reflect.DeepEqual(rule.Resources, []string{"clusterroles"}) || len(rule.ResourceNames) == 0 {
				t.Fatalf("bind permission must be scoped to named ClusterRoles: %#v", rule)
			}
			names = append(names, rule.ResourceNames...)
		}
	}
	slices.Sort(names)
	want := []string{"collect-application-logs", "collect-audit-logs", "collect-infrastructure-logs", "logging-collector-logs-writer"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("CSV bind permissions = %q, want explicit allowlist %q", names, want)
	}
}

func TestDumpNetworkPolicies(t *testing.T) {
	tstConfig := configuration{
		csvVersion:          "1.0.0",
		namespace:           "openshift-cnv",
		operatorImage:       "quay.io/test/autopilot:test",
		operatorVersion:     "1.0.0",
		pullPolicy:          "IfNotPresent",
		dumpNetworkPolicies: true,
	}

	csv := buildCSV(tstConfig, nil, nil)

	if actual, required := len(csv.Spec.Install.Spec.Deployments), 1; actual != required {
		t.Fatalf("csv has wrong number of deployments: %d, want %d", actual, required)
	}

	if val, exists := csv.Spec.Install.Spec.Deployments[0].Spec.Template.Metadata.Labels[allowIngressToMetricsEndpointLabel]; !exists || val != "true" {
		t.Errorf("the %q pod label is missing or misconfigured with %q", allowIngressToMetricsEndpointLabel, val)
	}

	if val, exists := csv.Spec.Install.Spec.Deployments[0].Spec.Template.Metadata.Labels[allowEgressToDNSAndAPIServerLabel]; !exists || val != "true" {
		t.Errorf("the %q pod label is missing or misconfigured with %q", allowEgressToDNSAndAPIServerLabel, val)
	}
}

func TestDontDumpNetworkPolicies(t *testing.T) {
	tstConfig := configuration{
		csvVersion:          "1.0.0",
		namespace:           "openshift-cnv",
		operatorImage:       "quay.io/test/autopilot:test",
		operatorVersion:     "1.0.0",
		pullPolicy:          "IfNotPresent",
		dumpNetworkPolicies: false,
	}

	csv := buildCSV(tstConfig, nil, nil)

	if actual, required := len(csv.Spec.Install.Spec.Deployments), 1; actual != required {
		t.Fatalf("csv has wrong number of deployments: %d, want %d", actual, required)
	}

	if _, exists := csv.Spec.Install.Spec.Deployments[0].Spec.Template.Metadata.Labels[allowIngressToMetricsEndpointLabel]; exists {
		t.Errorf("the %q pod label is not expected, but it exists", allowIngressToMetricsEndpointLabel)
	}

	if _, exists := csv.Spec.Install.Spec.Deployments[0].Spec.Template.Metadata.Labels[allowEgressToDNSAndAPIServerLabel]; exists {
		t.Errorf("the %q pod label is not expected, but it exists", allowEgressToDNSAndAPIServerLabel)
	}
}
