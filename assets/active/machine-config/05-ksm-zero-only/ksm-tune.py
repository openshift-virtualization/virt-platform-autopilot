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
  - max_ptes_none = 0 when KSM is active, else 511. Same logic as thp-tune.py so
    boot order or manual re-runs stay coordinated. Tradeoff of max_ptes_none=0:
    slower khugepaged re-collapse on sparse regions, avoiding conflict between
    each other.

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
THP_SYSFS = "/sys/kernel/mm/transparent_hugepage"
KHUGEPAGED_SYSFS = os.path.join(THP_SYSFS, "khugepaged")
KSM_RUN_PATH = os.path.join(KSM_SYSFS, "run")
MAX_PTES_NONE_KSM = 0
MAX_PTES_NONE_DEFAULT = 511


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


def read_sysfs(path):
    try:
        with open(path) as f:
            return f.read().strip()
    except OSError:
        return None


def write_sysfs(path, value):
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


def apply_max_ptes_none():
    """Set max_ptes_none from live KSM state; safe to call from either tune script."""
    ksm_active = read_sysfs(KSM_RUN_PATH) == "1"
    value = MAX_PTES_NONE_KSM if ksm_active else MAX_PTES_NONE_DEFAULT
    reason = "ksm active" if ksm_active else "no ksm"
    path = os.path.join(KHUGEPAGED_SYSFS, "max_ptes_none")
    write_sysfs(path, value)
    return value, reason


def main():
    mem_gb = read_total_mem_gb()
    pages_to_scan, sleep_ms = compute_params(mem_gb)
    scan_rate = pages_to_scan * 1000 // sleep_ms

    write_sysfs(os.path.join(KSM_SYSFS, "redhat_only_zero_pages"), 1)
    write_sysfs(os.path.join(KSM_SYSFS, "pages_to_scan"), pages_to_scan)
    write_sysfs(os.path.join(KSM_SYSFS, "sleep_millisecs"), sleep_ms)
    write_sysfs(KSM_RUN_PATH, 1)
    max_ptes_none, max_ptes_reason = apply_max_ptes_none()

    print(f"ksm-tune: node={mem_gb}GB pages_to_scan={pages_to_scan} "
          f"sleep={sleep_ms}ms rate={scan_rate}p/s "
          f"bw={scan_rate * 4 // 1024}MB/s "
          f"max_ptes_none={max_ptes_none} ({max_ptes_reason})")


if __name__ == "__main__":
    main()
