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

// prom-metrics-collector generates a JSON description of all Prometheus metrics
// exposed by virt-platform-autopilot, for use with prom-metrics-linter.
//
// Usage:
//
//	go run ./tools/prom-metrics-collector/ > /tmp/metrics.json
//
// The output is compatible with the --metric-families flag of the
// quay.io/kubevirt/prom-metrics-linter container image.
package main

import (
	"encoding/json"
	"os"

	dto "github.com/prometheus/client_model/go"
)

func strPtr(s string) *string { return &s }

func typePtr(t dto.MetricType) *dto.MetricType { return &t }

// metricFamilies lists all Prometheus metrics exposed by virt-platform-autopilot.
// Keep this in sync with pkg/observability/metrics.go.
var metricFamilies = []*dto.MetricFamily{
	{
		Name: strPtr("kubevirt_autopilot_compliance_status"),
		Help: strPtr("Compliance status of managed resources (1=synced, 0=drifted/failed)"),
		Type: typePtr(dto.MetricType_GAUGE),
	},
	{
		Name: strPtr("kubevirt_autopilot_thrashing_total"),
		Help: strPtr("Total number of reconciliation throttling events (anti-thrashing gate hits)"),
		Type: typePtr(dto.MetricType_COUNTER),
	},
	{
		Name: strPtr("kubevirt_autopilot_paused_resources"),
		Help: strPtr("Resources currently paused due to edit war detection (1=paused, 0=active)"),
		Type: typePtr(dto.MetricType_GAUGE),
	},
	{
		Name: strPtr("kubevirt_autopilot_customization_info"),
		Help: strPtr("Tracks intentional customizations (always 1 when present). Type: patch/ignore/unmanaged"),
		Type: typePtr(dto.MetricType_GAUGE),
	},
	{
		Name: strPtr("kubevirt_autopilot_missing_dependency"),
		Help: strPtr("Indicates missing optional CRDs (1=missing, 0=present)"),
		Type: typePtr(dto.MetricType_GAUGE),
	},
	{
		Name: strPtr("kubevirt_autopilot_reconcile_duration_seconds"),
		Help: strPtr("Duration of asset reconciliation operations (rendering + SSA apply)"),
		Type: typePtr(dto.MetricType_HISTOGRAM),
	},
	{
		Name: strPtr("kubevirt_autopilot_tombstone_status"),
		Help: strPtr("Tombstone deletion status (1=exists, 0=deleted, -1=error, -2=skipped)"),
		Type: typePtr(dto.MetricType_GAUGE),
	},
}

type collectorOutput struct {
	MetricFamilies []*dto.MetricFamily `json:"metricFamilies"`
}

func main() {
	out := collectorOutput{MetricFamilies: metricFamilies}
	if err := json.NewEncoder(os.Stdout).Encode(out); err != nil {
		panic(err)
	}
}
