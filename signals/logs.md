# Logs (→ Loki)

Log emission from synthkit covers blueprint-scoped app logs, Faro/RUM frontend beacons (via the
Faro collector), user-actions golden-thread events, and browser spans (via OTLP to Tempo). Substrate
log sources (SM, dbo11y, CSP) are pointer stubs to their own signal files. Global cardinality and
content-strip rules: see [`00-canon.md`](00-canon.md) `[slug: cardinality]`, `[slug: golden-thread]`,
`[slug: content-strip]`.

> `session_id` is ALWAYS a body/metadata field — never a stream label or structured-metadata key (T12).

---

## App logs — ScopeBlueprint `[slug: logs-app]`
*Provenance: predecessor SIGNALS §4.1 row 'app' + `emit/app_logs.go`.*

**Stream labels (low-cardinality — the Loki index key):** `env`, `service_name` (⚠ standardized on
`service_name` not `service` so ONE Tempo trace→logs mapping `service.name→service_name` covers every
stream), `level` ∈ {info,warn,error}, `source="app"`, `cluster`, `job`={service_namespace}/{service_name}.

**Structured metadata (high-cardinality — NOT stream labels):** `trace_id` (32-hex), `span_id`
(16-hex where available), `correlation_id`, `request_id`, `session_id`. ⚠ The log field key stays
`correlation_id` (unchanged) — the span attribute is `app.correlation_id` (vendor-neutral; OTLP side only,
2026-06-23). Correlation log lines additionally carry a `traceparent` field
(W3C trace-context; 2026-06-23).

**Body shapes (JSON, content-stripped).** Backend event types:
- Request lifecycle (one per active request): `{"msg":"request completed", "status":"success|
  client_error|server_error|throttled", "phase":"{phase}", "latency_ms":N, "outcome":"success"}`.
- k8s pod logs stream (separate): ✅ **live-verified (staff stack, modern k8s-monitoring/Alloy — SK-20):**
  pod/container log streams carry **NO `source` label** (the old `source="k8s"` was wrong; `source` is
  reserved for the `journal` and `kubernetes-events` lanes). Real stream labels are **OTel-resource
  style**: `{k8s_cluster_name, k8s_namespace_name, k8s_pod_name, k8s_container_name,
  k8s_{deployment,daemonset,statefulset,job}_name, service_name, service_namespace, service_instance_id,
  cluster, k8s_node_name, log_iostream∈{stdout,stderr}, logtag}` (+ derived `detected_level`) — NOT the
  older `{namespace, pod, container}` promtail style, and no `job` label observed. Body format is
  **mixed per-workload**: modern apps emit JSON (`{"level","msg","ts"}`), infra components emit plain
  klog/log4j text. ⚠ this label style is k8s-monitoring-config-dependent (otel-resource vs promtail);
  captured = the modern default.

> ⚠ `session_id` is always a body/metadata field, never a stream label (T12). No content-bearing
> field in any body (I23). SK-20 resolved (above); pod-log label style is config-dependent.

```yaml signals
source: app_logs
scope: blueprint
sink: loki
stream_labels:
  env: <env>
  service_name: <service>
  level: info|warn|error
  source: app
  cluster: <cluster>
  job: <service_namespace>/<service_name>
structured_metadata:
  - trace_id       # 32-hex
  - span_id        # 16-hex, where available
  - correlation_id  # log field key unchanged; span attr is app.correlation_id (OTLP-side; 2026-06-23)
  - request_id
body_fields_note: "correlation log lines also carry traceparent (W3C trace-context; 2026-06-23)"
body_fields:
  - session_id     # ALWAYS body, never label (T12)
  - msg
  - status         # success|client_error|server_error|throttled
  - phase
  - latency_ms
  - outcome
```

---

## Synthetic Monitoring logs — ScopeSubstrate `[slug: logs-sm]`

Cross-reference stub — SM log signals are documented in [`signals/sm.md`](sm.md) (see §4.2 of the
pre-split source; SM logs are substrate-scoped and carry no blueprint label).

---

## dbo11y / CSP logs — ScopeSubstrate `[slug: logs-dbo11y-csp]`

Cross-reference stub — dbo11y log signals are documented in [`signals/dbo11y.md`](dbo11y.md)
(`[slug: dbo11ymysql-logs]`, `[slug: dbo11ypg-logs]`); CSP log signals are documented in
[`signals/cspazure.md`](cspazure.md) and [`signals/cspgcp.md`](cspgcp.md); these are
substrate-scoped and carry no blueprint label.

---

## Faro / RUM (Frontend Observability) — collector-scoped (NOT blueprint-labeled) `[slug: logs-faro-rum]`
*Provenance: predecessor SIGNALS §4.2 + `research/faro-rum.md` (empirical) + `emit/rum.go`, `sink/faro.go`.
**Live-validated** against the Grafana Frontend Observability (Faro) product stack (Rob) — `v: ok`.
⚠ **Scope correction (2026-06-16):** RUM beacons POST to the Grafana Faro COLLECTOR, which OWNS the
Loki label mapping → the stream carries ONLY the 6 collector labels (app_id/app_key/
deployment_environment/kind/service_name/service_namespace) and **NO `blueprint` label** (synthkit's
Loki scoped-writer is bypassed, so it cannot stamp one). RUM is therefore disambiguated by the Faro app
identity (`app_key` + `service_name` + `deployment_environment`), NOT a blueprint selector — dashboards
must select Faro streams by `service_name`/`app_key`, never `blueprint=`. (Cross-blueprint coexistence ⇒
each blueprint needs a distinct Faro app_key + frontend service_name.)
**Web-vitals are Loki measurement log events + traces ONLY — NEVER Prometheus metrics** (confirmed via
live capture, `{service_name="frontend"} |= "type=web-vitals"`; SK-56/57/58 RESOLVED). synthkit's
`rum_faro.go` previously declared fabricated `largest_contentful_paint`/`cumulative_layout_shift`/
`interaction_to_next_paint` GAUGES — a realism divergence flagged for removal (emit the vitals as
`type=web-vitals` measurement lines instead, per the body below).*

> **⚠ Collector requirement (load-bearing).** Beacons MUST POST to the Faro collector
> `https://faro-collector-prod-{region}.grafana.net/collect/{app-key}`, NOT direct Loki. The
> collector is the sole writer of the `AppConfig firstReceivedDataAt`/`lastReceivedDataAt` lifecycle
> timestamps; the FEO app-list UI gates on `firstReceivedDataAt != nil`. The `X-Faro-Session-Id`
> header (from `Meta.Session.ID`) is REQUIRED — beacons without it are rejected HTTP 400. The contract
> below describes collector OUTPUT (what lands in Loki).

**Loki stream labels (uniform for ALL kinds — only 6):** `app_id` (numeric string), `app_key`,
`deployment_environment`, `kind` ∈ {measurement, event, exception, log}, `service_name`,
`service_namespace`.

**Structured metadata:** `app` (all kinds), `detected_level` ∈ {info,error} (collector-set),
`service_version`, `stack_id`; `trace_id`/`span_id` (traced events only); `hash`/`value_template`
(kind=exception only). ⚠ `session_id` is always a body field, never a label or metadata.

**Body — logfmt** (first field `timestamp=<ISO8601>`): `sdk_name=faro-web`, `sdk_version=2.x`,
`app_name`, `app_namespace`, `app_version`, `app_environment`, `session_id={uuid}`, `page_id`,
`page_url`, `view_name`, `browser_{name,version,os,mobile,language,viewportWidth,viewportHeight}`,
`os_{name,version}`.

- **kind=measurement, type=web-vitals** (one logfmt line per vital; each metric as a plain key AND a
  `value_*` full-precision twin — the COLLECTOR derives the `value_*` twin from the posted `Values` key,
  so synth posts only the plain key). Sub-fields below are from a **live Faro 2.7.0 capture** —
  corrected 2026-06-16 from earlier doc-sourced names: **LCP** (`lcp`/`value_lcp` + `element_render_delay`,
  `resource_load_delay`, `resource_load_duration`, `time_to_first_byte`; ctx `context_element` = CSS selector),
  **CLS** (`cls`/`value_cls`; `largest_shift_time`/`largest_shift_value` only when cls>0), **INP**
  (`inp`/`value_inp` + `input_delay`, `interaction_time`, `next_paint_time`; ctx `context_interaction_target`
  = CSS selector, `context_interaction_type` e.g. pointer, `context_load_state`), **TTFB** (`ttfb`/`value_ttfb`
  + `request_duration`, `waiting_duration`), **FCP** (`fcp`/`value_fcp` + `first_byte_to_fcp`,
  `time_to_first_byte`; ctx `context_load_state`). Shared: `delta`/`value_delta`,
  `context_rating` ∈ {good,needs-improvement,poor}, `context_id`, `context_load_state` (e.g.
  `dom-content-loaded`), `context_navigation_entry_id`,
  `context_navigation_type` ∈ {navigate,reload,back-forward,prerender}. ⚠ **No FID** — deprecated; Faro 2.x
  emits INP instead. (Live capture 2026-06-16 confirmed this envelope on the `frontend` service stream.)
- **kind=event** (`event_name` enum): `session_start` (drives FEO Sessions), `faro.user.action`
  (parent — see [User-actions golden-thread lane](#user-actions-golden-thread-lane-slug-logs-user-actions)),
  `faro.tracing.fetch` (traced; +`event_data_http.*`, `traceID`/`spanID` body fields),
  `faro.performance.resource`, `navigation`, `view_changed` (+`event_data_fromView`,`_toView`).
  `event_domain` ∈ {session,browser}.
- **kind=exception:** `type` (e.g. ConnectError), `value` (generic message — no content), `stacktrace`
  (structural frames only); metadata `hash`, `value_template`.

```yaml signals
source: faro_rum
scope: blueprint
sink: faro
stream_labels:
  app_id: <numeric-string>
  app_key: <app-key>
  deployment_environment: <env>    # Faro COLLECTOR label — the legacy key form; not a synthkit promrw emit
  kind: measurement|event|exception|log
  service_name: <service>
  service_namespace: <namespace>
structured_metadata:
  - app
  - detected_level    # info|error, collector-set
  - service_version
  - stack_id
  - trace_id          # traced events only
  - span_id           # traced events only
  - hash              # kind=exception only
  - value_template    # kind=exception only
body_fields:
  - timestamp         # ISO8601, first field
  - sdk_name          # faro-web
  - sdk_version       # 2.x
  - app_name
  - app_namespace
  - app_version
  - app_environment
  - session_id        # ALWAYS body, never label or metadata (T12)
  - page_id
  - page_url
  - view_name
  - browser_name
  - browser_version
  - browser_os
  - browser_mobile
  - browser_language
  - browser_viewportWidth
  - browser_viewportHeight
  - os_name
  - os_version
```

---

## User-actions golden-thread lane `[slug: logs-user-actions]`

Every browser-origin request is wrapped in a user action — the TOP of the golden thread. Parent
`faro.user.action` event: `action_id={request_id}` (the real cross-system join key), `action_name`
(low-card, e.g. submit/generate), `event_data_userActionDuration`, `_userActionStartTime`,
`_userActionEndTime`, `_userActionEventType` ∈ {click,submit}, `_userActionImportance` ∈
{normal,critical}. Seeing `faro.user.action` flips the app's `action_received_at` flag. Children in the
same beacon carry `action_name` + `action_parent_id`(=parent `action_id`): `faro.tracing.fetch`
(carries ledger `trace_id`/`span_id` — golden-thread join to backend spans; `event_data_http.status_code
≥400` drives action HTTP-error rate); on 5xx a `kind=exception` with `action_parent_id`.

```yaml signals
source: faro_user_actions
scope: blueprint
sink: faro
stream_labels:
  app_id: <numeric-string>
  app_key: <app-key>
  deployment_environment: <env>    # Faro COLLECTOR label — legacy key form; not a synthkit promrw emit
  kind: event
  service_name: <service>
  service_namespace: <namespace>
structured_metadata:
  - app
  - detected_level
  - service_version
  - stack_id
body_fields:
  - timestamp
  - session_id        # ALWAYS body, never label (T12)
  - event_name        # faro.user.action | faro.tracing.fetch
  - action_id         # = request_id (cross-system join key)
  - action_name       # low-card: submit/generate/…
  - action_parent_id  # children only
  - event_data_userActionDuration
  - event_data_userActionStartTime
  - event_data_userActionEndTime
  - event_data_userActionEventType   # click|submit
  - event_data_userActionImportance  # normal|critical
  - trace_id          # faro.tracing.fetch (golden-thread join to backend spans)
  - span_id
  - event_data_http.status_code
```

---

## Multi-page browser SESSION emission `[slug: logs-faro-session]`
*Provenance: `internal/workload/app/rum.go` + `internal/workload/app/rum_session.go` —
implementation confirmed 2026-06-16. Session shape derives from the frontend blueprint page
inventory (the first blueprint to declare a `pages:` list on the frontend node).*

The `app` workload (not `web_service`) enriches each browser-origin request into an **ordered,
multi-beacon session** whose beacons all share one `session_id`. The session shape is
**deterministic per request** (seeded from `trace_id`) so the same request always renders the
same navigation path.

**Session structure (per browser-origin request):**

1. **Navigation beacons** — 1..4 page-view beacons drawn from the frontend node's blueprint
   `pages:` inventory, spread strictly BEFORE the assist beacon (each step ~1500 ms earlier).
   Each nav beacon carries:
   - **First beacon only:** `event_name=session_start` (`event_domain=session`) — opens the FEO
     Session. All subsequent nav beacons carry `view_changed` instead (see below).
   - **Subsequent beacons:** `event_name=view_changed` (`event_domain=browser`) with
     `event_data_fromView` = previous page's view name, `event_data_toView` = current page name.
   - **`faro.user.action` parent event** (`event_domain=browser`) — slash-free action name drawn
     from the page's declared `actions:` list (e.g. "search documents", "generate draft",
     "submit query", "run quality check", "route for approval"); `event_data_userActionEventType=click`,
     `event_data_userActionImportance=normal`. A fresh `action_id` is minted per nav step
     (NOT the request_id — nav actions are not the golden-thread join key).
   - **5 web-vitals measurements** (LCP/CLS/INP/TTFB/FCP) — same shapes as the assist beacon;
     `Trace` context stripped (nav beacons have no backend fetch, so no `trace_id`/`span_id` on
     the measurements). Action join via `action_id` only.
   - **Meta:** `page_id`/`page_url`/`view_name` set to the NAV page (not the request route).
   - **No `faro.tracing.fetch`, no trace context, no exception** — nav is pure browser-side.

2. **Assist beacon** (last in the session) — the existing golden-thread beacon (see
   `[slug: logs-user-actions]`): `faro.user.action` with `action_id=request_id` + `faro.tracing.fetch`
   carrying `trace_id`/`span_id` + 5 web-vitals with trace context + optional exception on error.
   `session_start` is **suppressed** when nav beacons already opened the session.

**Fallback (no `pages:` declared):** single standalone assist beacon (today's behavior —
`session_start` included on that beacon, same as before the enrichment).

> **Cardinality note:** `page_id`, `page_url`, `view_name`, `action_name`, session length are ALL
> **body fields only** — never stream labels, never structured metadata. The `-dump` series
> inventory is unchanged by this enrichment (T12 + high-card invariant preserved).

**Example page inventory** (blueprint-declared `pages:` on the frontend node — these are
the view names and paths that nav beacons draw from):

| View name | Path | Example actions |
|---|---|---|
| Dashboard | `/dashboard` | "submit query", "route for approval" |
| Document Library | `/document-library` | "search documents", "generate draft" |
| AI Assistant | `/assistant` | "submit query" |
| Results | `/results` | "route for approval" |
| Review & Collaboration | `/review` | "route for approval" |
| Settings | `/settings` | |

---

## Browser spans (Tempo lane) `[slug: logs-browser-spans]`

`SPAN_KIND_CLIENT`; `parent_span_id=""` (trace root). ⚠ **Span NAME = the HTTP method only** (e.g.
`POST`/`GET`) — NOT "{method} {url.template}"; the un-truncated name rides in the `original_span_name`
attr (= `HTTP POST`). Emitted by the Faro Web SDK's `@opentelemetry/instrumentation-fetch` scope.
Web vitals do NOT appear as span attributes (Loki measurement path only).

*Provenance: **live Tempo capture** 2026-06-16 (`{ span.component = "fetch" }`, otel-demo frontend) —
the real browser-RUM resource + span attr set below. `v: ok`.*

```yaml signals
source: browser_spans
scope: blueprint
sink: otlp
span_kind: SPAN_KIND_CLIENT
parent_span_id: ""        # trace root
span_name: "<http.method>"   # e.g. "POST" — method only; full name in original_span_name
scope: "@opentelemetry/instrumentation-fetch"   # instrumentation scope (faro-web-sdk)
resource_attrs:           # the browser-RUM resource (distinct from backend services)
  - telemetry.distro.name        # = faro-web-sdk
  - telemetry.distro.version     # = 2.7.0
  - telemetry.sdk.name           # = opentelemetry
  - telemetry.sdk.language       # = webjs
  - telemetry.sdk.version        # = 2.7.1
  - process.runtime.name         # = browser
  - browser.brands               # array: ["Not;A=Brand","Chromium","Google Chrome"]
  - browser.language             # = en-GB
  - browser.mobile               # bool
  - browser.platform             # = "Mac OS 10.15.7"
  - user_agent.original
  - service.name                 # = frontend
  - service.namespace            # = opentelemetry-demo
  - service.version
  - deployment.environment.name  # synthkit-native form only (legacy deployment.environment dropped 2026-06-23)
  # + standard k8s.* resource attrs (cluster, k8s.namespace.name, k8s.pod.name, k8s.node.name) from the collector
span_attrs:
  - component              # = fetch
  - session.id
  - enduser.id             # = session.id
  - demo.synthetic_request # bool string (app-specific; the otel-demo synthetic-traffic flag)
  - http.method
  - http.url               # full URL (NOT url.template — url.template is on the faro.tracing.fetch LOG event)
  - http.host
  - http.scheme
  - http.status_code       # 0 on network error
  - http.status_text       # e.g. "network error"
  - http.user_agent
  - http.response_content_length
  - original_span_name     # = "HTTP <METHOD>"
  # synthkit ADDS the golden-thread join keys below (NOT native to real Faro browser spans —
  # synthkit's cross-signal correlation convention): app.correlation_id, request_id, app.user_action_id
status: STATUS_CODE_ERROR on http.status_code 0/5xx, else unset
note: "web vitals do NOT appear as span attrs — Loki measurement path only"
```

---

## Derived cloud-side metric (not emitted) `[slug: logs-derived-cloud]`

`grafanacloud_feo11y_app_info` (0/1) is derived cloud-side by the FEO pipeline after the first beacon
reaches the collector — synthkit cannot reproduce it via remote_write.

> **No `yaml signals` block** — this metric is NOT emitted by synthkit. It is recorded here for
> completeness: the FEO pipeline generates it automatically once `firstReceivedDataAt != nil` is set
> by the collector. Do not attempt to emit it via `sink/promrw`.

---

## k8s Addon logs — ScopeSubstrate `[slug: logs-k8s-addons]`

*Provenance: live capture from a reference EKS cluster 2026-06-16. Substrate-scoped — NO `blueprint` label. Stream labels follow the modern k8s-monitoring OTel-resource style (see `[slug: logs-app]` k8s pod-log note): `k8s_cluster_name`, `k8s_namespace_name`, `k8s_pod_name`, `k8s_container_name` (± `k8s_deployment_name`/`k8s_statefulset_name`), `service_name`, `service_namespace`, `log_iostream` ∈ {stdout,stderr}, plus `detected_level` (derived).*

> ⚠ **kube-system Alloy coverage gap:** `kube-system` namespace IS in the reference-cluster Loki scrape (LBC logs confirmed). However, **Karpenter** pods run on static `t4g.large` control-plane nodes that LACK Alloy DaemonSet coverage → karpenter pod logs are ABSENT from Loki on such clusters. Synthkit emits them anyway (structurally correct shape).

> ⚠ **CoreDNS and etcd** have NO log stream in Loki on the reference cluster (`kube-system` is scraped but CoreDNS writes plain-text to stdout at very low volume; no capture in the recon window). Not documented here.

---

### cert-manager (klog format) — containers `cert-manager-controller`, `cert-manager-cainjector`, `cert-manager-webhook`

*ns `cert-manager`; klog format (`I`/`W`/`E MMDD HH:MM:SS.µs goroutine file:line] msg key=val`). Alloy parses klog: `I`-prefix → `detected_level=unknown`; `W`-prefix → `detected_level=warn`; `E`-prefix → `detected_level=error`. All three components write to `log_iostream=stderr`.*

```yaml signals
source: cert_manager_logs
scope: substrate
sink: loki
stream_labels:
  k8s_cluster_name: <cluster>
  k8s_namespace_name: cert-manager
  k8s_pod_name: <pod>           # cert-manager-<hash>, cert-manager-cainjector-<hash>, cert-manager-webhook-<hash>
  k8s_container_name: cert-manager-controller | cert-manager-cainjector | cert-manager-webhook
  k8s_deployment_name: cert-manager | cert-manager-cainjector | cert-manager-webhook
  service_name: cert-manager
  service_namespace: cert-manager
  log_iostream: stderr
  detected_level: unknown | warn | error   # klog I→unknown, W→warn, E→error
body_format: klog
body_fields:
  - level_prefix    # I/W/E
  - timestamp       # MMDD HH:MM:SS.µsec
  - goroutine_id
  - file_line       # file.go:NN
  - msg
  - key_values      # space-separated key=val pairs (controller, reason, message, etc.)
note: "klog format; Alloy cannot parse I-prefix → detected_level=unknown for INFO lines; E-prefix → error"
```

---

### external-dns (zap JSON) — container `external-dns`

*ns `kube-system` (or custom ns); 1 replica; JSON format with `level` field ∈ {info,warning,error}. Alloy maps `warning` → `detected_level=warn`.*

```yaml signals
source: external_dns_logs
scope: substrate
sink: loki
stream_labels:
  k8s_cluster_name: <cluster>
  k8s_namespace_name: kube-system
  k8s_pod_name: <pod>           # external-dns-<hash>
  k8s_container_name: external-dns
  k8s_deployment_name: external-dns
  service_name: external-dns
  service_namespace: kube-system
  log_iostream: stderr
  detected_level: info | warn | error
body_format: JSON
body_fields:
  - level       # info | warning | error  (Alloy maps warning→detected_level=warn)
  - time        # RFC3339 (NOT "ts")
  - msg
  - context     # additional structured key-value pairs (e.g. source, provider, record_type)
note: "time field (not ts); level=warning (not warn) in raw body; Alloy maps → detected_level=warn"
```

---

### AWS Load Balancer Controller (zap JSON) — container `aws-load-balancer-controller`

*ns `kube-system`; 2 replicas; zap JSON; `ts` field (NOT `time`). Alloy maps `error` → `detected_level=error`; `info` → `detected_level=info`.*

```yaml signals
source: lbc_logs
scope: substrate
sink: loki
stream_labels:
  k8s_cluster_name: <cluster>
  k8s_namespace_name: kube-system
  k8s_pod_name: <pod>           # aws-load-balancer-controller-<hash>
  k8s_container_name: aws-load-balancer-controller
  k8s_deployment_name: aws-load-balancer-controller
  service_name: aws-load-balancer-controller
  service_namespace: kube-system
  log_iostream: stderr
  detected_level: info | warn | error
body_format: JSON
body_fields:
  - level       # info | warn | error
  - ts          # epoch float (NOT "time") — e.g. 1750096123.456
  - msg
  - controller
  - reconcileID
  - error       # optional, on error lines
note: "ts field (epoch float), NOT time; standard zap JSON output"
```

---

### Karpenter (zap JSON) — container `controller`

*ns `kube-system`; 2 replicas; zap JSON; `time` field RFC3339 millisecond UTC; `level` ∈ {INFO,WARN,ERROR} UPPERCASE. Alloy → `detected_level=info/warn/error`.*

> ⚠ **May be absent from Loki on Karpenter clusters.** Karpenter pods run on static `t4g.large` control-plane nodes that may lack Alloy DaemonSet coverage. The log shape below is sourced from vendor format documentation — `v: assumed`. Synthkit emits these logs for structural completeness.

```yaml signals
source: karpenter_logs
scope: substrate
sink: loki
stream_labels:
  k8s_cluster_name: <cluster>
  k8s_namespace_name: kube-system
  k8s_pod_name: <pod>           # karpenter-<hash>
  k8s_container_name: controller    # NOT "karpenter" — container name is "controller"
  k8s_deployment_name: karpenter
  service_name: karpenter
  service_namespace: kube-system
  log_iostream: stderr
  detected_level: info | warn | error
body_format: JSON
body_fields:
  - level       # INFO | WARN | ERROR (UPPERCASE — raw body; Alloy maps → lowercase detected_level)
  - time        # RFC3339 with millisecond precision, UTC (e.g. "2026-06-16T12:34:56.789Z")
  - msg
  - controller  # optional — which Karpenter controller (e.g. "provisioner", "disruption")
  - node        # optional — node name being acted on
  - error       # optional — on error lines
note: "level UPPERCASE in body (INFO/WARN/ERROR); time field RFC3339 UTC; container=controller NOT karpenter; may be absent from Loki on Karpenter clusters (v: assumed)"
```

---

### Argo CD (logfmt + JSON) — containers `application-controller`, `applicationset-controller`, `server`, `repo-server`

*ns `argocd`; most components use logfmt (Go zap logfmt output); notifications manager uses JSON. `time=` key in logfmt. `level` ∈ {info,warning,error}. Alloy maps `warning` → `detected_level=warn`.*

```yaml signals
source: argocd_logs
scope: substrate
sink: loki
stream_labels:
  k8s_cluster_name: <cluster>
  k8s_namespace_name: argocd
  k8s_pod_name: <pod>           # argocd-application-controller-0 (StatefulSet), argocd-server-<hash>, etc.
  k8s_container_name: application-controller | applicationset-controller | server | repo-server
  # StatefulSet pods carry k8s_statefulset_name; Deployment pods carry k8s_deployment_name
  service_name: argocd
  service_namespace: argocd
  log_iostream: stderr
  detected_level: info | warn | error
body_format: logfmt       # most components; notifications-controller uses JSON
body_fields:
  - time        # logfmt key "time=" (not "ts" or "timestamp")
  - level       # info | warning | error  (Alloy maps warning→warn)
  - msg
  - component   # optional (e.g. "application-controller", "argocd-server")
  - error       # optional
note: "logfmt format for most components; notifications-controller uses JSON; StatefulSet app-controller pod is argocd-application-controller-0; level=warning (not warn) raw logfmt; time= key"
```

---

### Envoy Gateway — control plane (zap tab-separated) + data plane (JSON access logs) — containers `envoy-gateway`, `envoy`, `shutdown-manager`

*ns `envoy-gateway-system`. Two distinct log formats:*
*- Control plane (`container=envoy-gateway`, 1 pod): zap tab-separated text (`{ts}\t{level}\t{logger}\t{msg}\t{json_fields}`)*
*- Data plane (`container=envoy`, 2 pods per Gateway): JSON access logs with `start_time`, `method`, `path`, `protocol`, `response_code`, `response_flags`, `duration`.*
*- Shutdown-manager sidecar (`container=shutdown-manager`): same JSON format as data plane.*

```yaml signals
source: envoy_gateway_logs
scope: substrate
sink: loki
stream_labels:
  k8s_cluster_name: <cluster>
  k8s_namespace_name: envoy-gateway-system
  k8s_pod_name: <pod>           # control: envoy-gateway-<hash>; data: envoy-<gw-ns>-<gw-name>-<hash>-<suffix>
  k8s_container_name: envoy-gateway | envoy | shutdown-manager
  k8s_deployment_name: envoy-gateway | envoy-default-eg-proxy
  service_name: envoy-gateway
  service_namespace: envoy-gateway-system
  log_iostream: stderr
  detected_level: info | warn | error
body_format: "zap tab-separated (control plane) | JSON (data plane)"
body_fields:
  # Control plane (zap tab-separated)
  - ts          # epoch float
  - level       # info | warn | error
  - logger      # e.g. "gateway.runner", "provider", "xds-translator"
  - msg
  # Data plane JSON access log fields
  - start_time
  - method      # HTTP method
  - path
  - protocol    # HTTP/1.1 | HTTP/2
  - response_code
  - response_flags   # e.g. "-" for success, "UH" for no healthy upstream
  - duration    # request duration ms
  - upstream_cluster
note: "control plane: zap tab-separated (ts\\tlevel\\tlogger\\tmsg\\t{json}); data plane: JSON access log per request; shutdown-manager sidecar uses same JSON format as data plane"
```
