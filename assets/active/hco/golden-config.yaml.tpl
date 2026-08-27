apiVersion: hco.kubevirt.io/v1
kind: HyperConverged
metadata:
  name: kubevirt-hyperconverged
  namespace: {{ dig "metadata" "namespace" "openshift-cnv" .HCO.Object }}
  annotations:
    platform.kubevirt.io/managed-by: virt-platform-autopilot
    platform.kubevirt.io/version: "1.0.0"
{{- if objectFieldBool "sriovnetwork.openshift.io/v1" "SriovOperatorConfig" "sriov-network-operator" "default" "spec.enableInjector" }}
spec:
  deployment:
    deployNetworkResourcesInjector: false
{{- else }}
spec: {}
{{- end }}

