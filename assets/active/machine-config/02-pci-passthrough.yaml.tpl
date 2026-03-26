{{- if or .Hardware.PCIDevicesPresent .Hardware.GPUPresent }}
apiVersion: machineconfiguration.openshift.io/v1
kind: MachineConfig
metadata:
  name: 50-virt-pci-passthrough
  labels:
    machineconfiguration.openshift.io/role: worker
spec:
  kernelArguments:
  # according to https://docs.redhat.com/en/documentation/openshift_container_platform/4.20/html/hardware_networks/configuring-sriov-device#nw-sriov-configuring-device_configuring-sriov-device
    - intel_iommu=on
    - iommu=pt
{{- end }}
