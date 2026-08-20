{{- $envOverrides := dig "metadata" "annotations" "platform.kubevirt.io/kubevirt-metrics-exporter-env" "{}" .HCO.Object | fromJson }}
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
              value: {{ index $envOverrides "ENABLE_QMP" | default "true" | quote }}
            - name: ENABLE_QGA
              value: {{ index $envOverrides "ENABLE_QGA" | default "true" | quote }}
            - name: QGA_POLL_INTERVAL
              value: {{ index $envOverrides "QGA_POLL_INTERVAL" | default "1m" | quote }}
            - name: ENABLE_EBPF
              value: {{ index $envOverrides "ENABLE_EBPF" | default "true" | quote }}
            - name: ENABLE_EBPF_BLOCK
              value: {{ index $envOverrides "ENABLE_EBPF_BLOCK" | default "true" | quote }}
            - name: ENABLE_EBPF_NFS
              value: {{ index $envOverrides "ENABLE_EBPF_NFS" | default "true" | quote }}
            - name: ENABLE_EBPF_NFS_KPROBE
              value: {{ index $envOverrides "ENABLE_EBPF_NFS_KPROBE" | default "false" | quote }}
            - name: ENABLE_KVM
              value: {{ index $envOverrides "ENABLE_KVM" | default "true" | quote }}
            - name: KVM_POLL_INTERVAL
              value: {{ index $envOverrides "KVM_POLL_INTERVAL" | default "30s" | quote }}
            - name: ENABLE_CGROUP
              value: {{ index $envOverrides "ENABLE_CGROUP" | default "true" | quote }}
            - name: CGROUP_POLL_INTERVAL
              value: {{ index $envOverrides "CGROUP_POLL_INTERVAL" | default "30s" | quote }}
            - name: QMP_POLL_INTERVAL
              value: {{ index $envOverrides "QMP_POLL_INTERVAL" | default "1m" | quote }}
            - name: EBPF_SCAN_INTERVAL
              value: {{ index $envOverrides "EBPF_SCAN_INTERVAL" | default "30" | quote }}
            - name: LOG_LEVEL
              value: {{ index $envOverrides "LOG_LEVEL" | default "info" | quote }}
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
