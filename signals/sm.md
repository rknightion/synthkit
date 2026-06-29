# Synthetic Monitoring (fake SM checks) — ScopeSubstrate

Substrate-scoped: disambiguated by check identity (`job`/`instance`/`probe`/`config_version`/`check_name`);
never carries a `blueprint` label (SK-22 resolved). Global rules: see [`00-canon.md`](00-canon.md) — scoping
`[slug: scoping]`, cardinality `[slug: cardinality]`, shape rules `[slug: shape-rules]`.

*Provenance: predecessor extract 06 + `internal/canon/sm.go`. **Live-validated** against the Grafana Synthetic
Monitoring product stack (Rob) — `v: ok`, not assumed.*

---

## Synthetic Monitoring (fake SM checks) [slug: sm-checks]
*Provenance: predecessor extract 06 + `internal/canon/sm.go`, `internal/emit/sm.go`, `cmd/sm-provision/main.go`.*

Two cleanly-separated halves over `canon/sm.go`: a **one-shot control-plane provisioner**
(`cmd/sm-provision`) that registers an offline private probe + checks via the SM API (mandatory — the
SM app hard-guards on an empty check list, so metric injection alone does not light it up), and a
**per-tick data-plane** (`emit/sm.go`) that supplies ALL telemetry (no real probe ever runs).

**Offline probe (canon):** name (config-driven), region, lat/lon, precomputed geohash (precision 12).
Registered private with no agent; never executes. The app shows it as `"<name> (offline)"`;
`probesUp` still reads 1/1 (driven by recent metric presence, not connection state).

**Check templates** (config-driven; `<workload>-health-{env}` pattern, one per enabled env →
`len(checks) × len(envs)`). Fields: `Type="http"`, `FrequencyMs`, `BaseLatency`, `AlertSensitivity`,
`{env}` = `strings.ToLower(env.Name)`, `Timeout = SMTimeoutSeconds(3.0)×1000` ms.

**Base label set (every series):** `job` (check Job), `instance` (check Target URL), `probe`
(probe name), `config_version` (⚠ stable join-key constant = `modified×1e9`; the SM app joins all
series + `sm_check_info` on `(instance,job,probe,config_version)` — any check edit changes `modified`→
`config_version`→ all series must be re-stamped, T1). User labels ride as `label_<k>` (e.g.
`label_test`).

**Series per check (60s):** `probe_success` (G 0/1; 0 under failure), `probe_duration_seconds` (G;
= `SMTimeoutSeconds` under failure), `probe_all_success_sum`/`_count` (Counters — a SUMMARY, no
buckets, separate `Add` calls — T3), `probe_all_duration_seconds_bucket{le}`/`_sum`/`_count`
(Histogram via `Observe` — ⚠ do NOT also `Add` `_sum`/`_count`, T2; buckets
`[0.1,0.25,0.5,1,2.5,5,10]`), `sm_check_info` (G=1; metadata anchor). `sm_check_info` extra labels:
`check_name`, `region`, `frequency` (decimal string), `geohash`, `alert_sensitivity` (the check's REAL
value — ⚠ independent of the provisioner's registered `"none"`, T4), `label_*`.

**Failure mode** `sm_probe_failure` (per-env): `probe_success→0`, `probe_duration_seconds→3.0s`, log
`msg="Check failed"`, stream `probe_success="0"`.

> ⚠ Provision-before-emit: the SM app clamps its native timeline to `check.created`; backfilled points
> before creation are hidden in SM's native views (raw metrics still in Explore). Always register
> first, then push forward-in-time data. synthkit's provisioner is a standalone `cmd/sm-provision` CLI
> (SK-21 resolved) reading config-driven check templates from blueprint YAML; the offline-probe identity
> is single-sourced from exported `sm.DefaultProbe*` constants so provisioner and emitter cannot drift.

```yaml signals
family: sm_checks
scope: substrate
sink: promrw
labels:
  job: <check-job>
  instance: <check-target-url>
  probe: <probe-name>
  config_version: <modified×1e9>   # ⚠ stable join-key; all series re-stamped on check edit (T1)
  label_*: <user-labels>           # e.g. label_test
metrics:
  - {root: probe_success, type: gauge, unit: bool, v: ok, note: "0 under failure"}
  - {root: probe_duration_seconds, type: gauge, unit: seconds, v: ok, note: "= SMTimeoutSeconds (3.0s) under failure"}
  - {root: probe_all_success_sum, type: counter, unit: count, v: ok, note: "SUMMARY — separate Add call (T3); no buckets"}
  - {root: probe_all_success_count, type: counter, unit: count, v: ok}
  - {root: probe_all_duration_seconds_bucket, type: histogram, unit: seconds, v: ok, note: "Observe only — do NOT also Add _sum/_count (T2); le buckets [0.1,0.25,0.5,1,2.5,5,10]"}
  - {root: probe_all_duration_seconds_sum, type: histogram, unit: seconds, v: ok}
  - {root: probe_all_duration_seconds_count, type: histogram, unit: count, v: ok}
  - {root: sm_check_info, type: gauge, unit: info, v: ok, note: "G=1; metadata anchor; extra labels below"}
info_series:
  name: sm_check_info
  extra_labels: [check_name, region, frequency, geohash, alert_sensitivity, "label_*"]
  note: "alert_sensitivity is the check's REAL value — ⚠ independent of the provisioner's registered 'none' (T4)"
```

## SM Loki logs [slug: sm-logs]

**Loki stream** (`{source="synthetic-monitoring-agent", check_name, instance, job, probe, region,
probe_success ("1"|"0" — ⚠ a STREAM label so failure queries `{…, probe_success="0"} | logfmt | msg=
"Check failed"` filter at stream speed), label_*}`; logfmt body with `msg="Check succeeded|failed"`,
`duration_seconds`).

```yaml signals
family: sm_logs
scope: substrate
sink: loki
stream_labels:
  source: synthetic-monitoring-agent
  check_name: <check-name>
  instance: <check-target-url>
  job: <check-job>
  probe: <probe-name>
  region: <probe-region>
  probe_success: '"1"|"0"'   # ⚠ STREAM label — failure queries filter at stream speed on probe_success="0"
  label_*: <user-labels>
body_fields:
  format: logfmt
  msg: '"Check succeeded"|"Check failed"'
  duration_seconds: <float>
```
