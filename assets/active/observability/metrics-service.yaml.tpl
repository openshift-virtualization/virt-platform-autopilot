apiVersion: v1
kind: Service
metadata:
  name: virt-platform-autopilot-metrics
  namespace: {{ .HCO.GetNamespace | default "openshift-cnv" }}
  labels:
    app: virt-platform-autopilot
    app.kubernetes.io/name: virt-platform-autopilot
    app.kubernetes.io/component: autopilot
spec:
  selector:
    app: virt-platform-autopilot
    control-plane: controller-manager
  ports:
    - name: metrics
      port: 8080
      targetPort: 8080
      protocol: TCP
