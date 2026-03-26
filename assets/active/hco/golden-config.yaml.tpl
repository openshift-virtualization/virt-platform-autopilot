apiVersion: hco.kubevirt.io/v1beta1
kind: HyperConverged
metadata:
  name: kubevirt-hyperconverged
  namespace: {{ dig "metadata" "namespace" "openshift-cnv" .HCO.Object }}
  annotations:
    platform.kubevirt.io/managed-by: virt-platform-autopilot
    platform.kubevirt.io/version: "1.0.0"
spec:
  # Opinionated defaults for production virtualization workloads

  # Control plane tuning - HighBurst for better control plane performance (CNV-69442)
  # according to https://access.redhat.com/articles/6994974
  tuningPolicy: highBurst

  # Note: VM-level performance defaults (networkInterfaceMultiqueue, ioThreadsPolicy, etc.)
  # should be configured via instanceTypes/templates or VirtualMachine specs.
  # See CNV performance recommendations for details.
