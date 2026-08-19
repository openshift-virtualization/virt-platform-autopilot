apiVersion: machineconfiguration.openshift.io/v1
kind: MachineConfig
metadata:
  labels:
    machineconfiguration.openshift.io/role: {{ .Params.role }}
  name: 99-{{ .Params.role }}-thp-tuning
spec:
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
          Description=Configure THP madvise mode and khugepaged scan rate
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
