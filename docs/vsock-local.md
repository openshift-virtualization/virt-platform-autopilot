# VSOCK local namespace mode

This GA feature is enabled by default and sets `net.vsock.child_ns_mode = local`
at boot on workers and schedulable masters
(`Scheduler/cluster.spec.mastersSchedulable`). The host's initial network
namespace (`init_net`) stays in global mode; new child network namespaces use
local VSOCK mode. Existing Pods are unaffected by the sysctl change. Custom
pools selecting worker MachineConfigs also inherit the setting.

## Compatibility

Local mode requires an OpenShift/RHCOS kernel with VSOCK support and the
`child_ns_mode` and `ns_mode` interfaces, and a CNV version with VSOCK network
namespace support.

On kernels with VSOCK but without local namespace support, systemd-sysctl
ignores the missing setting and VSOCK retains its legacy global behavior. CNV's
mode detection falls back to global when that support is absent.

Autopilot does not validate kernel or CNV support. Opt out before deploying or
upgrading Autopilot if CNV lacks VSOCK network namespace support. If affected
nodes run other VSOCK workloads or have competing sysctl settings, check
compatibility.

## Rollout and verification

Autopilot creates `90-worker-vsock-local` and, when masters are schedulable,
`90-master-vsock-local`. Each MachineConfig installs:

| Node file | Content |
| --- | --- |
| `/etc/modules-load.d/90-virt-vsock.conf` | `vsock` |
| `/etc/sysctl.d/90-virt-vsock.conf` | `net.vsock.child_ns_mode = local` |

`vsock` loads early so the sysctl is available when `systemd-sysctl` runs.
The kernel loads `vhost_vsock` on demand when CNV opens `/dev/vhost-vsock`;
that module does not need to load at boot for this sysctl.

Wait for the affected MachineConfigPools to complete rollout and confirm nodes
rebooted. Custom disruption policies may require a separate supported reboot.
Initial creation can trigger rollout and node reboots immediately; opt out
before deploying or upgrading Autopilot to prevent this. Later updates follow
the [coalescing policy](machineconfig-rollout-coalescing.md). Single-node
reboots interrupt VMs.

On a kernel with namespace support, under `/proc/sys/net/vsock/`, verify host
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
switched back in place after its first write. Before downgrading CNV,
verify host `child_ns_mode=global` and VM Pod `ns_mode=global`.
