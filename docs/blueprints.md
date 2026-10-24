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
queue groups; optional `enrichment` accepts only explicit supported P2 metadata keys. DynamoDB
requires positive provisioned capacities/quotas; its account snapshots have no table dimensions.
`write_source` is omitted, `Customer` or `GlobalTable`; the last requires independent `replication`.
Table operations are GetItem, PutItem, Query, Scan and SELECT-only ExecuteStatement. Returned items
apply only to the three selection operations. An optional `stream` declares its own `label` and
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

## LLM-assisted blueprint creation

The `/create-blueprint` skill (see [tools.md](tools.md)) walks you through an interactive blueprint generation session in Claude Code: it asks about your estate, drafts the YAML, validates it, and explains what each section emits.

## Explore the sub-topics

<div class="grid cards" markdown>

- **[Blueprint reference](blueprint-reference.md)** — every key documented, grouped by section
- **[Infrastructure](infrastructure.md)** — environments, clusters, databases, caches, and how identity joins work
- **[Workloads](workloads.md)** — `web_service`, `app` and `ai_agent` workload config
- **[Emission switches](emission-switches.md)** — which constructs each declaration gates
- **[Incidents](incidents.md)** — scenarios, failure modes, scheduling, and live activation
- **[Blueprint examples](blueprint-examples.md)** — annotated example blueprints from the catalog

</div>
