{{- /*
  Merges nodeExporter.collectors.ksmd into the existing cluster-monitoring-config.
  Reads the live ConfigMap, deep-merges our settings, and outputs the full CM.
  Admin customizations (prometheusK8s, alertmanager, etc.) are preserved.

  TODO: The cluster-monitoring-config ConfigMap is being replaced by the
  ClusterMonitoring CRD (configv1alpha1). When that API goes GA, migrate
  this asset to manage the CR instead. See https://redhat.atlassian.net/browse/MON-3630
*/ -}}
{{- $liveYAML := getConfigMapData "openshift-monitoring" "cluster-monitoring-config" "config.yaml" }}
{{- $live := dict }}
{{- if $liveYAML }}
  {{- $live = fromYaml $liveYAML }}
{{- end }}
{{- $ksmdConfig := dict "nodeExporter" (dict "collectors" (dict "ksmd" (dict "enabled" true))) }}
{{- $merged := mergeOverwrite $live $ksmdConfig }}
apiVersion: v1
kind: ConfigMap
metadata:
  name: cluster-monitoring-config
  namespace: openshift-monitoring
data:
  config.yaml: |
{{ toYaml $merged | indent 4 }}
