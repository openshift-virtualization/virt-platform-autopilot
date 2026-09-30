# Logging setup

Before enabling `platform.kubevirt.io/enable-logging=true` on HyperConverged,
install the Loki Operator and Red Hat OpenShift Logging Operator, and create
`openshift-logging/logging-loki-storage` with the object storage configuration.
Autopilot does not install or enable NooBaa/MCG or create S3 buckets or credentials.

## Loki block storage

New LokiStacks use the cluster default StorageClass. If multiple defaults exist,
the newest wins (with the name as a deterministic tie-breaker). Cloud and bare-metal
clusters follow the same selection policy; bare metal does not require LVMS.

To select a different class, set this annotation on HyperConverged:

```bash
oc annotate hyperconverged kubevirt-hyperconverged -n openshift-cnv \
  platform.kubevirt.io/logging-storage-class=ocs-storagecluster-ceph-rbd --overwrite
```

The selected class must exist and support Loki's filesystem PVCs. This is block
storage for Loki's internal volumes, separate from the S3 bucket configured in
`logging-loki-storage`.

Without this annotation, an existing LokiStack keeps its configured class even
if the cluster default changes or that StorageClass is removed. Bound PVCs can
remain usable after their class is removed, so autopilot does not replace an
existing class automatically. An explicitly selected class must exist. If neither
an explicit class nor an existing class nor a default is available, reconciliation
reports how to configure storage. API discovery failures are also reported.

Changing the class on an existing stack requires a separate PVC migration:
existing PVCs and StatefulSet volume templates cannot be changed in place.
For stacks that never started because the class was missing, administrators can
recreate the affected StatefulSets and only the unbound, empty PVCs after selecting
a valid class. Bound volumes require a planned migration that preserves log data.
Autopilot does not delete or migrate volumes automatically.

The existing `platform.kubevirt.io/patch` override on LokiStack remains supported
and takes precedence over the rendered configuration. Keep it when upgrading;
the HCO annotation provides storage configuration before the initial stack exists.

## Collector permissions

Autopilot creates collector bindings for application and infrastructure log
collection and for writing to Loki. Audit collection is separately enabled by
`platform.kubevirt.io/enable-audit-logging=true`.

The Logging Operator supplies the referenced ClusterRoles. Autopilot's generated
installation permissions include an explicit `bind` allowlist restricted to
`collect-application-logs`, `collect-infrastructure-logs`, `collect-audit-logs`,
and `logging-collector-logs-writer`. Adding an asset that references another role
does not expand this allowlist. Both `config/rbac/role.yaml` and OLM
ClusterServiceVersion generation use these rules. Administrators do not need to
manually create collector bindings after enabling logging.

An upgrade must update the operator's installation RBAC as well as its image.
Existing manual collector bindings can coexist with autopilot's bindings. The
`bind` grants do not themselves enable audit collection or grant permission to
bind arbitrary ClusterRoles.

Verify the stack and forwarding independently:

```bash
oc get lokistack logging-loki -n openshift-logging
oc get clusterlogforwarder instance -n openshift-logging
oc get pvc -n openshift-logging
```

## Troubleshooting

The examples below assume the default operator and HCO namespace, `openshift-cnv`.
Adjust the namespace and HCO name if your installation uses different values.

Start with the controller logs and events attached to HyperConverged:

```bash
oc logs -n openshift-cnv deploy/virt-platform-autopilot -c manager --since=30m
oc get events -n openshift-cnv \
  --field-selector involvedObject.name=kubevirt-hyperconverged \
  --sort-by=.metadata.creationTimestamp
```

`RenderFailed` warnings identify an asset that could not be rendered, such as
`logging-lokistack` with invalid storage configuration. `ApplyFailed` warnings
report Kubernetes API rejections after rendering. The controller logs include
the asset name and underlying error, and failed reconciliations are retried.
Render failures happen before resource compliance metrics are updated; do not
rely on the existing per-resource sync-failure alert to detect every render error.

### Missing operators or disabled logging

```bash
oc get crd lokistacks.loki.grafana.com \
  clusterlogforwarders.observability.openshift.io
oc get hyperconverged kubevirt-hyperconverged -n openshift-cnv \
  -o jsonpath='{.metadata.annotations}{"\n"}'
```

For `CRDMissing`, install the Loki Operator or Red Hat OpenShift Logging Operator
that supplies the missing CRD. Confirm the `openshift-logging` namespace exists.
Ensure `platform.kubevirt.io/autopilot` is not `"false"`, and that
`platform.kubevirt.io/disabled-resources` does not exclude the logging resources.
Then enable logging:

```bash
oc annotate hyperconverged kubevirt-hyperconverged -n openshift-cnv \
  platform.kubevirt.io/enable-logging=true --overwrite
```

### No default or an invalid StorageClass

```bash
oc get storageclass
```

If `RenderFailed` reports no default class, choose an existing class suitable for
Loki's filesystem PVCs and set `platform.kubevirt.io/logging-storage-class` using
the command in [Loki block storage](#loki-block-storage), or ask the storage
administrator to configure a cluster default. If the annotation names a missing
class, replace its value with an existing class. Alternatively, remove the
annotation to use the existing LokiStack class or, for a new stack, the default:

```bash
oc annotate hyperconverged kubevirt-hyperconverged -n openshift-cnv \
  platform.kubevirt.io/logging-storage-class-
```

Removing the annotation does not replace an existing LokiStack's class, even if
that class is missing. Changing a stack's class is not a PVC migration. Inspect
the PVCs and their events, and plan any required migration with the storage
administrator. Do not delete bound volumes or PVCs to clear an error.

### Forbidden reads or collector binding failures

If logs report forbidden StorageClass reads or collector binding admission
failures, verify that the operator upgrade included both the new image and
installation RBAC. For manifest installations, apply the matching release's
RBAC manifests; for OLM installations, verify the installed CSV carries the
updated permissions. An image-only replacement leaves the old permissions.

A cluster administrator with impersonation permission can check the service
account's access without granting it broader privileges:
Replace the example StorageClass name with the class you selected.

```bash
oc auth can-i list storageclasses \
  --as=system:serviceaccount:openshift-cnv:virt-platform-autopilot
oc auth can-i watch storageclasses \
  --as=system:serviceaccount:openshift-cnv:virt-platform-autopilot
oc auth can-i get storageclasses/ocs-storagecluster-ceph-rbd \
  --as=system:serviceaccount:openshift-cnv:virt-platform-autopilot
oc auth can-i bind clusterroles/collect-application-logs \
  --as=system:serviceaccount:openshift-cnv:virt-platform-autopilot
```

Repeat the bind check for `collect-infrastructure-logs`, `collect-audit-logs`,
and `logging-collector-logs-writer`. The Logging Operator must also supply those
roles. Do not work around these failures by granting Autopilot `cluster-admin`
or unrestricted `bind` permissions.

### Resources exist but logging is not ready

Autopilot successfully applying an object does not guarantee that the logging
operators can make it ready. Inspect their status and PVC provisioning:

```bash
oc describe lokistack logging-loki -n openshift-logging
oc describe clusterlogforwarder instance -n openshift-logging
oc get pvc -n openshift-logging
oc get events -n openshift-logging --sort-by=.metadata.creationTimestamp
oc get secret logging-loki-storage -n openshift-logging -o name
```

If the Secret is missing, create it with the object-storage settings required by
the Loki Operator. If the status reports authentication, endpoint, TLS, or bucket
errors, correct those settings with the object-storage administrator. Check
credentials locally; do not paste Secret contents into logs, issues, or support
messages. PVC provisioning failures require checking the selected StorageClass
and storage provisioner rather than changing collector RBAC.
