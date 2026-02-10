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

package main

import (
	"context"
	"flag"
	"os"
	"time"

	eventsv1 "k8s.io/api/events/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/selection"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/kubevirt/virt-platform-operator/pkg/controller"
	"github.com/kubevirt/virt-platform-operator/pkg/engine"
	"github.com/kubevirt/virt-platform-operator/pkg/util"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(apiextensionsv1.AddToScheme(scheme))
	utilruntime.Must(eventsv1.AddToScheme(scheme))

	// Register Unstructured for HCO GVK so the manager can use it in ByObject cache config
	// This avoids REST mapping queries that would fail if the CRD doesn't exist yet
	hcoGV := schema.GroupVersion{Group: controller.HCOGroup, Version: controller.HCOVersion}
	scheme.AddKnownTypes(hcoGV, &unstructured.Unstructured{})
}

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	var probeAddr string
	var namespace string
	var crdValidationTimeout time.Duration

	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.StringVar(&namespace, "namespace", "openshift-cnv",
		"The namespace where HyperConverged CR is located.")
	flag.DurationVar(&crdValidationTimeout, "crd-validation-timeout", 10*time.Second,
		"Timeout for validating that required CRDs exist at startup.")

	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	// Create label selector for cache filtering
	// Only cache resources managed by this operator (reduces memory in large clusters)
	managedByRequirement, err := labels.NewRequirement(
		engine.ManagedByLabel,
		selection.Equals,
		[]string{engine.ManagedByValue},
	)
	if err != nil {
		setupLog.Error(err, "unable to create label selector")
		os.Exit(1)
	}
	managedBySelector := labels.NewSelector().Add(*managedByRequirement)

	// Create unstructured object for HCO cache configuration
	// We registered Unstructured with the HCO GVK in init(), so this won't require API queries
	hcoForCache := &unstructured.Unstructured{}
	hcoForCache.SetGroupVersionKind(controller.HCOGVK)

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme: scheme,
		Metrics: metricsserver.Options{
			BindAddress: metricsAddr,
		},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "virt-platform-operator.kubevirt.io",
		Cache: cache.Options{
			// By default, only cache objects with our managed-by label
			// This dramatically reduces memory usage in large clusters
			DefaultLabelSelector: managedBySelector,
			// IMPORTANT: Exempt certain resource types from label filtering
			ByObject: map[client.Object]cache.ByObject{
				// Watch all HCOs (labeled or not) to adopt pre-existing ones
				hcoForCache: {
					Label: labels.Everything(),
				},
				// Watch all CRDs for soft dependency detection
				// CRDs are managed by other operators and won't have our label
				&apiextensionsv1.CustomResourceDefinition{}: {
					Label: labels.Everything(),
				},
			},
		},
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	// Validate HCO CRD exists before proceeding
	// This operator requires the HyperConverged CRD to be installed by OLM
	setupLog.Info("Validating HCO CRD exists", "timeout", crdValidationTimeout)
	crdChecker := util.NewCRDChecker(mgr.GetAPIReader())
	// Use a short-lived context for validation (not the signal handler context)
	validateCtx, cancel := context.WithTimeout(context.Background(), crdValidationTimeout)
	defer cancel()
	hcoCRDInstalled, err := crdChecker.IsCRDInstalled(validateCtx, "hyperconvergeds.hco.kubevirt.io")
	if err != nil {
		setupLog.Error(err, "failed to check for HCO CRD")
		os.Exit(1)
	}
	if !hcoCRDInstalled {
		setupLog.Error(nil, "HyperConverged CRD not found - this operator requires the HCO CRD to be installed by OLM")
		os.Exit(1)
	}
	setupLog.Info("HCO CRD validation passed")

	// Setup platform controller
	// The API reader bypasses cache to detect and adopt unlabeled objects
	reconciler, err := controller.NewPlatformReconciler(
		mgr.GetClient(),
		mgr.GetAPIReader(),
		namespace,
	)
	if err != nil {
		setupLog.Error(err, "unable to create platform reconciler")
		os.Exit(1)
	}

	// Setup event recorder
	eventRecorder := util.NewEventRecorder(
		mgr.GetEventRecorder("virt-platform-operator"),
	)
	reconciler.SetEventRecorder(eventRecorder)

	if err = reconciler.SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to setup platform controller")
		os.Exit(1)
	}

	// Setup health checks
	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
