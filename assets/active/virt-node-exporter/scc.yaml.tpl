apiVersion: security.openshift.io/v1
kind: SecurityContextConstraints
metadata:
  name: virt-node-exporter
  labels:
    app.kubernetes.io/name: virt-node-exporter
    app.kubernetes.io/component: virt-node-exporter
    app.kubernetes.io/managed-by: virt-platform-autopilot
allowPrivilegedContainer: false
allowPrivilegeEscalation: false
allowHostDirVolumePlugin: true
allowHostPID: true
allowHostIPC: false
allowHostNetwork: false
allowHostPorts: false
allowedCapabilities:
  - SYS_PTRACE
  - DAC_OVERRIDE
  - BPF
  - PERFMON
  - SYS_RESOURCE
requiredDropCapabilities:
  - ALL
readOnlyRootFilesystem: true
runAsUser:
  type: MustRunAs
  uid: 0
seLinuxContext:
  type: MustRunAs
fsGroup:
  type: RunAsAny
supplementalGroups:
  type: RunAsAny
volumes:
  - hostPath
  - emptyDir
  - projected
  - secret
  - configMap
  - downwardAPI
{{- $ns := dig "metadata" "namespace" "openshift-cnv" .HCO.Object }}
users:
  - system:serviceaccount:{{ $ns }}:virt-node-exporter
