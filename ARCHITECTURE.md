# synthkit — architecture (frozen seams)

synthkit is a general-purpose, composable synthetic-telemetry generator: a **catalog** of
accurately-shaped construct + workload modules (each emits the REAL metric/label/field names of the
technology it models) and a **composition layer** where a declarative YAML **blueprint** assembles
them with config. Zero coupling between constructs; zero coupling from any construct back to any
blueprint. A newcomer authors a new blueprint in one YAML file touching no construct code; deleting
that file affects nothing else.

Provenance: mechanics and signal shapes are lifted from a proven private generator (the "predecessor");
every construct's signal contract is reproduced byte-exact in the [`signals/`](./signals/) catalogue (indexed by [SIGNALS.md](./SIGNALS.md)) with
citations. The predecessor's coupling anti-patterns (scenario policy matrix, env fold layer, positional
nonces, global self-registration) are deliberately inverted here — see Invariants.

## 1. The three tiers

```
BLUEPRINT (YAML)            the recipe AND the wiring layer: a bill-of-materials of construct +
   │                        workload INSTANCES with config. The ONLY place blueprint-specific
   │ load → validate →      config lives. Resolves shared identity ONCE (EC2↔EKS node, DB↔cloud).
   │ resolve fixtures
   ▼
COMPOSITION ROOT (runner)   instantiates the BoM via the explicit registry, builds the
   │                        per-blueprint ledger + scoped sink writers, runs the two-cadence
   │ Build(cfg, fixtures)   scheduler, routes signals to sinks.
   ▼
CONSTRUCTS ⊥ WORKLOADS      isolated modules. Constructs render infra telemetry from config +
                            fixtures. Workloads mint/project correlated request telemetry via the
                            ledger. Neither knows blueprints or each other exist.
```

## 2. Frozen seams (exact signatures — `internal/core`, `internal/ledger`, `internal/fixture`)

### Construct

```go
type Construct interface {
    Kind() string                  // registry key, snake_case ("coredns", "rds")
    Signals() []SignalClass        // Metrics | Traces | Logs | RUM | PyroscopeProfiles — framework only asks for these
    Interval() time.Duration       // tick cadence; metric lanes ≥60s (DPM floor, runner-clamped)
    Tick(ctx context.Context, now time.Time, w *World) error
}

type ConstructReg struct {
    Kind      string
    Doc       string
    Scope     Scope                              // ScopeBlueprint | ScopeSubstrate (see §5)
    NewConfig func() any                         // pointer to the kind's YAML config struct
    Build     func(cfg any, fx *fixture.Set) (Construct, error)
}
```

### Workload

```go
type Workload interface {
    Kind() string
    Name() string                  // instance name from the blueprint ("acme-api")
    Signals() []SignalClass
    Interval() time.Duration
    Minter() ledger.Minter         // contributes request volume to the blueprint's ledger
    Tick(ctx context.Context, now time.Time, w *World) error                       // metric lane
    ProjectBatch(ctx context.Context, now time.Time, w *World, batch []*ledger.Request) error // trace/log/RUM lane
}

type WorkloadReg struct {
    Kind      string
    Doc       string
    NewConfig func() any
    Build     func(cfg any, b Binding) (Workload, error)   // Binding = resolved cluster + call targets + optional RUM sink
}
```

**Two workload kinds — when to use which:**
- **`web_service`** — a SINGLE service: a browser→backend→DB hop tree with the gen_ai / RUM / Beyla
  lanes. The simple, common case.
- **`app`** — a blueprint-declared service GRAPH of typed nodes (`services:`), each emitting its OWN
  custom telemetry via the DSL. ONE request-flow workload mints at the entry node and projects ONE
  trace across the whole graph (the runner dispatches by the single `req.Workload`, so a graph node
  is NOT its own `Workload`). Reach for `app` when you need multiple distinct backend services each
  with their own (possibly custom) metrics/logs/spans, per-service incident targeting, or per-service
  scaling; use `web_service` otherwise. The two kinds may COEXIST in one blueprint (the core
  correlated flow as an `app`; simpler/peripheral services as standalone `web_service`).

The `app` service-node + the custom-telemetry DSL are mechanic-lib seams (`internal/telemetryspec`,
a peer of `internal/cw`/`internal/genai`; NO OTel SDK — metrics via promrw final names, spans via
the hand-encoded OTLP seam):
- `ServiceNode{Name, Type, Runtime, Entry, Replicas, Profiles, Metrics, Logs, Spans, Calls}` — a
  typed graph node. `Type` selects span semantics from a registry with a DEFAULT FALLBACK
  (frontend/web/grpc/worker/job/stream/gateway/db/cache/llm/agent/tool/workflow/retrieval). Node
  names are UNIQUE within a graph; the node identity is AUTO-STAMPED on every series/stream/span
  before author labels (so two nodes sharing a profile never collide).
- `telemetryspec.{MetricSpec,LogSpec,SpanSpec}` built from `ValueModel` one-of value generators
  (`const`/`const_str`/`enum`/`int_range`/`float_range`/`normal`/`bool`/`shape`/`ref`). `shape` is
  driven by the incident engine (incident-responsive values); `ref` pulls a correlation field (the
  request-correlation glue). A load-time CAPABILITY MATRIX enforces: a high-card `ref` is legal ONLY in a
  log body / span attribute (never a label/stream-label); a label source must be
  `const`/`const_str`/`enum` with a stable, total domain (I32 determinism, I40). The canonical
  high-card key set lives in `internal/highcard`, shared by the loki + promrw sinks AND the DSL — so
  they agree by construction.
- `Profile` — a named bundle of specs; the catalog ships generic, real-named templates
  (`internal/telemetryspec/profiles`), composed onto a node by name and/or extended inline.
- spanmetrics/service-graph stay metrics-generator-DERIVED; `app` honors the per-blueprint
  `EmitSpanMetrics` opt-in IDENTICALLY to `web_service` (synthesizing them from its own graph only
  when a tenant's metrics-generator is off).

### World (handed to every Tick/ProjectBatch)

```go
type World struct {
    Shape   *shape.Engine    // diurnal plateau × weekly × noise × incidents
    Metrics MetricWriter     // pre-scoped: stamps the blueprint label iff ScopeBlueprint (clone-before-stamp)
    Logs    LogWriter        // Loki 3-tuple; sink ASSERTS no high-card stream label
    Traces  TraceWriter      // OTLP ResourceSpans — TRACES ONLY
    Pyroscope PyroscopeWriter // Pyroscope profile series (hand-built pprof); span profiles via ActiveFor (workload-lane only)
    Ledger  *ledger.Ledger   // workloads only; constructs receive nil and never read it
}
```

Writers are nil for classes the instance did not declare in `Signals()`.

### Ledger (request-correlation spine; `internal/ledger`)

```go
type Minter interface {
    Workload() string
    Mint(now time.Time, tickSec float64, eng *shape.Engine) []*Request  // volume × tickSec/30 (cadence-invariant)
}

func (l *Ledger) Mint(now time.Time) []*Request                     // master clock only; RETURNS the batch
func (l *Ledger) Active(now time.Time, window time.Duration) []*Request
func (l *Ledger) ActiveFor(workload string, now, window) []*Request
```

`Request` carries `Correlation{CorrelationID, TraceID, SpanID, BrowserSpanID, SessionID, RequestID}`
plus identity dims (Workload/Env/Cluster/Route), timing (`Start` = windowing key,
`RenderStart() = Start − Duration − RenderOffset` = span-timing base — traces are BACKDATED to
completion so they END at ~Start ≈ now, matching real spans that export on completion), and facts
(Outcome/StatusCode/Calls/BrowserOrigin). **Only the ledger mints request-scoped IDs.**

### Fixtures (`internal/fixture`) — the shared-identity vocabulary

`Env`, `Cloud{AccountID, Region, VpcID, NATGatewayIDs}`, `Cluster{Name, Nodes, Workloads,
K8sMonitoring}`, `Node{InstanceID, Hostname, PrivateIP, InstanceType}` (THE EC2↔EKS identity),
`DB{Name, ServerID, InstanceKey, Queries}` (THE dbo11y↔cloud identity), `Cache`, `CallTarget`,
`Set` (the bundle handed to Build). All identity is deterministic via `fixture.Sum/HexID/...`
helpers seeded from `"<blueprint>:<path>"` strings — same blueprint, same identities, every run.
This package is frozen like an interface; changes are a wiring event.

### Registry

Explicit instance built in the composition root's catalog wiring file (single owner). **No global
registries, no `init()` self-registration.** The YAML loader validates every construct/addon/
workload/feature kind and every config field against it — unknown anything is a loud load error.

## 3. The blueprint YAML

```yaml
name: acme                      # unique; also the determinism seed root
label: acme                     # sink-stamped selector value (defaults to name); stable, NEVER positional
shape: business_hours_plateau   # default shape profile for everything in this blueprint
timezone: Europe/Zurich         # optional; business-hours anchor

environments:                   # each env = its own account + VPC (+ optional cluster)
  - name: prod
    weight: 1.0
    production: true            # default: true iff name == "prod"
    cloud:    { provider: aws, account_id: "111122223333", region: us-east-1,
                vpc_id: vpc-0acme01, nat_gateways: 2 }
    cluster:
      type: eks
      name: acme-prod-use1      # must be globally unique across enabled blueprints (load check)
      node_groups: [{ name: general, instance_type: m6i.xlarge, desired: 4 }]
      k8s_monitoring: { enabled: true, chart_version: "4.1.4", alloy: true, opencost: true, kepler: true }
      addons:                   # registry kinds; scalar or {name:…, <config>} map form
        - load_balancer_controller
        - core_dns
        - { name: cluster_autoscaler, min_nodes: 3, max_nodes: 10 }
    databases:
      - { engine: postgres, version: "16.2", name: acme-app-db,
          observability: { cloudwatch: true, dbo11y: true, digests: 40 } }  # emission switch — see below
    caches:
      - { engine: redis, version: "7.1", name: acme-sessions }

workloads:
  - type: web_service
    name: acme-api
    runs_on: acme-prod-use1     # WIRING: workload → cluster (resolved or load error)
    tracing: true
    rum: true                   # needs GC_FARO_* creds; absent → disabled with a load warning
    traffic: { shape: diurnal, off_peak_rps: 20, peak_rps: 120 }   # overrides blueprint shape for this workload
    calls: [{ db: acme-app-db }, { cache: acme-sessions }]         # resolved or load error
    endpoints:
      - { route: "GET /v1/items",  error_rate: 0.01, p95_ms: 120 }

features:                       # Grafana Cloud products you enable in your stack
  synthetic_monitoring: { enabled: true, checks: [acme-api-health] }
  fleet_management:     { enabled: true, collectors_per_os: { linux: 6, darwin: 2 } }

integrations:                   # external systems Grafana Cloud ingests/observes
  cloudflare: { enabled: true, zone: acme.example.com }
  csp_azure:  { enabled: true, sub_signals: [compute, databases] }

incidents:
  - { kind: latency_spike, target: acme-api, at: "2026-06-19T14:00", for: 20m, intensity: 0.8 }
```

**Two decode paths** (both end in the construct's own typed config struct):

1. **Direct-config constructs** (cluster add-ons, plus the two top-level sections —
   `features:` for Grafana Cloud products and `integrations:` for external sources GC ingests):
   the YAML node decodes straight into `ConstructReg.NewConfig()`'s struct via strict `yaml.v3`
   decoding (unknown field = error). `features:` and `integrations:` share one decode path; the
   construct's declared `Group` (feature|integration) must match the section, so a mis-bucketed
   declaration (a GC product under `integrations:`, an external source under `features:`, or a
   topology/add-on kind under either) fails loudly at load. The split is by what the thing IS —
   it does NOT track `Scope` (Cloudflare is ScopeBlueprint, the CSPs and SM/Fleet are substrate).
2. **Resolved-topology declarations** (`cloud`, `cluster`, `databases`, `caches`, `workloads`):
   owned by the blueprint schema because one declaration fans into MULTIPLE construct instances +
   shared fixtures (a `databases:` entry becomes an `rds` instance AND a `dbo11y_postgres` instance
   sharing one `fixture.DB`). The loader's **topology resolver** runs after full parse:
   - node count: explicit `desired` wins; else `NodesNeeded = max(3, ceil(pods/8))`,
     pods = Σ replicas of workloads bound to the cluster;
   - builds `fixture.Node` identities (instance IDs, hostnames, IPs) ONCE — handed to BOTH the
     k8s construct and the EC2-CloudWatch construct;
   - builds `fixture.DB`/`fixture.Cache` ONCE — handed to BOTH the cloud and dbo11y constructs;
   - resolves every string reference (`runs_on`, `calls[].db/cache`, `incidents[].target`) or
     fails loudly listing the available names;
   - rejects identity collisions ACROSS enabled blueprints (cluster name, account_id, DB/cache name).

Blueprints can equally be built programmatically — YAML decodes into the same structs.

**Per-environment fan-out (`for_each_env` / `envs`).** A top-level `workloads[]` entry OR an
`integrations:` declaration may carry the reserved `for_each_env: true` (and optional `envs: [subset]`)
key, fanning it into ONE instance per target environment. The resolver threads each env's
`fixture.Set{Env, Cloud}` into the instance: a fanned **workload** binds to that env's cluster, is
named `"<name>-<env>"`, and is its own ledger/correlation domain (so traces correlate *within* each
env); a fanned **integration** stamps the low-cardinality `env` label on **every** series it emits
(self-telemetry included — each per-env instance is a distinct process) and weight-scales its
magnitudes by `Shape.Factor(now, env.Weight, env.NonProd)`. A fanned construct that left ANY family
env-less would push byte-identical series from every instance (Mimir duplicate-sample rejection),
so the "env on every series" rule is enforced by `integration.TestNoDuplicateSeries` (no two emitted
series share an identical name + label set across the whole estate). A construct with no `fixture.Env`
(aggregate, the default for non-fanned declarations) keeps its prior behaviour byte-for-byte —
`BusinessFactor(now)` and no `env` label (an absent dimension is omitted, never `""` — I13). The
magnitude path **branches** on whether `Env` is set; it never blanket-swaps `BusinessFactor`→`Factor`
(they differ on weekends: 0.3 vs 0.2). A fanned workload may declare only AI/`service:` calls — a
`db:`/`cache:` call (a globally-unique physical resource) is rejected at load. This is how one
declaration models a dev/test/prod estate with per-env scale + within-env trace correlation.

### 3.1 Construct boundaries (read before adding constructs)

The catalog grows by adding constructs/workloads, never by inventing metrics in YAML (fidelity is the
product). Draw a construct boundary at the **smallest unit that is independently declarable in a
blueprint AND carries a distinct shared identity / cross-construct join** — *not* at the delivery
pipeline. "It all arrives via the CloudWatch metric stream" is a transport fact, not a boundary:

- **Distinct fixture + distinct declaration + distinct join → separate construct**, sharing a mechanic
  library. `ec2` (`fixture.Node`, joins EKS via `provider_id`/`InstanceId`), `rds` (`fixture.DB`, joins
  dbo11y via `db_instance_identifier`), and `elasticache` (`fixture.Cache`) are separate for this
  reason — and all delegate stat expansion to `internal/cw`. Rolling `rds` into a CloudWatch monolith
  would forfeit the per-DB fixture and the RDS↔dbo11y join, then have to re-introduce both inside the
  monolith — cost with no benefit.
- **Same pipeline + same identity + declared together → ONE construct with config-gated sub-families.**
  `cwinfra` bundles ALB/NLB/EBS/NAT/EKS/S3/Firehose off one cloud identity, each family gated by a
  per-family switch in the `cloud.cloudwatch` block (`nlb: false`, `ebs: false`, `nat_gateway: false`,
  `eks: false`, `firehose: false`; `albs`/`s3_buckets` are count knobs where an explicit `0` disables
  the family — omitted ⇒ default 1 ALB / 2 buckets, full toggle parity with the `*bool` families);
  `k8s_cluster` gates
  OpenCost/Kepler/Alloy off `k8s_monitoring`; `cspazure`/`cspgcp` gate their per-service families off
  `sub_signals: [...]` (azure: compute/databases/storage/networking/messaging/logs; gcp adds
  loadbalancing/pubsub/cloudrun/bigtable). Empty/omitted ⇒ all families emit.
- **Engine/type variants of one resource → a config discriminator, not a new kind.** RDS Postgres vs
  Aurora Postgres share the `databases:` declaration, the `rds` construct, and the dbo11y lane (dbo11y
  is the Postgres wire protocol either way); the engine/type selects the CloudWatch family variant and
  is gated by the same emission switch below.

### 3.2 Incident model: definition vs activation

The incident model separates WHAT CAN happen (vocabulary) from WHEN it happens (activation):

**Construct vocabulary.** Every construct/workload registration declares the failure modes it
responds to — a `[]failuremode.Mode{Name, Axis, Help}` list (e.g. k8s_cluster declares `oom_kill`,
`pod_crashloop`, `node_not_ready` on `AxisCluster`; dbo11y_postgres declares `connection_saturation`,
`replication_lag`, `lock_contention`, `slow_query_storm` on `AxisDatabase`). The construct itself
owns the physics: on each tick it calls `shape.Eval(mode, ownIdentity)` for the modes it implements.
Axes: `workload | cluster | database | cache | cloud | service`.

The **`service` axis** (`AxisService`) targets an individual NODE of an `app` service graph.
The `app` workload declares its modes (`error_spike`/`latency_storm`/`throughput_drop`/`fallback_storm`/
`retry_storm`) on `AxisService`; the resolver enumerates the graph's node names as addressable targets
by peeking the raw `services:` YAML (no `internal/workload/app` import in the loader). An incident on a
node shifts BOTH that node's aggregate metrics (its `shape`-driven DSL values) AND its correlated
sample (its trace hop slows / fails) while sibling nodes are unaffected — a localized blast radius.

**Blueprint scenarios.** A blueprint composes modes into named, reusable `scenarios:` bundles
(`[]EffectDecl{Mode, Target, Intensity}`). An effect target is a declared instance name,
`<axis>:*` (every instance of that axis), or empty (mode's sole axis only — rejected at load for a
multi-axis mode). The resolver validates every effect against the construct vocabulary + the
addressable-target inventory and rejects unknown modes, targets, and axis mismatches.

An `incidents:` entry can reference a scenario by `scenario: name` (fires the whole bundle on the
declared schedule) OR declare a single `kind`/`target`/`intensity` directly. A scenario-ref and a
single-mode entry are mutually exclusive in one `IncidentDecl`.

**Control-plane activation.** Live activation is additive on top of the scheduled windows:
- `POST /control/scenarios` activates any set of scenarios by `blueprint/name` (validated against
  the derived schema). The runner's per-blueprint `shape.Engine.Live` hook unions all active
  scenarios' effects with any ad-hoc `POST /control/failures` knobs; `<axis>:*` targets are
  expanded at activation time against the blueprint's target inventory.
- `POST /control/failures` (legacy/escape-hatch): ad-hoc `{mode → enabled/intensity/scope}` map.
  Unknown modes/scopes are WARNED (logged), never rejected, so operators can exercise modes outside
  the inventory — hard bounds (intensity range, cardinality cap) still apply.
- `GET /control/schema` returns the schema DERIVED from the loaded blueprints and construct
  vocabulary (modes, targets with live scaling state, scenarios) — replacing the former hardcoded
  descriptor list. Satisfies `control.SchemaSource` via `*Runner.ControlSchema()`.

### 3.3 Live k8s pod scaling with node cascade

`POST /control/scaling` sets live workload pod counts by target name. Each workload target has
`ScaleBounds{Dimension:"replicas", Min:1, Max:50, Default:<declared>}` (validated at load).

On each tick, `k8scluster` calls `liveCluster(w)`, which produces a **per-tick shallow copy** of
the cluster fixture with live replica counts read from `w.Scaling` (a `*scale.Source`). The node
count **cascades**: both k8scluster and ec2 call the shared pure function
`fixture.DeriveNodes(seed, cluster, nodeGroups, region, totalPods)` each tick, so they agree
byte-for-byte without shared mutable state. Determinism IS the cross-construct contract (I12-
compatible): identities are seed+ordinal, only the loop bound is live. At default scaling (no
override) `liveCluster` reproduces the resolver-built fixture byte-for-byte, so `-once -dump`
output and existing tests are unaffected.

Scale-DOWN retires series via `state.DropWhere(pred)` against the high-water pod/node inventory
(state has no TTL). Only identities the construct KNOWS it minted (workload pod ordinals + derived
node hostnames) are dropped — substrate DaemonSet pods and node-exporter pods are never included.

An **`app`** workload places ONE cluster workload PER service node (each node is its own deployment),
so the per-cluster pod total — and thus the node cascade + the fleet-manager instances that key off
node identity — sums per service NODE. Each `ServiceNode` declares its own replica bounds; a node is
both a failure target and a scaling target on `AxisService`, so `POST /control/scaling` addresses
individual services and the cascade follows automatically (§3.2).

Read-replica and non-k8s scaling are deferred (no telemetry surface in v1).

### 3.4 The emission switch (which constructs a declaration fans into)

A resource declaration may fan into MULTIPLE constructs, and the blueprint gates *which* — without
touching construct code. The reference case is a database's `observability` block:

```yaml
observability: { cloudwatch: true, dbo11y: true, digests: 40 }
```

- `cloudwatch` (default **true**) → emit the `rds` CloudWatch lane (`aws_rds_*`).
- `dbo11y` (default **false**) → emit the `dbo11y_{postgres,mysql}` lane (`database_observability_*`).
- Omitting the block ⇒ CloudWatch only (back-compat). Setting **both false** is valid: the DB still
  exists as a workload call-target (its `db.*` span identity resolves) but emits no infra telemetry —
  the resolver always builds the `fixture.DB`, the switch only gates the construct instances.

This is the canonical answer to "some blueprints emit X, some don't": gate at the declaration, keep
constructs isolated and unconditional. `ValidateSet` claims a DB's substrate identity if it emits the
CloudWatch **or** the dbo11y lane (a CloudWatch-less dbo11y DB still needs cross-blueprint uniqueness),
deduped within a blueprint so a both-lanes DB is not a self-collision.

The same switch pattern is applied across resource declarations (the fixture is always built — it is
the workload call-target identity — and the switch only gates which construct instances emit):

| Declaration | Switch | Gates |
|---|---|---|
| `databases:` | `observability: { cloudwatch, dbo11y, digests }` | `rds` and/or `dbo11y_{pg,mysql}` |
| `caches:` | `observability: { cloudwatch }` | `elasticache` (false ⇒ call-target only) |
| `cluster:` | `observability: { cloudwatch }` | the per-node `ec2` CloudWatch lane (k8s substrate always emits) |
| `cloud:` | `cloudwatch: { nlb, ebs, nat_gateway, eks, firehose, albs, s3_buckets }` | `cw_infra` per-family sub-families |
| `integrations: { csp_azure/csp_gcp }` | `sub_signals: [...]` | per-cloud-service families |

Distinguish the two cluster lanes: `cluster.observability.cloudwatch` gates `aws_ec2_*` (the nodes' own
EC2 namespace, the `ec2` construct), while `cloud.cloudwatch.eks` gates `aws_eks_*` (the EKS control
plane, a `cw_infra` family) — independent toggles for independent families.

## 4. Two-cadence scheduler (per blueprint)

- **Master tick (fast, default 5s):** `Ledger.Mint(now)` returns the batch (volume is
  cadence-invariant: minters scale by `tickSec/30`); the runner immediately calls each workload's
  `ProjectBatch` with exactly its own minted requests → traces/logs/RUM emitted ONCE by
  construction, unfloored.
- **Metric lanes:** every construct/workload `Tick`s on its own `Interval()` (≥60s — DPM floor);
  workload metric lanes read `Ledger.ActiveFor(name, now, interval)`.
- ONE lane = ONE signal class. Span timing uses `r.RenderStart()`; ledger windowing keys on `r.Start`.
- **Per-blueprint goroutines (`Run`):** each blueprint runs its master + budget-reset tickers on its
  OWN goroutine, so a slow or hung sink push on one blueprint cannot delay another's cadence
  (blueprints share nothing but the concurrency-safe sinks; the dry-run inventory maps + the
  `coretest` captures are mutex-guarded). Individual pushes are bounded by each sink's `http.Client`
  timeout (15s) plus bounded retry-with-backoff (`internal/sink/httpretry`, modelled on Alloy's
  remote_write/otlp/loki behaviour — promrw + emit-once lanes; otlp excludes HTTP 500 per the OTLP
  spec); an optional `TICK_TIMEOUT` adds a coarse whole-tick backstop (default OFF). A blueprint that
  overruns the master period is surfaced via the `CycleFunc` dropped-tick metric, never by cancelling
  in-flight work. `RunOnce` (the `-once`/`-dump` verification path) stays SERIAL and deterministic.
- **Retry is at-least-once on the emit-once lanes (known, accepted property).** Retrying loki/otlp/faro
  pushes trades a dropped batch for a possible DUPLICATE: if the server ingested but the response was
  lost, the retry re-sends the same spans/logs. This is deliberately fine for synthetic data — there is
  no source of truth to reconcile against and duplicate synthetic spans/logs are harmless — so we prefer
  delivery over exactly-once. (promrw is naturally idempotent: re-pushing the same cumulative sample at
  the same timestamp is a no-op in Mimir.)

## 5. Scoping: blueprint-scoped vs substrate

Declared per construct kind at registration (`Scope`), enforced by the scoped writers:

- **ScopeBlueprint** — the writer stamps `blueprint=<label>` on every series/stream/span, cloning
  labels first (Collect() output aliases live cumulative state). Constructs NEVER stamp it.
  Applies to: workload signals, CloudWatch families, Cloudflare.
- **ScopeSubstrate** — NO blueprint label, ever (vendor-app conformance: fanning these per
  blueprint breaks `count by(cluster,…)` panels). Identity labels disambiguate (`cluster`,
  `account_id`, dbo11y instance identity) — all blueprint-declared and collision-checked at load.
  Applies to: k8s-monitoring substrate, k8s add-ons, dbo11y, CSP Azure/GCP, SM, Fleet/Alloy.

Definition-of-done restated accordingly: two concurrent blueprints separate by the `blueprint`
label on blueprint-scoped families and by declared identity on substrate families. A test asserts
no substrate series ever carries `blueprint`; the catalog-isolation test asserts no construct
source mentions any blueprint name.

**Cross-scope joins survive across concurrent blueprints.** A node's node-exporter/cAdvisor series
(substrate, no `blueprint` label) must join its CloudWatch EC2/EBS network & volume series
(blueprint-scoped) — operators correlate node-level OS metrics against the cloud's view of the same
instance. The join key is shared FIXTURE identity, resolved once: `fixture.Node.InstanceID` is
simultaneously the k8s `provider_id`/`kube_node_info` identity AND the `aws_ec2_*` `dimension_InstanceId`;
`fixture.Node` volume identity is the EBS-CSI attachment AND the `aws_ebs_*` `dimension_VolumeId`. Because
substrate identity is globally unique (cluster/instance names are collision-checked across enabled
blueprints), a given InstanceID/VolumeID belongs to exactly one estate — so its unlabeled substrate
series and its `blueprint`-labelled CloudWatch series join unambiguously no matter how many blueprints
run at once. Two blueprints modelling the *same kind* of infra each get their own unique identities;
two blueprints cannot claim the *same* instance (that collision is rejected at load). The `internal/cw`
refactor preserves these joins by construction: it changes only the stat-value expansion, never the
identity (`dimension_*`) labels. `internal/integration` asserts the EC2↔node and DB↔cloud joins;
EBS↔node is the natural next assertion as that fixture link is exercised.

## 6. Sinks (lifted mechanics)

| Sink | Carries | Key contracts |
|---|---|---|
| `sink/promrw` | ALL metrics (final pre-mangled names) | Prometheus **Remote-Write v2** wire format (`io.prometheus.write.v2.Request`); proto types vendored under `internal/sink/promrw/writev2` (provenance-tracked, regenerated via `make proto`); marshalled with `google.golang.org/protobuf` + snappy block compression; raw `http.Client`; symbols-table string interning (egress reduction); per-series `Metadata` stamped from `Series.Kind`; optional `Exemplars` field on the `Series` seam; per-push series budget + kill switch; per-blueprint budget in the scoped writer; dry-run inventory |
| `sink/otlp` | traces ONLY | hand-encoded ResourceSpans protobuf (multi-Resource per export, explicit timestamps); never the OTel SDK; never the collector/trace/v1 import |
| `sink/loki` | logs | 3-tuple `[ts, line, {meta}]`; ASSERTS no high-card key in Stream.Labels (per-sink forbidden sets — `model` legal in Mimir, forbidden as Loki stream label) |
| `sink/faro` | RUM beacons | POST to Faro collector with app key (4th credential surface; optional) |
| `sink/pyroscope` | Pyroscope profiles (hand-built pprof) | Pyroscope **push.v1 connect-unary** (`POST /push.v1.PusherService/Push`, `Content-Type: application/proto`, `Connect-Protocol-Version: 1`, basic auth — NO `X-Scope-OrgID`); proto types vendored under `internal/sink/pyroscope/pushv1` + `internal/pyroscope/pprofpb` (provenance-tracked, regen via `make pyroscope-proto`); inner pprof gzipped into `RawSample.raw_profile` (NOT the HTTP body); deterministic-per-tick `RawSample.ID` (retry-dedup); raw `http.Client` + `httpretry.EmitOncePolicy`; dry-run inventory; 5th credential surface `GC_PROFILES_*` (optional). The `internal/pyroscope` mechanic lib (peer of `state`/`cw`) builds the pprof `Profile` protos + per-runtime flamegraph vocab from `signals/profiles.md` |

**Delivery is decoupled from the tick (I41).** Every sink above is fronted by an in-memory delivery
queue (`internal/sink/queue`) wired in `runner.buildWorld`: the scoped writer's `Write` enqueues and
background senders batch (size-or-deadline) before calling the sink's real `Write`. This keeps the
synchronous push off the tick's critical path (a slow remote no longer stalls cadence) and collapses
many small per-construct round-trips into a few large batched ones. Per-identity sharding preserves
the cumulative-counter ordering (I3); the existing `pushhook` outcome seam still fires per real push
(now per batch). See I41 for the full contract (no WAL, backpressure, RunOnce flush, self-obs).

**`sink/promrw` — RW2 implementation notes.** Final pre-mangled metric names are unchanged: the name is simply the `__name__` symbol in the RW2 symbols table. The OTel metrics SDK is **banned** on the synthetic-data path — the promrw sink uses `google.golang.org/protobuf` (the protobuf runtime), not the metrics SDK. Per-series `Metadata.Type` is stamped from `Series.Kind`; classic-histogram component series (`_bucket`/`_sum`/`_count`) all carry `METRIC_TYPE_HISTOGRAM` because `state.Collect` stamps them `KindHistogram` — this is **deliberate**: RW2 per-series metadata is advisory and Mimir tolerates it, so do not "fix" it. A blocking `make rw-proto-check` target detects upstream RW2-proto drift.

`internal/state` is the sink-correctness layer (per-instance, no sharing, no mutex): cumulative
counters/monotonic gauges (`Add`), instantaneous gauges (`Set`), histogram expansion (`Observe` →
`_bucket{le}/_sum/_count` against pinned bounds with per-family `le` STYLE: `LEBare` minimal
decimals vs `LEDotZero` forced ".0" on integer bounds), `Reset` for synthetic restarts.

**Native (exponential) histograms:** `state.ObserveNative` / `ObserveDual` accumulate a cumulative
sparse exponential histogram (`internal/state/nativehist.go`); `Collect` emits it as a
`promrw.Series` with a non-nil `Native`, which the RW2 encoder writes to
`TimeSeries.Histograms`. Used ONLY for the Tempo-metrics-generator-derived span histograms
(`traces_spanmetrics_latency`, `traces_service_graph_request_{server,client}_seconds`),
dual-emitted alongside the classic `_bucket`/`_sum`/`_count` form (`signals/apm.md` SK-28). The
encoding is hand-built into the vendored `writev2.Histogram` — no OTel/client_golang SDK —
preserving the §6.1 SDK ban.

**Metric exemplar production.** Request-correlated histograms and counters carry `trace_id`
exemplars so a Grafana metric panel can click through to a real Tempo trace. The exemplars are
sourced from real `ledger.ActiveFor` requests — the same `*Request` objects that `ProjectBatch`
already shipped as spans — bridged into the metric lane (the two-cadence seam, I10). The
`webservice` workload calls `state.ObserveExemplar` / `state.CounterExemplar` for a small sample
(newest-first, capped at `state.MaxExemplarsPerSeries`/`webservice.maxExemplars` = 5 per series per
tick); `state.Collect` bins each exemplar onto its **landing bucket** (smallest `le` ≥ value —
OpenMetrics single-bucket convention) and drains the slice after every `Collect` (per-emit, not
cumulative). Exemplar label key is `trace_id` (RW2 best practice; matches Tempo metrics-generator +
Grafana exemplar→trace linking). The M4 high-card guard rejects `trace_id` as a **series label**
but does NOT inspect **exemplar** labels — `trace_id` in `Exemplar.Labels` is legal and safe.

Exemplar-carrying families:
- `traces_spanmetrics_latency` (histogram buckets) and `traces_spanmetrics_calls_total` /
  `traces_spanmetrics_size_total` (counters) — route-filtered so each route's latency buckets
  receive exemplars only from requests on that route (no cross-route smear); client-hop counters
  use all samples.
- `traces_service_graph_request_{server,client}_seconds` (histogram buckets) and
  `traces_service_graph_request_total` (counter).
- All `gen_ai_client_*` / `gen_ai_server_*` histograms (operation duration, token usage, TTFC,
  time-per-output-chunk) — every request traverses the AI hops, so all samples apply.

**Rules:** exemplars go on **histograms (buckets) and counters only** — never gauges (OpenMetrics
semantics). Exemplar value = the observation's own synthetic value; only the `trace_id` is real.
Exemplars are intentionally **not** surfaced in the `-dump` series inventory (not part of the
series-inventory / dashgen contract). RUM has no metric exemplars — Faro ships beacons (with their
own `TraceContext`), not Prometheus series.

**Span-metrics per-blueprint opt-in** (`World.EmitSpanMetrics`, **default OFF**): backend
spanmetrics + service-graph emission (`traces_spanmetrics_*` / `traces_service_graph_*` from the
webservice workload) is a **per-blueprint** control-plane opt-in. A blueprint is opted IN only when
its name appears in `State.SpanMetricsBlueprints` (`POST /control/spanmetrics` body
`{"span_metrics_blueprints":[...]}`, live-toggleable in the operator UI under "Span-metrics
emission"). Default empty list = all OFF, deferring to Grafana Cloud metrics-generator / beyla,
which produce the same `source="tempo"` families from the trace stream and own their own exemplars.
`World.EmitSpanMetrics` is set per-tick by the runner from the atomic control snapshot (keyed by
blueprint name); there is no global env var. gen_ai metrics are **never gated** —
metrics-generator/beyla do not produce gen_ai families.

`internal/cw` is the CloudWatch sibling of `state`: a shared mechanic lib (peer, in the construct
import allowlist) that owns the five-stat expansion (`cw.EmitStats` over a `cw.StatSet` →
`_sum/_average/_maximum/_minimum/_sample_count`), the per-period-GAUGE rule (every stat via
`state.Set`, never `Add` — I5), and per-suffix label isolation (I17). It owns NO value policy (each
construct fills the `StatSet` from the metric's real semantics) and deliberately does NOT generate
metric names (that would invent them — names are passed in, sourced from signals/). Every AWS
construct (ec2, rds, elasticache, cwinfra, and future families) delegates here.

### 6.1 Self-observability + profiling (the generator's OWN telemetry)

Distinct from everything above (which is SYNTHETIC data the generator emits), `internal/selfobs`
and `internal/profiling` instrument the synthkit PROCESS itself and ship to a SEPARATE Grafana Cloud
stack over their OWN credential triplets (`GC_SELF_OTLP_*`, `GC_PYROSCOPE_*` — NEVER `GC_TOKEN`).
Both are default-off and decoupled from `DRY_RUN` (profiling the process is unrelated to whether
synthetic data is being pushed). Wired only in `cmd/synthkit/main.go`.

> **Do NOT confuse `GC_PYROSCOPE_*` with `GC_PROFILES_*`.** `GC_PYROSCOPE_*` (here) profiles the
> synthkit PROCESS via the pyroscope-go SDK → a SEPARATE staff stack. `GC_PROFILES_*` (§6,
> `sink/pyroscope`) ships SYNTHETIC profiles (hand-built pprof, no SDK) to the configured TARGET
> stack — it is the synthetic-data path, gated by `config.SynthProfilesEnabled()`, and obeys every
> synthetic-path rule (no OTel/pyroscope-go SDK; `archtest.TestSinkRunnerSDKIsolation` enforces it).

- `internal/selfobs` is the **sole sanctioned OTel SDK user** in the repo (the "OTel metrics SDK is
  banned" rule governs the synthetic path; this is its one exception, for the process's own
  telemetry to a different stack). It emits push RED metrics, Go runtime metrics, per-tick traces,
  and the operational log stream over one OTLP endpoint. It builds its OWN Tracer/Meter/Logger
  providers and **never installs them as OTel globals** — so the synthetic `sink/otlp` (which
  hand-encodes proto and bypasses the global API) is wholly unaffected. Disabled/under-configured ⇒
  a no-op handle; the generator behaves byte-for-byte as without it.
  - **gen_ai on the synthetic path (Spec 2b) is NOT an SDK exception.** The synthetic path may now
    carry `gen_ai.*` trace spans and `gen_ai_client_*`/`gen_ai_server_*` metrics, but still via the
    SAME seams as everything else: spans via the hand-encoded `sink/otlp` ResourceSpans, metrics via
    `sink/promrw` final names. `internal/genai` is gen_ai semconv VOCABULARY (constants + builders),
    not an SDK; the OTel metrics SDK ban on the synthetic path is unchanged.
- Three **stdlib-only seams** keep the SDK out of the synthetic-data path entirely:
  - `internal/pushhook` — each sink exposes an `Observe pushhook.Observer` field (nil = unchanged
    push path), set only by main when self-obs is on. The sink reports one `pushhook.Event` per push.
  - `runner.TickFunc` — the runner wraps every instance `Tick`/`ProjectBatch` through an optional
    stdlib callback (`selfobs.ObserveTick`), so it can be span-wrapped + timed without the runner
    importing the SDK. nil ⇒ fn is called directly; error is returned unchanged either way.
  - `runner.CycleFunc` — the runner reports each completed per-blueprint master cycle (blueprint
    name, wall-clock duration, dropped/coalesced-tick count) through an optional stdlib callback
    (`selfobs.ObserveCycle`). nil ⇒ no-op. It surfaces the dropped-tick failure mode — a missed tick
    is a missed `Ledger.Mint`, silently undercounting volume — without the runner linking the SDK.
- `internal/profiling` ships continuous Pyroscope profiles (CPU/heap/goroutines, + mutex/block when
  their runtime sampling rate is set). `ApplicationName=synthkit`.

Constructs/workloads must NEVER import the OTel SDK, `selfobs`, `profiling`, or `pushhook` — the
isolation is the same three-tier rule (grep-tested by `archtest`; the self-obs packages are imported
only by `cmd/synthkit/main.go`, the sinks import only `pushhook`). `archtest.TestSinkRunnerSDKIsolation`
additionally asserts that `internal/sink/*` and `internal/runner` import neither the OTel SDK
(`go.opentelemetry.io/otel`), Pyroscope, `selfobs`, nor `profiling` — the OTel *proto* package the
`sink/otlp` hand-encodes with is a distinct module and stays allowed.

## 7. Invariants (each enforced by types/asserts/tests — never comments alone)

Metric mechanics
- **I1** ALL metrics via remote_write with final pre-mangled names; OTel metrics SDK banned **on the
  synthetic-data path** (the sole exception is `internal/selfobs` for the generator's own telemetry —
  I34).
- **I2** OTLP carries traces only; hand-encoded protos; multi-Resource exports.
- **I2b** Pyroscope profiles go via `sink/pyroscope` (push.v1 connect-unary, vendored pprof/push protos,
  hand-built — never the pyroscope-go SDK on the synthetic path). Profile↔trace **span profiles** ride the
  I10 `ledger.ActiveFor` bridge and are **workload-lane only** — constructs never tag `span_id` (I9). The
  substrate `k8s_profiling` construct carries no blueprint label (I21); workload SDK-push lanes are
  ScopeBlueprint. Profile-type strings + labels are sourced from `signals/profiles.md` (I33), never invented.
- **I3** Counters/histograms are CUMULATIVE across ticks — push running totals, never deltas.
- **I4** `le` formatting is ingestion-path-dependent (LEBare vs LEDotZero); some span-metric
  families are NATIVE histograms (no `_bucket`/`le`) — honor whichever form the real source emits.
- **I5** CloudWatch `_sum` series are PER-PERIOD GAUGES — never `rate()`; constructs document it.
- **I6** CloudWatch naming convention is law (`aws_<ns>_<metric snake, consecutive-caps kept>_<stat>`;
  `dimension_<Name>` preserving CW casing; traps: `cpuutilization`, `5_xx`, `un_healthy`, `ebsread_bytes`).
- **I7** Per-push series budget + kill switch, global AND per-blueprint.
- **I8** RNG is goroutine-safe (`math/rand/v2`) — no shared `*rand.Rand`.

Correlation & identity
- **I9** Request-scoped IDs minted ONLY by the ledger; constructs/workloads read, never mint.
- **I10** Two cadences: fast master mint returning the batch; ≥60s metric ticks reading Active();
  trace/log lanes handed the exact batch, emitted once by construction.
- **I11** Deterministic per-request RenderOffset from TraceID; `Start` stays the windowing key.
- **I12** Deterministic fixtures: sha256 of stable seeds; EC2↔node and DB↔cloud identity resolved
  ONCE by the composition root.
- **I13** No sentinels: an absent dimension is OMITTED — never `""`/`"NA"`.

Cardinality & labels
- **I14** UUID-class keys are NEVER Mimir labels or Loki stream labels — attrs/structured metadata
  only; the Loki sink asserts per push.
- **I15** Per-sink forbidden label sets (legal-in-Mimir ≠ legal-in-Loki-stream).
- **I16** When a real exporter's defaults under-disambiguate (KSM kube_ingress_*), inject `cluster`.
- **I17** The blueprint selector is stamped in exactly ONE place — the scoped writer, which clones
  before stamping (Collect() aliases live label maps).

Composition
- **I18** Constructs contain ZERO blueprint-name references (grep test over the catalog).
- **I19** NOTHING is derived from a blueprint's positional index; determinism seeds are
  name-derived. (The predecessor's trace-ID nonce is retired: per-blueprint ledgers mint independent
  random trace IDs, so cross-blueprint Frankentraces cannot occur.)
- **I20** No fold/strip transform layer exists — blueprints declare topology directly.
- **I21** Scope is explicit per construct kind (§5); substrate families never fan per blueprint.

Conformance & content
- **I22** Vendor-app conformance = exact discovery/join series (build_info with "v"-prefixed Alloy
  version, helm/agent build_info, cluster+job labels on logs, OpenCost/Kepler). Constructs own
  version canonicalization.
- **I23** Content/PII strip is first-class: no content-bearing fields, ever; emit the proof
  sentinel (`synthkit_content_leak_test` 0-gauge / `synthkit_content_dropped_total`).

Ops
- **I24** Control-plane JSON round-trips zero/false — NO `omitempty` on knobs; regression test.
- **I25** Docker state mount = DIRECTORY bind, owned by uid 65532.
- **I26** Control CORS echoes request headers; GETs side-effect-free.
- **I27** `.env` parser strips inline `# comments`.
- **I28** Generators write artifacts to explicit `--out` paths, never via stdout redirect.
- **I29** Container restart = counter reset = clean rate() window (no counter state volume);
  control-plane state DOES persist (/data dir).
- **I30** Self-observability uses its OWN credential triplet behind a seam.

Shape & verification
- **I31** Diurnal is a flat-topped business-hours PLATEAU; weekends down; non-prod →~0 weekends;
  incidents schedulable/staggered/time-boxed with end-to-end request correlation intact.
- **I32** `DRY_RUN` + `-once -dump` prints the FULL inventory of distinct series names + label keys
  for offline diff against signals/.
- **I33** the signals/ catalogue (indexed by SIGNALS.md) is the authoritative contract; per-construct adversarial review of emitted
  data against it at each wiring checkpoint.

Self-observability (the generator's own telemetry — §6.1)
- **I34** `internal/selfobs` is the SOLE OTel SDK user, for the synthkit PROCESS's own telemetry to a
  SEPARATE stack via its own credential triplet (never `GC_TOKEN`); default-off; decoupled from
  `DRY_RUN`; never installs OTel globals. It reaches the synthetic path only through two stdlib-only
  seams — `pushhook.Observer` (sink push outcomes) and `runner.TickFunc` (per-tick spans) — so the
  sinks and runner never link the SDK. nil seam ⇒ byte-for-byte unchanged behaviour. Constructs/
  workloads NEVER import the SDK, `selfobs`, `profiling`, or `pushhook`.

Incident model + live scaling (§3.2–3.3)
- **I35** Incident DEFINITION (construct vocabulary + blueprint scenarios) is completely separate from
  ACTIVATION (control-plane `POST /control/scenarios` or scheduled windows). A construct's physics
  fire only when the runner's `shape.Engine.Live` hook or a scheduled entry names that mode — the
  construct never polls the control plane.
- **I36** `GET /control/schema` is always DERIVED from the loaded blueprints + construct registry
  (modes, targets, scenarios, live scaling state). No hardcoded descriptor list is returned when a
  `SchemaSource` is wired. Scenario POSTs and scaling POSTs are unavailable (400) when no
  `SchemaSource` is configured.
- **I37** Live pod scaling moves ONLY the ordinal loop bound in `liveCluster`; it never mutates
  shared fixture state. Node count cascades from the same pure `fixture.DeriveNodes` call both
  k8scluster and ec2 use — cross-construct agreement is guaranteed by shared determinism, not shared
  state (I12-compatible). Scale-DOWN retires series via `state.DropWhere`; state has no TTL.
- **I38** `internal/failuremode` and `internal/scale` are leaf packages (no synthkit-tier imports)
  and are in the archtest construct/workload import allowlist. Constructs/workloads MUST NOT import
  any other runner/blueprint/control/selfobs package.

Custom-telemetry DSL (`app` workload — §2)
- **I39** The DSL capability matrix is load-enforced: a high-card `ref` (the `internal/highcard`
  canonical set — `trace_id`/`span_id`/`request_id`/`session_id`/`correlation_id`/`run_id`/`plan_id`/
  `user_id`/`portkey_trace_id`) is REJECTED in any metric label or Loki stream-label and allowed ONLY
  in a log body / span attribute. `internal/highcard` is the single source of truth shared by the
  loki + promrw sinks AND the DSL validator — they cannot drift (agreement test). `internal/highcard`
  and `internal/telemetryspec/` are in the archtest allowlist (mechanic libs, like cw/genai).
- **I40** A DSL value model feeding a metric/stream LABEL must be `const`/`const_str`/`enum` with a
  total, non-empty domain (enum labels expand to the FULL cross-product every tick); `int_range`/
  `normal`/`ref`/etc. are rejected as label sources — so the `-once -dump` name+label-key inventory
  is run-stable (I32). Metric VALUES, log body fields, and span attribute values are unconstrained.
  The `app` workload auto-stamps node identity on every series/stream/span before author labels, so
  two nodes sharing a profile never produce duplicate series.
- **I41** Sink DELIVERY is decoupled from EMISSION. Each signal's scoped writer feeds a bounded
  in-memory queue (`internal/sink/queue`, a `core.{Metric,Log,Trace,Pyroscope}Writer` decorator):
  a construct's `Write` enqueues (non-blocking under capacity) and background senders batch by
  size-or-deadline before calling the real sink — so a slow remote can never stall the tick cadence
  (the cause of the multi-minute cycles / bursty DPM this replaced). Items are sharded by IDENTITY
  (metric name+labels, stream labels, profile labels; traces round-robin since Tempo is order-free),
  so successive snapshots of one series always reach the same ordered sender — preserving the
  timestamp order cumulative counters (I3) require. There is NO disk WAL (synthetic data regenerates
  on restart). Backpressure on a full queue blocks the tick (never drops) and is surfaced via a
  rate-limited WARN log + the `synthkit.queue.enqueue_blocked` self-obs counter. `RunOnce` `Flush`es
  synchronously before returning (so `-once` delivery is complete and surfaces push errors); the
  `-once -dump` inventory is recorded pre-queue and is unaffected. The queue is stdlib-only and never
  links the OTel SDK (preserving the §6 ban); self-obs reads it via the stdlib `queue.Observer` seam
  and the runner's `QueueDepths()` provider (`synthkit.queue.depth`).

## 8. Module layout

```
synthkit/
├── cmd/synthkit/            main: config → load blueprints → registry → runner loop
├── cmd/sm-provision/        one-shot Synthetic Monitoring provisioner (offline probe + checks)
├── blueprints/              example blueprints (k8s-minimal.yaml, k8s-full-stack.yaml, acme-ai-platform.yaml, …) — committed
├── ARCHITECTURE.md  SIGNALS.md (index)  signals/ (per-area contract)  CLAUDE.md  README.md  cantfind.md
├── docs/superpowers/        gitignored scratch (plans/specs — never committed)
└── internal/
    ├── core/                frozen seams: SignalClass, Scope, World, Construct, Workload, registry
    ├── config/              .env + process-env loader (DRY_RUN default true, inline-# strip; self-obs/profiling triplets)
    ├── fixture/             shared-identity vocabulary + deterministic seed helpers
    ├── ledger/              request-correlation master clock (Minter, Request, ring)
    ├── shape/               diurnal plateau + weekly + noise + schedulable incidents
    ├── state/               sink-correctness layer (cumulative counters, histogram expansion, le styles)
    ├── cw/                  shared CloudWatch stat-expansion mechanic (StatSet/EmitStats; peer of state)
    ├── failuremode/         failure-mode vocabulary types (Mode/Axis/MultiAxis); leaf — no synthkit-tier imports
    ├── scale/               live scaling overrides (atomic snapshot; lock-free tick hot path); leaf
    ├── sink/{promrw,otlp,loki,faro}   each carries an optional pushhook.Observe field (self-obs seam; nil = unchanged)
    ├── pushhook/            stdlib-only sink↔self-obs seam (push outcomes; no SDK, no cross-imports)
    ├── selfobs/             self-observability: OTel SDK → SEPARATE stack (sole SDK user; §6.1)
    ├── profiling/           continuous Pyroscope profiles → SEPARATE stack (§6.1)
    ├── blueprint/           YAML schema + strict loader + topology resolver + ValidateSet (wiring layer)
    ├── runner/              composition root: catalog wiring, scoped writers, two-cadence scheduler
    ├── construct/<kind>/    the catalog — one package per construct, zero cross-imports
    ├── workload/webservice/ the v1 workload (RED + traces + logs + optional RUM)
    ├── control/             schema-driven control plane + embedded operator UI    (Phase 6)
    ├── jsondata/            in-process Infinity JSON host                          (Phase 6)
    ├── fleet/               fake Fleet Management collector registration          (Phase 6)
    │                        (runner→fleet→construct/fleetmgmt is the one sanctioned edge: roster reuse)
    ├── archtest/            isolation + catalog-isolation + import-allowlist tests
    └── integration/         cross-construct identity-join + scoping DoD gate
```

AI/LLM is IN scope (Spec 2b ban lifted): tech-native AI metric constructs (bedrock, agentcore,
portkey_gateway/poller, langsmith_platform/eval, snowflake) plus a workload-AI lane that attaches
gen_ai trace hops + `gen_ai_client_*`/`gen_ai_server_*` metrics + correlated AI logs via the
Workload seam + the ledger (exactly as predicted). The gen_ai vocabulary lives in `internal/genai`
(a mechanic lib, peer to `internal/cw`). The OTel-metrics-SDK ban still holds on the synthetic path
(gen_ai metrics go via promrw final names; spans via the hand-encoded OTLP seam — §6.1). All
customer identity stays blueprint-only.

## 9. Verification

1. `go build ./... && go vet ./... && go test ./...` green at every wiring checkpoint and commit.
2. `DRY_RUN=true synthkit -once -dump` → series/label inventory diffed against signals/.
3. De-Rochification gate: grep test (no customer names / customer-specific strings in the catalog) + substrate
   no-blueprint-label test + construct-imports test (no construct imports another construct).
4. Per-construct adversarial review (fresh-context Opus) of emitted data vs signals/.
5. Live push smoke test (validated): metrics/traces/logs + Fleet collector registration confirmed in
   Grafana Cloud via gcx. Runbook: [docs/RUNBOOK.md](./docs/RUNBOOK.md).
