#!/usr/bin/env python3
"""Adaptive watermark tuning for Kubernetes NodeSwap.

Computes and applies vm.watermark_scale_factor so that:
  HIGH > LOW > eviction_threshold > MIN > OOM
"""

import os
import sys
import time
import yaml
from collections import namedtuple

KUBELET_CONFIG_PATHS = [
    "/var/lib/kubelet/config.json",
    "/var/lib/kubelet/config.yaml",
    "/etc/kubernetes/kubelet.conf",
]
DEFAULT_EVICTION_KB = 100 * 1024  # 100Mi
MAX_KUBELET_RETRIES = 5
KUBELET_RETRY_DELAY_SEC = 10
SCALE_MIN = 10
SCALE_MAX = 1000

Watermarks = namedtuple("Watermarks", "min low high managed")


def parse_memory_kb(val, total_ram_kb):
    """Convert a Kubernetes memory quantity to KB."""
    suffixes = {"Ti": 1024**4, "Gi": 1024**3, "Mi": 1024**2, "Ki": 1024,
                "T": 10**12, "G": 10**9, "M": 10**6, "k": 10**3}
    for suffix, mult in suffixes.items():
        if val.endswith(suffix):
            return int(val[:-len(suffix)]) * mult // 1024
    if val.endswith("%"):
        return int(total_ram_kb * float(val[:-1]) / 100)
    if val.isdigit():
        kb = int(val) // 1024
        if kb == 0:
            print(f"WARNING: memory quantity '{val}' rounds to 0 KB, using 1 KB")
            kb = 1
        return kb
    sys.exit(f"ERROR: cannot parse memory quantity: {val}")


def _find_kubelet_config():
    """Search known kubelet config paths, with retries for boot race."""
    for attempt in range(MAX_KUBELET_RETRIES):
        for path in KUBELET_CONFIG_PATHS:
            if os.path.isfile(path):
                print(f"Found kubelet config at {path}")
                return path
        print(f"Kubelet config not found (attempt {attempt + 1}/{MAX_KUBELET_RETRIES}), retrying...")
        time.sleep(KUBELET_RETRY_DELAY_SEC)
    return None


def get_eviction_kb(total_ram_kb):
    """Read eviction threshold from kubelet config, with retries."""
    path = _find_kubelet_config()
    if not path:
        sys.exit("ERROR: Kubelet config not found after retries")

    try:
        with open(path) as f:
            data = yaml.safe_load(f)
        raw = data.get("evictionHard", {}).get("memory.available", "") if isinstance(data, dict) else ""
    except Exception as e:
        print(f"WARNING: failed to read kubelet config: {e}")
        raw = ""

    if not raw:
        print("Eviction threshold not found in config, using default: 100Mi")
        return DEFAULT_EVICTION_KB

    print(f"Eviction threshold from config: {raw}")
    return parse_memory_kb(raw, total_ram_kb)


def read_zoneinfo(page_size_kb):
    """Sum per-zone fields from /proc/zoneinfo, return Watermarks in KB."""
    fields = {"min", "low", "high", "managed"}
    sums = dict.fromkeys(fields, 0)
    with open("/proc/zoneinfo") as f:
        for line in f:
            parts = line.split()
            if len(parts) == 2 and parts[0] in fields:
                sums[parts[0]] += int(parts[1])
    return Watermarks(*(sums[k] * page_size_kb for k in ("min", "low", "high", "managed")))


def read_total_ram_kb():
    with open("/proc/meminfo") as f:
        for line in f:
            if line.startswith("MemTotal:"):
                return int(line.split()[1])
    sys.exit("ERROR: could not read MemTotal from /proc/meminfo")


def main():
    try:
        page_size_kb = os.sysconf("SC_PAGE_SIZE") // 1024
    except (ValueError, OSError):
        page_size_kb = 4  # safe default for x86_64/aarch64
        print("WARNING: could not determine page size, defaulting to 4 KB")

    total_ram_kb = read_total_ram_kb()
    wm = read_zoneinfo(page_size_kb)
    eviction_kb = get_eviction_kb(total_ram_kb)

    if wm.managed == 0:
        sys.exit("ERROR: managed pages is 0, cannot compute scale factor")

    print(f"Total RAM: {total_ram_kb // 1024} MiB, managed: {wm.managed // 1024} MiB")
    print(f"Kernel MIN watermark: {wm.min // 1024} MiB")
    print(f"Eviction threshold: {eviction_kb // 1024} MiB")

    if wm.min >= eviction_kb:
        print(f"WARNING: MIN watermark ({wm.min // 1024} MiB) >= eviction threshold "
              f"({eviction_kb // 1024} MiB). Direct reclaim will stall allocations before "
              f"kubelet can evict pods. Consider raising the eviction threshold.")

    runway_kb = max(256 * 1024, total_ram_kb // 100)  # max(256 MiB, 1% of RAM)
    target_low_kb = eviction_kb + runway_kb

    if wm.low >= target_low_kb:
        print(f"LOW watermark ({wm.low // 1024} MiB) already exceeds target "
              f"({target_low_kb // 1024} MiB), no tuning needed.")
        return

    # Always positive: if MIN >= target, then LOW > MIN >= target, and the early exit above fires.
    gap_needed_kb = target_low_kb - wm.min
    scale = gap_needed_kb * 10000 // wm.managed
    scale = max(SCALE_MIN, min(SCALE_MAX, scale))

    if scale == SCALE_MAX:
        print("WARNING: scale_factor clamped to maximum")

    print(f"Runway: {runway_kb // 1024} MiB, computed scale_factor: {scale}")

    try:
        with open("/proc/sys/vm/watermark_scale_factor", "w") as f:
            f.write(str(scale))
    except PermissionError:
        sys.exit("ERROR: must run as root to set watermark_scale_factor")

    wm = read_zoneinfo(page_size_kb)
    print(f"Result: MIN={wm.min // 1024}Mi  LOW={wm.low // 1024}Mi  HIGH={wm.high // 1024}Mi")

    if wm.low <= eviction_kb:
        sys.exit(f"ERROR: LOW ({wm.low // 1024}Mi) <= Eviction ({eviction_kb // 1024}Mi) "
                 f"after setting scale_factor={scale}")

    if wm.low < target_low_kb:
        print(f"WARNING: LOW ({wm.low // 1024}Mi) < target ({target_low_kb // 1024}Mi), "
              f"runway is shorter than desired due to scale_factor clamping")

    print(f"Done. scale_factor={scale}, "
          f"HIGH({wm.high // 1024}Mi) > LOW({wm.low // 1024}Mi) > Eviction({eviction_kb // 1024}Mi), "
          f"gap: {(wm.low - eviction_kb) // 1024} MiB")


if __name__ == "__main__":
    main()
