---
title: Incidents & Scenarios
description: Declaring and activating failure modes — named scenario bundles, scheduled incidents, and live control-plane activation.
---

# Incidents & Scenarios

The incident model separates **what can happen** (the failure-mode vocabulary declared by each construct/workload) from **when it happens** (activation via a scheduled `incidents:` block or a live control-plane call).

A failure mode is not a synthetic error injection in a naive sense — it shifts the shape engine's multiplier for specific constructs so that the affected signal families brown out realistically: latency histograms right-tail, error counters climb, pod restarts increase, connection gauges approach max. The rest of the estate stays healthy.

## Scenarios: named failure bundles

A `scenarios:` block defines **reusable named bundles** of effects. Each effect declares a mode, a target instance, and an intensity in `[0, 1]`:

```yaml
scenarios:
  - name: database_overload
    title: Database overload storm
    summary: >
      Aurora connections saturate and query latency spikes;
      the API backend browns out.
    effects:
      - { mode: connection_saturation, target: mine-app-db,  intensity: 0.9 }
      - { mode: slow_query_storm,      target: mine-app-db,  intensity: 0.7 }
      - { mode: latency_spike,         target: mine-api,     intensity: 0.5 }

  - name: cluster_instability
    title: Cluster pod crash-loop
    effects:
      - { mode: pod_crashloop, target: mine-prod-use1, intensity: 0.4 }
      - { mode: oom_kill,      target: mine-prod-use1, intensity: 0.3 }
```

### Effect target addressing

The `target` field on an effect resolves to a specific declared instance:

| Target form | Meaning |
|---|---|
| `mine-app-db` | Exact instance name — the named database, cluster, workload, or service node |
| `database:*` | All database instances on the `database` axis |
| `cluster:*` | All k8s cluster instances |
| `workload:*` | All workload instances |
| `service:*` | All app service nodes across all app workloads |
| `cloud:*` | All cloud-scoped constructs (Bedrock, AgentCore, Portkey, etc.) |
| `network:*` | All network-topology instances |
| omitted | Valid only for single-axis modes — the mode's axis is inferred and all instances of that axis are targeted |

The resolver validates every effect target against the blueprint's actual instance inventory at load time. An unknown name or an axis mismatch is a loud load error.

A mode that appears on more than one axis (e.g. `lock_contention` exists on both `database` and `service`) **requires** an explicit target — the empty-target shorthand is rejected at load to avoid ambiguous targeting.

## Incidents: scheduled activations

An `incidents:` block schedules when a scenario or single-mode effect fires:

=== "Scenario reference"

    ```yaml
    incidents:
      - scenario: database_overload
        at: "2026-07-01T14:00"   # ISO-8601 wall time (local to the blueprint's timezone)
        for: 30m
    ```

=== "Single-mode (inline)"

    ```yaml
    incidents:
      - kind: pod_crashloop
        target: mine-prod-use1
        at: "2026-07-01T11:00"
        for: 15m
        intensity: 0.6
    ```

=== "Recurring (every)"

    ```yaml
    incidents:
      # Non-prod ambient churn: fires every 30 minutes, lasts 8 minutes.
      - kind: oom_kill
        target: mine-dev-use1
        every: 30m
        for: 8m
        intensity: 0.15
    ```

`scenario:` and `kind:` are mutually exclusive in one entry, and an entry needs exactly one of `at:` or `every:`. `at:` accepts either a full wall-clock timestamp (fires once) or a bare `HH:MM` (fires daily at that time); `every:` fires on the given interval continuously for the lifetime of the process.

`intensity` in `[0, 1]`: 0 is a no-op; 1 is the maximum effect the construct physics implement. Fractional intensities affect a proportional fraction of pods or metric series.

## Available failure modes

### cluster axis

| Mode | Effect |
|---|---|
| `oom_kill` | Containers OOM-killed; restart count climbs, status reason OOMKilled. `intensity` selects the fraction of pods affected. |
| `pod_crashloop` | Pods crash-looping; restarts climb, phase Pending not Running. `intensity` selects fraction of pods. |
| `node_not_ready` | A node flips NotReady; its pods go Pending. |

### database axis

| Mode | Effect |
|---|---|
| `connection_saturation` | Active connections climb toward max. |
| `replication_lag` | Replica falls behind primary. |
| `lock_contention` | Lock waits climb. (Also fires the `query_data_locks` dbo11y op when active.) |
| `slow_query_storm` | Query latency right-tail spikes. |

### workload axis (web_service)

| Mode | Effect |
|---|---|
| `latency_spike` | Elevated request latency (up to 4× at full intensity). |
| `error_burst` | Elevated 5xx error rate. |
| `cpu_hotspot` | CPU concentrated in a hot frame (profiling flamegraph). |
| `memory_leak` | Growing heap (profile sample values rise). |
| `lock_contention` | Elevated mutex/block contention (profile values). |
| `goroutine_leak` | Goroutine accumulation (profile values). |

### service axis (app workload — per-node)

| Mode | Effect |
|---|---|
| `latency_storm` | Elevated latency on the targeted service node. |
| `error_spike` | Elevated 5xx rate on the targeted service node. |
| `throughput_drop` | Reduced throughput on the targeted service node. |
| `fallback_storm` | Elevated gateway fallback rate on the targeted service node. |
| `retry_storm` | Elevated gateway retry rate on the targeted service node. |
| `cpu_hotspot` | Hot frame amplification on the targeted node (profiling). |
| `memory_leak` | Growing heap on the targeted node (profiling). |
| `lock_contention` | Mutex/block contention on the targeted node (profiling). |
| `goroutine_leak` | Goroutine accumulation on the targeted node (profiling). |
| `web_vitals_degraded` | Browser web-vitals degrade on the targeted frontend node — LCP/INP/TTFB/FCP/CLS spike. |

### cloud axis

| Mode | Effect |
|---|---|
| `bedrock_throttle` | Bedrock invocation throttling climbs. |
| `agentcore_throttle` | AgentCore request throttles + system_errors spike (region-scoped capacity constraint). |
| `portkey_scrape_degraded` | Portkey analytics scrape degrades — API error_rate and 4xx/5xx share climb, latency rises, poller falls behind. |
| `eval_quality_degraded` | LangSmith eval quality regresses — faithfulness/completeness/relevance and retrieval scores drop while retry/fallback/HITL rates and error/pending run-outcomes climb. |

### network axis

| Mode | Effect |
|---|---|
| `nettopo_devices_unreachable` | SNMP polling fails for a fraction of devices (walk errors spike, device discovery drops). |
| `nettopo_discovery_slow` | Discovery cycle duration inflates (cycle_duration_seconds and module walk times rise). |
| `nettopo_walker_degraded` | Walker outcome errors climb; edge count under-reports (partial topology visibility). |
| `nettopo_auth_failures` | SNMP credential trials fail (credential_trials_total error rate rises). |
| `nettopo_spoke_down` | A federation spoke goes offline (`network_topology_federation_spoke_up` drops to 0, hub/spoke session metrics degrade). |

## Definition vs activation

The `scenarios:` and `incidents:` blocks are the **definition** layer: they describe what modes exist in this blueprint and when they are scheduled to fire. The runner validates these at load time against the actual construct vocabulary and target inventory.

**Activation** is separate:

- The scheduled `incidents:` entries fire automatically according to their `at:` or `every:` schedule while the process runs.
- **Live activation** via the control plane is additive: it unions on top of any currently scheduled windows. A scenario activated live runs until explicitly deactivated, regardless of the `incidents:` schedule.

For live activation, see [Control Plane](control-plane.md). The control plane also exposes `GET /control/schema`, which returns the complete derived vocabulary — modes, addressable targets with current scaling state, and all named scenarios — for the loaded blueprints.

## Complete example

```yaml
name: mine
scenarios:
  - name: db_storm
    title: Database connection storm
    effects:
      - { mode: connection_saturation, target: mine-app-db, intensity: 0.8 }
      - { mode: slow_query_storm,      target: mine-app-db, intensity: 0.6 }
      - { mode: latency_spike,         target: mine-api,    intensity: 0.4 }

  - name: ai_brownout
    title: AI gateway brownout
    effects:
      - { mode: agentcore_throttle,                    intensity: 0.7 }
      - { mode: retry_storm, target: mine-api-backend, intensity: 0.5 }

incidents:
  # One-shot scheduled event
  - scenario: db_storm
    at: "2026-07-15T14:00"
    for: 25m

  # Ambient non-prod churn
  - kind: oom_kill
    target: mine-dev-eks
    every: 20m
    for: 5m
    intensity: 0.2

  # Single-mode with explicit target
  - kind: pod_crashloop
    target: mine-dev-eks
    at: "2026-07-15T10:05"
    for: 12m
    intensity: 0.5
```
