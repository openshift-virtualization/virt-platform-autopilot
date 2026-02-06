apiVersion: hco.kubevirt.io/v1beta1
kind: HyperConverged
metadata:
  name: kubevirt-hyperconverged
  namespace: {{ dig "metadata" "namespace" "openshift-cnv" .HCO.Object }}
  annotations:
    platform.kubevirt.io/managed-by: virt-platform-operator
    platform.kubevirt.io/version: "1.0.0"
spec:
  # Opinionated defaults for production virtualization workloads

  # Live migration configuration optimized for stability
  liveMigrationConfig:
    completionTimeoutPerGiB: 800
    parallelMigrationsPerCluster: 5
    parallelOutboundMigrationsPerNode: 2
    progressTimeout: 150

  # Feature gates for production readiness
  featureGates:
    - WithHostPassthroughCPU
    - HotplugVolumes
    - GPU
    - HostDevices
    - Snapshot
    - VMExport

  # Certificate rotation configuration
  certConfig:
    ca:
      duration: 48h
      renewBefore: 24h
    server:
      duration: 24h
      renewBefore: 12h

  # Infrastructure placement for operator components
  infra:
    nodePlacement:
      nodeSelector:
        node-role.kubernetes.io/worker: ""
      tolerations:
        - effect: NoSchedule
          key: node-role.kubernetes.io/infra
          operator: Exists

  # Workload placement for VM workloads
  workloads:
    nodePlacement:
      nodeSelector:
        node-role.kubernetes.io/worker: ""

  # Resource requirements for virt components
  resourceRequirements:
    vmiCPUAllocationRatio: 10

  # Storage configuration
  storageImport:
    insecureRegistries:
      - "registry.example.com"

  # Network configuration
  defaultCPUModel: "host-passthrough"
  defaultNetworkInterface: "masquerade"

  # Observability settings
  observability:
    enabled: true

  # High availability configuration
  highAvailability:
    interval: 30

  # Uninstall strategy
  uninstallStrategy: BlockUninstallIfWorkloadsExist
