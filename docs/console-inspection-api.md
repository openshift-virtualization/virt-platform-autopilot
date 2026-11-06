# Console Inspection API

Console consumes Autopilot inspection through ordinary Kubernetes ConfigMap
`get`, `list`, and `watch` permissions in the Autopilot namespace: the
namespace of the managed HyperConverged (HCO) object. Console must derive it
from that object rather than hardcoding `openshift-cnv`. There is no
Autopilot HTTP endpoint, Service proxy, client mTLS, or non-resource RBAC.

## Purpose and boundaries

This is GitOps-style inspection of platform configuration, not a second
Autopilot control plane. It lets an administrator discover feature maturity
and configured intent, inspect feature-to-asset evidence and reconciliation
state, and compare an authorized live object with its rendered golden
configuration. This matters especially for configuration such as
MachineConfigs, where an action can reboot nodes.

The transport deliberately uses bounded existing Kubernetes API objects rather
than a CRD or a general serving API. It survives restarts and leader changes,
fits Console's watch model, and does not add TLS, token-forwarding, or custom
authorization machinery to the operator.

The surface includes discovery, evidence, safe activation plans, golden
configuration, and non-sensitive provenance metadata. It explicitly excludes
forcing reconciliation, sync triggers, rollback, history, raw Autopilot-owned
write endpoints, Secrets, SSA dry-run output, managed fields, ignored-field
values, and server-computed diffs. Console applies an explicit activation plan
to the HCO using the logged-in user's ordinary Kubernetes RBAC; Autopilot never
acts as a privileged write proxy.

Golden configuration is rendered during reconciliation, never at Console
request time. `preview` means an object rendered with the current
RenderContext that is not currently effective desired state (for example a
root-excluded asset). It does not mean a counterfactual render after applying
an activation plan, and it never changes reconciliation behavior.

## Objects

- `virt-platform-autopilot-inspection` is the authoritative watched object. It
  has `data.snapshot.json` and `data.index.json` together, so feature and asset
  interpretation is a single-resource read.
- `virt-platform-autopilot-golden` contains optional `<asset-id>.json`
  rendered objects plus a reserved internal `generation` marker. Console must
  only consume golden entries named by `goldenKey`.

Console watches only the summary. On a new summary resource version, it uses an
asset's `goldenKey` to `get` its golden object and reads the live target using
the GVK/name/namespace in the summary under the user's normal Kubernetes RBAC.
An HCO activation plan is applied directly by Console with that user's `patch`
permission; Autopilot is never a write proxy.

## Snapshot contract

`snapshot.json` is `autopilot.kubevirt.io/status/v1beta1`. Its top-level
fields are `state`, `reason`, `observedAt`, `ready`, `lastSuccessfulReconcile`,
`lastFailedReconcile`, `stale`, `features`, and `assets`. A representative
asset entry is:

The versioned [JSON Schema](console-inspection-v1beta1.schema.json) is the
machine-readable contract. CI validates populated published snapshots and
golden indexes against it, including nested feature, asset, activation,
evidence, and render-mode fields. Additions remain compatible, while field
removal, type changes, or enum changes require a new API version.

```json
{
  "id": "cpu-manager",
  "apiVersion": "machineconfiguration.openshift.io/v1",
  "kind": "MachineConfig",
  "name": "99-virt-cpu-manager",
  "state": "synced",
  "lastTransitionTime": "2026-11-06T18:00:00Z",
  "goldenKey": "cpu-manager.json"
}
```

`index.json` in the inspection ConfigMap carries the same monotonic semantic
content `generation` as `snapshot.json`, plus
a map keyed by asset ID. It tells Console whether a golden object is the
currently effective desired object, a rendered preview, or why content is
unavailable:

```json
{
  "generation": 42,
  "assets": {
    "cpu-manager": { "renderMode": "applied" },
    "ksm": { "renderMode": "preview", "reason": "rootExclusion" },
    "credentials": { "renderMode": "notExposed", "reason": "sensitiveContent" }
  }
}
```

The publisher writes the golden ConfigMap before the authoritative summary. A
normal summary watch event therefore never references golden content that has
not already been persisted. If an asset has a `goldenKey` but the matching
entry is missing after a process interruption, Console shows that optional
detail as temporarily unavailable; it does not need a cross-object generation
handshake or a second watch. It must not reuse a different asset's content.
Assets without `goldenKey` have no published content; Console must consult the
index before retrying a missing key.

Because summary and index share one ConfigMap resource version, Console never
needs a cross-object generation handshake before rendering feature state.

The top-level `state` is `initializing`, `ready`, `error`, `disabled`, or
`absent`. `disabled` means the global Autopilot opt-out annotation is set;
`absent` means the HCO does not exist. In both cases the published asset and
golden-content sets are deliberately empty. `error` means the last
reconciliation failed: Console may show prior asset evidence as stale but must
not offer state-changing actions based on it.

`observedAt` records the latest reconciliation attempt, successful or not. A
semantically unchanged snapshot receives a heartbeat update at least every five
minutes without changing `generation` or golden content. Console may cache
golden content by generation and should consider the operator unavailable when
`observedAt` is more than 15 minutes old.

## Compatibility rules

`v1beta1` is additive-only. Console clients must ignore unfamiliar object
fields and enum values, including feature availability, asset state, reason,
and golden render mode. Missing optional fields mean unavailable/unknown, not a
default action. Renaming or reusing an existing feature ID, asset ID, or golden
key is a breaking change. A future incompatible payload uses a new API version
inside `snapshot.json`; ConfigMap names are not a versioning mechanism.

Console must derive actions only from an explicit `activation` object. It must
never infer an HCO annotation, target resource, or patch from a feature name or
its opt-in summary.

## UI state and actions

- With `ready: false` or no snapshot, show initialization and offer no inferred
  actions.
- With top-level `state: disabled`, `absent`, or `error`, show its `reason` and
  do not offer activation or remediation actions.
- For `enabled: false` with `activation`, offer the explicit opt-in action only
  when the user has HCO `patch` RBAC. Without `activation`, show one-click
  activation as unavailable.
- For `availability: healthy`, show observed assets and any authorized raw
  comparison. For `pending` or `unknown`, show progress or unknown state rather
  than healthy. `partial` means some assets are synced while others are
  excluded or unmanaged; surface its evidence rather than claiming success. For
  `blocked`, `unmanaged`, `degraded`, or an
  unfamiliar value, explain the evidence and never automatically repair it.
- For an asset with `goldenKey`, read only that named golden entry. This uses
  the same trusted-administrator RBAC grant as the summary watch.

Raw comparison is informational. `state: synced` or `state: drifted` remains
the Autopilot verdict, even if a client-side JSON comparison appears to differ.

Golden content has a 900 KiB serialized-ConfigMap budget. When a rendered object does
not fit, the inspection ConfigMap's `index.json` entry is `{ "renderMode": "tooLarge", "reason":
"sizeLimit" }` and no `<asset-id>.json` entry is present. Console should show
that limitation rather than retrying the missing key.

The summary has the same budget. If it approaches that limit, the publisher
sets `truncated: true` and `truncationReason: "sizeLimit"`, then sheds optional
activation plans, evidence, and finally trailing asset entries in that order.
Console must keep rendering the remaining discovery data and must not infer an
action when its activation plan has been omitted.

The current implementation retains rendered golden objects. Console must use
Autopilot's coarse asset state as the drift verdict. It may show an
informational raw diff only when the user can read both the golden ConfigMap
entry and the live Kubernetes object; it must never treat that diff as the
Autopilot verdict or expect SSA/managed-field detail from the operator.

Some assets are logical sidecars of a shared Kubernetes object. Such an asset
sets `sharedObject: true` and identifies its rendered slice with `sharedPath`.
For example, each Node Disruption Policy asset is a distinct
`spec.nodeDisruptionPolicy` contribution to the singleton
`MachineConfiguration/cluster`. Its golden entry contains only that sidecar's
desired slice; its state describes the aggregate's reconciliation of that
slice, not exclusive ownership of the singleton.

## RBAC

This is one trusted-administrator surface. A `list` or `watch` grant for the
summary ConfigMap in the shared Autopilot namespace also permits observing the
golden ConfigMap, because Kubernetes cannot name-scope those verbs. Do not
present summary and golden access as separable RBAC tiers; grant this interface
only to cluster administrators or equivalently trusted operators.

Both ConfigMaps are labeled
`platform.kubevirt.io/managed-by=virt-platform-autopilot` and receive an HCO
controller owner reference when the HCO has an identity. They are published
outside the asset pipeline; the ConfigMap cache is deliberately narrowed so
the manager does not consume its own inspection output.
