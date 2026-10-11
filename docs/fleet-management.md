---
title: Fleet Management
description: Registering synthetic Alloy collectors with Grafana Cloud Fleet Management and emitting their health signals without real probe agents.
---

# Fleet Management

synthkit's Fleet Management integration registers fake Alloy collectors with the Grafana Cloud Fleet Management API and emits their full Alloy self-metric set. The result is a realistic collector roster on the Fleet Management page — complete with per-OS breakdowns and component health — with no real Alloy agents running.

---

## What it does

At startup, one aggregate selected-roster controller goroutine owns all FM API side effects:

1. **Register** — each fake collector calls `RegisterCollector` with its deterministic ID, OS type, collector version, and `local_attributes` (including `collector.os` and `collector.version`, which drive the FM UI's "Operating System" and "Alloy version" columns).
2. **Heartbeat** — every 45 seconds, each collector calls `GetConfig` with `local_attributes` set. FM tracks liveness by heartbeat recency; omitting `local_attributes` causes collectors to go inactive.
3. **Reconcile** — collectors absent from the complete selected roster are unregistered, including persisted IDs left by a previous leader or removed blueprint. Ordinary shutdown/restart preserves registration; only explicit teardown removes the selected current roster.

### Restart, handoff, and teardown

Live registration records collector-ID intent in the existing gated state backend **before** the
HTTP registration request. An ambiguous response or crash therefore cannot lose the cleanup ID.
Successful unregistration removes the ID through compare-and-swap; failures retain it for retry.
One controller owns the aggregate ledger, so a blueprint-local diff cannot delete sibling collectors.
An empty selected roster still reconciles persisted departed IDs. The version-2 ledger binds these
IDs to a SHA-256 destination identity derived from `GC_FM_URL` (with trailing slashes removed) and
`GC_FM_STACK_ID`, never the token. Token rotation at the same destination preserves cleanup ownership.
A changed URL or stack is visibly rejected before any Fleet request or ledger mutation, even for an
empty roster. Retain the old ledger and restore its original destination to clean up its collectors;
use a separate state slot for a new destination. Nonempty legacy version-1 ledgers have no destination
binding and are rejected rather than silently relabelled; they require explicit ownership recovery.
Only missing state or an empty legacy ledger can initialize a new binding. No credentials, collector
attributes, or returned configuration are persisted in this document. Dry-run does not write it.

File mode retains the ledger beside `CONFIG_SNAPSHOT_PATH` with a `.fleet.json` suffix. Keep this
file across restarts. Kubernetes mode requires `STATE_FLEET_CONFIGMAP`, a distinct pre-created
ConfigMap in `HA_NAMESPACE`, with named get/update permissions; runtime never creates it.
Chart provisioning and RBAC wiring are prerequisites for using this slot in a chart deployment.

For intentional teardown, stop the ordinary emitter first, select the same blueprint/source set,
and run `DRY_RUN=false ./synthkit -fleet-unregister`. The command rejects dry-run rather than
crediting planned requests as successful teardown. This removes exactly the collectors the resolved selected
roster would register (including mirror collectors and current persisted scaling), without registering
or heartbeating them; unselected persisted IDs remain. A successful command exits zero. Missing
authentication, unavailable/corrupt state or an unregister failure exits nonzero. The HA-off teardown
shares `SEND_DRAIN_DEADLINE` across the whole unregister batch, not a fresh budget per collector. In lease mode the
command waits for leadership and uses the existing gate and bounded handoff lifecycle. Interruption
before leadership or before successful unregister completion exits nonzero; a never-led standby
neither deletes collectors nor releases another process's lease. Ordinary lease handoff does not
unregister collectors. Teardown and another emitter must not run concurrently
with HA off, because file state is not a distributed lock.

The emitter reads the controller's roster and pushes Alloy self-metrics to Mimir each tick. Stale-series cleanup ensures that when the roster shrinks (or when collector scaling changes), departed collectors' `up=1` series do not persist after unregistration.

### Configuration receipt evidence

Authenticated `GET /control/status` reports each active synthetic collector's registration and
heartbeat state alongside bounded configuration-receipt evidence. A `received` receipt carries
only a SHA-256 digest of a non-empty returned configuration and its observation time. `stale`
means Fleet returned `notModified`; `unavailable` includes an empty or unsupported successful
response; `error` means the request did not yield a usable response. The previous valid digest is
retained across stale, unavailable, and error observations.

Receipt proves delivery only, never parsing or execution: synthetic collectors do not execute
returned Alloy configuration. Diagnostics omit configuration content, Fleet's opaque response
hash, credentials, and collector attributes. The response shape is pinned to
`grafana/alloy-remote-config` v0.0.12, `api/collector/v1/collector.proto`.

### Emitted signals

For each registered collector, synthkit emits the full Alloy self-metric set against `job="integrations/alloy"`:

| Metric | Type | Notes |
|---|---|---|
| `alloy_build_info` | Gauge | `version` must start `"v"` |
| `up` | Gauge | 1 = healthy, 0 = unhealthy |
| `alloy_component_controller_running_components` | Gauge | `health_type` ∈ {`healthy`, `unhealthy`, `unknown`, `exited`} |
| `alloy_component_evaluation_seconds` | Histogram | 24 obs/tick |
| `alloy_resources_process_cpu_seconds_total` | Counter | |
| `alloy_resources_process_resident_memory_bytes` | Gauge | |
| `alloy_resources_machine_rx_bytes_total` | Counter | |
| `alloy_resources_machine_tx_bytes_total` | Counter | |
| `alloy_config_hash` | Gauge | `hash=sha256hex(collectorID)` |
| `go_goroutines` | Gauge | |
| `go_memstats_heap_inuse_bytes` | Gauge | |
| `scrape_duration_seconds` | Gauge | |

Base labels on every series: `job="integrations/alloy"`, `namespace="infra"`, `cluster`, `k8s_cluster_name`, `instance`, `collector_id`, `os`.

Intentionally **not** emitted: `remotecfg_*`, `prometheus_remote_storage_*`. These are real-Alloy remote_write internals that would misrepresent a fake collector.

The full signal contract is in [`signals/fm.md`](https://github.com/rknightion/synthkit/blob/main/signals/fm.md) on GitHub.

---

## Credentials

FM registration uses its own credential pair — distinct from the Mimir push credentials.

!!! warning "GC_FM_STACK_ID is the Grafana Cloud stack ID, not GC_PROM_USER"
    The FM API uses HTTP Basic auth with the stack ID as the username. This is a different number from the Mimir instance ID (`GC_PROM_USER`). Check the Fleet Management page in Grafana Cloud for your stack ID.

| Env var | Description |
|---|---|
| `GC_FM_URL` | FM API URL, e.g. `https://fleet-management-prod-0NN.grafana.net` |
| `GC_FM_STACK_ID` | FM Basic-auth username = Grafana Cloud **stack ID** (not `GC_PROM_USER`) |
| `GC_FM_TOKEN` | CAP token with `fleet-management:write` scope |

Two modes are intentionally distinct:

- **Fleet metrics:** a selected `fleet_management` declaration plus the normal Prometheus lane.
  No FM API credentials are required.
- **Fleet registration:** the same declaration plus the complete `GC_FM_URL`, `GC_FM_STACK_ID`,
  and `GC_FM_TOKEN` triplet. Verify both registration and fresh heartbeats.

Authenticated `/control/status` reports these as separate `fleet_metrics` and
`fleet_registration` dispositions. An intentionally disabled registration lane is not a metrics
failure. Partial registration inputs fail closed; they do not silently downgrade to metrics-only.

See [Credentials](credentials.md) for token-scoping guidance.

---

## Enabling Fleet Management

Add a `fleet_management` block under `features:` in a blueprint. The construct is substrate-scoped: series are disambiguated by `collector_id` and carry no `blueprint` label.

### Standalone collector fleet

```yaml
features:
  fleet_management:
    enabled: true
    collectors_per_os:
      linux: 12
      windows: 4
      darwin: 3
```

Each OS entry produces that many fake collectors with deterministic IDs of the form `<blueprint>-<env>-<os>-<NN>`. Absent OS types emit nothing.

See [`blueprints/fleet-management.yaml`](https://github.com/rknightion/synthkit/blob/main/blueprints/fleet-management.yaml) for a complete reference example, or [`blueprints/hostfleet.yaml`](https://github.com/rknightion/synthkit/blob/main/blueprints/hostfleet.yaml) for a combined host-fleet setup.

### k8s-monitoring collector mirror

When a cluster has `k8s_monitoring.fleet_management: true`, synthkit additionally registers the Alloy collectors that a real `grafana/k8s-monitoring` Helm chart deployment (v3) would create — `alloy-metrics`, `alloy-singleton`, `alloy-logs`, `alloy-profiles`, `alloy-receiver` — each feature-gated to match the chart's behaviour. See [`blueprints/synthetic-monitoring.yaml`](https://github.com/rknightion/synthkit/blob/main/blueprints/synthetic-monitoring.yaml) for a combined SM + FM example.

---

## LLM-assisted setup

The `/setup-fleet-management` skill walks through credential configuration, `.env` setup, and live verification. Run it from Claude Code after opening this repo, or install the synthkit plugin from any compatible agent runtime:

```
/setup-fleet-management
```

See [Tools](tools.md) for the full skill inventory.

---

## FM page badge

The FM online-status badge (green/red) reflects heartbeat recency tracked by a Redis TTL in Grafana Cloud, not metric presence. A firing alert carrying `collector_id` is required to show a red "unhealthy" badge. Metric-only simulation affects dashboards but not the FM page badge — this is a cosmetic limitation of the fake-collector model.

---

## See also

- [`signals/fm.md`](https://github.com/rknightion/synthkit/blob/main/signals/fm.md) — full signal contract with label shapes and provenance
- [Configuration](configuration.md) — complete environment variable reference
- [Credentials](credentials.md) — token scoping and the two-stacks model
- [Tools](tools.md) — LLM skills including `/setup-fleet-management`
