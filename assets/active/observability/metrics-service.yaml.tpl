apiVersion: v1
kind: Service
metadata:
  name: virt-platform-autopilot-metrics
  namespace: {{ .HCO.GetNamespace | default "openshift-cnv" }}
  labels:
    app: virt-platform-autopilot
    app.kubernetes.io/name: virt-platform-autopilot
    app.kubernetes.io/component: autopilot
  annotations:
    # Requests a TLS serving certificate from the OpenShift service-ca operator,
    # mounted into the operator pod to serve metrics over HTTPS (mTLS).
    service.beta.openshift.io/serving-cert-secret-name: virt-platform-autopilot-metrics-tls
spec:
  selector:
    app: virt-platform-autopilot
    control-plane: controller-manager
  ports:
    - name: metrics
      port: 8443
      targetPort: 8443
      protocol: TCP
