#!/usr/bin/env bash
# Provision file-backed swap for KubeVirt memory overcommitment.
# Swap activation deliberately belongs to ocpswap-file-enable.service.
set -euo pipefail

swap_file=/var/tmp/ocpswap.file
swap_partition=/dev/disk/by-partlabel/OCPSWAP
overcommit_percentage=${1:?memory overcommit percentage is required}

case "${overcommit_percentage}" in
  ''|*[!0-9]*)
    echo "memory overcommit percentage must be a non-negative integer" >&2
    exit 1
    ;;
esac

if [[ -L "${swap_file}" ]]; then
  echo "refusing to replace symbolic link ${swap_file}" >&2
  exit 1
fi

remove_inactive_swap_file() {
  if [[ -e "${swap_file}" ]] && grep -Fq -- "${swap_file}" /proc/swaps; then
    echo "refusing to remove active swap file ${swap_file}" >&2
    exit 1
  fi
  if [[ -e "${swap_file}" ]] && [[ ! -f "${swap_file}" ]]; then
    echo "refusing to remove non-regular file ${swap_file}" >&2
    exit 1
  fi
  rm -f "${swap_file}"
}

if [[ -e "${swap_partition}" ]]; then
  remove_inactive_swap_file
  echo "dedicated swap partition ${swap_partition} is available; skipping file-backed swap"
  exit 0
fi

if (( overcommit_percentage <= 100 )); then
  remove_inactive_swap_file
  echo "memory overcommit is ${overcommit_percentage}%; removed file-backed swap"
  exit 0
fi

memory_kib=$(awk '/^MemTotal:/ { print $2; exit }' /proc/meminfo)
if [[ -z "${memory_kib}" ]]; then
  echo "unable to determine node memory" >&2
  exit 1
fi

# Round up: swap equals the amount of RAM overcommitted on this node.
swap_size_mb=$(( (memory_kib * (overcommit_percentage - 100) + 102399) / 102400 ))
required_free_mb=$(( swap_size_mb * 2 ))
available_mb=$(df -m --output=avail /var/tmp | awk 'NR == 2 { print $1 }')
expected_size_bytes=$(( swap_size_mb * 1024 * 1024 ))

is_expected_swap_file() {
  [[ -f "${swap_file}" ]] || return 1
  [[ "$(stat -c '%a:%s' "${swap_file}")" == "600:${expected_size_bytes}" ]] || return 1
  [[ "$(blkid -p -o value -s TYPE "${swap_file}" 2>/dev/null || true)" == "swap" ]]
}

if [[ -e "${swap_file}" ]] && is_expected_swap_file; then
  echo "existing ${swap_size_mb} MiB file-backed swap matches the requested configuration"
  exit 0
fi

if [[ -e "${swap_file}" ]] && grep -Fq -- "${swap_file}" /proc/swaps; then
  echo "refusing to replace active swap file ${swap_file}" >&2
  exit 1
fi

reclaimable_mb=0
if [[ -e "${swap_file}" ]]; then
  if [[ ! -f "${swap_file}" ]]; then
    echo "refusing to replace non-regular file ${swap_file}" >&2
    exit 1
  fi
  existing_size_bytes=$(stat -c '%s' "${swap_file}")
  reclaimable_mb=$(( (existing_size_bytes + 1048575) / 1048576 ))
fi

# A mismatched file must not be activated if provisioning fails. It was checked
# above to be inactive, and its reclaimed capacity counts toward the guard.
rm -f "${swap_file}"

# The old inactive file has been removed, so this is the post-replacement
# capacity check.
if [[ -z "${available_mb}" ]]; then
  echo "refusing to create ${swap_size_mb} MiB swap file: unable to determine free space on /var/tmp" >&2
  exit 1
fi
available_after_replacement_mb=$(( available_mb + reclaimable_mb ))
if (( available_after_replacement_mb < required_free_mb )); then
  echo "refusing to create ${swap_size_mb} MiB swap file: /var/tmp has ${available_mb:-unknown} MiB free (${reclaimable_mb} MiB reclaimable); ${required_free_mb} MiB is required" >&2
  exit 1
fi

# Do not leave a partial file behind: a future boot must be able to retry.
cleanup() {
  rm -f "${swap_file}"
}
trap cleanup ERR

fallocate -l "${swap_size_mb}M" "${swap_file}"
chmod 0600 "${swap_file}"
mkswap "${swap_file}"

echo "provisioned ${swap_size_mb} MiB file-backed swap at ${swap_file}"
