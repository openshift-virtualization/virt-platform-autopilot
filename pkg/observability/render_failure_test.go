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
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestRenderFailureRecovery(t *testing.T) {
	RenderFailed.Reset()
	t.Cleanup(RenderFailed.Reset)
	SetRenderFailure("asset", "configuration", "NoDefaultStorageClass")
	SetRenderFailure("other", "internal", "")
	SetRenderFailure("asset", "internal", "")
	if got := testutil.CollectAndCount(RenderFailed); got != 2 {
		t.Fatalf("reason change retained stale series: %d", got)
	}
	SetRenderFailure("asset", "", "")
	if got := testutil.CollectAndCount(RenderFailed); got != 1 {
		t.Fatalf("recovery did not preserve other asset: %d", got)
	}
}
