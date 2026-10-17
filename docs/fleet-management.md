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
3. **Reconcile** — collectors absent from the complete selected roster are unregistered, including persisted IDs left by a previous leader or removed blueprint. Ordinary shutdown/restart preserves registration; only explicit teardown removes collectors: `-fleet-unregister` unregisters the selected current roster, and `-fleet-unregister-owned` permanently deletes everything the persisted ledger records.

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
Only an empty document in existing state or an empty legacy ledger can initialize a new binding. No credentials, collector
attributes, or returned configuration are persisted in this document. Dry-run does not write it.

File mode retains the ledger beside `CONFIG_SNAPSHOT_PATH` with a `.fleet.json` suffix. Keep this
file across restarts. Kubernetes mode requires `STATE_FLEET_CONFIGMAP`, a distinct pre-created
ConfigMap in `HA_NAMESPACE`, with named get/update permissions; runtime never creates it.
For the HA chart, set `ha.fleetConfigMap` and configure the existing Fleet credential group
(`credentials.fm.existingSecret` plus `GC_FM_URL`, `GC_FM_STACK_ID`, `GC_FM_TOKEN` key mappings).
The chart maps the slot to `STATE_FLEET_CONFIGMAP` and grants only named get/update/patch;
it grants no create/list/watch/delete or Secret API access. A configured Fleet credential group
requires the slot even in dry-run. It may be predeclared before enabling registration and must
remain separate from every other HA state object, runtime configuration and blueprint-source input.
Use a runtime image supporting the mapping and destination-bound ledger.

Precreate the ConfigMap in the workload namespace outside Helm, preserving an existing object.
Only a new object starts with empty `binaryData`; the runtime writes `binaryData.document` through
resource-version compare-and-swap. Helm never renders, looks up or owns this mutable object,
so install/upgrade/rollback/uninstall cannot reapply an empty initializer. Retain the same slot
across leadership handoff and ordinary restart; backups and eventual cleanup belong to the
operator. Missing objects fail startup instead of triggering creation. With HA off, the chart
ignores `ha.fleetConfigMap` and retains the existing file ledger and volume defaults.
See [Kubernetes state provisioning](kubernetes.md#optional-stateless-ha-chart).

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

`-fleet-unregister` calls `UnregisterCollector`, which only marks each collector inactive in Fleet
Management. The collector record stays visible as inactive, and Fleet Management deletes it
automatically after 30 days of inactivity. To remove collectors immediately, use
`-fleet-unregister-owned`.

#### Full teardown from the ownership ledger

`-fleet-unregister` cannot certify a full teardown: historical selections, a scale-down whose
unregistration failed, or a disabled `fleet_management` kind can leave more owned identities in the
ledger than the current selection names. To remove all of them, stop the ordinary emitter and run
`DRY_RUN=false ./synthkit -fleet-unregister-owned`.

!!! warning "Owned teardown deletes collectors permanently"
    `-fleet-unregister-owned` calls the management `DeleteCollector` API. The collector record and
    its remote attributes are removed from Fleet Management at once and cannot be restored.
    `GC_FM_TOKEN` needs Fleet Management write access; the scope requirement follows the Terraform
    provider's collector resource, and a refused delete is reported as `rejected` with the ID kept
    in the ledger.

It needs the same `GC_FM_URL` and
`GC_FM_STACK_ID` the ledger was bound to and does not depend on the blueprint selection.

Deletion authority comes only from the ledger for that exact destination, never from collector
names, prefixes or the current roster; a collector the ledger does not record is never touched.
For each recorded ID, in sorted order, the command:

1. Reads it with `GetCollector`. An exact Connect `not_found` answer (HTTP 404 with
   `{"code":"not_found"}`) is confirmed absence. Any other answer, including a bare 404 from a
   wrong path or a proxy, is unknown and the ID is not mutated.
2. Sends `DeleteCollector` if the collector is present, then reads it again. It never relies on
   `UnregisterCollector`, because an unregistered collector is only marked inactive and still
   reads as present.
3. Releases the ledger entry through compare-and-swap only after a confirmed-absence read.

A lost or ambiguous response is resolved only by the following read, so a rerun is always safe.
Requests are spaced 350 ms apart to stay under the documented default Collector API limit of
3 requests per second, so each ID costs about one second. The HA-off run shares
`SEND_DRAIN_DEADLINE` across the whole ledger and a lease-mode run shares `HA_FLUSH_TIMEOUT`, so a
large ledger may need several runs or a longer budget; each run keeps the progress it confirmed. Because the command reads collectors, the
token also needs `fleet-management:read`; without it each ID is reported `read_failed` with the
401 or 403 status and stays in the ledger.

The command prints one secret-free receipt line with a status of `complete`, `remaining` or
`no_ledger`, and exits zero only when the status is `complete`:

```text
fleet teardown receipt: {"status":"complete","inventoried":2,"retired":2,"remaining":0,"inventory_verified":true,"collectors":[{"id":"...","outcome":"confirmed_absent","reason":"absent_after_delete","retired":true}]}
```

Each entry has an `outcome` of `confirmed_absent`, `rejected` (a definite 4xx refusal with the
collector still present, reported with `http_status`) or `unknown` (a failed read, a collector
still present after an ambiguous response, an interruption, or an ID not attempted before the
deadline). The closing ledger re-read reports `remaining`: any ID still held, including one a
concurrent writer added during the run (`added_during_teardown`). An incomplete teardown has status
`remaining`, and every unretired ID stays in the ledger for the next run. Every status other than
`complete`, including `remaining` and `no_ledger`, exits nonzero.
The receipt carries collector IDs, outcomes, closed-set reasons and HTTP status codes only: no
token, stack, URL, destination digest, collector attributes or response body.

A ledger that does not exist proves nothing, so it is never an empty owned set. When the file
ledger beside `CONFIG_SNAPSHOT_PATH` is missing, a ConfigMap slot was never written, or the
document is an empty unbound version-1 ledger, the command sends no Fleet request, prints a
receipt with status `no_ledger` and exits nonzero. Point `CONFIG_SNAPSHOT_PATH` or
`STATE_FLEET_CONFIGMAP` at the ledger the emitter used. Only an emptied version-2 ledger bound to
the configured destination (one whose IDs were all released) certifies `complete` with nothing to
remove.

The teardown client never follows a redirect to another scheme or host. The redirect response is
reported as an `unknown` outcome with its 3xx status, so another server cannot answer a
confirmed-absence read.

These conditions refuse before any Fleet request, print no receipt and change nothing:

- `DRY_RUN=true`, a missing `GC_FM_URL` or `GC_FM_TOKEN`, or combining the flag with another mode.
- A ledger bound to a different URL or stack, a nonempty unbound version-1 ledger, or an
  unreadable or corrupt ledger. Restore the original destination, or recover legacy ownership
  explicitly, before rerunning.
- `STATE_BACKEND=kubernetes` with `HA_MODE=off`. The ConfigMap ledger is shared, and only the
  Lease proves that no emitter holds authority over it. Run the command in lease mode instead.

In lease mode the command runs as the elected leader inside the same lifecycle as
`-fleet-unregister`, so the blueprint selection must still load and the mandatory credential
preflight still applies. While another emitter holds the Lease it waits as a standby and sends no
request; interruption before leadership or before completion exits nonzero with the ledger
intact, and an interrupted leader prints a `remaining` receipt. With HA off and file state,
nothing excludes a concurrent emitter, so stop it first.

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
| `GC_FM_TOKEN` | CAP token with `fleet-management:write` scope (owned teardown also needs `fleet-management:read`) |

Two modes are intentionally distinct:

- **Fleet metrics:** a selected `fleet_management` declaration plus the normal Prometheus lane.
  No FM API credentials are required.
- **Fleet registration:** the same declaration plus the complete `GC_FM_URL`, `GC_FM_STACK_ID`,
  and `GC_FM_TOKEN` triplet. Verify both registration and fresh heartbeats.

Authenticated `/control/status` reports these as separate `fleet_metrics` and
`fleet_registration` dispositions. An intentionally disabled registration lane is not a metrics
failure. Partial registration inputs fail closed; they do not silently downgrade to metrics-only.

See [Credentials](credentials.md) for token-scoping guidance.

### Disabling Fleet Management from the first tick

Leaving the FM credentials empty does not stop the Fleet `alloy_*` series: fleet metrics emit without them.
To keep a deployment emission-only, with no Fleet series and no FM API traffic, disable the kind at
startup:

```bash
STARTUP_DISABLED_KINDS=fleet_management
```

The kind is disabled before the first tick, so no Fleet `alloy_*` series is pushed (other constructs, such as the Alloy health addon or a
Kubernetes monitoring cluster, can still emit their own `alloy_*` series) and the controller
registers and heartbeats nothing, even when `GC_FM_URL`, `GC_FM_STACK_ID` and `GC_FM_TOKEN` are set.
The list is unioned with the `disabled_kinds` persisted in the control snapshot, and removing the
variable later does not re-enable the kind while the snapshot still lists it. A later
`POST /control/kinds` replaces the whole list, and the next restart adds the startup kinds back.

Disabling `fleet_management` (at startup or at runtime) suspends the controller rather than tearing
the fleet down: it sends no request of any kind while disabled, collectors that are already
registered stay registered, and enabling the kind again resumes heartbeats. Disabling the kind never
unregisters collectors (a roster that shrinks while the kind is enabled still does; teardown is the
explicit `synthkit -fleet-unregister` path, and `synthkit -fleet-unregister-owned` also removes
collectors a paused or deselected kind left in the ledger). The precedence rules are in
[control-plane.md](control-plane.md#disabling-kinds-at-startup).

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
