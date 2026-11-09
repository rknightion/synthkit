# dashboards/internal — synthkit generator self-observability (staff stack)

**Internal** monitoring dashboard for the synthkit generator **process itself**
(`service.name=synthkit`), as opposed to the synthetic telemetry synthkit emits for the target
(customer) stack. synthkit instruments itself via [`internal/selfobs`](../../internal/selfobs) +
[`internal/profiling`](../../internal/profiling) and ships RED metrics, Go-runtime metrics,
per-tick traces, continuous profiles, and its operational log stream to a **separate** Grafana
Cloud **staff stack** over the `GC_SELF_OTLP_*` / `GC_PYROSCOPE_*` credentials (never `GC_TOKEN`).
This folder visualises that self-telemetry.

## Scoped offline export

Use the offline exporter when several synthkit deployments share observability backends, or when
dashboard resources need deployment-specific identities and datasource bindings. It imports the
same generator as the staff dashboard, writes local resources only, and needs no credentials or
network access:

```bash
just selfobs-export \
  --namespace demo-a --deployment demo-a --out /path/to/new-export \
  --metrics-datasource metrics-main --logs-datasource logs-main \
  --traces-datasource traces-main --profiles-datasource profiles-main
```

The output is `folder.json`, `dashboard.json` and `queries.json`. The query inventory records every
enabled panel and variable query, its language, datasource, exact deployment scope and any omitted
panels with reasons. Identical inputs produce identical resource bytes. Different resource
namespaces produce distinct dashboard and folder UIDs, including when long namespaces share a
prefix. Use `--folder` to choose a base folder UID and `--folder-title` for its base title. Both
remain namespaced: the exported UID includes namespace identity and the title gains a namespace
suffix, so two exports cannot replace each other's folder resource. These options do not target
an existing shared folder. The output
directory must be new; the command refuses an existing directory rather than replacing its files.
The no-argument `build_selfobs_dashboard.py` command and its staff artifact retain their defaults.

### Deployment identity and backend prerequisites

`--namespace` names dashboard resources. `--deployment` filters telemetry. They are separate so a
dashboard can be renamed without changing which deployment it observes.

Set a stable, deployment-unique resource namespace on **every process** in the deployment through
the existing `SELFOBS_TAGS` setting. Profiles use the existing `env` profile tag separately:

```dotenv
SELFOBS_TAGS=service.namespace=demo-a
PYROSCOPE_TAGS=env=demo-a
```

Append these pairs to any existing tags instead of removing those tags. Keep the default
`service.name=synthkit`. Reuse the same values after a restart and across HA processes, and choose
different values for independent deployments. The exporter accepts deployment values of 1 to 128
letters, digits, dots, underscores or hyphens, starting with a letter or digit. Quotes, backslashes
and Grafana variable expressions are refused. It does not discover or change a running process's
configuration.

The supported projection is:

| Signal | Required receiver projection | Query boundary |
| --- | --- | --- |
| Metrics | Standard OTLP conversion preserves `service.namespace` and `service.name` as `job=<deployment>/synthkit`, and `service.instance.id` as `instance`. | Every metric selector and variable query has the exact `job` matcher before aggregation. |
| Logs | Native OTLP ingestion preserves `service.namespace` as `service_namespace`, either indexed or as structured metadata. | Every LogQL stream pipeline filters the exact `service_namespace` value. |
| Traces | Resource attributes retain `service.namespace`. | Every TraceQL query filters the exact `resource.service.namespace` value. |
| Profiles | `PYROSCOPE_TAGS` supplies the matching `env` tag. | Every profile selector filters the exact `env` value. |

Grafana's [Mimir OTLP conversion guide](https://grafana.com/docs/mimir/latest/configure/configure-otel-collector/)
defines the metrics job/instance projection. Loki's [OTLP mapping guide](https://grafana.com/docs/loki/latest/send-data/otel/)
defines normalized attribute names and indexed versus structured metadata storage. A receiver that
rewrites or drops these dimensions is unsupported. The exporter cannot verify backend configuration
offline; preserve these dimensions before using its resources.

The instance picker selects metric processes within the fixed deployment. Selecting All includes
replacement processes without widening the deployment boundary. Logs, traces and profiles remain
deployment-wide; the exporter makes no per-process profiling claim. A URL-provided instance value
cannot widen the fixed metrics job matcher.

Backend-derived Tempo spanmetrics are omitted with machine-readable diagnostics because their
default dimensions do not establish a deployment namespace. They never fall back to all synthkit
processes. Use `--signals metrics,logs,traces` if profile tags are unavailable, or a narrower list
when another receiver projection is unavailable. Metrics are required for the dashboard's process
and dimension variables. An unsupported signal name or malformed scope/binding refuses export
before writing resources.

These filters select data; they do not authorize datasource access. Configure Grafana datasource
permissions separately. An empty result does not establish delivery or backend readiness.

### Offline verification

The Go receiver proof exercises actual self-observability providers and OTLP HTTP exports for two
deployments and replacement process identities. The Python CLI suite checks resources, bindings,
scope rejection, omissions and unchanged default generation. It also executes every exported
PromQL query and variable selector with `promtool` when available, including foreign-deployment
negative fixtures and multi-process positive fixtures. Run the additional query proof explicitly:

```bash
REQUIRE_PROMTOOL=1 python3 -m unittest discover -s dashboards/internal \
  -p 'test_export_selfobs_dashboard.py'
just test-packages ./dashboards/internal </dev/null
```

`REQUIRE_PROMTOOL=1` fails if the tool is missing. The portable Python suite otherwise reports that
query-execution check as skipped when `promtool` is unavailable. These checks establish OTLP
resource transport and offline PromQL behavior against the documented projection. They do not
establish a live Mimir, Loki, Tempo or Pyroscope deployment, or prove Grafana rendering. Publishing
and visual verification remain separate operations.

## Dashboard

| File | Stack | Folder | UID |
|---|---|---|---|
| `synthkit-selfobs.json` | **staff stack** | `synthkit — Self-Monitoring` (`synthkit-selfmon`) | `synthkit-selfobs` |

Schema **`dashboard.grafana.app/v2alpha1`** — a *dynamic* dashboard: **TabsLayout** (9 tabs) + a
per-`$sink` repeating **RowsLayout** on the Push tab.

Tabs: **Overview** (triage) · **Push pipeline (RED)** (summary + per-blueprint items+bytes breakdown + one
repeating row per sink) · **Cycle & instance health** (per-blueprint cycle pacing + per-instance tick health,
enriched with `construct_kind` + `blueprint` columns, plus tick-rate-by-kind and tick-rate-by-blueprint) ·
**Cardinality** (`synthkit_cardinality_series` X-ray: total series, by blueprint, top-15 instances, by kind) ·
**Go runtime** · **Profiling** (Pyroscope — *conditionally rendered*) ·
**Traces & spans** (Tempo spanmetrics + trace list) · **Logs** (Loki severity volume + streams) ·
**Config & volume** (volume multiplier / blueprint count / ledger + build identity).

### Telemetry contract (the generator is the source of truth)
The dashboard is mechanically generated from the metric surface emitted by `internal/selfobs`.
Two things to know:

- **`construct_instance`, not `instance`.** Per-tick metrics carry the ticked construct/workload
  instance under `construct_instance`. The datapoint attribute was renamed from `instance`
  because OTLP→Prometheus derives the `instance` label from the resource `service.instance.id`
  (the container id), which silently clobbered a datapoint attribute of the same name — so the
  per-instance tick breakdown was unqueryable. **The Construct instances tab reads empty until the
  running generator includes this rename.**
- **`blueprint` is omitted, never empty.** `synthkit_push_total` carries a `blueprint` label only
  for blueprint-scoped pushes; substrate-scoped pushes (k8s, dbo11y, CSP …) carry no blueprint and
  group under the empty series — an absent dimension is never `""`. By-blueprint panels relabel that
  empty series to `(substrate)` (otherwise Grafana shows it as a meaningless `Value`).
- **`run_mode` filters out dry-run/dev noise.** Each process stamps `target_info{run_mode=live|dry_run}`
  (+ a pid-disambiguated `service.instance.id`). The generator gates self-obs OFF under `DRY_RUN`, so a
  proper build emits only `live`; the `$instance` picker excludes `run_mode="dry_run"`. A stray dry-run
  dev box on an old (pre-`run_mode`) build can still leak — the lasting fix is to stop/update it.
- **Newer signals (need a build that emits them):** `signal_type` on `synthkit_push_*`
  (metrics/traces/logs/rum/profiles); `synthkit_queue_flush_*` (per-flush latency [fine buckets] /
  batch size / flush-error count by sink); `event="config_change"` control-plane audit logs; and
  enriched op-traces — `cycle` / `flush <sink>` / `fleet <op>` spans (the `flush <sink>` spans make the
  QUEUED sinks visible in Tempo/spanmetrics, since the decoupled queue ships them off the traced tick path).

### Conditional Profiling tab
The **Profiling** tab carries a v2 `conditionalRendering` group (`ConditionalRenderingData →
value:true`) so it shows **only when Pyroscope profiles exist** for `service_name="synthkit"` —
i.e. when the generator ran with `PYROSCOPE_ENABLED=true`. Profiles carry `service_name`/`version`/
`env` labels only (no `instance` tag), so the Profiling panels don't take `$instance`.

## Rebuild / push / verify

`GC_SELF_GRAFANA_URL` (e.g. `https://<your-staff-stack>.grafana.net`) is the single source of truth for
both the control-UI deep-link ("Open generator self-obs in Grafana →" shown on the Overview and
Health pages) and the build+push shortcut below. Set it in `.env`; leave it empty to hide the
deep-link.

```bash
# Quickest path — build + push in one shot (reads GCX_CONTEXT from env or defaults to your staff stack):
just selfobs-dashboard

# Or step-by-step:
cd dashboards/internal
python3 build_selfobs_dashboard.py                                   # → synthkit-selfobs.json
gcx --context <staff-stack> resources validate -p synthkit-selfobs.json       # {"failures":[]}
gcx --context <staff-stack> resources push     -p synthkit-selfobs.json

# snapshot the ACTIVE (first) tab — the snapshot tool has no --tab flag
GCX_AGENT_MODE=true gcx dashboards snapshot synthkit-selfobs --context <staff-stack> \
  --output-dir snapshots --since 3h --width 1920 --theme dark

# verify a non-first tab: float it to position 0 under a temp uid, snapshot, then delete
python3 - <<'PY'
import json,copy
d=json.load(open("synthkit-selfobs.json")); tabs=d["spec"]["layout"]["spec"]["tabs"]
i=[t["spec"]["title"] for t in tabs].index("Push pipeline (RED)")   # tab to verify
d["metadata"]["name"]="synthkit-selfobs-verify"; d["spec"]["title"]="VERIFY"
d["spec"]["layout"]["spec"]["tabs"]=[tabs[i]]+[t for j,t in enumerate(tabs) if j!=i]
json.dump(d,open("/tmp/verify.json","w"),indent=2)
PY
gcx --context <staff-stack> resources push -p /tmp/verify.json
GCX_AGENT_MODE=true gcx dashboards snapshot synthkit-selfobs-verify --context <staff-stack> \
  --output-dir snapshots --since 3h --width 1920 --theme dark
gcx --context <staff-stack> resources delete dashboards/synthkit-selfobs-verify   # clean up the temp
```
