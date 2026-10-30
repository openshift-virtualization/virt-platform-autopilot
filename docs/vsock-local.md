# VSOCK local namespace mode

VSOCK normally uses one host-wide CID space, so VMs in different Pods cannot
use the same CID. Autopilot configures workers and schedulable masters
(`Scheduler/cluster.spec.mastersSchedulable`) with
`net.vsock.child_ns_mode = local`. This gives each Pod network namespace its
own CID space, allowing VMs in different Pods to use the same CID. The host's
initial network namespace (`init_net`) remains in global mode, and custom pools
selecting worker MachineConfigs inherit the setting.

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

The MachineConfigs use MCO's normal rebooting rollout so the module and sysctl
are applied before Pod network namespaces are created. Deleting them rolls the
setting back through the same reboot-based rollout.

Under `/proc/sys/net/vsock/`, verify host
`ns_mode=global` and `child_ns_mode=local`, and `ns_mode=local` in a new
virt-launcher Pod.
VM owners still enable VSOCK with `autoattachVSOCK`.
