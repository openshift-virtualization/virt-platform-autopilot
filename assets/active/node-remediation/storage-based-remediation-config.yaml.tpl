{{- $sc := dig "metadata" "annotations" "platform.kubevirt.io/sbr-storage-class" "" .HCO.Object -}}
{{- if not $sc }}{{ $sc = storageProfileRWXClass }}{{ end -}}
apiVersion: storage-based-remediation.medik8s.io/v1alpha1
kind: StorageBasedRemediationConfig
metadata:
  name: autopilot-recommended-values-detection-only
  namespace: openshift-workload-availability
spec:
  {{- if $sc }}
  sharedStorageClass: {{ $sc }}
  {{- end }}
  detectOnlyMode: Enabled
