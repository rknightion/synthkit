---
title: Blueprints Overview
description: What a blueprint is, how it loads, and how one YAML declaration fans into multiple telemetry constructs.
---

# Blueprints Overview

A blueprint is a single deletable YAML file that declares a synthetic estate. It is a telemetry model, not an infrastructure or application deployment manifest: synthkit emits the resulting signals but does not create the declared resources. Delete the file and only that blueprint's telemetry disappears — constructs know nothing about blueprints, so removing one affects nothing else.

Bundled `*.yaml` files under `BLUEPRINTS` (default `./blueprints`) are available at startup but are
not selected automatically. Empty or unset `BLUEPRINT_NAMES` starts setup mode and emits nothing.
Set it to a comma-separated list of exact runtime identities to load only those blueprints, or to
`*` to explicitly load the complete available catalog; unselected files are not fully decoded or validated.
Decoding remains strict for every selected blueprint: any unknown top-level key, unknown construct
kind, or unknown field inside a construct's config is a loud load error, not a silent ignore.

!!! warning "The high-DPM reference is intentionally cost-bearing"
    `high-dpm-churn` is a small detector-test blueprint, not a normal fidelity example. It projects
    115 series at 6 DPM per series, or 690 data points per minute, and replaces one declared topology
    edge identity per minute. Select it explicitly with `BLUEPRINT_NAMES=high-dpm-churn`. It is not
    registered in the reality corpus because its deliberately over-scraped cadence is not collector
    fidelity evidence. Startup logs and `/control/schema` report the resolved projection.

## Where blueprints come from

- **Bundled:** files shipped in `BLUEPRINTS` are available on the next startup. In a checkout, copy
  a bundled example to another `*.yaml` file in that directory only when you intentionally maintain
  a local bundled-style blueprint.
- **Selected directory:** set `BLUEPRINTS` to a directory containing the `*.yaml` files you want
  synthkit to load at startup. This replaces the bundled-directory location; it is not a staging
  directory.
- **Custom paste/upload:** submit YAML through the control-plane upload endpoint or UI. synthkit
  validates it and writes it below `BLUEPRINT_DATA_DIR/custom/`; it is **staged** and takes effect
  after restart. Do not copy files into the root of `BLUEPRINT_DATA_DIR`: that root is manager
  metadata, not a source directory.
- **Git source:** configure a source through the control plane, fetch it, then restart. Fetched YAML
  is staged below `BLUEPRINT_DATA_DIR/git/<source-id>/`; do not write there by hand.

Use `GET /control/blueprints/staged` to see staged custom/git blueprints and
`GET /control/blueprints/pending` to see whether restart will change what is running. See
[Custom blueprints](custom-blueprints.md) for the request formats.

## Captured CloudWatch consumers

Optional environment blocks `cloud.lambda`, `cloud.sqs`, `cloud.dynamodb`, `cloud.redshift` and
`cloud.cloudwatch_logs` each build an
independent blueprint-scoped metrics construct. Omit the block to emit nothing for that kind.
They are **remote-write only**: `cloudwatch_export: otlp` fails Build, without fallback or aliases.
AWS does have a native Metric Streams form, but these consumers do not yet implement it. The
[canonical emitted contract](https://github.com/rknightion/synthkit/blob/main/signals/cw.md#initial-captured-service-emitters-slug-cw-captured-emitted)
lists the 51 admitted bases (35 original plus ten Redshift, two Logs intake and four optional Lambda delivery observations; plus three opt-in DynamoDB throttle bases) and exact alternative dimensions separately from positive source tables.
ESM/fair-queue/info families remain withheld. There are no pipeline adapters, failure modes or
cross-service request joins in this subset.

Declare **at most one block per blueprint/account/region/kind**, putting all resources in its
`functions`, `queues`, `tables`, `clusters` or `log_groups` list. A second environment in the same account/region rejects
even with different resources: account snapshots and resource identities must not be duplicated.
Different kinds, account/regions and blueprint scopes remain distinct. The scoped writer alone
adds `blueprint`; environment names do not distinguish these CloudWatch series.

All inputs are synthetic operator assumptions, not captured numeric fidelity. For the original
Lambda/SQS/DynamoDB configs, omitted numerical traffic inputs are zero, not hidden traffic defaults.
Redshift snapshot fields and Logs rate/size fields require explicit presence (zero/false is valid);
Redshift read/write I/O blocks are optional and their omitted rate is zero. Positive master volume keeps the configured
fixed rates; volume zero emits nothing, freezes active carry and advances the observation fence.
The first observed positive tick after a pause is a silent boundary, not catch-up. First ordinary
startup assumes one preceding minute; later observations use bounded elapsed windows, not replay.
Sparse families need not appear in a single dump; never raise shipped rates to force them.
Use the deliberate all-family test populations only in `e2e/fixtures/e2e-cw-consumers.yaml` and
`e2e/fixtures/e2e-cw-remaining.yaml` (intake/snapshots), and
`e2e/fixtures/e2e-cw-delivery.yaml` (independent delivery observations).

```yaml
# Example cloud block: small application assumptions, not capture statistics or AWS defaults.
cloud:
  provider: aws
  account_id: "111122223333"
  region: us-east-1
  vpc_id: vpc-example
  lambda:
    reserved_concurrency: 2 # Two explicitly reserved slots outside the unreserved function.
    functions:
      # One synchronous task every two minutes, taking 20 ms, with no async/stream traffic.
      - {name: example-handler, invocations_per_min: 0.5, duration_ms: 20}
  sqs:
    queues:
      # A small worker handles one 128-byte message per 100 seconds; polls every 10 seconds.
      - {name: example-jobs, collector_name: declared-jobs, arrivals_per_second: 0.01, receives_per_second: 0.1, message_bytes: 128, processing_seconds: 2}
  dynamodb:
    # Explicit application inventory quota assumptions with headroom, not AWS default quotas.
    account: {max_reads: 1000, max_writes: 1000, max_table_reads: 500, max_table_writes: 500}
    tables:
      - name: example-orders
        collector_name: declared-orders
        provisioned_reads: 10 # Ten reads/s reserved for this small provisioned application.
        provisioned_writes: 5 # Five writes/s reserved; no write traffic declared here.
        # One successful two-item eventual-read query every ten seconds, 4 ms service time.
        operations: [{name: Query, requests_per_second: 0.1, read_units: 0.5, returned_items: 2, latency_ms: 4}]
```

Lambda accepts unqualified function names only; `name=global` is the sourced collector profile,
not a generated ARN. Total invocation rate must cover admitted async (received minus dropped) plus
stream batch rates; the residual is synchronous traffic. Counts/ages use those disjoint populations.
Reserved concurrency is a whole count; duration/ages are fixed per observation. Lambda has no
resource-tag/ESM/Resource alias config.

SQS and DynamoDB require explicit non-global `collector_name`, never an ARN guessed from resource
names. SQS models one-message successful receives/deletes, declared delay/processing time, whole
bytes, four-day retention and six-hour activity retention. It does not model retries, DLQs or fair
queue groups; optional `enrichment` accepts only explicit supported P2 metadata keys. A queue may set
`resource:` to a declared `queue` resource (`environments[].resources`); the resource then owns the
queue's traffic, so a bound queue omits `arrivals_per_second`, `receives_per_second`,
`processing_seconds`, `delay_seconds` and `load`, and the resource omits `max_depth` (a standard
queue is unbounded). Per window, sends are the model's arrivals, receives and deletes what its
consumers served, visible messages the backlog and the oldest-message age the age of the head of the
queue, so `dependency_slow` on the dependency the consumers hold each message across grows both and
they drain after the fault; `consumer_slowdown` on the queue does the same. A bound queue does not
emit NotVisible, Delayed or EmptyReceives, and one resource binds at most one queue. DynamoDB
requires positive provisioned capacities/quotas; its account snapshots have no table dimensions.
`write_source` is omitted, `Customer` or `GlobalTable`; the last requires independent `replication`.
Table operations are GetItem, PutItem, UpdateItem, Query, Scan and SELECT-only ExecuteStatement.
UpdateItem is a write operation declared separately from PutItem (write units only, latency only; the
write demand is split between them, never added to). Returned items apply only to the three selection operations. An optional `stream` declares its own `label` and
GetRecords polling; StreamLabel is absent from ordinary operations. Whole returned items/records/
bytes and fractional read units/rates are distinct. Capacity samples may exist at zero traffic;
latency/returned observations are sparse. Floating-point floors may delay a decimal-boundary
request; this is not measured throughput or a promise of numerical fidelity.

Redshift `clusters[]` require explicit `name`, non-global `collector_name` and all six `snapshot`
assumptions: `cpu_percent`, `database_connections`, `healthy`, `maintenance`, `disk_used_percent`,
`total_tables`. Percentages are 0–100, not fractions; counts are whole. Stocks have sample_count=1.
Optional `wlm_queues[]` require `name`, `waiting`, `running`; QueueName never leaks to cluster
snapshots or latency. Optional independent `read_io`/`write_io` declare `operations_per_second`
and required positive `latency_seconds`; N completed synthetic I/O observations produce sum=N×L,
sample_count=N and average/min/max=L **seconds**, without unit scaling. No I/O observations means
no latency family. No NodeID, QueryType/user-query, throughput, managed-storage exception, automatic
cluster pause, pipeline or cross-service attribution is added.

Logs `log_groups[]` require explicit unique `name`, `events_per_second` and whole
`uncompressed_bytes_per_event`. Zero rate emits neither intake family. For N uploads of size B,
event sum/count=N and average/min/max=1; byte sum=N×B, count=N and average/min/max=B.
Source event unit is AWS **None**, not Count; bytes are uncompressed. LogGroupName is the only
vendor dimension for intake and `name=global` is the selected collector projection. No bodies,
Loki output, compression ratio, by-data-source, info or lifecycle joins follow.

Optionally declare `subscription_filters[]` under an existing log group. Each filter needs a unique
unpadded `name` within that group, `destination_type: Lambda` (case-sensitive; alternatives reject)
and all four `period_observation` values: `delivery_errors`, `delivery_throttling`,
`forwarded_log_events`, `forwarded_bytes`. All must be finite whole nonnegative numbers; absence
rejects and explicit zero is valid. These independent authored observations mean errored events,
throttled events, forwarded events and **already-compressed bytes** respectively, not terminal
loss, API requests, invocations or incoming bytes. There is no inferred compression ratio,
retry/duplication/loss partition, batching, lag or relationship to intake or each other. This is
**period-observation synthesis, not a delivery simulator**.

Each filter observes each value x exactly once per synthetic active minute: sum/average/min/max=x
and sample_count=1, not the event or byte magnitude. Zero is an explicit synthetic observation,
not an AWS idle-publication guarantee. A group with zero intake still progresses and emits its
explicit delivery observations. Nonzero master amplitude never scales these authored values.
The first ordinary active tick admits one bootstrap period, not measured historical delivery;
later ticks retain exact active-nanosecond fractions, cap only new progress at 60 seconds and
emit at most one period per tick without gap replay. The pause/resume/error rules below apply.
Omitting filters leaves previous intake output unchanged. For a fully populated **offline-only**
example, select `e2e/fixtures/e2e-cw-delivery.yaml`; its deliberately independent values are not
production defaults. Supply deployment-justified observations rather than deriving them from intake.

The selected Redshift P18/P14 and Logs P36 (intake)/P21 (Lambda delivery) vendor-dimension
projections omit captured enrichment; they are not complete collector envelopes or native evidence.
Delivery uses exactly LogGroupName+FilterName+DestinationType, without tags, ARN, function identity
or a cross-service join. P24 only corroborates the Lambda literal; it does not authorize a tag union.
SELECT is now observed as a Redshift P13 literal, but its numerical population/timing/statistic
semantics remain held; do not author a user-query emitter.

These two consumers retain exact fractional carry over decoded binary-float rates and integer
nanoseconds; decimal-intent boundaries may be delayed, never rounded up with an epsilon. Only
new accrual is capped at 60 seconds; earned fractions survive jitter/gaps and observed pause.
Repeated/backward timestamps emit nothing. An initially paused instance does not bootstrap on
resume. Pause/resume entirely between observations is unobservable; writer failure consumes the
attempted window with no replay, durable checkpoint or backend-readback guarantee. Supply stocks,
rates, latency and event sizes from the estate being modelled, with a real-world basis beside
any shipped values; the coverage fixture is not a production default.

See [field reference](blueprint-reference.md#captured-cloudwatch-config) and the generated schema
for the complete accepted keys and engineering bounds. Unknown fields reject, including withheld
family switches. Validate via the ordinary selected-directory CLI and compare actual dump inventory
to the canonical emitted blocks, not the larger source tables.

## Anatomy of a blueprint

A blueprint has a small set of top-level keys that you control at the YAML level, plus the sections that fan into constructs.

### Identity and shape

| Key | Purpose |
|---|---|
| `name` | Unique identifier; also the determinism seed root. Required. |
| `label` | The selector value stamped on every blueprint-scoped series. Defaults to `name`. |
| `metadata` | Human-facing annotation (description, tags, owner, links, category). UI-only; never affects emission. |
| `shape` | Default shape profile for the whole blueprint (e.g. `business_hours_plateau`). |
| `timezone` | Business-hours anchor for the diurnal curve. Default `Europe/Zurich`. Mutually exclusive with `regions`. |
| `regions` | Follow-the-sun multi-timezone composite: a list of `{name, timezone, weight}` entries. Mutually exclusive with `timezone`. |
| `series_budget` | Optional per-blueprint data-point allowance for each fixed one-minute budget window. A high-DPM blueprint must fund repeated samples of the same series. |
| `high_dpm.metric_interval` | Explicit cost-bearing override for every metric-bearing instance in this blueprint. Omit it to keep the default 60-second floor. |

### Environments

`environments` is a list of named deployment environments. Each environment carries its own cloud account, optional cluster, databases, and caches. The `weight` key scales traffic volume relative to other environments; `production: true` keeps weekend traffic up (defaults to true when `name == "prod"`).

Within an environment:

- `cloud` — AWS account, region, VPC, and the `cloudwatch` sub-family toggles (ALBs, EBS, NAT Gateway, S3, Firehose, EKS control-plane, PrivateLink). Also the home for cloud-service configs: `aoss`, `mwaa`, `glue`, `bedrock`, `agentcore`, and the remote-write-only `lambda`, `sqs`, `dynamodb` consumers.
- `cluster` — EKS cluster name, node groups, k8s-monitoring config (Alloy, OpenCost, Kepler, Fleet Management, feature gates, control-plane), add-ons, and cluster-level `observability` switches.
- `databases` — one entry per RDS/DocumentDB/Neptune instance: engine, version, name, instance class, and the `observability` block that gates the CloudWatch lane and/or the Database Observability lane.
- `caches` — ElastiCache clusters with engine, version, name, and CloudWatch gate.

### Per-agent Bedrock application inference profiles

`environments[].cloud.bedrock.application_profiles` optionally models one synthetic application
inference profile per agent/model association. It does not provision AWS resources. Profile IDs are
fictional opaque alphanumeric strings. The same `(account_id, region, profile ID)` must not be
declared in multiple environments within a blueprint: those environments would produce identical
series, so loading rejects the collision rather than coalescing them. `agent`, `provider` and `model`
remain configuration associations, never added CloudWatch dimensions.

```yaml
cloud:
  provider: aws
  account_id: "000000000000" # Fictional account.
  region: eu-west-1
  vpc_id: vpc-example
  cloudwatch_export: remote_write # Profile OTLP is not admitted yet.
  bedrock:
    sub_signals: [application_profiles] # Do not inherit generic model/Agents/guardrail/log families.
    application_profiles:
      - id: synsource01
        agent: source
        provider: bedrock
        model: anthropic.claude-haiku-4-5-20251001-v1:0
        # Synthetic sizing: 30 writers × 12 sections per 8-hour day / 480 minutes,
        # with one source-model call per section. Constant rate, not capture-derived.
        requests_per_min: 0.75
        input_tokens_per_request: 1200 # Operator assumption: a short source-note bundle.
        output_tokens_per_request: 250 # Operator assumption: a short structured summary.
        latency_ms_per_request: 900 # Synthetic constant latency, not a vendor benchmark.
        estimated_tpm_quota_usage_per_request: 1700 # Explicit synthetic quota estimate, not a token/pricing formula.
```

All traffic/value fields above are required and positive; there are no production-volume defaults.
`provider` must be `bedrock`; `model` must retain a catalogued Bedrock foundation model identity.
Declare the same provider/model separately on the generation workload for Agent Observability
pricing: **never replace its generation model with the profile ID**. No automatic binding or
cross-construct rewrite is performed. Additional profiles may reference the same foundation model
without merging their independent `ModelId` dimensions.

Only the five captured `AWS/Bedrock` bases are modelled: `Invocations`, `InputTokenCount`,
`OutputTokenCount`, `InvocationLatency` and `EstimatedTPMQuotaUsage`. `dimension_ModelId` carries
the profile ID. Whole requests are scheduled in UTC minute windows, so a low fractional rate has
idle windows rather than being raised to one request every tick. Per-request values are constant:
Sum is value × requests, Average/Minimum/Maximum are the configured value, and SampleCount is the
request count. Invocations use value 1. Every statistic is a per-period gauge, not a counter.

Omitting profiles preserves legacy Bedrock output. Omitting `sub_signals` includes both configured
profiles and the legacy families on their separate identities. No agent/team tag labels, profile
logs, TTFT, cache, errors/throttles, Bedrock Agents or guardrail telemetry is inferred for profiles.
Profile declarations with `cloudwatch_export: otlp` fail closed (even if excluded by `sub_signals`):
raw profile metric-stream parity and collector enrichment remain **PENDING**. Remote-write output
is an offline projection using `name=global`, not proof of live metric-stream ingestion. See the
[signal contract](https://github.com/rknightion/synthkit/blob/main/signals/bedrock.md)
for the API capture boundary and the unsupported lanes.

### Workloads

`workloads` is a list of application workloads that emit request traffic (metrics, traces, logs, optional RUM). Each entry has a `type` (workload kind), a `name`, a `runs_on` reference that binds it to a cluster, and kind-specific config. The built-in kinds are `web_service` (a single service), `app` (a declared multi-service graph with a custom-telemetry DSL), and `ai_agent` (correlated agent sessions and model calls).

See [workloads.md](workloads.md) for the per-kind detail.

### Features and integrations

`features` is a map of registered Grafana Cloud product kinds (for example, `synthetic_monitoring` and `fleet_management`). `integrations` is a map of registered external-source kinds that Grafana Cloud ingests (for example, `cloudflare`, `csp_azure`, `csp_gcp`, `portkey_gateway`, and `langsmith_eval`). Each key must name a supported kind in the matching section; the mapping may contain any supported set. The fields inside each kind's config object are schema-strict, so an unknown config field fails at load.

### Incidents and scenarios

`scenarios` is a list of named, reusable failure bundles, each composed of one or more `effects` (mode, target, intensity). `incidents` fires individual modes or entire scenarios on a schedule (a one-shot `at:` time or an interval-recurring `every:`/`for:` pair). Scenarios can also be activated live without a restart via the control plane.

See [incidents.md](incidents.md) for the full incident and scenario reference.

### Hosts

`hosts` is a list of traditional non-Kubernetes machines (Linux, Windows, macOS) emitting node/windows/macos exporter metrics and optional host logs and Docker cadvisor metrics.

## The emission switch

One blueprint declaration can fan into multiple constructs. The `observability` block on a `databases:` entry is the canonical example:

```yaml
databases:
  - engine: postgres
    version: "16.2"
    name: mine-db
    observability:
      cloudwatch: true   # emit aws_rds_* CloudWatch lane
      dbo11y: true       # emit database_observability_* lane
      digests: 40        # query-catalogue size for dbo11y
```

Both lanes share the same `fixture.DB` identity, so they join correctly. Setting both to `false` is valid — the database still exists as a workload call-target. The same pattern applies to cluster EC2 CloudWatch, cache CloudWatch, and cloud sub-family toggles.

A `postgres`, `mysql` or `docdb` database may also set `resource:` to a declared `dependency` or `pool`
(`environments[].resources`): its CloudWatch read and write latency then scale with the resource's
latency factor (the dependency's, or for a pool the dependency it is held across), and a pool-bound
database's `DatabaseConnections` follows the pool's busy connections (a `docdb` cluster reports them
on its `WRITER` row only, so summing roles counts them once). The legacy `database`-axis
incident modes no longer target a bound database; `dependency_slow` and `pool_exhausted` do. The
binding needs the CloudWatch lane, cannot combine with `observability.dbo11y`, and is checked when
the construct is built.

See [emission-switches.md](emission-switches.md) for the full list of gates.

## How loading works

At startup synthkit scans the configured sources for available identities, strict-decodes each
**selected** blueprint into its Go struct, and runs the topology resolver. Empty or unset
`BLUEPRINT_NAMES` selects none; exact comma-separated names select only those identities; `*`
selects the complete catalog.

- Cross-blueprint uniqueness is checked: cluster names, `account_id`, DB names, and cache names must be globally unique across all enabled blueprints.
- Every string reference (`runs_on`, `calls[].db`, `calls[].cache`, `incidents[].target`) is resolved or the load fails with the list of available names.
- Fixtures (node instance IDs, DB identifiers) are built once and handed to every construct that needs them, so joins are guaranteed by construction.

The resolver also handles `for_each_env: true` on workloads and integrations, fanning a single declaration into one instance per target environment.

## Your first blueprint

Copy this minimal skeleton, rename it, and drop it in `BLUEPRINTS`:

```yaml
name: mine
label: mine
shape: business_hours_plateau
timezone: America/New_York

environments:
  - name: prod
    weight: 1.0
    cloud:
      provider: aws
      account_id: "210987654321"
      region: eu-west-1
      vpc_id: vpc-0mine01
    cluster:
      type: eks
      name: mine-prod-euw1
      node_groups:
        - { name: general, instance_type: m6i.large, desired: 3 }
      k8s_monitoring:
        alloy: true
        features:
          cluster_metrics: true
    databases:
      - { engine: postgres, version: "16.2", name: mine-db,
          observability: { cloudwatch: true, dbo11y: true, digests: 40 } }
    caches:
      - { engine: redis, version: "7.1", name: mine-sessions }

workloads:
  - type: web_service
    name: mine-api
    runs_on: mine-prod-euw1
    tracing: true
    traffic: { off_peak_rps: 5, peak_rps: 40 }
    endpoints:
      - { route: "GET /v1/ping", error_rate: 0.01, p95_ms: 80 }
```

## Declaring causes

A failure scenario is most useful when it names a cause. `error_spike` and `latency_storm` scale a
number: they raise an error rate or stretch a latency and leave everything else as it was. A
declared cause changes the state of something requests depend on (a connection pool, a function's
concurrency limit, a queue's consumers, a slow database), and the latency, errors, retries and
backlog follow from that state across every signal that reads it. Declare causes by default, and
keep a rate-only mode for an effect that has no resource to name.

### Resources and faults

An environment opts in with `resources:`. Without it nothing changes: no model is built and the
output is exactly what it was.

| Kind | Models | Required fields besides `name` and `kind` |
|---|---|---|
| `pool` | Connections a service holds, such as a database pool. Waits, then rejects. | `size`, `hold_ms`, `acquire_timeout_ms`, `demand` |
| `concurrency` | A function's concurrent-execution limit. Rejects at once. | `size`, `hold_ms`, `demand` |
| `queue` | Consumers draining a backlog that grows and ages. | `size`, `hold_ms`, `demand` |
| `quota` | A request-rate limit on a shared API. Rejects at once. | `limit_rps`, `demand` |
| `dependency` | A downstream system with a baseline latency. | `latency_ms` (and `slow_factor`, default 4) |
| `credential` | The certificate or token a dependency needs. | `of`, `credential_type` |
| `rule` | A validation rule or a bad release that rejects a share of requests. | `reject_share` |

Resource names are unique across the blueprint. A pool, concurrency limit or queue can be
`held_across` a dependency: its effective hold is `hold_ms + latency_ms x (latency factor - 1)`, so a
slow dependency stretches every unit of work held across it. When the service that holds the resource also calls a service bound to that dependency, the slowed call is the stretch and is counted once in the holder's duration. `demand` is the load the resource
sees; it must cover every request that passes through it, and the loader rejects an app whose traffic exceeds
the demand declared for a resource it uses. An app's share of that demand follows the requests that
actually reach the resource: a request rejected earlier on its path (by a quota, a pool, a rule, or a
call that failed before this one) never arrives, so a bound Lambda function, queue or broker queue
reports the calls that get through. With no fault on the path the declared demand is unchanged.

Something has to meet a resource before a signal moves:

- An `app` service node lists the resources a request meets on arrival in `uses`, and may set
  `call_policies` (timeout, retries and backoff) on the calls it makes. A queue can drive an app's
  request rate with `traffic.from_resource`, with a `worker: true` entry consuming it.
- A Lambda function, an SQS queue, a Solace queue or a database instance binds to a resource with
  its own `resource:` field and then takes its metrics from the resource's state.

A fault is an ordinary failure mode on the `resource` axis, aimed at a resource by name and
activated by `incidents:`, a scenario or the control plane. The [fault catalogue](fault-catalogue.md)
lists the eight modes, what each acts on and a complete blueprint for each.

### Worked example: a pool held across a slow database

A generic intake service takes a connection from a pool for every request, and the pool's
connections are spent on queries to a records database. The scenario makes the database slow and
nothing else. This is the complete blueprint, and it is the same file the tests load as
`e2e/fixtures/causal-example.yaml`.

```yaml
# causal-example: the worked example in the authoring guide's "Declaring causes" section.
# A fixture, not a shipped blueprint: it sits outside blueprints/ so BLUEPRINT_NAMES=* never
# deploys it, and its tests pin the figures the guide quotes.
name: causal-example
label: causal-example
shape: business_hours_plateau
timezone: UTC

environments:
  - name: prod
    weight: 1.0
    cloud:
      provider: aws
      account_id: "111122223333"
      region: eu-west-1
      vpc_id: vpc-causal-example
    cluster:
      type: eks
      name: causal-example-prod
      node_groups:
        - { name: general, instance_type: m6i.large, desired: 3 }
    resources:
      # A service's database connection pool. One transaction holds a connection for 20 ms
      # (a 15 ms query plus driver overhead). At the 60 rps business peak that offers
      # 60 x 0.020 = 1.2 busy connections of 10 (utilization 0.12): comfortable.
      - name: intake-db-pool
        kind: pool
        size: 10                     # service pools are typically 10 to 50 connections
        hold_ms: 20
        held_across: records-db      # the hold stretches with records-db's extra latency
        acquire_timeout_ms: 3000     # a caller waits this long for a connection, then fails
        demand: { off_peak_rps: 5, peak_rps: 60 }   # must cover the app's traffic below
        error:
          status: 503
          type: SQLTransientConnectionException
          message: "intake-db-pool: connection is not available, request timed out after 3000ms"
      # The database the pool's connections are spent on. dependency_slow multiplies its
      # latency by up to slow_factor (lock contention: 15 ms becomes 180 ms at intensity 1).
      - { name: records-db, kind: dependency, latency_ms: 15, slow_factor: 12 }

workloads:
  - type: app
    name: intake
    runs_on: causal-example-prod
    traffic: { off_peak_rps: 5, peak_rps: 60 }
    services:
      - name: intake-api
        entry: true
        uses: [intake-db-pool]       # every request takes a connection on arrival
        calls: [records-db]
      - { name: records-db, type: db, uses: [records-db] }   # the query's latency is the dependency's

scenarios:
  - name: records-db-lock-contention
    title: Lock contention on the records database
    summary: >
      Queries slow from 15 ms to 180 ms, each connection is held 9 times longer, and the
      intake pool runs out of connections at peak.
    effects:
      - { mode: dependency_slow, target: records-db }   # intensity defaults to 1

incidents:
  - { scenario: records-db-lock-contention, at: "10:30", for: 40m }
```

**The declaration.** `intake-db-pool` has 10 connections, and one transaction holds one for 20 ms
(a 15 ms query plus driver overhead). At the 60 rps business peak that offers
`60 x 0.020 = 1.2` busy connections of 10, a utilization of 0.12. `records-db` is the dependency the
connections are spent on, with a 15 ms baseline. `held_across: records-db` couples the two: the
pool's hold is its own `hold_ms` plus whatever the dependency adds. The `intake-api` node lists the
pool in `uses`, and `records-db` is a database node with the dependency's latency.

**The scenario.** `dependency_slow` on `records-db` at intensity 1 multiplies its latency by
`slow_factor`: 15 ms becomes 180 ms. The pool is not declared faulty. Its hold becomes
`20 + 15 x (12 - 1) = 185 ms`, so the same 60 rps now offers `60 x 0.185 = 11.1` connections to a
pool of 10, a utilization of 1.11. The pool exhausts because the arithmetic says it must. The
incident runs daily from 10:30 for 40 minutes, and the pool returns to 0.12 as soon as it ends.

**What each signal shows.**

| Signal | Healthy | During the incident |
|---|---|---|
| Pool state (`resource: {name: intake-db-pool, field: utilization}` in a custom metric) | 0.12, with 1.2 connections in use | 1.11, with all 10 in use and requests waiting |
| Request outcome | Ordinary background errors only | About 10% of requests (`1 - 1 / 1.11`) fail at the entry as a 503 with outcome server error |
| Request duration | Tens of milliseconds | Failures last the 3 s acquire timeout, and requests that get a connection wait between 1.5 s and 3 s for it |
| `records-db` hop duration | About 15 ms | About 180 ms |
| Spans | As requests are | Spans follow each request's status and duration, so failed requests and the waiting show in the trace |
| Log status and outcome | 200 and success | 503 and server error on the failed requests |
| Span metrics and service graph | Error share at the background rate | Error share and latency rise with the requests that produce them |

Everything in the table is derived from one shared state. The same declaration, bound to a Lambda
function, an SQS queue or a database instance, moves their metrics in the same window: the
consumers read the state the application reads and never re-simulate it.

**The cause on spans and logs.** The failing request carries the cause as well as the status: the
resource's declared `error` block supplies the class and the message, and the failing hop reports
them instead of a bare code. The declared values are yours to choose and the catalogue ships none.
The attribute and log field names that carry them are fixed by the signal contracts in
[signals/](https://github.com/rknightion/synthkit/tree/main/signals) once sourced, not by this
guide.

### Declare causes, not rates

- **Name the resource first.** Ask what a real incident would break: a pool too small for its hold
  time, a function with too little reserved concurrency, consumers that cannot keep up, a
  dependency that gets slow. Declare that, then write a scenario that perturbs it. Do not reach for
  an error rate or a latency multiplier to produce the symptom directly.
- **Size resources with arithmetic.** Offered load is `rps x hold seconds`. A pool is healthy below
  roughly 80% of its size, exhausts above 100%, and a dependency's slowdown stretches the hold. Put the
  sum in a comment beside the declaration, as the example does.
- **Keep rates plausible.** Every size, hold time, latency and rate carries its real-world basis in
  a comment. A rate that no real deployment produces is fabricated data, and it looks legitimate.
- **Never raise a shipped rate to force a signal.** A scenario that must fire inside one tick
  belongs in a fixture under `e2e/fixtures/`, never in `blueprints/`.
- **Aim at the cause.** A scenario's effect targets the resource that is at fault (`records-db`),
  not the resource that shows the symptom (`intake-db-pool`).
- **Retries amplify.** A caller that resends adds demand to the resource it hits, and a resource
  that is already near its limit tips over sooner. Declare `call_policies` where the real client
  resends, and size `demand` to include it.

The model is an approximation: within a 5-second step rates are constant, pools, concurrency limits
and quotas have no memory between steps, and only queues carry a backlog from one step to the next.
Faults activated live are sampled at each master tick. DynamoDB, `web_service` and `ai_agent` do not
bind to resources.

## LLM-assisted blueprint creation

The `/create-blueprint` skill (see [tools.md](tools.md)) walks you through an interactive blueprint generation session in Claude Code: it asks about your estate, drafts the YAML, validates it, and explains what each section emits.

## Explore the sub-topics

<div class="grid cards" markdown>

- **[Blueprint reference](blueprint-reference.md)** — every key documented, grouped by section
- **[Infrastructure](infrastructure.md)** — environments, clusters, databases, caches, and how identity joins work
- **[Workloads](workloads.md)** — `web_service`, `app` and `ai_agent` workload config
- **[Emission switches](emission-switches.md)** — which constructs each declaration gates
- **[Incidents](incidents.md)** — scenarios, failure modes, scheduling, and live activation
- **[Fault catalogue](fault-catalogue.md)** — the causal fault modes on declared resources, each with a loadable blueprint
- **[Blueprint examples](blueprint-examples.md)** — annotated example blueprints from the catalog

</div>
