apiVersion: operator.openshift.io/v1
kind: KubeDescheduler
metadata:
  name: cluster
  namespace: openshift-kube-descheduler-operator
spec:
  mode: Automatic
  deschedulingIntervalSeconds: 60
  profiles:
    - DevKubeVirtRelieveAndMigrate
  {{- if has "WithHostPassthroughCPU" (.HCO.spec.featureGates | default list) }}
  profileCustomizations:
    devDeviationThresholds: AsymmetricLow
  {{- end }}
  evictorPodLimits:
    {{- $migTotal := dig "spec" "liveMigrationConfig" "parallelMigrationsPerCluster" 5 .HCO }}
    total: {{ $migTotal }}
    {{- $migNode := dig "spec" "liveMigrationConfig" "parallelOutboundMigrationsPerNode" 2 .HCO }}
    node: {{ $migNode }}
