package main

import "flag"

type configuration struct {
	csvVersion          string
	namespace           string
	operatorImage       string
	operatorVersion     string
	pullPolicy          string
	additionalImages    string
	dumpNetworkPolicies bool
}

var config configuration

func init() {
	flag.StringVar(&config.csvVersion, "csv-version", "", "Version for the ClusterServiceVersion (required)")
	flag.StringVar(&config.namespace, "namespace", "openshift-cnv", "Target namespace for the operator")
	flag.StringVar(&config.operatorImage, "operator-image", "quay.io/openshift-virtualization/virt-platform-autopilot:latest", "Operator container image reference")
	flag.StringVar(&config.operatorVersion, "operator-version", "", "Operator version string (defaults to csv-version)")
	flag.StringVar(&config.pullPolicy, "pull-policy", "IfNotPresent", "Image pull policy")
	flag.StringVar(&config.additionalImages, "additional-images", "", "Comma-separated list of ENVKEY:image pairs to inject as environment variables")
	flag.BoolVar(&config.dumpNetworkPolicies, "dump-network-policies", false, "Dump network policies as part of the output, or use common labels is applicable")
	_ = flag.Bool("dump-crds", false, "Dump owned CRDs (virt-platform-autopilot owns none; flag accepted for pipeline compatibility)")
}
