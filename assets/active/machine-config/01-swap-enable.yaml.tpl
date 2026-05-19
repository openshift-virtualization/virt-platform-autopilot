apiVersion: machineconfiguration.openshift.io/v1
kind: MachineConfig
metadata:
  labels:
    machineconfiguration.openshift.io/role: worker
  name: 90-worker-swap-online
spec:
  config:
    ignition:
      version: 3.5.0
    storage:
      files:
      - path: /etc/openshift/kubelet.conf.d/90-swap.conf
        overwrite: true
        contents:
          source: data:text/plain;charset=utf-8;base64,{{ readAsset "machine-config/01-swap-enable/kubelet-90-swap.conf" | b64enc }}
        mode: 420
      - contents:
          compression: gzip
          source: data:;base64,{{ readAsset "machine-config/01-swap-enable/kubevirt-tune-watermarks.py" | gzip | b64enc }}
        mode: 493
        overwrite: true
        path: /usr/local/bin/kubevirt-tune-watermarks.py
      - contents:
          compression: gzip
          source: data:;base64,{{ readAsset "machine-config/01-swap-enable/kubevirt-io-latency-setup.py" | gzip | b64enc }}
        mode: 493
        overwrite: true
        path: /usr/local/bin/kubevirt-io-latency-setup.py
    systemd:
      units:
      - contents: |
          [Unit]
          Description=Enable swap
          ConditionFirstBoot=no
          ConditionPathExists=/dev/disk/by-partlabel/OCPSWAP

          [Service]
          Type=oneshot
          ExecStart=/bin/sh -c "sudo swapon --priority 100 /dev/disk/by-partlabel/OCPSWAP"

          [Install]
          RequiredBy=kubelet-dependencies.target
        enabled: true
        name: swap-disk-enable.service
      - contents: |
          [Unit]
          Description=Enable OCP file swap
          ConditionFirstBoot=no
          ConditionPathExists=/var/tmp/ocpswap.file

          [Service]
          Type=oneshot
          ExecStart=/bin/sh -c "sudo swapon --priority 10 /var/tmp/ocpswap.file"

          [Install]
          RequiredBy=kubelet-dependencies.target
        enabled: true
        name: ocpswap-file-enable.service
      - contents: |
          [Unit]
          Description=KubeVirt adaptive watermark tuning for swap optimization
          After=kubelet.service

          [Service]
          Type=oneshot
          ExecStart=/usr/local/bin/kubevirt-tune-watermarks.py
          RemainAfterExit=true
          StandardOutput=journal
          StandardError=journal

          [Install]
          WantedBy=multi-user.target
        enabled: true
        name: kubevirt-tune-watermarks.service
      - contents: |
          [Unit]
          Description=KubeVirt IO latency protection for swap devices
          After=local-fs.target swap.target
          Wants=swap.target

          [Service]
          Type=oneshot
          ExecStart=/usr/local/bin/kubevirt-io-latency-setup.py
          RemainAfterExit=true
          StandardOutput=journal
          StandardError=journal

          [Install]
          WantedBy=multi-user.target
        enabled: true
        name: kubevirt-io-latency-setup.service
      - contents: |
          [Unit]
          Description=Remove legacy OCI hook configuration
          ConditionPathExists=/run/containers/oci/hooks.d/swap-for-burstable.json

          [Service]
          Type=oneshot
          ExecStart=/bin/sh -c "rm -f /run/containers/oci/hooks.d/swap-for-burstable.json"

          [Install]
          RequiredBy=kubelet-dependencies.target
        enabled: true
        name: remove-swap-for-burstable-hook.service
      - contents: |
          [Unit]
          Description=Remove legacy OCI hook swap script
          ConditionPathExists=/opt/oci-hook-swap.sh

          [Service]
          Type=oneshot
          ExecStart=/bin/sh -c "rm -f /opt/oci-hook-swap.sh"

          [Install]
          RequiredBy=kubelet-dependencies.target
        enabled: true
        name: remove-oci-hook-swap.service
      - dropins:
          - contents: |
              [Slice]
              MemorySwapMax=0
              IOWeight=800
              CPUWeight=800
            name: 10-kubevirt-protect.conf
        name: system.slice
      - dropins:
          - contents: |
              [Slice]
              IOWeight=100
            name: 10-kubevirt-io-priority.conf
        name: kubepods.slice
