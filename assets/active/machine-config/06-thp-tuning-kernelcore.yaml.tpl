apiVersion: machineconfiguration.openshift.io/v1
kind: MachineConfig
metadata:
  labels:
    machineconfiguration.openshift.io/role: {{ .Params.role }}
  name: 99-{{ .Params.role }}-thp-tuning-kernelcore
spec:
  # Optional sub-feature: ZONE_MOVABLE split for mixed OCP + VM worker nodes.
  # max(2G, 3% MemTotal) non-movable pool per node at boot:
  #   kernelcore=2G     — floor for slab/page tables (see kernel.org kernelcore=)
  #   movablecore=97%   — scales kernel share to 3% on large nodes (movablecore=
  #                       complement; kernelcore is at least 2G but may be more)
  # Requires node reboot. Opt in separately from thp-tuning sysfs tuning.
  kernelArguments:
    - kernelcore=2G
    - movablecore=97%
  config:
    ignition:
      version: 3.5.0
