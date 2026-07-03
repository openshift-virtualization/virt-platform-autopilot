package e2e

import (
	"fmt"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

type testAsset struct {
	GVK           schema.GroupVersionKind
	Plural        string
	Name          string
	Namespace     string
	GateCRD       string
	ClusterScoped bool
}

func (a testAsset) webhookName() string {
	return fmt.Sprintf("autopilot-e2e-block-%s", a.Plural)
}

// assetsUnderTest lists all phase-1 "install: always" assets from metadata.yaml.
// Used by anti-thrashing, alert, and other E2E test suites.
var assetsUnderTest = []testAsset{
	{
		GVK:           schema.GroupVersionKind{Group: "machineconfiguration.openshift.io", Version: "v1", Kind: "MachineConfig"},
		Plural:        "machineconfigs",
		Name:          "90-worker-swap-online",
		ClusterScoped: true,
	},
	{
		GVK:           schema.GroupVersionKind{Group: "machineconfiguration.openshift.io", Version: "v1", Kind: "MachineConfig"},
		Plural:        "machineconfigs",
		Name:          "99-openshift-machineconfig-worker-psi-karg",
		GateCRD:       "kubedeschedulers.operator.openshift.io",
		ClusterScoped: true,
	},
	{
		GVK:           schema.GroupVersionKind{Group: "machineconfiguration.openshift.io", Version: "v1", Kind: "KubeletConfig"},
		Plural:        "kubeletconfigs",
		Name:          "virt-perf-settings",
		GateCRD:       "kubeletconfigs.machineconfiguration.openshift.io",
		ClusterScoped: true,
	},
	{
		GVK:           schema.GroupVersionKind{Group: "observability.openshift.io", Version: "v1alpha1", Kind: "UIPlugin"},
		Plural:        "uiplugins",
		Name:          "monitoring",
		GateCRD:       "uiplugins.observability.openshift.io",
		ClusterScoped: true,
	},
	{
		GVK:           schema.GroupVersionKind{Group: "operator.openshift.io", Version: "v1", Kind: "KubeDescheduler"},
		Plural:        "kubedeschedulers",
		Name:          "cluster",
		Namespace:     "openshift-kube-descheduler-operator",
		GateCRD:       "kubedeschedulers.operator.openshift.io",
		ClusterScoped: false,
	},
}
