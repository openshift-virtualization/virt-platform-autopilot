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
          source: data:text/plain;charset=utf-8;base64,{{- printf `
apiVersion: kubelet.config.k8s.io/v1beta1
kind: KubeletConfiguration

# Prevent uneven scheduling based on image count (BZ#1984442)
# according to https://access.redhat.com/articles/6994974
nodeStatusMaxImages: 500

{{- $maxPods := dig "spec" "deployment" "nodePlacements" "infra" "maxPods" 500 .HCO.Object }}
maxPods: {{ $maxPods }}

# Auto-size kubelet reserved resources. Default-enabled on worker nodes since
# OCP 4.21 (OCPNODE-3719, machine-config-operator#5390).
autoSizingReserved: true
` | b64enc }}
        mode: 420
