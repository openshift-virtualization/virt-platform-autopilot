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

// csv-generator outputs an OLM ClusterServiceVersion YAML for virt-platform-autopilot
// to stdout.  It is embedded at /usr/bin/csv-generator in the operator image so that
// HCO's build-manifests.sh can invoke it (via "podman/docker run --entrypoint") to
// collect the operator's contribution when building the unified HCO OLM bundle.
//
// Usage:
//
//	csv-generator \
//	  --csv-version=0.1.0 \
//	  --namespace=openshift-cnv \
//	  --operator-image=quay.io/openshift-virtualization/virt-platform-autopilot@sha256:... \
//	  --operator-version=0.1.0 \
//	  [--pull-policy=IfNotPresent] \
//	  [--dump-crds]
package main

import (
	"flag"
	"fmt"
	"os"

	"sigs.k8s.io/yaml"

	"github.com/kubevirt/virt-platform-autopilot/assets"
	"github.com/kubevirt/virt-platform-autopilot/pkg/rbac"
)

// ─── OLM ClusterServiceVersion types ─────────────────────────────────────────
// Minimal type definitions matching operators.coreos.com/v1alpha1.
// We avoid importing operator-framework/api to keep the dependency tree small.

type PolicyRule struct {
	APIGroups []string `json:"apiGroups"`
	Resources []string `json:"resources"`
	Verbs     []string `json:"verbs"`
}

type StrategyDeploymentPermissions struct {
	ServiceAccountName string       `json:"serviceAccountName"`
	Rules              []PolicyRule `json:"rules"`
}

type InstallStrategySpec struct {
	ClusterPermissions []StrategyDeploymentPermissions `json:"clusterPermissions,omitempty"`
	Deployments        []StrategyDeploymentSpec        `json:"deployments"`
}

type NamedInstallStrategy struct {
	Strategy string              `json:"strategy"`
	Spec     InstallStrategySpec `json:"spec"`
}

type StrategyDeploymentSpec struct {
	Name  string            `json:"name"`
	Label map[string]string `json:"label,omitempty"`
	Spec  DeploymentSpec    `json:"spec"`
}

type DeploymentSpec struct {
	Replicas int32           `json:"replicas,omitempty"`
	Selector *LabelSelector  `json:"selector,omitempty"`
	Template PodTemplateSpec `json:"template"`
}

type LabelSelector struct {
	MatchLabels map[string]string `json:"matchLabels,omitempty"`
}

type PodTemplateSpec struct {
	Metadata PodMetadata `json:"metadata,omitempty"`
	Spec     PodSpec     `json:"spec"`
}

type PodMetadata struct {
	Labels map[string]string `json:"labels,omitempty"`
}

type PodSpec struct {
	Containers                    []Container         `json:"containers"`
	ServiceAccountName            string              `json:"serviceAccountName,omitempty"`
	SecurityContext               *PodSecurityContext `json:"securityContext,omitempty"`
	TerminationGracePeriodSeconds *int64              `json:"terminationGracePeriodSeconds,omitempty"`
}

type PodSecurityContext struct {
	RunAsNonRoot *bool `json:"runAsNonRoot,omitempty"`
}

type Container struct {
	Name            string               `json:"name"`
	Image           string               `json:"image"`
	ImagePullPolicy string               `json:"imagePullPolicy,omitempty"`
	Command         []string             `json:"command,omitempty"`
	Args            []string             `json:"args,omitempty"`
	Resources       ResourceRequirements `json:"resources,omitempty"`
	SecurityContext *SecurityContext     `json:"securityContext,omitempty"`
	LivenessProbe   *Probe               `json:"livenessProbe,omitempty"`
	ReadinessProbe  *Probe               `json:"readinessProbe,omitempty"`
}

type ResourceRequirements struct {
	Limits   map[string]string `json:"limits,omitempty"`
	Requests map[string]string `json:"requests,omitempty"`
}

type SecurityContext struct {
	AllowPrivilegeEscalation *bool         `json:"allowPrivilegeEscalation,omitempty"`
	Capabilities             *Capabilities `json:"capabilities,omitempty"`
}

type Capabilities struct {
	Drop []string `json:"drop,omitempty"`
}

type Probe struct {
	HTTPGet             *HTTPGetAction `json:"httpGet,omitempty"`
	InitialDelaySeconds int            `json:"initialDelaySeconds,omitempty"`
	PeriodSeconds       int            `json:"periodSeconds,omitempty"`
}

type HTTPGetAction struct {
	Path string `json:"path"`
	Port int    `json:"port"`
}

type CRDDescription struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Kind        string `json:"kind"`
	DisplayName string `json:"displayName,omitempty"`
	Description string `json:"description,omitempty"`
}

type CustomResourceDefinitions struct {
	Owned    []CRDDescription `json:"owned,omitempty"`
	Required []CRDDescription `json:"required,omitempty"`
}

type InstallMode struct {
	Type      string `json:"type"`
	Supported bool   `json:"supported"`
}

type Link struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

type Maintainer struct {
	Name  string `json:"name"`
	Email string `json:"email,omitempty"`
}

type NamedEntity struct {
	Name string `json:"name"`
}

type CSVSpec struct {
	DisplayName               string                    `json:"displayName"`
	Description               string                    `json:"description"`
	Keywords                  []string                  `json:"keywords,omitempty"`
	Links                     []Link                    `json:"links,omitempty"`
	Maintainers               []Maintainer              `json:"maintainers,omitempty"`
	Maturity                  string                    `json:"maturity,omitempty"`
	Provider                  NamedEntity               `json:"provider,omitempty"`
	Version                   string                    `json:"version"`
	CustomResourceDefinitions CustomResourceDefinitions `json:"customresourcedefinitions,omitempty"`
	InstallModes              []InstallMode             `json:"installModes"`
	Install                   NamedInstallStrategy      `json:"install"`
}

type CSVMetadata struct {
	Name        string            `json:"name"`
	Namespace   string            `json:"namespace,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

type ClusterServiceVersion struct {
	APIVersion string      `json:"apiVersion"`
	Kind       string      `json:"kind"`
	Metadata   CSVMetadata `json:"metadata"`
	Spec       CSVSpec     `json:"spec"`
}

// ─── Main ─────────────────────────────────────────────────────────────────────

func main() {
	csvVersion := flag.String("csv-version", "", "Version for the ClusterServiceVersion (required)")
	namespace := flag.String("namespace", "openshift-cnv", "Target namespace for the operator")
	operatorImage := flag.String("operator-image", "quay.io/openshift-virtualization/virt-platform-autopilot:latest", "Operator container image reference")
	operatorVersion := flag.String("operator-version", "", "Operator version string (defaults to csv-version)")
	pullPolicy := flag.String("pull-policy", "IfNotPresent", "Image pull policy")
	dumpCRDs := flag.Bool("dump-crds", false, "Dump owned CRDs (virt-platform-autopilot owns none; flag accepted for pipeline compatibility)")
	flag.Parse()

	if *csvVersion == "" {
		fmt.Fprintln(os.Stderr, "error: --csv-version is required")
		flag.Usage()
		os.Exit(1)
	}
	if *operatorVersion == "" {
		*operatorVersion = *csvVersion
	}

	// Derive RBAC rules from the embedded assets FS.
	// This guarantees the CSV always reflects the actual managed resources.
	rules, err := rbac.AllRules(assets.EmbeddedFS)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error generating RBAC rules: %v\n", err)
		os.Exit(1)
	}

	csv := buildCSV(*csvVersion, *namespace, *operatorImage, *operatorVersion, *pullPolicy, rules)

	data, err := yaml.Marshal(csv)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error marshalling CSV: %v\n", err)
		os.Exit(1)
	}

	fmt.Print(string(data))

	// --dump-crds: virt-platform-autopilot intentionally owns no CRDs (zero API surface).
	// Accept the flag for pipeline compatibility but produce no additional output.
	_ = dumpCRDs
}

// buildCSV constructs the ClusterServiceVersion for virt-platform-autopilot.
func buildCSV(csvVersion, namespace, operatorImage, operatorVersion, pullPolicy string, rules []rbac.Rule) ClusterServiceVersion {
	falseVal := false
	trueVal := true
	gracePeriod := int64(10)

	labels := map[string]string{
		"app":           "virt-platform-autopilot",
		"control-plane": "controller-manager",
	}

	permissions := buildClusterPermissions(rules)

	return ClusterServiceVersion{
		APIVersion: "operators.coreos.com/v1alpha1",
		Kind:       "ClusterServiceVersion",
		Metadata: CSVMetadata{
			Name:      fmt.Sprintf("virt-platform-autopilot.v%s", csvVersion),
			Namespace: namespace,
			Annotations: map[string]string{
				"capabilities":   "Basic Install",
				"categories":     "Virtualization",
				"description":    "Automatically configures OpenShift platform settings for optimal KubeVirt/OpenShift Virtualization performance.",
				"repository":     "https://github.com/openshift-virtualization/virt-platform-autopilot",
				"containerImage": operatorImage,
			},
		},
		Spec: CSVSpec{
			DisplayName: "KubeVirt Platform Autopilot",
			Description: `KubeVirt Platform Autopilot automatically configures OpenShift platform settings
for optimal KubeVirt/OpenShift Virtualization performance.

It watches the HyperConverged custom resource and applies platform-level configuration
such as KubeletConfig, MachineConfig, KubeDescheduler settings, and Prometheus alert
rules based on the cluster's hardware capabilities and the desired virtualization profile.

The operator has zero API surface: it introduces no new CRDs and is fully controlled
through the existing HyperConverged resource.`,
			Keywords: []string{"kubevirt", "virtualization", "platform", "performance", "openshift"},
			Maturity: "alpha",
			Version:  operatorVersion,
			Provider: NamedEntity{Name: "Red Hat"},
			Links: []Link{
				{Name: "Source Code", URL: "https://github.com/openshift-virtualization/virt-platform-autopilot"},
			},
			Maintainers: []Maintainer{
				{Name: "KubeVirt Team", Email: "kubevirt-dev@redhat.com"},
			},
			// virt-platform-autopilot owns no CRDs (zero API surface by design).
			// It requires the HyperConverged CRD, which is owned by HCO itself.
			CustomResourceDefinitions: CustomResourceDefinitions{
				Required: []CRDDescription{
					{
						Name:        "hyperconvergeds.hco.kubevirt.io",
						Version:     "v1beta1",
						Kind:        "HyperConverged",
						DisplayName: "HyperConverged",
						Description: "HyperConverged is the configuration API for the KubeVirt ecosystem.",
					},
				},
			},
			InstallModes: []InstallMode{
				{Type: "OwnNamespace", Supported: true},
				{Type: "SingleNamespace", Supported: false},
				{Type: "MultiNamespace", Supported: false},
				{Type: "AllNamespaces", Supported: false},
			},
			Install: NamedInstallStrategy{
				Strategy: "deployment",
				Spec: InstallStrategySpec{
					ClusterPermissions: permissions,
					Deployments: []StrategyDeploymentSpec{
						{
							Name:  "virt-platform-autopilot",
							Label: labels,
							Spec: DeploymentSpec{
								Replicas: 1,
								Selector: &LabelSelector{MatchLabels: labels},
								Template: PodTemplateSpec{
									Metadata: PodMetadata{Labels: labels},
									Spec: PodSpec{
										ServiceAccountName:            "virt-platform-autopilot",
										TerminationGracePeriodSeconds: &gracePeriod,
										SecurityContext: &PodSecurityContext{
											RunAsNonRoot: &trueVal,
										},
										Containers: []Container{
											{
												Name:            "manager",
												Image:           operatorImage,
												ImagePullPolicy: pullPolicy,
												Command:         []string{"/manager"},
												Args: []string{
													"--leader-elect",
													fmt.Sprintf("--namespace=%s", namespace),
												},
												SecurityContext: &SecurityContext{
													AllowPrivilegeEscalation: &falseVal,
													Capabilities:             &Capabilities{Drop: []string{"ALL"}},
												},
												Resources: ResourceRequirements{
													Limits:   map[string]string{"cpu": "500m", "memory": "512Mi"},
													Requests: map[string]string{"cpu": "100m", "memory": "128Mi"},
												},
												LivenessProbe: &Probe{
													HTTPGet:             &HTTPGetAction{Path: "/healthz", Port: 8082},
													InitialDelaySeconds: 15,
													PeriodSeconds:       20,
												},
												ReadinessProbe: &Probe{
													HTTPGet:             &HTTPGetAction{Path: "/readyz", Port: 8082},
													InitialDelaySeconds: 5,
													PeriodSeconds:       10,
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
}

// buildClusterPermissions converts pkg/rbac Rules to OLM PolicyRules under the
// virt-platform-autopilot service account.
func buildClusterPermissions(rules []rbac.Rule) []StrategyDeploymentPermissions {
	policyRules := make([]PolicyRule, 0, len(rules))
	for _, r := range rules {
		policyRules = append(policyRules, PolicyRule{
			APIGroups: r.APIGroups,
			Resources: r.Resources,
			Verbs:     r.Verbs,
		})
	}
	return []StrategyDeploymentPermissions{
		{
			ServiceAccountName: "virt-platform-autopilot",
			Rules:              policyRules,
		},
	}
}
