// Package status records and publishes the bounded Autopilot inspection view.
package status

import (
	"context"
	"encoding/json"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kubevirt/virt-platform-autopilot/pkg/assets"
	pkgcontext "github.com/kubevirt/virt-platform-autopilot/pkg/context"
)

const (
	SummaryConfigMapName = "virt-platform-autopilot-inspection"
	GoldenConfigMapName  = "virt-platform-autopilot-golden"
	InspectionLabel      = "platform.kubevirt.io/inspection"
	// GoldenGenerationKey reserves a durable generation marker in the golden
	// ConfigMap. It lets a restarted leader advance past a golden-only write if
	// it crashed before publishing the corresponding summary.
	GoldenGenerationKey = "generation"
	ManagedByLabel      = "platform.kubevirt.io/managed-by"
	ManagedByValue      = "virt-platform-autopilot"
	// Leave headroom below the Kubernetes ConfigMap object limit for object
	// metadata and serialization overhead.
	maxInspectionConfigMapDataBytes = 900 * 1024
	// inspectionHeartbeatInterval bounds how long Console can see a healthy or
	// failing snapshot without a durable liveness update. It deliberately
	// matches the controller's normal reconciliation cadence.
	inspectionHeartbeatInterval = 5 * time.Minute
)

const (
	AssetReasonNotRenderable                       = "notRenderable"
	AssetReasonRootExclusion                       = "rootExclusion"
	AssetReasonNodeDisruptionPolicyPending         = "nodeDisruptionPolicyPending"
	AssetReasonReconciliationPaused                = "reconciliationPaused"
	AssetReasonUserOptOut                          = "userOptOut"
	AssetReasonDriftDetectionFailed                = "driftDetectionFailed"
	AssetReasonMachineConfigRollout                = "machineConfigRollout"
	AssetReasonDetected                            = "detected"
	AssetReasonNamespaceNotFound                   = "namespaceNotFound"
	AssetReasonApplyFailed                         = "applyFailed"
	AssetReasonMissingCRD                          = "missingCRD"
	AssetReasonDependencyCheckFailed               = "dependencyCheckFailed"
	AssetReasonConditionEvaluationFailed           = "conditionEvaluationFailed"
	AssetReasonConditionNotMet                     = "conditionNotMet"
	AssetReasonNodeDisruptionPolicyAggregationFail = "nodeDisruptionPolicyAggregationFailed"
	AssetReasonNodeDisruptionPolicyApplyFail       = "nodeDisruptionPolicyApplyFailed"
)

// AssetReasonCodes is the complete, versioned set of machine-readable asset
// reasons. The emitting sites reference the constants above; the schema test
// validates this list against the published contract.
var AssetReasonCodes = []string{
	AssetReasonNotRenderable, AssetReasonRootExclusion, AssetReasonNodeDisruptionPolicyPending, AssetReasonReconciliationPaused, AssetReasonUserOptOut,
	AssetReasonDriftDetectionFailed, AssetReasonMachineConfigRollout, AssetReasonDetected, AssetReasonNamespaceNotFound, AssetReasonApplyFailed,
	AssetReasonMissingCRD, AssetReasonDependencyCheckFailed, AssetReasonConditionEvaluationFailed, AssetReasonConditionNotMet,
	AssetReasonNodeDisruptionPolicyAggregationFail, AssetReasonNodeDisruptionPolicyApplyFail,
}

type Asset struct {
	ID                 string     `json:"id,omitempty"`
	APIVersion         string     `json:"apiVersion"`
	Kind               string     `json:"kind"`
	Name               string     `json:"name"`
	Namespace          string     `json:"namespace"`
	State              string     `json:"state"`
	Reason             string     `json:"reason,omitempty"`
	LastTransitionTime time.Time  `json:"lastTransitionTime"`
	LastDriftTime      *time.Time `json:"lastDriftTime,omitempty"`
	GoldenKey          string     `json:"goldenKey,omitempty"`
	// SharedObject identifies a logical sidecar whose desired slice is merged
	// into the target object shared with other Autopilot assets.
	SharedObject bool   `json:"sharedObject,omitempty"`
	SharedPath   string `json:"sharedPath,omitempty"`
}

type Response struct {
	APIVersion              string     `json:"apiVersion"`
	Generation              uint64     `json:"generation"`
	State                   string     `json:"state"`
	Reason                  string     `json:"reason,omitempty"`
	ObservedAt              *time.Time `json:"observedAt,omitempty"`
	Ready                   bool       `json:"ready"`
	LastSuccessfulReconcile *time.Time `json:"lastSuccessfulReconcile,omitempty"`
	LastFailedReconcile     *time.Time `json:"lastFailedReconcile,omitempty"`
	Stale                   bool       `json:"stale"`
	Truncated               bool       `json:"truncated,omitempty"`
	TruncationReason        string     `json:"truncationReason,omitempty"`
	Features                []Feature  `json:"features"`
	Assets                  []Asset    `json:"assets"`
}
type Feature struct {
	assets.FeatureStatus
	Enabled      bool              `json:"enabled"`
	Availability string            `json:"availability"`
	Reason       string            `json:"reason,omitempty"`
	Activation   *ActivationPlan   `json:"activation,omitempty"`
	Evidence     []FeatureEvidence `json:"evidence"`
}

// FeatureEvidence lets clients explain an availability decision without
// reproducing the operator's feature-to-asset mapping.
type FeatureEvidence struct {
	AssetID string `json:"assetID"`
	State   string `json:"state"`
	Reason  string `json:"reason,omitempty"`
}
type GoldenMetadata struct {
	RenderMode string `json:"renderMode"`
	Reason     string `json:"reason,omitempty"`
}
type GoldenIndex struct {
	Generation uint64                    `json:"generation"`
	Assets     map[string]GoldenMetadata `json:"assets"`
}
type ActivationPlan struct {
	Target    ResourceRef    `json:"target"`
	Method    string         `json:"method"`
	PatchType string         `json:"patchType"`
	Patch     map[string]any `json:"patch"`
}

// ResourceRef identifies the Kubernetes object on which Console performs an
// activation action. It is deliberately fully qualified so clients do not
// need to carry Autopilot's HCO topology as configuration.
type ResourceRef struct {
	Group     string `json:"group"`
	Version   string `json:"version"`
	Resource  string `json:"resource"`
	Kind      string `json:"kind"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

type Store struct {
	mu                      sync.RWMutex
	assets                  map[string]Asset
	features                []Feature
	ready                   bool
	state                   string
	reason                  string
	lastSuccessfulReconcile *time.Time
	lastFailedReconcile     *time.Time
	desired                 map[string]*unstructured.Unstructured
	golden                  map[string]*unstructured.Unstructured
	generation              uint64
	generationInitialized   bool
	ownerReferences         []metav1.OwnerReference
}

func (s *Store) SetFeatures(catalog *assets.FeatureCatalog, enabledByName map[string]bool, hco *unstructured.Unstructured) {
	features := make([]Feature, 0, len(catalog.Features))
	for _, feature := range catalog.Features {
		enabled := enabledByName[feature.Name]
		availability, reason := "unknown", "notAssessed"
		if !enabled {
			availability, reason = "disabled", "notConfigured"
		}
		features = append(features, Feature{FeatureStatus: feature, Enabled: enabled, Availability: availability, Reason: reason, Activation: activationPlan(feature, hco)})
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.features = features
	s.pruneLocked(catalog)
}
func activationPlan(feature assets.FeatureStatus, hco *unstructured.Unstructured) *ActivationPlan {
	if feature.OptIn == nil || hco == nil {
		return nil
	}
	conditions := strings.Split(*feature.OptIn, ",")
	if len(conditions) != 1 {
		return nil
	}
	parts := strings.SplitN(strings.TrimSpace(conditions[0]), "=", 2)
	// The flattened catalog is only safe for an action when it contains one
	// explicit user opt-in. Any additional condition may describe an
	// environment marker, feature gate, or independent sub-feature.
	if len(parts) != 2 || !strings.HasPrefix(parts[0], "platform.kubevirt.io/enable-") {
		return nil
	}
	annotations := map[string]string{parts[0]: parts[1]}
	return &ActivationPlan{
		Target: ResourceRef{
			Group: pkgcontext.HCOGroup, Version: pkgcontext.HCOVersion, Resource: "hyperconvergeds", Kind: pkgcontext.HCOKind,
			Namespace: hco.GetNamespace(), Name: hco.GetName(),
		},
		Method: "PATCH", PatchType: "application/merge-patch+json",
		Patch: map[string]any{"metadata": map[string]any{"annotations": annotations}},
	}
}
func (s *Store) response() Response {
	s.mu.RLock()
	defer s.mu.RUnlock()
	stale := s.lastFailedReconcile != nil && (s.lastSuccessfulReconcile == nil || s.lastFailedReconcile.After(*s.lastSuccessfulReconcile))
	observedAt := s.lastSuccessfulReconcile
	if s.lastFailedReconcile != nil && (observedAt == nil || s.lastFailedReconcile.After(*observedAt)) {
		observedAt = s.lastFailedReconcile
	}
	return Response{APIVersion: "autopilot.kubevirt.io/status/v1beta1", State: s.state, Reason: s.reason, ObservedAt: observedAt, Ready: s.ready, LastSuccessfulReconcile: s.lastSuccessfulReconcile, LastFailedReconcile: s.lastFailedReconcile, Stale: stale, Features: s.featuresWithAvailabilityLocked(), Assets: s.listLocked()}
}

// Publish persists the bounded inspection summary and eligible golden objects.
// It is called by the elected reconciler; Console reads these objects through
// ordinary Kubernetes get/list/watch RBAC rather than an Autopilot HTTP API.
//
// The controller currently configures one reconciler worker. Publish therefore
// assumes a single writer while it snapshots separate store fields; preserve
// that invariant or make this method take one coherent store snapshot before
// raising MaxConcurrentReconciles.
func (s *Store) Publish(ctx context.Context, c client.Client, reader client.Reader, namespace string) error {
	generation, err := s.currentGeneration(ctx, reader, namespace)
	if err != nil {
		return err
	}
	response := s.response()
	response.Generation = generation
	ownerReferences := s.ownerReferencesCopy()
	golden, index, err := s.buildGoldenData(generation, namespace, response, ownerReferences)
	if err != nil {
		return err
	}
	clearUnavailableGoldenKeys(&response, golden)
	summary, err := summaryData(namespace, response, index, ownerReferences)
	if err != nil {
		return err
	}
	change, err := inspectionChange(ctx, reader, namespace, golden, summary, ownerReferences)
	if err != nil {
		return err
	}
	if change == inspectionUnchanged {
		return nil
	}
	if change == inspectionHeartbeat {
		// Freshness is deliberately not semantic content: retain the current
		// generation and golden payload so a heartbeat neither rewrites large
		// rendered manifests nor asks Console to re-fetch them.
		return upsertConfigMap(ctx, c, reader, namespace, SummaryConfigMapName, summary, ownerReferences)
	}
	generation++
	response.Generation = generation
	golden, index, err = s.buildGoldenData(generation, namespace, response, ownerReferences)
	if err != nil {
		return err
	}
	clearUnavailableGoldenKeys(&response, golden)
	summary, err = summaryData(namespace, response, index, ownerReferences)
	if err != nil {
		return err
	}
	// Reserve the generation before writing golden content. If the process
	// survives a crash or write failure between the two ConfigMaps, the retry
	// advances again instead of reusing a partially published generation.
	s.mu.Lock()
	s.generation = generation
	s.mu.Unlock()
	if err := upsertConfigMap(ctx, c, reader, namespace, GoldenConfigMapName, golden, ownerReferences); err != nil {
		return err
	}
	if err := upsertConfigMap(ctx, c, reader, namespace, SummaryConfigMapName, summary, ownerReferences); err != nil {
		return err
	}
	return nil
}

func clearUnavailableGoldenKeys(response *Response, golden map[string]string) {
	for i := range response.Assets {
		if _, ok := golden[response.Assets[i].GoldenKey]; !ok {
			response.Assets[i].GoldenKey = ""
		}
	}
}

func (s *Store) buildGoldenData(generation uint64, namespace string, response Response, ownerReferences []metav1.OwnerReference) (map[string]string, GoldenIndex, error) {
	s.mu.RLock()
	deferred := make(map[string]*unstructured.Unstructured, len(s.golden))
	applied := make(map[string]bool, len(s.desired))
	for id := range s.desired {
		applied[id] = true
	}
	for id, object := range s.golden {
		deferred[id] = object.DeepCopy()
	}
	s.mu.RUnlock()
	golden := make(map[string]string, len(deferred)+1)
	golden[GoldenGenerationKey] = strconv.FormatUint(generation, 10)
	byID := make(map[string]Asset, len(response.Assets))
	for _, asset := range response.Assets {
		byID[asset.ID] = asset
	}
	index := make(map[string]GoldenMetadata, len(deferred))
	baseSize, err := emptyConfigMapSize(namespace, GoldenConfigMapName, ownerReferences)
	if err != nil {
		return nil, GoldenIndex{}, err
	}
	markerBytes, err := configMapEntryBytes(GoldenGenerationKey, golden[GoldenGenerationKey])
	if err != nil {
		return nil, GoldenIndex{}, err
	}
	contentBytes, contentEntries := markerBytes, 1
	ids := make([]string, 0, len(deferred))
	for id := range deferred {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		object := deferred[id]
		metadata := GoldenMetadata{RenderMode: "preview"}
		if applied[id] {
			metadata.RenderMode = "applied"
		}
		if asset, ok := byID[id]; ok && asset.Reason != "" {
			metadata.Reason = asset.Reason
		}
		if object.GetKind() == "Secret" {
			index[id] = GoldenMetadata{RenderMode: "notExposed", Reason: "sensitiveContent"}
			continue
		}
		content, err := json.Marshal(object.Object)
		if err != nil {
			return nil, GoldenIndex{}, err
		}
		golden[id+".json"] = string(content)
		index[id] = metadata
		entryBytes, err := configMapEntryBytes(id+".json", string(content))
		if err != nil {
			return nil, GoldenIndex{}, err
		}
		candidateBytes := contentBytes + entryBytes
		if contentEntries > 0 {
			candidateBytes++
		}
		total := baseSize + candidateBytes
		if total > maxInspectionConfigMapDataBytes {
			delete(golden, id+".json")
			index[id] = GoldenMetadata{RenderMode: "tooLarge", Reason: "sizeLimit"}
			continue
		}
		contentBytes = candidateBytes
		contentEntries++
	}
	return golden, GoldenIndex{Generation: generation, Assets: index}, nil
}

func emptyConfigMapSize(namespace, name string, ownerReferences []metav1.OwnerReference) (int, error) {
	content, err := json.Marshal(corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: inspectionLabels(), OwnerReferences: ownerReferences}, Data: map[string]string{}})
	return len(content), err
}

func configMapEntryBytes(key, value string) (int, error) {
	encodedKey, err := json.Marshal(key)
	if err != nil {
		return 0, err
	}
	encodedValue, err := json.Marshal(value)
	if err != nil {
		return 0, err
	}
	return len(encodedKey) + 1 + len(encodedValue), nil
}

func summaryData(namespace string, response Response, index GoldenIndex, ownerReferences []metav1.OwnerReference) (map[string]string, error) {
	data, fits, err := marshalSummaryData(namespace, response, index, ownerReferences)
	if err != nil || fits {
		return data, err
	}

	// Preserve feature discovery first: activation plans and evidence are the
	// largest optional summary fields, while assets can be shed last. This keeps
	// Console useful as the catalog grows instead of replacing all information
	// with an error snapshot.
	truncated := response
	truncated.Truncated, truncated.TruncationReason = true, "sizeLimit"
	truncated.Features = append([]Feature(nil), response.Features...)
	for i := range truncated.Features {
		truncated.Features[i].Activation = nil
	}
	if data, fits, err = marshalSummaryData(namespace, truncated, index, ownerReferences); err != nil || fits {
		return data, err
	}
	for i := range truncated.Features {
		truncated.Features[i].Evidence = []FeatureEvidence{}
	}
	if data, fits, err = marshalSummaryData(namespace, truncated, index, ownerReferences); err != nil || fits {
		return data, err
	}
	for count := len(truncated.Assets); count >= 0; count-- {
		truncated.Assets = response.Assets[:count]
		if data, fits, err = marshalSummaryData(namespace, truncated, index, ownerReferences); err != nil || fits {
			return data, err
		}
	}

	// Features alone should remain far below the limit. Retain the explicit
	// error fallback only for an unexpected future catalog explosion.
	fallback := Response{APIVersion: response.APIVersion, Generation: response.Generation, State: "error", Reason: "summaryTooLarge", Ready: false, Features: []Feature{}, Assets: []Asset{}, Truncated: true, TruncationReason: "sizeLimit"}
	data, _, err = marshalSummaryData(namespace, fallback, GoldenIndex{Generation: response.Generation, Assets: map[string]GoldenMetadata{}}, ownerReferences)
	return data, err
}

func marshalSummaryData(namespace string, response Response, index GoldenIndex, ownerReferences []metav1.OwnerReference) (map[string]string, bool, error) {
	summary, err := json.Marshal(response)
	if err != nil {
		return nil, false, err
	}
	indexContent, err := json.Marshal(index)
	if err != nil {
		return nil, false, err
	}
	summaryData := map[string]string{"snapshot.json": string(summary), "index.json": string(indexContent)}
	fits, err := fitsConfigMapBudget(namespace, SummaryConfigMapName, summaryData, ownerReferences)
	return summaryData, fits, err
}

type inspectionPublicationChange int

const (
	inspectionUnchanged inspectionPublicationChange = iota
	inspectionHeartbeat
	inspectionSemanticChange
)

func inspectionChange(ctx context.Context, reader client.Reader, namespace string, golden, summary map[string]string, ownerReferences []metav1.OwnerReference) (inspectionPublicationChange, error) {
	change := inspectionUnchanged
	for _, item := range []struct {
		name string
		data map[string]string
	}{{GoldenConfigMapName, golden}, {SummaryConfigMapName, summary}} {
		cm := &corev1.ConfigMap{}
		err := reader.Get(ctx, client.ObjectKey{Namespace: namespace, Name: item.name}, cm)
		if apierrors.IsNotFound(err) {
			return inspectionSemanticChange, nil
		}
		if err != nil {
			return inspectionSemanticChange, err
		}
		if !hasInspectionMetadata(cm, ownerReferences) {
			return inspectionSemanticChange, nil
		}
		if reflect.DeepEqual(cm.Data, item.data) {
			continue
		}
		if item.name != SummaryConfigMapName {
			return inspectionSemanticChange, nil
		}
		equivalent, fresh := summaryEquivalentExceptFreshness(cm.Data, item.data, time.Now().UTC())
		if !equivalent {
			return inspectionSemanticChange, nil
		}
		if !fresh {
			change = inspectionHeartbeat
		}
	}
	return change, nil
}

// currentGeneration resumes from the greatest durable generation after a
// leader or pod change. Publish reserves the next value only when data changes.
func (s *Store) currentGeneration(ctx context.Context, reader client.Reader, namespace string) (uint64, error) {
	s.mu.RLock()
	initialized := s.generationInitialized
	s.mu.RUnlock()
	if !initialized {
		persisted, err := persistedGeneration(ctx, reader, namespace)
		if err != nil {
			return 0, err
		}
		s.mu.Lock()
		if !s.generationInitialized {
			s.generation = persisted
			s.generationInitialized = true
		}
		s.mu.Unlock()
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.generation, nil
}

func persistedGeneration(ctx context.Context, c client.Reader, namespace string) (uint64, error) {
	cm := &corev1.ConfigMap{}
	err := c.Get(ctx, client.ObjectKey{Namespace: namespace, Name: SummaryConfigMapName}, cm)
	if err != nil && !apierrors.IsNotFound(err) {
		return 0, err
	}
	var response Response
	if err == nil {
		_ = json.Unmarshal([]byte(cm.Data["snapshot.json"]), &response)
	}
	golden := &corev1.ConfigMap{}
	err = c.Get(ctx, client.ObjectKey{Namespace: namespace, Name: GoldenConfigMapName}, golden)
	if err != nil && !apierrors.IsNotFound(err) {
		return 0, err
	}
	if err == nil {
		goldenGeneration, parseErr := strconv.ParseUint(golden.Data[GoldenGenerationKey], 10, 64)
		if parseErr == nil && goldenGeneration > response.Generation {
			return goldenGeneration, nil
		}
	}
	return response.Generation, nil
}

func fitsConfigMapBudget(namespace, name string, data map[string]string, ownerReferences []metav1.OwnerReference) (bool, error) {
	content, err := json.Marshal(corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: inspectionLabels(), OwnerReferences: ownerReferences},
		Data:       data,
	})
	if err != nil {
		return false, err
	}
	return len(content) <= maxInspectionConfigMapDataBytes, nil
}

func upsertConfigMap(ctx context.Context, c client.Client, reader client.Reader, namespace, name string, data map[string]string, ownerReferences []metav1.OwnerReference) error {
	cm := &corev1.ConfigMap{}
	key := client.ObjectKey{Namespace: namespace, Name: name}
	err := reader.Get(ctx, key, cm)
	if apierrors.IsNotFound(err) {
		return c.Create(ctx, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: inspectionLabels(), OwnerReferences: ownerReferences}, Data: data})
	}
	if err != nil {
		return err
	}
	if reflect.DeepEqual(cm.Data, data) && hasInspectionMetadata(cm, ownerReferences) {
		return nil
	}
	cm.Data = data
	if cm.Labels == nil {
		cm.Labels = map[string]string{}
	}
	for key, value := range inspectionLabels() {
		cm.Labels[key] = value
	}
	cm.OwnerReferences = ownerReferences
	return c.Update(ctx, cm)
}

func inspectionLabels() map[string]string {
	return map[string]string{InspectionLabel: "true", ManagedByLabel: ManagedByValue}
}

func hasInspectionMetadata(cm *corev1.ConfigMap, ownerReferences []metav1.OwnerReference) bool {
	for key, value := range inspectionLabels() {
		if cm.Labels[key] != value {
			return false
		}
	}
	return reflect.DeepEqual(cm.OwnerReferences, ownerReferences)
}

// summaryEquivalentExceptFreshness ignores only reconciliation freshness fields
// and reports whether the persisted heartbeat is still recent. This permits
// MarkReconciled and MarkFailed to track every attempt in memory without
// making freshness a semantic generation change.
func summaryEquivalentExceptFreshness(existingData, candidateData map[string]string, now time.Time) (bool, bool) {
	if existingData["index.json"] != candidateData["index.json"] {
		return false, false
	}
	var existing, candidate Response
	if json.Unmarshal([]byte(existingData["snapshot.json"]), &existing) != nil || json.Unmarshal([]byte(candidateData["snapshot.json"]), &candidate) != nil {
		return false, false
	}
	candidate.ObservedAt = existing.ObservedAt
	candidate.LastSuccessfulReconcile = existing.LastSuccessfulReconcile
	candidate.LastFailedReconcile = existing.LastFailedReconcile
	if !reflect.DeepEqual(existing, candidate) {
		return false, false
	}
	return true, existing.ObservedAt != nil && now.Sub(*existing.ObservedAt) < inspectionHeartbeatInterval
}

func (s *Store) featuresWithAvailabilityLocked() []Feature {
	byID := make(map[string]Asset, len(s.assets))
	for _, asset := range s.assets {
		if asset.ID != "" {
			byID[asset.ID] = asset
		}
	}
	features := make([]Feature, len(s.features))
	copy(features, s.features)
	for i := range features {
		feature := &features[i]
		feature.Evidence = make([]FeatureEvidence, 0, len(feature.Assets))
		if !feature.Enabled {
			feature.Availability, feature.Reason = "disabled", "notConfigured"
			continue
		}
		var synced, excluded, unmanaged, pending, degraded bool
		degradedReason := ""
		for _, id := range feature.Assets {
			asset, ok := byID[id]
			if !ok {
				feature.Evidence = append(feature.Evidence, FeatureEvidence{AssetID: id, State: "unobserved"})
				pending = true
				continue
			}
			feature.Evidence = append(feature.Evidence, FeatureEvidence{AssetID: id, State: asset.State, Reason: asset.Reason})
			switch asset.State {
			case "applyFailed", "drifted":
				if !degraded {
					degraded, degradedReason = true, asset.State
				}
			case "staged":
				pending = true
			case "synced":
				synced = true
			case "excluded":
				excluded = true
			case "unmanaged":
				unmanaged = true
			}
		}
		switch {
		case degraded:
			feature.Availability, feature.Reason = "degraded", degradedReason
		case pending:
			feature.Availability, feature.Reason = "pending", "awaitingReconciliation"
		case synced && excluded:
			feature.Availability, feature.Reason = "partial", "someAssetsExcluded"
		case synced && unmanaged:
			feature.Availability, feature.Reason = "partial", "someAssetsUnmanaged"
		case synced:
			feature.Availability, feature.Reason = "healthy", ""
		case unmanaged:
			feature.Availability, feature.Reason = "unmanaged", "userOptOut"
		case excluded:
			feature.Availability, feature.Reason = "blocked", "noApplicableAssets"
		default:
			feature.Availability, feature.Reason = "unknown", "notAssessed"
		}
	}
	return features
}
func (s *Store) MarkReconciled() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	s.ready = true
	s.state, s.reason = "ready", ""
	s.lastSuccessfulReconcile = &now
}
func (s *Store) MarkFailed() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	s.ready = false
	s.state, s.reason = "error", "reconciliationFailed"
	s.lastFailedReconcile = &now
}

// SetUnavailable replaces any prior in-memory inspection view when Autopilot
// is disabled or its HCO no longer exists. Publishing immediately afterward
// prevents a durable snapshot from claiming that old assets are still managed.
func (s *Store) SetUnavailable(state, reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.assets = map[string]Asset{}
	s.features = []Feature{}
	s.desired = map[string]*unstructured.Unstructured{}
	s.golden = map[string]*unstructured.Unstructured{}
	s.ready = false
	s.state, s.reason = state, reason
	s.lastSuccessfulReconcile = nil
	s.lastFailedReconcile = nil
}

var DefaultStore = NewStore()

func NewStore() *Store {
	return &Store{assets: map[string]Asset{}, desired: map[string]*unstructured.Unstructured{}, golden: map[string]*unstructured.Unstructured{}, state: "initializing"}
}

// SetOwner gives inspection ConfigMaps the same HCO lifecycle as the
// configuration they explain. A nil or identity-less owner is ignored so
// early reconciliation and unit tests remain safe.
func (s *Store) SetOwner(owner *unstructured.Unstructured) {
	if owner == nil || owner.GetUID() == "" || owner.GetAPIVersion() == "" || owner.GetKind() == "" {
		return
	}
	ref := metav1.NewControllerRef(owner, owner.GroupVersionKind())
	ref.BlockOwnerDeletion = nil
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ownerReferences = []metav1.OwnerReference{*ref}
}

// ClearOwner prevents an absent-HCO snapshot from inheriting a dangling owner
// reference that Kubernetes garbage collection would immediately remove.
func (s *Store) ClearOwner() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ownerReferences = nil
}

func (s *Store) ownerReferencesCopy() []metav1.OwnerReference {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]metav1.OwnerReference(nil), s.ownerReferences...)
}
func (s *Store) SetGolden(asset string, object *unstructured.Unstructured) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.golden[asset] = object.DeepCopy()
}
func (s *Store) SetDesired(asset string, object *unstructured.Unstructured) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.desired[asset] = object.DeepCopy()
	for key, entry := range s.assets {
		if entry.APIVersion == object.GetAPIVersion() && entry.Kind == object.GetKind() && entry.Name == object.GetName() && entry.Namespace == object.GetNamespace() {
			entry.ID = asset
			s.assets[key] = entry
		}
	}
}
func (s *Store) ClearDesired(asset string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.desired, asset)
}

// MarkAssetUnavailable removes stale rendered data while retaining any known
// identity as evidence that an asset is presently excluded or not renderable.
func (s *Store) MarkAssetUnavailable(asset, reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.desired, asset)
	delete(s.golden, asset)
	now := time.Now().UTC()
	found := false
	for key, entry := range s.assets {
		if entry.ID == asset {
			entry.State, entry.Reason, entry.GoldenKey, entry.LastTransitionTime = "excluded", reason, "", now
			s.assets[key] = entry
			found = true
		}
	}
	if !found {
		s.assets["asset/"+asset] = Asset{ID: asset, State: "excluded", Reason: reason, LastTransitionTime: now}
	}
}

func (s *Store) pruneLocked(catalog *assets.FeatureCatalog) {
	known := make(map[string]bool)
	for _, feature := range catalog.Features {
		for _, asset := range feature.Assets {
			known[asset] = true
		}
	}
	for asset := range s.desired {
		if !known[asset] {
			delete(s.desired, asset)
		}
	}
	for asset := range s.golden {
		if !known[asset] {
			delete(s.golden, asset)
		}
	}
	for key, asset := range s.assets {
		if asset.ID != "" && !known[asset.ID] {
			delete(s.assets, key)
		}
	}
}
func key(o *unstructured.Unstructured) string {
	return o.GetAPIVersion() + "/" + o.GetKind() + "/" + o.GetNamespace() + "/" + o.GetName()
}

func assetKey(assetID string, o *unstructured.Unstructured) string {
	if assetID != "" {
		return "asset/" + assetID
	}
	return key(o)
}
func (s *Store) ObserveAsset(assetID string, o *unstructured.Unstructured, state, reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	k := assetKey(assetID, o)
	a := s.assets[k]
	changed := a.APIVersion != o.GetAPIVersion() || a.Kind != o.GetKind() || a.Name != o.GetName() || a.Namespace != o.GetNamespace() || a.State != state || a.Reason != reason
	a.APIVersion, a.Kind, a.Name, a.Namespace, a.State, a.Reason = o.GetAPIVersion(), o.GetKind(), o.GetName(), o.GetNamespace(), state, reason
	if changed || a.LastTransitionTime.IsZero() {
		a.LastTransitionTime = now
	}
	if assetID != "" {
		a.ID = assetID
	} else {
		for asset, desired := range s.desired {
			if desired.GetAPIVersion() == a.APIVersion && desired.GetKind() == a.Kind && desired.GetName() == a.Name && desired.GetNamespace() == a.Namespace {
				a.ID = asset
				break
			}
		}
	}
	if state == "drifted" {
		a.LastDriftTime = &now
	}
	s.assets[k] = a
}

// ObserveSharedAsset records a logical sidecar's slice of a shared Kubernetes
// object. Its state concerns that slice's aggregate reconciliation, not
// exclusive ownership of the whole target object.
func (s *Store) ObserveSharedAsset(assetID string, o *unstructured.Unstructured, state, reason, path string) {
	s.ObserveAsset(assetID, o, state, reason)
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.assets["asset/"+assetID]
	if !ok {
		return
	}
	entry.SharedObject, entry.SharedPath = true, path
	s.assets["asset/"+assetID] = entry
}
func (s *Store) listLocked() []Asset {
	out := make([]Asset, 0, len(s.assets))
	for _, a := range s.assets {
		if a.ID != "" {
			if _, ok := s.golden[a.ID]; ok {
				a.GoldenKey = a.ID + ".json"
			}
		}
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		if out[i].Namespace != out[j].Namespace {
			return out[i].Namespace < out[j].Namespace
		}
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].ID < out[j].ID
	})
	return out
}
