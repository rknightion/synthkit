---
title: Synthetic Monitoring
description: Emitting Grafana Cloud Synthetic Monitoring data without a real probe using a two-phase provisioner and data-plane emitter.
---

# Synthetic Monitoring

synthkit emits Grafana Cloud Synthetic Monitoring data — `probe_success`, `probe_duration_seconds`, the full SM histogram family, `sm_check_info`, and the SM Loki log stream — with no real probe ever executing. The SM app becomes populated with checks, probe status, and check history purely from injected telemetry.

This requires a two-phase startup: a one-shot provisioner registers the offline probe and checks with the SM API before the data-plane emitter produces useful output.

---

## Why two phases

The Grafana Cloud SM app hard-guards on its own check registry: it clamps timelines to `check.created` and will not display injected metrics for checks it does not know about. Metric injection alone is not enough. The offline probe must be registered first, the checks must be created (or updated) in Grafana Cloud, and then the data-plane emitter can push forward-in-time data that populates the SM views.

The probe name, region, and coordinates are shared constants in `internal/construct/sm`, so the provisioner and the emitter share a single source of truth and cannot drift.

---

## Credentials

SM provisioning uses a dedicated SM API token — separate from `GC_TOKEN` and from all other sinks.

| Env var | Description |
|---|---|
| `GC_SM_URL` | SM API base URL, e.g. `https://synthetic-monitoring-api-<region>.grafana.net` |
| `GC_SM_TOKEN` | SM API bearer token (not `GC_TOKEN`) |

The `BLUEPRINTS` environment variable controls which blueprint directory the provisioner scans (default: `./blueprints`). The `DRY_RUN` flag (default `true`) applies to the provisioner separately from the main emitter — see below.

See [Credentials](credentials.md) for token-scoping guidance.

---

## Phase 1 — provision the offline probe and checks

Run the one-shot provisioner once per environment. It is idempotent: it lists existing probes and checks first, creates only what is absent, and updates existing checks in place.

```bash
GC_SM_URL=https://synthetic-monitoring-api-<region>.grafana.net \
  GC_SM_TOKEN=<sm-bearer-token> \
  DRY_RUN=false \
  go run ./cmd/sm-provision
```

!!! tip "Preview first with DRY_RUN=true (the default)"
    Without `DRY_RUN=false`, the provisioner prints the planned operations and exits without making any API calls:

    ```
    [DRY RUN] Would register offline probe "synthkit-private" (region=EMEA lat=50.1109 lon=8.6821)
    [DRY RUN] Would upsert 3 check(s):
      job="synmon-api-health" target="https://api.example.com/health" frequency=60000ms alertSensitivity=none
      …
    ```

    This is safe to run at any time to inspect what would be provisioned.

The provisioner reads every `*.yaml` file in the `BLUEPRINTS` directory, collects all `synthetic_monitoring` construct instances, and:

1. Registers one offline private probe (idempotent — adds only if absent by name).
2. For each declared check: creates if absent by `(job, target)` key; updates if present.

`alertSensitivity` is always registered as `"none"` at the API level (the real value is stamped on the `sm_check_info` metric by the data-plane emitter independently of the provisioner).

---

## Phase 2 — run the emitter

Once the probe and checks are registered, run the generator normally. The data-plane emitter pushes all SM telemetry each tick.

```bash
./synthkit
```

Or with Docker Compose:

```bash
docker compose up
```

The SM emitter pushes per-tick:

- `probe_success` (gauge, 0/1)
- `probe_duration_seconds` (gauge)
- `probe_all_success_sum` / `_count` (summary counters)
- `probe_all_duration_seconds_bucket` / `_sum` / `_count` (histogram, buckets `[0.1, 0.25, 0.5, 1, 2.5, 5, 10]`)
- `sm_check_info` (info gauge, carries `check_name`, `region`, `frequency`, `geohash`, `alert_sensitivity`, user labels)

Each check also emits a Loki log line per tick with stream labels `{source="synthetic-monitoring-agent", check_name, instance, job, probe, region, probe_success}` and a logfmt body (`msg="Check succeeded"` or `"Check failed"`, `duration_seconds`).

Base labels on every series: `job` (check job), `instance` (check target URL), `probe` (probe name), `config_version`. User labels declared in the blueprint become `label_<k>` labels on both metrics and the Loki stream.

!!! warning "config_version is a stable join key"
    All SM series join on `(instance, job, probe, config_version)`. `config_version` encodes the check's `modified` timestamp. If you re-provision after editing a check, all series must be re-stamped with the new `config_version`. The emitter handles this automatically; no manual action is needed.

---

## Blueprint configuration

Enable SM by adding a `synthetic_monitoring` block under `features:`:

```yaml
features:
  synthetic_monitoring:
    enabled: true
    checks:
      - name: my-api-health
        target: "https://api.example.com/health"
        frequency: 60000         # ms; default 60000
        probe: synthkit-private  # must match the registered probe name
        region: EMEA
        labels:
          team: platform
          tier: api
```

Each check entry becomes one registered SM check and one emitted series family. `target` defaults to `https://<name>.example.com/health` if omitted.

See [`blueprints/synthetic-monitoring.yaml`](https://github.com/rknightion/synthkit/blob/main/blueprints/synthetic-monitoring.yaml) for a complete example combining SM checks with a Fleet Management collector roster.

---

## Failure mode

The `sm_probe_failure` incident mode targets a specific check and environment: it sets `probe_success=0`, `probe_duration_seconds=3.0` (the SM timeout), and emits a Loki log line with `probe_success="0"` and `msg="Check failed"`. See [Incidents](incidents.md) for how to declare and activate failure modes.

---

## See also

- [`signals/sm.md`](https://github.com/rknightion/synthkit/blob/main/signals/sm.md) — full signal contract with label shapes, histogram buckets, and provenance
- [CLI](cli.md) — `sm-provision` command reference
- [Configuration](configuration.md) — complete environment variable reference
- [Credentials](credentials.md) — SM token scoping
