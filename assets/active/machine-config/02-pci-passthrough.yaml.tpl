{{- if or .Hardware.PCIDevicesPresent .Hardware.GPUPresent }}
apiVersion: machineconfiguration.openshift.io/v1
kind: MachineConfig
metadata:
  name: 50-virt-pci-passthrough
  labels:
    machineconfiguration.openshift.io/role: worker
spec:
  kernelArguments:
    - intel_iommu=on
    - iommu=pt
{{- end }}
