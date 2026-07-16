{{- $incidentsEnabled := hasAnnotation .HCO.Object "platform.kubevirt.io/enable-incident-detection" "true" }}
apiVersion: observability.openshift.io/v1alpha1
kind: UIPlugin
metadata:
  name: monitoring
spec:
  type: Monitoring
  monitoring:
    perses:
      enabled: true
    {{- if $incidentsEnabled }}
    incidents:
      enabled: true
    {{- end }}
