#!/usr/bin/env bash
# Migrate the pre-provisioning file-backed swap path before activation.
set -euo pipefail

legacy_swap_file=/var/ocpswap.file
swap_file=/var/swapfile

if [[ -e "${swap_file}" ]]; then
  echo "${swap_file} already exists; leaving ${legacy_swap_file} unchanged"
  exit 0
fi

if [[ ! -f "${legacy_swap_file}" ]] || [[ -L "${legacy_swap_file}" ]]; then
  echo "refusing to migrate non-regular file ${legacy_swap_file}" >&2
  exit 1
fi

if grep -Fq -- "${legacy_swap_file}" /proc/swaps; then
  echo "refusing to migrate active swap file ${legacy_swap_file}" >&2
  exit 1
fi

mv -- "${legacy_swap_file}" "${swap_file}"

# Persist the SELinux type for the new path so a complete relabel preserves it.
if [[ "$(matchpathcon -n "${swap_file}")" != *:swapfile_t:* ]]; then
  semanage fcontext -a -f a -t swapfile_t "${swap_file}"
fi
restorecon -v "${swap_file}"

echo "migrated file-backed swap from ${legacy_swap_file} to ${swap_file}"
