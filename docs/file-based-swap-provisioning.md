# File-based swap provisioning

This Developer Preview feature provisions `/var/tmp/ocpswap.file` on workers
for KubeVirt memory overcommitment. It calculates the file size independently on
each node as its RAM multiplied by the configured overcommit amount:

```text
swap size = node RAM × (memoryOvercommitPercentage - 100) / 100
```

The feature creates and formats the swap file only. The always-on **Swap
Enablement** feature remains the sole owner of `swapon`; its activation unit is
ordered after provisioning, avoiding a first-boot race or duplicate `swapon`.
If a worker has the dedicated `OCPSWAP` partition, provisioning skips the file
and removes an inactive file that this feature had created previously.

## Enable provisioning

Configure a memory overcommit percentage greater than 100 and enable the
feature on the HyperConverged CR:

```bash
oc patch hyperconverged kubevirt-hyperconverged -n openshift-cnv --type merge \
  -p '{"spec":{"virtualization":{"higherWorkloadDensity":{"memoryOvercommitPercentage":150}}}}'
oc annotate hyperconverged kubevirt-hyperconverged -n openshift-cnv \
  platform.kubevirt.io/enable-file-based-swap-provisioning=true
```

Autopilot creates `90-worker-file-based-swap-provisioning`. It adds an opt-in
systemd drop-in that makes the existing file-swap activation unit wait for
`swap-provision.service`; the always-on Swap Enablement MachineConfig is not
changed for clusters that do not opt in. The drop-in also enables provisioning
and activation on a newly provisioned worker's first boot.
The Machine Config Operator applies the new MachineConfig and reboots affected
workers once.

## Capacity safeguard

Before allocating the file, `swap-provision.service` checks free capacity on
`/var/tmp`. It requires at least twice the calculated swap size, leaving enough
space for the filesystem after the allocation. When replacing an inactive swap
file, the check includes the space that deleting the old file releases. If the
check fails, the service removes any mismatched inactive file so that it cannot
be activated with the wrong configuration; inspect the reason with:

```bash
oc debug node/<worker-node> -- chroot /host journalctl -u swap-provision.service
```

Provisioning is ordered before kubelet dependencies but is not a hard kubelet
dependency: a capacity or allocation failure is recorded in the journal without
preventing the node from starting kubelet.

## Disable and clean up file-backed swap

Removing the opt-in annotation removes the provisioning MachineConfig, but does
not remove an existing `/var/tmp/ocpswap.file`. Remove the file before a later
reboot, otherwise the always-on Swap Enablement feature can activate it again.

For each worker, cordon and drain the node using the process appropriate for the
workloads it hosts, then deactivate and remove the file:

```bash
oc adm cordon <worker-node>
oc adm drain <worker-node> --ignore-daemonsets --delete-emptydir-data
oc debug node/<worker-node> -- chroot /host sh -c \
  'swapoff /var/tmp/ocpswap.file && rm -f /var/tmp/ocpswap.file'
oc adm uncordon <worker-node>
```

Remove the opt-in annotation after the file is removed from every affected
worker:

```bash
oc annotate hyperconverged kubevirt-hyperconverged -n openshift-cnv \
  platform.kubevirt.io/enable-file-based-swap-provisioning-
```

Verify that no file-backed swap remains:

```bash
oc debug node/<worker-node> -- chroot /host cat /proc/swaps
```

### Optional one-shot cleanup MachineConfig

For a worker pool that is already being rolled out, administrators can use the
following temporary MachineConfig instead of manually running the cleanup
command on each node. It is deliberately not managed by Autopilot because it
performs a destructive, one-time action.

Remove the file-based-swap-provisioning opt-in annotation before applying this
MachineConfig. Otherwise its cleanup unit can race with the provisioning unit
and the file can be recreated. Applying this MachineConfig triggers a worker
pool rollout, and deleting it can trigger another one; use the manual procedure
when those additional rollouts are not acceptable.

```yaml
apiVersion: machineconfiguration.openshift.io/v1
kind: MachineConfig
metadata:
  name: 99-worker-file-swap-cleanup
  labels:
    machineconfiguration.openshift.io/role: worker
spec:
  config:
    ignition:
      version: 3.5.0
    systemd:
      units:
      - name: file-swap-cleanup.service
        enabled: true
        contents: |
          [Unit]
          Description=Remove obsolete file-backed swap
          ConditionPathExists=/var/tmp/ocpswap.file
          Before=ocpswap-file-enable.service
          Before=kubelet-dependencies.target

          [Service]
          Type=oneshot
          ExecStart=/bin/sh -c 'if grep -Fq -- /var/tmp/ocpswap.file /proc/swaps; then swapoff /var/tmp/ocpswap.file || exit $?; fi; rm -f /var/tmp/ocpswap.file'

          [Install]
          RequiredBy=kubelet-dependencies.target
```

Apply the MachineConfig, wait for the worker pool to update, verify that the
file is gone, and then delete the temporary MachineConfig. Deleting it does not
recreate the removed file. Delete it before re-enabling file-based swap
provisioning.

## Security considerations

The swap file is created with `0600` permissions, which prevents non-root local
users from reading it. Swap can contain VM and container memory, however, so
those permissions do not protect against an offline reader of the worker's boot
disk or a disk snapshot.

When the worker boot disk is encrypted, `/var/tmp/ocpswap.file` inherits that
at-rest encryption. On an unencrypted boot disk, this Developer Preview feature
does not add a separate encryption layer for swap. Clusters with a physical-media
or disk-snapshot threat model should use encrypted worker storage, or provision
their own encrypted swap device. See [Encrypting block devices using LUKS](https://docs.redhat.com/en/documentation/red_hat_enterprise_linux/9/html/managing_storage_devices/encrypting-block-devices-using-luks_managing-storage-devices).

Adding encrypted file-backed swap would require a per-boot key and a managed
dm-crypt mapping, and is intentionally out of scope for this initial feature.

Changing `memoryOvercommitPercentage` changes the desired swap size and updates
the provisioning MachineConfig. Plan that change as a worker-pool rollout. At
the following boot, the service validates the existing file's type, permissions,
and size; it recreates it when they do not match the calculated configuration.
When the percentage is reduced to 100 or below, it removes an inactive swap file
before the activation unit runs. It refuses to replace or remove an already active
swap file, protecting a running node
from an unexpected `swapoff`.
