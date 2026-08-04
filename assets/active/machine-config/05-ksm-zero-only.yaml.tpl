apiVersion: machineconfiguration.openshift.io/v1
kind: MachineConfig
metadata:
  labels:
    machineconfiguration.openshift.io/role: worker
  name: 99-worker-ksm-zero-only
spec:
  config:
    ignition:
      version: 3.5.0
    storage:
      files:
      - contents:
          compression: gzip
          source: data:;base64,{{ readAsset "machine-config/05-ksm-zero-only/ksm-tune.py" | gzip | b64enc }}
        mode: 493
        overwrite: true
        path: /usr/local/bin/kubevirt-ksm-tune.py
    systemd:
      units:
      - contents: |
          [Unit]
          Description=Configure and enable KSM zero-pages-only mode
          After=sys-kernel-mm-ksm.mount

          [Service]
          Type=oneshot
          ExecStart=/usr/local/bin/kubevirt-ksm-tune.py
          RemainAfterExit=yes
          StandardOutput=journal
          StandardError=journal

          [Install]
          WantedBy=multi-user.target
        enabled: true
        name: kubevirt-ksm-tune.service
