apiVersion: machineconfiguration.openshift.io/v1
kind: MachineConfig
metadata:
  labels:
    machineconfiguration.openshift.io/role: {{ .Params.role }}
    platform.kubevirt.io/managed-by: virt-platform-autopilot
  name: 99-{{ .Params.role }}-multipath-reservation-key
spec:
  config:
    ignition:
      version: 3.5.0
    storage:
      files:
      - contents:
          source: data:text/plain;charset=utf-8;base64,{{ readAsset "machine-config/08-scsi-persistent-reservations/reservation.conf" | b64enc }}
        filesystem: root
        mode: 420
        overwrite: true
        path: /etc/multipath/conf.d/reservation.conf
