# vm-drain-shutdown-inhibitor

The vm-drain-shutdown-inhibitor is an opt-in worker MachineConfig that causes node shutdown to be delayed while it attempts to gracefully shutdown KubeVirt VMs that may be running on the node. This requires intervention from an administrator or another tool to cordon the node before a shutdown, so that the shut down VMs are not immediately rescheduled to the same node due to their runStrategy.

This MachineConfig can be enabled by setting the platform.kubevirt.io/vm-drain-shutdown-inhibitor annotation to "true". This MC installs the following things:

    a logind configuration file that increases the max inhibitor delay
    a python script that registers a systemd-inhibitor which waits for PrepareForShutdown signals and responds by shutting down any running virt-launcher containers.
    a systemd unit that starts the python script

This is to work around a situation where a node has become degraded and requires manual intervention to restart it, but the node still has VM workloads operating on it. Because the processes in a container scope all receive the SIGTERM sent during shutdown, qemu, libvirtd, and the virt-launcher can be killed out of order without having a chance for the virt-launcher to gracefully shutdown the VM. Inhibiting shutdown allows the python script to access each container and instruct virt-launcher to shut it down gracefully ahead of the rest of the node shutdown sequence.
