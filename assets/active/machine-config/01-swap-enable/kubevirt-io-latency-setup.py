#!/usr/bin/env python3
"""Set up io.latency protection so system services (kubelet, CRI-O) are not
starved by swap IO from workload pods.

How it works:
  - The kernel's io.latency controller can throttle one cgroup when a sibling
    cgroup's IO latency exceeds its target. We give system.slice a tight target
    (5ms) and kubepods.slice a loose one (50ms). When swap IO from kubepods
    pushes system.slice past 5ms, the kernel throttles kubepods automatically.
  - io.latency targets are per block device, so we need to discover which
    physical disks actually back swap and root, then write the targets into
    systemd drop-in files.
  - Drop-ins go under /run/ (tmpfs), so they're cleared on reboot and
    re-created by this script on next boot.
"""

import os
import subprocess

SYSTEM_LATENCY_MS = 5
KUBEPODS_LATENCY_MS = 50
SYSTEM_DROPIN_DIR = "/run/systemd/system/system.slice.d"
KUBEPODS_DROPIN_DIR = "/run/systemd/system/kubepods.slice.d"
DROPIN_NAME = "20-io-latency.conf"


def resolve_to_physical(dev):
    """Given a device path like /dev/dm-3 or /dev/sda2, return the set of
    physical whole-disk devices (e.g. {"/dev/sda"}).

    Why: io.latency must target the physical disk where IO actually contends.
    /proc/swaps and /proc/mounts report higher-level devices that may be
    LVM/DM volumes or partitions, not the physical disk itself.

    NOTE: callers must resolve symlinks with os.path.realpath() before calling
    this function. /proc/swaps entries may be symlink paths such as
    /dev/disk/by-partlabel/OCPSWAP whose basename does not appear in
    /sys/class/block/, causing device discovery to silently return an empty set.

    Resolution walks sysfs:
      /dev/dm-3  -> /sys/class/block/dm-3/slaves/sda2  -> recurse
      /dev/sda2  -> /sys/class/block/sda2/partition exists -> parent is sda
      /dev/sda   -> no slaves, no partition marker -> this is the physical disk
    """
    devname = os.path.basename(dev)
    sysdir = f"/sys/class/block/{devname}"
    if not os.path.exists(sysdir):
        return set()

    # DM/LVM/LUKS: follow the slave chain to the underlying device(s)
    slaves_dir = os.path.join(sysdir, "slaves")
    if os.path.isdir(slaves_dir):
        slaves = os.listdir(slaves_dir)
        if slaves:
            result = set()
            for slave in slaves:
                result |= resolve_to_physical(f"/dev/{slave}")
            return result

    # Partition (e.g. sda2): go up to the whole disk (sda)
    if os.path.exists(os.path.join(sysdir, "partition")):
        parent = os.path.basename(os.path.realpath(os.path.join(sysdir, "..")))
        return {f"/dev/{parent}"}

    # Already a whole disk
    return {f"/dev/{devname}"}


def discover_devices():
    """Find all physical disks that back swap and root.

    We need both because:
      - Swap disk: where swap IO happens (the main source of contention).
      - Root disk: where kubelet/CRI-O do their IO (what we're protecting).
    If they're on the same disk, deduplication handles it.
    """
    devices = set()

    # Swap devices: parse /proc/swaps, skip zram (RAM-backed, no real IO)
    try:
        with open("/proc/swaps") as f:
            for line in f:
                parts = line.split()
                if len(parts) < 2 or parts[0] == "Filename":
                    continue
                if parts[1] != "partition":
                    continue
                if os.path.basename(parts[0]).startswith("zram"):
                    continue
                for phys in resolve_to_physical(os.path.realpath(parts[0])):
                    print(f"Swap backing device: {phys}")
                    devices.add(phys)
    except FileNotFoundError:
        print("WARNING: /proc/swaps not found")

    # Root device: parse /proc/mounts for the "/" mountpoint
    try:
        with open("/proc/mounts") as f:
            for line in f:
                parts = line.split()
                if len(parts) >= 2 and parts[1] == "/":
                    for phys in resolve_to_physical(os.path.realpath(parts[0])):
                        print(f"Root backing device: {phys}")
                        devices.add(phys)
                    break
    except FileNotFoundError:
        print("WARNING: /proc/mounts not found")

    return devices


def write_dropin(directory, lines):
    os.makedirs(directory, exist_ok=True)
    path = os.path.join(directory, DROPIN_NAME)
    with open(path, "w") as f:
        f.write("\n".join(lines) + "\n")


def main():
    devices = discover_devices()
    if not devices:
        print("WARNING: no block devices discovered, skipping io.latency setup")
        return

    # Write a systemd drop-in for each slice with IODeviceLatencyTargetSec
    # for every discovered device
    sys_lines = ["[Slice]"]
    kp_lines = ["[Slice]"]
    for dev in sorted(devices):
        sys_lines.append(f"IODeviceLatencyTargetSec={dev} {SYSTEM_LATENCY_MS}ms")
        kp_lines.append(f"IODeviceLatencyTargetSec={dev} {KUBEPODS_LATENCY_MS}ms")
        print(f"io.latency: {dev} system={SYSTEM_LATENCY_MS}ms kubepods={KUBEPODS_LATENCY_MS}ms")

    write_dropin(SYSTEM_DROPIN_DIR, sys_lines)
    write_dropin(KUBEPODS_DROPIN_DIR, kp_lines)

    subprocess.run(["systemctl", "daemon-reload"], check=True, timeout=30)
    print("io.latency drop-ins applied and daemon reloaded")


if __name__ == "__main__":
    main()
