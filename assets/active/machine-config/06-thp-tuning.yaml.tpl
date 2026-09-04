apiVersion: machineconfiguration.openshift.io/v1
kind: MachineConfig
metadata:
  labels:
    machineconfiguration.openshift.io/role: {{ .Params.role }}
  name: 99-{{ .Params.role }}-thp-tuning
spec:
  # Experimental: kernelcore caps slab/non-movable memory for mixed workloads
  # to minimize memory fragmentation by scattered unmovable blocks.
  # Formula intent: max(2GB, 2% MemTotal). Requires node reboot.
  kernelArguments:
    - kernelcore=2G
  config:
    ignition:
      version: 3.5.0
    storage:
      files:
      - contents:
          compression: gzip
          source: data:;base64,{{ readAsset "machine-config/06-thp-tuning/thp-tune.py" | gzip | b64enc }}
        mode: 493
        overwrite: true
        path: /usr/local/bin/kubevirt-thp-tune.py
    systemd:
      units:
      - contents: |
          [Unit]
          Description=Configure THP madvise/defrag mode, khugepaged scan rate, and max_ptes_none
          After=sys-kernel-mm-transparent_hugepage.mount

          [Service]
          Type=oneshot
          ExecStart=/usr/local/bin/kubevirt-thp-tune.py
          RemainAfterExit=yes
          StandardOutput=journal
          StandardError=journal

          [Install]
          WantedBy=multi-user.target
        enabled: true
        name: kubevirt-thp-tune.service
