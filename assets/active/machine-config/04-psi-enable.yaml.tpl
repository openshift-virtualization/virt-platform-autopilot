apiVersion: machineconfiguration.openshift.io/v1
kind: MachineConfig
metadata:
  name: 99-openshift-machineconfig-{{ .Params.role }}-psi-karg
  labels:
    machineconfiguration.openshift.io/role: {{ .Params.role }}
    platform.kubevirt.io/managed-by: virt-platform-autopilot
spec:
  kernelArguments:
    - psi=1
