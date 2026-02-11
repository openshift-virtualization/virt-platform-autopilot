# Prometheus Alert Rule Unit Tests

This directory contains [promtool](https://prometheus.io/docs/prometheus/latest/configuration/unit_testing_rules/) unit tests for the virt-platform-autopilot alert rules.

## Running Tests

### Prerequisites

Install `promtool` (part of Prometheus):

```bash
# macOS
brew install prometheus

# Linux (download from Prometheus releases)
# Or use container:
docker run --rm -v $(pwd):/workspace prom/prometheus:latest \
  promtool test rules /workspace/test/promtool/alert_tests.yml
```

### Run Tests

The recommended way to run tests is via the Makefile target, which auto-generates the rules file:

```bash
# From repository root:
make test-alerts
```

This will:
1. Generate `test/promtool/prometheus-rules.yml` from the PrometheusRule template
2. Run promtool tests
3. Validate alert firing logic

**Note:** The `test/promtool/prometheus-rules.yml` file is **generated dynamically** and not committed to git. Only the source template (`assets/observability/prometheus-rules.yaml.tpl`) is version controlled.

Expected output:
```
✓ Alert expressions are syntactically correct
✓ Alerts fire at correct times (for: durations validated)
✓ Alert labels match correctly
✓ Alert thresholds work correctly
```

## Test Coverage

The tests validate:

1. **VirtPlatformSyncFailed (Critical)**
   - ✅ Alert fires after 15 minutes of compliance_status == 0
   - ✅ Alert does NOT fire for transient failures (< 15min)
   - ✅ Alert does NOT fire when resource is synced (compliance_status == 1)

2. **VirtPlatformThrashingDetected (Warning)**
   - ✅ Alert fires when increase(thrashing_total[10m]) > 5
   - ✅ Alert does NOT fire for low thrashing (< 5 events)
   - ✅ Alert does NOT fire when no thrashing occurs

3. **VirtPlatformDependencyMissing (Warning)**
   - ✅ Alert fires after 5 minutes of missing_dependency == 1
   - ✅ Alert does NOT fire for transient CRD absence (< 5min)
   - ✅ Alert does NOT fire when CRD is present (missing_dependency == 0)

## What promtool Tests

- ✅ Alert expression syntax is valid
- ✅ Alerts fire under expected conditions (time series scenarios)
- ✅ Alert labels are correctly templated
- ✅ Alert annotations are correctly templated
- ✅ "for" duration clauses work correctly

## What promtool Does NOT Test

- ❌ Actual metric collection from the operator
- ❌ PrometheusRule CRD YAML validity (covered by integration tests)
- ❌ Alert delivery to Alertmanager (covered by E2E tests)

## Modifying Tests

When adding new alerts or changing expressions:

1. Update `assets/observability/prometheus-rules.yaml.tpl`
2. Add corresponding test scenarios to `alert_tests.yml`
3. Run `promtool test rules test/promtool/alert_tests.yml`
4. Verify all tests pass

## CI Integration

These tests run in CI as part of the lint workflow (fast, no cluster needed):

```yaml
- name: Test Prometheus Alert Rules
  run: |
    # Install promtool
    curl -LO https://github.com/prometheus/prometheus/releases/download/v2.48.0/prometheus-2.48.0.linux-amd64.tar.gz
    tar xzf prometheus-2.48.0.linux-amd64.tar.gz
    sudo mv prometheus-2.48.0.linux-amd64/promtool /usr/local/bin/

    # Run tests
    promtool test rules test/promtool/alert_tests.yml
```

## References

- [Prometheus Unit Testing Rules](https://prometheus.io/docs/prometheus/latest/configuration/unit_testing_rules/)
- [promtool documentation](https://prometheus.io/docs/prometheus/latest/command-line/promtool/)
