#!/usr/bin/env bash

#
# This file is part of the KubeVirt project
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
# http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.
#
# Copyright 2026 Red Hat, Inc.
#

set -euo pipefail

LINTER_IMAGE_TAG="v0.0.11"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

# Parse command-line arguments
operator_name=""
sub_operator_name=""
while [[ $# -gt 0 ]]; do
    case "$1" in
        --operator-name=*)
            operator_name="${1#*=}"
            shift
            ;;
        --sub-operator-name=*)
            sub_operator_name="${1#*=}"
            shift
            ;;
        *)
            echo "Invalid argument: $1"
            exit 1
            ;;
    esac
done

if [[ -z "$operator_name" || -z "$sub_operator_name" ]]; then
    echo "Usage: $0 --operator-name=<name> --sub-operator-name=<name>"
    exit 1
fi

# Build the metrics collector
echo "Building prom-metrics-collector..."
cd "$PROJECT_ROOT"
mkdir -p _out
go build -o _out/prom-metrics-collector ./tools/prom-metrics-collector/...

# Generate metrics JSON
echo "Collecting metrics..."
json_output=$(_out/prom-metrics-collector 2>/dev/null)

# Select container runtime
CRI_BIN=$(command -v podman 2>/dev/null || command -v docker 2>/dev/null || true)
if [[ -z "$CRI_BIN" ]]; then
    echo "ERROR: Neither podman nor docker found. Please install one."
    exit 1
fi

# Run the prom-metrics-linter container
echo "Running prom-metrics-linter (${LINTER_IMAGE_TAG})..."
errors=$("$CRI_BIN" run --rm -i "quay.io/kubevirt/prom-metrics-linter:${LINTER_IMAGE_TAG}" \
    --metric-families="$json_output" \
    --operator-name="$operator_name" \
    --sub-operator-name="$sub_operator_name" 2>/dev/null) || true

# Check if there were any errors
if [[ -n "$errors" ]]; then
    echo "Metrics linting errors found:"
    echo "$errors"
    exit 1
fi

echo "✓ All metrics pass linting checks."
