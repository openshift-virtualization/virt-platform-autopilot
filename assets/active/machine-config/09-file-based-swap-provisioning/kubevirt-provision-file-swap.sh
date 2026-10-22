#!/usr/bin/env bash
# Provision file-backed swap for KubeVirt memory overcommitment.
# Swap activation deliberately belongs to swap-file-enable.service.
set -euo pipefail

swap_file=/var/ocpswap.file
legacy_swap_files=(/var/tmp/ocpswap.file /var/tmp/swapfile)
# A replacement is built here and renamed into place, so ${swap_file} only ever
# holds a complete, mkswap-formatted file. A run that is interrupted part way
# cannot leave a stub behind for swap-file-enable.service to swapon, and the
# swap the node already has stays usable until the replacement is ready.
staging_file="${swap_file}.new"
swap_partition=/dev/disk/by-partlabel/OCPSWAP
# Percentage of /var's filesystem that must remain free once the swap file
# is in place. The kubelet's disk eviction thresholds are relative to the
# filesystem (OpenShift defaults: nodefs.available<10%, imagefs.available<15%)
# and /var shares that filesystem with the container image store, so a swap
# file that passes the size-relative checks below can still push the node into
# permanent DiskPressure. 15% covers the stricter of the two thresholds.
min_free_percent=${2:-15}
overcommit_percentage=${1:?memory overcommit percentage is required}

# The active-swap checks only fire when this script is run by hand on a live
# node: at boot the unit is ordered before swap-file-enable.service, so
# nothing has been swapon'd yet. They exist for the readable error message. The
# real backstop is the kernel, which fails unlink and rename on an active swap
# file with EPERM (S_SWAPFILE), so swap cannot be pulled out from under a
# running node either way.
swap_file_is_active() {
  [[ -e "${swap_file}" ]] && grep -Fq -- "${swap_file}" /proc/swaps
}

remove_inactive_swap_file() {
  if swap_file_is_active; then
    echo "refusing to remove active swap file ${swap_file}" >&2
    exit 1
  fi
  if [[ -e "${swap_file}" ]] && [[ ! -f "${swap_file}" ]]; then
    echo "refusing to remove non-regular file ${swap_file}" >&2
    exit 1
  fi
  rm -f "${swap_file}"
}

# Ownership is part of the identity check. Anything not owned by root is
# treated as a stranger's file and replaced rather than adopted.
is_expected_swap_file() {
  [[ -f "${swap_file}" ]] || return 1
  [[ "$(stat -c '%u:%g:%a:%s' "${swap_file}")" == "0:0:600:${expected_size_bytes}" ]] || return 1
  [[ "$(blkid -p -o value -s TYPE "${swap_file}" 2>/dev/null || true)" == "swap" ]]
}

# Persist the SELinux type for the final path, so a complete filesystem relabel
# restores swapfile_t rather than reverting the file to /var's default type.
# Skip semanage when the path already maps to swapfile_t.
set_swap_file_context() {
  if [[ "$(matchpathcon -n "${swap_file}")" != *:swapfile_t:* ]]; then
    semanage fcontext -a -f a -t swapfile_t "${swap_file}"
  fi
  restorecon -v "${swap_file}"
}

cleanup() {
  rm -f -- "${staging_file}"
}

validate_configuration() {
  case "${overcommit_percentage}" in
    ''|*[!0-9]*)
      echo "memory overcommit percentage must be a non-negative integer" >&2
      exit 1
      ;;
  esac
  case "${min_free_percent}" in
    ''|*[!0-9]*)
      echo "minimum free-space percentage must be an integer between 0 and 100" >&2
      exit 1
      ;;
  esac

  # Bash arithmetic reads a leading zero as octal, so normalize both inputs.
  overcommit_percentage=$(( 10#${overcommit_percentage} ))
  min_free_percent=$(( 10#${min_free_percent} ))
  if (( min_free_percent > 100 )); then
    echo "minimum free-space percentage must be an integer between 0 and 100" >&2
    exit 1
  fi

  # Earlier procedures provisioned swap under /var/tmp. Keep either known
  # configuration intact rather than creating a second swap file without an
  # explicit migration.
  for legacy_swap_file in "${legacy_swap_files[@]}"; do
    if [[ -f "${legacy_swap_file}" ]]; then
      echo "legacy file-backed swap ${legacy_swap_file} exists; leaving it unchanged"
      exit 0
    fi
  done
  if [[ -L "${swap_file}" ]]; then
    echo "refusing to replace symbolic link ${swap_file}" >&2
    exit 1
  fi
}

validate_configuration

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
expected_size_bytes=$(( swap_size_mb * 1024 * 1024 ))

if is_expected_swap_file; then
  set_swap_file_context
  echo "existing ${swap_size_mb} MiB file-backed swap matches the requested configuration"
  exit 0
fi

if swap_file_is_active; then
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
  echo "existing file-backed swap ${swap_file} is ${reclaimable_mb} MiB; trying to replace with ${swap_size_mb} MiB"
fi

# One df for both figures. A df failure leaves them empty rather than aborting
# the pipeline, so the refusal below explains what happened.
read -r total_mb available_mb < <(df -m --output=size,avail /var | awk 'NR == 2 { print $1, $2 }') || true
if [[ -z "${total_mb}" ]] || [[ -z "${available_mb}" ]]; then
  echo "refusing to create ${swap_size_mb} MiB swap file: unable to determine free space on /var" >&2
  exit 1
fi

# Three limits, all checked before anything is removed, so a node that cannot
# accommodate a larger swap file keeps the working one it already has instead of
# being left with no swap at all.
min_free_mb=$(( (total_mb * min_free_percent + 99) / 100 ))
free_after_mb=$(( available_mb + reclaimable_mb - swap_size_mb ))
if (( available_mb < swap_size_mb )); then
  echo "refusing to create ${swap_size_mb} MiB swap file: /var has ${available_mb} MiB free and the replacement is built alongside the current file" >&2
  exit 1
fi
if (( available_mb + reclaimable_mb < required_free_mb )); then
  echo "refusing to create ${swap_size_mb} MiB swap file: /var has ${available_mb} MiB free (${reclaimable_mb} MiB reclaimable); ${required_free_mb} MiB is required" >&2
  exit 1
fi
if (( free_after_mb < min_free_mb )); then
  echo "refusing to create ${swap_size_mb} MiB swap file: it would leave ${free_after_mb} MiB free of ${total_mb} MiB on /var, below the ${min_free_percent}% the kubelet needs to stay clear of disk-pressure eviction" >&2
  exit 1
fi
trap cleanup EXIT

# Clear anything a previously killed run left behind, then create the staging
# file with noclobber, which makes the redirection an O_EXCL create. umask 077 is what
# actually sets the mode: systemd runs services with umask 022, and a file left
# even briefly world-readable at this predictable path can be opened by a local
# process whose descriptor then survives both the chmod and the rename and reads
# live swap. The chmod stays as a cheap assertion of the final mode.
rm -f -- "${staging_file}"
( umask 077; set -o noclobber; : > "${staging_file}" )
chmod 0600 "${staging_file}"
fallocate -l "${swap_size_mb}M" "${staging_file}"
mkswap "${staging_file}"

# Rename last, so the swap file is either the previous one or the complete new
# one and never anything in between.
mv -f -- "${staging_file}" "${swap_file}"
set_swap_file_context

echo "provisioned ${swap_size_mb} MiB file-backed swap at ${swap_file}"
