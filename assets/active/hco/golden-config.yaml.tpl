apiVersion: hco.kubevirt.io/v1beta1
kind: HyperConverged
metadata:
  name: kubevirt-hyperconverged
  namespace: {{ dig "metadata" "namespace" "openshift-cnv" .HCO.Object }}
  annotations:
    platform.kubevirt.io/managed-by: virt-platform-autopilot
    platform.kubevirt.io/version: "1.0.0"
spec: {}

