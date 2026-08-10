{{- if .Topology.HasSchedulableMasters }}
apiVersion: machineconfiguration.openshift.io/v1
kind: MachineConfig
metadata:
  name: 99-openshift-machineconfig-master-psi-karg
  labels:
    machineconfiguration.openshift.io/role: master
    platform.kubevirt.io/managed-by: virt-platform-autopilot
spec:
  kernelArguments:
    - psi=1
{{- end }}
