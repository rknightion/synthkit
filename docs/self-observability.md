---
title: Self-Observability
description: How to configure synthkit's own process telemetry — RED metrics, traces, logs, and continuous profiles — shipped to a separate Grafana Cloud stack.
---

# Self-Observability

synthkit ships its own operational telemetry alongside (and completely separate from) the synthetic data it produces. This is the generator's self-observability: RED metrics on the synthetic-push pipeline, Go runtime metrics, per-tick traces, the operational log stream, and continuous process profiles via Pyroscope — all sent to a **different** Grafana Cloud stack through their own credential triplets, never `GC_TOKEN`.

Self-observability is off by default and is independent of synthetic `DRY_RUN`. The profiling path
has no master flag of its own; it follows `SELFOBS_ENABLED` but has its own credential triplet.

---

## What it ships

### OTLP signals (metrics + traces + logs)

All three signal types flow over a single OTLP/HTTP endpoint to the self-obs stack:

| Signal | What it covers |
|---|---|
| **Metrics** | `synthkit.push` (count/items/bytes/duration per sink/blueprint/outcome), `synthkit.tick` (invocations + duration), `synthkit.cycle.duration`, `synthkit.dropped_ticks`, delivery-queue depth/flush/loss metrics (`synthkit.queue.*`), Fleet Management operations (`synthkit.fleet.*`), and observable gauges: `synthkit.ledger.size`, `synthkit.volume.multiplier`, `synthkit.blueprint.count`, `synthkit.cardinality.series` |
| **Traces** | Per-tick spans wrapping each construct invocation; `cycle` spans (backdated to the generation window); `push <sink>` child spans for each live push; `flush <sink>` spans for the decoupled delivery queue; `fleet <op>` spans for FM registration and heartbeat round-trips |
| **Logs** | Structured OTLP LogRecords for push failures (event=`push_error`), tick errors (event=`tick_error`), FM failures (event=`fleet_error`), config-change events (event=`config_change`), and the operational heartbeat (every 5 minutes) |

Go runtime metrics (goroutines, memory, GC) are collected via the OTel contrib runtime instrumentation against the self-obs `MeterProvider`.

Delivery-queue metrics use only the bounded `sink` label:

| Metric | Meaning |
|---|---|
| `synthkit.queue.depth` | Current enqueued-but-not-yet-flushed items |
| `synthkit.queue.enqueue_blocked` | Times a producer blocked on a full shard buffer |
| `synthkit.queue.flush` / `.duration` / `.batch` | Completed flushes, latency, and attempted item count |
| `synthkit.queue.dropped.items` | Cumulative items discarded after sink retry exhaustion |
| `synthkit.queue.current_loss` | `1` while any affected sender shard has not completed a later successful flush; otherwise `0` |
| `synthkit.queue.last_loss` | Unix seconds of the most recent discarded sub-batch |

Loss metrics do not imply replay. A transition of `current_loss` back to `0` records recovery for
new delivery; `dropped.items` remains cumulative until the process restarts.

In lease mode, both standby and leader forward standard operational logs and the five-minute
heartbeat to their current role-scoped OTLP provider. A role transition detaches the old log
destination before provider shutdown, so new log records are attributed to the current role.
Standard stderr output remains available; disabled self-observability leaves logging unchanged.

#### Leadership-session attribution

`service.instance.id` is `<hostname>-<pid>`. A container restart can reuse both, and `ha.role`
only says what a provider was when it started, so neither proves that an export belongs to the
process that holds the Lease now. Every OTLP self-observability resource therefore carries two
synthkit-namespaced attributes. OpenTelemetry semantic conventions define no leader-election or
Lease-holder attribute, so these names are synthkit's own.

| Resource attribute | Values | Meaning |
|---|---|---|
| `synthkit.ha.session.attribution` | `lease` or `unknown` | Whether this process has a Lease election identity. |
| `synthkit.ha.session.id` | 64 lowercase hex characters with `lease`; `unknown` with `unknown` | SHA-256 of the process's election identity. |

In lease mode each process mints one election identity at startup: the downward-API Pod UID, `/`,
and a fresh 128-bit random nonce. The Lease's `spec.holderIdentity` is exactly that string while
the process leads. A process acquires at most once, because losing the Lease exits it. A restart
or any reacquisition is therefore a new process with a new nonce and a new session ID. Standby and
leader providers in one process carry the same session ID and differ in `ha.role`.

To join an export to the current Lease, hash the holder yourself:

```sh
holder=$(kubectl -n <namespace> get lease <HA_LEASE_NAME> -o jsonpath='{.spec.holderIdentity}')
printf %s "$holder" | sha256sum | cut -d' ' -f1      # Linux
printf %s "$holder" | shasum -a 256 | cut -d' ' -f1  # macOS
```

A crashed leader's `holderIdentity` stays on the Lease until another replica acquires it, so a
matching digest alone is not enough. The session is current only while the Lease is also
unexpired: `spec.renewTime + spec.leaseDurationSeconds` is later than now.

| Export | Classification |
|---|---|
| `ha.role=leader`, session ID equals the holder digest, and the Lease is unexpired | Current leadership session |
| `ha.role=leader` and session ID differs, or the Lease has expired | Stale: an earlier or crashed session's buffered or late export |
| `ha.role=standby` | Standby, whatever its session ID |
| `synthkit.ha.session.attribution=unknown` | Unattributed. Never infer a session from hostname, PID or role |

Attribution is `unknown` with HA off and whenever no election identity exists; the session ID is
then the literal `unknown`, never a digest. Neither `SELFOBS_TAGS` nor `OTEL_RESOURCE_ATTRIBUTES`
can set or override either attribute. Continuous profiles carry `ha.role` but no session
attribute; treat profile session attribution as unknown.

The digest is exported instead of the raw identity because the configuration view classifies
`POD_UID` as secret, and the nonce has no use outside the Lease. The cost is one hashing step for the
observer. The session ID is a resource attribute and changes on every process start, so a metrics
backend gets one new resource series per start, even when a restart keeps `service.instance.id`. The ID
carries no user, credential or hardware identifier.

### Continuous profiles (Pyroscope)

When `GC_PYROSCOPE_*` credentials are set, the generator's own process is profiled continuously and sent to a Grafana Cloud Profiles (Pyroscope) instance via its own `GC_PYROSCOPE_*` credential triplet — independent of the OTLP self-obs triplet, though both are typically the same staff stack. Profile types: CPU, heap allocation/inuse (objects + bytes), goroutines, and optionally mutex and block profiles. Application name is `synthkit`.

---

## Isolation design

`internal/selfobs` is the **sole** package in synthkit that imports the OpenTelemetry SDK. The SDK is banned on the synthetic-data path — the synthetic OTLP sink hand-encodes proto and never touches the OTel API.

The seam is stdlib-only in both directions:

- Sinks report push outcomes through `internal/pushhook` (a plain function type). `selfobs.PushObserver()` returns an observer that `internal/selfobs` registers; the sinks themselves never import `selfobs`.
- The runner reports per-tick outcomes through `runner.TickFunc` (also a plain function type). The runner stores a method value from `selfobs`; it never links the SDK.

`selfobs` builds its own `TracerProvider`, `MeterProvider`, and `LoggerProvider`, and **never installs them as OTel globals** (`otel.SetTracerProvider` is never called). The synthetic OTLP sink, which bypasses the OTel global API entirely, is therefore completely unaffected.

See [Architecture](architecture.md) §6.1 for the full isolation rationale.

---

## Configuration

Self-observability activates only with `SELFOBS_ENABLED=true` and the complete
`GC_SELF_OTLP_ENDPOINT`, `GC_SELF_OTLP_USER`, and `GC_SELF_OTLP_PASSWORD` triplet. Its in-process
status can prove configuration/startup, not remote landing: verify fresh `synthkit_*` telemetry in
the staff context after `SELFOBS_METRIC_INTERVAL` plus the delivery deadline.

Process profiling is a separate lane. It additionally requires the complete
`GC_PYROSCOPE_URL`, `GC_PYROSCOPE_USER`, and `GC_PYROSCOPE_PASSWORD` triplet. Verify the synthkit
process profile in the staff profiles tenant. These are not the customer synthetic profiles emitted
by a workload declaration through `GC_PROFILES_*` and `GC_TOKEN`.

Enable self-observability by setting `SELFOBS_ENABLED=true` and providing the OTLP credential triplet. All three vars are required; if any is missing, synthkit logs a warning and falls back to a no-op handle.

### OTLP self-observability

| Env var | Required | Default | Description |
|---|---|---|---|
| `SELFOBS_ENABLED` | — | `false` | Master on/off switch |
| `GC_SELF_OTLP_ENDPOINT` | When enabled | — | OTLP gateway base URL for the self-obs stack (e.g. `https://otlp-gateway-<region>.grafana.net/otlp`) |
| `GC_SELF_OTLP_USER` | When enabled | — | Self-obs stack ID (HTTP Basic username) |
| `GC_SELF_OTLP_PASSWORD` | When enabled | — | CAP token with `metrics:write`, `logs:write`, `traces:write` — **never** `GC_TOKEN` |
| `SELFOBS_TAGS` | — | — | Extra resource attributes as `key=value,key=value` CSV |
| `GC_SELF_GRAFANA_URL` | — | — | Staff Grafana base URL (e.g. `https://your-stack.grafana.net`) — enables deep-links to the self-obs dashboard in the operator UI; leave empty to hide them |
| `SELFOBS_METRIC_INTERVAL` | — | `15s` | Metric flush cadence (Go duration string); traces and logs use their own batchers and are unaffected |

### Continuous profiling (Pyroscope)

Profiling has no master flag of its own. It is independent of synthetic `DRY_RUN`, but activation
requires `SELFOBS_ENABLED=true` and the complete three-variable `GC_PYROSCOPE_*` triplet. An
incomplete gate is a no-op reported by the optional-lane disposition.

| Env var | Required | Default | Description |
|---|---|---|---|
| `GC_PYROSCOPE_URL` | When profiling | — | Profiles ingest URL (e.g. `https://profiles-prod-XXX.grafana.net`) |
| `GC_PYROSCOPE_USER` | When profiling | — | Profiles instance ID (Basic-auth username) |
| `GC_PYROSCOPE_PASSWORD` | When profiling | — | CAP token with `profiles:write` scope |
| `PYROSCOPE_TAGS` | — | — | Extra profile tags as `key=value,key=value` CSV |
| `PYROSCOPE_MUTEX_FRACTION` | — | `5` | `runtime.SetMutexProfileFraction`; `0` = mutex profiling off |
| `PYROSCOPE_BLOCK_RATE` | — | `5` | `runtime.SetBlockProfileRate` in nanoseconds; `0` = block profiling off |

A value of `5` for both is high-fidelity and fine for a lab or demo generator. Set both to `0` to reduce overhead on resource-constrained hosts.

---

## Example `.env` snippet

```bash
# Self-observability — ships to a SEPARATE stack (never GC_TOKEN)
SELFOBS_ENABLED=true
GC_SELF_OTLP_ENDPOINT=https://otlp-gateway-prod-eu-west-0.grafana.net/otlp
GC_SELF_OTLP_USER=123456
GC_SELF_OTLP_PASSWORD=glc_...
SELFOBS_TAGS=env=prod,team=platform
GC_SELF_GRAFANA_URL=https://myorg.grafana.net
SELFOBS_METRIC_INTERVAL=15s

# Continuous profiling (also ships to a separate stack)
GC_PYROSCOPE_URL=https://profiles-prod-006.grafana.net
GC_PYROSCOPE_USER=78910
GC_PYROSCOPE_PASSWORD=glc_...
PYROSCOPE_TAGS=env=prod
PYROSCOPE_MUTEX_FRACTION=5
PYROSCOPE_BLOCK_RATE=5
```

---

## Relationship to DRY_RUN

`SELFOBS_ENABLED` and the complete staff credential triplets control these process signals.
Synthetic `DRY_RUN` is stamped as run-mode context but does not suppress them. This lets operators
observe a dry-run synthkit process without sending customer synthetic data.

---

## See also

- [Configuration](configuration.md) — full environment variable reference
- [Architecture](architecture.md) — §6.1 for the OTel SDK isolation design
- [Credentials](credentials.md) — the two-stacks model and token scoping
