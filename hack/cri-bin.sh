#!/usr/bin/env bash

set -ex

if podman ps >/dev/null; then
    _cri_bin=podman
    _cri_insecure="--tls-verify=false"
    >&2 echo "selecting podman as container runtime"
elif docker ps >/dev/null; then
    _cri_bin=docker
    >&2 echo "selecting docker as container runtime"
else
    >&2 echo "no working container runtime found. Neither docker nor podman seems to work."
    exit 1
fi

# shellcheck disable=SC2034  # variables are used by sourcing scripts
CRI_BIN=${_cri_bin}
# shellcheck disable=SC2034  # variables are used by sourcing scripts
CRI_INSECURE=${_cri_insecure}

