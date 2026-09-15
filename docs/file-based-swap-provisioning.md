# File-based swap provisioning

A dedicated `OCPSWAP` partition on its own device remains the recommended way to
give workers swap: it keeps swap I/O off the root filesystem and its capacity is
planned at install time. This Developer Preview feature is the cheaper
alternative for clusters where that partition is not available — it is opt-in,
optional, and stands aside automatically on any worker that does have the
partition.

One caveat applies to the partition today, not to this feature.
`swap-disk-enable.service` in the always-on Swap Enablement MachineConfig
carries `ConditionFirstBoot=no`, and a scaled-up worker whose Ignition config
already matches the pool's rendered config never reboots after its first boot.
Such a node therefore runs without partition-backed swap until something else
reboots it, which can be a long time. This feature's drop-in resets that
condition for the file-swap activation unit, so file-backed swap is active from
the first boot. Removing the condition from `swap-disk-enable.service` means
editing an always-on MachineConfig and rolling every worker pool, so it is left
for the next change that touches that asset.

When enabled, it provisions `/var/tmp/ocpswap.file` on workers for KubeVirt
memory overcommitment. It calculates the file size independently on each node as
its RAM multiplied by the configured overcommit amount:

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
`/var/tmp` against three limits:

- The replacement is built alongside any file already in place, so `/var/tmp`
  must have the full calculated swap size free at that moment.
- The finished state must still leave one swap size free once the old file is
  released, so the total requirement is twice the calculated size. When
  replacing an inactive swap file, this second check counts the space that
  deleting the old file releases.
- At least 15% of the filesystem must remain free afterwards. The two limits
  above scale with the swap file and say nothing about the size of the disk,
  while the kubelet's eviction thresholds are relative to the filesystem
  (`nodefs.available<10%`, `imagefs.available<15%` by default) and `/var/tmp`
  shares that filesystem with the container image store. Without this limit a
  swap file could satisfy both size checks and still leave the node permanently
  under `DiskPressure`, evicting pods and garbage-collecting images.

All three safeguards run before anything is removed. A node that cannot accommodate a
larger swap file therefore keeps the working swap file it already has rather
than ending up with none, and the service reports the reason:

```bash
oc debug node/<worker-node> -- chroot /host journalctl -u swap-provision.service
```

Because the requirement is twice the swap size, large-memory workers are the
least likely to qualify: a 512 GiB node at 150% asks for 256 GiB of swap and so
needs 512 GiB free on the root filesystem, which also holds container images and
logs. On such nodes the feature does nothing, and the only signal is the journal
entry above — the outcome is node-local and not reported to the cluster. Check
it after enabling the feature or after raising `memoryOvercommitPercentage`.

Provisioning is ordered before kubelet dependencies but is not a hard kubelet
dependency: a capacity or allocation failure is recorded in the journal without
preventing the node from starting kubelet.

## Storage placement

File-backed swap shares a physical device with the root filesystem, so swap I/O
and the I/O of kubelet, CRI-O and container images contend by construction. The
always-on Swap Enablement feature already sets `io.latency` targets that give
`system.slice` a 5 ms budget and `kubepods.slice` a 50 ms one, and it discovers
the root device as well as dedicated swap devices, so the protection does apply
here. It bounds the contention rather than removing it. A dedicated `OCPSWAP`
partition on a separate device remains the better option where latency matters;
provisioning stands aside automatically when that partition is present.

Workers that place `/var` on a separate device are supported: the provisioning
unit declares `RequiresMountsFor=/var/tmp`, so it waits for whichever mount backs
that path — local or network-attached — and does not run at all if the mount is
missing, rather than writing the swap file into the unmounted directory on the
parent filesystem.

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
          WantedBy=kubelet-dependencies.target
```

`WantedBy` rather than `RequiredBy` is deliberate, matching
`swap-provision.service`. `swapoff` has to move every swapped-out page back into
RAM and can fail on a node that is short of memory — precisely the node that is
using swap. Under `RequiredBy` that failure would fail
`kubelet-dependencies.target`, and with it the kubelet, turning a cleanup
problem into an unschedulable node. Under `WantedBy` the failure is recorded in
the journal and the node still starts; check
`journalctl -u file-swap-cleanup.service` when verifying.

Apply the MachineConfig, wait for the worker pool to update, verify that the
file is gone, and then delete the temporary MachineConfig. Deleting it does not
recreate the removed file. Delete it before re-enabling file-based swap
provisioning.

## Security considerations

The swap file is created with `0600` permissions, which prevents non-root local
users from reading it. Swap can contain VM and container memory, however, so
those permissions do not protect against an offline reader of the worker's boot
disk or a disk snapshot.

`/var/tmp` is world-writable, so any local process can create a file at
`/var/tmp/ocpswap.file` before the service first runs. The service therefore
treats root ownership as part of the file's identity: a file of the right size
carrying a swap signature is reused only when it is owned by `root:root` with
mode `0600`. Anything else is replaced rather than activated, so a file planted
by another user is never turned into swap — which would otherwise have given its
creator read and write access to swapped-out guest memory. The replacement is
staged as `/var/tmp/ocpswap.file.new`, created with `O_EXCL` so a file or
symbolic link appearing at that path is refused instead of followed, and under
`umask 077` so it is never briefly world-readable at a predictable path — a
descriptor opened in such a window would survive the rename and read live swap.
The staged file is then renamed into place.

The file is also labelled `swapfile_t` rather than inheriting the label of
`/var/tmp`. This is best effort: a node whose policy does not know the type logs
a warning and keeps the inherited label, which is what the activation unit has
always consumed, rather than being left without swap. Verify the label and a
successful `swapon` under enforcing SELinux when validating the feature on a new
platform:

```bash
oc debug node/<worker-node> -- chroot /host ls -Z /var/tmp/ocpswap.file
oc debug node/<worker-node> -- chroot /host ausearch -m AVC -ts boot
```

When the worker boot disk is encrypted, `/var/tmp/ocpswap.file` inherits that
at-rest encryption. On an unencrypted boot disk, this Developer Preview feature
does not add a separate encryption layer for swap. Clusters with a physical-media
or disk-snapshot threat model should use encrypted worker storage, or provision
their own encrypted swap device. See [Encrypting block devices using LUKS](https://docs.redhat.com/en/documentation/red_hat_enterprise_linux/9/html/managing_storage_devices/encrypting-block-devices-using-luks_managing-storage-devices).

Adding encrypted file-backed swap would require a per-boot key and a managed
dm-crypt mapping, and is intentionally out of scope for this initial feature.

Changing `memoryOvercommitPercentage` changes the desired swap size and updates
the provisioning MachineConfig. Plan that change as a worker-pool rollout. At
the following boot, the service validates the existing file's type, ownership,
permissions, and size; it recreates it when they do not match the calculated
configuration. A new file is built alongside the current one and renamed into
place, so `/var/tmp/ocpswap.file` is always either the previous complete file or
the new one, never a half-written stub for the activation unit to `swapon`. When
the percentage is reduced to 100 or below, the file is removed before the
activation unit runs.

Resizing never disturbs swap on a running node. `swap-provision.service` runs
only at boot and is ordered before `ocpswap-file-enable.service`, so it always
acts on a file that has not been activated yet; its `/proc/swaps` checks only
come into play if an administrator runs the script by hand, where they produce a
clear error instead of an obscure one. The guarantee itself comes from the
kernel, which refuses to unlink or rename an active swap file (`EPERM`, via the
`S_SWAPFILE` inode flag), so no invocation of the script can pull swap out from
under a running node. Deactivating swap remains a deliberate administrative
action — see the cleanup procedure above.
