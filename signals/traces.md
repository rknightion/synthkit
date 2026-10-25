# Traces (→ OTLP gateway → Tempo)

## App platform automation [slug: traces-app-automation]

The optional app automation declaration emits a bounded simulated sequence per ledger
request, not a real orchestrator. Workflow/task names are span names; controller
identity is the existing service resource. Approval decisions use `approval approved
<step>` or `approval rejected <step>`, not invented approval attributes.

Provenance: OpenTelemetry semantic-conventions v1.44.0:
https://github.com/open-telemetry/semantic-conventions/blob/v1.44.0/docs/http/http-spans.md
https://github.com/open-telemetry/semantic-conventions/blob/v1.44.0/docs/registry/attributes/service.md
https://github.com/open-telemetry/semantic-conventions/blob/v1.44.0/docs/registry/attributes/error.md
OpenTelemetry specification v1.61.0:
https://github.com/open-telemetry/opentelemetry-specification/blob/v1.61.0/specification/trace/api.md

Each HTTP send is a CLIENT span. The first omits `http.request.resend_count`; resends
carry ordinal 1, 2, etc. Resends follow failed attempts only. Recovery leaves task
and workflow UNSET while the failed attempt stays ERROR. An approval decision is
UNSET even when rejected; rejection stops later tasks and makes the workflow ERROR
with `error.type="_OTHER"`. This is the recipe's terminal convention, not a standard
approval field. Resource `service.namespace=platform-automation` distinguishes
controllers from `service.namespace=user-workloads`; routing checks the resource,
not a similarly named span attribute.

```yaml signals
app_automation:
  v: ok
  emitter: app workload
  resource_classifier:
    name: service.namespace
    type: string
    platform_value: platform-automation
    workload_value: user-workloads
  spans:
    workflow: {kind: SPAN_KIND_INTERNAL, name: "<declared workflow>"}
    task: {kind: SPAN_KIND_INTERNAL, name: "<declared task>"}
    approval:
      kind: SPAN_KIND_INTERNAL
      names: ["approval approved <step>", "approval rejected <step>"]
      status: STATUS_CODE_UNSET
    http_attempt: {kind: SPAN_KIND_CLIENT, name: "<known HTTP method>"}
  http_attempt_attributes:
    - {name: http.request.method, type: string}
    - {name: http.response.status_code, type: int}
    - {name: http.request.resend_count, type: int, present: resends-only}
    - {name: server.address, type: string}
    - {name: server.port, type: int}
    - {name: url.full, type: string, note: static operational URL without credentials/query/fragment}
    - {name: error.type, type: string, present: failed-operations-only}
  status:
    success: STATUS_CODE_UNSET
    failed_http_attempt: STATUS_CODE_ERROR
    exhausted_task: STATUS_CODE_ERROR
    rejected_workflow: STATUS_CODE_ERROR
  forbidden: [payloads, secrets, command_output, arbitrary_attributes]
```

## App causal request spans [slug: traces-app-causal]

An `app` workload that opts into causal resources (`uses`, `call_policies`, `worker`,
`traffic.from_resource`) projects each request's derived hop facts. Healthy requests keep the
legacy spans. Only failing spans gain attributes.

Provenance: OpenTelemetry semantic-conventions v1.44.0:
https://github.com/open-telemetry/semantic-conventions/blob/v1.44.0/docs/http/http-spans.md
(status rules, `http.response.status_code`, `http.request.resend_count`, `error.type`)
https://github.com/open-telemetry/semantic-conventions/blob/v1.44.0/docs/db/database-spans.md
(`error.type` on a database client span)
https://github.com/open-telemetry/semantic-conventions/blob/v1.44.0/docs/registry/attributes/error.md
(`error.type`, fallback value `_OTHER`)
https://github.com/open-telemetry/semantic-conventions/blob/v1.44.0/docs/messaging/messaging-spans.md
(a processing span of a message consumer is `CONSUMER`)
https://github.com/open-telemetry/semantic-conventions/blob/v1.44.0/docs/exceptions/exceptions-spans.md
(document status Deprecated: "Use Semantic conventions for exceptions in logs instead")
https://github.com/open-telemetry/semantic-conventions/blob/v1.44.0/docs/exceptions/exceptions-logs.md
(`exception.type` on a log record).
OpenTelemetry specification v1.61.0:
https://github.com/open-telemetry/opentelemetry-specification/blob/v1.61.0/specification/trace/api.md
(span status description only with `Error`; `CONSUMER` span kind).

- HTTP SERVER span status is ERROR only for a 5xx response; a 4xx answer stays UNSET. A gRPC
  SERVER span follows the pinned gRPC rule instead: ERROR only for UNKNOWN, DEADLINE_EXCEEDED,
  UNIMPLEMENTED, INTERNAL, UNAVAILABLE and DATA_LOSS (here the 5xx class and a call that timed
  out), UNSET for a throttle or a 4xx rejection ([slug: traces-causal-grpc-status]). CLIENT spans
  are ERROR for 4xx, 5xx and transport failures (connect, credential, timeout), gRPC included. A
  worker entry's root is a CONSUMER span; the HTTP rules do not apply to it, so any failed request
  sets ERROR.
- `http.response.status_code` is stamped on failing HTTP service hops and a failing SERVER root,
  never on gRPC hops; db/cache leaf CLIENT spans get `error.type` only.
- `error.type` is set only on a span whose status is ERROR (a SERVER span answering 4xx or 429
  stays UNSET with `http.response.status_code` and no `error.type`; the cause stays on the log).
  It is the origin's own type at the origin span (callee SERVER span for server-side
  faults; caller CLIENT span for transport failures and timeouts). Upstream spans carry the mapped
  status as a string, or `timeout` / the transport type when no status was seen. A resource
  failure with neither a status nor a declared type uses the well-known fallback `_OTHER`.
- The origin span's status description is the fault message, set only when the span is ERROR (the
  specification forbids a description on any other status). The semantic conventions advise
  omitting a description the status code already implies; the message is kept because it names
  the declared cause (a resource and its mode), which a status code cannot.
- A request that fails only from the workload's baseline error rate (no declared fault) projects
  exactly as a non-causal request.
- Each resend is its own CLIENT span (and callee SERVER subtree) with
  `http.request.resend_count` = 1, 2, ...; the first attempt omits it.
- No `exception` span event is emitted (deprecated in the pinned conventions). The class rides
  the error log's `exception_type` field instead ([logs.md](logs.md)), a blueprint-declared log
  field whose name is the author's choice.

```yaml signals
app_causal:
  v: ok
  emitter: app workload (causal requests)
  attributes:
    - {name: http.response.status_code, type: int, present: failing-http-hops-only}
    - {name: error.type, type: string, present: failed-operations-only}
    - {name: http.request.resend_count, type: int, present: resends-only}
  status:
    http_server_5xx: STATUS_CODE_ERROR
    http_server_4xx: STATUS_CODE_UNSET
    grpc_server_error_class: STATUS_CODE_ERROR      # 5xx class and timeouts; see traces-causal-grpc-status
    grpc_server_throttle_or_4xx: STATUS_CODE_UNSET
    client_4xx_5xx_transport_timeout: STATUS_CODE_ERROR
    consumer_root_any_failure: STATUS_CODE_ERROR
  span_kinds:
    worker_entry_root: SPAN_KIND_CONSUMER
  span_events: none
```

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

**Collector-processed backend resource shape (measured 2026-08-31).** When the
`application_observability` collector is enabled, a near-default k8s-monitoring receiver does not
produce a uniform cloud resource. Only Node-runtime services on
AKS/GKE carry `cloud.provider` (`azure`/`gcp`); non-Node services omit it, and EKS services omit it
entirely. Detected `cloud.*` is therefore never a reliable provider-scoping key. The receiver's
resource-detection override also stamps collector identity: `host.name` is one deterministic
collector-shaped value shared by every application in a cluster, `host.arch` follows the
collector node architecture, and `os.type="linux"`. These attributes contribute zero
per-application cardinality. Absent attributes are omitted rather than sentinelled (I13).

*Provenance: canonical per-substrate capture set and its recorded span-resource measurements,
2026-08-31. Values in synthkit are deterministic synthetic identities; no captured identity is
copied.*

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

An `app` `services[]` node can set `signals.traces: false`. It emits no root, SERVER, CLIENT, DSL child or agent-flow spans of its own. A traced caller's CLIENT span remains, and the untraced node passes W3C trace context through to a traced callee. The callee's SERVER span uses the same trace ID and the nearest emitted CLIENT as parent, so Tempo can show a direct caller-to-callee connection across the untraced node. An untraced entry with no upstream span leaves the first traced callee as the observed root. This switch does not stop the ledger from minting correlation IDs or remove trace IDs from enabled logs. An entry carrying `rum_faro` and a node declaring `agentic_flow` must keep traces enabled.

**Queue-driven worker spans** (`app` with `traffic.from_resource` and a `worker` entry). The worker's root is a `SPAN_KIND_CONSUMER` span. When another app's hop publishes into the queue (its service `uses` the queue), the worker request that takes that message continues the producing request's trace: `trace_id` is the producing request's, `parent_span_id` is the publishing hop's span (the publisher's SERVER span, else the caller's CLIENT span), and the span carries one OTLP `Span.links` entry to that same span context (semantic-conventions v1.44.0 messaging-spans.md, "Message creation context as parent of Process span": a single-message process span MAY use the creation context as parent and links to it). The worker's logs carry the same `trace_id`. The pairing is first in, first out per queue: a worker request takes the oldest unconsumed message whose producing request started no later than the worker request started minus its duration, and a request that finds none keeps its own root with no parent and no link. A failed publishing hop publishes no message. Span counts and span metrics are unchanged: no span is added or removed.

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

## Causal gRPC hop span status [slug: traces-causal-grpc-status]

A causal hop to or from a `type: grpc` node follows the gRPC span-status rules, not the HTTP
ones. Provenance: OpenTelemetry semantic-conventions v1.44.0
https://github.com/open-telemetry/semantic-conventions/blob/v1.44.0/docs/rpc/grpc.md

- A gRPC SERVER span (a callee's, or a gRPC entry's root) is ERROR only for the statuses the
  convention lists as server errors: UNKNOWN, DEADLINE_EXCEEDED, UNIMPLEMENTED, INTERNAL,
  UNAVAILABLE and DATA_LOSS. The causal facts carry HTTP-class statuses, so the 5xx class and a
  call that timed out are server errors; a throttle (429, RESOURCE_EXHAUSTED) and a rule or
  argument rejection (4xx, INVALID_ARGUMENT) leave the SERVER span UNSET with no `error.type`.
- A CLIENT span calling a gRPC node is ERROR on any failed answer (every status except OK).
- No HTTP status attribute is stamped on any span of a gRPC hop. The gRPC status attribute
  (`rpc.response.status_code` in v1.44.0) is not emitted until its values are sourced (cantfind
  SK-133); `error.type` keeps its generic value.
- Only HTTP service types carry `http.response.status_code`; `worker`, `stream` and `job`
  nodes do not.
