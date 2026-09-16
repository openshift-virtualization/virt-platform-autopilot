#!/usr/bin/env bash
# Provision file-backed swap for KubeVirt memory overcommitment.
# Swap activation deliberately belongs to ocpswap-file-enable.service.
set -euo pipefail

swap_file=/var/tmp/ocpswap.file
# A replacement is built here and renamed into place, so ${swap_file} only ever
# holds a complete, mkswap-formatted file. A run that is interrupted part way
# cannot leave a stub behind for ocpswap-file-enable.service to swapon, and the
# swap the node already has stays usable until the replacement is ready.
staging_file="${swap_file}.new"
swap_partition=/dev/disk/by-partlabel/OCPSWAP
# Percentage of /var/tmp's filesystem that must remain free once the swap file
# is in place. The kubelet's disk eviction thresholds are relative to the
# filesystem (OpenShift defaults: nodefs.available<10%, imagefs.available<15%)
# and /var/tmp shares that filesystem with the container image store, so a swap
# file that passes the size-relative checks below can still push the node into
# permanent DiskPressure. 15% covers the stricter of the two thresholds.
min_free_percent=15
overcommit_percentage=${1:?memory overcommit percentage is required}

case "${overcommit_percentage}" in
  ''|*[!0-9]*)
    echo "memory overcommit percentage must be a non-negative integer" >&2
    exit 1
    ;;
esac
# Force base 10. Bash arithmetic reads a leading zero as octal, so "0150" would
# otherwise be evaluated as 104 and size the swap file from the wrong figure.
overcommit_percentage=$(( 10#${overcommit_percentage} ))

if [[ -L "${swap_file}" ]]; then
  echo "refusing to replace symbolic link ${swap_file}" >&2
  exit 1
fi

# The active-swap checks only fire when this script is run by hand on a live
# node: at boot the unit is ordered before ocpswap-file-enable.service, so
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

# Ownership is part of the identity check. /var/tmp is world-writable, so a
# non-root process can leave a correctly sized file carrying a swap signature at
# this path; reusing it would hand its creator read and write access to swapped
# guest memory. Anything not owned by root is treated as a stranger's file and
# replaced rather than adopted.
is_expected_swap_file() {
  [[ -f "${swap_file}" ]] || return 1
  [[ "$(stat -c '%u:%g:%a:%s' "${swap_file}")" == "0:0:600:${expected_size_bytes}" ]] || return 1
  [[ "$(blkid -p -o value -s TYPE "${swap_file}" 2>/dev/null || true)" == "swap" ]]
}

# A file created under /var/tmp inherits that directory's SELinux label rather
# than the swapfile_t type the policy defines for swap areas, so set it
# explicitly. This is also re-applied to a file that already matches, because a
# full filesystem relabel resets the label without changing anything the
# identity check above looks at. Best effort by design: the activation unit has
# always consumed a file with the inherited label at this path, so a node whose
# policy does not know the type is no worse off than before and must not be left
# without swap over it.
label_swap_file() {
  local path=$1
  command -v chcon >/dev/null 2>&1 || return 0
  chcon -t swapfile_t "${path}" 2>/dev/null ||
    echo "warning: could not label ${path} as swapfile_t; keeping the inherited label" >&2
}

if is_expected_swap_file; then
  label_swap_file "${swap_file}"
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
fi

# One df for both figures. A df failure leaves them empty rather than aborting
# the pipeline, so the refusal below explains what happened.
read -r total_mb available_mb < <(df -m --output=size,avail /var/tmp | awk 'NR == 2 { print $1, $2 }') || true
if [[ -z "${total_mb}" ]] || [[ -z "${available_mb}" ]]; then
  echo "refusing to create ${swap_size_mb} MiB swap file: unable to determine free space on /var/tmp" >&2
  exit 1
fi

# Three limits, all checked before anything is removed, so a node that cannot
# accommodate a larger swap file keeps the working one it already has instead of
# being left with no swap at all.
#
#   1. The staging file is built alongside any file already in place, so the
#      build needs swap_size_mb free right now.
#   2. The finished state must still leave swap_size_mb free once the old file
#      is released, which is what required_free_mb expresses.
#   3. What is left must also clear the kubelet's filesystem-relative eviction
#      thresholds. The first two limits scale with the swap file and say nothing
#      about the size of the disk, so on a large filesystem they can both pass
#      while leaving the node permanently under DiskPressure.
min_free_mb=$(( (total_mb * min_free_percent + 99) / 100 ))
free_after_mb=$(( available_mb + reclaimable_mb - swap_size_mb ))
if (( available_mb < swap_size_mb )); then
  echo "refusing to create ${swap_size_mb} MiB swap file: /var/tmp has ${available_mb} MiB free and the replacement is built alongside the current file" >&2
  exit 1
fi
if (( available_mb + reclaimable_mb < required_free_mb )); then
  echo "refusing to create ${swap_size_mb} MiB swap file: /var/tmp has ${available_mb} MiB free (${reclaimable_mb} MiB reclaimable); ${required_free_mb} MiB is required" >&2
  exit 1
fi
if (( free_after_mb < min_free_mb )); then
  echo "refusing to create ${swap_size_mb} MiB swap file: it would leave ${free_after_mb} MiB free of ${total_mb} MiB on /var/tmp, below the ${min_free_percent}% the kubelet needs to stay clear of disk-pressure eviction" >&2
  exit 1
fi

# Covers normal exits and command failures, so a partial file is cleaned up
# whenever the shell can run its EXIT trap. SIGKILL and power loss can still
# leave the staging file behind, but never at ${swap_file}; the next run removes
# the harmless .new file before creating a replacement.
cleanup() {
  rm -f -- "${staging_file}"
}
trap cleanup EXIT

# Clear anything a previously killed run left behind, then create the staging
# file with noclobber, which makes the redirection an O_EXCL create: if a file
# or a symlink reappears at this path in world-writable /var/tmp between the two
# statements, the create fails instead of following it. umask 077 is what
# actually sets the mode: systemd runs services with umask 022, and a file left
# even briefly world-readable at this predictable path can be opened by a local
# process whose descriptor then survives both the chmod and the rename and reads
# live swap. The chmod stays as a cheap assertion of the final mode.
rm -f -- "${staging_file}"
( umask 077; set -o noclobber; : > "${staging_file}" )
chmod 0600 "${staging_file}"
label_swap_file "${staging_file}"
fallocate -l "${swap_size_mb}M" "${staging_file}"
mkswap "${staging_file}"

# Rename last, so the swap file is either the previous one or the complete new
# one and never anything in between.
mv -f -- "${staging_file}" "${swap_file}"

echo "provisioned ${swap_size_mb} MiB file-backed swap at ${swap_file}"
