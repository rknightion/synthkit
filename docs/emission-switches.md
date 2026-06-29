---
title: Emission Switches
description: How a single resource declaration fans into multiple constructs, and how blueprint-level switches gate which constructs build.
---

# Emission Switches

A single resource declaration in a blueprint can fan into **multiple constructs** — each emitting a distinct signal family. An emission switch on the declaration tells the blueprint loader which of those constructs to build, without touching construct code. Constructs themselves are isolated and unconditional: they emit if they are built, and they are built if the switch says so.

The canonical example is a database: one `databases:` entry can produce both the `rds` CloudWatch construct (`aws_rds_*` metric families) and the `dbo11y_postgres` or `dbo11y_mysql` construct (`database_observability_*` families). The `observability:` block on the entry gates which constructs are instantiated.

## Why this design

The blueprint is the **only** place blueprint-specific configuration lives. Constructs are isolated modules that never import blueprints or each other. If constructs made their own decisions about whether to emit, that logic would have to live in construct code and reference blueprint concepts — violating the three-tier isolation rule.

The emission switch keeps the contract clean:

- The **blueprint** gates which constructs build (the switch).
- The **construct** emits unconditionally once built — it has no awareness of whether the switch is on or off.
- The **fixture** (the shared identity object — `fixture.DB`, `fixture.Cache`, etc.) is always built for every declared resource, so the workload's call-target references always resolve correctly, even when all construct lanes are off.

## Database observability switch

```yaml
databases:
  - engine: postgres
    version: "16.2"
    name: mine-app-db
    observability:
      cloudwatch: true   # default true — emit the rds construct (aws_rds_* CloudWatch families)
      dbo11y: true       # default false — emit the dbo11y_postgres construct (database_observability_*)
      digests: 40        # number of synthetic query digests when dbo11y is on
```

| `cloudwatch` | `dbo11y` | Effect |
|---|---|---|
| `true` (default) | `false` (default) | `rds` construct only |
| `true` | `true` | both `rds` + `dbo11y_{postgres,mysql}` |
| `false` | `true` | `dbo11y` only (no CloudWatch lane) |
| `false` | `false` | neither; the DB still exists as a workload call-target (its `db.*` span identity resolves) but emits no infra telemetry |

For MySQL, the engine field selects `dbo11y_mysql` instead of `dbo11y_postgres`; the switch keys are identical.

## Cache observability switch

```yaml
caches:
  - engine: redis
    version: "7.1"
    name: mine-sessions
    observability:
      cloudwatch: true   # default true — emit the elasticache construct
                         # false = call-target only, no ElastiCache metrics
```

## Cluster EC2 CloudWatch switch

```yaml
cluster:
  type: eks
  name: mine-prod-use1
  observability:
    cloudwatch: true   # default true — emit the ec2 construct (aws_ec2_* per-node families)
                       # false = k8s substrate only, no EC2 CloudWatch lane
```

This is distinct from the EKS control-plane switch (see below). `cluster.observability.cloudwatch` gates `aws_ec2_*` (the node-level EC2 namespace), while `cloud.cloudwatch.eks` gates `aws_eks_*` (the EKS control-plane families).

## Cloud CloudWatch sub-family switches

The `cw_infra` construct bundles several AWS infrastructure families under one cloud identity. Each sub-family is individually gated:

```yaml
cloud:
  provider: aws
  account_id: "210987654321"
  region: us-east-1
  vpc_id: vpc-0mine01
  nat_gateways: 2
  cloudwatch:
    albs: 3           # number of ALB instances (omit or 0 = no ALB families)
    nlb: true         # NLB families (default true if omitted)
    ebs: true         # EBS families (default true if omitted)
    nat_gateway: true # NAT Gateway families (default true if omitted)
    eks: true         # EKS control-plane families aws_eks_* (default true if omitted)
    s3_buckets: 2     # number of S3 buckets (omit or 0 = no S3 families)
    firehose: true    # Kinesis Firehose families (default true if omitted)
```

`albs` and `s3_buckets` are count knobs where `0` or omission disables that family. The remaining sub-families use a boolean: `false` disables; omitted means the default (all families enabled).

## CSP Azure sub_signals

The `csp_azure` integration gates per-service metric families with `sub_signals`:

```yaml
integrations:
  csp_azure:
    enabled: true
    sub_signals: [compute, databases, storage, networking, messaging, logs]
```

Valid values: `compute`, `databases`, `storage`, `networking`, `messaging`, `logs`. An empty or omitted `sub_signals` emits all families. Setting a non-empty list restricts emission to exactly those families.

```yaml
integrations:
  csp_azure:
    enabled: true
    sub_signals: [compute, databases]   # only azure_microsoft_compute_* and azure_microsoft_sql_* families
```

## CSP GCP sub_signals

`csp_gcp` follows the same pattern:

```yaml
integrations:
  csp_gcp:
    enabled: true
    sub_signals: [compute, storage, loadbalancing, pubsub, cloudrun, bigtable]
```

Valid values: `compute`, `databases`, `storage`, `networking`, `messaging`, `loadbalancing`, `pubsub`, `cloudrun`, `bigtable`. Empty or omitted = all families.

## Kubernetes feature gates

The `k8s_monitoring` block on a cluster controls which k8s-monitoring collector features the `k8s_cluster` construct activates:

```yaml
cluster:
  k8s_monitoring:
    enabled: true
    alloy: true
    opencost: true          # OpenCost cost-allocation families
    kepler: true            # Kepler energy-monitoring families
    fleet_management: true  # register Alloy collectors with Fleet Management API
    features:
      cluster_metrics: true           # KSM + cAdvisor + kubelet + node-exporter
      cluster_events: true            # k8s event stream to Loki
      pod_logs: true                  # pod log collection
      node_logs: true                 # node-level journal logs
      profiling: true                 # eBPF continuous profiling per pod
      application_observability: true # OTLP ingest from instrumented apps
```

Omitting `opencost` or `kepler` (or setting to `false`) suppresses those signal families entirely without affecting the base k8s metrics.

## Cluster addon feature gates

Addons are declared by kind name (short form) or as a map with additional config. Declaring an addon activates it; omitting it suppresses it:

```yaml
cluster:
  addons:
    - core_dns
    - vpc_cni
    - ebs_csi
    - cert_manager
    - { name: cluster_autoscaler, min_nodes: 3, max_nodes: 10 }
    - karpenter
    - argocd
    - envoy_gateway
    - external_dns
    - load_balancer_controller
```

## Summary table

| Declaration | Switch key | Gates |
|---|---|---|
| `databases:` | `observability: { cloudwatch, dbo11y, digests }` | `rds` and/or `dbo11y_{postgres,mysql}` |
| `caches:` | `observability: { cloudwatch }` | `elasticache` (false = call-target only) |
| `cluster:` | `observability: { cloudwatch }` | per-node `ec2` CloudWatch lane (`aws_ec2_*`) |
| `cloud:` | `cloudwatch: { nlb, ebs, nat_gateway, eks, firehose, albs, s3_buckets }` | `cw_infra` per-family sub-families |
| `integrations: csp_azure` | `sub_signals: [...]` | per-service azure metric families |
| `integrations: csp_gcp` | `sub_signals: [...]` | per-service GCP metric families |
| `cluster.k8s_monitoring` | `opencost`, `kepler`, `features.*` | OpenCost / Kepler / collector feature lanes |
| `cluster.addons` | presence in the list | per-addon construct instance |
