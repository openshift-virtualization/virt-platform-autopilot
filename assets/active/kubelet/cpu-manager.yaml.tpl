apiVersion: machineconfiguration.openshift.io/v1
kind: KubeletConfig
metadata:
  name: virt-cpu-manager
spec:
  kubeletConfig:
    # CPU Manager for pinned workloads
    # according to https://access.redhat.com/articles/6994974
    cpuManagerPolicy: static
    # according to https://docs.redhat.com/en/documentation/openshift_container_platform/4.20/html/virtualization/postinstallation-configuration#virt-CPU-manager-policy_virt-perf-optimization
    cpuManagerPolicyOptions:
      full-pcpus-only: "true"
    cpuManagerReconcilePeriod: 5s
    reservedSystemCPUs: "0-1"
    # Reserve 1 CPU for Kubernetes system components (required for cluster stability
    # when full-pcpus-only is enabled per virt-perf-optimization docs)
    kubeReserved:
      cpu: "1"
    # Topology Manager for NUMA awareness (required for VM pinning)
    topologyManagerPolicy: best-effort
    # Memory Manager: memoryManagerPolicy Static is intentionally NOT set here.
    # The static Memory Manager requires reservedMemory to exactly equal the node's
    # total memory reservation (kubeReserved + systemReserved + eviction thresholds).
    # That total is computed dynamically per node by OCP's default auto-node-size
    # (kubelet-auto-node-size.service / autoSizingReserved), enabled by default on
    # worker nodes since OCP 4.21 (OCPNODE-3719, machine-config-operator#5390), so
    # no cluster-wide hardcoded reservedMemory can ever match it. A mismatch is fatal
    # and crash-loops the kubelet (CNV-96059). CPU pinning (cpuManagerPolicy: static
    # above) does not depend on the Memory Manager policy.
  machineConfigPoolSelector:
    matchLabels:
      pools.operator.machineconfiguration.openshift.io/worker: ""
