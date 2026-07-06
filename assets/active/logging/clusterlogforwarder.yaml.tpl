{{- $auditEnabled := hasAnnotation .HCO.Object "platform.kubevirt.io/enable-audit-logging" "true" }}
apiVersion: observability.openshift.io/v1
kind: ClusterLogForwarder
metadata:
  name: instance
  namespace: openshift-logging
spec:
  serviceAccount:
    name: collector
  outputs:
    - name: default-lokistack
      type: lokiStack
      lokiStack:
        target:
          name: logging-loki
          namespace: openshift-logging
        authentication:
          token:
            from: serviceAccount
      tls:
        ca:
          key: service-ca.crt
          configMapName: openshift-service-ca.crt
  filters:
    - name: drop-debug-infra
      type: drop
      drop:
        - test:
            - field: .level
              matches: "debug|trace"
    - name: drop-health-probes
      type: drop
      drop:
        - test:
            - field: .message
              matches: '"GET /healthz" 200|"GET /readyz" 200|"GET /livez" 200'
    {{- if $auditEnabled }}
    - name: audit-drop-non-complete
      type: drop
      drop:
        - test:
            - field: .stage
              notMatches: ResponseComplete
    - name: audit-drop-system-users
      type: drop
      drop:
        - test:
            - field: .user.username
              matches: "^system:serviceaccount:"
        - test:
            - field: .user.username
              matches: "^system:node:"
        - test:
            - field: .user.username
              matches: "^system:(apiserver|kube-|anonymous|unauthenticated|openshift:|aggregator|monitoring|multus)"
    - name: audit-drop-noisy-verbs
      type: drop
      drop:
        - test:
            - field: .verb
              matches: "^(watch|deletecollection|proxy)$"
    {{- end }}
  pipelines:
    - name: infra-logs
      inputRefs:
        - infrastructure
      filterRefs:
        - drop-debug-infra
        - drop-health-probes
      outputRefs:
        - default-lokistack
    - name: app-logs
      inputRefs:
        - application
      filterRefs:
        - drop-health-probes
      outputRefs:
        - default-lokistack
    {{- if $auditEnabled }}
    - name: audit-logs
      inputRefs:
        - audit
      filterRefs:
        - audit-drop-non-complete
        - audit-drop-system-users
        - audit-drop-noisy-verbs
      outputRefs:
        - default-lokistack
    {{- end }}
