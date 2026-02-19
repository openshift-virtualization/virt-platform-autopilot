# Debug Endpoints and Render Command

This document describes the debug HTTP endpoints and offline render command for troubleshooting and inspecting the platform autopilot's asset rendering.

## Overview

The platform autopilot provides two ways to inspect rendered assets:

1. **HTTP Debug Server** (running controller) - Debug endpoints on the live controller
2. **Render Subcommand** (offline) - CLI tool for offline rendering and CI/CD integration

Both use the same rendering engine and provide consistent output.

## HTTP Debug Server

The debug server runs on `127.0.0.1:8081` (localhost only for security) when the controller is running.

### Accessing Debug Endpoints

```bash
# Port-forward to access from your local machine
oc port-forward deploy/virt-platform-autopilot 8081:8081

# Or exec into the pod
oc exec -it deploy/virt-platform-autopilot -- curl http://localhost:8081/debug/health
```

### Available Endpoints

#### `/debug/render`

Renders all assets based on the current HCO configuration.

**Query Parameters:**
- `format` - Output format: `yaml` (default) or `json`
- `show-excluded` - Include excluded/filtered assets: `true` or `false` (default)

**Examples:**
```bash
# Render all included assets (YAML format)
curl http://localhost:8081/debug/render

# Show all assets including excluded (JSON format)
curl http://localhost:8081/debug/render?format=json&show-excluded=true

# Pretty-print JSON
curl http://localhost:8081/debug/render?format=json | jq '.'
```

**Response (YAML format):**

Multi-document YAML with comment headers - directly usable with `kubectl apply`:

```yaml
# Asset: hco-golden-config
# Path: active/hco/golden-config.yaml.tpl
# Component: HyperConverged
# Status: INCLUDED
apiVersion: hco.kubevirt.io/v1beta1
kind: HyperConverged
metadata:
  name: kubevirt-hyperconverged
  namespace: openshift-cnv
  labels:
    platform.kubevirt.io/managed-by: virt-platform-autopilot
spec:
  featureGates:
    deployKubeSecondaryDNS: true
  # ... full rendered spec ...
---
# Asset: swap-enable
# Path: active/machine-config/01-swap-enable.yaml
# Component: MachineConfig
# Status: INCLUDED
apiVersion: machineconfiguration.openshift.io/v1
kind: MachineConfig
metadata:
  name: 90-worker-swap-online
  labels:
    machineconfiguration.openshift.io/role: worker
    platform.kubevirt.io/managed-by: virt-platform-autopilot
spec:
  config:
    ignition:
      version: 3.2.0
    systemd:
      units:
        - name: swap.target
          enabled: true
        # ... full rendered spec ...
---
# Asset: pci-passthrough
# Path: active/machine-config/02-pci-passthrough.yaml.tpl
# Component: MachineConfig
# Status: EXCLUDED
# Reason: Conditions not met
---
```

**Usage:**
```bash
# Save to file and apply directly
curl http://localhost:8081/debug/render > manifests.yaml
kubectl apply -f manifests.yaml

# Or pipe directly
curl http://localhost:8081/debug/render | kubectl apply -f -

# Save to git
curl http://localhost:8081/debug/render > gitops/rendered-assets.yaml
git add gitops/rendered-assets.yaml
```

**Status Values:**
- `INCLUDED` - Asset will be applied
- `EXCLUDED` - Conditions not met (install mode or hardware detection)
- `FILTERED` - Removed by root exclusion (disabled-resources annotation)
- `ERROR` - Template rendering error

#### `/debug/render/{asset}`

Renders a specific asset by name.

**Examples:**
```bash
# Render swap-enable asset
curl http://localhost:8081/debug/render/swap-enable

# Render HCO golden config as JSON
curl http://localhost:8081/debug/render/hco-golden-config?format=json
```

#### `/debug/exclusions`

Lists all excluded or filtered assets with reasons.

**Query Parameters:**
- `format` - Output format: `yaml` (default) or `json`

**Examples:**
```bash
# List all exclusions
curl http://localhost:8081/debug/exclusions

# Get exclusions as JSON
curl http://localhost:8081/debug/exclusions?format=json | jq '.[] | select(.reason == "Root exclusion")'
```

**Response:**
```yaml
- asset: pci-passthrough
  path: active/machine-config/02-pci-passthrough.yaml.tpl
  component: MachineConfig
  reason: Conditions not met
  details:
    platform.kubevirt.io/openshift: "expected=true, actual="
---
- asset: descheduler-loadaware
  path: active/descheduler/recommended.yaml.tpl
  component: KubeDescheduler
  reason: Root exclusion
  details:
    annotation: platform.kubevirt.io/disabled-resources
    value: "KubeDescheduler/cluster"
    resource: "KubeDescheduler/cluster"
```

#### `/debug/tombstones`

Lists all tombstones (obsolete resources to be deleted).

**Query Parameters:**
- `format` - Output format: `yaml` (default) or `json`

**Examples:**
```bash
# List all tombstones
curl http://localhost:8081/debug/tombstones

# Check if specific resource is tombstoned
curl http://localhost:8081/debug/tombstones?format=json | jq '.[] | select(.name == "old-config")'
```

**Response:**
```yaml
- kind: ConfigMap
  name: obsolete-tuning-config
  namespace: openshift-cnv
  path: tombstones/v1.1-cleanup/tuning-config.yaml
```

#### `/debug/health`

Simple health check endpoint.

**Example:**
```bash
curl http://localhost:8081/debug/health
# Output: OK
```

## Render Subcommand (Offline Mode)

The `render` subcommand allows offline asset rendering without a running cluster. Useful for:
- CI/CD pipeline validation
- Template debugging during development
- Generating example manifests for documentation
- Customer support (render with their HCO config)

### Usage

```bash
# Basic usage with HCO file
virt-platform-autopilot render --hco-file=hco.yaml

# Render specific asset
virt-platform-autopilot render --hco-file=hco.yaml --asset=swap-enable

# Show excluded assets with reasons
virt-platform-autopilot render --hco-file=hco.yaml --show-excluded

# JSON output
virt-platform-autopilot render --hco-file=hco.yaml --output=json

# Status table (summary)
virt-platform-autopilot render --hco-file=hco.yaml --output=status

# Use HCO from cluster (requires kubeconfig)
virt-platform-autopilot render --kubeconfig=/path/to/kubeconfig
```

### Flags

| Flag | Description | Default |
|------|-------------|---------|
| `--hco-file` | Path to HyperConverged YAML file (offline mode) | - |
| `--kubeconfig` | Path to kubeconfig (cluster mode) | - |
| `--asset` | Render only this specific asset | - |
| `--show-excluded` | Include excluded/filtered assets | `false` |
| `--output` | Output format: `yaml`, `json`, or `status` | `yaml` |

**Note:** `--hco-file` and `--kubeconfig` are mutually exclusive. You must provide one or the other.

### Output Formats

#### YAML (default)

Multi-document YAML with comments:

```yaml
# Asset: hco-golden-config
# Path: active/hco/golden-config.yaml.tpl
# Component: HyperConverged
# Status: INCLUDED
apiVersion: hco.kubevirt.io/v1beta1
kind: HyperConverged
metadata:
  name: kubevirt-hyperconverged
  namespace: openshift-cnv
spec:
  # ...
---
# Asset: swap-enable
# Path: active/machine-config/01-swap-enable.yaml
# Component: MachineConfig
# Status: INCLUDED
apiVersion: machineconfiguration.openshift.io/v1
kind: MachineConfig
# ...
---
# Asset: pci-passthrough
# Path: active/machine-config/02-pci-passthrough.yaml.tpl
# Component: MachineConfig
# Status: EXCLUDED
# Reason: Conditions not met
---
```

#### JSON

Structured JSON array:

```json
[
  {
    "asset": "hco-golden-config",
    "path": "active/hco/golden-config.yaml.tpl",
    "component": "HyperConverged",
    "status": "INCLUDED",
    "conditions": [],
    "object": {
      "apiVersion": "hco.kubevirt.io/v1beta1",
      "kind": "HyperConverged",
      ...
    }
  },
  {
    "asset": "pci-passthrough",
    "path": "active/machine-config/02-pci-passthrough.yaml.tpl",
    "component": "MachineConfig",
    "status": "EXCLUDED",
    "reason": "Conditions not met",
    "conditions": [
      {
        "type": "annotation",
        "key": "platform.kubevirt.io/openshift",
        "value": "true"
      }
    ]
  }
]
```

#### Status Table

Concise summary table:

```
ASSET                          STATUS          COMPONENT            REASON
----------------------------------------------------------------------------------------------------
hco-golden-config              INCLUDED        HyperConverged       -
swap-enable                    INCLUDED        MachineConfig        -
pci-passthrough                EXCLUDED        MachineConfig        Conditions not met
numa-topology                  EXCLUDED        MachineConfig        Conditions not met
kubelet-perf-settings          INCLUDED        KubeletConfig        -
node-health-check              INCLUDED        NodeHealthCheck      -
mtv-operator                   EXCLUDED        ForkliftController   Conditions not met
metallb-operator               EXCLUDED        MetalLB              Conditions not met
observability-operator         EXCLUDED        UIPlugin             Conditions not met
descheduler-loadaware          FILTERED        KubeDescheduler      Root exclusion
kubelet-cpu-manager            EXCLUDED        KubeletConfig        Conditions not met
----------------------------------------------------------------------------------------------------
Summary: 3 included, 7 excluded, 1 filtered, 0 errors
```

## Use Cases

### 1. Debugging Template Errors

```bash
# Render specific asset to see full output
virt-platform-autopilot render --hco-file=customer-hco.yaml --asset=pci-passthrough

# Check why asset is excluded
virt-platform-autopilot render --hco-file=customer-hco.yaml --show-excluded | grep -A 10 "pci-passthrough"
```

### 2. Validating PR Changes

```bash
# In CI pipeline
virt-platform-autopilot render --hco-file=testdata/hco-minimal.yaml > /tmp/rendered.yaml
kubectl apply --dry-run=server -f /tmp/rendered.yaml
```

### 3. Generating Documentation Examples

```bash
# Generate example manifests for docs
virt-platform-autopilot render --hco-file=examples/hco-with-all-features.yaml \
  --output=yaml > docs/examples/all-assets-rendered.yaml
```

### 4. Customer Support

```bash
# Customer provides their HCO config
virt-platform-autopilot render --hco-file=customer-hco.yaml --output=status

# Check specific asset rendering
virt-platform-autopilot render --hco-file=customer-hco.yaml --asset=metallb-operator
```

### 5. Testing Root Exclusion

```bash
# Create HCO with disabled resources
cat > hco-with-exclusions.yaml <<EOF
apiVersion: hco.kubevirt.io/v1beta1
kind: HyperConverged
metadata:
  name: kubevirt-hyperconverged
  namespace: openshift-cnv
  annotations:
    platform.kubevirt.io/disabled-resources: |
      - kind: KubeDescheduler
        name: cluster
      - kind: MachineConfig
        name: 50-swap-enable
EOF

# Verify exclusions work
virt-platform-autopilot render --hco-file=hco-with-exclusions.yaml --output=status
```

### 6. Comparing Configurations

```bash
# Render with two different HCO configs
virt-platform-autopilot render --hco-file=hco-minimal.yaml > /tmp/minimal.yaml
virt-platform-autopilot render --hco-file=hco-full.yaml > /tmp/full.yaml
diff /tmp/minimal.yaml /tmp/full.yaml
```

## Security Considerations

### HTTP Debug Server

- **Localhost only**: Debug server binds to `127.0.0.1:8081` by default
- **Read-only**: All endpoints are GET requests that only read cluster state
- **No authentication**: Relies on pod network isolation and port-forwarding
- **Disable in production**: Use `--enable-debug-server=false` if not needed

### Render Subcommand

- **No cluster access**: Offline mode doesn't touch the cluster
- **Input validation**: HCO YAML files are validated before rendering
- **Safe by default**: Only renders templates, doesn't apply to cluster

## Troubleshooting

### Debug server not accessible

```bash
# Check if debug server is enabled
oc logs deploy/virt-platform-autopilot | grep "Starting debug server"

# Check port-forward
oc port-forward deploy/virt-platform-autopilot 8081:8081 &
curl http://localhost:8081/debug/health
```

### "No HyperConverged resources found"

The HCO CRD or instance doesn't exist in the cluster:

```bash
# Check HCO exists
oc get hyperconverged -n openshift-cnv

# Create test HCO if needed
oc apply -f examples/hco-minimal.yaml
```

### Asset not rendering

Check conditions and exclusions:

```bash
# See why asset is excluded
curl http://localhost:8081/debug/exclusions?format=json | jq '.[] | select(.asset == "my-asset")'

# Check asset details
curl http://localhost:8081/debug/render/my-asset
```

### Template rendering errors

```bash
# Render specific asset to see error
virt-platform-autopilot render --hco-file=hco.yaml --asset=problematic-asset

# Check template syntax
cat assets/active/path/to/asset.yaml.tpl
```

## Implementation Details

### Architecture

```
┌─────────────────────────────────────────┐
│  Debug Server (HTTP)                    │
│  ├─ /debug/render                       │
│  ├─ /debug/render/{asset}               │
│  ├─ /debug/exclusions                   │
│  ├─ /debug/tombstones                   │
│  └─ /debug/health                       │
└─────────────────┬───────────────────────┘
                  │
                  ├─ pkg/debug/handlers.go
                  │
┌─────────────────▼───────────────────────┐
│  Render Command (CLI)                   │
│  virt-platform-autopilot render         │
└─────────────────┬───────────────────────┘
                  │
                  ├─ cmd/render/render.go
                  │
┌─────────────────▼───────────────────────┐
│  Shared Rendering Engine                │
│  ├─ pkg/assets (loader, registry)       │
│  ├─ pkg/engine (renderer, patcher)      │
│  └─ pkg/context (render context)        │
└─────────────────────────────────────────┘
```
