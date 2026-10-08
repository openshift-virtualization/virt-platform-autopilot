# virt-platform-autopilot — Agent Guide

`virt-platform-autopilot` is an OpenShift Virtualization operator that renders
and reconciles opinionated platform assets. It manages existing Kubernetes and
OpenShift APIs; it intentionally introduces no CRDs or autopilot status API.

Keep this file concise. Detailed design intent lives in the linked documents.

## Read First

- [README.md](README.md): user-facing behavior, feature maturity, and controls.
- [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md): reconciliation flow, override
  semantics, asset catalog, and feature generation.
- [CONTRIBUTING.md](CONTRIBUTING.md): contribution and DCO requirements.
- [docs/adding-assets.md](docs/adding-assets.md): required workflow for assets.

Read the relevant design document before changing its area:

- `docs/lifecycle-management.md`: tombstones and root exclusion.
- `docs/anti-thrashing-design.md`: reconciliation-rate protection.
- `docs/machineconfig-rollout-coalescing.md`: safe MachineConfig rollout policy.
- `docs/logging.md`, `docs/scsi-persistent-reservations.md`, and
  `docs/vm-drain-shutdown-inhibitor.md`: feature-specific constraints.
- `docs/debug-endpoints.md`: rendering and diagnostic interfaces.
- `docs/local-development.md`: Kind-based local workflow.
- `test/README.md` and `test/e2e/README.md`: integration and E2E scope.

## Repository Map

- `cmd/`: manager, offline render command, and generation tools.
- `pkg/controller/`: HCO context and top-level reconciliation.
- `pkg/engine/`: rendering, patched-baseline application, drift, exclusion, and
  MachineConfig coalescing.
- `pkg/assets/`: embedded-asset loading, metadata, and tombstones.
- `pkg/overrides/`: patch, ignored-field, unmanaged, and activation validation.
- `assets/active/`: managed asset templates and `metadata.yaml` catalog.
- `assets/tombstones/`: safe deletion manifests for retired assets.
- `config/`: deployment and generated RBAC manifests.
- `test/`: envtest integration tests; `test/e2e/` is Kind-based E2E coverage.
- `hack/`: CRD/KME synchronization, Kind, lint, and test helpers.

## Non-Negotiable Domain Rules

- Reconcile HCO before dependent assets. Other rendering uses the effective HCO
  state through `RenderContext`.
- Preserve user control mechanisms: `platform.kubevirt.io/patch`,
  `ignore-fields`, `mode: unmanaged`, disabled resources, feature opt-ins, and
  the global autopilot opt-out. Do not bypass their validation or precedence.
- Preserve SSA behavior. Drift detection and field ownership require real API
  server semantics; use envtest rather than fake clients for SSA behavior.
- Optional APIs are soft dependencies. Missing CRDs or absent operator
  namespaces must skip only the affected asset, not fail the reconciliation.
- Every asset must be idempotent. Treat repeated reconciliations and partially
  installed clusters as normal operation.
- Tombstones are destructive: only delete objects labeled
  `platform.kubevirt.io/managed-by=virt-platform-autopilot`. Follow
  `docs/lifecycle-management.md`; never weaken this safety gate.
- MachineConfig changes can reboot nodes. Preserve rollout coalescing and do
  not introduce a bypass without the documented Node Disruption Policy basis.
- `KubeletConfig` assets are prohibited; `make lint-assets` enforces this.

## Commands

`go.mod` is the source of truth for the required Go version.

```bash
make fmt                         # Format Go
make vet                         # Go static checks
make goimport                    # Normalize imports; rewrites Go files
make test                        # fmt + vet + goimport + unit tests
go test ./pkg/engine/...         # Focused package test while iterating
make test-integration            # envtest integration suite
make lint                        # golangci-lint
make shellcheck                  # Shell scripts under hack/ and assets/
make lint-python                 # Python assets, if present
make test-alerts                 # Prometheus rule tests
make lint-metrics                # Prometheus metric naming
make lint-assets                 # Prohibited KubeletConfig check
make build                       # Build manager and CSV generator
```

`make test` and `make goimport` can alter the worktree. Inspect the diff before
including formatting changes in a patch.

## Generated and Synchronized Files

Do not hand-edit derived output. Run the generator and commit its result.

- Asset resource types or tombstones changed:
  `make generate-rbac`; do not manually edit `config/rbac/role.yaml`.
- Feature metadata changed:
  `make generate-feature-status`; this updates the README feature table and
  `docs/generated/feature-status.json`.
- Upstream CRD collection changed:
  `make update-crds`; validate with `make verify-crds`.
- KubeVirt Metrics Exporter assets changed:
  `make sync-kme-assets`; validate with `make verify-kme-assets`.
- Before submitting generated changes, run the applicable `verify-*` target.

## Changing Assets

For an asset addition or modification:

1. Read `docs/adding-assets.md` and the relevant feature design document.
2. Update the template and its `assets/active/metadata.yaml` entry together.
3. Make activation and dependency conditions explicit; use CRD checks for
   optional operators.
4. Add or update unit/render/integration coverage appropriate to the behavior.
5. Render offline where useful:
   `virt-platform-autopilot render --hco-file=<file> --output=yaml`.
6. Regenerate RBAC and feature status when the catalog change requires it.
7. Update user-facing docs when behavior, controls, or maturity changes.

## Test Boundaries

- Unit tests cover focused Go behavior.
- Integration tests use envtest and are required for SSA, CRD lifecycle, and
  patched-baseline behavior.
- E2E tests validate manager, watches, cache, deployment, and live
  reconciliation. They require Docker or Podman, Kind, and kubectl.

Run E2E only against the dedicated Kind workflow:

```bash
make test-e2e
CLEANUP=false make test-e2e      # Keep the Kind cluster for diagnosis
```

Never point E2E or deployment commands at a shared, staging, or production
cluster unless the user explicitly requests that target.

## Documentation and Contributions

- Keep changes focused. Update the relevant design doc when changing component
  boundaries, reconciliation semantics, public controls, or safety invariants.
- Follow existing terminology: assets, HCO, patched baseline, SSA, soft
  dependency, tombstone, and root exclusion.
- Commits require DCO sign-off under `CONTRIBUTING.md`. Do not create commits
  or sign on behalf of a human unless explicitly authorized.
- Do not edit `vendor/` directly. Use `make vendor` after intentional module
  dependency changes.
