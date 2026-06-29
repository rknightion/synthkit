---
title: Declaring Infrastructure
description: How to declare environments, clusters, databases, and caches in a blueprint, and how shared identity ties constructs together.
---

# Declaring Infrastructure

Infrastructure in a blueprint means declaring the cloud accounts, Kubernetes clusters, databases, and caches that exist in your estate. synthkit builds shared identity once from these declarations, hands it to every construct that needs it, and uses it to produce the cross-signal joins (EC2 ↔ k8s node, RDS ↔ Database Observability) that real Grafana Cloud dashboards depend on.

Every field here is documented exhaustively in [blueprint-reference.md](blueprint-reference.md). This page explains the concepts and shows the patterns.

## Environments

An environment is the unit of cloud isolation — one AWS account, one VPC, one cluster. Multiple environments in one blueprint model a real estate:

```yaml
environments:
  - name: prod
    weight: 1.0
    production: true    # full weekend traffic; auto-true when name == "prod"
  - name: staging
    weight: 0.5         # half the traffic of prod
  - name: dev
    weight: 0.2
```

`production: false` (or any name other than `prod` without an explicit `production: true`) reduces weekend traffic — non-prod environments naturally go quieter outside business hours.

## Cloud accounts

The `cloud` block sets the AWS account identity that all blueprint-scoped CloudWatch constructs use:

```yaml
environments:
  - name: prod
    cloud:
      provider: aws
      account_id: "210987654321"
      region: eu-west-1
      vpc_id: vpc-0mine01
      nat_gateways: 2
```

`account_id` must be globally unique across all enabled blueprints — the loader rejects collisions.

### CloudWatch infrastructure sub-families

The `cloud.cloudwatch` block controls which AWS CloudWatch infrastructure families the `cw_infra` construct emits. Omitting the block enables all defaults.

```yaml
cloud:
  provider: aws
  account_id: "210987654321"
  region: eu-west-1
  vpc_id: vpc-0mine01
  cloudwatch:
    albs: 3          # 3 ALB instances → aws_applicationelb_*; 0 disables
    s3_buckets: 5    # 5 S3 buckets → aws_s3_*; 0 disables
    firehose: true
    nlb: true
    ebs: true
    nat_gateway: true
    eks: true        # EKS control-plane → aws_eks_* (separate from the k8s substrate)
    private_link: true
```

!!! note "Two independent EKS toggles"
    `cloud.cloudwatch.eks` gates `aws_eks_*` (the EKS control-plane CloudWatch namespace, a `cw_infra` family). `cluster.observability.cloudwatch` gates `aws_ec2_*` (per-node EC2 metrics, the `ec2` construct). They are independent — you can run the k8s substrate without CloudWatch, or CloudWatch without the k8s substrate.

### AWS-native service configs

Optional services in the cloud block are absent by default (not emitted). Set the key to enable:

```yaml
cloud:
  provider: aws
  account_id: "210987654321"
  region: us-west-2
  vpc_id: vpc-0mine01
  aoss: {}                           # Amazon OpenSearch Serverless (empty config uses defaults)
  mwaa:
    environments: [my-airflow-env]  # Amazon MWAA
  glue:
    jobs: [etl-daily, etl-hourly]   # AWS Glue
  bedrock:
    models: [anthropic.claude-3-5-sonnet-20241022-v2:0]
    sub_signals: [models, guardrails]
  agentcore:
    agents: [planner, retriever]
    sub_signals: [runtime, resource_usage, usage_logs]
```

See [blueprint-reference.md](blueprint-reference.md) for each block's fields.

## Clusters

A cluster requires a unique name and a `type` (only `"eks"` in v1). The name is the cross-construct join key — workloads reference it via `runs_on`, and the loader validates every reference.

```yaml
cluster:
  type: eks
  name: mine-prod-euw1      # globally unique across all enabled blueprints
  node_groups:
    - { name: general, instance_type: m6i.xlarge, desired: 4, provisioner: managed }
    - { name: spot,    instance_type: c6i.large,  desired: 2, provisioner: karpenter }
  platform:
    os: al2023               # al2 | al2023 (default) | bottlerocket
    kubernetes_version: "1.31"
```

Node count defaults to `max(3, ceil(totalPods / 8))` when `desired` is omitted — the resolver derives it from the workloads bound to the cluster.

### k8s-monitoring config

The `k8s_monitoring` block configures the Grafana k8s-monitoring Helm chart emulation:

```yaml
k8s_monitoring:
  enabled: true
  alloy: true
  alloy_version: "1.16.3"
  opencost: true            # cost allocation
  kepler: true              # energy monitoring
  fleet_management: true    # register collectors with Fleet Management API
  metrics_replicas: 2       # alloy-metrics StatefulSet replica count
  receiver_as_daemonset: true
  features:
    cluster_metrics: true           # KSM + cAdvisor + kubelet + node-exporter
    cluster_events: true            # k8s event stream
    pod_logs: true                  # pod log collection
    node_logs: true                 # node journal logs
    profiling: true                 # eBPF continuous profiling per pod
    application_observability: true # OTLP ingest from instrumented apps
  control_plane:
    api_server: true
    kube_proxy: true
    kube_scheduler: true
    kube_controller_manager: true
    kubelet_probes: true
```

Absent feature flags are treated as `false` — that collector role is not deployed.

### Cluster observability gate

`cluster.observability.cloudwatch` controls the per-node `aws_ec2_*` lane. Set it to `false` for a pure k8s-only footprint:

```yaml
cluster:
  type: eks
  name: k8smin-prod-usw2
  observability:
    cloudwatch: false    # k8s substrate only; no EC2 CloudWatch lane
  k8s_monitoring:
    enabled: true
    alloy: true
    features: { cluster_metrics: true }
```

### Cluster add-ons

Add-ons are listed under `cluster.addons[]` as bare scalars or as maps with extra config:

```yaml
addons:
  - core_dns
  - vpc_cni
  - ebs_csi
  - cert_manager
  - karpenter
  - argocd
  - envoy_gateway
  - external_dns
  - load_balancer_controller
  - alloy_health
  - { name: cluster_autoscaler, min_nodes: 3, max_nodes: 12 }
  - { name: ksm_ingress, ingresses: [{ name: api, service_name: mine-api, service_port: 443, tls: true }] }
```

Unknown add-on kinds fail at load.

## Databases

Each entry in `databases[]` fans into one or more constructs depending on the `observability` block. The `name` must be globally unique across all enabled blueprints.

```yaml
databases:
  - engine: postgres
    version: "16.2"
    name: mine-orders-db
    instance_class: db.r6g.large
    observability:
      cloudwatch: true    # emit aws_rds_* CloudWatch family
      dbo11y: true        # emit database_observability_* lane
      digests: 40         # query-catalogue size for dbo11y

  - engine: mysql
    version: "8.4"
    name: mine-events-db
    observability:
      cloudwatch: true
      dbo11y: true
      digests: 20
```

Omitting the `observability` block entirely is equivalent to `cloudwatch: true, dbo11y: false`. Setting both to `false` keeps the DB as a workload call-target without emitting any infra telemetry — the `fixture.DB` identity is still built and resolved.

!!! info "Supported database engines"
    `postgres` and `mysql` support both the CloudWatch lane and the Database Observability lane. `docdb` (DocumentDB) and `neptune` support CloudWatch only — the `dbo11y` key is accepted but has no effect for those engines.

The `dbo11y-mysql.yaml` blueprint shows the minimal Database Observability setup for MySQL:

```yaml
databases:
  - { engine: mysql, version: "8.4", name: dbo11y-mysql-app,
      instance_class: db.r6g.large,
      observability: { dbo11y: true, digests: 20 } }
```

`cloudwatch` defaults to `true`, so both the `aws_rds_*` and `database_observability_*` lanes emit.

## Caches

Each cache entry emits `aws_elasticache_*` when `observability.cloudwatch: true` (the default). The name must be globally unique.

```yaml
caches:
  - engine: redis
    version: "7.1"
    name: mine-sessions
    instance_class: cache.r6g.large
    observability:
      cloudwatch: true    # emit aws_elasticache_*; false = call-target only
```

## Shared identity and cross-construct joins

The shared-identity model is how constructs from different families join in Grafana panels without any global coupling:

- The loader builds `fixture.Node` (InstanceID, hostname, PrivateIP, InstanceType) once from the cluster's node groups and hands it to BOTH the k8s substrate construct (`k8s_cluster`) and the EC2 CloudWatch construct (`ec2`). A Grafana panel that correlates `kube_node_info` with `aws_ec2_*` uses `provider_id` ↔ `InstanceId` — both stamped from the same `fixture.Node`.
- The loader builds `fixture.DB` once from a `databases[]` entry and hands it to BOTH the `rds` (CloudWatch) construct and the `dbo11y_{postgres,mysql}` construct. The join key `db_instance_identifier` is identical in both.
- The cluster `name` must be unique across blueprints. Two blueprints cannot share the same cluster name (load-rejected), but two blueprints CAN run the same cluster type concurrently with their own distinct names and identities.

Substrate-scoped constructs (k8s substrate, add-ons, Database Observability, CSP Azure/GCP, Synthetic Monitoring, Fleet Management) carry **no `blueprint` label**. Their identity labels (`cluster`, `account_id`, DB instance identifier) are the disambiguators. This is enforced by the scoped writer and asserted by the integration tests.

## Per-environment fan-out

A single `workloads[]` or `integrations:` declaration can fan into one instance per environment using `for_each_env: true`:

```yaml
integrations:
  portkey_poller:
    for_each_env: true        # one portkey_api_* series set per env, with env= label
    workspace: ws-mine-00001
    use_cases: [assistant, summarization]
```

Each fanned instance is independent, uses its own `env` label, and weight-scales its magnitudes by the environment's `weight`. A `db:`/`cache:` call from a fanned workload is rejected at load (physical resources are globally unique, not per-env).

## Putting it together

The `cw-infra-aws.yaml` blueprint shows a full AWS infrastructure setup:

```yaml
name: aws-cloudwatch-infra
label: aws-cw-estate
shape: business_hours_plateau

environments:
  - name: prod
    cloud:
      provider: aws
      account_id: "412000000001"
      region: us-east-1
      vpc_id: vpc-cwinfra01
      nat_gateways: 2
      cloudwatch:
        albs: 3
        s3_buckets: 5
        firehose: true
        nlb: true
        ebs: true
        nat_gateway: true
        eks: true
        private_link: true
    cluster:
      type: eks
      name: cwinfra-prod-use1
      node_groups:
        - { name: general, instance_type: m6i.xlarge, desired: 4 }
      k8s_monitoring:
        enabled: true
        alloy: true
        features: { cluster_metrics: true }
      observability:
        cloudwatch: true    # per-node aws_ec2_* lane
    databases:
      - { engine: postgres, version: "15", name: cwinfra-orders-db,
          instance_class: db.r6g.large,
          observability: { cloudwatch: true, dbo11y: false } }
    caches:
      - { engine: redis, version: "7", name: cwinfra-sessions-cache,
          instance_class: cache.r6g.large,
          observability: { cloudwatch: true } }
```

For the emission-switch reference table (which constructs each declaration gates), see [emission-switches.md](emission-switches.md). For every field with its type and default, see [blueprint-reference.md](blueprint-reference.md).
