# dashboards/internal — synthkit generator self-observability (staff stack)

**Internal** monitoring dashboard for the synthkit generator **process itself**
(`service.name=synthkit`), as opposed to the synthetic telemetry synthkit emits for the target
(customer) stack. synthkit instruments itself via [`internal/selfobs`](../../internal/selfobs) +
[`internal/profiling`](../../internal/profiling) and ships RED metrics, Go-runtime metrics,
per-tick traces, continuous profiles, and its operational log stream to a **separate** Grafana
Cloud **staff stack** over the `GC_SELF_OTLP_*` / `GC_PYROSCOPE_*` credentials (never `GC_TOKEN`).
This folder visualises that self-telemetry.

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
make selfobs-dashboard

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
