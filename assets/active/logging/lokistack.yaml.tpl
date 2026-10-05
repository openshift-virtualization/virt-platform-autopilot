{{- $sc := dig "metadata" "annotations" "platform.kubevirt.io/logging-storage-class" "" .HCO.Object -}}
{{- if not $sc }}{{ $sc = objectField "loki.grafana.com/v1" "LokiStack" "openshift-logging" "logging-loki" "spec" "storageClassName" }}{{ end -}}
{{- if not $sc }}{{ $sc = defaultStorageClass }}{{ end -}}
apiVersion: loki.grafana.com/v1
kind: LokiStack
metadata:
  name: logging-loki
  namespace: openshift-logging
spec:
  {{- if .Topology.IsHCP }}
  size: 1x.extra-small
  {{- else if le .Topology.TotalNodeCount 3 }}
  size: 1x.pico
  {{- else if le .Topology.TotalNodeCount 10 }}
  size: 1x.extra-small
  {{- else }}
  size: 1x.medium
  {{- end }}
  storage:
    schemas:
      - version: v13
        effectiveDate: "2024-10-01"
    secret:
      name: logging-loki-storage
      {{- if .Topology.IsAzure }}
      type: azure
      {{- else if .Topology.IsGCP }}
      type: gcs
      {{- else }}
      type: s3
      {{- end }}
  {{- if $sc }}
  storageClassName: {{ $sc | quote }}
  {{- end }}
  tenants:
    mode: openshift-logging
  limits:
    global:
      retention:
        days: 7
  managementState: Managed
