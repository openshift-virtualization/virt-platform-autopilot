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
	"strings"
	"testing"

	"github.com/kubevirt/virt-platform-autopilot/pkg/assets"
)

func TestRenderTableIncludesCompactControlsAndRollout(t *testing.T) {
	table := renderTable(assets.FrameworkStatus{Maturity: "GA"}, []assets.FeatureStatus{{
		Name: "CPU Manager", Description: "Pins CPUs", Maturity: "DP", Install: "opt-in",
		Enablement: []assets.FeatureEnablementControl{
			{ID: "cpu", Label: "Enable CPU Manager tunings", Annotation: "platform.kubevirt.io/enable-cpu-manager", Value: "true"},
			{ID: "audit", Label: "Enable audit collection", Annotation: "platform.kubevirt.io/enable-audit-logging", Value: "true"},
		},
		Prerequisites: []assets.FeaturePrerequisiteMetadata{{Type: "kubevirtFeatureGate", Name: "CPUManager"}},
		Rollout:       &assets.FeatureRollout{ExpectedDisruption: "nodeDrainRequired", Coverage: "partial"},
	}})
	for _, want := range []string{
		"| Install | Disruption | Dependencies |",
		"node drain required",
		"<details><summary>opt-in</summary><code>platform.kubevirt.io/enable-cpu-manager=true</code><br><code>platform.kubevirt.io/enable-audit-logging=true</code><br>requires <code>CPUManager</code></details>",
	} {
		if !strings.Contains(table, want) {
			t.Fatalf("table does not contain %q:\n%s", want, table)
		}
	}
}
