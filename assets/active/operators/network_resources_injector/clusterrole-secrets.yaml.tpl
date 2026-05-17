apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: network-resources-injector-secrets
rules:
- apiGroups:
  - ""
  resources:
  - secrets
  verbs:
  - create
  - delete
  - get
  - list
  - patch
  - update
  - watch
