---
title: Troubleshooting
description: Common synthkit problems with causes and fixes, from no data in Grafana to control-plane access and state persistence.
---

# Troubleshooting

This page covers the most common operational problems. For a structured end-to-end verification walkthrough, see [RUNBOOK.md](RUNBOOK.md). For generator health metrics, see [self-observability.md](self-observability.md).

## Executable symptom matrix

The ten source symptoms on this page are marked with `troubleshooting-symptom` comments and have
one row in the maintained [machine-readable matrix](../e2e/acceptance/troubleshooting/matrix.json).
The [clean-environment check](../e2e/acceptance/troubleshooting/check.py) executes every local,
non-destructive precondition, remedy, and post-remedy assertion without Docker, Compose, a live
stack query, or an external-product write. Run it through the repository's `troubleshooting-check`
recipe when that root-owned wiring is present.

The matrix deliberately separates local startup evidence from live delivery evidence. A local
assertion can prove configuration parsing, load-time validation, an offline inventory, or a safe
control-plane bind; it cannot prove that Grafana accepted a later batch. Rows marked
`live-delivery-required` name the destination and credential prerequisite, and the clean check
reports the external part as `BLOCKED_EXTERNAL` rather than silently treating it as passed.

---

## Start with optional-lane dispositions

```bash
curl -s -u control http://127.0.0.1:8088/control/status | jq '.optional_lanes'
```

All nine lanes report one of `enabled`, `partial`, `disabled`, or `unsupported`. `partial` includes
only closed non-secret missing field names; `disabled` is intentional/no request; `unsupported` is
a hard stop. Fleet metrics and API registration are separate rows. For Synthetic Monitoring,
`partial` after an apply normally means the required `docker compose restart synthkit` was not
performed or the snapshot/registration binding is stale.

---

## No data appearing in Grafana

<!-- troubleshooting-symptom: TS-01 -->
### `DRY_RUN` is still `true`

**Cause:** `DRY_RUN` defaults to `true`. A live push is always an explicit opt-in.

**Fix:** Set `DRY_RUN=false` in `.env`. Restart the container or process.

**Verify:** `curl -s -u control http://127.0.0.1:8088/control/status | jq '.dry_run'` must return `false`.

---

<!-- troubleshooting-symptom: TS-02 -->
### Credentials are wrong or missing

**Cause:** `GC_TOKEN`, or one of the endpoint/user pairs, is empty or incorrect.

**Fix:**

1. Confirm the minimum set is filled in `.env`: `GC_TOKEN`, `GC_PROM_RW`, `GC_PROM_USER`, `GC_OTLP_ENDPOINT`, `GC_OTLP_USER`, `GC_LOKI`, `GC_LOKI_USER`.
2. Check that `GC_PROM_USER` is the numeric Mimir instance ID, not an email address.
3. Confirm the CAP token has `metrics:write`, `logs:write`, and `traces:write` scopes.

**Verify:** Look at sink failures in the status strip:

```bash
curl -s -u control http://127.0.0.1:8088/control/status | jq '.sinks[] | {sink,failures,last_error_code}'
```

A non-zero `failures` count with `last_error_code="authentication"` confirms an authentication
problem without exposing remote response text.

---

<!-- troubleshooting-symptom: TS-03 -->
### Inline comment in `.env` corrupted a value

**Cause:** Docker Compose's `env_file` does NOT strip inline comments. `TOKEN=abc # my token` sets the variable to `abc # my token`.

**Fix:** Move comments to their own line above the value. Restart.

---

<!-- troubleshooting-symptom: TS-04 -->
### Metrics arrive but traces or logs are missing

**Cause:** The three sinks are independent. A credentials problem on one does not affect the others.

**Fix:** Check each sink separately in `GET /control/status`. Fill the missing endpoint/user pair and restart.

---

<!-- troubleshooting-symptom: TS-05 -->
## Series cap / kill switch

!!! warning "SERIES_CAP truncates pushes globally"
    When `SERIES_CAP` is set to a positive integer, synthkit truncates each metric push to that many
    series. It is a per-push series backstop, not the DPM limiter. `MAX_DPM_PER_SERIES` is the separate
    per-series cadence ceiling for an explicit `high_dpm.metric_interval`; changing `SERIES_CAP` does
    not make a series arrive more or less often.

**Symptom:** Some constructs have data, others do not — especially lower-priority or substrate constructs.

**Fix:** Increase or unset `SERIES_CAP` in `.env`. If the cap is intentional, reduce the blueprint's
declared constructs. For a high-DPM blueprint, also check its fixed-minute `series_budget`: it must
cover the projected series count multiplied by DPM per series. Startup and `/control/schema` expose
that projection; `high-dpm-churn` is 115 projected series × 6 DPM = 690 data points per minute.

---

<!-- troubleshooting-symptom: TS-06 -->
## Loki rejected high-cardinality stream labels

**Cause:** Loki rejects streams where a label carries high cardinality (e.g. request IDs, trace IDs). synthkit's Loki sink asserts this contract at startup; the error appears in the process log.

**Fix:** High-cardinality fields must be JSON payload fields, not stream labels. If you are authoring a custom `app` blueprint with a telemetry DSL, check your `labels:` declarations — a `ref` to a high-card key is only legal in log body or span attributes, never as a label. See the `internal/highcard` constraint in [architecture.md](architecture.md).

---

<!-- troubleshooting-symptom: TS-07 -->
## Control plane unreachable

**Cause:** `JSON_HTTP_ADDR` defaults to `127.0.0.1:8088` (loopback only) for direct binary runs. In Docker Compose the binary binds `0.0.0.0:8088` inside the container, but the host-side interface is `SYNTHKIT_BIND` (defaults to `127.0.0.1`).

**Fix (reach from another host):** Prefer an SSH tunnel. For deliberate non-loopback exposure, set
`SYNTHKIT_BIND`, a non-empty `CONTROL_TOKEN`, and `CONTROL_EXPOSURE_ACK=trusted-network` or
`tls-proxy`; startup fails without all required values.

```bash
ssh -L 8088:localhost:8088 <host>
# then access http://localhost:8088/control/ui locally
```

**Fix (reach from Grafana Cloud):** Configure a PDC Tailscale connection so Grafana Cloud can reach the Tailscale IP directly without public exposure.

---

<!-- troubleshooting-symptom: TS-08 -->
## Control-plane state not persisting across restarts

**Cause:** The `/data` bind mount is a single-file mount, or the directory is not owned by uid 65532.

The control plane saves state atomically (write to a temp file → rename). A single-file bind mount breaks the rename step. A directory not owned by uid 65532 (distroless nonroot) produces a `permission denied` error on every save attempt — visible in `persist.last_error`:

```bash
curl -s -u control http://127.0.0.1:8088/control/status | jq '.persist'
```

**Fix — wrong uid:**

```bash
if [ -L control-state-data ] || { [ -e control-state-data ] && [ ! -d control-state-data ]; }; then echo 'refusing unsafe state path' >&2; exit 1; fi
mkdir -p control-state-data
chmod 700 control-state-data
docker run --rm --volume "$PWD/control-state-data:/data" --entrypoint /bin/sh \
  node:24-alpine@sha256:d32cdf619f63fe0471182d08996dd516c6275bb5fd31ae06e55a570bd9e1ad43 \
  -ceu 'chown 65532:65532 /data; chmod 0700 /data'
docker compose restart
```

**Fix — single-file mount:** Remove the single-file bind mount from `docker-compose.yml` and replace it with a directory bind as shown in [deployment.md](deployment.md). A state file absent at startup is normal; it is created lazily on the first mutation.

---

<!-- troubleshooting-symptom: TS-09 -->
## Off-tailnet / offline push failures

**Cause:** The Forgejo autocommit hook (or similar) cannot reach the Forgejo server outside the Tailscale tailnet. This is expected and harmless for synthkit itself — the push-status hook exits `0` silently when offline.

**For synthkit sinks:** The in-memory queue buffers only items waiting for their first delivery
attempt. Each sink applies its bounded retry policy. When those retries are exhausted, the failed
sub-batch is discarded and counted as loss; synthkit has no WAL and does not retain that sub-batch
until connectivity returns. Later items continue normally when the affected sender shard completes
a successful flush.

Inspect the authoritative queue state separately from push-attempt history:

```bash
curl -s -u control http://127.0.0.1:8088/control/status |
  jq '.queues[] | {sink,depth,blocked_enqueues,dropped_items,last_loss_ms,last_recovery_ms,current_loss,affected_shards}'
```

`dropped_items` is cumulative for the process lifetime. `current_loss=false` with a non-zero dropped
count means delivery recovered after historical loss; it does not mean the lost items were replayed.
`depth=0` likewise means the queue drained, not that no loss occurred.

---

<!-- troubleshooting-symptom: TS-10 -->
## Using `-once -dump` as an offline diagnostic

Before debugging live connectivity, always confirm blueprints load and series look correct offline:

```bash
DRY_RUN=true BLUEPRINT_NAMES=otlp-native ./synthkit -once -dump 2>&1 | less
```

Expected output per blueprint:
- `loaded blueprint "<name>"` line
- `synthkit up: N blueprints` summary
- `[dry-run promrw|loki|otlp]` summaries with example series/streams/spans

Cross-check a few metric names against `signals/` — synthkit never invents names, so anything unexpected is a bug or a misconfigured blueprint.

This command requires no network connectivity and exits cleanly after one tick.

If startup logs `WARNING: no blueprints selected`, the process is intentionally in setup mode and
emits nothing. Set `BLUEPRINT_NAMES` to exact names and restart; use `*` only for the full catalog.

---

## Debugging further

| Signal | Where to look |
|---|---|
| Sink push outcomes | `GET /control/status` → `sinks[].last_error_code` |
| Optional-product activation | `GET /control/status` → `optional_lanes[]` |
| Per-construct tick errors | `GET /control/health` |
| Load-time blueprint problems | `GET /control/diagnostics` |
| Generator throughput, queue depth/loss, dropped ticks | [self-observability.md](self-observability.md) |
| Series inventory vs. signal contracts | `DRY_RUN=true BLUEPRINT_NAMES=otlp-native ./synthkit -once -dump` |

## Matrix rows

Each row contains the five executable parts required for the symptom: precondition, action,
expected diagnostic, remedy, and post-remedy assertion. `Proof boundary` states whether the final
assertion is local startup evidence or needs live delivery. The acceptance check executes the local
parts in a clean temporary environment and never supplies real credentials.

| ID | Scope | Precondition | Action | Expected diagnostic | Remedy | Post-remedy assertion | Proof boundary |
| --- | --- | --- | --- | --- | --- | --- | --- |
| TS-01 | local startup | A selected blueprint runs with `DRY_RUN` unset or `true`. | Run one selected blueprint offline and inspect the effective mode or status. | The run reports dry-run mode and no live sink delivery is attempted. | Set `DRY_RUN=false` and restart. | Local status reports `dry_run=false`; remote acceptance still needs the live follow-up. | local-startup-only |
| TS-02 | local startup and live delivery | `DRY_RUN=false` has an empty, malformed, or unauthorized mandatory sink setting. | Start once, then inspect redacted sink outcomes. | Startup names missing mandatory variables; an affected sink reports `last_error_code=authentication` without token or response text. | Fill the mandatory token, endpoint/user pairs, positive-decimal users, and required write scopes. | Local shape validation passes; live proof requires every configured sink's `last_success_ms` to advance without a current authentication error. | live-delivery-required |
| TS-03 | local startup | An unquoted env value contains an inline `#`, or a quoted value intentionally contains `#`. | Load both forms from a temporary env file without printing values. | The unquoted comment becomes part of the value; a quoted hash remains data. | Put comments on their own lines, or quote a hash that is data, then restart. | The unquoted value ends before the space preceding `#`, and the quoted value retains its hash; this proves parsing only. | local-startup-only |
| TS-04 | live delivery | One independent metric, trace, or log endpoint/user pair is absent or rejected while the process runs. | Inspect each sink independently in `GET /control/status`. | Only the affected sink reports authentication or transport failure; other sinks retain their own success timestamps. | Correct the affected pair or scope and restart. | Local status accounting proves independence; live proof requires metrics, traces, and logs to be queryable at their respective destinations. | live-delivery-required |
| TS-05 | local configuration and live delivery | Positive `SERIES_CAP` is below the selected blueprint's projected series count. | Exercise the bounded-series path and distinguish the per-push cap from `MAX_DPM_PER_SERIES`. | The promrw sink reports a cap hit and truncates the push, so lower-priority constructs can be absent. | Increase or unset `SERIES_CAP`, or reduce declared constructs; size high-DPM `series_budget` for projected series multiplied by DPM. | Local cap tests show the bound is raised or removed; only live read-back proves every intended family arrives. | live-delivery-required |
| TS-06 | local startup | A custom app declaration puts `trace_id`, `request_id`, or another high-cardinality reference in a metric or Loki stream label. | Load it through capability and sink validation. | Validation rejects the high-cardinality label and names the offending field class. | Move it to the log body, structured metadata, or span attributes; keep stream labels bounded. | Local validation accepts the corrected placement and the stream has no forbidden label; no remote stack is needed. | local-startup-only |
| TS-07 | local startup and operator access | A loopback bind is used for remote access, or a non-loopback bind lacks both safeguards. | Validate bind, token, and acknowledgement combinations before using the UI or API. | Unsafe non-loopback startup fails closed and names `CONTROL_TOKEN`, `CONTROL_EXPOSURE_ACK`, or `SYNTHKIT_BIND`. | Use an SSH tunnel or private PDC/Tailscale path; deliberate exposure needs a token, `trusted-network` or `tls-proxy`, and trusted HTTPS. | Local validation accepts the safe bind; remote reachability remains unproven without the named network path. | local-startup-only |
| TS-08 | local filesystem and container restart | The snapshot is a single-file mount or a directory the runtime uid cannot write. | Perform an atomic temp-file-to-snapshot rename and inspect persisted-state health after a mutation. | The mount cannot be atomically replaced, or `persist.last_error` reports permission denied. | Use a real `/data` directory, reject symlinks and non-directories, and make the host directory owned by uid 65532 before restart. | A local atomic-write probe succeeds; only a Docker restart with the production uid proves mounted persistence. | local-startup-only |
| TS-09 | live delivery | A sink or the host hook cannot reach its remote endpoint, or retries exhaust while the process stays up. | Inspect queue depth, blocked enqueues, dropped items, loss timestamps, recovery timestamp, and current loss separately from attempts. | Queue state exposes pressure or loss; a recovered `current_loss=false` can coexist with cumulative dropped items because discarded data is not replayed. | Restore the affected network or endpoint and continue observing the bounded queue. | Local retry and queue semantics pass; live proof requires a later accepted batch and authenticated recovery status. | live-delivery-required |
| TS-10 | local startup | Blueprint selection or series shape is uncertain and diagnosis must not require network access. | Run `DRY_RUN=true` with one exact blueprint and `-once -dump`; observe setup mode when selection is blank. | Output includes selected/loaded blueprint, `synthkit up`, and dry-run summaries; blank selection prints `WARNING: no blueprints selected`. | Set exact runtime names, or use `*` only deliberately, then rerun. | The clean command produces a deterministic local inventory and no network push; remote landing is not proven. | local-startup-only |

## External prerequisite dispositions

The clean check cannot prove an external product from a missing credential or an absent tenant. It
therefore emits one explicit disposition for each optional lane rather than silently dropping the
scenario. Supply the named prerequisite and run the live follow-up from [RUNBOOK.md](RUNBOOK.md)
when the product is available.

| Lane | Required external prerequisite | Clean-check disposition |
| --- | --- | --- |
| Synthetic profiles | `GC_PROFILES_URL`, `GC_PROFILES_USER`, shared `GC_TOKEN`, and a profile-emitting declaration. | `BLOCKED_EXTERNAL`; no profile endpoint is contacted. |
| Faro/RUM | `GC_FARO_COLLECTOR`, `GC_FARO_APP_KEY`, a RUM-enabled workload, and a tenant accepting Faro beacons. | `BLOCKED_EXTERNAL`; no Faro collector is contacted. |
| Synthetic Monitoring | `GC_SM_URL`, `GC_SM_TOKEN`, a selected declaration, matching `sm-provision` image, and remote-write authority for registration. | `BLOCKED_EXTERNAL`; provisioning is an external write and is not run. |
| Fleet Management | `GC_FM_URL`, `GC_FM_STACK_ID`, `GC_FM_TOKEN`, a fleet declaration, and Fleet registration authority. | `BLOCKED_EXTERNAL`; no Fleet API is contacted. |
| sigil AI observability | `GC_SIGIL_ENDPOINT`, `GC_SIGIL_TENANT_ID`, `GC_SIGIL_TOKEN`, and the sigil ingest scope. | `BLOCKED_EXTERNAL`; no sigil endpoint is contacted. |
| Self-observability OTLP | `SELFOBS_ENABLED=true`, the complete `GC_SELF_OTLP_*` triplet, and a separate staff destination with metrics/logs/traces write scopes. | `BLOCKED_EXTERNAL`; no self-observability destination is contacted. |
| Process profiling | `SELFOBS_ENABLED=true`, the complete `GC_PYROSCOPE_*` triplet, and `profiles:write` on the separate staff destination. | `BLOCKED_EXTERNAL`; no Pyroscope destination is contacted. |
| All optional lanes | Every lane prerequisite, valid declarations, and a bounded observation window at every destination. | `BLOCKED_EXTERNAL`; capability is not inferred from absent credentials. |
