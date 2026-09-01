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

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	corev1 "k8s.io/api/core/v1"

	"github.com/kubevirt/virt-platform-autopilot/pkg/metricstls"
	"github.com/kubevirt/virt-platform-autopilot/pkg/tlsprofile"
)

// metricsTLSRequest is the single, fixed reconcile key this controller enqueues.
// The reconcile is a full refresh of the in-memory TLS state, so it does not
// matter which watched object triggered it; collapsing every event onto one key
// lets the workqueue dedupe bursts (e.g. a CA rotation touching the ConfigMap).
var metricsTLSRequest = reconcile.Request{
	NamespacedName: types.NamespacedName{Name: "metrics-tls"},
}

// MetricsTLSReconciler keeps the metrics endpoint's mTLS state fresh by watching
// the cluster TLS policy and client-CA sources instead of polling them:
//
//   - the cluster APIServer CR (config.openshift.io) — its spec.tlsSecurityProfile
//     drives the negotiated min TLS version / cipher suites, and
//   - the kube-system extension-apiserver-authentication ConfigMap — its
//     client-ca-file is the trust anchor for verifying scraper client certs.
//
// On any change it re-reads both authoritatively (via an uncached reader) and
// updates the process-local caches (pkg/tlsprofile) and the client-CA pool. It
// runs on every replica (not just the leader) because each pod serves its own
// metrics endpoint.
type MetricsTLSReconciler struct {
	// reader is authoritative (uncached) so a stale cache never narrows the
	// trusted client set or applies an out-of-date TLS policy. The watches
	// (informers) decide *when* to refresh; the reads stay live.
	reader client.Reader
	caPool *metricstls.ClientCAPool
	// watchAPIServer gates the config.openshift.io APIServer watch: its CRD only
	// exists on OpenShift, so on other clusters we watch the client-CA ConfigMap
	// alone and leave the APIServer profile at its default.
	watchAPIServer bool
}

// NewMetricsTLSReconciler builds a reconciler that refreshes the metrics mTLS
// state. Set watchAPIServer only when the apiservers.config.openshift.io CRD is
// installed.
func NewMetricsTLSReconciler(reader client.Reader, caPool *metricstls.ClientCAPool, watchAPIServer bool) *MetricsTLSReconciler {
	return &MetricsTLSReconciler{
		reader:         reader,
		caPool:         caPool,
		watchAPIServer: watchAPIServer,
	}
}

// Reconcile re-reads the APIServer TLS security profile and the metrics client
// CA and updates the in-memory state consumed per-connection by the metrics TLS
// server. Both reads are best-effort: a missing source leaves the last-known
// (or default) state in place rather than dropping to an insecure config.
func (r *MetricsTLSReconciler) Reconcile(ctx context.Context, _ reconcile.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	if r.watchAPIServer {
		if _, err := tlsprofile.RefreshAPIServer(ctx, r.reader); err != nil && !apierrors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
	}

	if err := metricstls.RefreshClientCA(ctx, r.reader, r.caPool); err != nil {
		if apierrors.IsNotFound(err) {
			// The ConfigMap should always exist; if it briefly does not, keep the
			// current pool and wait for the next event rather than hot-looping.
			logger.Info("metrics client-CA ConfigMap not found; keeping current pool")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// SetupWithManager registers the watches. It runs the controller on every
// replica (NeedLeaderElection=false) since the TLS state is per-pod. The
// informers backing these watches require the matching ByObject cache
// exemptions in the manager options (the sources are unlabeled cluster
// singletons that the default managed-by selector would otherwise filter out).
func (r *MetricsTLSReconciler) SetupWithManager(mgr ctrl.Manager) error {
	enqueue := handler.EnqueueRequestsFromMapFunc(
		func(context.Context, client.Object) []reconcile.Request {
			return []reconcile.Request{metricsTLSRequest}
		},
	)

	needLeaderElection := false
	b := ctrl.NewControllerManagedBy(mgr).
		Named("metrics-tls").
		WithOptions(controller.Options{NeedLeaderElection: &needLeaderElection}).
		Watches(&corev1.ConfigMap{}, enqueue, builder.WithPredicates(clientCAConfigMapPredicate()))

	if r.watchAPIServer {
		apiServer := &unstructured.Unstructured{}
		apiServer.SetGroupVersionKind(tlsprofile.APIServerGVK)
		b = b.Watches(apiServer, enqueue, builder.WithPredicates(apiServerSingletonPredicate()))
	}

	return b.Complete(r)
}

// clientCAConfigMapPredicate limits reconciles to the authoritative client-CA
// ConfigMap. The cache informer is already scoped to it via ByObject, but the
// predicate keeps intent explicit and guards against any wider event source.
func clientCAConfigMapPredicate() predicate.Predicate {
	return predicate.NewPredicateFuncs(func(obj client.Object) bool {
		return obj.GetNamespace() == metricstls.ClientCAConfigMapNamespace &&
			obj.GetName() == metricstls.ClientCAConfigMapName
	})
}

// apiServerSingletonPredicate limits reconciles to the cluster APIServer CR.
func apiServerSingletonPredicate() predicate.Predicate {
	return predicate.NewPredicateFuncs(func(obj client.Object) bool {
		return obj.GetName() == "cluster"
	})
}
