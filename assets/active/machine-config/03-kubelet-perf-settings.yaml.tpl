apiVersion: machineconfiguration.openshift.io/v1
kind: MachineConfig
metadata:
  labels:
    machineconfiguration.openshift.io/role: worker
  name: 95-worker-kubelet-perf-settings
spec:
  config:
    ignition:
      version: 3.5.0
    storage:
      files:
      - path: /etc/openshift/kubelet.conf.d/95-perf.conf
        overwrite: true
        contents:
          # Evaluate dig outside the printf string: text/template does not
          # expand actions inside raw/quoted string literals.
          source: data:text/plain;charset=utf-8;base64,{{ readAsset "machine-config/03-kubelet-perf-settings/95-perf.conf" | b64enc }}
        mode: 420
