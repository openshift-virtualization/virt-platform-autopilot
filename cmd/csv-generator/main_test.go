package main

import (
	"testing"

	"github.com/kubevirt/virt-platform-autopilot/cmd/csv-generator/parser"
)

func TestBuildCSVAddsHCOPolicyLabelsOnlyToPodTemplate(t *testing.T) {
	csv := buildCSV("1.2.3", "openshift-cnv", "example.invalid/autopilot:latest", "1.2.3", "IfNotPresent", []parser.EnvVar{}, nil)
	deployment := csv.Spec.Install.Spec.Deployments[0]

	for _, key := range []string{
		"np.kubevirt.io/allow-access-cluster-services",
		"np.kubevirt.io/allow-prometheus-access",
	} {
		if deployment.Spec.Template.Metadata.Labels[key] != "true" {
			t.Errorf("pod template label %q = %q, want true", key, deployment.Spec.Template.Metadata.Labels[key])
		}
		if _, found := deployment.Spec.Selector.MatchLabels[key]; found {
			t.Errorf("deployment selector unexpectedly contains network-policy label %q", key)
		}
	}
}
