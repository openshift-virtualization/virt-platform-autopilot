apiVersion: machineconfiguration.openshift.io/v1
kind: KubeletConfig
metadata:
  name: virt-cpu-manager
spec:
  kubeletConfig:
    cpuManagerPolicy: static
    cpuManagerPolicyOptions:
      full-pcpus-only: "true"
    cpuManagerReconcilePeriod: 5s
    reservedSystemCPUs: "0-1"
  machineConfigPoolSelector:
    matchLabels:
      pools.operator.machineconfiguration.openshift.io/worker: ""
