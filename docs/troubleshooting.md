---
title: Troubleshooting
description: Common synthkit problems with causes and fixes, from no data in Grafana to control-plane access and state persistence.
---

# Troubleshooting

This page covers the most common operational problems. For a structured end-to-end verification walkthrough, see [RUNBOOK.md](RUNBOOK.md). For generator health metrics, see [self-observability.md](self-observability.md).

---

## No data appearing in Grafana

### `DRY_RUN` is still `true`

**Cause:** `DRY_RUN` defaults to `true`. A live push is always an explicit opt-in.

**Fix:** Set `DRY_RUN=false` in `.env`. Restart the container or process.

**Verify:** `curl -s http://127.0.0.1:8088/control/status | jq '.dry_run'` must return `false`.

---

### Credentials are wrong or missing

**Cause:** `GC_TOKEN`, or one of the endpoint/user pairs, is empty or incorrect.

**Fix:**

1. Confirm the minimum set is filled in `.env`: `GC_TOKEN`, `GC_PROM_RW`, `GC_PROM_USER`, `GC_OTLP_ENDPOINT`, `GC_OTLP_USER`, `GC_LOKI`, `GC_LOKI_USER`.
2. Check that `GC_PROM_USER` is the numeric Mimir instance ID, not an email address.
3. Confirm the CAP token has `metrics:write`, `logs:write`, and `traces:write` scopes.

**Verify:** Look at sink failures in the status strip:

```bash
curl -s http://127.0.0.1:8088/control/status | jq '.sinks[] | {name, failures, last_error}'
```

A non-zero `failures` count and a `last_error` message (e.g. `401 Unauthorized`) confirms a credential problem.

---

### Inline comment in `.env` corrupted a value

**Cause:** Docker Compose's `env_file` does NOT strip inline comments. `TOKEN=abc # my token` sets the variable to `abc # my token`.

**Fix:** Move comments to their own line above the value. Restart.

---

### Metrics arrive but traces or logs are missing

**Cause:** The three sinks are independent. A credentials problem on one does not affect the others.

**Fix:** Check each sink separately in `GET /control/status`. Fill the missing endpoint/user pair and restart.

---

## Series cap / kill switch

!!! warning "SERIES_CAP truncates pushes globally"
    When `SERIES_CAP` is set to a positive integer, synthkit will not push more than that many series per tick across all sinks. Cardinality above the cap is silently dropped.

**Symptom:** Some constructs have data, others do not — especially lower-priority or substrate constructs.

**Fix:** Increase or unset `SERIES_CAP` in `.env`. If the cap is intentional, reduce the blueprint's declared constructs or tick cadence to stay under the limit.

---

## Loki rejected high-cardinality stream labels

**Cause:** Loki rejects streams where a label carries high cardinality (e.g. request IDs, trace IDs). synthkit's Loki sink asserts this contract at startup; the error appears in the process log.

**Fix:** High-cardinality fields must be JSON payload fields, not stream labels. If you are authoring a custom `app` blueprint with a telemetry DSL, check your `labels:` declarations — a `ref` to a high-card key is only legal in log body or span attributes, never as a label. See the `internal/highcard` constraint in [architecture.md](architecture.md).

---

## Control plane unreachable

**Cause:** `JSON_HTTP_ADDR` defaults to `127.0.0.1:8088` (loopback only) for direct binary runs. In Docker Compose the binary binds `0.0.0.0:8088` inside the container, but the host-side interface is `SYNTHKIT_BIND` (defaults to `127.0.0.1`).

**Fix (reach from another host):** Set `SYNTHKIT_BIND=0.0.0.0` (or a specific Tailscale/LAN IP) in `.env`, set `CONTROL_TOKEN` to a non-empty value, and restart. Alternatively, use an SSH tunnel:

```bash
ssh -L 8088:localhost:8088 <host>
# then access http://localhost:8088/control/ui locally
```

**Fix (reach from Grafana Cloud):** Configure a PDC Tailscale connection so Grafana Cloud can reach the Tailscale IP directly without public exposure.

---

## Control-plane state not persisting across restarts

**Cause:** The `/data` bind mount is a single-file mount, or the directory is not owned by uid 65532.

The control plane saves state atomically (write to a temp file → rename). A single-file bind mount breaks the rename step. A directory not owned by uid 65532 (distroless nonroot) produces a `permission denied` error on every save attempt — visible in `persist.last_error`:

```bash
curl -s http://127.0.0.1:8088/control/status | jq '.persist'
```

**Fix — wrong uid:**

```bash
sudo chown -R 65532:65532 control-state-data
docker compose restart
```

**Fix — single-file mount:** Remove the single-file bind mount from `docker-compose.yml` and replace it with a directory bind as shown in [deployment.md](deployment.md). A state file absent at startup is normal; it is created lazily on the first mutation.

---

## Off-tailnet / offline push failures

**Cause:** The Forgejo autocommit hook (or similar) cannot reach the Forgejo server outside the Tailscale tailnet. This is expected and harmless for synthkit itself — the push-status hook exits `0` silently when offline.

**For synthkit sinks:** If you are running synthkit on a machine that has lost connectivity to Grafana Cloud, the sink will log failures. synthkit keeps running and will resume pushing when connectivity is restored (the decoupled delivery queue buffers series internally up to `SEND_QUEUE_CAPACITY`).

---

## Using `-once -dump` as an offline diagnostic

Before debugging live connectivity, always confirm blueprints load and series look correct offline:

```bash
DRY_RUN=true ./synthkit -once -dump 2>&1 | less
```

Expected output per blueprint:
- `loaded blueprint "<name>"` line
- `synthkit up: N blueprints` summary
- `[dry-run promrw|loki|otlp]` summaries with example series/streams/spans

Cross-check a few metric names against `signals/` — synthkit never invents names, so anything unexpected is a bug or a misconfigured blueprint.

This command requires no network connectivity and exits cleanly after one tick.

---

## Debugging further

| Signal | Where to look |
|---|---|
| Sink push outcomes | `GET /control/status` → `sinks[].last_error` |
| Per-construct tick errors | `GET /control/health` |
| Load-time blueprint problems | `GET /control/diagnostics` |
| Generator throughput, queue depth, dropped ticks | [self-observability.md](self-observability.md) |
| Series inventory vs. signal contracts | `DRY_RUN=true ./synthkit -once -dump` |
