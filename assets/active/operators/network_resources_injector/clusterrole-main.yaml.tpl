apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: network-resources-injector
rules:
- apiGroups:
  - ""
  - k8s.cni.cncf.io
  - extensions
  - apps
  resources:
  - replicationcontrollers
  - replicasets
  - daemonsets
  - statefulsets
  - pods
  - network-attachment-definitions
  verbs:
  - create
  - delete
  - get
  - list
  - patch
  - update
  - watch
