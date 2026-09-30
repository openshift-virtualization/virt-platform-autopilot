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
	csv := buildCSV("1.0.0", "openshift-cnv", "quay.io/test/autopilot:test", "1.0.0", "IfNotPresent", nil, rules)
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
