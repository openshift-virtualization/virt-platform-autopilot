# VSOCK local namespace mode

This GA feature is enabled by default and sets `net.vsock.child_ns_mode = local`
at boot on workers and schedulable masters
(`Scheduler/cluster.spec.mastersSchedulable`). The host's initial network
namespace (`init_net`) stays in global mode. Its newly created child namespaces
use local VSOCK mode; existing Pods are unaffected. Custom pools selecting
worker MachineConfigs also inherit the setting.

Local mode gives each new Pod network namespace its own VSOCK CID space. VMs in
different Pods can then use the same CID without a host-wide collision. Without
this setting, child namespaces inherit global mode and share CID allocation.

## Compatibility

RHCOS provides the VSOCK `child_ns_mode` and `ns_mode` interfaces. KubeVirt
must support VSOCK network namespaces. Autopilot does not validate the
installed KubeVirt version, so opt out before deploying or upgrading Autopilot
if that support is unavailable. If affected nodes run other VSOCK workloads
or have competing sysctl settings, check compatibility.

## Rollout and verification

Autopilot creates `90-worker-vsock-local` and, when masters are schedulable,
`90-master-vsock-local`. Each MachineConfig installs:

| Node file | Content |
| --- | --- |
| `/etc/modules-load.d/90-virt-vsock.conf` | `vsock` |
| `/etc/sysctl.d/90-virt-vsock.conf` | `net.vsock.child_ns_mode = local` |

The MachineConfig loads `vsock` at boot to expose `net.vsock.child_ns_mode`
for `systemd-sysctl`. It does not preload `vhost_vsock`; the kernel loads that
module on demand when KubeVirt opens `/dev/vhost-vsock`.

This asset uses MCO's normal rebooting rollout. Draining the node and
restarting kubelet alone would not run the boot-time module and sysctl setup.
The kernel also locks `child_ns_mode` after its first write, so a kubelet
restart cannot change an already selected mode. A reboot applies the setting
before new Pod network namespaces are created and resets it during rollback.
See the [kernel's VSOCK sysctl documentation](https://github.com/torvalds/linux/blob/master/Documentation/admin-guide/sysctl/net.rst#child_ns_mode).

Wait for the affected MachineConfigPools to complete rollout and confirm nodes
rebooted. Custom disruption policies may require a separate supported reboot.
Initial creation can trigger rollout and node reboots immediately; opt out
before deploying or upgrading Autopilot to prevent this. Later updates follow
the [coalescing policy](machineconfig-rollout-coalescing.md). Single-node
reboots interrupt VMs.

Under `/proc/sys/net/vsock/`, verify host
`ns_mode=global` and `child_ns_mode=local`, and `ns_mode=local` in a new
virt-launcher Pod.
VM owners still enable VSOCK with `autoattachVSOCK`.

## Opt out and roll back

Use [root exclusion](lifecycle-management.md#root-exclusion-day-0-prevention)
on the HyperConverged CR to stop Autopilot from creating or managing both
MachineConfigs. Add these entries to any existing `disabled-resources` list:

```yaml
metadata:
  annotations:
    platform.kubevirt.io/disabled-resources: |
      - kind: MachineConfig
        name: 90-worker-vsock-local
      - kind: MachineConfig
        name: 90-master-vsock-local
```

Exclusion does not delete existing MachineConfigs. To roll back, wait for
Autopilot to reconcile the exclusion, then delete `90-worker-vsock-local` and,
if present, `90-master-vsock-local`. An existing master MachineConfig also
needs manual removal if masters become unschedulable. Keep the exclusions in
place to prevent recreation; remove them to re-enable management.

Complete the removal rollout and reboot affected nodes; the sysctl cannot be
switched back in place after its first write. Before downgrading KubeVirt,
verify host `child_ns_mode=global` and VM Pod `ns_mode=global`.
