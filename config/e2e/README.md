# E2E Test Configuration

This directory contains Kustomize overlays optimized for E2E testing to minimize test execution time.

## Key Optimizations

### 1. Leader Election Disabled

Leader election is **disabled** for E2E tests because:
- **Single Replica**: E2E tests run with only 1 operator replica
- **Unnecessary Overhead**: With 1 replica, leader election adds ~15-20s per restart

### 2. Aggressive Health Probes

Readiness and liveness probes are configured for rapid detection:

| Probe | Production | E2E | Impact |
|-------|-----------|-----|--------|
| **Readiness** initialDelaySeconds | 5s | **1s** | Check readiness almost immediately |
| **Readiness** periodSeconds | 10s | **1s** | Check every second instead of every 10s |
| **Liveness** initialDelaySeconds | 15s | **3s** | Faster crash detection |
| **Liveness** periodSeconds | 20s | **5s** | More frequent liveness checks |

**Impact**: Operator restarts are detected in ~2-3 seconds instead of ~15-25 seconds.

### 3. Reduced CRD Validation Timeout

The startup CRD validation timeout is reduced from 10s to 2s via `--crd-validation-timeout=2s`.

**Impact**: Operator startup is 8 seconds faster when CRD already exists (E2E scenario).

## Total Performance Improvement

For tests that restart the operator (e.g., CRD lifecycle tests):
- **Before optimizations**: ~50-60 seconds per restart
- **After optimizations**: ~5-10 seconds per restart
- **Savings**: ~45-50 seconds per restart

For a test suite with 3 operator restarts:
- **Before**: ~3 minutes total
- **After**: ~30-60 seconds total
- **Improvement**: 60-80% faster

## Production vs E2E

| Configuration | Leader Election | Probe Settings | CRD Timeout | Use Case |
|--------------|----------------|----------------|-------------|----------|
| `config/manager` | **Enabled** | Conservative (5s/10s) | 10s | Production deployments |
| `config/e2e` | **Disabled** | Aggressive (1s/1s) | 2s | E2E tests, local dev |

## Usage

This overlay is automatically used by `hack/deploy-local.sh deploy-prebuilt` (called by E2E tests).

For manual testing with E2E optimizations:
```bash
kubectl kustomize config/e2e | kubectl apply -f -
```

For production deployments:
```bash
kubectl apply -f config/manager/manager.yaml
```

## Safety Notes

The aggressive E2E settings are **NOT recommended for production** because:
- Frequent probe checks increase API server load
- 1-second probe intervals may cause false positives during high load
- No leader election means no high availability

These settings are safe for E2E because:
- Single-node kind cluster with minimal load
- Single operator replica
- Short-lived test environment
