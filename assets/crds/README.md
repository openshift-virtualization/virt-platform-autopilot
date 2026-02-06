# CRD Collection for Testing

This directory contains CRDs required for envtest and Kind testing.

**Last updated:** 2026-02-06 17:35:51 UTC

## CRD Sources

### KubeVirt Ecosystem
- **HyperConverged**: https://github.com/kubevirt/hyperconverged-cluster-operator
  - Path: `deploy/crds/hco.kubevirt.io_hyperconvergeds.yaml`

### OpenShift Platform
- **MachineConfig**: https://github.com/openshift/machine-config-operator
  - Note: CRDs may need manual extraction from OpenShift release images
- **KubeDescheduler**: https://github.com/openshift/cluster-kube-descheduler-operator
  - Path: `manifests/stable/cluster-kube-descheduler-operator.crd.yaml`

### Medik8s Remediation
- **NodeHealthCheck**: https://github.com/medik8s/node-healthcheck-operator
  - Path: `config/crd/bases/remediation.medik8s.io_nodehealthchecks.yaml`
- **Self Node Remediation**: https://github.com/medik8s/self-node-remediation
  - Path: `config/crd/bases/self-node-remediation.medik8s.io_selfnoderemediations.yaml`
- **Fence Agents**: https://github.com/medik8s/fence-agents-remediation
  - Path: `config/crd/bases/fence-agents-remediation.medik8s.io_fenceagentsremediations.yaml`

### Third-Party Operators
- **MTV (Forklift)**: https://github.com/kubev2v/forklift
  - Path: `operator/config/crd/bases/forklift.konveyor.io_forkliftcontrollers.yaml`
- **MetalLB**: https://github.com/metallb/metallb-operator
  - Path: `config/crd/bases/metallb.io_metallbs.yaml`
- **AAQ**: https://github.com/kubevirt/application-aware-quota
  - Path: `config/crd/bases/aaq.kubevirt.io_aaqoperatorconfigs.yaml`

## Update Instructions

Run `make update-crds` to fetch the latest CRDs from upstream.
Run `make verify-crds` to validate CRDs load correctly in envtest.

## Usage in Tests

### envtest
```go
testEnv = &envtest.Environment{
    CRDDirectoryPaths: []string{
        filepath.Join("..", "internal", "assets", "crds", "kubevirt"),
        filepath.Join("..", "internal", "assets", "crds", "openshift"),
        filepath.Join("..", "internal", "assets", "crds", "remediation"),
        filepath.Join("..", "internal", "assets", "crds", "operators"),
    },
}
```

### Kind
CRDs are automatically installed by `make kind-setup` or `make kind-install-crds`.

## Manual Installation

To install CRDs into a cluster manually:
```bash
kubectl apply -f internal/assets/crds/kubevirt/
kubectl apply -f internal/assets/crds/openshift/
kubectl apply -f internal/assets/crds/remediation/
kubectl apply -f internal/assets/crds/operators/
```
