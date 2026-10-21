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

- `cloud` — AWS account, region, VPC, and the `cloudwatch` sub-family toggles (ALBs, EBS, NAT Gateway, S3, Firehose, EKS control-plane, PrivateLink). Also the home for AWS-native service configs: `aoss`, `mwaa`, `glue`, `bedrock`, `agentcore`.
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
