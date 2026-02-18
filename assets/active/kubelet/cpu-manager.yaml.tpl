apiVersion: machineconfiguration.openshift.io/v1
kind: KubeletConfig
metadata:
  name: virt-cpu-manager
spec:
  kubeletConfig:
    # CPU Manager for pinned workloads
    cpuManagerPolicy: static
    cpuManagerPolicyOptions:
      full-pcpus-only: "true"
    cpuManagerReconcilePeriod: 5s
    reservedSystemCPUs: "0-1"
    # Topology Manager for NUMA awareness (required for VM pinning)
    topologyManagerPolicy: best-effort
    # Memory Manager for static memory allocation (required for VM pinning)
    memoryManagerPolicy: Static
    # Reserved memory for NUMA node 0 (adjust based on host size)
    reservedMemory:
      - numaNode: 0
        limits:
          memory: "1124Mi"
  machineConfigPoolSelector:
    matchLabels:
      pools.operator.machineconfiguration.openshift.io/worker: ""
