apiVersion: machineconfiguration.openshift.io/v1
kind: KubeletConfig
metadata:
  name: virt-perf-settings
spec:
  kubeletConfig:
    # Prevent uneven scheduling based on image count (BZ#1984442)
    nodeStatusMaxImages: -1
    {{- $maxPods := dig "spec" "infra" "nodePlacement" "maxPods" 500 .HCO.Object }}
    maxPods: {{ $maxPods }}
    # Auto-size kubelet reserved resources (will be OCP default per RFE-8045)
    autoSizingReserved: true
    # Allow swap to be enabled later without reconfiguration
    failSwapOn: false
  machineConfigPoolSelector:
    matchLabels:
      pools.operator.machineconfiguration.openshift.io/worker: ""
