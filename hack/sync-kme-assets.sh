#!/usr/bin/env bash

# Fetch kubevirt-metrics-exporter deploy manifests from GitHub and sync them into
# assets/active/kubevirt-metrics-exporter/, applying autopilot-specific transforms
# to the DaemonSet.
#
# Usage:
#   ./hack/sync-kme-assets.sh           - fetch and sync
#   ./hack/sync-kme-assets.sh --verify  - verify assets match upstream (for CI)
#
# Optional env: KME_FORK=openshift-virtualization/kubevirt-metrics-exporter KME_BRANCH=main

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
DEST_DIR="${REPO_ROOT}/assets/active/kubevirt-metrics-exporter"
KME_FORK="${KME_FORK:-openshift-virtualization/kubevirt-metrics-exporter}"
KME_BRANCH="${KME_BRANCH:-main}"
VERIFY=false
TEMP_DIR=""
FAILED=0

# shellcheck disable=SC2016 # $envOverrides is Helm syntax, not shell expansion
TEMPLATE_HEADER='{{- $envOverrides := dig "metadata" "annotations" "platform.kubevirt.io/kubevirt-metrics-exporter-env" "{}" .HCO.Object | fromJson }}'

declare -a COPY_MAP=(
	"deploy/base/namespace.yaml|namespace.yaml"
	"deploy/base/serviceaccount.yaml|serviceaccount.yaml"
	"deploy/base/clusterrole.yaml|clusterrole.yaml"
	"deploy/base/clusterrolebinding.yaml|clusterrolebinding.yaml"
	"deploy/podmonitor/podmonitor.yaml|podmonitor.yaml"
	"deploy/prometheus-rules/prometheusrule.yaml|prometheusrule.yaml"
	"deploy/openshift/scc.yaml|scc.yaml"
	"deploy/openshift/scc-clusterrole.yaml|scc-clusterrole.yaml"
	"deploy/openshift/scc-clusterrolebinding.yaml|scc-clusterrolebinding.yaml"
	"deploy/openshift/datasource.yaml|datasource.yaml"
	"deploy/openshift/dashboard.yaml|dashboard.yaml"
)

cleanup() {
	if [[ -n "${TEMP_DIR}" && -d "${TEMP_DIR}" ]]; then
		rm -rf "${TEMP_DIR}"
	fi
}
trap cleanup EXIT

usage() {
	cat <<EOF
Usage:
  ${0##*/}           Fetch and sync assets
  ${0##*/} --verify  Verify assets match upstream (for CI)

Optional env:
  KME_FORK=owner/repo   GitHub slug (default: upstream)
  KME_BRANCH=main
EOF
}

upstream_url() {
	local relpath=$1
	printf 'https://raw.githubusercontent.com/%s/%s/%s' "${KME_FORK}" "${KME_BRANCH}" "${relpath}"
}

fetch_upstream() {
	local relpath=$1
	local dest=$2
	mkdir -p "$(dirname "${dest}")"
	curl -sSfL "$(upstream_url "${relpath}")" -o "${dest}"
}

write_or_compare_file() {
	local dest=$1
	local src=$2

	if [[ "${VERIFY}" == true ]]; then
		if [[ ! -f "${dest}" ]]; then
			echo "  ✗ MISSING ${dest}" >&2
			FAILED=1
			return
		fi
		if cmp -s "${src}" "${dest}"; then
			echo "  ✓ ${dest}"
		else
			echo "  ✗ OUT OF SYNC ${dest}" >&2
			FAILED=1
		fi
		return
	fi

	mkdir -p "$(dirname "${dest}")"
	cp "${src}" "${dest}"
	echo "  ✓ ${dest}"
}

transform_daemonset() {
	local src=$1
	printf '%s\n' "${TEMPLATE_HEADER}"
	sed -e 's|^[[:space:]]*image:.*|          image: {{ index .Images "kubevirt-metrics-exporter" }}|' \
		-e '/hostPID: true/a\
      nodeSelector:\
        node-role.kubernetes.io/worker: ""
' "${src}" | awk '
/^[[:space:]]+- name: / {
	match($0, /^[[:space:]]+/)
	env_indent = substr($0, RSTART, RLENGTH)
	split($0, parts, "name: ")
	current_env = parts[2]
	gsub(/[[:space:]]+/, "", current_env)
	print
	next
}
/^[[:space:]]+valueFrom:/ {
	current_env = "NODE_NAME"
	print
	next
}
/^[[:space:]]+value:/ && current_env != "" && current_env != "NODE_NAME" {
	split($0, value_parts, "value:")
	default_val = value_parts[2]
	gsub(/^[[:space:]]+/, "", default_val)
	gsub(/^"/, "", default_val)
	gsub(/"$/, "", default_val)
	printf "%s  value: {{ index $envOverrides \"%s\" | default \"%s\" | quote }}\n", env_indent, current_env, default_val
	next
}
{ print }
'
}

sync_assets() {
	local entry upstream_relpath dest_name src

	for entry in "${COPY_MAP[@]}"; do
		IFS='|' read -r upstream_relpath dest_name <<< "${entry}"
		src="${TEMP_DIR}/${upstream_relpath}"
		write_or_compare_file "${DEST_DIR}/${dest_name}" "${src}"
	done

	local daemonset_tmp
	daemonset_tmp="$(mktemp)"
	transform_daemonset "${TEMP_DIR}/deploy/base/daemonset.yaml" > "${daemonset_tmp}"
	write_or_compare_file "${DEST_DIR}/kubevirt-metrics-exporter.yaml.tpl" "${daemonset_tmp}"
	rm -f "${daemonset_tmp}"
}

main() {
	if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
		usage
		exit 0
	fi
	if [[ "${1:-}" == "--verify" ]]; then
		VERIFY=true
	fi

	TEMP_DIR="$(mktemp -d)"

	echo "$([[ "${VERIFY}" == true ]] && echo Verifying || echo Syncing) kubevirt-metrics-exporter assets from https://github.com/${KME_FORK}.git@${KME_BRANCH}"

	fetch_upstream "deploy/base/daemonset.yaml" "${TEMP_DIR}/deploy/base/daemonset.yaml"
	for entry in "${COPY_MAP[@]}"; do
		IFS='|' read -r upstream_relpath _ <<< "${entry}"
		fetch_upstream "${upstream_relpath}" "${TEMP_DIR}/${upstream_relpath}"
	done

	sync_assets

	if [[ "${VERIFY}" == true ]]; then
		if [[ "${FAILED}" -eq 0 ]]; then
			echo "✓ All kubevirt-metrics-exporter assets are in sync with KME"
			exit 0
		fi
		echo "✗ kubevirt-metrics-exporter assets are out of sync with KME" >&2
		exit 1
	fi

	if [[ "${FAILED}" -ne 0 ]]; then
		exit 1
	fi

	echo "✓ Sync complete"
}

main "$@"
