apiVersion: machineconfiguration.openshift.io/v1
kind: MachineConfig
metadata:
  labels:
    machineconfiguration.openshift.io/role: {{ .Params.role }}
    platform.kubevirt.io/managed-by: virt-platform-autopilot
  name: 90-{{ .Params.role }}-vsock-local
spec:
  config:
    ignition:
      version: 3.5.0
    storage:
      files:
      - contents:
          source: data:text/plain;charset=utf-8;base64,{{ readAsset "machine-config/10-vsock-local/modules-load.conf" | b64enc }}
        mode: 420
        overwrite: true
        path: /etc/modules-load.d/90-virt-vsock.conf
      - contents:
          source: data:text/plain;charset=utf-8;base64,{{ readAsset "machine-config/10-vsock-local/sysctl.conf" | b64enc }}
        mode: 420
        overwrite: true
        path: /etc/sysctl.d/90-virt-vsock.conf
