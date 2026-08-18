#!/usr/bin/env python3
"""Configure KSMD in zero-pages-only mode with adaptive scan rate.

Strategy:
  - Fixed pages_to_scan=1000 keeps batch lock time low and predictable.
  - sleep_millisecs = min(50, 25600 / node_gb):
      Nodes <= 512 GB: sleep stays at 50ms floor (rate = 20,000 p/s = 78 MB/s).
      Nodes  > 512 GB: sleep decreases to cap sweep time at ~112 min.
  - The formula makes sweep time grow linearly with node size up to 512 GB,
    then stays constant because the node_gb in the denominator cancels out:
      sweep = node_gb * 262144 / (1000 / (25600/node_gb)) = 6711s ≈ 112 min
  - redhat_only_zero_pages restricts KSM to only merge zero pages (no content dedup).
  - Two full scans are needed before pages can be merged (convergence = 2x sweep).

Performance table:
  Node RAM | sleep_ms | Rate p/s | Sweep     | Convergence
  ---------|----------|----------|-----------|------------
    16 GB  |    50    |  20,000  |   3.5 min |     7 min
    96 GB  |    50    |  20,000  |    21 min |    42 min
   256 GB  |    50    |  20,000  |    56 min |   112 min
   512 GB  |    50    |  20,000  |   112 min |   224 min
     1 TB  |    25    |  40,000  |   112 min |   224 min
"""

import os
import sys

KSM_SYSFS = "/sys/kernel/mm/ksm"


def read_total_mem_gb():
    with open("/proc/meminfo") as f:
        for line in f:
            if line.startswith("MemTotal:"):
                return int(line.split()[1]) // 1048576
    sys.exit("ERROR: cannot read MemTotal from /proc/meminfo")


def compute_params(mem_gb):
    """Compute pages_to_scan and sleep_millisecs.

    pages_to_scan is fixed at 1000. sleep_millisecs is min(50, 25600/node_gb)
    which keeps scan rate at 20,000 p/s for nodes up to 512 GB and increases
    it proportionally above that to cap sweep time at ~112 min.
    """
    pages_to_scan = 1000
    sleep_ms = min(50, 25600 // max(mem_gb, 1))
    sleep_ms = max(sleep_ms, 10)  # safety floor
    return pages_to_scan, sleep_ms


def write_sysfs(name, value):
    path = os.path.join(KSM_SYSFS, name)
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
    pages_to_scan, sleep_ms = compute_params(mem_gb)
    scan_rate = pages_to_scan * 1000 // sleep_ms

    write_sysfs("redhat_only_zero_pages", 1)
    write_sysfs("pages_to_scan", pages_to_scan)
    write_sysfs("sleep_millisecs", sleep_ms)
    write_sysfs("run", 1)

    print(f"ksm-tune: node={mem_gb}GB pages_to_scan={pages_to_scan} "
          f"sleep={sleep_ms}ms rate={scan_rate}p/s "
          f"bw={scan_rate * 4 // 1024}MB/s")


if __name__ == "__main__":
    main()
