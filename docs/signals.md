---
title: Reading the Catalogue
description: How to read and use the synthkit signal catalogue — the living, provenance-cited contract of every metric, trace, log, and RUM signal.
---

# Reading the Catalogue

The [`signals/`](https://github.com/rknightion/synthkit/blob/main/signals/) directory is the **living, definitive contract** of every signal synthkit can emit: every metric name, label key and value, trace span attribute, log stream label, structured metadata field, and RUM beacon field. It is provenance-cited (vendor docs, live empirical capture, or predecessor codebase), meant to grow as real signals are discovered, and authoritative over the Go code — if the two diverge, the code is wrong.

The companion index [`SIGNALS.md`](https://github.com/rknightion/synthkit/blob/main/SIGNALS.md) lists every area file and its family slugs. The per-area navigable index is at [Signal Areas](signal-areas.md). Open items — signals referenced in code but not yet fully verified — live in [`cantfind.md`](https://github.com/rknightion/synthkit/blob/main/cantfind.md).

---

## The `yaml signals` block schema

Each area file contains **prose** (provenance, traps, verification notes) and one or more fenced `yaml signals` blocks — the machine-readable contract. A representative block from `signals/cw.md`:

```yaml
family: aws_applicationelb
scope: blueprint
sink: promrw
stats: [_sum, _average, _maximum, _minimum, _sample_count]
labels:
  account_id: <account>
  region: <aws-region>
  namespace: AWS/ApplicationELB
  job: cloud/aws/applicationelb
  name: <resource-arn>|global
  dimension_LoadBalancer: app/<name>/<hex-id>
  dimension_TargetGroup: targetgroup/<name>/<hex-id>
  dimension_AvailabilityZone: <az>
  tag_*: <resource-tags>
metrics:
  - {root: request_count, type: gauge, unit: count, v: ok}
  - {root: target_response_time, type: gauge, unit: seconds, v: ok}
  - {root: httpcode_target_2_xx_count, type: gauge, unit: count, v: ok}
```

| Field | Meaning |
|---|---|
| `family` | Series name prefix (for metrics), span kind (for traces), or stream identity (for logs). |
| `scope` | `blueprint` or `substrate` — see [Scope](#scope-blueprint-vs-substrate) below. |
| `sink` | Where the signal is delivered — see [Sinks](#sinks) below. |
| `stats` | CloudWatch 5-stat suffix expansion only; omitted for all other families. |
| `labels` | A key → value/shape **map** of every Prometheus label, Loki stream label, or OTLP resource attribute. `job` is just a label, not metadata. Absent dimensions are **omitted** — never `""` or `"NA"`. |
| `metrics` | Per-metric entries: `root` (series name root), `type` (`gauge`, `counter`, `histogram`, `summary`), `unit`, `v` (verification marker), `note` (traps or caveats). |

Logs blocks use `stream_labels` / `structured_metadata` / `body_fields` instead of `labels` / `metrics`. Traces blocks use `span_attributes` / `resource_attributes` / `correlation_fields`.

The `stats:` field on a CloudWatch block declares the five per-period gauge suffixes that expand each `root` into five separate Mimir series — `aws_applicationelb_request_count_sum`, `aws_applicationelb_request_count_average`, and so on. These are **per-period gauges**, never rate-able counters.

---

## Verification legend

The `v:` field on each metric entry comes from [`signals/00-canon.md`](https://github.com/rknightion/synthkit/blob/main/signals/00-canon.md):

| Marker | Meaning |
|---|---|
| `ok` | Verified — confirmed against vendor docs and/or live empirical capture from a real stack. |
| `assumed` | Synthetic/frozen value, pending live confirmation. The signal exists in the spec but has not been observed on a real stack yet. |
| `trap` | Known trap or constraint — the signal is correct but has a non-obvious behaviour (wrong type inference, always-zero baseline, cardinality risk, etc.). Read the prose note. |

The full verification key is stated in `signals/00-canon.md` and never restated in area files — they reference it by `[slug: ...]`. Open items with no confirmed shape are tracked in [`cantfind.md`](https://github.com/rknightion/synthkit/blob/main/cantfind.md) as `SK-N` entries rather than emitted as invented signals.

---

## Scope: blueprint vs substrate

Every construct is registered as either `ScopeBlueprint` or `ScopeSubstrate`. This controls how two concurrently-running blueprints are separated and whether a `blueprint` label appears on the emitted series.

**Blueprint-scoped** constructs emit per-blueprint signals. The composition root stamps a `blueprint=<label>` selector on every series — constructs themselves never stamp it. Two blueprints running simultaneously produce disjoint series sets distinguished by this label. Examples: CloudWatch families, APM span-metrics, gen_ai metrics, Cloudflare, traces, app logs, RUM.

**Substrate-scoped** constructs model shared infrastructure that exists independently of any particular application blueprint. They carry **no `blueprint` label** — a test asserts this invariant. Two blueprints are disambiguated instead by **declared identity** embedded in the signals themselves:

| Substrate construct | Disambiguation identity |
|---|---|
| k8s-monitoring, k8s add-ons | `cluster` + `k8s_cluster_name` |
| dbo11y (MySQL, Postgres) | `instance`, `server_id`, `db_instance_identifier` |
| CSP Azure / GCP | `subscriptionID` / `project_id` + `resourceID` / `database_id` |
| Synthetic Monitoring | `job`, `instance`, `probe`, `config_version`, `check_name` |
| Fleet Management + Alloy | `cluster` + `collector_id` |
| Network topology | device and topology identity labels |
| Portkey, LangSmith, Snowflake | gateway/platform instance identity |

Some constructs are **dual-scoped** (e.g. Beyla, profiles, logs): some families within the area are blueprint-scoped and others are substrate-scoped. The area file's per-family `scope:` field is authoritative.

See [`signals/00-canon.md`](https://github.com/rknightion/synthkit/blob/main/signals/00-canon.md) `[slug: scoping]` for the full invariants.

---

## Sinks

synthkit routes signals to four delivery sinks, each with its own endpoint and credential:

| Sink | Delivery target | What it carries |
|---|---|---|
| `promrw` | Mimir via Prometheus Remote-Write v2 | All metrics — Prometheus-native, CloudWatch-shaped, k8s, span-metrics, gen_ai, service-graph. Series names are **final pre-mangled** (OTLP-translation spellings already applied). The OTel metrics SDK is banned on the synthetic-data path. |
| `otlp` | OTLP gateway → Tempo | Traces only — hand-encoded `ResourceSpans` protobuf, multi-Resource per export, explicit timestamps. Also carries native OTLP application metrics when the `otel:` lane is active. |
| `loki` | Loki push API | Logs — 3-tuple `[timestamp, line, {metadata}]`. The sink asserts that no high-cardinality key ever appears in `Stream.Labels`. |
| `faro` | Faro collector | RUM beacons — POSTed to the Faro collector with the app key. The collector (not direct Loki/Tempo push) writes the FEO app-registration lifecycle. |
| `pyroscope` | Pyroscope push API | Continuous profiling — eBPF, pprof-scrape, Java, and SDK-push emitter shapes. |

The sink identifiers used in `yaml signals` blocks (`promrw`, `loki`, `otlp`, `faro`, `pyroscope`) map directly to the sinks listed above. A family can appear under more than one sink (e.g. `dbo11y` emits to both `promrw` and `loki`).

---

## Global canon rules

[`signals/00-canon.md`](https://github.com/rknightion/synthkit/blob/main/signals/00-canon.md) states the cross-cutting rules that all area files reference by slug but never restate. The key rules:

**Cardinality hard limit.** UUID-class keys — `trace_id`, `span_id`, `request_id`, `session_id`, `correlation_id`, KSM pod `uid`, dbo11y `digest`/`queryid`, and similar — are **never** Mimir labels or Loki stream labels. They ride as span attributes or Loki structured metadata only. The Loki sink asserts this on every push. A key legal as a Mimir label may still be forbidden as a Loki stream label.

**Absent dimension rule.** If a dimension or label is not applicable in a given context, it is **omitted** from the emitted series — never emitted as `""` or `"NA"`. Queries must use `label_values()` or `absent()` guards accordingly.

**Content strip.** No prompt, completion, query result, row content, or user-identifiable payload ever reaches Grafana. DB spans carry schema/statement shape only; logs carry counts and event names. Two sentinel metrics in the Fleet Management area prove the strip processor is running and nothing is leaking (`synthkit_content_dropped_total`, `synthkit_content_leak_test`).

**Environment label key variance.** The same environment value (`prod`, `dev1`, …) appears under a **different label key** depending on the signal family. synthkit's own native promrw emit uses `deployment_environment_name` (OTel semconv `_name` form) on span-metrics, service-graph, gen_ai metrics, and `target_info`. Cloudflare tunnel metrics, dbo11y, and Fleet Management use `env`. Querying across families requires accounting for this variance.

**Cumulative counters.** Counters and histograms are cumulative across ticks (`internal/state`). The sink always pushes running totals, never deltas. A process restart resets counters, producing a clean `rate()` window.

**Blueprint selector stamped once.** The scoped metric/log/span writer in the composition root stamps `blueprint=<label>` after cloning the label set — constructs never stamp it. Substrate series carry no blueprint label.

---

## Verifying locally what will be emitted

Run synthkit in dry-run mode with `-once -dump` to print the full series/label inventory to stdout without pushing anything to Grafana Cloud:

```bash
DRY_RUN=true go run ./cmd/synthkit -once -dump
```

The output is a structured inventory of every series that would be emitted in one tick, with full label sets. Diff it against the `yaml signals` blocks in the relevant area files to verify a new construct, check for label drift, or confirm that a signals/ change is reflected in the emit path.

`DRY_RUN` defaults to `true`, so this command is safe to run without a live Grafana Cloud stack or a populated `.env` file.

---

## Growing the catalogue

The `signals/` catalogue is meant to grow. When you discover a real signal via any pathway — live metric capture, exporter source inspection, vendor documentation, metric-stream output — record it:

1. Add or update the family's `yaml signals` block in the right area file, with prose provenance and a capture date.
2. If the discovery resolves a `cantfind.md` SK-N item, move it out of that file and into the area file.
3. Verify with `DRY_RUN=true go run ./cmd/synthkit -once -dump` and diff the output.
4. Correct the synth to match observed reality — never the reverse.

Never invent a metric, label, or field name. If a name cannot be sourced, add a PENDING SK-N entry to [`cantfind.md`](https://github.com/rknightion/synthkit/blob/main/cantfind.md) and flag it rather than emitting an assumed name.

For the full per-area index, see [Signal Areas](signal-areas.md). For construct wiring, see [Constructs](constructs.md) and [Emission Switches](emission-switches.md).
