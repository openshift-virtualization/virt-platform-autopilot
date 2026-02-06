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

package controller

import (
	"context"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/kubevirt/virt-platform-operator/pkg/assets"
	pkgcontext "github.com/kubevirt/virt-platform-operator/pkg/context"
	"github.com/kubevirt/virt-platform-operator/pkg/engine"
)

const (
	// HCOGroup is the API group for HyperConverged
	HCOGroup = "hco.kubevirt.io"

	// HCOVersion is the API version for HyperConverged
	HCOVersion = "v1beta1"

	// HCOKind is the kind for HyperConverged
	HCOKind = "HyperConverged"

	// HCOName is the expected name of the HCO instance
	HCOName = "kubevirt-hyperconverged"

	// DefaultHCONamespace is the default namespace for HCO
	DefaultHCONamespace = "openshift-cnv"
)

var (
	// HCOGVK is the GroupVersionKind for HyperConverged
	HCOGVK = schema.GroupVersionKind{
		Group:   HCOGroup,
		Version: HCOVersion,
		Kind:    HCOKind,
	}
)

// PlatformReconciler reconciles the virt platform based on HCO state
type PlatformReconciler struct {
	client.Client
	Namespace string

	loader             *assets.Loader
	registry           *assets.Registry
	patcher            *engine.Patcher
	contextBuilder     *RenderContextBuilder
	conditionEvaluator *assets.DefaultConditionEvaluator
}

// NewPlatformReconciler creates a new platform reconciler
func NewPlatformReconciler(c client.Client, namespace string) (*PlatformReconciler, error) {
	loader := assets.NewLoader()

	registry, err := assets.NewRegistry(loader)
	if err != nil {
		return nil, fmt.Errorf("failed to create asset registry: %w", err)
	}

	return &PlatformReconciler{
		Client:             c,
		Namespace:          namespace,
		loader:             loader,
		registry:           registry,
		patcher:            engine.NewPatcher(c, loader),
		contextBuilder:     NewRenderContextBuilder(c),
		conditionEvaluator: &assets.DefaultConditionEvaluator{},
	}, nil
}

// Reconcile reconciles the virt platform
func (r *PlatformReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	logger.Info("Reconciling virt platform",
		"namespace", req.Namespace,
		"name", req.Name,
	)

	// Get the HyperConverged instance
	hco := &unstructured.Unstructured{}
	hco.SetGroupVersionKind(HCOGVK)

	err := r.Get(ctx, req.NamespacedName, hco)
	if err != nil {
		if errors.IsNotFound(err) {
			logger.Info("HCO not found, skipping reconciliation")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Step 1: Apply HCO golden config FIRST (reconcile_order: 0)
	logger.Info("Applying HCO golden configuration")
	if err := r.reconcileHCO(ctx, hco); err != nil {
		logger.Error(err, "Failed to reconcile HCO golden config")
		return ctrl.Result{RequeueAfter: 30 * time.Second}, err
	}

	// Re-fetch HCO to get effective state after applying golden config
	if err := r.Get(ctx, req.NamespacedName, hco); err != nil {
		return ctrl.Result{}, err
	}

	// Step 2: Build RenderContext from effective HCO state
	logger.Info("Building render context from HCO state")
	renderCtx, err := r.contextBuilder.Build(ctx, hco)
	if err != nil {
		logger.Error(err, "Failed to build render context")
		return ctrl.Result{RequeueAfter: 30 * time.Second}, err
	}

	// Update condition evaluator with current context
	r.updateConditionEvaluator(hco, renderCtx)

	// Step 3: Reconcile all other assets in reconcile_order
	logger.Info("Reconciling platform assets")
	if err := r.reconcileAssets(ctx, renderCtx); err != nil {
		logger.Error(err, "Failed to reconcile assets")
		return ctrl.Result{RequeueAfter: 30 * time.Second}, err
	}

	logger.Info("Successfully reconciled virt platform")
	return ctrl.Result{RequeueAfter: 5 * time.Minute}, nil
}

// reconcileHCO applies the golden HCO configuration
func (r *PlatformReconciler) reconcileHCO(ctx context.Context, currentHCO *unstructured.Unstructured) error {
	logger := log.FromContext(ctx)

	// Get HCO asset from registry
	hcoAsset, err := r.registry.GetAsset("hco-golden-config")
	if err != nil {
		return fmt.Errorf("failed to get HCO asset: %w", err)
	}

	// Build minimal context for HCO rendering (use current HCO state)
	minimalCtx, err := r.contextBuilder.Build(ctx, currentHCO)
	if err != nil {
		return fmt.Errorf("failed to build context for HCO: %w", err)
	}

	// Reconcile HCO using Patched Baseline algorithm
	applied, err := r.patcher.ReconcileAsset(ctx, hcoAsset, minimalCtx)
	if err != nil {
		return fmt.Errorf("failed to reconcile HCO: %w", err)
	}

	if applied {
		logger.Info("Applied HCO golden configuration")
	} else {
		logger.V(1).Info("HCO golden configuration unchanged")
	}

	return nil
}

// reconcileAssets reconciles all non-HCO assets
func (r *PlatformReconciler) reconcileAssets(ctx context.Context, renderCtx *pkgcontext.RenderContext) error {
	logger := log.FromContext(ctx)

	// Get all assets sorted by reconcile_order (HCO should be 0, others 1+)
	allAssets := r.registry.ListAssetsByReconcileOrder()

	// Filter out HCO (already reconciled) and check conditions
	var assetsToReconcile []assets.AssetMetadata
	for i := range allAssets {
		asset := &allAssets[i]

		// Skip HCO (already reconciled in step 1)
		if asset.ReconcileOrder == 0 {
			continue
		}

		// Check if asset should be applied based on conditions
		shouldApply, err := r.registry.ShouldApply(ctx, asset, r.conditionEvaluator)
		if err != nil {
			logger.Error(err, "Failed to evaluate asset conditions, skipping",
				"asset", asset.Name,
			)
			continue
		}

		if !shouldApply {
			logger.V(1).Info("Asset conditions not met, skipping",
				"asset", asset.Name,
			)
			continue
		}

		assetsToReconcile = append(assetsToReconcile, *asset)
	}

	// Reconcile all applicable assets
	appliedCount, err := r.patcher.ReconcileAssets(ctx, assetsToReconcile, renderCtx)
	logger.Info("Reconciled assets",
		"total", len(assetsToReconcile),
		"applied", appliedCount,
	)

	return err
}

// updateConditionEvaluator updates the condition evaluator with current context
func (r *PlatformReconciler) updateConditionEvaluator(hco *unstructured.Unstructured, ctx *pkgcontext.RenderContext) {
	// Update hardware context
	r.conditionEvaluator.HardwareContext = ctx.Hardware.AsMap()

	// Extract feature gates from HCO
	r.conditionEvaluator.FeatureGates = extractFeatureGates(hco)

	// Extract annotations from HCO
	r.conditionEvaluator.Annotations = hco.GetAnnotations()
}

// extractFeatureGates extracts feature gates from HCO spec
func extractFeatureGates(hco *unstructured.Unstructured) map[string]bool {
	gates := make(map[string]bool)

	featureGates, found, err := unstructured.NestedStringSlice(hco.Object, "spec", "featureGates")
	if err != nil || !found {
		return gates
	}

	for _, gate := range featureGates {
		gates[gate] = true
	}

	return gates
}

// SetupWithManager sets up the controller with the Manager
func (r *PlatformReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Create unstructured object for HCO
	hco := &unstructured.Unstructured{}
	hco.SetGroupVersionKind(HCOGVK)

	return ctrl.NewControllerManagedBy(mgr).
		For(hco).
		Named("platform").
		Complete(r)
}

// Ensure PlatformReconciler implements reconcile.Reconciler
var _ reconcile.Reconciler = &PlatformReconciler{}
