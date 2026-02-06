apiVersion: machineconfiguration.openshift.io/v1
kind: KubeletConfig
metadata:
  name: virt-perf-settings
spec:
  kubeletConfig:
    nodeStatusMaxImages: -1
    {{- $maxPods := dig "spec" "infra" "nodePlacement" "maxPods" 500 .HCO }}
    maxPods: {{ $maxPods }}
  machineConfigPoolSelector:
    matchLabels:
      pools.operator.machineconfiguration.openshift.io/worker: ""
