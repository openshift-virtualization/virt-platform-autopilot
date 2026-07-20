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
  {{- if .Topology.IsAWS }}
  storageClassName: gp3-csi
  {{- else if .Topology.IsAzure }}
  storageClassName: managed-csi
  {{- else if .Topology.IsGCP }}
  storageClassName: standard-csi
  {{- else if .Topology.IsBareMetal }}
  storageClassName: lvms-vg1
  {{- else if .Topology.IsVSphere }}
  storageClassName: thin-csi
  {{- end }}
  tenants:
    mode: openshift-logging
  limits:
    global:
      retention:
        days: 7
  managementState: Managed
