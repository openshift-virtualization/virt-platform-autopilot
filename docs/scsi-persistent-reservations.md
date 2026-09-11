# SCSI Persistent Reservations for shared LUN disks

This Developer Preview feature configures the host multipath daemon for virtual
machines that share a LUN-backed disk and use SCSI persistent reservations, such
as Windows Server Failover Clusters (WSFC).

See: https://access.redhat.com/solutions/7106114

## Prerequisite: configure the multipath reservation-key file

All OpenShift worker nodes that can host these VMs must configure
`reservation_key file` in the multipath `defaults` section. This causes multipath
to persist reservation keys across the node configuration required for shared-disk
workloads. See [Red Hat Knowledgebase solution 7106114](https://access.redhat.com/solutions/7106114).

The feature configures workers and also configures masters only when they are
schedulable, because those masters can host the affected VMs.

Enable the DP feature on the HyperConverged CR:

```bash
oc annotate hyperconverged kubevirt-hyperconverged -n openshift-cnv \
  platform.kubevirt.io/enable-scsi-persistent-reservations=true
```

Autopilot creates the following MachineConfig for the worker pool (and an
equivalent `99-master-multipath-reservation-key` MachineConfig when masters are
schedulable):

```yaml
apiVersion: machineconfiguration.openshift.io/v1
kind: MachineConfig
metadata:
  name: 99-worker-multipath-reservation-key
  labels:
    machineconfiguration.openshift.io/role: worker
spec:
  config:
    ignition:
      version: 3.5.0
    storage:
      files:
      - contents:
          source: data:text/plain;charset=utf-8;base64,ZGVmYXVsdHMgewogICAgcmVzZXJ2YXRpb25fa2V5IGZpbGUKfQo=
        filesystem: root
        mode: 0644
        overwrite: true
        path: /etc/multipath/conf.d/reservation.conf
```

The Machine Config Operator rolls out the change and reboots affected workers.
Wait until the worker MachineConfigPool has completed its update before restarting
the VMs and retrying shared-disk validation:

```bash
oc get mcp worker
oc debug node/<worker-node> -- chroot /host cat /etc/multipath/conf.d/reservation.conf
```

The file should contain:

```text
defaults {
    reservation_key file
}
```

## Existing SCSI-3 PR workloads

If a `virt-handler` restart occurs while a VM using SCSI-3 Persistent
Reservations is running, the VM can lose connectivity to `qemu-pr-helper` and
WSFC validation can fail. Restart affected VMs after the Machine Config Operator
rollout (or any `virt-handler` restart) to restore that connection before
retrying validation. This behavior is tracked in CNV-55559.

For diagnosis, a failed reservation due to an unset multipath key commonly logs
`configured reservation key doesn't match: 0x0`; failure to find a newly
discovered SCSI device commonly reports `ENOENT` from `qemu-pr-helper`.
