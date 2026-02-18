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
	"os"
	"sync"
	"time"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/kubevirt/virt-platform-autopilot/pkg/assets"
	pkgcontext "github.com/kubevirt/virt-platform-autopilot/pkg/context"
	"github.com/kubevirt/virt-platform-autopilot/pkg/engine"
	"github.com/kubevirt/virt-platform-autopilot/pkg/util"
)

// PlatformReconciler reconciles the virt platform based on HCO state
type PlatformReconciler struct {
	client.Client
	Namespace string

	loader              *assets.Loader
	registry            *assets.Registry
	patcher             *engine.Patcher
	tombstoneReconciler *engine.TombstoneReconciler
	contextBuilder      *RenderContextBuilder
	conditionEvaluator  *assets.DefaultConditionEvaluator
	crdChecker          *util.CRDChecker
	eventRecorder       *util.EventRecorder
	watchedCRDs         map[string]bool    // Track CRDs we're watching to avoid restart loops
	watchedCRDsMu       sync.RWMutex       // Protects watchedCRDs from concurrent access
	shutdownFunc        context.CancelFunc // Graceful shutdown instead of os.Exit
	shutdownMu          sync.Mutex         // Protects shutdownFunc
}

// NewPlatformReconciler creates a new platform reconciler
// The apiReader enables object adoption (detecting and labeling unlabeled objects)
// For tests with fake clients, pass nil for apiReader
// The event recorder will be set automatically by SetupWithManager()
func NewPlatformReconciler(c client.Client, apiReader client.Reader, namespace string) (*PlatformReconciler, error) {
	loader := assets.NewLoader()

	registry, err := assets.NewRegistry(loader)
	if err != nil {
		return nil, fmt.Errorf("failed to create asset registry: %w", err)
	}

	return &PlatformReconciler{
		Client:              c,
		Namespace:           namespace,
		loader:              loader,
		registry:            registry,
		patcher:             engine.NewPatcher(c, apiReader, loader),
		tombstoneReconciler: engine.NewTombstoneReconciler(c, loader),
		contextBuilder:      NewRenderContextBuilder(c),
		conditionEvaluator:  &assets.DefaultConditionEvaluator{},
		crdChecker:          util.NewCRDChecker(apiReader), // Use apiReader (not cache-dependent)
		watchedCRDs:         make(map[string]bool),
	}, nil
}

// SetEventRecorder sets the event recorder for this reconciler
func (r *PlatformReconciler) SetEventRecorder(recorder *util.EventRecorder) {
	r.eventRecorder = recorder
	// Also set it on the patcher so it can emit events during reconciliation
	if r.patcher != nil {
		r.patcher.SetEventRecorder(recorder)
	}
	// Also set it on the tombstone reconciler for tombstone events
	if r.tombstoneReconciler != nil {
		r.tombstoneReconciler.SetEventRecorder(recorder)
	}
	// Also set it on the context builder for hardware detection events
	if r.contextBuilder != nil {
		r.contextBuilder.SetEventRecorder(recorder)
	}
}

// SetShutdownFunc sets the shutdown function for graceful operator restart
// This allows the reconciler to trigger graceful shutdown instead of os.Exit(0)
func (r *PlatformReconciler) SetShutdownFunc(shutdownFunc context.CancelFunc) {
	r.shutdownMu.Lock()
	defer r.shutdownMu.Unlock()
	r.shutdownFunc = shutdownFunc
}

// triggerShutdown initiates graceful shutdown (for CRD watch reconfiguration)
func (r *PlatformReconciler) triggerShutdown() {
	r.shutdownMu.Lock()
	defer r.shutdownMu.Unlock()
	if r.shutdownFunc != nil {
		r.shutdownFunc()
	} else {
		// Fallback to os.Exit if shutdown func not set (for tests or legacy usage)
		os.Exit(0)
	}
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
	hco.SetGroupVersionKind(pkgcontext.HCOGVK)

	err := r.Get(ctx, req.NamespacedName, hco)
	if err != nil {
		if errors.IsNotFound(err) {
			logger.Info("HCO not found, skipping reconciliation")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Step 0: Process tombstones FIRST (before HCO reconciliation)
	logger.Info("Processing tombstones")
	deletedCount, err := r.tombstoneReconciler.ReconcileTombstones(ctx, hco)
	if err != nil {
		// Log error but don't fail reconciliation - tombstone cleanup is best-effort
		logger.Error(err, "Failed to process tombstones (continuing with reconciliation)")
	} else if deletedCount > 0 {
		logger.Info("Tombstone processing completed", "deleted", deletedCount)
	}

	// Step 1: Apply HCO golden config FIRST (reconcile_order: 0)
	logger.Info("Applying HCO golden configuration")
	if err := r.reconcileHCO(ctx, hco); err != nil {
		logger.Error(err, "Failed to reconcile HCO golden config")
		return ctrl.Result{}, err
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
		return ctrl.Result{}, err
	}

	// Update condition evaluator with current context
	r.updateConditionEvaluator(hco, renderCtx)

	// Step 3: Reconcile all other assets in reconcile_order
	logger.Info("Reconciling platform assets")
	if err := r.reconcileAssets(ctx, renderCtx); err != nil {
		logger.Error(err, "Failed to reconcile assets")
		return ctrl.Result{}, err
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

		// Check if component's CRD is installed (soft dependency handling)
		if asset.Component != "" {
			supported, crdName, err := r.crdChecker.IsComponentSupported(ctx, asset.Component)
			if err != nil {
				logger.Error(err, "Failed to check CRD availability, skipping asset",
					"asset", asset.Name,
					"component", asset.Component,
				)
				continue
			}

			if !supported {
				logger.V(1).Info("CRD not installed, skipping asset (soft dependency)",
					"asset", asset.Name,
					"component", asset.Component,
					"crd", crdName,
				)
				// Record event about missing CRD (only once per reconciliation to avoid spam)
				if r.eventRecorder != nil {
					r.eventRecorder.CRDMissing(renderCtx.HCO, asset.Component, crdName)
				}
				continue
			}
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
			// Optionally record event (commented out to avoid spam for opt-in assets)
			// if r.eventRecorder != nil {
			// 	r.eventRecorder.AssetSkipped(renderCtx.HCO, asset.Name, "conditions not met")
			// }
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

	// Record reconciliation event
	if r.eventRecorder != nil && err == nil {
		r.eventRecorder.ReconcileSucceeded(renderCtx.HCO, appliedCount, len(assetsToReconcile))
	}

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
// isManagedCRD checks if a CRD is for a resource type we manage
func (r *PlatformReconciler) isManagedCRD(crdName string) bool {
	for _, mappedCRD := range util.ComponentKindMapping {
		if crdName == mappedCRD {
			return true
		}
	}
	return false
}

// isWatchedCRD safely checks if a CRD is currently being watched
func (r *PlatformReconciler) isWatchedCRD(crdName string) bool {
	r.watchedCRDsMu.RLock()
	defer r.watchedCRDsMu.RUnlock()
	return r.watchedCRDs[crdName]
}

// markCRDAsWatched safely marks a CRD as being watched
func (r *PlatformReconciler) markCRDAsWatched(crdName string) {
	r.watchedCRDsMu.Lock()
	defer r.watchedCRDsMu.Unlock()
	r.watchedCRDs[crdName] = true
}

// crdEventHandler handles CRD creation/update/deletion events
// On create/delete of managed CRDs: restart operator to reconfigure watches
// On update: invalidate cache and trigger reconciliation
func (r *PlatformReconciler) crdEventHandler(ctx context.Context) handler.EventHandler {
	logger := log.FromContext(ctx)

	return handler.Funcs{
		CreateFunc: func(ctx context.Context, e event.CreateEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			crd, ok := e.Object.(*apiextensionsv1.CustomResourceDefinition)
			if !ok {
				return
			}

			// If this is a new managed CRD we aren't watching yet, restart to add watch
			if r.isManagedCRD(crd.Name) && !r.isWatchedCRD(crd.Name) {
				logger.Info("New managed CRD created - restarting operator to configure watch for drift detection",
					"crd", crd.Name)
				// Trigger graceful shutdown so deployment restarts us with new watches
				r.triggerShutdown()
			}

			// For non-managed CRDs, just invalidate cache and trigger reconciliation
			r.crdChecker.InvalidateCache("")
			q.Add(reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      pkgcontext.HCOName,
					Namespace: r.Namespace,
				},
			})
		},
		DeleteFunc: func(ctx context.Context, e event.DeleteEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			crd, ok := e.Object.(*apiextensionsv1.CustomResourceDefinition)
			if !ok {
				return
			}

			// If we were watching this CRD, restart to remove watch
			if r.isManagedCRD(crd.Name) && r.isWatchedCRD(crd.Name) {
				logger.Info("Watched CRD deleted - restarting operator to remove watch",
					"crd", crd.Name)
				// Trigger graceful shutdown so deployment restarts us without the watch
				r.triggerShutdown()
			}

			// For non-managed CRDs, just invalidate cache and trigger reconciliation
			r.crdChecker.InvalidateCache("")
			q.Add(reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      pkgcontext.HCOName,
					Namespace: r.Namespace,
				},
			})
		},
		UpdateFunc: func(ctx context.Context, e event.UpdateEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			crd, ok := e.ObjectNew.(*apiextensionsv1.CustomResourceDefinition)
			if !ok {
				return
			}

			logger.Info("CRD updated, invalidating cache and triggering HCO reconciliation",
				"crd", crd.Name)

			// Invalidate cache and trigger reconciliation
			r.crdChecker.InvalidateCache("")
			q.Add(reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      pkgcontext.HCOName,
					Namespace: r.Namespace,
				},
			})
		},
	}
}

func (r *PlatformReconciler) SetupWithManager(mgr ctrl.Manager) error {
	logger := mgr.GetLogger().WithName("setup")
	ctx := context.Background()

	// Create unstructured object for HCO
	hco := &unstructured.Unstructured{}
	hco.SetGroupVersionKind(pkgcontext.HCOGVK)

	// Build controller with HCO watch
	builder := ctrl.NewControllerManagedBy(mgr).
		For(hco).
		Watches(
			&apiextensionsv1.CustomResourceDefinition{},
			r.crdEventHandler(ctx),
		).
		Named("platform")

	// Dynamically add watches for resource types we manage (if their CRDs exist)
	logger.Info("Discovering managed resource types to watch")

	// Iterate through ALL components in ComponentKindMapping
	// This ensures we watch all managed types, even if they don't have assets yet
	for component, crdName := range util.ComponentKindMapping {

		// Check if CRD is installed
		installed, err := r.crdChecker.IsCRDInstalled(ctx, crdName)
		if err != nil {
			logger.Error(err, "Failed to check CRD", "crd", crdName)
			continue
		}

		if !installed {
			logger.Info("CRD not installed, skipping watch", "component", component, "crd", crdName)
			continue
		}

		// Fetch CRD to get GVK information
		crd := &apiextensionsv1.CustomResourceDefinition{}
		if err := mgr.GetAPIReader().Get(ctx, types.NamespacedName{Name: crdName}, crd); err != nil {
			logger.Error(err, "Failed to fetch CRD", "crd", crdName)
			continue
		}

		// Use the preferred version for the watch
		var version string
		for _, v := range crd.Spec.Versions {
			if v.Storage {
				version = v.Name
				break
			}
		}
		if version == "" && len(crd.Spec.Versions) > 0 {
			version = crd.Spec.Versions[0].Name
		}

		// Construct GVK
		gvk := schema.GroupVersionKind{
			Group:   crd.Spec.Group,
			Version: version,
			Kind:    crd.Spec.Names.Kind,
		}

		// Create unstructured object for this type
		obj := &unstructured.Unstructured{}
		obj.SetGroupVersionKind(gvk)

		// Add watch - enqueue HCO for reconciliation when these resources change
		logger.Info("Adding watch for managed resource type",
			"component", component,
			"gvk", gvk.String())

		// Track that we're watching this CRD
		r.markCRDAsWatched(crdName)

		builder = builder.Watches(
			obj,
			handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, o client.Object) []reconcile.Request {
				// All managed resources trigger HCO reconciliation
				return []reconcile.Request{
					{
						NamespacedName: types.NamespacedName{
							Name:      pkgcontext.HCOName,
							Namespace: r.Namespace,
						},
					},
				}
			}),
		)
	}

	return builder.Complete(r)
}

// Ensure PlatformReconciler implements reconcile.Reconciler
var _ reconcile.Reconciler = &PlatformReconciler{}
