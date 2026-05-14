apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: network-resources-injector-role-binding
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: network-resources-injector
subjects:
- kind: ServiceAccount
  name: network-resources-injector-sa
  namespace: kube-system
