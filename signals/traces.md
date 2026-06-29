# Traces (→ OTLP gateway → Tempo)

The `web_service` workload emits ONE connected trace per request modelling the real browser→backend→DB
path. Hand-encoded multi-Resource `ResourceSpans` (I2); Tempo assembles by `trace_id`+`parent_span_id`
across exports. See [`00-canon.md`](00-canon.md) for scoping `[slug: request-correlation]`, envelope keys
`[slug: env-label-keys]`, and high-cardinality strip rules `[slug: content-strip]`.

The tree below is the db/cache/service core. It ALSO admits typed AI hops (Spec 2b) —
`gateway`/`model`/`agent`/`tool`/`workflow`/`retrieval`, each a CLIENT span carrying `gen_ai.*`
attrs from `internal/genai`, nested via `via:`; an `llm_gateway` hop additionally emits a connected
gateway SERVER span (Path-B) carrying `portkey_trace_id`. See [`genai.md`](genai.md) `[slug: genai-spans]`.

---

## The span tree (db/cache/service core; AI hops admitted via genai.md) [slug: traces-span-tree]

```
[S1] HTTP POST/GET   (browser, webjs/faro)        TRACE ROOT
     SPAN_KIND_CLIENT; parent_span_id=""
     Resource: service.name={frontend-svc}, telemetry.sdk.language=webjs,
               telemetry.distro.name=faro-web-sdk, gf.feo11y.app.id/name,
               browser.{language,mobile,platform}, deployment.environment.name
     Attrs: session.id, enduser.id, component=fetch, http.{method,url,host,scheme,status_code},
            url.template, app.correlation_id, request_id, app.user_action, app.user_action_id

[S2] POST /api/v1/...  (backend SERVER span)       parent = S1 span_id
     service.name={backend-svc}; SPAN_KIND_SERVER
     Attrs: http.method, http.route, http.status_code, app.correlation_id, request_id, session_id

[S3..] DB / cache CLIENT spans                     parent = S2 span_id
     service.name={backend-svc}; SPAN_KIND_CLIENT; connection_type="database" on service-graph
     Attrs: db.system, db.name, db.operation, db.statement (SCHEMA/SHAPE ONLY — never row content)
     span name = "{db.operation} {db.name}"
```

**Non-browser requests** (batch/backend workloads): the backend [S2] SERVER span is itself the trace
root (no S1). `BackendParentID=""` for non-browser origins.

```yaml signals
span_tree:
  S1:
    kind: SPAN_KIND_CLIENT
    origin: browser (webjs/faro)
    role: TRACE ROOT
    parent_span_id: ""   # root — empty
  S2:
    kind: SPAN_KIND_SERVER
    origin: backend
    parent: S1.span_id
  S3plus:
    kind: SPAN_KIND_CLIENT
    origin: backend
    parent: S2.span_id
    note: one span per DB/cache hop; connection_type=database on service-graph edge
span_attributes:
  # S1 (browser)
  - session.id
  - enduser.id
  - component           # =fetch
  - http.method
  - http.url
  - http.host
  - http.scheme
  - http.status_code
  - url.template
  - app.correlation_id               # vendor-neutral application-level correlation (2026-06-23 §4.1)
  - request_id
  - app.user_action
  - app.user_action_id
  # S2 (backend SERVER)
  - http.method
  - http.route
  - http.status_code
  - app.correlation_id               # vendor-neutral application-level correlation (2026-06-23 §4.1)
  - request_id
  - session_id
  # S3.. (DB/cache CLIENT)
  - db.system
  - db.name
  - db.operation
  - db.statement        # SCHEMA/SHAPE ONLY — never row content
resource_attributes:
  # all spans
  - service.name
  - deployment.environment.name          # synthkit-native form only (legacy deployment.environment dropped 2026-06-23)
  - service.namespace
  - service.version
  # browser (S1) only
  - telemetry.sdk.language       # =webjs
  - telemetry.sdk.name           # =opentelemetry
  - telemetry.distro.name        # =faro-web-sdk
  - telemetry.distro.version
  - browser.language
  - browser.mobile
  - browser.platform
  - gf.feo11y.app.id
  - gf.feo11y.app.name
correlation_fields:
  - trace_id
  - span_id
  - app.correlation_id  # universal UUID — span attribute, never a label (renamed from app.correlation_id 2026-06-23 §4.1)
  - request_id            # per-request UUID; equals browser app.user_action_id — span attribute
  - session_id            # browser/user session UUID — span attribute
```

## Resource attributes on every span [slug: traces-resource-attrs]

`deployment.environment.name={env}` (synthkit-native; legacy `deployment.environment` dropped 2026-06-23), `service.name={placeholder}`,
`service.namespace={k8s namespace}`, `service.version={semver}`. Backend spans additionally carry
`k8s.cluster.name`, `k8s.namespace.name`, `k8s.pod.name`, `k8s.deployment.name`,
`telemetry.sdk.language`, `telemetry.sdk.name="opentelemetry"`, and (when the workload has a node
placement) **`k8s.node.name`** (omitted when empty, I13). Browser spans additionally:
`telemetry.sdk.language=webjs`, `telemetry.sdk.name=opentelemetry`, `telemetry.distro.name=faro-web-sdk`,
`telemetry.distro.version`, `browser.{language,mobile,platform}`, `gf.feo11y.app.id`, `gf.feo11y.app.name`.

## Correlation fields on every span (§1.4) [slug: traces-correlation]

`app.correlation_id` (universal UUID — vendor-neutral application-level correlation, 2026-06-23),
`request_id` (per-request UUID; equals the browser `app.user_action_id`), `session_id` (browser/user
session UUID), W3C `trace_id`/`span_id`. These are span ATTRIBUTES, never labels. Log field stays
`correlation_id` (unchanged — the span attribute is OTLP-side only). New: `traceparent` W3C field
on correlation log lines and AgentCore JSON bodies (2026-06-23).

## Timing & trace-break note [slug: traces-timing]

Spans spread across the batch window via a deterministic per-TraceID seed (idempotent re-projection:
same TraceID → same waterfall). An occasional un-instrumented hop produces a recoverable trace break
(child span with no parent in Tempo) — joinable via `app.correlation_id` across log streams; this is
expected realism, NOT modelled as an error.

> ⚠ Service-graph `blueprint` promotion lands as `client_blueprint`/`server_blueprint` per edge-side
> (§2.8.4) — never a bare `blueprint`.

Span timing uses `r.RenderStart()`; ledger windowing keys on `r.Start` (I11).

## App db/cache leaf → RDS instance link (db-CLIENT span) [slug: traces-app-db-instance]

The `app` workload models its service graph as nodes; a `type: db`/`type: cache` node is a **leaf**
(no SERVER span — the **caller's CLIENT span** `"call {node}"` IS the db hop). A leaf may declare
`db_instance: {base}` naming the blueprint database it represents; the workload resolves it per-env
(same-env preferred: `{base}-{lower(env)}`, case-insensitive — env names UPPERCASE, db names lowercase
suffix; exact name is the primary match) against the resolved RDS/Postgres fixtures threaded onto the
binding. The resolved RDS identity decorates that CLIENT span with **stable OTel semconv** DB attrs,
so the trace links the calling service to its environment's RDS instance and the service-graph edge
becomes a `Service → RDSInstance` join (`connection_type=database`, `server` = the node name; the
edge's RDS identity rides on the db-client span the metrics-generator reads).

This is **faithful to real OTel/Beyla DB instrumentation** (a Postgres client span carries
`db.system.name`/`db.namespace`/`server.address`) and **demoability-motivated**: synthkit already
declared the per-env RDS instances under `databases:` but never trace-linked them, so the Knowledge
Graph showed no `Service→RDSInstance` edge. We emit the **current** semconv names and intentionally do
NOT emit the deprecated `db.name` alongside `db.namespace`. (The older `web_service` db hop still uses
the legacy `db.system`/`db.name` form — see `[slug: traces-span-tree]` S3; the two coexist.)

- `db.system.name` — `postgresql` (postgres) | `mysql` | `redis` (cache).
- `db.namespace` — the logical database (the DB fixture's first `Databases` entry, e.g. `app`).
- `server.address` — the RDS endpoint FQDN (reused from `buildDBFixture`'s host, stored in the DB
  fixture's `InstanceKey`); its **first DNS label is the RDS `db_instance_identifier`** (e.g.
  `acme-pg-bve.<hex>.<region>.rds.amazonaws.com` → `db_instance_identifier=acme-pg-bve`), the same
  identifier the dbo11y lane carries (see [`dbo11y.md`](dbo11y.md)) — the cross-signal join key
  between the trace and the RDS instance's metrics.

The live `Service→RDSInstance` service-graph edge (the Asserts `aws_rds_servicegraph` view)
additionally depends on Tempo's metrics-generator service-graph **DB virtual-node detection** being
enabled on the stack (operator config, like the gen_ai span allowlist) — the app workload does not
self-emit `traces_service_graph_*` (no `emit_span_metrics`); production derives the edge from these traces.

```yaml signals
app_db_leaf_client_span:
  emitter: app workload (db/cache leaf node with db_instance set)
  span: "call {node}"                 # CLIENT span in the CALLER's resource
  kind: SPAN_KIND_CLIENT
  parent: caller node's span
  service_graph_edge:
    connection_type: database         # db/cache leaf edge (else "")
    server: "{node name}"             # e.g. rds-acl
  resolution:
    db_instance: "{base}"             # blueprint node field, e.g. acme-pg
    per_env: "{base}-{lower(env)}"    # same-env instance preferred (case-insensitive)
    fallback: exact-name match
  attributes:                          # stable OTel semconv (NOT the deprecated db.name)
    - db.system.name                  # postgresql | mysql | redis
    - db.namespace                    # logical database (e.g. app)
    - server.address                  # RDS endpoint FQDN; first DNS label = db_instance_identifier
    - app.correlation_id             # universal correlation (vendor-neutral, 2026-06-23)
    - request_id
    - session_id
  join:
    db_instance_identifier: first DNS label of server.address   # → dbo11y RDS instance metrics
  provenance: 2026-06-17 — app db-leaf RDS link (Option A); faithful to OTel/Beyla DB semconv, demoability-motivated
```
