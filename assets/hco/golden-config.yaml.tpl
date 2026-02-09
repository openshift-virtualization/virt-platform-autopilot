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
    objectGraph: false
    persistentReservation: false
    videoConfig: true

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
