apiVersion: monitoring.coreos.com/v1
kind: PrometheusRule
metadata:
  name: virt-platform-autopilot-alerts
  namespace: openshift-cnv
  labels:
    app: virt-platform-autopilot
    prometheus: k8s
    role: alert-rules
spec:
  groups:
    - name: virt-platform-autopilot.critical
      interval: 30s
      rules:
        - alert: VirtPlatformSyncFailed
          # Core health indicator: Asset failed to apply for >15min
          # Expr: virt_platform_compliance_status == 0 (for > 15m)
          # 15m allows for transient API errors or slow rollouts (like MachineConfig)
          # If it persists longer, the automation is broken and requires attention
          expr: |
            virt_platform_compliance_status == 0
          for: 15m
          labels:
            severity: critical
            operator: virt-platform-autopilot
          annotations:
            summary: "virt-platform-autopilot failed to sync {{ $labels.kind }}/{{ $labels.name }}"
            description: |-
              virt-platform-autopilot has failed to apply the Golden State
              to {{ $labels.kind }}/{{ $labels.name }} in namespace {{ $labels.namespace }}
              for 15 minutes.

              This indicates the automation is broken and requires immediate attention.

              Current compliance status: {{ $value }}
              (0 = Drifted/Sync Failed, 1 = Synced)
            runbook_url: "https://github.com/kubevirt/virt-platform-autopilot/blob/main/docs/runbooks/VirtPlatformSyncFailed.md"

    - name: virt-platform-autopilot.warning
      interval: 30s
      rules:
        - alert: VirtPlatformThrashingDetected
          # Edit war indicator: Another controller or user is fighting our configuration
          # Expr: increase(virt_platform_thrashing_total[10m]) > 5
          # More than 5 thrashing events in 10 minutes indicates an active conflict
          # The operator has paused automation to protect the API server
          expr: |
            increase(virt_platform_thrashing_total[10m]) > 5
          labels:
            severity: warning
            operator: virt-platform-autopilot
          annotations:
            summary: "Edit war detected on {{ $labels.kind }}/{{ $labels.name }}"
            description: |-
              virt-platform-autopilot detected an "Edit War" on
              {{ $labels.kind }}/{{ $labels.name }} in namespace {{ $labels.namespace }}.

              Automation has been paused to protect the API server from thrashing.

              Thrashing events in last 10 minutes: {{ $value }}

              This indicates another controller or user is modifying the resource,
              conflicting with the autopilot's desired state.
            runbook_url: "https://github.com/kubevirt/virt-platform-autopilot/blob/main/docs/runbooks/VirtPlatformThrashingDetected.md"

        - alert: VirtPlatformDependencyMissing
          # Soft dependency indicator: Optional CRD is missing
          # Expr: virt_platform_missing_dependency == 1
          # Related platform features will not be configured until CRD is installed
          # This is a warning, not critical - cluster is functional but feature-incomplete
          expr: |
            virt_platform_missing_dependency == 1
          for: 5m
          labels:
            severity: warning
            operator: virt-platform-autopilot
          annotations:
            summary: "Missing optional CRD: {{ $labels.kind }}.{{ $labels.version }}.{{ $labels.group }}"
            description: |-
              virt-platform-autopilot detected that the optional CRD
              {{ $labels.kind }}.{{ $labels.version }}.{{ $labels.group }} is missing
              from the cluster.

              Related platform features (e.g., LoadAware Scheduling, Node Health Checks)
              will not be configured until this CRD is installed.

              If the related operator is not installed intentionally, you can silence
              this alert or opt-out via platform.kubevirt.io/mode: unmanaged annotation.
            runbook_url: "https://github.com/kubevirt/virt-platform-autopilot/blob/main/docs/runbooks/VirtPlatformDependencyMissing.md"
