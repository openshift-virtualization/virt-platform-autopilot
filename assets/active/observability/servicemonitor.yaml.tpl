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
      scheme: https
      interval: 30s
      path: /metrics
      # Native mTLS: Prometheus authenticates with its metrics client cert and
      # verifies the server via the service-ca CA bundle. These file paths are
      # mounted into the in-cluster prometheus-k8s pod by the monitoring operator.
      tlsConfig:
        caFile: /etc/prometheus/configmaps/serving-certs-ca-bundle/service-ca.crt
        certFile: /etc/prometheus/secrets/metrics-client-certs/tls.crt
        keyFile: /etc/prometheus/secrets/metrics-client-certs/tls.key
        serverName: virt-platform-autopilot-metrics.{{ .HCO.GetNamespace | default "openshift-cnv" }}.svc
