apiVersion: perses.dev/v1alpha2
kind: PersesDashboard
metadata:
  name: descheduler-memory-aware-rebalancing
  namespace: {{ .HCO.GetNamespace | default "openshift-cnv" }}
  labels:
    platform.kubevirt.io/managed-by: virt-platform-autopilot
spec:
  config:
    display:
      name: Descheduler / Memory-Aware Rebalancing
    duration: 1h
    refreshInterval: 0s
    variables:
      - kind: ListVariable
        spec:
          name: node
          display:
            name: Node
            hidden: false
          allowMultiple: true
          allowAllValue: true
          defaultValue: $__all
          plugin:
            kind: PrometheusLabelValuesVariable
            spec:
              datasource:
                kind: PrometheusDatasource
                name: perses-thanos-datasource
              labelName: instance
              matchers:
                - descheduler:nodeutilization:cpu:avg1m
      - kind: ListVariable
        spec:
          name: resolution
          display:
            name: Classification Resolution
            hidden: false
          allowMultiple: false
          allowAllValue: false
          defaultValue: 5m
          plugin:
            kind: StaticListVariable
            spec:
              values:
                - 1m
                - 2m
                - 5m
                - 10m
      - kind: ListVariable
        spec:
          name: limit
          display:
            name: Max evictions shown
            hidden: false
          allowMultiple: false
          allowAllValue: false
          defaultValue: '15'
          plugin:
            kind: StaticListVariable
            spec:
              values:
                - '10'
                - '15'
                - '20'
    panels:
      cpuUtilByNode:
        kind: Panel
        spec:
          display:
            name: CPU Utilization — per Node
            description: CPU utilization over time for each node. The red ★ avg workers series is the fleet average — nodes far from it are candidates for rebalancing.
          plugin:
            kind: TimeSeriesChart
            spec:
              legend:
                position: right
                mode: list
              yAxis:
                label: CPU Utilization
                format:
                  unit: percent-decimal
                min: 0
              visual:
                display: line
                lineWidth: 1
                areaOpacity: 0.05
              querySettings:
                - queryIndex: 1
                  colorMode: fixed-single
                  colorValue: '#888888'
                - queryIndex: 2
                  colorMode: fixed-single
                  colorValue: '#e02020'
          queries:
            - kind: TimeSeriesQuery
              spec:
                plugin:
                  kind: PrometheusTimeSeriesQuery
                  spec:
                    datasource:
                      kind: PrometheusDatasource
                      name: perses-thanos-datasource
                    query: descheduler:nodeutilization:cpu:avg1m{instance=~"$node"} and on(instance) label_replace(kube_node_role{role="worker"}, "instance", "$1", "node", "(.*)")
                    seriesNameFormat: '{{"{{instance}}"}}'
            - kind: TimeSeriesQuery
              spec:
                plugin:
                  kind: PrometheusTimeSeriesQuery
                  spec:
                    datasource:
                      kind: PrometheusDatasource
                      name: perses-thanos-datasource
                    query: descheduler:nodeutilization:cpu:avg1m{instance=~"$node"} and on(instance) label_replace(kube_node_role{role="master"}, "instance", "$1", "node", "(.*)")
                    seriesNameFormat: (M) {{"{{instance}}"}}
            - kind: TimeSeriesQuery
              spec:
                plugin:
                  kind: PrometheusTimeSeriesQuery
                  spec:
                    datasource:
                      kind: PrometheusDatasource
                      name: perses-thanos-datasource
                    query: descheduler:averageworkersutilization:cpu:avg1m{} or vector(0)
                    seriesNameFormat: ★ avg workers
      memUtilByNode:
        kind: Panel
        spec:
          display:
            name: Memory Utilization — per Node
            description: Memory utilization over time for each node. The red ★ avg workers series is the fleet average — nodes far from it are candidates for rebalancing.
          plugin:
            kind: TimeSeriesChart
            spec:
              legend:
                position: right
                mode: list
              yAxis:
                label: Memory Utilization
                format:
                  unit: percent-decimal
                min: 0
              visual:
                display: line
                lineWidth: 1
                areaOpacity: 0.05
              querySettings:
                - queryIndex: 1
                  colorMode: fixed-single
                  colorValue: '#888888'
                - queryIndex: 2
                  colorMode: fixed-single
                  colorValue: '#e02020'
          queries:
            - kind: TimeSeriesQuery
              spec:
                plugin:
                  kind: PrometheusTimeSeriesQuery
                  spec:
                    datasource:
                      kind: PrometheusDatasource
                      name: perses-thanos-datasource
                    query: descheduler:nodeutilization:memory:avg1m{instance=~"$node"} and on(instance) label_replace(kube_node_role{role="worker"}, "instance", "$1", "node", "(.*)")
                    seriesNameFormat: '{{"{{instance}}"}}'
            - kind: TimeSeriesQuery
              spec:
                plugin:
                  kind: PrometheusTimeSeriesQuery
                  spec:
                    datasource:
                      kind: PrometheusDatasource
                      name: perses-thanos-datasource
                    query: descheduler:nodeutilization:memory:avg1m{instance=~"$node"} and on(instance) label_replace(kube_node_role{role="master"}, "instance", "$1", "node", "(.*)")
                    seriesNameFormat: (M) {{"{{instance}}"}}
            - kind: TimeSeriesQuery
              spec:
                plugin:
                  kind: PrometheusTimeSeriesQuery
                  spec:
                    datasource:
                      kind: PrometheusDatasource
                      name: perses-thanos-datasource
                    query: descheduler:averageworkersutilization:memory:avg1m{} or vector(0)
                    seriesNameFormat: ★ avg workers
      cpuPressureByNode:
        kind: Panel
        spec:
          display:
            name: CPU Pressure (PSI) — per Node
            description: CPU PSI pressure over time per node. Near-zero under normal conditions. The red ★ avg workers is the fleet average.
          plugin:
            kind: TimeSeriesChart
            spec:
              legend:
                position: right
                mode: list
              yAxis:
                label: CPU Pressure
                format:
                  unit: percent-decimal
                min: 0
              visual:
                display: line
                lineWidth: 1
                areaOpacity: 0.05
              querySettings:
                - queryIndex: 1
                  colorMode: fixed-single
                  colorValue: '#888888'
                - queryIndex: 2
                  colorMode: fixed-single
                  colorValue: '#e02020'
          queries:
            - kind: TimeSeriesQuery
              spec:
                plugin:
                  kind: PrometheusTimeSeriesQuery
                  spec:
                    datasource:
                      kind: PrometheusDatasource
                      name: perses-thanos-datasource
                    query: descheduler:nodepressure:cpu:avg1m{instance=~"$node"} and on(instance) label_replace(kube_node_role{role="worker"}, "instance", "$1", "node", "(.*)")
                    seriesNameFormat: '{{"{{instance}}"}}'
            - kind: TimeSeriesQuery
              spec:
                plugin:
                  kind: PrometheusTimeSeriesQuery
                  spec:
                    datasource:
                      kind: PrometheusDatasource
                      name: perses-thanos-datasource
                    query: descheduler:nodepressure:cpu:avg1m{instance=~"$node"} and on(instance) label_replace(kube_node_role{role="master"}, "instance", "$1", "node", "(.*)")
                    seriesNameFormat: (M) {{"{{instance}}"}}
            - kind: TimeSeriesQuery
              spec:
                plugin:
                  kind: PrometheusTimeSeriesQuery
                  spec:
                    datasource:
                      kind: PrometheusDatasource
                      name: perses-thanos-datasource
                    query: descheduler:averageworkerspressure:cpu:avg1m{} or vector(0)
                    seriesNameFormat: ★ avg workers
      memPressureByNode:
        kind: Panel
        spec:
          display:
            name: Memory Pressure (PSI) — per Node
            description: Memory PSI pressure over time per node. Near-zero under normal conditions. The red ★ avg workers is the fleet average.
          plugin:
            kind: TimeSeriesChart
            spec:
              legend:
                position: right
                mode: list
              yAxis:
                label: Memory Pressure
                format:
                  unit: percent-decimal
                min: 0
              visual:
                display: line
                lineWidth: 1
                areaOpacity: 0.05
              querySettings:
                - queryIndex: 1
                  colorMode: fixed-single
                  colorValue: '#888888'
                - queryIndex: 2
                  colorMode: fixed-single
                  colorValue: '#e02020'
          queries:
            - kind: TimeSeriesQuery
              spec:
                plugin:
                  kind: PrometheusTimeSeriesQuery
                  spec:
                    datasource:
                      kind: PrometheusDatasource
                      name: perses-thanos-datasource
                    query: descheduler:nodepressure:memory:avg1m{instance=~"$node"} and on(instance) label_replace(kube_node_role{role="worker"}, "instance", "$1", "node", "(.*)")
                    seriesNameFormat: '{{"{{instance}}"}}'
            - kind: TimeSeriesQuery
              spec:
                plugin:
                  kind: PrometheusTimeSeriesQuery
                  spec:
                    datasource:
                      kind: PrometheusDatasource
                      name: perses-thanos-datasource
                    query: descheduler:nodepressure:memory:avg1m{instance=~"$node"} and on(instance) label_replace(kube_node_role{role="master"}, "instance", "$1", "node", "(.*)")
                    seriesNameFormat: (M) {{"{{instance}}"}}
            - kind: TimeSeriesQuery
              spec:
                plugin:
                  kind: PrometheusTimeSeriesQuery
                  spec:
                    datasource:
                      kind: PrometheusDatasource
                      name: perses-thanos-datasource
                    query: descheduler:averageworkerspressure:memory:avg1m{} or vector(0)
                    seriesNameFormat: ★ avg workers
      utilizationValues:
        kind: Panel
        spec:
          display:
            name: Synthetic Node Utilization Value vs Dynamic Thresholds
            description: The descheduler's computed synthetic utilization score per node (combining CPU and memory). Nodes above the high threshold (red dashed) are Overutilized; nodes below the low threshold (orange dashed) are Underutilized and will receive migrated workloads.
          plugin:
            kind: TimeSeriesChart
            spec:
              legend:
                position: right
                mode: table
                values:
                  - last
              yAxis:
                label: Utilization Value
                format:
                  unit: percent-decimal
                min: 0
              visual:
                display: line
                lineWidth: 1
                areaOpacity: 0.05
              querySettings:
                - queryIndex: 1
                  colorMode: fixed-single
                  colorValue: '#888888'
                - queryIndex: 2
                  colorMode: fixed-single
                  colorValue: '#f2495c'
                - queryIndex: 3
                  colorMode: fixed-single
                  colorValue: '#ff9830'
          queries:
            - kind: TimeSeriesQuery
              spec:
                plugin:
                  kind: PrometheusTimeSeriesQuery
                  spec:
                    datasource:
                      kind: PrometheusDatasource
                      name: perses-thanos-datasource
                    query: descheduler_lownodeutilization_node_utilization_value{node=~"$node"} and on(node) kube_node_role{role="worker"}
                    seriesNameFormat: '{{"{{node}}"}}'
            - kind: TimeSeriesQuery
              spec:
                plugin:
                  kind: PrometheusTimeSeriesQuery
                  spec:
                    datasource:
                      kind: PrometheusDatasource
                      name: perses-thanos-datasource
                    query: descheduler_lownodeutilization_node_utilization_value{node=~"$node"} and on(node) kube_node_role{role="master"}
                    seriesNameFormat: (M) {{"{{node}}"}}
            - kind: TimeSeriesQuery
              spec:
                plugin:
                  kind: PrometheusTimeSeriesQuery
                  spec:
                    datasource:
                      kind: PrometheusDatasource
                      name: perses-thanos-datasource
                    query: avg(descheduler_lownodeutilization_node_utilization_threshold{threshold_type="high"}) or vector(0)
                    seriesNameFormat: High Threshold
            - kind: TimeSeriesQuery
              spec:
                plugin:
                  kind: PrometheusTimeSeriesQuery
                  spec:
                    datasource:
                      kind: PrometheusDatasource
                      name: perses-thanos-datasource
                    query: avg(descheduler_lownodeutilization_node_utilization_threshold{threshold_type="low"}) or vector(0)
                    seriesNameFormat: Low Threshold
      nodeClassification:
        kind: Panel
        spec:
          display:
            name: Node Classification over Time
            description: 'Each row is a node. The color shows the worst-case classification within each resolution bucket: Overutilized (red, workloads will be evicted/migrated away) > Normal (green, balanced) > Underutilized (blue, will receive workloads). Resolution is controlled by the Classification Resolution variable.'
          plugin:
            kind: StatusHistoryChart
            spec:
              legend:
                position: bottom
              mappings:
                - kind: Value
                  spec:
                    value: '0'
                    result:
                      value: Underutilized
                      color: '#5794f2'
                - kind: Value
                  spec:
                    value: '1'
                    result:
                      value: Normal
                      color: '#73bf69'
                - kind: Value
                  spec:
                    value: '2'
                    result:
                      value: Overutilized
                      color: '#f2495c'
          queries:
            - kind: TimeSeriesQuery
              spec:
                plugin:
                  kind: PrometheusTimeSeriesQuery
                  spec:
                    datasource:
                      kind: PrometheusDatasource
                      name: perses-thanos-datasource
                    query: max_over_time((descheduler_lownodeutilization_node_classification{node=~"$node"} and on(node) kube_node_role{role="worker"})[$resolution:$resolution])
                    seriesNameFormat: '{{"{{node}}"}}'
            - kind: TimeSeriesQuery
              spec:
                plugin:
                  kind: PrometheusTimeSeriesQuery
                  spec:
                    datasource:
                      kind: PrometheusDatasource
                      name: perses-thanos-datasource
                    query: max_over_time((descheduler_lownodeutilization_node_classification{node=~"$node"} and on(node) kube_node_role{role="master"})[$resolution:$resolution])
                    seriesNameFormat: (M) {{"{{node}}"}}
      totalEvictions:
        kind: Panel
        spec:
          display:
            name: Evictions Summary
            description: 'Counts over the selected timeframe. Completed: evictions that finished (regular pods evicted directly + VM migrations that settled). VM Migrations Started: live migrations initiated for virt-launcher pods (background evictions). Failed: eviction errors.'
          plugin:
            kind: StatChart
            spec:
              calculation: last-number
              format:
                unit: decimal
                decimalPlaces: 0
          queries:
            - kind: TimeSeriesQuery
              spec:
                plugin:
                  kind: PrometheusTimeSeriesQuery
                  spec:
                    datasource:
                      kind: PrometheusDatasource
                      name: perses-thanos-datasource
                    query: round(increase((sum(descheduler_pods_evicted_total{profile="KubeVirtRelieveAndMigrate", result="success", node=~"$node"} or vector(0)))[$__range:1m]))
                    seriesNameFormat: Completed
            - kind: TimeSeriesQuery
              spec:
                plugin:
                  kind: PrometheusTimeSeriesQuery
                  spec:
                    datasource:
                      kind: PrometheusDatasource
                      name: perses-thanos-datasource
                    query: round(increase((sum(descheduler_pods_evicted_total{profile="KubeVirtRelieveAndMigrate", result="background", node=~"$node"} or vector(0)))[$__range:1m]))
                    seriesNameFormat: VM Migrations Started
            - kind: TimeSeriesQuery
              spec:
                plugin:
                  kind: PrometheusTimeSeriesQuery
                  spec:
                    datasource:
                      kind: PrometheusDatasource
                      name: perses-thanos-datasource
                    query: round(increase((sum(descheduler_pods_evicted_total{profile="KubeVirtRelieveAndMigrate", result="error", node=~"$node"} or vector(0)))[$__range:1m]))
                    seriesNameFormat: Failed
      evictionsRate:
        kind: Panel
        spec:
          display:
            name: Evictions (KubeVirtRelieveAndMigrate) — per Node, rolling 5 min
            description: Number of successful evictions in the trailing 5-minute window per node.
          plugin:
            kind: TimeSeriesChart
            spec:
              legend:
                position: right
                mode: table
                values:
                  - last
              yAxis:
                label: Evictions (last 5 min)
                format:
                  unit: decimal
                  decimalPlaces: 0
                min: 0
              visual:
                display: line
                lineWidth: 2
                areaOpacity: 0.4
          queries:
            - kind: TimeSeriesQuery
              spec:
                plugin:
                  kind: PrometheusTimeSeriesQuery
                  spec:
                    datasource:
                      kind: PrometheusDatasource
                      name: perses-thanos-datasource
                    query: clamp_min(descheduler_pods_evicted_total{profile="KubeVirtRelieveAndMigrate", result="success", node=~"$node"} - (descheduler_pods_evicted_total{profile="KubeVirtRelieveAndMigrate", result="success", node=~"$node"} offset 5m or descheduler_pods_evicted_total{profile="KubeVirtRelieveAndMigrate", result="success", node=~"$node"} * 0), 0)
                    seriesNameFormat: '{{"{{node}}"}}'
      evictionHistory:
        kind: Panel
        spec:
          display:
            name: Recent Evictions — by Node & Namespace
            description: $limit (node, namespace) pairs with the most recent eviction in the trailing 6 hours, sorted by recency.
          plugin:
            kind: Table
            spec:
              density: compact
              columnSettings:
                - name: node
                  header: Node
                  enableSorting: true
                - name: exported_namespace
                  header: Namespace
                  enableSorting: true
                - name: value
                  header: Last eviction
                  enableSorting: true
                  sort: asc
                  align: right
                  format:
                    unit: seconds
                - name: timestamp
                  hide: true
                - name: endpoint
                  hide: true
                - name: instance
                  hide: true
                - name: job
                  hide: true
                - name: namespace
                  hide: true
                - name: pod
                  hide: true
                - name: profile
                  hide: true
                - name: result
                  hide: true
                - name: service
                  hide: true
          queries:
            - kind: TimeSeriesQuery
              spec:
                plugin:
                  kind: PrometheusTimeSeriesQuery
                  spec:
                    datasource:
                      kind: PrometheusDatasource
                      name: perses-thanos-datasource
                    query: bottomk($limit, time() - (max_over_time((time() * (((descheduler_pods_evicted_total{profile="KubeVirtRelieveAndMigrate", result="success", node=~"$node"} > descheduler_pods_evicted_total{profile="KubeVirtRelieveAndMigrate", result="success", node=~"$node"} offset 5m) or (descheduler_pods_evicted_total{profile="KubeVirtRelieveAndMigrate", result="success", node=~"$node"} unless descheduler_pods_evicted_total{profile="KubeVirtRelieveAndMigrate", result="success", node=~"$node"} offset 5m)) > bool 0))[6h:1m]) > 0))
      evictionsTimeSeries:
        kind: Panel
        spec:
          display:
            name: Completed Evictions (KubeVirtRelieveAndMigrate) — per Node
            description: Cumulative completed evictions (result=success) over time broken down by source node.
          plugin:
            kind: TimeSeriesChart
            spec:
              legend:
                position: right
                mode: table
                values:
                  - last
              yAxis:
                label: Evicted Pods
                format:
                  unit: decimal
                  decimalPlaces: 0
                min: 0
              visual:
                display: line
                lineWidth: 2
                areaOpacity: 0.1
          queries:
            - kind: TimeSeriesQuery
              spec:
                plugin:
                  kind: PrometheusTimeSeriesQuery
                  spec:
                    datasource:
                      kind: PrometheusDatasource
                      name: perses-thanos-datasource
                    query: descheduler_pods_evicted_total{profile="KubeVirtRelieveAndMigrate", result="success", node=~"$node"}
                    seriesNameFormat: '{{"{{node}}"}}'
      kubevirtCooldownBlocksStat:
        kind: Panel
        spec:
          display:
            name: Cooldown Blocks (total)
            description: Total virt-launcher evictions deferred by the PreEvictionFilter because the VMI completed a migration within the configured cooldown window. Resets when the descheduler restarts.
          plugin:
            kind: StatChart
            spec:
              calculation: last-number
              format:
                unit: decimal
                decimalPlaces: 0
          queries:
            - kind: TimeSeriesQuery
              spec:
                plugin:
                  kind: PrometheusTimeSeriesQuery
                  spec:
                    datasource:
                      kind: PrometheusDatasource
                      name: perses-thanos-datasource
                    query: sum(descheduler_kubevirt_eviction_blocks_total{reason="cooldown"}) or vector(0)
                    seriesNameFormat: Cooldown blocks
      kubevirtInflightBlocksStat:
        kind: Panel
        spec:
          display:
            name: In-flight Migration Blocks (total)
            description: Total virt-launcher evictions hard-blocked by the Filter because the VMI was still mid-migration (startTimestamp set, endTimestamp absent). Resets when the descheduler restarts.
          plugin:
            kind: StatChart
            spec:
              calculation: last-number
              format:
                unit: decimal
                decimalPlaces: 0
          queries:
            - kind: TimeSeriesQuery
              spec:
                plugin:
                  kind: PrometheusTimeSeriesQuery
                  spec:
                    datasource:
                      kind: PrometheusDatasource
                      name: perses-thanos-datasource
                    query: sum(descheduler_kubevirt_eviction_blocks_total{reason="migration_in_progress"}) or vector(0)
                    seriesNameFormat: In-flight blocks
      kubevirtBlocksByReason:
        kind: Panel
        spec:
          display:
            name: Eviction Block Rate — by Reason
            description: Rate of eviction deferrals per minute. 'cooldown' (PreEvictionFilter) fires when a VM migrated recently and is still within its adaptive protection window. 'migration_in_progress' (Filter) fires when the VMI is still mid-flight. A sustained 'cooldown' signal means the descheduler is being held back from re-evicting VMs it just moved — exactly the protection the plugin is designed to provide.
          plugin:
            kind: TimeSeriesChart
            spec:
              legend:
                position: right
                mode: table
                values:
                  - last
              yAxis:
                label: Blocks / min
                format:
                  unit: decimal
                  decimalPlaces: 2
                min: 0
              visual:
                display: line
                lineWidth: 2
                areaOpacity: 0.3
          queries:
            - kind: TimeSeriesQuery
              spec:
                plugin:
                  kind: PrometheusTimeSeriesQuery
                  spec:
                    datasource:
                      kind: PrometheusDatasource
                      name: perses-thanos-datasource
                    query: sum(rate(descheduler_kubevirt_eviction_blocks_total{reason="cooldown", node=~"$node"}[5m])) * 60 or vector(0)
                    seriesNameFormat: cooldown (PreEvictionFilter)
            - kind: TimeSeriesQuery
              spec:
                plugin:
                  kind: PrometheusTimeSeriesQuery
                  spec:
                    datasource:
                      kind: PrometheusDatasource
                      name: perses-thanos-datasource
                    query: sum(rate(descheduler_kubevirt_eviction_blocks_total{reason="migration_in_progress", node=~"$node"}[5m])) * 60 or vector(0)
                    seriesNameFormat: migration in progress (Filter)
      kubevirtBlocksByNode:
        kind: Panel
        spec:
          display:
            name: Eviction Block Rate — by Node
            description: Rate of eviction deferrals per minute split by the source node of the blocked virt-launcher pod. A persistently high value for a specific node means the descheduler keeps identifying it as overloaded and targeting its VMs, but they are all still within their cooldown windows — a sign of a chronic hotspot the plugin is actively protecting against churn.
          plugin:
            kind: TimeSeriesChart
            spec:
              legend:
                position: right
                mode: table
                values:
                  - last
              yAxis:
                label: Blocks / min
                format:
                  unit: decimal
                  decimalPlaces: 2
                min: 0
              visual:
                display: line
                lineWidth: 2
                areaOpacity: 0.15
          queries:
            - kind: TimeSeriesQuery
              spec:
                plugin:
                  kind: PrometheusTimeSeriesQuery
                  spec:
                    datasource:
                      kind: PrometheusDatasource
                      name: perses-thanos-datasource
                    query: sum(rate(descheduler_kubevirt_eviction_blocks_total{node=~"$node"}[5m])) by (node) * 60 or vector(0)
                    seriesNameFormat: '{{"{{node}}"}}'
      actuationScoreRawVsDampened:
        kind: Panel
        spec:
          display:
            name: Actuation Score — Raw vs Dampened per Node
            description: 'Solid lines: raw stable distance score (descheduler:node:linear_amplified_ideal_point_positive_distance:k3:p66_5m). Dashed lines: dampened score after the cooldown multiplier is applied (descheduler:node:actuation_priority:p66_5m). The gap between solid and dashed for the same node is the suppression introduced by recent evictions. When the lines overlap the node has not been recently evicted from and is fully eligible.'
          plugin:
            kind: TimeSeriesChart
            spec:
              legend:
                position: right
                mode: table
                values:
                  - last
              yAxis:
                label: Score
                format:
                  unit: percent-decimal
                min: 0
              visual:
                display: line
                lineWidth: 2
                areaOpacity: 0.05
          queries:
            - kind: TimeSeriesQuery
              spec:
                plugin:
                  kind: PrometheusTimeSeriesQuery
                  spec:
                    datasource:
                      kind: PrometheusDatasource
                      name: perses-thanos-datasource
                    query: descheduler:node:linear_amplified_ideal_point_positive_distance:k3:p66_5m{instance=~"$node"}
                    seriesNameFormat: raw — {{"{{instance}}"}}
            - kind: TimeSeriesQuery
              spec:
                plugin:
                  kind: PrometheusTimeSeriesQuery
                  spec:
                    datasource:
                      kind: PrometheusDatasource
                      name: perses-thanos-datasource
                    query: descheduler:node:actuation_priority:p66_5m{instance=~"$node"}
                    seriesNameFormat: dampened — {{"{{instance}}"}}
      cooldownMultiplierByNode:
        kind: Panel
        spec:
          display:
            name: Cooldown Multiplier per Node
            description: The dampening multiplier applied to each node's actuation priority score (descheduler:node:cooldown_multiplier:10m). Starts at 1.0 (no suppression). Each successful eviction from a node in the last 10 minutes reduces it by 0.10. Drops to 0.01 after 10 evictions (fully muted). Watch this alongside the eviction rate to understand how much each node is being suppressed at any moment.
          plugin:
            kind: TimeSeriesChart
            spec:
              legend:
                position: right
                mode: table
                values:
                  - last
              yAxis:
                label: Multiplier
                format:
                  unit: percent-decimal
                min: 0
              visual:
                display: line
                lineWidth: 2
                areaOpacity: 0.15
          queries:
            - kind: TimeSeriesQuery
              spec:
                plugin:
                  kind: PrometheusTimeSeriesQuery
                  spec:
                    datasource:
                      kind: PrometheusDatasource
                      name: perses-thanos-datasource
                    query: descheduler:node:cooldown_multiplier:10m{instance=~"$node"}
                    seriesNameFormat: '{{"{{instance}}"}}'
      kubevirtCooldownQuantiles:
        kind: Panel
        spec:
          display:
            name: Effective Cooldown Duration — p50 / p90 / p99
            description: Distribution of the adaptive cooldown applied when an eviction is deferred. Base cooldown is max(configured 15 min, migration duration). For each additional migration recorded within the 24-hour history window the base is doubled (exponential backoff), capped at 6 hours. When p99 >> p50 some VMs have been migrated multiple times and are receiving progressively longer protection — the plugin is suppressing oscillation.
          plugin:
            kind: TimeSeriesChart
            spec:
              legend:
                position: right
                mode: table
                values:
                  - last
              yAxis:
                label: Cooldown Duration
                format:
                  unit: seconds
                min: 0
              visual:
                display: line
                lineWidth: 2
                areaOpacity: 0.1
          queries:
            - kind: TimeSeriesQuery
              spec:
                plugin:
                  kind: PrometheusTimeSeriesQuery
                  spec:
                    datasource:
                      kind: PrometheusDatasource
                      name: perses-thanos-datasource
                    query: (histogram_quantile(0.50, sum(rate(descheduler_kubevirt_effective_cooldown_seconds_bucket[15m])) by (le)) >= 0) or vector(0)
                    seriesNameFormat: p50
            - kind: TimeSeriesQuery
              spec:
                plugin:
                  kind: PrometheusTimeSeriesQuery
                  spec:
                    datasource:
                      kind: PrometheusDatasource
                      name: perses-thanos-datasource
                    query: (histogram_quantile(0.90, sum(rate(descheduler_kubevirt_effective_cooldown_seconds_bucket[15m])) by (le)) >= 0) or vector(0)
                    seriesNameFormat: p90
            - kind: TimeSeriesQuery
              spec:
                plugin:
                  kind: PrometheusTimeSeriesQuery
                  spec:
                    datasource:
                      kind: PrometheusDatasource
                      name: perses-thanos-datasource
                    query: (histogram_quantile(0.99, sum(rate(descheduler_kubevirt_effective_cooldown_seconds_bucket[15m])) by (le)) >= 0) or vector(0)
                    seriesNameFormat: p99
    layouts:
      - kind: Grid
        spec:
          display:
            title: Utilization — CPU & Memory per Node
            collapse:
              open: true
          items:
            - x: 0
              "y": 0
              width: 12
              height: 10
              content:
                $ref: '#/spec/panels/cpuUtilByNode'
            - x: 12
              "y": 0
              width: 12
              height: 10
              content:
                $ref: '#/spec/panels/memUtilByNode'
      - kind: Grid
        spec:
          display:
            title: Pressure (PSI) — CPU & Memory per Node
            collapse:
              open: true
          items:
            - x: 0
              "y": 0
              width: 12
              height: 10
              content:
                $ref: '#/spec/panels/cpuPressureByNode'
            - x: 12
              "y": 0
              width: 12
              height: 10
              content:
                $ref: '#/spec/panels/memPressureByNode'
      - kind: Grid
        spec:
          display:
            title: Synthetic Utilization Value & Dynamic Thresholds
            collapse:
              open: true
          items:
            - x: 0
              "y": 0
              width: 24
              height: 10
              content:
                $ref: '#/spec/panels/utilizationValues'
      - kind: Grid
        spec:
          display:
            title: Actuation Score & Dampening Multiplier
            collapse:
              open: true
          items:
            - x: 0
              "y": 0
              width: 16
              height: 10
              content:
                $ref: '#/spec/panels/actuationScoreRawVsDampened'
            - x: 16
              "y": 0
              width: 8
              height: 10
              content:
                $ref: '#/spec/panels/cooldownMultiplierByNode'
      - kind: Grid
        spec:
          display:
            title: Node Classification over Time
            collapse:
              open: false
          items:
            - x: 0
              "y": 0
              width: 24
              height: 10
              content:
                $ref: '#/spec/panels/nodeClassification'
      - kind: Grid
        spec:
          display:
            title: Evictions
            collapse:
              open: true
          items:
            - x: 0
              "y": 0
              width: 4
              height: 8
              content:
                $ref: '#/spec/panels/totalEvictions'
            - x: 4
              "y": 0
              width: 20
              height: 8
              content:
                $ref: '#/spec/panels/evictionsTimeSeries'
            - x: 0
              "y": 8
              width: 24
              height: 8
              content:
                $ref: '#/spec/panels/evictionsRate'
            - x: 0
              "y": 16
              width: 24
              height: 10
              content:
                $ref: '#/spec/panels/evictionHistory'
      - kind: Grid
        spec:
          display:
            title: KubevirtMigrationAware — Live Migration Protection
            collapse:
              open: true
          items:
            - x: 0
              "y": 0
              width: 6
              height: 4
              content:
                $ref: '#/spec/panels/kubevirtCooldownBlocksStat'
            - x: 0
              "y": 4
              width: 6
              height: 4
              content:
                $ref: '#/spec/panels/kubevirtInflightBlocksStat'
            - x: 6
              "y": 0
              width: 18
              height: 8
              content:
                $ref: '#/spec/panels/kubevirtBlocksByReason'
            - x: 0
              "y": 8
              width: 12
              height: 10
              content:
                $ref: '#/spec/panels/kubevirtBlocksByNode'
            - x: 12
              "y": 8
              width: 12
              height: 10
              content:
                $ref: '#/spec/panels/kubevirtCooldownQuantiles'
