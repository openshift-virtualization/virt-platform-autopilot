#!/bin/bash
# Test Prometheus alert rules with promtool
#
# This script runs promtool tests and validates alert firing logic,
# labels, and annotations strictly against the generated rules.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

# Check if promtool is available
if ! command -v promtool &> /dev/null; then
    echo "ERROR: promtool not found"
    echo ""
    echo "Please install Prometheus:"
    echo "  - macOS: brew install prometheus"
    echo "  - Linux: Download from https://prometheus.io/download/"
    echo "  - Container: docker run -v \$(pwd):/workspace prom/prometheus:latest promtool test rules /workspace/test/promtool/alert_tests.yml"
    exit 1
fi

echo "Generating test/promtool/prometheus-rules.yml from PrometheusRule template..."

# Generate plain rules file for promtool from the PrometheusRule CRD template
# Promtool doesn't understand Kubernetes CRDs, so we extract just the groups section
python3 << 'PYSCRIPT'
import yaml
import sys

try:
    import re
    # Read the PrometheusRule CRD
    with open('assets/active/observability/prometheus-rules.yaml.tpl') as f:
        content = f.read()
    # Preserve escaped Prometheus expressions before replacing Go expressions.
    # A flat regex otherwise leaves trailing backticks/braces in annotations.
    content = re.sub(r'\{\{`(.*?)`\}\}', lambda match: match.group(1), content, flags=re.DOTALL)
    content = re.sub(r'\{\{(?!\s*\$)[^}]*\}\}', 'placeholder', content)
    crd = yaml.safe_load(content)

    # Extract just the groups (what promtool expects)
    rule_groups = crd['spec']['groups']

    # Write to a plain rules file for promtool in the test directory
    output = {'groups': rule_groups}

    with open('test/promtool/prometheus-rules.yml', 'w') as f:
        yaml.dump(output, f, default_flow_style=False, sort_keys=False)

    print("✓ Generated test/promtool/prometheus-rules.yml from prometheus-rules.yaml.tpl")
except Exception as e:
    print(f"ERROR: Failed to generate prometheus-rules.yml: {e}", file=sys.stderr)
    sys.exit(1)
PYSCRIPT

echo ""
# Configuration alert coverage is strict, including its remediation annotations.
promtool test rules "${REPO_ROOT}/test/promtool/logging_storage_tests.yml"

echo "Running promtool tests on alert rules..."
echo "File: test/promtool/alert_tests.yml"
echo ""

# Run promtool test (strict: labels and annotations must match exactly;
# set -e propagates any failure)
promtool test rules "${REPO_ROOT}/test/promtool/alert_tests.yml"
echo "✓ All promtool tests passed"
