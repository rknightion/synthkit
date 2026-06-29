# synthkit signal catalogue — global canon

Cross-cutting rules that span ALL signal families. Area files in `signals/` reference
these by slug (e.g. `[slug: cardinality]`) and never restate them. This is the single
home for the global contract; see `../SIGNALS.md` for the family index.

**Verification key for `yaml signals` blocks (`v:` field):** `ok` = verified (✅) · `assumed` = assumed/synthetic, pending confirmation (Ⓐ) · `trap` = known trap/constraint (⚠).

---

**Verification key used throughout:** ✅ = verified (vendor docs and/or live empirical capture in the
predecessor) · Ⓐ = assumed/synthetic frozen value (pending confirmation) · ⚠ = known trap/constraint.

**AI/LLM admitted (Spec 2b).** The trace tree admits typed AI hops in addition to db/cache/service:
`gateway`/`model`/`agent`/`tool`/`workflow`/`retrieval`, each carrying `gen_ai.*` span attributes
sourced from `internal/genai` (the gen_ai semconv vocabulary mechanic lib). A `workflow`→`agent`→
`gateway`→`model` chain nests via `via:` exactly like any other hop. gen_ai metrics
(`gen_ai_client_*`/`gen_ai_server_*`) and the LLM-gateway / eval-platform metric families are
ordinary emitted series. Content is NEVER emitted (prompt/completion/messages keys are strip-listed
in `internal/genai`). Customer identity stays blueprint-only.

---

## Push topology (generic) [slug: push-topology]

Every construct routes its signals to one of four sinks. Exact endpoint URLs, stack ids, usernames,
and tokens are deployment configuration (env/credentials), never baked into the catalog or this
contract. See ARCHITECTURE §6 (sinks) for mechanics.

| Signal class | Sink | What it carries | Key contract |
|---|---|---|---|
| **Metrics** | `sink/promrw` → Mimir `remote_write` | ALL metrics — Prometheus-native, CloudWatch-shaped, k8s, span-metrics | FINAL pre-mangled series names (post-OTLP-translation spellings already applied). The OTel metrics SDK is **banned** (I1). Per-push + per-blueprint series budget + kill switch (I7). |
| **Traces** | `sink/otlp` → OTLP gateway → Tempo | traces ONLY | hand-encoded `ResourceSpans` protobuf, multi-Resource per export, explicit timestamps (I2). Never the OTel SDK; never the collector trace import. |
| **Logs** | `sink/loki` → Loki push | logs | 3-tuple `[ts, line, {meta}]`; the sink ASSERTS no high-card key in `Stream.Labels` (I14/I15). |
| **RUM** | `sink/faro` → Faro collector | RUM beacons | POST to the Faro collector with the app key (4th credential surface; optional). The collector — not direct Loki/Tempo push — is the sole writer of the FEO app-registration lifecycle timestamps; an app fed by direct push never registers (§4). |

> **⚠ Metric-routing rule (I1).** The OTel Go *metrics* SDK stamps export-time timestamps, defaults
> to cumulative-only quirks, and hides bucket control behind Views — wrong tool for a fabricator.
> synthkit emits ALL metrics via Mimir `remote_write` with the final names pre-applied. OTLP is used
> for **traces only** (spans accept explicit timestamps).

> **⚠ Cumulative-state rule (I3).** Counters/histograms are cumulative across ticks (`internal/state`):
> push running totals, never deltas. Container/gateway restarts reset counters = clean `rate()`
> window (I29).


---

## Global canon

### The `blueprint` selector label (I17) [slug: blueprint-label]

The `blueprint=<label>` selector is stamped by **exactly one** component: the scoped metric/log/span
writer in the composition root, which clones labels before stamping (`Collect()` output aliases live
cumulative state — clone-before-stamp). **Constructs never stamp it.** It is stamped ONLY on
blueprint-scoped families.

### Substrate vs blueprint scoping (ARCHITECTURE §5, §7 I21) [slug: scoping]

Scope is declared per construct *kind* at registration (`Scope` ∈ `ScopeBlueprint | ScopeSubstrate`)
and enforced by the scoped writers. Two concurrent blueprints separate by the `blueprint` label on
blueprint-scoped families, and by **declared identity** (cluster name, account_id, dbo11y instance
identity) on substrate families — all blueprint-declared and collision-checked at load.

| Construct / signal kind | Scope | How a second blueprint is disambiguated |
|---|---|---|
| Workload signals (web_service: APM span-metrics, service-graph, app spans, app logs, RUM) | **ScopeBlueprint** | `blueprint` label (on service-graph: `client_blueprint` / `server_blueprint` per edge-side — §3.4) |
| CloudWatch families (ALB, NLB, EC2, EBS, NAT GW, S3, EKS-control-plane, Firehose, RDS, ElastiCache) | **ScopeBlueprint** | `blueprint` label |
| Cloudflare (zone + tunnel) | **ScopeBlueprint** | `blueprint` label |
| k8s-monitoring substrate (KSM, cAdvisor, kubelet, node-exporter, conformance/discovery) | **ScopeSubstrate** | `cluster` + `k8s_cluster_name` (dual) |
| k8s add-ons (LBC, ExternalDNS, CoreDNS, VPC CNI, cert-manager, cluster-autoscaler, EBS-CSI, KSM-ingress) | **ScopeSubstrate** | `cluster` + `k8s_cluster_name` |
| dbo11y (MySQL, Postgres) | **ScopeSubstrate** | dbo11y instance identity (`instance`, `server_id`, `db_instance_identifier`) |
| CSP Azure / CSP GCP | **ScopeSubstrate** | `subscriptionID` / `project_id` + `resourceID` / `database_id` |
| Synthetic Monitoring (SM) | **ScopeSubstrate** | check identity (`job`, `instance`, `probe`, `config_version`, `check_name`). Decided substrate (SK-22 resolved): the SM app disambiguates by check identity, never by a blueprint label. |
| Fleet Management + Alloy self-metrics | **ScopeSubstrate** | `cluster` + `collector_id` |

> ⚠ A test asserts no substrate series EVER carries `blueprint`; the de-Rochification test asserts no
> construct source mentions any blueprint name (I18). Fanning substrate families per blueprint would
> break vendor-app conformance panels (`count by(cluster,…)`).

### The environment-label-key variance [slug: env-label-keys]

The same env value (`prod`, `dev1`, …) rides under a **different label key** depending on the signal
family. Emit the right key per family — this is a hard correctness rule, not cosmetic:

| Key | Where it appears |
|---|---|
| `deployment_environment_name` | **synthkit's native promrw emit** — ALL of: span-metrics (`traces_spanmetrics_*`), service-graph (`traces_service_graph_*`), OTel HTTP metrics (`http_server_*`), gen_ai client metrics (`gen_ai_client_*`), and resource-info metrics (`target_info` / `traces_target_info`). Also the OTLP span resource attribute `deployment.environment.name` (the OTEL semconv `_name` form). ✅ This is the single authoritative form for synthkit-native series (2026-06-23). |
| `deployment_environment` | **Gateway-promoted label only.** The GC OTLP gateway auto-promotes the `deployment.environment.name` resource attr into BOTH `deployment_environment` AND `deployment_environment_name` labels on Tempo-derived metrics. So on a live stack with a real gateway, BOTH forms may appear on gateway-derived series — this is gateway behaviour, not synthkit's own emit. The legacy `deployment_environment` form is **gone from synthkit's own native promrw emit**. |
| `env` | Cloudflare tunnel metrics, dbo11y (where the cell/env identity is carried), FM/Alloy collector identity, Faro/RUM Loki stream label |
| (Beyla) | Beyla span-metric / service-graph labels use `deployment_environment_name` (confirmed v:ok; see `signals/beyla.md`) |

### Correlation key-set (end-to-end request correlation) [slug: request-correlation]

`{correlation_id, trace_id, span_id, session_id, request_id}` are span attributes / log structured
metadata — **span/log FIELDS, NEVER labels** (I9, I14). They are minted ONLY by the per-blueprint
ledger; constructs and workloads read them, never mint (I9). On browser spans the user-action id
equals the `request_id` (the cross-system join key — §4.8).

⚠ **Span attr (2026-06-23 §4.1):** The OTLP span attribute is `app.correlation_id` (vendor-neutral
application-level correlation). The log structured-metadata field stays `correlation_id` (unchanged).
Correlation log lines also carry a `traceparent` W3C field. AgentCore JSON bodies also carry
`traceparent`.

### Hard cardinality rule (I14, I15) [slug: cardinality]

UUID-class keys (`trace_id`, `span_id`, `request_id`, `session_id`, `correlation_id`, KSM pod `uid`,
dbo11y `digest`/`queryid`, …) are NEVER Mimir labels or Loki **stream** labels → carried as span
attributes or Loki **structured metadata** only. The Loki sink asserts no high-card key ever lands in
`Stream.Labels` on every push.

**Per-sink forbidden sets (I15):** legal-in-Mimir ≠ legal-in-Loki-stream. A key may be a Mimir label
and simultaneously forbidden as a Loki stream label (it must ride as structured metadata there).
`session_id` in particular is always a **body field**, never a label or structured metadata, in both
Faro/RUM and app logs (§3, §4).

### Content-strip rule + sentinel (I23) [slug: content-strip]

No content-bearing field ever reaches Grafana — not on any span attribute, log field, or metric. DB
spans carry schema/statement shape only, never row content; logs carry counts and event names, never
payloads. The strip is proven by **two** sentinel signals (§6 of the Alloy meta-health construct):

- `synthkit_content_dropped_total` (Counter, accumulates) — proves the strip processor is *running*.
- `synthkit_content_leak_test` (Gauge, **always exactly 0.0**) — proves nothing is *leaking*. A
  single alert `synthkit_content_leak_test > 0` fires on any data-egress violation.

> ⚠ The original names (`<prefix>_content_*`) are normalised to `synthkit_content_*` — prefix is the
> only change; the shape and label contract are identical.


---

## Realistic-shape rules (shared `shape` engine — I31) [slug: shape-rules]

- **Diurnal:** a flat-topped business-hours **PLATEAU** (not a single-cosine spike), low overnight;
  anchored to the blueprint timezone.
- **Weekly:** weekends lower; **non-prod environments scale toward ~0 on weekends**.
- **Env weighting:** `prod` ≫ non-prod.
- **Counters & histograms:** per-series monotonic state lives in the emitter across ticks (I3);
  histograms emitted with the pinned `le` sets (per family) — `le` STYLE is ingestion-path-dependent
  (`LEBare` minimal decimals vs `LEDotZero` forced ".0"); some span-metric families are NATIVE
  histograms (no `_bucket`/`le`) — honor whichever form the real source emits (I4).
- **Scripted incidents** (schedulable, staggered, time-boxed): latency_spike, throttle/error spike,
  pod crashloop (k8s restarts + APM errors), SM probe failure, dbo11y replication-lag /
  lock-contention / slow-query-storm / connection-saturation. Each keeps end-to-end request correlation intact
  (shared request ledger) so cross-signal correlation stays demonstrable.

---

## Cardinality budget (per blueprint) [slug: cardinality-budget]

Per-construct estimates at default knobs. One representative blueprint (1 prod env, 1 EKS cluster
@replicas=2 → ~4 nodes, 1 web_service workload, k8s-monitoring on, CW on) sits well within a typical
`SERIES_CAP`. The per-blueprint scoped writer applies its own budget; `promrw` `SERIES_CAP` is the
global backstop (I7).

| Construct family | Estimate (defaults) | Source |
|---|---|---|
| k8s-monitoring substrate (KSM + cAdvisor + kubelet + node-exporter + conformance) | ~1.6k series / cluster | extract 02 / predecessor SIGNALS §0 (~1.6k → ~11.4k across 7 clusters) |
| CloudWatch families (ALB/NLB/EC2/EBS/NATGW/S3/EKS/Firehose, 5 stats each) | ~0.5–1.5k / env | extract 01 + predecessor §2.9 |
| APM span-metrics + service-graph + target_info (web_service) | ~0.3–1k / workload | extract 05 / predecessor §2.5 |
| dbo11y (both lanes; 5 MySQL + 6 Postgres, 40 digests/DB) | ~2,055 series | extract 03 §6 |
| CSP Azure + GCP (3 subs / 3 projects, default counts) | ~1.5–3k (both) | extract 04 + predecessor §13 |
| k8s add-ons (8 modules, when enabled) | ~0.5–1.5k / cluster | extract 02 §6 |
| SM checks (N templates × M envs) | ~8 series/check + 1 `sm_check_info` | extract 06 §1.5 |
| Fleet Management (per fake collector) | ~15 series/collector | extract 06 §2.4 |
| Alloy meta-health + content sentinel | ~50–100 series / cluster | extract 06 §3–4 |

Scale guards: per-construct count knobs (replicas, instance counts, digests/DB), per-feature
sub-signal subsets, `feature.MaxEstateCount` clamps, and the `promrw` `SERIES_CAP` backstop.
