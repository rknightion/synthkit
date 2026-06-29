#!/usr/bin/env python3
"""
Generator for the synthkit SELF-OBSERVABILITY dashboard (staff stack).

This is the INTERNAL monitoring dashboard for the synthkit generator PROCESS itself — NOT the
synthetic telemetry synthkit emits for the target stack (customer stack). It visualises the telemetry
produced by internal/selfobs (RED on the synthetic-push pipeline, per-construct-instance tick
health, Go runtime, self-obs traces/spanmetrics, the operational log stream) plus internal/profiling
(Pyroscope), all shipped to a SEPARATE stack (staff stack) over the GC_SELF_OTLP_* / GC_PYROSCOPE_* creds.

Schema: dashboard.grafana.app/v2alpha1 — TabsLayout + a per-sink repeating RowsLayout on the Push
tab. Emits synthkit-selfobs.json.

Datasources on staff stack:
  metrics  prometheus  grafanacloud-prom
  logs     loki        grafanacloud-logs
  traces   tempo       grafanacloud-traces
  profiles pyroscope   grafanacloud-profiles

Telemetry surface (internal/selfobs, updated 2026-06-16):
  synthkit_push_total{sink,signal_type,blueprint,outcome}              counter  (signal_type NEW: metrics/traces/logs/rum/profiles)
  synthkit_push_items_total{sink,signal_type,blueprint}                counter  (blueprint omitted for substrate pushes)
  synthkit_push_bytes_total{sink,signal_type,blueprint}                counter  (promrw excluded by design)
  synthkit_push_duration_seconds_{bucket,sum,count}{sink,signal_type,blueprint}    histogram (s)
  synthkit_queue_flush_total{sink,outcome}                             counter NEW — per-batch delivery-queue flushes
  synthkit_queue_flush_duration_seconds_{bucket,sum,count}{sink}       histogram (s) NEW — FINE buckets (.005–10s)
  synthkit_queue_flush_batch_{bucket,sum,count}{sink}                  histogram NEW — items per flush
  target_info{...,run_mode}                                            run_mode=live|dry_run NEW (dashboards drop dry_run)
  Loki event="config_change" {volume_multiplier,failures_active,disabled_*,active_scenarios,span_metrics_bps}  NEW
  Tempo span_name: cycle (CONSUMER) · flush <sink> (CONSUMER) · push faro (CLIENT) · fleet <op> (CLIENT) · tick (INTERNAL)
  synthkit_tick_total{construct_instance,construct_kind,blueprint,outcome}         counter  (construct_kind+blueprint NEW)
  synthkit_tick_duration_seconds_{bucket,...}{construct_instance,construct_kind,blueprint}  histogram (s)  (construct_kind+blueprint NEW)
  synthkit_cardinality_series{blueprint,construct_kind,construct_instance}         gauge NEW — distinct synthetic series emitted
                                                                                   (internal X-ray; blueprint omitted for substrate)
  synthkit_cycle_duration_seconds_{bucket,sum,count}{blueprint}    histogram (s) — full blueprint cycle
  synthkit_dropped_ticks_total{blueprint}                  counter (ticks skipped when prior cycle ran long)
  synthkit_queue_depth{sink}                               gauge (observable) — enqueued-but-unflushed items per
                                                           delivery-queue shard; healthy ≈ 0; sustained growth =
                                                           delivery falling behind generation
  synthkit_queue_enqueue_blocked_total{sink}               counter — increments each time a tick blocked enqueuing
                                                           because a queue shard was full (backpressure); healthy = 0;
                                                           any rate > 0 = host can't ship as fast as it generates
  synthkit_ledger_size                                     gauge (observable)
  synthkit_volume_multiplier                               gauge (observable)
  synthkit_blueprint_count                                 gauge (observable)
  go_goroutine_count / go_memory_used_bytes{go_memory_type} / go_memory_allocated_bytes_total /
  go_memory_allocations_total / go_memory_gc_goal_bytes / go_config_gogc_percent / go_processor_limit
  traces_spanmetrics_calls_total{service,span_name,span_kind,status_code} + traces_spanmetrics_latency
  Loki {service_name="synthkit"} severity_text/scope_name/env/telemetry_kind
       structured push failures: event="push_error" {sink,blueprint,outcome,http.status_code}
  Tempo { resource.service.name="synthkit" }  root span tick > push <sink>

NOTE: synthkit_tick_total's `construct_instance` label only appears once the generator runs a build
that includes the selfobs `construct_instance` rename (the older `instance` datapoint attribute was
clobbered by the Prometheus target `instance` label). Until the server redeploys, the Construct
instances tab and the per-instance Overview stat read empty — every other panel renders immediately.
"""
import json

FOLDER_UID = "synthkit-selfmon"
DASH_NAME = "synthkit-selfobs"
PLUGIN_VER = "13.1.0-27360463442"  # Grafana build; vizConfig.version

PROM = ("grafanacloud-prom", "prometheus")
LOKI = ("grafanacloud-logs", "loki")
TEMPO = ("grafanacloud-traces", "tempo")
PYRO = ("grafanacloud-profiles", "grafana-pyroscope-datasource")
PYRO_SEL = '{service_name="synthkit"}'  # profiles carry service_name/version/env (no instance tag)

JOB = '{job="synthkit", instance=~"$instance"}'

def sub_bp(expr):
    """Relabel the empty (substrate-scoped) `blueprint` series to a readable name. Substrate-scoped
    pushes (k8s/dbo11y/CSP …) carry blueprint="" by design; with legend {{blueprint}} Grafana then
    falls back to displaying the series as "Value" — this turns that into "(substrate)"."""
    return f'label_replace({expr}, "blueprint", "(substrate)", "blueprint", "^$")'

# Min step for Prometheus range queries: self-obs metrics flush every SELFOBS_METRIC_INTERVAL
# (default 15s, was 60s). Flooring the step at the emit cadence keeps rate windows / resolution
# aligned with the data (no over-smoothing); Grafana still coarsens the step on wide time ranges.
EMIT_INTERVAL = "15s"

_id = [0]
def nid():
    _id[0] += 1
    return _id[0]

ELEMENTS = {}

def q(ref, expr, ds=PROM, legend=None, instant=False, fmt=None, extra=None):
    """Build a v2alpha1 PanelQuery. datasource ({type,uid}) lives at spec level;
    query is {kind:<dstype>, spec:<query-model>}."""
    dstype = ds[1]
    if dstype == "tempo":
        spec = {"refId": ref, "query": expr, "queryType": "traceqlSearch"}
    else:
        spec = {"refId": ref, "expr": expr, "editorMode": "code"}
        if instant:
            spec.update({"instant": True, "range": False, "queryType": "instant"})
        else:
            spec.update({"instant": False, "range": True, "queryType": "range"})
            spec["interval"] = EMIT_INTERVAL  # min step = self-obs emit cadence
        if legend is not None:
            spec["legendFormat"] = legend
        if fmt:
            spec["format"] = fmt
    if extra:
        spec.update(extra)
    return {
        "kind": "PanelQuery",
        "spec": {
            "refId": ref,
            "hidden": False,
            "datasource": {"type": dstype, "uid": ds[0]},
            "query": {"kind": dstype, "spec": spec},
        },
    }

def pq(ref, profile_type_id, qtype="metrics", group_by=None, max_nodes=None):
    """Build a v2alpha1 PanelQuery for the Pyroscope datasource. qtype: 'metrics' (time series)
    or 'profile' (flame graph)."""
    spec = {"refId": ref, "queryType": qtype, "profileTypeId": profile_type_id,
            "labelSelector": PYRO_SEL, "groupBy": group_by or []}
    if max_nodes:
        spec["maxNodes"] = max_nodes
    return {"kind": "PanelQuery", "spec": {
        "refId": ref, "hidden": False,
        "datasource": {"type": PYRO[1], "uid": PYRO[0]},
        "query": {"kind": PYRO[1], "spec": spec},
    }}

def panel(key, title, viz, queries, desc="", unit="short", decimals=None,
          options=None, defaults_extra=None, overrides=None, mappings=None,
          thresholds=None, color=None, custom=None, links=None, transforms=None):
    defaults = {"unit": unit}
    if decimals is not None:
        defaults["decimals"] = decimals
    if color:
        defaults["color"] = color
    if thresholds:
        defaults["thresholds"] = thresholds
    if mappings:
        defaults["mappings"] = mappings
    if custom:
        defaults["custom"] = custom
    if defaults_extra:
        defaults.update(defaults_extra)
    ELEMENTS[key] = {
        "kind": "Panel",
        "spec": {
            "id": nid(),
            "title": title,
            "description": desc,
            "links": links or [],
            "data": {"kind": "QueryGroup", "spec": {
                "queries": queries,
                "transformations": transforms or [],
                "queryOptions": {},
            }},
            "vizConfig": {
                "kind": viz,  # v2alpha1: VizConfigKind.kind IS the panel plugin id
                "spec": {
                    "pluginVersion": PLUGIN_VER,
                    "options": options or {},
                    "fieldConfig": {"defaults": defaults, "overrides": overrides or []},
                },
            },
        },
    }
    return key

def gitem(key, x, y, w, h):
    return {"kind": "GridLayoutItem", "spec": {
        "x": x, "y": y, "width": w, "height": h,
        "element": {"kind": "ElementReference", "name": key},
    }}

def grid(items):
    return {"kind": "GridLayout", "spec": {"items": items}}

def tab(title, layout, cond=None):
    spec = {"title": title, "layout": layout}
    if cond:
        spec["conditionalRendering"] = cond
    return {"kind": "TabsLayoutTab", "spec": spec}

# Show the tab only when its panels' queries return data — i.e. only when Pyroscope profiling is
# enabled and shipping to staff stack. If PYROSCOPE_ENABLED is off, no profiles exist → the tab hides.
COND_HAS_DATA = {"kind": "ConditionalRenderingGroup", "spec": {
    "visibility": "show", "condition": "and",
    "items": [{"kind": "ConditionalRenderingData", "spec": {"value": True}}],
}}

# ---- reusable viz option/threshold helpers ----
TS = {"drawStyle": "line", "lineWidth": 1, "fillOpacity": 12, "showPoints": "never",
      "spanNulls": False, "lineInterpolation": "smooth"}
TS_STEP = {"drawStyle": "line", "lineWidth": 2, "fillOpacity": 6, "spanNulls": False,
           "lineInterpolation": "stepAfter"}
LEG = {"legend": {"displayMode": "list", "placement": "bottom", "showLegend": True},
       "tooltip": {"mode": "multi", "sort": "desc"}}
LEG_TBL = {"legend": {"displayMode": "table", "placement": "right", "showLegend": True,
                      "calcs": ["lastNotNull", "max"]},
           "tooltip": {"mode": "multi", "sort": "desc"}}
def stat_opts(graph="area", colormode="value", orient="auto", text="auto"):
    return {"reduceOptions": {"calcs": ["lastNotNull"], "values": False},
            "colorMode": colormode, "graphMode": graph, "textMode": text,
            "orientation": orient, "justifyMode": "auto"}
def thr(steps):
    return {"mode": "absolute", "steps": steps}
def fixed(c):
    return {"mode": "fixed", "fixedColor": c}
CLASSIC = {"mode": "palette-classic"}

# =====================================================================================
# TAB 1 — OVERVIEW (triage)
# =====================================================================================
panel("ov_fresh", "Telemetry freshness", "stat",
      [q("A", f'time() - max(timestamp(go_goroutine_count{JOB}))')],
      desc="Seconds since the generator's self-obs metrics last arrived in staff stack (15s emit interval). "
           "Green < 2 min. Red ⇒ the synthkit process is down or its OTLP export to staff stack is broken.",
      unit="s", decimals=0, color={"mode": "thresholds"},
      thresholds=thr([{"color": "green", "value": None}, {"color": "yellow", "value": 120},
                      {"color": "red", "value": 300}]),
      options=stat_opts(graph="none", colormode="background"))

panel("ov_goroutines", "Goroutines", "stat",
      [q("A", f'max(go_goroutine_count{JOB})')],
      desc="Live goroutine count. A steady climb = goroutine leak.",
      unit="short", decimals=0, color=fixed("blue"), options=stat_opts())

panel("ov_heap", "Memory in use", "stat",
      [q("A", f'sum(go_memory_used_bytes{JOB})')],
      desc="Total Go runtime memory in use (stack + other), summed.",
      unit="bytes", decimals=1, color=fixed("purple"), options=stat_opts())

panel("ov_ledger", "Request-ledger size", "stat",
      [q("A", f'max(synthkit_ledger_size{JOB})')],
      desc="Current total request-ledger size across all blueprints (entries retained for join). "
           "Unbounded growth = ledger not being trimmed.",
      unit="short", decimals=0, color=fixed("green"), options=stat_opts())

panel("ov_push_rate", "Push attempts/s", "stat",
      [q("A", f'sum(rate(synthkit_push_total{JOB}[$__rate_interval]))')],
      desc="Synthetic-data push attempts per second across all sinks.",
      unit="reqps", decimals=1, color=fixed("blue"), options=stat_opts())

panel("ov_push_err", "Push error ratio", "stat",
      [q("A", f'(sum(rate(synthkit_push_total{{job="synthkit", instance=~"$instance", outcome!="ok", outcome!="dry_run"}}[$__rate_interval])) or vector(0)) '
              f'/ clamp_min(sum(rate(synthkit_push_total{JOB}[$__rate_interval])), 0.0001)')],
      desc="Fraction of push attempts that failed (any non-ok, non-dry_run outcome). "
           "Healthy ≈ 0. Rising ⇒ a sink endpoint is rejecting (429/4xx/5xx) — see the Push tab.",
      unit="percentunit", decimals=2, color={"mode": "thresholds"},
      thresholds=thr([{"color": "green", "value": None}, {"color": "yellow", "value": 0.01},
                      {"color": "red", "value": 0.1}]),
      options=stat_opts(graph="area", colormode="background"))

panel("ov_tick_err", "Tick error ratio", "stat",
      [q("A", f'(sum(rate(synthkit_tick_total{{job="synthkit", instance=~"$instance", outcome="error"}}[$__rate_interval])) or vector(0)) '
              f'/ clamp_min(sum(rate(synthkit_tick_total{JOB}[$__rate_interval])), 0.0001)')],
      desc="Fraction of construct/workload instance ticks that returned an error.",
      unit="percentunit", decimals=2, color={"mode": "thresholds"},
      thresholds=thr([{"color": "green", "value": None}, {"color": "yellow", "value": 0.01},
                      {"color": "red", "value": 0.1}]),
      options=stat_opts(graph="area", colormode="background"))

panel("ov_bp_count", "Blueprints loaded", "stat",
      [q("A", f'max(synthkit_blueprint_count{JOB})')],
      desc="Number of blueprints currently loaded and ticking. Matches the blueprints/ directory the "
           "generator was started with.",
      unit="short", decimals=0, color=fixed("green"), options=stat_opts(graph="none"))

panel("ov_queue_depth", "Queue depth (max)", "stat",
      [q("A", f'max(synthkit_queue_depth{JOB})')],
      desc="Maximum enqueued-but-unflushed items across all delivery queue shards/sinks. "
           "Healthy ≈ 0. Sustained > 0 means delivery is falling behind generation.",
      unit="short", decimals=0, color={"mode": "thresholds"},
      thresholds=thr([{"color": "green", "value": None}, {"color": "yellow", "value": 50},
                      {"color": "red", "value": 500}]),
      options=stat_opts(graph="area", colormode="background"))

panel("ov_queue_blocked", "Backpressure events/s", "stat",
      [q("A", f'sum(rate(synthkit_queue_enqueue_blocked_total{JOB}[$__rate_interval]))')],
      desc="Tick-enqueue blocks/s across all sinks — a tick tried to enqueue but a queue shard was full. "
           "Healthy = 0. Any non-zero value means the host can't ship as fast as it generates.",
      unit="short", decimals=3, color={"mode": "thresholds"},
      thresholds=thr([{"color": "green", "value": None}, {"color": "red", "value": 0.001}]),
      options=stat_opts(graph="area", colormode="background"))

panel("ov_push_outcome_ts", "Push attempts/s by outcome", "timeseries",
      [q("A", f'sum by (outcome) (rate(synthkit_push_total{JOB}[$__rate_interval]))', legend="{{outcome}}")],
      desc="Stacked push rate split by outcome. ok / error / rate_limited / client_error / server_error / dry_run.",
      unit="reqps", color=CLASSIC,
      custom={**TS, "fillOpacity": 35, "stacking": {"mode": "normal", "group": "A"}}, options=LEG,
      overrides=[
          {"matcher": {"id": "byName", "options": "ok"}, "properties": [{"id": "color", "value": fixed("green")}]},
          {"matcher": {"id": "byName", "options": "error"}, "properties": [{"id": "color", "value": fixed("red")}]},
          {"matcher": {"id": "byName", "options": "server_error"}, "properties": [{"id": "color", "value": fixed("orange")}]},
          {"matcher": {"id": "byName", "options": "client_error"}, "properties": [{"id": "color", "value": fixed("super-light-orange")}]},
          {"matcher": {"id": "byName", "options": "rate_limited"}, "properties": [{"id": "color", "value": fixed("yellow")}]},
          {"matcher": {"id": "byName", "options": "dry_run"}, "properties": [{"id": "color", "value": fixed("blue")}]},
      ])

panel("ov_push_sink_ts", "Push attempts/s by sink", "timeseries",
      [q("A", f'sum by (sink) (rate(synthkit_push_total{JOB}[$__rate_interval]))', legend="{{sink}}")],
      desc="Push rate per sink (faro / loki / otlp / promrw).",
      unit="reqps", color=CLASSIC, custom=TS, options=LEG_TBL)

panel("ov_tick_outcome_ts", "Tick rate by outcome", "timeseries",
      [q("A", f'sum by (outcome) (rate(synthkit_tick_total{JOB}[$__rate_interval]))', legend="{{outcome}}")],
      desc="Construct/workload instance tick invocations/s by outcome.",
      unit="reqps", color=CLASSIC, custom={**TS, "fillOpacity": 25, "stacking": {"mode": "normal", "group": "A"}},
      options=LEG,
      overrides=[
          {"matcher": {"id": "byName", "options": "ok"}, "properties": [{"id": "color", "value": fixed("green")}]},
          {"matcher": {"id": "byName", "options": "error"}, "properties": [{"id": "color", "value": fixed("red")}]},
      ])

panel("ov_logs", "Recent warnings & errors", "logs",
      [q("A", '{service_name="synthkit"} | severity_text=~"WARN|ERROR"', ds=LOKI)],
      desc="Live tail of the generator's WARN/ERROR log lines from staff stack Loki. Empty = healthy.",
      options={"showTime": True, "showLabels": False, "wrapLogMessage": True, "prettifyLogMessage": False,
               "enableLogDetails": True, "dedupStrategy": "none", "sortOrder": "Descending"})

OVERVIEW = grid([
    # Two rows of 4 stats at w6 (was 8×w3 — titles were truncated). Row 1: liveness + load.
    gitem("ov_fresh", 0, 0, 6, 4), gitem("ov_goroutines", 6, 0, 6, 4),
    gitem("ov_heap", 12, 0, 6, 4), gitem("ov_ledger", 18, 0, 6, 4),
    # Row 2: pipeline health.
    gitem("ov_push_rate", 0, 4, 6, 4), gitem("ov_push_err", 6, 4, 6, 4),
    gitem("ov_tick_err", 12, 4, 6, 4), gitem("ov_bp_count", 18, 4, 6, 4),
    # Delivery-queue health (queue depth + backpressure; healthy both ≈ 0 post-decoupled queue)
    gitem("ov_queue_depth", 0, 8, 12, 4), gitem("ov_queue_blocked", 12, 8, 12, 4),
    gitem("ov_push_outcome_ts", 0, 12, 12, 8), gitem("ov_push_sink_ts", 12, 12, 12, 8),
    gitem("ov_tick_outcome_ts", 0, 20, 8, 8), gitem("ov_logs", 8, 20, 16, 8),
])

# =====================================================================================
# TAB 2 — PUSH PIPELINE (RED) — summary row + per-sink REPEATING row
# =====================================================================================
panel("pp_rate", "Total push rate", "timeseries",
      [q("A", f'sum by (sink) (rate(synthkit_push_total{JOB}[$__rate_interval]))', legend="{{sink}}")],
      desc="Push attempts/s per sink.", unit="reqps", color=CLASSIC, custom=TS, options=LEG_TBL)

panel("pp_errrate", "Push errors/s by sink & outcome", "timeseries",
      [q("A", f'sum by (sink, outcome) (rate(synthkit_push_total{{job="synthkit", instance=~"$instance", outcome!="ok", outcome!="dry_run"}}[$__rate_interval]))',
          legend="{{sink}} · {{outcome}}")],
      desc="Failed push attempts/s, split by sink and failure outcome.", unit="reqps",
      color=CLASSIC, custom={**TS, "fillOpacity": 25}, options=LEG)

panel("pp_blueprint", "Push attempts/s by blueprint", "timeseries",
      [q("A", sub_bp(f'sum by (blueprint) (rate(synthkit_push_total{JOB}[$__rate_interval]))'), legend="{{blueprint}}")],
      desc="Push rate attributed to each blueprint. The `blueprint` label is stamped on blueprint-scoped "
           "pushes; substrate-scoped pushes (k8s, dbo11y, CSP …) carry no blueprint and group under the "
           "empty series — that omission is by design (an absent dimension is never \"\").",
      unit="reqps", color=CLASSIC, custom=TS, options=LEG_TBL)

panel("pp_latency", "Average push latency by sink", "timeseries",
      [q("A", f'sum by (sink) (rate(synthkit_push_duration_seconds_sum{JOB}[$__rate_interval])) '
              f'/ sum by (sink) (rate(synthkit_push_duration_seconds_count{JOB}[$__rate_interval]))', legend="{{sink}}")],
      desc="Mean wall-clock per push, per sink (rate of _sum ÷ rate of _count). NOTE: the duration histogram "
           "uses OTel default buckets (smallest 5s) but real pushes are sub-second, so they all fall in one "
           "bucket and histogram_quantile is meaningless — the mean is the accurate measure. For true span "
           "quantiles see the Traces tab (Tempo native histogram).", unit="s",
      color=CLASSIC, custom=TS, options=LEG_TBL)

panel("pp_items", "Items pushed/s by sink", "timeseries",
      [q("A", f'sum by (sink) (rate(synthkit_push_items_total{JOB}[$__rate_interval]))', legend="{{sink}}")],
      desc="Logical items (series / log lines / spans / RUM beacons) pushed per second.", unit="cps",
      color=CLASSIC, custom=TS, options=LEG_TBL)

panel("pp_bytes", "Wire bytes pushed/s by sink", "timeseries",
      [q("A", f'sum by (sink) (rate(synthkit_push_bytes_total{JOB}[$__rate_interval]))', legend="{{sink}}")],
      desc="Wire bytes/s (sinks that build their own body — faro / loki / otlp; promrw is excluded by design ⇒ no series).",
      unit="Bps", color=CLASSIC, custom=TS, options=LEG_TBL)

panel("pp_items_blueprint", "Push items/s by blueprint", "timeseries",
      [q("A", sub_bp(f'sum by (blueprint) (rate(synthkit_push_items_total{JOB}[$__rate_interval]))'), legend="{{blueprint}}")],
      desc="Logical items (series / log lines / spans / RUM beacons) pushed per second, attributed to each blueprint. "
           "The `blueprint` label is omitted for substrate-scoped pushes (k8s, dbo11y, CSP …) — an absent dimension "
           "is never \"\".",
      unit="cps", color=CLASSIC, custom=TS, options=LEG_TBL)

panel("pp_bytes_blueprint", "Push bytes/s by blueprint", "timeseries",
      [q("A", sub_bp(f'sum by (blueprint) (rate(synthkit_push_bytes_total{JOB}[$__rate_interval]))'), legend="{{blueprint}}")],
      desc="Wire bytes/s attributed to each blueprint (faro / loki / otlp; promrw excluded by design). "
           "The `blueprint` label is omitted for substrate-scoped pushes — an absent dimension is never \"\".",
      unit="Bps", color=CLASSIC, custom=TS, options=LEG_TBL)

panel("pp_signal_type", "Push throughput by signal type", "timeseries",
      [q("A", f'sum by (signal_type) (rate(synthkit_push_items_total{JOB}[$__rate_interval]))', legend="{{signal_type}}")],
      desc="Logical items/s grouped by telemetry signal type (metrics / traces / logs / rum / profiles), derived "
           "deterministically from the sink. Complements the per-sink view — the generator's output mix by signal "
           "class. NEW: requires a build emitting the signal_type label on synthkit_push_items_total.",
      unit="cps", color=CLASSIC, custom={**TS, "fillOpacity": 20, "stacking": {"mode": "normal", "group": "A"}}, options=LEG_TBL)

# ---- Delivery-queue panels (decoupled in-memory queue, per-sink) ----
panel("dq_flush_lat", "Flush latency by sink (p50/p95)", "timeseries",
      [q("A", f'histogram_quantile(0.50, sum by (sink, le) (rate(synthkit_queue_flush_duration_seconds_bucket{JOB}[$__rate_interval])))', legend="p50 · {{sink}}"),
       q("B", f'histogram_quantile(0.95, sum by (sink, le) (rate(synthkit_queue_flush_duration_seconds_bucket{JOB}[$__rate_interval])))', legend="p95 · {{sink}}")],
      desc="Per-flush wall-clock of one delivery-queue batch ship, by sink. Uses FINE explicit histogram buckets "
           "(.005–10s), so quantiles ARE accurate here (unlike the coarse default-bucket push/cycle duration "
           "histograms). Rising p95 = a sink endpoint is slow — correlate with queue depth + backpressure. NEW.",
      unit="s", color=CLASSIC, custom=TS, options=LEG_TBL)

panel("dq_flush_batch", "Flush batch size by sink (avg)", "timeseries",
      [q("A", f'sum by (sink) (rate(synthkit_queue_flush_batch_sum{JOB}[$__rate_interval])) '
              f'/ clamp_min(sum by (sink) (rate(synthkit_queue_flush_batch_count{JOB}[$__rate_interval])), 0.0001)', legend="{{sink}}")],
      desc="Mean items shipped per flush, by sink. Small (→1) = deadline-driven flushes (partial batches, the "
           "queue is keeping up); near BatchMax = capacity-driven (throughput-bound). A sustained climb toward "
           "BatchMax with rising depth means delivery is falling behind generation. NEW.",
      unit="short", color=CLASSIC, custom=TS, options=LEG_TBL)

panel("dq_flush_err", "Flush error rate by sink", "timeseries",
      [q("A", f'sum by (sink) (rate(synthkit_queue_flush_total{{job="synthkit", instance=~"$instance", outcome="error"}}[$__rate_interval]))', legend="{{sink}}")],
      desc="Failed delivery-queue flushes/s by sink — the sink's flush() returned an error after its own retries, "
           "so a whole batch ship failed on the background sender. Healthy = 0. Complementary to the push-outcome "
           "RED (per-attempt): this is per-batch-ship on the decoupled queue. NEW.",
      unit="cps", color=CLASSIC, custom={**TS, "fillOpacity": 25}, options=LEG_TBL,
      thresholds=thr([{"color": "green", "value": None}, {"color": "red", "value": 0.001}]))

panel("dq_depth_ts", "Queue depth by sink", "timeseries",
      [q("A", f'sum by (sink) (synthkit_queue_depth{JOB})', legend="{{sink}}")],
      desc="Enqueued-but-unflushed items per sink delivery queue. Healthy ≈ 0. "
           "Sustained growth = delivery falling behind generation — raise the tick interval or lower the "
           "volume multiplier, or investigate sink latency on the Push tab.",
      unit="short", color=CLASSIC, custom={**TS, "fillOpacity": 20}, options=LEG_TBL,
      thresholds=thr([{"color": "green", "value": None}, {"color": "yellow", "value": 50},
                      {"color": "red", "value": 500}]))

panel("dq_blocked_ts", "Enqueue-blocked rate by sink", "timeseries",
      [q("A", f'sum by (sink) (rate(synthkit_queue_enqueue_blocked_total{JOB}[$__rate_interval]))', legend="{{sink}}")],
      desc="Tick-enqueue blocks/s per sink — increments each time a tick blocked trying to enqueue because "
           "a queue shard was full (backpressure). Healthy = 0. Any non-zero value means the host can't "
           "ship as fast as it generates for that sink.",
      unit="short", color=CLASSIC, custom={**TS, "fillOpacity": 25}, options=LEG_TBL,
      thresholds=thr([{"color": "green", "value": None}, {"color": "red", "value": 0.001}]))

panel("dq_depth_stat", "Queue depth (max, now)", "stat",
      [q("A", f'max(synthkit_queue_depth{JOB})')],
      desc="Maximum queue depth across all sinks right now. Healthy ≈ 0.",
      unit="short", decimals=0, color={"mode": "thresholds"},
      thresholds=thr([{"color": "green", "value": None}, {"color": "yellow", "value": 50},
                      {"color": "red", "value": 500}]),
      options=stat_opts(graph="area", colormode="background"))

panel("dq_blocked_stat", "Backpressure events/s (now)", "stat",
      [q("A", f'sum(rate(synthkit_queue_enqueue_blocked_total{JOB}[$__rate_interval]))')],
      desc="Total enqueue-blocked events/s across all sinks. Healthy = 0.",
      unit="short", decimals=3, color={"mode": "thresholds"},
      thresholds=thr([{"color": "green", "value": None}, {"color": "red", "value": 0.001}]),
      options=stat_opts(graph="area", colormode="background"))

# per-sink repeating row panels (filter by the repeat var $sink)
SINKF = '{job="synthkit", instance=~"$instance", sink=~"$sink"}'
panel("ps_rate", "$sink · push rate", "stat",
      [q("A", f'sum(rate(synthkit_push_total{SINKF}[$__rate_interval]))')],
      desc="Push attempts/s for this sink.", unit="reqps", decimals=2,
      color=fixed("blue"), options=stat_opts())
panel("ps_err", "$sink · error %", "stat",
      [q("A", f'(sum(rate(synthkit_push_total{{job="synthkit", instance=~"$instance", sink=~"$sink", outcome!="ok", outcome!="dry_run"}}[$__rate_interval])) or vector(0)) '
              f'/ clamp_min(sum(rate(synthkit_push_total{SINKF}[$__rate_interval])), 0.0001)')],
      desc="Error ratio for this sink.", unit="percentunit", decimals=2, color={"mode": "thresholds"},
      thresholds=thr([{"color": "green", "value": None}, {"color": "yellow", "value": 0.01}, {"color": "red", "value": 0.1}]),
      options=stat_opts(colormode="background"))
panel("ps_p95", "$sink · avg latency", "stat",
      [q("A", f'sum(rate(synthkit_push_duration_seconds_sum{SINKF}[$__rate_interval])) '
              f'/ clamp_min(sum(rate(synthkit_push_duration_seconds_count{SINKF}[$__rate_interval])), 0.0001)')],
      desc="Mean push wall-clock for this sink (the duration histogram's default buckets are too coarse for "
           "quantiles — see the summary-panel note).",
      unit="s", decimals=3, color=fixed("orange"), options=stat_opts())
panel("ps_lat_ts", "$sink · avg latency", "timeseries",
      [q("A", f'sum(rate(synthkit_push_duration_seconds_sum{SINKF}[$__rate_interval])) '
              f'/ clamp_min(sum(rate(synthkit_push_duration_seconds_count{SINKF}[$__rate_interval])), 0.0001)', legend="avg")],
      desc="Mean push latency for this sink over time.", unit="s", color=fixed("orange"), custom=TS, options=LEG)
panel("ps_io_ts", "$sink · items/s & bytes/s", "timeseries",
      [q("A", f'sum(rate(synthkit_push_items_total{SINKF}[$__rate_interval]))', legend="items/s"),
       q("B", f'sum(rate(synthkit_push_bytes_total{SINKF}[$__rate_interval]))', legend="bytes/s")],
      desc="Throughput for this sink. faro / loki / otlp report bytes (promrw is excluded by design ⇒ no bytes series).",
      unit="cps", color=CLASSIC, custom=TS, options=LEG,
      overrides=[{"matcher": {"id": "byName", "options": "bytes/s"},
                  "properties": [{"id": "unit", "value": "Bps"}, {"id": "custom.axisPlacement", "value": "right"}]}])

PUSH = {"kind": "RowsLayout", "spec": {"rows": [
    {"kind": "RowsLayoutRow", "spec": {"title": "Pipeline summary (all sinks)", "collapse": False,
        "layout": grid([
            gitem("pp_rate", 0, 0, 8, 7), gitem("pp_errrate", 8, 0, 8, 7), gitem("pp_blueprint", 16, 0, 8, 7),
            gitem("pp_latency", 0, 7, 8, 7), gitem("pp_items", 8, 7, 8, 7), gitem("pp_bytes", 16, 7, 8, 7),
            gitem("pp_items_blueprint", 0, 14, 12, 7), gitem("pp_bytes_blueprint", 12, 14, 12, 7),
            gitem("pp_signal_type", 0, 21, 24, 7),
        ])}},
    {"kind": "RowsLayoutRow", "spec": {"title": "Delivery queue (decoupled in-memory queue)", "collapse": False,
        "layout": grid([
            gitem("dq_depth_stat", 0, 0, 6, 5), gitem("dq_blocked_stat", 6, 0, 6, 5),
            gitem("dq_depth_ts", 12, 0, 12, 8),
            gitem("dq_blocked_ts", 0, 8, 12, 8),
            # Flush-level health (NEW): latency (fine buckets), batch size, per-batch flush errors.
            gitem("dq_flush_lat", 0, 16, 8, 8), gitem("dq_flush_batch", 8, 16, 8, 8), gitem("dq_flush_err", 16, 16, 8, 8),
        ])}},
    {"kind": "RowsLayoutRow", "spec": {"title": "Sink · $sink", "collapse": False,
        "repeat": {"mode": "variable", "value": "sink"},
        "layout": grid([
            gitem("ps_rate", 0, 0, 4, 6), gitem("ps_err", 4, 0, 4, 6), gitem("ps_p95", 8, 0, 4, 6),
            gitem("ps_lat_ts", 12, 0, 6, 6), gitem("ps_io_ts", 18, 0, 6, 6),
        ])}},
]}}

# =====================================================================================
# TAB 3 — CYCLE & INSTANCE HEALTH (per-blueprint cycle pacing + per-instance tick health)
# =====================================================================================
# Cycle-health row — the per-blueprint scheduler loop that DRIVES the per-instance ticks below.
# synthkit_cycle_duration_seconds (histogram, blueprint) + synthkit_dropped_ticks_total (counter,
# blueprint) come from runner.CycleFunc → selfobs.ObserveCycle. A dropped tick means a blueprint's
# previous cycle was still running when the next tick fired, so the runner skipped it to avoid
# pile-up — the headline "is the generator keeping up?" signal.
panel("cy_dropped_stat", "Dropped ticks (window)", "stat",
      [q("A", f'sum(increase(synthkit_dropped_ticks_total{JOB}[$__range]))')],
      desc="Total ticks dropped over the selected window (summed across blueprints). A drop = the prior "
           "cycle for that blueprint was still running when the next tick fired, so the runner skipped it. "
           "Healthy = 0. > 0 ⇒ the generator is not keeping up — raise the tick interval or lower the volume "
           "multiplier. Correlate with the cycle-duration panel.",
      unit="short", decimals=0, color={"mode": "thresholds"},
      thresholds=thr([{"color": "green", "value": None}, {"color": "yellow", "value": 1},
                      {"color": "red", "value": 10}]),
      options=stat_opts(graph="none", colormode="background"))

panel("cy_cycle_slow", "Slowest avg cycle", "stat",
      [q("A", f'max(sum by (blueprint) (rate(synthkit_cycle_duration_seconds_sum{JOB}[$__rate_interval])) '
              f'/ sum by (blueprint) (rate(synthkit_cycle_duration_seconds_count{JOB}[$__rate_interval])))')],
      desc="Mean full-cycle wall-clock of the slowest blueprint. Approaching the configured tick interval "
           "is the warning sign that precedes dropped ticks.",
      unit="s", decimals=3, color=fixed("orange"), options=stat_opts())

panel("cy_cycle_ts", "Avg cycle duration by blueprint", "timeseries",
      [q("A", f'sum by (blueprint) (rate(synthkit_cycle_duration_seconds_sum{JOB}[$__rate_interval])) '
              f'/ sum by (blueprint) (rate(synthkit_cycle_duration_seconds_count{JOB}[$__rate_interval]))',
          legend="{{blueprint}}")],
      desc="Mean wall-clock of one full blueprint cycle (rate of _sum ÷ rate of _count). NOTE: the cycle-duration "
           "histogram uses OTel default buckets, too coarse for quantiles — the mean is the accurate measure "
           "(same as the push/tick duration panels). Watch this trend toward the tick interval.",
      unit="s", color=CLASSIC, custom=TS, options=LEG_TBL)

panel("cy_dropped_ts", "Dropped ticks/s by blueprint", "timeseries",
      [q("A", f'sum by (blueprint) (rate(synthkit_dropped_ticks_total{JOB}[$__rate_interval]))', legend="{{blueprint}}")],
      desc="Dropped ticks per second, per blueprint. Any non-zero series ⇒ that blueprint's cycle is overrunning "
           "its tick interval. Empty = every cycle finished in time (healthy).",
      unit="cps", color=CLASSIC, custom={**TS, "fillOpacity": 25}, options=LEG_TBL,
      thresholds=thr([{"color": "green", "value": None}, {"color": "red", "value": 0.001}]))

panel("em_table", "Per-instance health", "table",
      [q("A", f'sum by (construct_instance, construct_kind, blueprint) (rate(synthkit_tick_total{JOB}[$__rate_interval]))', instant=True, fmt="table"),
       q("B", f'sum by (construct_instance, construct_kind, blueprint) (increase(synthkit_tick_total{{job="synthkit", instance=~"$instance", outcome="error"}}[$__range]))', instant=True, fmt="table"),
       q("C", f'sum by (construct_instance, construct_kind, blueprint) (rate(synthkit_tick_duration_seconds_sum{JOB}[$__rate_interval])) '
              f'/ sum by (construct_instance, construct_kind, blueprint) (rate(synthkit_tick_duration_seconds_count{JOB}[$__rate_interval]))', instant=True, fmt="table")],
      desc="One row per construct/workload instance: kind, blueprint, tick rate, total errors in the window, and mean tick "
           "duration. Sort by Errors to find the offender. NOTE: requires a generator build with the selfobs "
           "`construct_instance` rename — empty on older builds.",
      unit="short",
      transforms=[{"kind": "joinByField", "spec": {"id": "joinByField", "options": {"byField": "construct_instance", "mode": "outer"}}}],
      options={"showHeader": True, "cellHeight": "md", "sortBy": [{"displayName": "Errors (window)", "desc": True}]},
      defaults_extra={"custom": {"minWidth": 110, "filterable": True}},
      overrides=[
          {"matcher": {"id": "byName", "options": "Time"}, "properties": [{"id": "custom.hidden", "value": True}]},
          {"matcher": {"id": "byName", "options": "construct_instance"}, "properties": [{"id": "displayName", "value": "Instance"}, {"id": "custom.minWidth", "value": 260}]},
          {"matcher": {"id": "byName", "options": "construct_kind"}, "properties": [{"id": "displayName", "value": "Kind"}]},
          {"matcher": {"id": "byName", "options": "blueprint"}, "properties": [{"id": "displayName", "value": "Blueprint"}]},
          {"matcher": {"id": "byName", "options": "Value #A"}, "properties": [{"id": "displayName", "value": "Ticks/s"}, {"id": "decimals", "value": 3}, {"id": "unit", "value": "reqps"}]},
          {"matcher": {"id": "byName", "options": "Value #B"}, "properties": [{"id": "displayName", "value": "Errors (window)"}, {"id": "decimals", "value": 0},
                {"id": "custom.cellOptions", "value": {"type": "color-background", "mode": "gradient"}},
                {"id": "thresholds", "value": thr([{"color": "green", "value": None}, {"color": "red", "value": 1}])}]},
          {"matcher": {"id": "byName", "options": "Value #C"}, "properties": [{"id": "displayName", "value": "Avg dur"}, {"id": "unit", "value": "s"}, {"id": "decimals", "value": 4}]},
      ])

panel("em_rate_ts", "Tick rate by instance (top 15)", "timeseries",
      [q("A", f'topk(15, sum by (construct_instance) (rate(synthkit_tick_total{JOB}[$__rate_interval])))', legend="{{construct_instance}}")],
      desc="Tick invocations/s, top 15 instances by rate.", unit="reqps", color=CLASSIC, custom=TS, options=LEG_TBL)

panel("em_err_bar", "Tick errors by instance (in window)", "bargauge",
      [q("A", f'sort_desc(sum by (construct_instance) (increase(synthkit_tick_total{{job="synthkit", instance=~"$instance", outcome="error"}}[$__range])) > 0)',
          legend="{{construct_instance}}", instant=True)],
      desc="Total tick errors per instance over the selected window, worst first. Empty = all instances healthy.",
      unit="short", decimals=0,
      color={"mode": "thresholds"},
      thresholds=thr([{"color": "green", "value": None}, {"color": "red", "value": 1}]),
      options={"orientation": "horizontal", "displayMode": "gradient", "valueMode": "color",
               "reduceOptions": {"calcs": ["lastNotNull"], "values": False}})

panel("em_dur_ts", "Avg tick duration by instance (top 15)", "timeseries",
      [q("A", f'topk(15, sum by (construct_instance) (rate(synthkit_tick_duration_seconds_sum{JOB}[$__rate_interval])) '
              f'/ sum by (construct_instance) (rate(synthkit_tick_duration_seconds_count{JOB}[$__rate_interval])))', legend="{{construct_instance}}")],
      desc="Mean wall-clock per tick, top 15 slowest instances (histogram buckets too coarse for quantiles).",
      unit="s", color=CLASSIC, custom=TS, options=LEG_TBL)

panel("em_err_ts", "Tick error rate by instance", "timeseries",
      [q("A", f'sum by (construct_instance) (rate(synthkit_tick_total{{job="synthkit", instance=~"$instance", outcome="error"}}[$__rate_interval])) > 0',
          legend="{{construct_instance}}")],
      desc="Per-instance tick error rate over time (only erroring instances shown).", unit="reqps",
      color=CLASSIC, custom={**TS, "fillOpacity": 20}, options=LEG)

panel("em_rate_kind_ts", "Tick rate/s by construct kind", "timeseries",
      [q("A", f'sum by (construct_kind) (rate(synthkit_tick_total{JOB}[$__rate_interval]))', legend="{{construct_kind}}")],
      desc="Tick invocations per second, summed by construct kind. Shows which construct families are "
           "generating the most tick activity across all blueprints.",
      unit="reqps", color=CLASSIC, custom=TS, options=LEG_TBL)

panel("em_rate_bp_ts", "Tick rate/s by blueprint", "timeseries",
      [q("A", sub_bp(f'sum by (blueprint) (rate(synthkit_tick_total{JOB}[$__rate_interval]))'), legend="{{blueprint}}")],
      desc="Tick invocations per second, summed by blueprint. The `blueprint` label is omitted for "
           "substrate-scoped constructs (k8s, dbo11y, CSP …) — an absent dimension is never \"\".",
      unit="reqps", color=CLASSIC, custom=TS, options=LEG_TBL)

EMITTERS = grid([
    # Cycle-health row (per-blueprint scheduler pacing) — drives the per-instance ticks below.
    gitem("cy_dropped_stat", 0, 0, 4, 6), gitem("cy_cycle_slow", 4, 0, 4, 6),
    gitem("cy_cycle_ts", 8, 0, 8, 6), gitem("cy_dropped_ts", 16, 0, 8, 6),
    # Per-instance tick health — table gets the full row width (was w14, cramped) with auto-sized
    # columns; the per-instance error bargauge sits on its own full-width row below it.
    gitem("em_table", 0, 6, 24, 10),
    gitem("em_err_bar", 0, 16, 24, 6),
    gitem("em_rate_ts", 0, 22, 12, 8), gitem("em_dur_ts", 12, 22, 12, 8),
    gitem("em_err_ts", 0, 30, 24, 7),
    # Tick breakdown by construct kind and blueprint.
    gitem("em_rate_kind_ts", 0, 37, 12, 7), gitem("em_rate_bp_ts", 12, 37, 12, 7),
])

# =====================================================================================
# TAB 3b — CARDINALITY (synthkit_cardinality_series X-ray)
# =====================================================================================
# synthkit_cardinality_series{blueprint, construct_kind, construct_instance} — gauge tracking the
# distinct synthetic series each construct/workload instance has ever emitted (high-water count).
# The `blueprint` label is omitted for substrate-scoped constructs (absent dimension ≠ "").
panel("ca_total", "Total distinct series", "stat",
      [q("A", f'sum(synthkit_cardinality_series{JOB})')],
      desc="Total distinct synthetic series currently emitted across all constructs/workloads and blueprints. "
           "This is an X-ray high-water count from the internal bookkeeping; it reflects the current cardinality "
           "footprint synthkit puts on the target stack.",
      unit="short", decimals=0, color=fixed("blue"), options=stat_opts(graph="none", colormode="value"))

panel("ca_bp_ts", "Distinct series by blueprint", "timeseries",
      [q("A", sub_bp(f'sum by (blueprint) (synthkit_cardinality_series{JOB})'), legend="{{blueprint}}")],
      desc="Distinct synthetic series broken down by blueprint. Shows the relative cardinality contribution of "
           "each blueprint; substrate-scoped constructs (k8s, dbo11y, CSP …) group under the empty series — "
           "an absent blueprint dimension is never \"\".",
      unit="short", color=CLASSIC, custom={**TS, "fillOpacity": 15}, options=LEG_TBL)

panel("ca_topk_ts", "Top constructs by cardinality", "timeseries",
      [q("A", f'topk(15, sum by (construct_instance) (synthkit_cardinality_series{JOB}))', legend="{{construct_instance}}")],
      desc="Top 15 construct/workload instances by distinct series count. Use this to identify which instances "
           "contribute the most to the target stack's cardinality.",
      unit="short", color=CLASSIC, custom=TS, options=LEG_TBL)

panel("ca_kind_ts", "Cardinality by construct kind", "timeseries",
      [q("A", f'sum by (construct_kind) (synthkit_cardinality_series{JOB})', legend="{{construct_kind}}")],
      desc="Distinct synthetic series summed by construct kind. Reveals which construct families (e.g. k8s_cluster, "
           "ec2, rds, app …) carry the highest cardinality footprint.",
      unit="short", color=CLASSIC, custom={**TS, "fillOpacity": 15}, options=LEG_TBL)

CARDINALITY = grid([
    gitem("ca_total", 0, 0, 6, 4),
    gitem("ca_bp_ts", 6, 0, 18, 8),
    gitem("ca_topk_ts", 0, 8, 12, 8), gitem("ca_kind_ts", 12, 8, 12, 8),
])

# =====================================================================================
# TAB 4 — GO RUNTIME
# =====================================================================================
panel("rt_goroutines", "Goroutines", "timeseries",
      [q("A", f'max(go_goroutine_count{JOB})', legend="goroutines")],
      desc="Live goroutines. Sustained growth = leak.", unit="short", color=fixed("blue"),
      custom={**TS, "fillOpacity": 20}, options=LEG)

panel("rt_mem", "Memory in use by type + GC goal", "timeseries",
      [q("A", f'sum by (go_memory_type) (go_memory_used_bytes{JOB})', legend="used · {{go_memory_type}}"),
       q("B", f'max(go_memory_gc_goal_bytes{JOB})', legend="GC goal (heap target)")],
      desc="Go memory in use (stack / other) and the GC heap-size goal. Used approaching goal ⇒ GC about to run.",
      unit="bytes", color=CLASSIC, custom={**TS, "fillOpacity": 15}, options=LEG_TBL,
      overrides=[{"matcher": {"id": "byName", "options": "GC goal (heap target)"},
                  "properties": [{"id": "custom.fillOpacity", "value": 0}, {"id": "custom.lineStyle", "value": {"dash": [8, 4], "fill": "dash"}},
                                 {"id": "color", "value": fixed("red")}]}])

panel("rt_alloc_bytes", "Allocation rate (bytes/s)", "timeseries",
      [q("A", f'sum(rate(go_memory_allocated_bytes_total{JOB}[$__rate_interval]))', legend="alloc bytes/s")],
      desc="Heap bytes allocated per second (cumulative counter rate). Allocation pressure driving GC.",
      unit="Bps", color=fixed("purple"), custom={**TS, "fillOpacity": 20}, options=LEG)

panel("rt_alloc_count", "Allocation rate (objects/s)", "timeseries",
      [q("A", f'sum(rate(go_memory_allocations_total{JOB}[$__rate_interval]))', legend="allocs/s")],
      desc="Heap object allocations per second.", unit="ops", color=fixed("orange"),
      custom={**TS, "fillOpacity": 20}, options=LEG)

panel("rt_gomaxprocs", "GOMAXPROCS", "stat",
      [q("A", f'max(go_processor_limit{JOB})')],
      desc="go_processor_limit — CPUs the runtime may use.", unit="short", decimals=0,
      color=fixed("blue"), options=stat_opts(graph="none"))
panel("rt_gogc", "GOGC", "stat",
      [q("A", f'max(go_config_gogc_percent{JOB})')],
      desc="go_config_gogc_percent — GC target percentage (default 100).", unit="percent", decimals=0,
      color=fixed("green"), options=stat_opts(graph="none"))
panel("rt_goroutines_now", "Goroutines (now)", "stat",
      [q("A", f'max(go_goroutine_count{JOB})')],
      desc="Current goroutine count.", unit="short", decimals=0, color=fixed("blue"), options=stat_opts())
panel("rt_heap_now", "Memory in use (now)", "stat",
      [q("A", f'sum(go_memory_used_bytes{JOB})')],
      desc="Current total memory in use.", unit="bytes", decimals=1, color=fixed("purple"), options=stat_opts())

RUNTIME = grid([
    gitem("rt_gomaxprocs", 0, 0, 3, 4), gitem("rt_gogc", 3, 0, 3, 4),
    gitem("rt_goroutines_now", 6, 0, 3, 4), gitem("rt_heap_now", 9, 0, 3, 4),
    gitem("rt_goroutines", 12, 0, 12, 8),
    gitem("rt_mem", 0, 8, 12, 8), gitem("rt_alloc_bytes", 12, 8, 12, 8),
    gitem("rt_alloc_count", 0, 16, 12, 7),
])

# =====================================================================================
# TAB 4b — PROFILING (Pyroscope) — conditionally rendered (only when profiles exist)
# =====================================================================================
CPU_NS = "process_cpu:cpu:nanoseconds:cpu:nanoseconds"
INUSE = "memory:inuse_space:bytes:space:bytes"
ALLOC = "memory:alloc_space:bytes:space:bytes"
GORO = "goroutines:goroutine:count:goroutine:count"
# Contention + object-count profiles — collected when PYROSCOPE_MUTEX_FRACTION/BLOCK_RATE > 0
# (both =5 in .env). IDs verified live on the staff stack via `gcx profiles profile-types`.
MUTEX_DELAY = "mutex:delay:nanoseconds:contentions:count"
MUTEX_CNT = "mutex:contentions:count:contentions:count"
BLOCK_DELAY = "block:delay:nanoseconds:contentions:count"
BLOCK_CNT = "block:contentions:count:contentions:count"
ALLOC_OBJ = "memory:alloc_objects:count:space:bytes"
INUSE_OBJ = "memory:inuse_objects:count:space:bytes"

panel("pf_cpu_ts", "CPU profile (time)", "timeseries",
      [pq("A", CPU_NS, "metrics")],
      desc="CPU nanoseconds attributed per step from the continuous CPU profile (Pyroscope).",
      unit="ns", color=fixed("orange"), custom={**TS, "fillOpacity": 20}, options=LEG)
panel("pf_inuse_ts", "In-use heap (profile)", "timeseries",
      [pq("A", INUSE, "metrics")],
      desc="Live heap in use, from the inuse_space profile. Cross-check against go_memory_used_bytes on the Go runtime tab.",
      unit="bytes", color=fixed("purple"), custom={**TS, "fillOpacity": 20}, options=LEG)
panel("pf_alloc_ts", "Alloc space (profile)", "timeseries",
      [pq("A", ALLOC, "metrics")],
      desc="Bytes allocated per step from the alloc_space profile — allocation pressure (drives GC).",
      unit="bytes", color=fixed("blue"), custom={**TS, "fillOpacity": 20}, options=LEG)
panel("pf_goro_ts", "Goroutines (profile)", "timeseries",
      [pq("A", GORO, "metrics")],
      desc="Goroutine count from the goroutine profile. Should track go_goroutine_count on the Go runtime tab.",
      unit="short", color=fixed("green"), custom={**TS, "fillOpacity": 20}, options=LEG)

panel("pf_cpu_flame", "CPU flame graph", "flamegraph",
      [pq("A", CPU_NS, "profile", max_nodes=8192)],
      desc="Where CPU time is spent across the selected window (merged CPU profile). Widen the time range "
           "for a more representative graph.")
panel("pf_inuse_flame", "In-use space flame graph", "flamegraph",
      [pq("A", INUSE, "profile", max_nodes=8192)],
      desc="Call stacks holding live heap (inuse_space) — where retained memory is allocated.")
panel("pf_alloc_flame", "Alloc space flame graph", "flamegraph",
      [pq("A", ALLOC, "profile", max_nodes=8192)],
      desc="Call stacks driving allocations (alloc_space) — the GC/allocation hot paths.")
panel("pf_goro_flame", "Goroutines flame graph", "flamegraph",
      [pq("A", GORO, "profile", max_nodes=8192)],
      desc="Call stacks of live goroutines — useful for spotting goroutine leaks (which package is parked where).")

# Contention profiles — the prime suspects when a blueprint cycle overruns its tick interval
# (see the dropped-ticks panel on the Cycle & instance health tab). delay (ns) and contention count
# share a panel (count on the right axis). Mutex/block profiling is enabled by PYROSCOPE_MUTEX_FRACTION
# and PYROSCOPE_BLOCK_RATE; with both 0 these panels read empty.
RIGHT_CNT = [{"matcher": {"id": "byName", "options": "contentions"},
              "properties": [{"id": "unit", "value": "short"}, {"id": "custom.axisPlacement", "value": "right"}]}]
panel("pf_mutex_ts", "Mutex contention", "timeseries",
      [pq("A", MUTEX_DELAY, "metrics"), pq("B", MUTEX_CNT, "metrics")],
      desc="Lock-contention wait time (delay, ns) and contention count from the mutex profile. Sustained "
           "growth points at a hot lock serialising the cycle — confirm location in the flame graph.",
      unit="ns", color=CLASSIC, custom={**TS, "fillOpacity": 20}, options=LEG, overrides=RIGHT_CNT)
panel("pf_block_ts", "Goroutine block", "timeseries",
      [pq("A", BLOCK_DELAY, "metrics"), pq("B", BLOCK_CNT, "metrics")],
      desc="Time goroutines spent blocked (delay, ns) and block count from the block profile — channel/select, "
           "mutex, and network/IO waits. High block delay with low CPU = the cycle is waiting, not computing.",
      unit="ns", color=CLASSIC, custom={**TS, "fillOpacity": 20}, options=LEG, overrides=RIGHT_CNT)
panel("pf_alloc_obj_ts", "Alloc objects", "timeseries",
      [pq("A", ALLOC_OBJ, "metrics")],
      desc="Heap objects allocated (count) from the alloc_objects profile — allocation churn by object count, "
           "complementary to alloc_space (bytes). High count with modest bytes = many tiny allocations.",
      unit="short", color=fixed("blue"), custom={**TS, "fillOpacity": 20}, options=LEG)
panel("pf_inuse_obj_ts", "In-use objects", "timeseries",
      [pq("A", INUSE_OBJ, "metrics")],
      desc="Live heap objects (count) from the inuse_objects profile — object-count footprint, complementary to "
           "inuse_space (bytes). Unbounded growth = an object leak (cross-check goroutines + ledger size).",
      unit="short", color=fixed("purple"), custom={**TS, "fillOpacity": 20}, options=LEG)

panel("pf_mutex_flame", "Mutex contention flame graph", "flamegraph",
      [pq("A", MUTEX_DELAY, "profile", max_nodes=8192)],
      desc="Call stacks where goroutines wait on locks (mutex delay) — pinpoints the contended lock.")
panel("pf_block_flame", "Goroutine block flame graph", "flamegraph",
      [pq("A", BLOCK_DELAY, "profile", max_nodes=8192)],
      desc="Call stacks where goroutines block (block delay) — channel/select/IO waits serialising work.")
panel("pf_alloc_obj_flame", "Alloc objects flame graph", "flamegraph",
      [pq("A", ALLOC_OBJ, "profile", max_nodes=8192)],
      desc="Call stacks driving object allocations (by count) — the churn hot paths.")
panel("pf_inuse_obj_flame", "In-use objects flame graph", "flamegraph",
      [pq("A", INUSE_OBJ, "profile", max_nodes=8192)],
      desc="Call stacks holding live objects (by count) — where retained objects are allocated.")

PROFILING = grid([
    gitem("pf_cpu_ts", 0, 0, 6, 7), gitem("pf_inuse_ts", 6, 0, 6, 7),
    gitem("pf_alloc_ts", 12, 0, 6, 7), gitem("pf_goro_ts", 18, 0, 6, 7),
    gitem("pf_cpu_flame", 0, 7, 12, 16), gitem("pf_inuse_flame", 12, 7, 12, 16),
    gitem("pf_alloc_flame", 0, 23, 12, 16), gitem("pf_goro_flame", 12, 23, 12, 16),
    # Contention + object-count profiles (previously collected but undashboarded).
    gitem("pf_mutex_ts", 0, 39, 6, 7), gitem("pf_block_ts", 6, 39, 6, 7),
    gitem("pf_alloc_obj_ts", 12, 39, 6, 7), gitem("pf_inuse_obj_ts", 18, 39, 6, 7),
    gitem("pf_mutex_flame", 0, 46, 12, 16), gitem("pf_block_flame", 12, 46, 12, 16),
    gitem("pf_alloc_obj_flame", 0, 62, 12, 16), gitem("pf_inuse_obj_flame", 12, 62, 12, 16),
])

# =====================================================================================
# TAB 5 — TRACES & SPANS (spanmetrics from Tempo + Tempo search)
# =====================================================================================
SVC = '{service="synthkit"}'
panel("tr_calls", "Span calls/s by span (cycle · flush · push · fleet)", "timeseries",
      [q("A", f'sum by (span_name) (rate(traces_spanmetrics_calls_total{SVC}[$__rate_interval]))', legend="{{span_name}}")],
      desc="Spanmetrics call rate per span name, derived by Tempo from the self-obs traces. The metered spans "
           "are now the operational workflow: `cycle` (per-blueprint generation, CONSUMER), `flush <sink>` "
           "(delivery-queue batch ship — how the QUEUED sinks promrw/loki/otlp/pyroscope become visible, since "
           "the decoupled queue ships them off the traced tick path), `push faro` (synchronous RUM push, CLIENT) "
           "and `fleet <op>` (FM register/heartbeat, CLIENT). The tick root stays INTERNAL ⇒ not metered.",
      unit="reqps", color=CLASSIC, custom=TS, options=LEG_TBL)

panel("tr_errs", "Span error rate by span", "timeseries",
      [q("A", f'sum by (span_name) (rate(traces_spanmetrics_calls_total{{service="synthkit", status_code="STATUS_CODE_ERROR"}}[$__rate_interval]))',
          legend="{{span_name}}")],
      desc="Errored spans/s per span name (status_code=ERROR). Empty = no failing pushes.",
      unit="reqps", color=CLASSIC, custom={**TS, "fillOpacity": 25}, options=LEG)

panel("tr_lat", "Span latency p50/p95/p99 (all spans, blended)", "timeseries",
      [q("A", f'histogram_quantile(0.50, sum(rate(traces_spanmetrics_latency{SVC}[$__rate_interval])))', legend="p50"),
       q("B", f'histogram_quantile(0.95, sum(rate(traces_spanmetrics_latency{SVC}[$__rate_interval])))', legend="p95"),
       q("C", f'histogram_quantile(0.99, sum(rate(traces_spanmetrics_latency{SVC}[$__rate_interval])))', legend="p99")],
      desc="True span-duration quantiles from traces_spanmetrics_latency — a Tempo-generated NATIVE histogram "
           "(seconds), so quantiles are accurate here (unlike the coarse push-duration histogram on the Push tab).",
      unit="s", color=CLASSIC, custom=TS, options=LEG)

panel("tr_lat_byspan", "p95 span latency by span", "timeseries",
      [q("A", f'histogram_quantile(0.95, sum by (span_name) (rate(traces_spanmetrics_latency{SVC}[$__rate_interval])))', legend="{{span_name}}")],
      desc="p95 latency per span name (push faro / loki / otlp / promrw), from the Tempo native histogram.",
      unit="s", color=CLASSIC, custom=TS, options=LEG_TBL)

panel("tr_search", "Recent tick traces", "table",
      [q("A", '{ resource.service.name="synthkit" }', ds=TEMPO,
          extra={"queryType": "traceql", "limit": 30, "tableType": "traces"})],
      desc="Most recent self-obs traces (root span tick, with push <sink> children). "
           "Click a trace ID to open it in Tempo. Use the time picker to widen the window.",
      unit="short", options={"showHeader": True, "cellHeight": "sm"})

TRACES = grid([
    gitem("tr_calls", 0, 0, 12, 8), gitem("tr_errs", 12, 0, 12, 8),
    gitem("tr_lat", 0, 8, 12, 8), gitem("tr_lat_byspan", 12, 8, 12, 8),
    gitem("tr_search", 0, 16, 24, 10),
])

# =====================================================================================
# TAB 6 — LOGS (Loki operational stream)
# =====================================================================================
panel("lg_vol", "Log volume by severity", "timeseries",
      [q("A", 'sum(count_over_time({service_name="synthkit"} | severity_text="INFO" [$__auto]))', ds=LOKI, legend="INFO"),
       q("B", 'sum(count_over_time({service_name="synthkit"} | severity_text="WARN" [$__auto]))', ds=LOKI, legend="WARN"),
       q("C", 'sum(count_over_time({service_name="synthkit"} | severity_text="ERROR" [$__auto]))', ds=LOKI, legend="ERROR")],
      desc="Log lines per interval by severity, inferred from line content by the self-obs log bridge. "
           "severity_text is structured metadata (filtered via the | pipe); one explicit query per level since "
           "Loki does not propagate structured-metadata labels through sum by(). Drawn as stacked BARS: "
           "WARN/ERROR are sparse (often 1–2 lines/min), so bars stay legible and isolating a single severity "
           "in the legend renders its (scattered) bars rather than looking empty as stacked lines did.",
      unit="logs", color=CLASSIC,
      custom={"drawStyle": "bars", "lineWidth": 0, "fillOpacity": 80, "barAlignment": 0,
              "stacking": {"mode": "normal", "group": "A"}}, options=LEG,
      overrides=[
          {"matcher": {"id": "byName", "options": "ERROR"}, "properties": [{"id": "color", "value": fixed("red")}]},
          {"matcher": {"id": "byName", "options": "WARN"}, "properties": [{"id": "color", "value": fixed("yellow")}]},
          {"matcher": {"id": "byName", "options": "INFO"}, "properties": [{"id": "color", "value": fixed("green")}]},
      ])

panel("lg_err_rate", "Error log rate", "stat",
      [q("A", '(sum(rate({service_name="synthkit"} | severity_text="ERROR" [$__auto])) or vector(0))', ds=LOKI)],
      desc="Error log lines per second. > 0 ⇒ check the error stream below.", unit="logs", decimals=3,
      color={"mode": "thresholds"},
      thresholds=thr([{"color": "green", "value": None}, {"color": "yellow", "value": 0.001}, {"color": "red", "value": 0.05}]),
      options=stat_opts(graph="area", colormode="background"))

panel("lg_warn_rate", "Warn log rate", "stat",
      [q("A", '(sum(rate({service_name="synthkit"} | severity_text="WARN" [$__auto])) or vector(0))', ds=LOKI)],
      desc="Warning log lines per second.", unit="logs", decimals=3, color=fixed("yellow"),
      options=stat_opts(graph="area"))

panel("lg_errors", "Error stream", "logs",
      [q("A", '{service_name="synthkit"} | severity_text="ERROR"', ds=LOKI)],
      desc="ERROR-level lines only. Empty = healthy.",
      options={"showTime": True, "showLabels": True, "wrapLogMessage": True, "enableLogDetails": True,
               "dedupStrategy": "none", "sortOrder": "Descending"})

# The runner logs tick errors as FREE TEXT (`runner: blueprint "X" construct "Y": <err>`), not
# structured. LogQL parses the structure at QUERY time: this regexp pulls blueprint / kind / unit
# out of all three runner-error shapes (construct, workload, master tick) so we can dashboard them
# without touching the runner's stdlib-only seam. Brittle to a format change in runner.go — but zero
# code churn and works on the logs already in Loki. (Failure phrases like "context deadline exceeded"
# now also classify ERROR — the selfobs severity heuristic was widened — so they ALSO appear in the
# Error stream; this stays the parsed, per-blueprint/unit view.)
RUNNER_RE = (r'| regexp "runner: blueprint \"(?P<blueprint>[^\"]+)\" '
             r'(?:(?P<kind>construct|workload) \"(?P<unit>[^\"]+)\"|(?P<kind2>master tick)):"')

panel("lg_runner_ts", "Runner tick errors/s by blueprint · unit", "timeseries",
      [q("A", 'sum by (blueprint, unit) (count_over_time({service_name="synthkit"} '
              '|= "runner: blueprint" ' + RUNNER_RE + ' [$__auto]))', ds=LOKI, legend="{{blueprint}} · {{unit}}")],
      desc="Per-construct/workload tick errors the runner logged, parsed from free text at query time "
           "(blueprint + unit extracted via LogQL regexp). Complements the structured Push-failures panel: "
           "that is sink-level push errors, this is every runner-level tick error (construct logic, workload, "
           "master tick) broken down by blueprint·unit. Master-tick lines show with an empty unit.",
      unit="logs", color=CLASSIC, custom={**TS, "fillOpacity": 25}, options=LEG_TBL)

panel("lg_pushfail", "Push failures (structured)", "logs",
      [q("A", '{service_name="synthkit"} | event="push_error"', ds=LOKI)],
      desc="Failed synthetic pushes as STRUCTURED logs (event=push_error) from selfobs.PushObserver — the only "
           "place a push error's reason reaches the log stream (otherwise it lives only in the metric + trace "
           "span). Structured metadata carries sink / blueprint / outcome / http.status_code, so you can filter "
           "to one sink or blueprint (expand a line → Fields). rate_limited is WARN; other failures are ERROR. "
           "Substrate-scoped pushes carry no blueprint (omitted by design). Empty = no push failures.",
      options={"showTime": True, "showLabels": True, "wrapLogMessage": True, "enableLogDetails": True,
               "dedupStrategy": "none", "sortOrder": "Descending"})

panel("lg_runner", "Runner tick errors", "logs",
      [q("A", '{service_name="synthkit"} |= "runner: blueprint" ' + RUNNER_RE, ds=LOKI)],
      desc="The runner's per-tick error lines, with blueprint / kind / unit parsed into fields (expand a line → "
           "Fields to filter by blueprint or construct). This is the panel to watch for tick-level failures, "
           "broken down by the construct/workload that failed. Empty = no construct/workload/master-tick errors.",
      options={"showTime": True, "showLabels": True, "wrapLogMessage": True, "enableLogDetails": True,
               "dedupStrategy": "none", "sortOrder": "Descending"})

panel("lg_all", "Full operational stream", "logs",
      [q("A", '{service_name="synthkit"}', ds=LOKI)],
      desc="All self-obs log lines (scope synthkit/selfobs). Includes startup, heartbeat, per-push info "
           "(dry-run summaries when DRY_RUN=true), and errors.",
      options={"showTime": True, "showLabels": False, "wrapLogMessage": True, "enableLogDetails": True,
               "dedupStrategy": "none", "sortOrder": "Descending"})

LOGS = grid([
    gitem("lg_vol", 0, 0, 16, 7), gitem("lg_err_rate", 16, 0, 4, 7), gitem("lg_warn_rate", 20, 0, 4, 7),
    gitem("lg_runner_ts", 0, 7, 24, 7),
    gitem("lg_errors", 0, 14, 24, 8), gitem("lg_pushfail", 0, 22, 24, 8),
    gitem("lg_runner", 0, 30, 24, 9), gitem("lg_all", 0, 39, 24, 10),
])

# =====================================================================================
# TAB 7 — CONFIG & VOLUME
# =====================================================================================
panel("cf_build", "Build / identity", "table",
      [q("A", f'target_info{{service_name="synthkit", instance=~"$instance"}}', instant=True, fmt="table")],
      desc="Resource identity of the running generator: version, env tag, instance, telemetry_kind.",
      unit="short", options={"showHeader": True, "cellHeight": "sm"},
      overrides=[
          {"matcher": {"id": "byName", "options": "Time"}, "properties": [{"id": "custom.hidden", "value": True}]},
          {"matcher": {"id": "byName", "options": "Value"}, "properties": [{"id": "custom.hidden", "value": True}]},
          {"matcher": {"id": "byName", "options": "__name__"}, "properties": [{"id": "custom.hidden", "value": True}]},
          {"matcher": {"id": "byName", "options": "job"}, "properties": [{"id": "custom.hidden", "value": True}]},
      ])

panel("cf_mult", "Volume multiplier", "stat",
      [q("A", f'max(synthkit_volume_multiplier{JOB})')],
      desc="Control-plane master volume multiplier — scales synthetic output across all blueprints. "
           "Driven live from the operator UI / control-state.json.",
      unit="short", decimals=2, color=fixed("blue"), options=stat_opts(graph="none", colormode="value"))
panel("cf_bp", "Blueprints loaded", "stat",
      [q("A", f'max(synthkit_blueprint_count{JOB})')],
      desc="Number of loaded blueprints.", unit="short", decimals=0, color=fixed("green"),
      options=stat_opts(graph="none", colormode="value"))
panel("cf_ledger", "Request-ledger size", "stat",
      [q("A", f'max(synthkit_ledger_size{JOB})')],
      desc="Total request-ledger size across all blueprints (now).", unit="short", decimals=0,
      color=fixed("purple"), options=stat_opts(graph="none", colormode="value"))

panel("cf_mult_ts", "Volume multiplier over time", "timeseries",
      [q("A", f'max(synthkit_volume_multiplier{JOB})', legend="multiplier")],
      desc="Live master volume multiplier over time — steps mark a re-configuration of the generator's load shape.",
      unit="short", color=fixed("blue"), custom=TS_STEP, options=LEG)

panel("cf_ledger_ts", "Request-ledger size over time", "timeseries",
      [q("A", f'max(synthkit_ledger_size{JOB})', legend="ledger size")],
      desc="Correlation request-ledger size. Should plateau; unbounded growth = trim not running.",
      unit="short", color=fixed("green"), custom={**TS, "fillOpacity": 20}, options=LEG)

panel("cf_note", "About this dashboard", "text",
      [], desc="",
      options={"mode": "markdown", "content":
        "## synthkit · Self-Observability\n\n"
        "Internal monitoring of the **synthkit generator process itself** (`service.name=synthkit`), "
        "instrumented by `internal/selfobs` + `internal/profiling` and shipped to the **staff** stack over "
        "OTLP/Pyroscope (creds `GC_SELF_OTLP_*` / `GC_PYROSCOPE_*`) — wholly separate from the synthetic "
        "telemetry synthkit produces for the target stack.\n\n"
        "| Signal | Source |\n|---|---|\n"
        "| `synthkit_push_*` | RED on the synthetic-push pipeline (by sink / **signal_type** / blueprint / outcome) |\n"
        "| `synthkit_queue_flush_*` | per-flush latency (fine buckets) / batch size / flush-error count by sink — **Push** tab |\n"
        "| `event=\"config_change\"` | control-plane mutation audit (volume / failures / enablement) — **Config** tab |\n"
        "| `target_info{run_mode}` | live vs dry_run process identity (dashboards exclude dry_run) |\n"
        "| `synthkit_tick_*` | per construct/workload instance tick health (`construct_instance`, `construct_kind`, `blueprint`) |\n"
        "| `synthkit_cardinality_series` | distinct synthetic series per construct/workload instance (X-ray cardinality bookkeeping) — **Cardinality** tab |\n"
        "| `synthkit_cycle_duration_seconds`, `synthkit_dropped_ticks_total` | per-blueprint cycle pacing — is the generator keeping up? (`blueprint`) |\n"
        "| `synthkit_queue_depth` | enqueued-but-unflushed items per sink delivery queue (`sink`); gauge; healthy ≈ 0 |\n"
        "| `synthkit_queue_enqueue_blocked_total` | tick-enqueue blocks/s per sink — backpressure counter; healthy = 0 |\n"
        "| `synthkit_ledger_size`, `synthkit_volume_multiplier`, `synthkit_blueprint_count` | live process state (observable gauges) |\n"
        "| `go_*` | Go runtime (OTel runtime instrumentation) |\n"
        "| `traces_spanmetrics_*` | Tempo-derived RED on the `push <sink>` spans |\n"
        "| Loki `{service_name=\"synthkit\"}` | operational log stream (scope `synthkit/selfobs`) |\n"
        "| Loki `event=\"push_error\"` | structured push failures (sink/blueprint/outcome/status) — **Logs** tab |\n"
        "| Tempo `tick` | per-tick traces, `push <sink>` children |\n"
        "| Pyroscope `service_name=\"synthkit\"` | continuous profiles (`internal/profiling`) — **Profiling** tab |\n\n"
        "The **Profiling** tab is *conditionally rendered*: it appears only when Pyroscope profiles are "
        "present (`PYROSCOPE_ENABLED=true` and shipping to staff stack); with profiling off it hides.\n\n"
        "**Note:** the **Cycle & instance health** tab needs a generator build carrying the selfobs "
        "`construct_instance` label (the legacy `instance` datapoint attribute collided with the Prometheus "
        "target `instance` label and was dropped); it reads empty on older builds.\n\n"
        "The **Cardinality** tab uses `synthkit_cardinality_series{blueprint, construct_kind, construct_instance}` "
        "— a gauge tracking the distinct synthetic series each instance has ever emitted. "
        "The `blueprint` label is omitted for substrate-scoped constructs (absent dimension ≠ \"\")."}
)

panel("cf_changes", "Config-change audit (control-plane mutations)", "logs",
      [q("A", '{service_name="synthkit"} | event="config_change"', ds=LOKI)],
      desc="Operator control-plane mutations as STRUCTURED logs (event=config_change) from selfobs.EmitEvent — "
           "every successful POST to /control/* (volume / failure injection / blueprint+construct+kind enablement "
           "/ scenarios / scaling) emits one line carrying the new knob state. Structured metadata: "
           "volume_multiplier, failures_active, disabled_blueprints/constructs/kinds, active_scenarios, "
           "span_metrics_bps (expand a line → Fields). Empty = no control changes in the window. NEW.",
      options={"showTime": True, "showLabels": True, "wrapLogMessage": True, "enableLogDetails": True,
               "dedupStrategy": "none", "sortOrder": "Descending"})

CONFIG = grid([
    gitem("cf_mult", 0, 0, 6, 4), gitem("cf_bp", 6, 0, 6, 4), gitem("cf_ledger", 12, 0, 6, 4),
    gitem("cf_build", 0, 4, 12, 7), gitem("cf_mult_ts", 12, 4, 12, 7),
    gitem("cf_ledger_ts", 0, 11, 12, 7), gitem("cf_note", 12, 11, 12, 7),
    gitem("cf_changes", 0, 18, 24, 9),
])

# =====================================================================================
# VARIABLES
# =====================================================================================
def qvar(name, label, query, multi=True):
    cur = {"text": ["All"], "value": ["$__all"]} if multi else {"text": "All", "value": "$__all"}
    spec = {
        "name": name, "label": label, "hide": "dontHide", "skipUrlSync": False,
        "multi": multi, "includeAll": True, "allValue": ".*", "current": cur,
        "options": [], "refresh": "load", "sort": "alphabetical",
        "definition": query,
        "datasource": {"type": PROM[1], "uid": PROM[0]},
        "query": {"kind": PROM[1], "spec": {"qryType": 1, "query": query, "refId": name}},
    }
    return {"kind": "QueryVariable", "spec": spec}

VARIABLES = [
    # Exclude explicit dry-run processes from the instance picker (run_mode is stamped by
    # internal/selfobs; absent on pre-run_mode builds, which `!=` still matches — tolerant). A
    # gated build never emits selfobs under DRY_RUN, so this is belt-and-suspenders for any
    # process that bypasses the gate.
    qvar("instance", "Instance", "label_values(target_info{service_name=\"synthkit\", run_mode!=\"dry_run\"}, instance)"),
    qvar("sink", "Sink", "label_values(synthkit_push_total, sink)"),
    qvar("blueprint", "Blueprint", "label_values(synthkit_push_total, blueprint)"),
    qvar("construct_instance", "Construct instance", "label_values(synthkit_tick_total, construct_instance)"),
]

# =====================================================================================
# ASSEMBLE
# =====================================================================================
dashboard = {
    "apiVersion": "dashboard.grafana.app/v2alpha1",
    "kind": "Dashboard",
    "metadata": {"name": DASH_NAME, "annotations": {"grafana.app/folder": FOLDER_UID}},
    "spec": {
        "title": "synthkit · Self-Observability",
        "description": "Internal self-monitoring of the synthkit generator process (service.name=synthkit) "
                       "on the staff stack — push-pipeline RED, per-construct-instance tick health, Go runtime, "
                       "Pyroscope profiling (conditional tab), self-obs traces/spanmetrics, and the operational "
                       "log stream. Source: internal/selfobs + internal/profiling.",
        "tags": ["synthkit", "self-obs", "internal"],
        "editable": True,
        "liveNow": False,
        "preload": False,
        "cursorSync": "Crosshair",
        "timeSettings": {"from": "now-3h", "to": "now", "autoRefresh": "5s", "timezone": "browser",
                         "hideTimepicker": False, "fiscalYearStartMonth": 0},
        "variables": VARIABLES,
        "elements": ELEMENTS,
        "links": [],
        "layout": {"kind": "TabsLayout", "spec": {"tabs": [
            tab("Overview", OVERVIEW),
            tab("Push pipeline (RED)", PUSH),
            tab("Cycle & instance health", EMITTERS),
            tab("Cardinality", CARDINALITY),
            tab("Go runtime", RUNTIME),
            tab("Profiling", PROFILING, cond=COND_HAS_DATA),
            tab("Traces & spans", TRACES),
            tab("Logs", LOGS),
            tab("Config & volume", CONFIG),
        ]}},
    },
}

if __name__ == "__main__":
    import os
    out = os.path.join(os.path.dirname(os.path.abspath(__file__)), "synthkit-selfobs.json")
    with open(out, "w") as f:
        json.dump(dashboard, f, indent=2)
    print(f"wrote {out} — {len(ELEMENTS)} panels, {len(dashboard['spec']['layout']['spec']['tabs'])} tabs")
