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
  tuningPolicy: highBurst

  # Live migration configuration optimized for stability
  liveMigrationConfig:
    allowAutoConverge: false
    allowPostCopy: false
    completionTimeoutPerGiB: 150
    parallelMigrationsPerCluster: 5
    parallelOutboundMigrationsPerNode: 2
    progressTimeout: 150

  # Feature gates for production readiness
  featureGates:
    alignCPUs: false
    decentralizedLiveMigration: false
    declarativeHotplugVolumes: false
    deployKubeSecondaryDNS: false
    disableMDevConfiguration: false
    downwardMetrics: false
    enableMultiArchBootImageImport: false
    persistentReservation: false

  # Certificate rotation configuration
  certConfig:
    ca:
      duration: 48h0m0s
      renewBefore: 24h0m0s
    server:
      duration: 24h0m0s
      renewBefore: 12h0m0s

  # Resource requirements for virt components
  resourceRequirements:
    vmiCPUAllocationRatio: 10

  # Uninstall strategy
  uninstallStrategy: BlockUninstallIfWorkloadsExist

  # Note: VM-level performance defaults (networkInterfaceMultiqueue, ioThreadsPolicy, etc.)
  # should be configured via instanceTypes/templates or VirtualMachine specs.
  # See CNV performance recommendations for details.
