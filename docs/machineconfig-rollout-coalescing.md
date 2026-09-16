# Coalescing MachineConfig updates

MachineConfig updates can trigger a costly Machine Config Operator (MCO) rollout and node reboot. Autopilot therefore creates MachineConfigs immediately, but stages updates to an existing Autopilot-managed MachineConfig while all matching MachineConfigPools (MCPs) are stable.

When any matching MCP reports `Updating=True`, Autopilot applies the latest staged version. MCO can then include that change in the rollout already in progress. The first matching updating MCP wins. Matching covers both the live and proposed MachineConfig labels, so target-pool label changes are coalesced safely. Staging is stored in the `virt-platform-autopilot-mc-staging` ConfigMap in the HyperConverged namespace, so an operator restart does not discard it. The ConfigMap is owned by HyperConverged, and entries are removed when their MachineConfig leaves Autopilot's active set.

## Degraded pools

A pool's `Degraded` condition does not hold a staged update back. `Degraded=True` together with `Updating=True` is a rollout that has hit a failing node: nodes are already being disrupted, so that is precisely the rollout worth joining, and the condition flaps for the duration. Treating it as a veto would skip the window and defer the update to some later rollout instead.

A degraded pool that is *not* updating behaves like any other stable pool — nothing is rolling out, so there is nothing to coalesce into and the update stays staged. When the pool recovers and resumes, `Updating=True` releases it.

`RenderDegraded=True` releases a staged update immediately. It means MCO cannot turn the pool's MachineConfigs into a rendered configuration at all, so applying the update cannot start a rollout or reboot a node. Holding it would be an indefinite wait with no upside, and the staged update may itself be the change that fixes the render.

## What coalescing does and does not guarantee

`Updating=True` says a rollout is *running*, not that it is *starting*. Autopilot has no reliable way to detect the beginning of a rollout, so a staged update can be released at any point in the pool's progress.

The consequence is that coalescing reduces reboots on a best-effort basis rather than guaranteeing a single pass:

- Nodes that have **not yet** been updated by the in-flight rollout pick up the staged change together with the change that started it — one reboot instead of two. This is the saving the feature targets, and it is the common case for a large pool.
- Nodes that have **already** been updated by the in-flight rollout reboot a second time, because MCO renders a new configuration and the pool re-converges on it. Those nodes would have rebooted twice anyway had the update been applied immediately, so coalescing does not make them worse — it simply does not help them.

In the worst case (an update released just as a rollout finishes) coalescing saves nothing and only delays the change. It never causes more reboots than applying the update immediately would have.

## Pool matching and scope

A MachineConfig does not name a pool. Each MCP selects MachineConfigs using `spec.machineConfigSelector`, so one MachineConfig can match more than one pool. For example, a custom `infra` pool can also select `machineconfiguration.openshift.io/role: worker` MachineConfigs. Autopilot evaluates pools matching either the current or proposed MachineConfig labels, and releases the staged update as soon as the first of them is updating.

This prevents an update from waiting indefinitely for all matching pools to overlap. It does not change MCO fan-out: once a shared MachineConfig is updated, MCO can roll out every pool that selects it, including pools that were stable at release time.

The MachineConfig API being present while the MachineConfigPool API is absent is
an extreme compatibility corner case. In that case Autopilot has no rollout
signal to coalesce against, so it retains normal immediate MachineConfig
reconciliation rather than leaving updates staged indefinitely.

## Bypass staging

For a time-sensitive change, add this annotation to the live Autopilot-managed MachineConfig:

```yaml
metadata:
  annotations:
    platform.kubevirt.io/bypass-mcp-rollout-coalescing: "true"
```

Autopilot preserves this annotation and applies future updates immediately. Remove it to resume staging. Use it sparingly: it can start a new MCO rollout and reboot nodes.

## Observability

`kubevirt_autopilot_machineconfig_update_staged{machineconfig,pool}` is `1` for each MCP matching either label set while an update is staged. `kubevirt_autopilot_machineconfig_update_staged_since_seconds` records when it was first staged. The **Autopilot / Asset Health** dashboard includes a **Staged MachineConfig Updates** table.

While an update is staged, `kubevirt_autopilot_compliance_status` for that MachineConfig reports `2` (Staged) rather than `1` (Synced): the live object genuinely does not match the golden state. `VirtPlatformAutopilotSyncFailed` matches `== 0`, so a deliberate deferral does not raise a sync failure, and the dashboard shows the asset as Staged rather than Synced.
