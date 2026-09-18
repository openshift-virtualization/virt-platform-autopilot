package engine

import (
	"context"
	"maps"
	"slices"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	pkgcontext "github.com/kubevirt/virt-platform-autopilot/pkg/context"
	"github.com/kubevirt/virt-platform-autopilot/pkg/util"
)

func TestMachineConfigUpdateIsStagedUntilMatchingPoolUpdates(t *testing.T) {
	ctx := context.Background()
	p := NewPatcher(fake.NewClientBuilder().WithRuntimeObjects(testMCP("worker", "False")).Build(), nil, nil)
	events := &countingRecorder{counts: map[string]int{}}
	p.SetEventRecorder(util.NewEventRecorder(events))
	desired, live := testMachineConfigs()

	apply, err := p.coalesceMachineConfigUpdate(ctx, desired, live, testHash(t, desired), testRenderContext())
	if err != nil || apply {
		t.Fatalf("stable MCP: apply=%t err=%v, want false nil", apply, err)
	}
	if events.counts[util.EventReasonMachineConfigUpdateStaged] != 1 {
		t.Fatalf("staged events = %d, want 1", events.counts[util.EventReasonMachineConfigUpdateStaged])
	}
	stage, err := p.machineConfigStage(ctx, "openshift-cnv", desired.GetName())
	if err != nil || stage == nil {
		t.Fatalf("stage was not persisted: stage=%v err=%v", stage, err)
	}
	if len(stage.MatchingPools) != 1 || stage.MatchingPools[0] != "worker" {
		t.Fatalf("stage.MatchingPools = %v, want [worker]", stage.MatchingPools)
	}
	cm := &corev1.ConfigMap{}
	if err := p.client.Get(ctx, client.ObjectKey{Namespace: "openshift-cnv", Name: machineConfigStagingConfigMap}, cm); err != nil {
		t.Fatal(err)
	}
	if len(cm.OwnerReferences) != 1 || cm.OwnerReferences[0].UID != "test-hco" {
		t.Fatalf("staging ConfigMap owner references = %#v, want HCO owner", cm.OwnerReferences)
	}
	if cm.OwnerReferences[0].BlockOwnerDeletion != nil {
		t.Fatalf("staging ConfigMap BlockOwnerDeletion = %v, want nil", *cm.OwnerReferences[0].BlockOwnerDeletion)
	}

	setPoolUpdating(t, p.client, "worker")
	apply, err = p.coalesceMachineConfigUpdate(ctx, desired, live, testHash(t, desired), testRenderContext())
	if err != nil || !apply {
		t.Fatalf("updating MCP: apply=%t err=%v, want true nil", apply, err)
	}

	// Releasing must not retire the entry: the apply can still be refused by the
	// namespace guard or the token bucket, and the original staging timestamp has
	// to survive that. ReconcileAsset clears it after a successful apply.
	stage, err = p.machineConfigStage(ctx, "openshift-cnv", desired.GetName())
	if err != nil || stage == nil {
		t.Fatalf("stage was dropped on release before apply: stage=%v err=%v", stage, err)
	}
	if _, err := p.clearMachineConfigStaging(ctx, desired, testRenderContext()); err != nil {
		t.Fatal(err)
	}
	stage, err = p.machineConfigStage(ctx, "openshift-cnv", desired.GetName())
	if err != nil || stage != nil {
		t.Fatalf("stage was not cleared after apply: stage=%v err=%v", stage, err)
	}
	if err := p.client.Get(ctx, client.ObjectKey{Namespace: "openshift-cnv", Name: machineConfigStagingConfigMap}, cm); err != nil {
		t.Fatal(err)
	}
	if _, ok := cm.Data[desired.GetName()]; ok {
		t.Fatalf("staging ConfigMap still holds %s: %v", desired.GetName(), cm.Data)
	}
}

// The staging timestamp is the only record of how long an update has waited. It
// must only restart when the update itself changes.
func TestMachineConfigStagingTimestampSurvivesUnchangedDesiredState(t *testing.T) {
	ctx := context.Background()
	p := NewPatcher(fake.NewClientBuilder().WithRuntimeObjects(testMCP("worker", "False")).Build(), nil, nil)
	desired, live := testMachineConfigs()
	hash := testHash(t, desired)

	if _, err := p.coalesceMachineConfigUpdate(ctx, desired, live, hash, testRenderContext()); err != nil {
		t.Fatal(err)
	}
	first, err := p.machineConfigStage(ctx, "openshift-cnv", desired.GetName())
	if err != nil || first == nil {
		t.Fatalf("stage was not persisted: %v", err)
	}

	time.Sleep(time.Millisecond)
	if _, err := p.coalesceMachineConfigUpdate(ctx, desired, live, hash, testRenderContext()); err != nil {
		t.Fatal(err)
	}
	second, err := p.machineConfigStage(ctx, "openshift-cnv", desired.GetName())
	if err != nil || second == nil {
		t.Fatalf("stage disappeared: %v", err)
	}
	if !second.StagedAt.Equal(first.StagedAt) {
		t.Errorf("StagedAt = %v on restage of an unchanged update, want %v", second.StagedAt, first.StagedAt)
	}

	if _, err := p.coalesceMachineConfigUpdate(ctx, desired, live, hash+"-changed", testRenderContext()); err != nil {
		t.Fatal(err)
	}
	third, err := p.machineConfigStage(ctx, "openshift-cnv", desired.GetName())
	if err != nil || third == nil {
		t.Fatalf("stage disappeared: %v", err)
	}
	if third.StagedAt.Equal(first.StagedAt) {
		t.Error("StagedAt did not restart after the desired state changed")
	}
}

// The staging ConfigMap is mirrored in memory, so consulting staged state for
// every MachineConfig on every reconcile must not cost an API read each time.
func TestMachineConfigStagingIsReadFromTheApiServerOnce(t *testing.T) {
	ctx := context.Background()
	backing := fake.NewClientBuilder().WithRuntimeObjects(testMCP("worker", "False")).Build()
	reader := &countingReader{Reader: backing}
	p := NewPatcher(backing, reader, nil)
	desired, live := testMachineConfigs()

	for range 3 {
		if _, err := p.coalesceMachineConfigUpdate(ctx, desired, live, testHash(t, desired), testRenderContext()); err != nil {
			t.Fatal(err)
		}
	}
	if reader.gets != 1 {
		t.Errorf("staging ConfigMap reads = %d, want 1", reader.gets)
	}
}

func TestMachineConfigBypassIsPreservedAndImmediate(t *testing.T) {
	ctx := context.Background()
	p := NewPatcher(fake.NewClientBuilder().Build(), nil, nil)
	desired, live := testMachineConfigs()
	live.SetAnnotations(map[string]string{MachineConfigCoalescingBypassAnnotation: "true"})
	apply, err := p.coalesceMachineConfigUpdate(ctx, desired, live, testHash(t, desired), testRenderContext())
	if err != nil || !apply {
		t.Fatalf("bypass: apply=%t err=%v, want true nil", apply, err)
	}
	if desired.GetAnnotations()[MachineConfigCoalescingBypassAnnotation] != "true" {
		t.Fatal("bypass annotation was not preserved")
	}
}

// Staging is the safe default: a typo in the bypass annotation must not silently
// start a node-rebooting rollout.
func TestMachineConfigBypassIgnoresUnparseableValue(t *testing.T) {
	ctx := context.Background()
	p := NewPatcher(fake.NewClientBuilder().WithRuntimeObjects(testMCP("worker", "False")).Build(), nil, nil)
	desired, live := testMachineConfigs()
	live.SetAnnotations(map[string]string{MachineConfigCoalescingBypassAnnotation: "yes-please"})
	apply, err := p.coalesceMachineConfigUpdate(ctx, desired, live, testHash(t, desired), testRenderContext())
	if err != nil || apply {
		t.Fatalf("unparseable bypass: apply=%t err=%v, want false nil", apply, err)
	}
}

func TestMachineConfigBypassAcceptsBooleanSpellings(t *testing.T) {
	for _, value := range []string{"true", "True", "TRUE", "1"} {
		ctx := context.Background()
		p := NewPatcher(fake.NewClientBuilder().WithRuntimeObjects(testMCP("worker", "False")).Build(), nil, nil)
		desired, live := testMachineConfigs()
		live.SetAnnotations(map[string]string{MachineConfigCoalescingBypassAnnotation: value})
		apply, err := p.coalesceMachineConfigUpdate(ctx, desired, live, testHash(t, desired), testRenderContext())
		if err != nil || !apply {
			t.Errorf("bypass %q: apply=%t err=%v, want true nil", value, apply, err)
		}
	}
}

func TestMachineConfigCreationIsNotStaged(t *testing.T) {
	p := NewPatcher(fake.NewClientBuilder().Build(), nil, nil)
	desired, _ := testMachineConfigs()
	apply, err := p.coalesceMachineConfigUpdate(context.Background(), desired, nil, "", testRenderContext())
	if err != nil || !apply {
		t.Fatalf("creation: apply=%t err=%v, want true nil", apply, err)
	}
}

// Degraded must not veto the release. A pool that is degraded *and* updating is
// already disrupting nodes, and Degraded flaps whenever one node fails mid-rollout;
// vetoing on it would skip exactly the window worth joining.
func TestMachineConfigUpdateReleasesIntoDegradedUpdatingPool(t *testing.T) {
	pool := testMCPConditions("worker", map[string]string{"Updating": "True", "Degraded": "True"})
	p := NewPatcher(fake.NewClientBuilder().WithRuntimeObjects(pool).Build(), nil, nil)
	desired, live := testMachineConfigs()
	apply, err := p.coalesceMachineConfigUpdate(context.Background(), desired, live, testHash(t, desired), testRenderContext())
	if err != nil || !apply {
		t.Fatalf("degraded updating MCP: apply=%t err=%v, want true nil", apply, err)
	}
}

// A degraded pool that is not updating is no different from any other stable pool:
// nothing is rolling out, so there is nothing to coalesce into and the update waits.
func TestMachineConfigUpdateStagesForDegradedStablePool(t *testing.T) {
	pool := testMCPConditions("worker", map[string]string{"Updating": "False", "Degraded": "True"})
	p := NewPatcher(fake.NewClientBuilder().WithRuntimeObjects(pool).Build(), nil, nil)
	desired, live := testMachineConfigs()
	apply, err := p.coalesceMachineConfigUpdate(context.Background(), desired, live, testHash(t, desired), testRenderContext())
	if err != nil || apply {
		t.Fatalf("degraded stable MCP: apply=%t err=%v, want false nil", apply, err)
	}
}

// RenderDegraded means MCO cannot render the pool at all, so an apply cannot start a
// rollout or reboot a node. Holding the update would be an indefinite hold with no
// upside, and the staged update may be the change that fixes the render.
func TestMachineConfigUpdateReleasesIntoRenderDegradedPool(t *testing.T) {
	pool := testMCPConditions("worker", map[string]string{"Updating": "False", "RenderDegraded": "True"})
	p := NewPatcher(fake.NewClientBuilder().WithRuntimeObjects(pool).Build(), nil, nil)
	desired, live := testMachineConfigs()
	apply, err := p.coalesceMachineConfigUpdate(context.Background(), desired, live, testHash(t, desired), testRenderContext())
	if err != nil || !apply {
		t.Fatalf("render-degraded MCP: apply=%t err=%v, want true nil", apply, err)
	}
}

func TestMachineConfigLabelChangeIncludesLivePool(t *testing.T) {
	p := NewPatcher(fake.NewClientBuilder().WithRuntimeObjects(testMCP("worker", "True")).Build(), nil, nil)
	desired, live := testMachineConfigs()
	desired.SetLabels(map[string]string{"machineconfiguration.openshift.io/role": "infra"})
	apply, err := p.coalesceMachineConfigUpdate(context.Background(), desired, live, testHash(t, desired), testRenderContext())
	if err != nil || !apply {
		t.Fatalf("live selected updating MCP: apply=%t err=%v, want true nil", apply, err)
	}
}

// One MachineConfig can be selected by several pools. The staged entry records
// all of them, and the first pool to enter a release-eligible state releases it —
// waiting for every matching pool to overlap could wait forever.
func TestMachineConfigReleasesOnFirstUpdatingPool(t *testing.T) {
	ctx := context.Background()
	p := NewPatcher(fake.NewClientBuilder().WithRuntimeObjects(testMCP("worker", "False"), testMCP("infra", "False")).Build(), nil, nil)
	desired, live := testMachineConfigs()

	apply, err := p.coalesceMachineConfigUpdate(ctx, desired, live, testHash(t, desired), testRenderContext())
	if err != nil || apply {
		t.Fatalf("two stable MCPs: apply=%t err=%v, want false nil", apply, err)
	}
	stage, err := p.machineConfigStage(ctx, "openshift-cnv", desired.GetName())
	if err != nil || stage == nil {
		t.Fatalf("stage was not persisted: %v", err)
	}
	if want := []string{"infra", "worker"}; !slices.Equal(stage.MatchingPools, want) {
		t.Fatalf("stage.MatchingPools = %v, want %v", stage.MatchingPools, want)
	}

	setPoolUpdating(t, p.client, "infra")
	apply, err = p.coalesceMachineConfigUpdate(ctx, desired, live, testHash(t, desired), testRenderContext())
	if err != nil || !apply {
		t.Fatalf("one of two MCPs updating: apply=%t err=%v, want true nil", apply, err)
	}
}

func TestMachineConfigWithoutMatchingPoolIsNotStaged(t *testing.T) {
	pool := testMCP("worker", "False")
	_ = unstructured.SetNestedStringMap(pool.Object, map[string]string{"machineconfiguration.openshift.io/role": "master"}, "spec", "machineConfigSelector", "matchLabels")
	p := NewPatcher(fake.NewClientBuilder().WithRuntimeObjects(pool).Build(), nil, nil)
	desired, live := testMachineConfigs()
	apply, err := p.coalesceMachineConfigUpdate(context.Background(), desired, live, testHash(t, desired), testRenderContext())
	if err != nil || !apply {
		t.Fatalf("no matching MCP: apply=%t err=%v, want true nil", apply, err)
	}
}

// An unreadable selector must fail the reconcile. Skipping the pool can release an
// update early, and an empty fallback selector would match every MachineConfig.
func TestMachineConfigUnreadableSelectorFailsReconcile(t *testing.T) {
	for name, selector := range map[string]map[string]any{
		"matchLabels":      {"matchLabels": map[string]any{"role": int64(5)}},
		"matchExpressions": {"matchExpressions": []any{"not-a-requirement"}},
	} {
		t.Run(name, func(t *testing.T) {
			pool := testMCP("worker", "False")
			_ = unstructured.SetNestedMap(pool.Object, selector, "spec", "machineConfigSelector")
			p := NewPatcher(fake.NewClientBuilder().WithRuntimeObjects(pool).Build(), nil, nil)
			desired, live := testMachineConfigs()
			if _, err := p.coalesceMachineConfigUpdate(context.Background(), desired, live, testHash(t, desired), testRenderContext()); err == nil {
				t.Fatal("expected an error for an unreadable machineConfigSelector")
			}
		})
	}
}

func TestMachineConfigPoolRolloutConditionsIgnoresUnrelatedChurn(t *testing.T) {
	pool := testMCP("worker", "False")
	before := MachineConfigPoolRolloutConditions(pool)
	_ = unstructured.SetNestedField(pool.Object, int64(7), "status", "updatedMachineCount")
	if after := MachineConfigPoolRolloutConditions(pool); len(after) != len(before) || after["Updating"] != before["Updating"] {
		t.Errorf("rollout conditions = %v after unrelated status churn, want %v", after, before)
	}
	_ = unstructured.SetNestedSlice(pool.Object, []any{
		map[string]any{"type": "Updating", "status": "True"},
	}, "status", "conditions")
	if after := MachineConfigPoolRolloutConditions(pool); after["Updating"] != "True" {
		t.Errorf("rollout conditions = %v after an Updating transition, want Updating=True", after)
	}
}

type countingReader struct {
	client.Reader
	gets int
}

func (r *countingReader) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	r.gets++
	return r.Reader.Get(ctx, key, obj, opts...)
}

func testHash(t *testing.T, obj *unstructured.Unstructured) string {
	t.Helper()
	hash, err := machineConfigDesiredHash(obj)
	if err != nil {
		t.Fatal(err)
	}
	return hash
}

func setPoolUpdating(t *testing.T, c client.Client, name string) {
	t.Helper()
	pool := &unstructured.Unstructured{}
	pool.SetGroupVersionKind(machineConfigPoolGVK)
	if err := c.Get(context.Background(), client.ObjectKey{Name: name}, pool); err != nil {
		t.Fatal(err)
	}
	if err := unstructured.SetNestedSlice(pool.Object, []any{
		map[string]any{"type": "Updating", "status": "True"},
	}, "status", "conditions"); err != nil {
		t.Fatal(err)
	}
	if err := c.Update(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
}

func testMachineConfigs() (*unstructured.Unstructured, *unstructured.Unstructured) {
	obj := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "machineconfiguration.openshift.io/v1", "kind": "MachineConfig", "metadata": map[string]any{"name": "99-test", "labels": map[string]any{"machineconfiguration.openshift.io/role": "worker"}}, "spec": map[string]any{"config": map[string]any{"ignition": map[string]any{"version": "3.5.0"}}}}}
	return obj.DeepCopy(), obj.DeepCopy()
}

func testMCP(name, updating string) *unstructured.Unstructured {
	return testMCPConditions(name, map[string]string{"Updating": updating})
}

func testMCPConditions(name string, conditions map[string]string) *unstructured.Unstructured {
	status := make([]any, 0, len(conditions))
	for _, conditionType := range slices.Sorted(maps.Keys(conditions)) {
		status = append(status, map[string]any{"type": conditionType, "status": conditions[conditionType]})
	}
	obj := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "machineconfiguration.openshift.io/v1", "kind": "MachineConfigPool", "metadata": map[string]any{"name": name}, "spec": map[string]any{"machineConfigSelector": map[string]any{"matchLabels": map[string]any{"machineconfiguration.openshift.io/role": "worker"}}}, "status": map[string]any{"conditions": status}}}
	obj.SetGroupVersionKind(schema.GroupVersionKind{Group: "machineconfiguration.openshift.io", Version: "v1", Kind: "MachineConfigPool"})
	return obj
}

func testRenderContext() *pkgcontext.RenderContext {
	hco := &unstructured.Unstructured{}
	hco.SetAPIVersion("hco.kubevirt.io/v1beta1")
	hco.SetKind("HyperConverged")
	hco.SetUID("test-hco")
	hco.SetNamespace("openshift-cnv")
	return &pkgcontext.RenderContext{HCO: hco}
}
