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

package observability

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func resetForTest(t *testing.T) {
	t.Helper()
	ResetRenderFailures()
	t.Cleanup(ResetRenderFailures)
}

func assertRenderFailed(t *testing.T, expected string) {
	t.Helper()
	if err := testutil.CollectAndCompare(RenderFailed, strings.NewReader(expected)); err != nil {
		t.Fatal(err)
	}
}

func TestRenderFailureRecovery(t *testing.T) {
	resetForTest(t)
	SetRenderFailure("asset", "configuration", "NoDefaultStorageClass")
	SetRenderFailure("other", "internal", "")
	SetRenderFailure("asset", "internal", "")
	// The superseded configuration series stays present at 0 instead of
	// disappearing: a missing series would reset the alert's for: timer.
	assertRenderFailed(t, `# HELP kubevirt_autopilot_render_failed Asset rendering failures by reason and code (1=failed, 0=healthy or excluded; the series persists after recovery)
# TYPE kubevirt_autopilot_render_failed gauge
kubevirt_autopilot_render_failed{asset="asset",code="",reason="internal"} 1
kubevirt_autopilot_render_failed{asset="asset",code="NoDefaultStorageClass",reason="configuration"} 0
kubevirt_autopilot_render_failed{asset="other",code="",reason="internal"} 1
`)

	// Recovery keeps the series at 0 and preserves the other asset's failure.
	SetRenderFailure("asset", "", "")
	assertRenderFailed(t, `# HELP kubevirt_autopilot_render_failed Asset rendering failures by reason and code (1=failed, 0=healthy or excluded; the series persists after recovery)
# TYPE kubevirt_autopilot_render_failed gauge
kubevirt_autopilot_render_failed{asset="asset",code="",reason="internal"} 0
kubevirt_autopilot_render_failed{asset="asset",code="NoDefaultStorageClass",reason="configuration"} 0
kubevirt_autopilot_render_failed{asset="other",code="",reason="internal"} 1
`)

	// A repeated failure with the same labels reuses the settled series.
	SetRenderFailure("asset", "internal", "")
	assertRenderFailed(t, `# HELP kubevirt_autopilot_render_failed Asset rendering failures by reason and code (1=failed, 0=healthy or excluded; the series persists after recovery)
# TYPE kubevirt_autopilot_render_failed gauge
kubevirt_autopilot_render_failed{asset="asset",code="",reason="internal"} 1
kubevirt_autopilot_render_failed{asset="asset",code="NoDefaultStorageClass",reason="configuration"} 0
kubevirt_autopilot_render_failed{asset="other",code="",reason="internal"} 1
`)
}

func TestResetRenderFailuresForgetsLabels(t *testing.T) {
	resetForTest(t)
	SetRenderFailure("asset", "configuration", "NoDefaultStorageClass")
	ResetRenderFailures()
	if got := testutil.CollectAndCount(RenderFailed); got != 0 {
		t.Fatalf("reset retained %d series", got)
	}
	// Recovery after a reset must not resurrect the deleted series.
	SetRenderFailure("asset", "", "")
	if got := testutil.CollectAndCount(RenderFailed); got != 0 {
		t.Fatalf("recovery resurrected %d series after reset", got)
	}
}
