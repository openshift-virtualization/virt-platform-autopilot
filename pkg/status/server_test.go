package status

import (
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/kubevirt/virt-platform-autopilot/pkg/assets"
)

type cacheRestrictedClient struct{ client.Client }

func (c cacheRestrictedClient) Get(context.Context, client.ObjectKey, client.Object, ...client.GetOption) error {
	return fmt.Errorf("unknown namespace for the cache")
}

func TestStoreRetainsLastDriftAfterSync(t *testing.T) {
	store := NewStore()
	obj := &unstructured.Unstructured{}
	obj.SetAPIVersion("v1")
	obj.SetKind("ConfigMap")
	obj.SetName("example")
	store.ObserveAsset("", obj, "drifted", "detected")
	store.ObserveAsset("", obj, "synced", "")
	assets := store.response().Assets
	if len(assets) != 1 || assets[0].State != "synced" || assets[0].LastDriftTime == nil {
		t.Fatalf("unexpected status snapshot: %#v", assets)
	}
}

func TestStoreLinksObservedAssetToGoldenConfiguration(t *testing.T) {
	store := NewStore()
	obj := &unstructured.Unstructured{}
	obj.SetKind("ConfigMap")
	obj.SetNamespace("openshift-cnv")
	obj.SetName("example")
	store.SetGolden("example-asset", obj)
	store.ObserveAsset("", obj, "synced", "")
	got := store.response().Assets
	if len(got) != 1 || got[0].ID != "" || got[0].GoldenKey != "" {
		t.Fatalf("unattributed observed asset must not be linked to golden configuration: %#v", got)
	}
	store.ObserveAsset("example-asset", obj, "synced", "")
	got = store.response().Assets
	if len(got) != 2 || got[1].ID != "example-asset" || got[1].GoldenKey != "example-asset.json" {
		t.Fatalf("observed asset was not linked to golden configuration: %#v", got)
	}
}

func TestStoreClearsEffectiveDesiredWithoutClearingGoldenConfiguration(t *testing.T) {
	store := NewStore()
	obj := &unstructured.Unstructured{}
	obj.SetKind("ConfigMap")
	obj.SetName("example")
	store.SetDesired("example-asset", obj)
	store.SetGolden("example-asset", obj)
	store.ObserveAsset("example-asset", obj, "synced", "")
	store.ClearDesired("example-asset")
	if store.response().Assets[0].GoldenKey != "example-asset.json" {
		t.Fatalf("expected golden configuration to remain available: %#v", store.response().Assets)
	}
}

func TestSharedAssetPublishesSidecarMetadataAndGoldenContent(t *testing.T) {
	store := NewStore()
	target := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "operator.openshift.io/v1", "kind": "MachineConfiguration", "metadata": map[string]any{"name": "cluster"}}}
	sidecar := target.DeepCopy()
	sidecar.Object["spec"] = map[string]any{"nodeDisruptionPolicy": map[string]any{"units": []any{}}}
	store.SetGolden("policy", sidecar)
	store.ObserveSharedAsset("policy", target, "synced", "", "spec.nodeDisruptionPolicy")
	asset := store.response().Assets[0]
	if !asset.SharedObject || asset.SharedPath != "spec.nodeDisruptionPolicy" || asset.GoldenKey != "policy.json" {
		t.Fatalf("shared sidecar was not described correctly: %#v", asset)
	}
}

func TestActivationPlanIsSelfDescribingMergePatch(t *testing.T) {
	optIn := "platform.kubevirt.io/enable-example=true"
	hco := &unstructured.Unstructured{}
	hco.SetNamespace("openshift-cnv")
	hco.SetName("kubevirt-hyperconverged")
	plan := activationPlan(assets.FeatureStatus{OptIn: &optIn}, hco)
	if plan == nil || plan.Target.Group != "hco.kubevirt.io" || plan.Target.Namespace != "openshift-cnv" || plan.Target.Name != "kubevirt-hyperconverged" || plan.Method != "PATCH" || plan.PatchType != "application/merge-patch+json" {
		t.Fatalf("unexpected activation plan: %#v", plan)
	}
	metadata := plan.Patch["metadata"].(map[string]any)
	annotations := metadata["annotations"].(map[string]string)
	if annotations["platform.kubevirt.io/enable-example"] != "true" {
		t.Fatalf("unexpected activation patch: %#v", plan.Patch)
	}
}

func TestActivationPlanRejectsMultipleOrNonEnableAnnotations(t *testing.T) {
	hco := &unstructured.Unstructured{}
	hco.SetName("kubevirt-hyperconverged")
	for _, optIn := range []string{
		"platform.kubevirt.io/enable-thp-tuning=true, platform.kubevirt.io/enable-thp-tuning-kernelcore=true",
		"platform.kubevirt.io/openshift=true",
	} {
		if plan := activationPlan(assets.FeatureStatus{OptIn: &optIn}, hco); plan != nil {
			t.Fatalf("activation plan must not infer broad or environment activation: %#v", plan)
		}
	}
}

func TestStoreKeepsDistinctAssetsForSharedObject(t *testing.T) {
	store := NewStore()
	obj := &unstructured.Unstructured{}
	obj.SetAPIVersion("operator.openshift.io/v1")
	obj.SetKind("MachineConfiguration")
	obj.SetName("cluster")
	store.ObserveAsset("cpu-policy", obj, "synced", "")
	store.ObserveAsset("ksm-policy", obj, "synced", "")
	if got := store.response().Assets; len(got) != 2 || got[0].ID == got[1].ID {
		t.Fatalf("shared object observations were collapsed: %#v", got)
	}
}

func TestPublishWritesSummaryAndGoldenConfigMaps(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	store := NewStore()
	object := &unstructured.Unstructured{}
	object.SetAPIVersion("v1")
	object.SetKind("ConfigMap")
	object.SetNamespace("openshift-cnv")
	object.SetName("example")
	store.SetGolden("example-asset", object)
	secret := &unstructured.Unstructured{}
	secret.SetAPIVersion("v1")
	secret.SetKind("Secret")
	secret.SetNamespace("openshift-cnv")
	secret.SetName("must-not-publish")
	store.SetGolden("sensitive-asset", secret)
	store.ObserveAsset("example-asset", object, "excluded", "rootExclusion")
	store.MarkReconciled()
	testClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	if err := store.Publish(context.Background(), testClient, testClient, "openshift-cnv"); err != nil {
		t.Fatalf("publish inspection: %v", err)
	}
	summary := &corev1.ConfigMap{}
	if err := testClient.Get(context.Background(), client.ObjectKey{Namespace: "openshift-cnv", Name: SummaryConfigMapName}, summary); err != nil {
		t.Fatalf("get summary: %v", err)
	}
	var response Response
	if err := json.Unmarshal([]byte(summary.Data["snapshot.json"]), &response); err != nil {
		t.Fatalf("decode summary: %v", err)
	}
	if len(response.Assets) != 1 || response.Assets[0].GoldenKey != "example-asset.json" {
		t.Fatalf("unexpected published summary: %#v", response)
	}
	golden := &corev1.ConfigMap{}
	if err := testClient.Get(context.Background(), client.ObjectKey{Namespace: "openshift-cnv", Name: GoldenConfigMapName}, golden); err != nil {
		t.Fatalf("get golden: %v", err)
	}
	if _, ok := golden.Data["example-asset.json"]; !ok {
		t.Fatalf("golden data does not contain the asset: %#v", golden.Data)
	}
	if _, ok := golden.Data["sensitive-asset.json"]; ok {
		t.Fatalf("golden data must not contain a Secret: %#v", golden.Data)
	}
	var index GoldenIndex
	if err := json.Unmarshal([]byte(summary.Data["index.json"]), &index); err != nil {
		t.Fatalf("decode golden index: %v", err)
	}
	if index.Generation != response.Generation {
		t.Fatalf("generation mismatch: summary=%d index=%d", response.Generation, index.Generation)
	}
	if got := index.Assets["example-asset"]; got.RenderMode != "preview" || got.Reason != "rootExclusion" {
		t.Fatalf("unexpected golden metadata: %#v", got)
	}
}

func TestPublishMarksOversizedGoldenObjectWithoutFailing(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	store := NewStore()
	object := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata":   map[string]any{"name": "large"},
		"data":       map[string]any{"payload": strings.Repeat("x", maxInspectionConfigMapDataBytes)},
	}}
	store.SetGolden("large-asset", object)
	store.ObserveAsset("large-asset", object, "synced", "")
	testClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	if err := store.Publish(context.Background(), testClient, testClient, "openshift-cnv"); err != nil {
		t.Fatalf("publish oversized golden object: %v", err)
	}
	golden := &corev1.ConfigMap{}
	if err := testClient.Get(context.Background(), client.ObjectKey{Namespace: "openshift-cnv", Name: GoldenConfigMapName}, golden); err != nil {
		t.Fatalf("get golden: %v", err)
	}
	if _, ok := golden.Data["large-asset.json"]; ok {
		t.Fatalf("oversized object was published: %#v", golden.Data)
	}
	summary := &corev1.ConfigMap{}
	if err := testClient.Get(context.Background(), client.ObjectKey{Namespace: "openshift-cnv", Name: SummaryConfigMapName}, summary); err != nil {
		t.Fatalf("get summary: %v", err)
	}
	var response Response
	if err := json.Unmarshal([]byte(summary.Data["snapshot.json"]), &response); err != nil {
		t.Fatalf("decode summary: %v", err)
	}
	if response.Assets[0].GoldenKey != "" {
		t.Fatalf("oversized asset advertises missing golden content: %#v", response.Assets[0])
	}
	var index GoldenIndex
	if err := json.Unmarshal([]byte(summary.Data["index.json"]), &index); err != nil {
		t.Fatalf("decode golden index: %v", err)
	}
	if got := index.Assets["large-asset"]; got.RenderMode != "tooLarge" || got.Reason != "sizeLimit" {
		t.Fatalf("unexpected oversized metadata: %#v", got)
	}
}

func TestSummaryDataDegradesGraduallyWhenOversized(t *testing.T) {
	large := strings.Repeat("x", maxInspectionConfigMapDataBytes/2)
	response := Response{
		APIVersion: "autopilot.kubevirt.io/status/v1beta1", Generation: 1, State: "ready", Ready: true,
		Features: []Feature{{FeatureStatus: assets.FeatureStatus{ID: "feature", Name: "Feature", Description: large, Maturity: "GA", Install: "always", Assets: []string{"asset"}}, Enabled: true, Availability: "healthy", Activation: &ActivationPlan{Patch: map[string]any{"payload": large}}, Evidence: []FeatureEvidence{{AssetID: "asset", State: "synced", Reason: large}}}},
		Assets:   []Asset{{ID: "asset-a", APIVersion: "v1", Kind: "ConfigMap", Name: large, State: "synced"}, {ID: "asset-b", APIVersion: "v1", Kind: "ConfigMap", Name: large, State: "synced"}},
	}
	data, err := summaryData("openshift-cnv", response, GoldenIndex{Generation: 1, Assets: map[string]GoldenMetadata{}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var published Response
	if err := json.Unmarshal([]byte(data["snapshot.json"]), &published); err != nil {
		t.Fatal(err)
	}
	if published.State != "ready" || !published.Truncated || published.TruncationReason != "sizeLimit" || len(published.Features) != 1 {
		t.Fatalf("oversized summary lost useful discovery data: %#v", published)
	}
}

func TestPublishHidesSecretGoldenReference(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	store := NewStore()
	secret := &unstructured.Unstructured{}
	secret.SetAPIVersion("v1")
	secret.SetKind("Secret")
	secret.SetName("must-not-publish")
	store.SetGolden("sensitive-asset", secret)
	store.ObserveAsset("sensitive-asset", secret, "synced", "")
	testClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	if err := store.Publish(context.Background(), testClient, testClient, "openshift-cnv"); err != nil {
		t.Fatalf("publish Secret inspection: %v", err)
	}
	summary := &corev1.ConfigMap{}
	if err := testClient.Get(context.Background(), client.ObjectKey{Namespace: "openshift-cnv", Name: SummaryConfigMapName}, summary); err != nil {
		t.Fatalf("get summary: %v", err)
	}
	var response Response
	if err := json.Unmarshal([]byte(summary.Data["snapshot.json"]), &response); err != nil {
		t.Fatalf("decode summary: %v", err)
	}
	if response.Assets[0].GoldenKey != "" {
		t.Fatalf("Secret advertises missing golden content: %#v", response.Assets[0])
	}
	golden := &corev1.ConfigMap{}
	if err := testClient.Get(context.Background(), client.ObjectKey{Namespace: "openshift-cnv", Name: GoldenConfigMapName}, golden); err != nil {
		t.Fatalf("get golden: %v", err)
	}
	var index GoldenIndex
	if err := json.Unmarshal([]byte(summary.Data["index.json"]), &index); err != nil {
		t.Fatalf("decode golden index: %v", err)
	}
	if got := index.Assets["sensitive-asset"]; got.RenderMode != "notExposed" || got.Reason != "sensitiveContent" {
		t.Fatalf("unexpected Secret metadata: %#v", got)
	}
}

func TestPublishGenerationContinuesAfterStoreRestart(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	testClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	first := NewStore()
	if err := first.Publish(context.Background(), testClient, testClient, "openshift-cnv"); err != nil {
		t.Fatalf("first publish: %v", err)
	}
	second := NewStore()
	second.MarkFailed()
	if err := second.Publish(context.Background(), testClient, testClient, "openshift-cnv"); err != nil {
		t.Fatalf("second publish: %v", err)
	}
	summary := &corev1.ConfigMap{}
	if err := testClient.Get(context.Background(), client.ObjectKey{Namespace: "openshift-cnv", Name: SummaryConfigMapName}, summary); err != nil {
		t.Fatalf("get summary: %v", err)
	}
	var response Response
	if err := json.Unmarshal([]byte(summary.Data["snapshot.json"]), &response); err != nil {
		t.Fatalf("decode summary: %v", err)
	}
	if response.Generation != 2 {
		t.Fatalf("generation = %d, want 2", response.Generation)
	}
}

func TestPublishRefreshesLivenessAtHeartbeatInterval(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	testClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	store := NewStore()
	store.MarkReconciled()
	if err := store.Publish(context.Background(), testClient, testClient, "openshift-cnv"); err != nil {
		t.Fatalf("first publish: %v", err)
	}

	// A new successful attempt updates in-memory freshness, but does not write
	// before the heartbeat is due.
	store.MarkReconciled()
	if err := store.Publish(context.Background(), testClient, testClient, "openshift-cnv"); err != nil {
		t.Fatalf("unchanged publish: %v", err)
	}
	summary := &corev1.ConfigMap{}
	if err := testClient.Get(context.Background(), client.ObjectKey{Namespace: "openshift-cnv", Name: SummaryConfigMapName}, summary); err != nil {
		t.Fatalf("get summary: %v", err)
	}
	var response Response
	if err := json.Unmarshal([]byte(summary.Data["snapshot.json"]), &response); err != nil {
		t.Fatalf("decode summary: %v", err)
	}
	if response.Generation != 1 {
		t.Fatalf("generation before heartbeat = %d, want 1", response.Generation)
	}
	golden := &corev1.ConfigMap{}
	if err := testClient.Get(context.Background(), client.ObjectKey{Namespace: "openshift-cnv", Name: GoldenConfigMapName}, golden); err != nil {
		t.Fatalf("get golden before heartbeat: %v", err)
	}
	goldenBefore := maps.Clone(golden.Data)

	old := time.Now().UTC().Add(-inspectionHeartbeatInterval - time.Second)
	oldPublished := old
	response.ObservedAt, response.LastSuccessfulReconcile = &old, &old
	raw, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	summary.Data["snapshot.json"] = string(raw)
	if err := testClient.Update(context.Background(), summary); err != nil {
		t.Fatalf("age summary: %v", err)
	}
	if err := store.Publish(context.Background(), testClient, testClient, "openshift-cnv"); err != nil {
		t.Fatalf("heartbeat publish: %v", err)
	}
	if err := testClient.Get(context.Background(), client.ObjectKey{Namespace: "openshift-cnv", Name: SummaryConfigMapName}, summary); err != nil {
		t.Fatalf("get heartbeated summary: %v", err)
	}
	if err := json.Unmarshal([]byte(summary.Data["snapshot.json"]), &response); err != nil {
		t.Fatalf("decode heartbeated summary: %v", err)
	}
	if response.Generation != 1 || response.ObservedAt == nil || !response.ObservedAt.After(oldPublished) {
		t.Fatalf("heartbeat did not refresh liveness (old=%s): %#v", oldPublished, response)
	}
	if err := testClient.Get(context.Background(), client.ObjectKey{Namespace: "openshift-cnv", Name: GoldenConfigMapName}, golden); err != nil {
		t.Fatalf("get golden after heartbeat: %v", err)
	}
	if !reflect.DeepEqual(goldenBefore, golden.Data) {
		t.Fatalf("heartbeat rewrote golden data: before=%#v after=%#v", goldenBefore, golden.Data)
	}
}

func TestFeatureEvidenceIsNeverNull(t *testing.T) {
	store := NewStore()
	store.features = []Feature{{FeatureStatus: assets.FeatureStatus{Name: "Disabled"}, Enabled: false}}
	payload, err := json.Marshal(store.response())
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Features []struct {
			Evidence []FeatureEvidence `json:"evidence"`
		} `json:"features"`
	}
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Features) != 1 || decoded.Features[0].Evidence == nil {
		t.Fatalf("disabled feature has null evidence: %s", payload)
	}
}

func TestMarkFailedRefreshesLivenessTimestamp(t *testing.T) {
	store := NewStore()
	old := time.Now().UTC().Add(-time.Hour)
	store.lastFailedReconcile = &old
	store.state = "error"
	store.MarkFailed()
	response := store.response()
	if response.LastFailedReconcile == nil || !response.LastFailedReconcile.After(old) || response.ObservedAt == nil || !response.ObservedAt.After(old) {
		t.Fatalf("failed reconciliation did not refresh liveness: %#v", response)
	}
}

func TestPublishLabelsAndOwnsInspectionConfigMaps(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	store := NewStore()
	owner := &unstructured.Unstructured{}
	owner.SetAPIVersion("hco.kubevirt.io/v1beta1")
	owner.SetKind("HyperConverged")
	owner.SetNamespace("openshift-cnv")
	owner.SetName("kubevirt-hyperconverged")
	owner.SetUID("test-hco")
	store.SetOwner(owner)
	testClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	if err := store.Publish(context.Background(), testClient, testClient, "openshift-cnv"); err != nil {
		t.Fatalf("publish inspection: %v", err)
	}
	for _, name := range []string{SummaryConfigMapName, GoldenConfigMapName} {
		cm := &corev1.ConfigMap{}
		if err := testClient.Get(context.Background(), client.ObjectKey{Namespace: "openshift-cnv", Name: name}, cm); err != nil {
			t.Fatalf("get %s: %v", name, err)
		}
		if cm.Labels[ManagedByLabel] != ManagedByValue || cm.Labels[InspectionLabel] != "true" {
			t.Fatalf("inspection metadata missing from %s: %#v", name, cm.Labels)
		}
		if len(cm.OwnerReferences) != 1 || cm.OwnerReferences[0].UID != "test-hco" {
			t.Fatalf("inspection owner missing from %s: %#v", name, cm.OwnerReferences)
		}
	}
}

func TestPublishAdvancesPastGoldenOnlyWrite(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	// This represents a process crash after golden content is written but
	// before its matching summary is persisted.
	testClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: GoldenConfigMapName, Namespace: "openshift-cnv"},
		Data:       map[string]string{GoldenGenerationKey: "5"},
	}).Build()
	store := NewStore()
	store.MarkFailed()
	if err := store.Publish(context.Background(), testClient, testClient, "openshift-cnv"); err != nil {
		t.Fatalf("publish after golden-only write: %v", err)
	}
	summary := &corev1.ConfigMap{}
	if err := testClient.Get(context.Background(), client.ObjectKey{Namespace: "openshift-cnv", Name: SummaryConfigMapName}, summary); err != nil {
		t.Fatalf("get summary: %v", err)
	}
	var response Response
	if err := json.Unmarshal([]byte(summary.Data["snapshot.json"]), &response); err != nil {
		t.Fatalf("decode summary: %v", err)
	}
	if response.Generation != 6 {
		t.Fatalf("generation = %d, want 6", response.Generation)
	}
}

func TestPublishReadsInspectionConfigMapsDirectly(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	direct := fake.NewClientBuilder().WithScheme(scheme).Build()
	store := NewStore()
	if err := store.Publish(context.Background(), cacheRestrictedClient{direct}, direct, "openshift-cnv"); err != nil {
		t.Fatalf("publish using direct reader: %v", err)
	}
	cm := &corev1.ConfigMap{}
	if err := direct.Get(context.Background(), client.ObjectKey{Namespace: "openshift-cnv", Name: SummaryConfigMapName}, cm); err != nil {
		t.Fatalf("summary was not created: %v", err)
	}
}

func TestPublishRecoversFromMalformedPersistedSnapshot(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	direct := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: SummaryConfigMapName, Namespace: "openshift-cnv"},
		Data:       map[string]string{"snapshot.json": "{"},
	}).Build()
	store := NewStore()
	if err := store.Publish(context.Background(), direct, direct, "openshift-cnv"); err != nil {
		t.Fatalf("publish after malformed snapshot: %v", err)
	}
}

func TestAssetObservationReplacesUnavailablePlaceholder(t *testing.T) {
	store := NewStore()
	store.MarkAssetUnavailable("example", "missingCRD")
	object := &unstructured.Unstructured{}
	object.SetAPIVersion("v1")
	object.SetKind("ConfigMap")
	object.SetName("example")
	store.ObserveAsset("example", object, "synced", "")
	assets := store.response().Assets
	if len(assets) != 1 || assets[0].State != "synced" {
		t.Fatalf("unavailable placeholder was not replaced: %#v", assets)
	}
}

func TestSetDesiredDoesNotCrossStampDifferentAPIVersions(t *testing.T) {
	store := NewStore()
	old := &unstructured.Unstructured{}
	old.SetAPIVersion("old.example.io/v1")
	old.SetKind("Example")
	old.SetNamespace("openshift-cnv")
	old.SetName("shared")
	store.ObserveAsset("old", old, "synced", "")
	current := old.DeepCopy()
	current.SetAPIVersion("new.example.io/v1")
	store.SetDesired("current", current)
	if got := store.response().Assets; len(got) != 1 || got[0].ID != "old" {
		t.Fatalf("asset identity crossed API versions: %#v", got)
	}
}

func TestInspectionSchemaMatchesPublishedEnvelope(t *testing.T) {
	rawSchema, err := os.ReadFile("../../docs/console-inspection-v1beta1.schema.json")
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	var document any
	if err := json.Unmarshal(rawSchema, &document); err != nil {
		t.Fatalf("decode schema: %v", err)
	}
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat()
	const schemaURL = "https://kubevirt.io/virt-platform-autopilot/console-inspection-v1beta1.schema.json"
	if err := compiler.AddResource(schemaURL, document); err != nil {
		t.Fatalf("register schema: %v", err)
	}
	snapshotSchema, err := compiler.Compile(schemaURL)
	if err != nil {
		t.Fatalf("compile snapshot schema: %v", err)
	}
	indexSchema, err := compiler.Compile(schemaURL + "#/$defs/goldenIndex")
	if err != nil {
		t.Fatalf("compile golden index schema: %v", err)
	}

	now := time.Now().UTC()
	response := Response{
		APIVersion: "autopilot.kubevirt.io/status/v1beta1", Generation: 7, State: "ready", Ready: true, ObservedAt: &now, LastSuccessfulReconcile: &now,
		Features: []Feature{{
			FeatureStatus: assets.FeatureStatus{ID: "cpu-manager", Name: "CPU Manager", Description: "Pins virtual machine CPUs", Maturity: "GA", Install: "always", Assets: []string{"cpu-manager"}},
			Enabled:       true, Availability: "healthy", Evidence: []FeatureEvidence{{AssetID: "cpu-manager", State: "synced"}},
			Activation: &ActivationPlan{Target: ResourceRef{Group: "hco.kubevirt.io", Version: "v1beta1", Resource: "hyperconvergeds", Kind: "HyperConverged", Namespace: "openshift-cnv", Name: "kubevirt-hyperconverged"}, Method: "PATCH", PatchType: "application/merge-patch+json", Patch: map[string]any{"metadata": map[string]any{"annotations": map[string]any{"platform.kubevirt.io/enable-cpu-manager": "true"}}}},
		}},
		Assets: []Asset{{ID: "cpu-manager", APIVersion: "machineconfiguration.openshift.io/v1", Kind: "MachineConfig", Name: "99-virt-cpu-manager", State: "synced", LastTransitionTime: now, GoldenKey: "cpu-manager.json"}},
	}
	payload, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	var object map[string]any
	if err := json.Unmarshal(payload, &object); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if err := snapshotSchema.Validate(object); err != nil {
		t.Fatalf("published snapshot violates schema: %v", err)
	}
	indexPayload, err := json.Marshal(GoldenIndex{Generation: 7, Assets: map[string]GoldenMetadata{"cpu-manager": {RenderMode: "applied"}}})
	if err != nil {
		t.Fatal(err)
	}
	var index map[string]any
	if err := json.Unmarshal(indexPayload, &index); err != nil {
		t.Fatal(err)
	}
	if err := indexSchema.Validate(index); err != nil {
		t.Fatalf("published golden index violates schema: %v", err)
	}
	asset := object["assets"].([]any)[0].(map[string]any)
	evidence := object["features"].([]any)[0].(map[string]any)["evidence"].([]any)[0].(map[string]any)
	indexAsset := index["assets"].(map[string]any)["cpu-manager"].(map[string]any)
	for _, reason := range AssetReasonCodes {
		asset["reason"], evidence["reason"], indexAsset["reason"] = reason, reason, reason
		if err := snapshotSchema.Validate(object); err != nil {
			t.Fatalf("schema omits emitted asset reason %q: %v", reason, err)
		}
		if err := indexSchema.Validate(index); err != nil {
			t.Fatalf("index schema omits emitted asset reason %q: %v", reason, err)
		}
	}

	// This specifically guards the common feature.evidence.map(...) consumer
	// path from regressing to JSON null.
	feature := object["features"].([]any)[0].(map[string]any)
	feature["evidence"] = nil
	if err := snapshotSchema.Validate(object); err == nil {
		t.Fatal("schema accepted null feature evidence")
	}
}

func TestAssetReasonCodesIncludeEveryDeclaredReasonConstant(t *testing.T) {
	file, err := parseStatusSource()
	if err != nil {
		t.Fatalf("parse status source: %v", err)
	}

	declared := declaredAssetReasonConstants(file)
	listed, err := listedAssetReasonConstants(file)
	if err != nil {
		t.Fatal(err)
	}
	if len(declared) == 0 {
		t.Fatal("no AssetReason constants found")
	}
	if !reflect.DeepEqual(declared, listed) {
		t.Fatalf("AssetReasonCodes drift: declared=%v listed=%v", declared, listed)
	}
}

func parseStatusSource() (*ast.File, error) {
	return parser.ParseFile(token.NewFileSet(), "server.go", nil, 0)
}

func declaredAssetReasonConstants(file *ast.File) map[string]bool {
	declared := map[string]bool{}
	for _, declaration := range file.Decls {
		general, ok := declaration.(*ast.GenDecl)
		if !ok || general.Tok != token.CONST {
			continue
		}
		for _, spec := range general.Specs {
			valueSpec, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for _, name := range valueSpec.Names {
				if strings.HasPrefix(name.Name, "AssetReason") {
					declared[name.Name] = true
				}
			}
		}
	}
	return declared
}

func listedAssetReasonConstants(file *ast.File) (map[string]bool, error) {
	for _, declaration := range file.Decls {
		general, ok := declaration.(*ast.GenDecl)
		if !ok || general.Tok != token.VAR {
			continue
		}
		for _, spec := range general.Specs {
			valueSpec, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			if len(valueSpec.Names) != 1 || valueSpec.Names[0].Name != "AssetReasonCodes" || len(valueSpec.Values) != 1 {
				continue
			}
			literal, ok := valueSpec.Values[0].(*ast.CompositeLit)
			if !ok {
				return nil, fmt.Errorf("AssetReasonCodes must be a literal list of named constants")
			}
			listed := map[string]bool{}
			for _, element := range literal.Elts {
				identifier, ok := element.(*ast.Ident)
				if !ok {
					return nil, fmt.Errorf("AssetReasonCodes entry %T is not a named constant", element)
				}
				listed[identifier.Name] = true
			}
			return listed, nil
		}
	}
	return nil, fmt.Errorf("AssetReasonCodes declaration not found")
}

func TestSetUnavailableClearsDurableInspectionContent(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	store := NewStore()
	object := &unstructured.Unstructured{}
	object.SetKind("ConfigMap")
	object.SetName("example")
	store.SetGolden("example-asset", object)
	store.ObserveAsset("example-asset", object, "synced", "")
	store.SetUnavailable("disabled", "autopilotOptOut")
	testClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	if err := store.Publish(context.Background(), testClient, testClient, "openshift-cnv"); err != nil {
		t.Fatalf("publish unavailable snapshot: %v", err)
	}
	summary := &corev1.ConfigMap{}
	if err := testClient.Get(context.Background(), client.ObjectKey{Namespace: "openshift-cnv", Name: SummaryConfigMapName}, summary); err != nil {
		t.Fatalf("get summary: %v", err)
	}
	var response Response
	if err := json.Unmarshal([]byte(summary.Data["snapshot.json"]), &response); err != nil {
		t.Fatalf("decode summary: %v", err)
	}
	if response.State != "disabled" || response.Reason != "autopilotOptOut" || response.Ready || len(response.Assets) != 0 {
		t.Fatalf("unexpected unavailable snapshot: %#v", response)
	}
	golden := &corev1.ConfigMap{}
	if err := testClient.Get(context.Background(), client.ObjectKey{Namespace: "openshift-cnv", Name: GoldenConfigMapName}, golden); err != nil {
		t.Fatalf("get golden: %v", err)
	}
	var index GoldenIndex
	if err := json.Unmarshal([]byte(summary.Data["index.json"]), &index); err != nil {
		t.Fatalf("decode golden index: %v", err)
	}
	if len(golden.Data) != 1 || golden.Data[GoldenGenerationKey] == "" || len(index.Assets) != 0 {
		t.Fatalf("golden content was not cleared: %#v", golden.Data)
	}
}

func TestMarkAssetUnavailableClearsRenderedData(t *testing.T) {
	store := NewStore()
	object := &unstructured.Unstructured{}
	object.SetKind("ConfigMap")
	object.SetName("example")
	store.SetGolden("example-asset", object)
	store.SetDesired("example-asset", object)
	store.ObserveAsset("example-asset", object, "synced", "")
	store.MarkAssetUnavailable("example-asset", "missingCRD")
	asset := store.response().Assets[0]
	if asset.State != "excluded" || asset.Reason != "missingCRD" || asset.GoldenKey != "" {
		t.Fatalf("unexpected unavailable asset: %#v", asset)
	}
}

func TestSetFeaturesPrunesAssetsNoLongerInCatalog(t *testing.T) {
	store := NewStore()
	object := &unstructured.Unstructured{}
	object.SetKind("ConfigMap")
	object.SetName("removed")
	store.SetGolden("removed-asset", object)
	store.SetDesired("removed-asset", object)
	store.ObserveAsset("removed-asset", object, "synced", "")
	catalog := &assets.FeatureCatalog{Features: []assets.FeatureStatus{{Name: "Current", Assets: []string{"current-asset"}}}}
	store.SetFeatures(catalog, nil, nil)
	if got := store.response().Assets; len(got) != 0 {
		t.Fatalf("removed catalog asset remains observed: %#v", got)
	}
	if _, ok := store.golden["removed-asset"]; ok {
		t.Fatal("removed catalog asset remains golden")
	}
}

func TestFeatureAvailabilityUsesAssetEvidence(t *testing.T) {
	store := NewStore()
	store.features = []Feature{
		{FeatureStatus: assets.FeatureStatus{Assets: []string{"healthy"}}, Enabled: true},
		{FeatureStatus: assets.FeatureStatus{Assets: []string{"excluded"}}, Enabled: true},
		{FeatureStatus: assets.FeatureStatus{Assets: []string{"staged"}}, Enabled: true},
		{FeatureStatus: assets.FeatureStatus{Assets: []string{"failed"}}, Enabled: true},
		{FeatureStatus: assets.FeatureStatus{Assets: []string{"unmanaged"}}, Enabled: true},
	}
	for id, state := range map[string]string{"healthy": "synced", "excluded": "excluded", "staged": "staged", "failed": "applyFailed", "unmanaged": "unmanaged"} {
		obj := &unstructured.Unstructured{}
		obj.SetKind("ConfigMap")
		obj.SetName(id)
		store.ObserveAsset(id, obj, state, "testReason")
	}
	got := store.response().Features
	want := []string{"healthy", "blocked", "pending", "degraded", "unmanaged"}
	for i, availability := range want {
		if got[i].Availability != availability || len(got[i].Evidence) != 1 || got[i].Evidence[0].AssetID == "" {
			t.Fatalf("feature %d = %#v, want availability %q with evidence", i, got[i], availability)
		}
	}
}

func TestFeatureAvailabilityReturnsAllEvidenceWhenDegraded(t *testing.T) {
	store := NewStore()
	store.features = []Feature{{FeatureStatus: assets.FeatureStatus{Assets: []string{"failed", "synced", "staged"}}, Enabled: true}}
	for id, state := range map[string]string{"failed": "applyFailed", "synced": "synced", "staged": "staged"} {
		obj := &unstructured.Unstructured{}
		obj.SetKind("ConfigMap")
		obj.SetName(id)
		store.ObserveAsset(id, obj, state, "test")
	}
	feature := store.response().Features[0]
	if feature.Availability != "degraded" || len(feature.Evidence) != 3 {
		t.Fatalf("unexpected degraded feature evidence: %#v", feature)
	}
}

func TestFeatureAvailabilityIsPartialWhenSyncedAssetsCoexistWithExcludedAssets(t *testing.T) {
	store := NewStore()
	store.features = []Feature{{FeatureStatus: assets.FeatureStatus{Assets: []string{"synced", "excluded"}}, Enabled: true}}
	for id, state := range map[string]string{"synced": "synced", "excluded": "excluded"} {
		obj := &unstructured.Unstructured{}
		obj.SetKind("ConfigMap")
		obj.SetName(id)
		store.ObserveAsset(id, obj, state, "missingCRD")
	}
	feature := store.response().Features[0]
	if feature.Availability != "partial" || feature.Reason != "someAssetsExcluded" || len(feature.Evidence) != 2 {
		t.Fatalf("mixed feature incorrectly reported as healthy: %#v", feature)
	}
}

func TestStoreReturnsAssetsInStableOrder(t *testing.T) {
	store := NewStore()
	for _, name := range []string{"z", "a"} {
		obj := &unstructured.Unstructured{}
		obj.SetKind("ConfigMap")
		obj.SetName(name)
		store.ObserveAsset("", obj, "synced", "")
	}
	assets := store.response().Assets
	if assets[0].Name != "a" || assets[1].Name != "z" {
		t.Fatalf("assets were not sorted: %#v", assets)
	}
}

func TestStoreOrdersSharedObjectsByAssetID(t *testing.T) {
	store := NewStore()
	obj := &unstructured.Unstructured{}
	obj.SetAPIVersion("operator.openshift.io/v1")
	obj.SetKind("MachineConfiguration")
	obj.SetName("cluster")
	store.ObserveAsset("z-policy", obj, "synced", "")
	store.ObserveAsset("a-policy", obj, "synced", "")
	assets := store.response().Assets
	if len(assets) != 2 || assets[0].ID != "a-policy" || assets[1].ID != "z-policy" {
		t.Fatalf("shared-object assets were not deterministically ordered: %#v", assets)
	}
}

func TestSnapshotMarksFailedReconciliationAsStale(t *testing.T) {
	store := NewStore()
	store.MarkReconciled()
	store.MarkFailed()
	response := store.response()
	if response.Ready || response.State != "error" || !response.Stale || response.ObservedAt == nil || response.LastFailedReconcile == nil {
		t.Fatalf("unexpected stale snapshot: %#v", response)
	}
}
