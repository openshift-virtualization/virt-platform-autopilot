#!/usr/bin/env python3
"""Configure Transparent Huge Pages and khugepaged for KVM workloads.

Strategy:
  - Set THP enabled mode to 'madvise': only processes that call
    madvise(MADV_HUGEPAGE) get THP at fault time. QEMU does this for guest
    RAM automatically. System services and sidecars stay on 4K pages,
    reducing memory overhead and improving predictability.
  - khugepaged pages_to_scan = 130,000 (fixed): provides ~254 regions/wake
    (each region = 512 pages = 2 MB), enough budget to re-collapse PMD
    splits caused by KSM or free-page-reporting.
  - khugepaged scan_sleep_millisecs = 1000 + 64000 / node_gb:
      Small nodes (16 GB): sleep=5000ms, wake 0.2/s — infrequent to save CPU.
      Large nodes (1 TB):  sleep=1063ms, wake 0.9/s — frequent to keep up.
  - Sweep time scales naturally: ~2 min for 16 GB, ~30 min for 1 TB
    (assuming VMs fill ~80% of node RAM).

Performance table:
  Node RAM | sleep_ms | Wakes/s | Regions/s | Sweep (80% VM)
  ---------|----------|---------|-----------|---------------
    16 GB  |  5,000   |   0.2   |     51    |   2.2 min
    32 GB  |  3,000   |   0.3   |     85    |   2.6 min
    64 GB  |  2,000   |   0.5   |    127    |   3.4 min
    96 GB  |  1,667   |   0.6   |    152    |   4.3 min
   256 GB  |  1,250   |   0.8   |    203    |   8.6 min
   512 GB  |  1,125   |   0.9   |    226    |  15.5 min
     1 TB  |  1,063   |   0.9   |    239    |  29.3 min
"""

import os
import sys

THP_SYSFS = "/sys/kernel/mm/transparent_hugepage"
KHUGEPAGED_SYSFS = os.path.join(THP_SYSFS, "khugepaged")


def read_total_mem_gb():
    with open("/proc/meminfo") as f:
        for line in f:
            if line.startswith("MemTotal:"):
                return int(line.split()[1]) // 1048576
    sys.exit("ERROR: cannot read MemTotal from /proc/meminfo")


def compute_params(mem_gb):
    """Compute khugepaged pages_to_scan and scan_sleep_millisecs.

    pages_to_scan is fixed at 130,000. scan_sleep_millisecs is
    1000 + 64000/node_gb which ranges from ~5s (16GB) to ~1s (1TB).
    """
    pages_to_scan = 130000
    scan_sleep_ms = 1000 + 64000 // max(mem_gb, 1)
    return pages_to_scan, scan_sleep_ms


def write_file(path, value):
    if not os.path.exists(path):
        print(f"WARNING: {path} not available, skipping")
        return False
    try:
        with open(path, "w") as f:
            f.write(str(value))
        return True
    except (PermissionError, OSError) as e:
        print(f"ERROR: cannot write {path}: {e}", file=sys.stderr)
        return False


def main():
    mem_gb = read_total_mem_gb()
    pages_to_scan, scan_sleep_ms = compute_params(mem_gb)
    regions_per_s = pages_to_scan // 512 * 1000 // scan_sleep_ms

    write_file(os.path.join(THP_SYSFS, "enabled"), "madvise")
    write_file(os.path.join(KHUGEPAGED_SYSFS, "pages_to_scan"), pages_to_scan)
    write_file(os.path.join(KHUGEPAGED_SYSFS, "scan_sleep_millisecs"), scan_sleep_ms)

    print(f"thp-tune: node={mem_gb}GB enabled=madvise "
          f"pages_to_scan={pages_to_scan} sleep={scan_sleep_ms}ms "
          f"regions/s={regions_per_s}")


if __name__ == "__main__":
    main()
