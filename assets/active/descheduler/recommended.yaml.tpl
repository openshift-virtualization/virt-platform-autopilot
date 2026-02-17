{{- $crdName := "kubedeschedulers.operator.openshift.io" -}}
{{- $profilesPath := "spec.profiles" -}}
{{- $preferredProfile := "" -}}
{{- $needsEvictionsInBackground := false -}}
{{- if crdHasEnum $crdName $profilesPath "KubeVirtRelieveAndMigrate" -}}
  {{- $preferredProfile = "KubeVirtRelieveAndMigrate" -}}
{{- else if crdHasEnum $crdName $profilesPath "DevKubeVirtRelieveAndMigrate" -}}
  {{- $preferredProfile = "DevKubeVirtRelieveAndMigrate" -}}
{{- else if crdHasEnum $crdName $profilesPath "LongLifecycle" -}}
  {{- $preferredProfile = "LongLifecycle" -}}
  {{- $needsEvictionsInBackground = true -}}
{{- else -}}
  {{- $preferredProfile = "DevKubeVirtRelieveAndMigrate" -}}
{{- end -}}
{{- $devActualUtilizationProfile := "" -}}
{{- if prometheusRuleHasRecordingRule "openshift-kube-descheduler-operator" "descheduler-rules" "descheduler:node:linear_amplified_ideal_point_positive_distance:k3:avg1m" -}}
  {{- $devActualUtilizationProfile = "PrometheusCPUMemoryCombinedProfile" -}}
{{- else if objectExists "PrometheusRule" "openshift-kube-descheduler-operator" "descheduler-rules" -}}
  {{- $devActualUtilizationProfile = "PrometheusCPUCombined" -}}
{{- end -}}
apiVersion: operator.openshift.io/v1
kind: KubeDescheduler
metadata:
  name: cluster
  namespace: openshift-kube-descheduler-operator
spec:
  managementState: Managed
  mode: Automatic
  deschedulingIntervalSeconds: 60
  profiles:
    - {{ $preferredProfile }}
  {{- if or $needsEvictionsInBackground $devActualUtilizationProfile }}
  profileCustomizations:
    {{- if $needsEvictionsInBackground }}
    devEnableEvictionsInBackground: true
    {{- end }}
    {{- if $devActualUtilizationProfile }}
    devActualUtilizationProfile: {{ $devActualUtilizationProfile }}
    {{- end }}
  {{- end }}
  evictionLimits:
    {{- $migTotal := dig "spec" "liveMigrationConfig" "parallelMigrationsPerCluster" 5 .HCO.Object }}
    total: {{ $migTotal }}
    {{- $migNode := dig "spec" "liveMigrationConfig" "parallelOutboundMigrationsPerNode" 2 .HCO.Object }}
    node: {{ $migNode }}
