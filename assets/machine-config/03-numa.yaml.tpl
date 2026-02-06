{{- if .Hardware.NUMANodesPresent }}
apiVersion: machineconfiguration.openshift.io/v1
kind: MachineConfig
metadata:
  name: 50-virt-numa
  labels:
    machineconfiguration.openshift.io/role: worker
spec:
  config:
    ignition:
      version: 3.2.0
    storage:
      files:
        - path: /etc/kubernetes/openshift-workload-pinning
          mode: 0644
          overwrite: true
          contents:
            source: data:text/plain;charset=utf-8;base64,eyAKICAibWFuYWdlbWVudCI6IHsKICAgICJjcHVzZXQiOiAiMC0xLDUyLTUzIgogIH0KfQo=
{{- end }}
