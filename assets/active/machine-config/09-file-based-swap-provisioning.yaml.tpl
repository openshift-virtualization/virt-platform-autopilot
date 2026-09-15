apiVersion: machineconfiguration.openshift.io/v1
kind: MachineConfig
metadata:
  labels:
    machineconfiguration.openshift.io/role: worker
    platform.kubevirt.io/managed-by: virt-platform-autopilot
  name: 90-worker-file-based-swap-provisioning
spec:
  config:
    ignition:
      version: 3.5.0
    storage:
      files:
      - contents:
          compression: gzip
          source: data:;base64,{{ readAsset "machine-config/09-file-based-swap-provisioning/kubevirt-provision-file-swap.sh" | gzip | b64enc }}
        mode: 493
        overwrite: true
        path: /usr/local/bin/kubevirt-provision-file-swap.sh
    systemd:
      units:
      # The base swap-enable MachineConfig remains unchanged for clusters that
      # do not opt into provisioning. This drop-in serializes its activation
      # unit with the provisioning unit on opted-in workers only.
      - name: ocpswap-file-enable.service
        dropins:
        - name: 10-wait-for-provisioning.conf
          contents: |
            [Unit]
            # Reset the condition inherited from the always-on unit so a newly
            # provisioned worker can activate the file swap on its first boot,
            # then restore the file-exists safeguard.
            ConditionFirstBoot=
            ConditionPathExists=/var/tmp/ocpswap.file
            After=swap-provision.service
      - contents: |
          [Unit]
          Description=Provision file-backed swap for KubeVirt memory overcommitment
          After=local-fs.target
          # The swap file lives on /var, which is always its own mount unit on
          # RHCOS (the OSTree bind mount) and can be placed on a separate device
          # at install time, including a network-attached one that belongs to
          # remote-fs.target rather than local-fs.target. RequiresMountsFor pulls
          # in whichever mount unit backs the path and orders after it, so a
          # multi-GiB swap file is never written into an unmounted directory on
          # the parent filesystem. Ordering after local-fs.target alone neither
          # covers a remote /var nor requires the mount to have succeeded.
          RequiresMountsFor=/var/tmp
          Before=ocpswap-file-enable.service
          Before=kubelet-dependencies.target

          [Service]
          Type=oneshot
          ExecStart=/usr/local/bin/kubevirt-provision-file-swap.sh {{ dig "spec" "virtualization" "higherWorkloadDensity" "memoryOvercommitPercentage" 100 .HCO.Object }}

          [Install]
          WantedBy=kubelet-dependencies.target
        enabled: true
        name: swap-provision.service
