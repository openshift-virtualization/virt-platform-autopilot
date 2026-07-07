apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: virt-node-exporter
  labels:
    app.kubernetes.io/name: virt-node-exporter
    app.kubernetes.io/component: virt-node-exporter
    app.kubernetes.io/managed-by: virt-platform-autopilot
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: virt-node-exporter
{{- $ns := dig "metadata" "namespace" "openshift-cnv" .HCO.Object }}
subjects:
  - kind: ServiceAccount
    name: virt-node-exporter
    namespace: {{ $ns }}
