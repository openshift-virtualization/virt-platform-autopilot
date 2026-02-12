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
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/spf13/cobra"
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

	"github.com/kubevirt/virt-platform-autopilot/cmd/render"
	"github.com/kubevirt/virt-platform-autopilot/pkg/assets"
	pkgcontext "github.com/kubevirt/virt-platform-autopilot/pkg/context"
	"github.com/kubevirt/virt-platform-autopilot/pkg/controller"
	"github.com/kubevirt/virt-platform-autopilot/pkg/debug"
	"github.com/kubevirt/virt-platform-autopilot/pkg/engine"
	"github.com/kubevirt/virt-platform-autopilot/pkg/util"
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
	hcoGV := schema.GroupVersion{Group: pkgcontext.HCOGroup, Version: pkgcontext.HCOVersion}
	scheme.AddKnownTypes(hcoGV, &unstructured.Unstructured{})
}

func main() {
	rootCmd := &cobra.Command{
		Use:   "virt-platform-autopilot",
		Short: "Automated platform configuration for KubeVirt workloads",
		Long: `virt-platform-autopilot automatically configures OpenShift/Kubernetes
clusters for optimal virtualization workload performance by managing
platform-level resources based on HyperConverged configuration.`,
	}

	// Add subcommands
	rootCmd.AddCommand(newRunCommand())
	rootCmd.AddCommand(render.NewRenderCommand())

	// Default to run command if no subcommand specified (backward compatibility)
	if len(os.Args) == 1 || (len(os.Args) > 1 && os.Args[1][0] == '-') {
		// No subcommand or starts with flag - use run command
		os.Args = append([]string{os.Args[0], "run"}, os.Args[1:]...)
	}

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

// newRunCommand creates the run subcommand for the controller
func newRunCommand() *cobra.Command {
	var metricsAddr string
	var debugAddr string
	var enableLeaderElection bool
	var probeAddr string
	var namespace string
	var crdValidationTimeout time.Duration
	var enableDebugServer bool
	var development bool

	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run the platform autopilot controller",
		Long:  `Start the controller manager that watches HyperConverged resources and manages platform configuration.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runController(
				metricsAddr,
				debugAddr,
				probeAddr,
				namespace,
				enableLeaderElection,
				enableDebugServer,
				development,
				crdValidationTimeout,
			)
		},
	}

	cmd.Flags().StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	cmd.Flags().StringVar(&debugAddr, "debug-bind-address", "127.0.0.1:8081", "The address the debug endpoint binds to (localhost only for security).")
	cmd.Flags().StringVar(&probeAddr, "health-probe-bind-address", ":8082", "The address the probe endpoint binds to.")
	cmd.Flags().BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	cmd.Flags().StringVar(&namespace, "namespace", "openshift-cnv",
		"The namespace where HyperConverged CR is located.")
	cmd.Flags().DurationVar(&crdValidationTimeout, "crd-validation-timeout", 10*time.Second,
		"Timeout for validating that required CRDs exist at startup.")
	cmd.Flags().BoolVar(&enableDebugServer, "enable-debug-server", true,
		"Enable debug HTTP server with /debug/render and /debug/exclusions endpoints.")
	cmd.Flags().BoolVar(&development, "development", true,
		"Enable development mode logging.")

	return cmd
}

// runController starts the controller manager
func runController(
	metricsAddr string,
	debugAddr string,
	probeAddr string,
	namespace string,
	enableLeaderElection bool,
	enableDebugServer bool,
	development bool,
	crdValidationTimeout time.Duration,
) error {
	// Setup logging
	opts := zap.Options{
		Development: development,
	}
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	// Create label selector for cache filtering
	// Only cache resources managed by this autopilot (reduces memory in large clusters)
	managedByRequirement, err := labels.NewRequirement(
		engine.ManagedByLabel,
		selection.Equals,
		[]string{engine.ManagedByValue},
	)
	if err != nil {
		setupLog.Error(err, "unable to create label selector")
		return err
	}
	managedBySelector := labels.NewSelector().Add(*managedByRequirement)

	// Create unstructured object for HCO cache configuration
	// We registered Unstructured with the HCO GVK in init(), so this won't require API queries
	hcoForCache := &unstructured.Unstructured{}
	hcoForCache.SetGroupVersionKind(pkgcontext.HCOGVK)

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme: scheme,
		Metrics: metricsserver.Options{
			BindAddress: metricsAddr,
		},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "virt-platform-autopilot.kubevirt.io",
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
		return err
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
		return err
	}
	if !hcoCRDInstalled {
		setupLog.Error(nil, "HyperConverged CRD not found - this component requires the HCO CRD to be installed by OLM")
		return fmt.Errorf("HCO CRD not found")
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
		return err
	}

	// Setup event recorder
	eventRecorder := util.NewEventRecorder(
		mgr.GetEventRecorder("virt-platform-autopilot"),
	)
	reconciler.SetEventRecorder(eventRecorder)

	// Create cancellable context for graceful shutdown
	// This allows the reconciler to trigger shutdown instead of calling os.Exit(0)
	signalCtx := ctrl.SetupSignalHandler()
	ctx, cancel := context.WithCancel(signalCtx)
	reconciler.SetShutdownFunc(cancel)

	if err = reconciler.SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to setup platform controller")
		return err
	}

	// Setup debug server if enabled
	if enableDebugServer {
		setupLog.Info("Starting debug server", "address", debugAddr)
		loader := assets.NewLoader()
		registry, err := assets.NewRegistry(loader)
		if err != nil {
			setupLog.Error(err, "unable to load asset registry for debug server")
			return err
		}

		debugServer := debug.NewServer(mgr.GetClient(), loader, registry)
		debugMux := http.NewServeMux()
		debugServer.InstallHandlers(debugMux)

		httpServer := &http.Server{
			Addr:    debugAddr,
			Handler: debugMux,
		}

		go func() {
			if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				setupLog.Error(err, "debug server failed")
			}
		}()

		// Shutdown debug server on context cancellation
		go func() {
			<-ctx.Done()
			shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer shutdownCancel()
			if err := httpServer.Shutdown(shutdownCtx); err != nil {
				setupLog.Error(err, "debug server shutdown failed")
			}
		}()
	}

	// Setup health checks
	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		return err
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		return err
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctx); err != nil {
		setupLog.Error(err, "problem running manager")
		return err
	}

	return nil
}
