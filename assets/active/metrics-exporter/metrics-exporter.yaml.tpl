apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: kubevirt-metrics-exporter
  namespace: kubevirt-metrics-exporter
  labels:
    app: kubevirt-metrics-exporter
spec:
  selector:
    matchLabels:
      app: kubevirt-metrics-exporter
  updateStrategy:
    type: RollingUpdate
    rollingUpdate:
      maxUnavailable: 1
  template:
    metadata:
      labels:
        app: kubevirt-metrics-exporter
    spec:
      serviceAccountName: kubevirt-metrics-exporter
      hostPID: true
      nodeSelector:
        node-role.kubernetes.io/worker: ""
      tolerations:
        - operator: Exists
      containers:
        - name: exporter
          image: {{ index .Images "kubevirt-metrics-exporter" }}
          env:
            - name: NODE_NAME
              valueFrom:
                fieldRef:
                  fieldPath: spec.nodeName
            - name: ENABLE_QMP
              value: "true"
            - name: ENABLE_QGA
              value: "true"
            - name: QGA_POLL_INTERVAL
              value: "1m"
            - name: ENABLE_EBPF
              value: "true"
            - name: ENABLE_EBPF_BLOCK
              value: "true"
            - name: ENABLE_EBPF_NFS
              value: "true"
            - name: ENABLE_EBPF_NFS_KPROBE
              value: "false"
            - name: QMP_POLL_INTERVAL
              value: "1m"
            - name: EBPF_SCAN_INTERVAL
              value: "30"
            - name: LOG_LEVEL
              value: "info"
          ports:
            - name: metrics
              containerPort: 8080
              protocol: TCP
          volumeMounts:
            - name: cri-socket
              mountPath: /run/crio/crio.sock
              readOnly: true
            - name: sys
              mountPath: /sys
              readOnly: true
              mountPropagation: HostToContainer
          resources:
            requests:
              cpu: 50m
              memory: 64Mi
            limits:
              cpu: 200m
              memory: 256Mi
          livenessProbe:
            httpGet:
              path: /healthz
              port: metrics
            initialDelaySeconds: 10
            periodSeconds: 30
          readinessProbe:
            httpGet:
              path: /healthz
              port: metrics
            initialDelaySeconds: 5
            periodSeconds: 10
          securityContext:
            allowPrivilegeEscalation: false
            readOnlyRootFilesystem: true
            runAsUser: 0
            capabilities:
              add:
                - SYS_PTRACE
                - DAC_OVERRIDE
                - BPF
                - PERFMON
                - SYS_RESOURCE
              drop:
                - ALL
      volumes:
        - name: cri-socket
          hostPath:
            path: /run/crio/crio.sock
        - name: sys
          hostPath:
            path: /sys
            type: Directory
