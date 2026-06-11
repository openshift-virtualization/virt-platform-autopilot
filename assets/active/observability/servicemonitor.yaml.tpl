apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: virt-platform-autopilot-metrics
  namespace: {{ .HCO.GetNamespace | default "openshift-cnv" }}
  labels:
    app: virt-platform-autopilot
    app.kubernetes.io/name: virt-platform-autopilot
    app.kubernetes.io/component: autopilot
spec:
  selector:
    matchLabels:
      app.kubernetes.io/name: virt-platform-autopilot
      app.kubernetes.io/component: autopilot
  endpoints:
    - port: metrics
      interval: 30s
      path: /metrics
