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

The provisioner never scans blueprints. The emitter resolves the exact selected built-in, custom,
and Git sources and writes a private snapshot under `/data/runtime`. The provisioner reads only
that snapshot. `SM_PROVISION_APPLY` is its independent write gate; emitter `DRY_RUN` is irrelevant.

See [Credentials](credentials.md) for token-scoping guidance.

---

## Phase 1 — provision the offline probe and checks

Start the emitter once with the intended SM blueprint selected. Until a matching registration
exists, synthkit writes the snapshot but suppresses SM emission. Preview the version-matched
Docker provisioner:

```bash
docker compose up -d synthkit
docker compose --profile sm-provision run --rm sm-provision
```

The preview reports action counts only and makes no remote mutation. Apply explicitly after review:

```bash
SM_PROVISION_APPLY=true docker compose --profile sm-provision run --rm sm-provision
docker compose restart synthkit
```

The restart is the activation boundary. On startup the same image validates the registration's
snapshot hash, target fingerprint, source version, resource IDs, and authoritative API `modified`
values. Missing, stale, corrupt, or incomplete state keeps SM suppressed and reports the lane as
partial.

The provisioner owns only IDs recorded in its private durable ownership ledger. That ledger is
independent of one snapshot/source version, so a changed check can update its recorded ID while the
activation registration remains bound to the exact current snapshot. A same-name probe or same
`(job,target)` check without matching ownership is a foreign collision and causes zero writes.

Credential or endpoint rotation changes the target fingerprint and suppresses SM until an explicit
migration succeeds. Recreate the emitter with the new credential so it writes the new snapshot, then
preview and apply the migration:

```bash
docker compose up -d --force-recreate synthkit
SM_PROVISION_MIGRATE_TARGET=true docker compose --profile sm-provision run --rm sm-provision
SM_PROVISION_MIGRATE_TARGET=true SM_PROVISION_APPLY=true docker compose --profile sm-provision run --rm sm-provision
docker compose restart synthkit
```

Preview uses the new credential to revalidate every recorded API ID, key, managed specification,
and remote revision against the ledger's last authoritative evidence, but changes only a mode-0600 private preview marker. Apply must run within 15
minutes with the identical snapshot, source version, remote evidence, and plan. Only then is the
ownership ledger atomically rebound. Migration is identity-only: every planned resource action must
be unchanged, so it makes no remote API writes. Reconcile any configuration or resource change as a
separate normal apply before or after rotation. Missing or changed resources, a stale or
absent preview, or an absent migration flag fails closed. Target migration and legacy adoption cannot
be combined; finish migration first and review any later adoption separately. If the process stops
after the ledger is rebound, the marker remains and blocks normal reconciliation; rerun apply with
the migration flag to resume the unchanged plan and consume the marker.

Legacy adoption is off by default. First preview with `SM_PROVISION_ADOPT_LEGACY=true`; the
provisioner records a private hash of that exact adoption plan. A later apply accepts only the same
snapshot and plan, and only one exact complete-spec match may be adopted. A pending journal stores
the expected specification as well as its hash. An ambiguous create is never reconciled by
name/spec: inspect it and establish ownership before explicitly clearing the journal and choosing
whether to adopt. Resources are never deleted.

`alertSensitivity` is always registered as `"none"` at the API level (the real value is stamped on the `sm_check_info` metric by the data-plane emitter independently of the provisioner).

---

## Phase 2 — run the emitter

After the successful apply and required restart, verify the authenticated disposition first:

```bash
curl -s -u control http://127.0.0.1:8088/control/status |
  jq '.optional_lanes[] | select(.lane=="synthetic_monitoring")'
```

Require `state="enabled"` and `verification="verified"`, then wait one declared emission interval
plus `SEND_BATCH_DEADLINE` before querying `probe_*` data. A successful provisioner exit without
the restart is not activation.

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
