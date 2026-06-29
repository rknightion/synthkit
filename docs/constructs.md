---
title: Constructs & Workloads
description: Catalogue of every construct kind and workload that synthkit can model, grouped by technology area.
---

# Constructs & Workloads

synthkit's catalog is a collection of isolated construct and workload modules. As of v1.0.0 there are **42 construct kinds** and **2 workload kinds**. Each construct is a separate Go package under `internal/construct/<kind>/`; workloads live under `internal/workload/<kind>/`. Neither constructs nor workloads import each other or the blueprint package — isolation is enforced by `internal/archtest`.

## Construct granularity principle

A construct boundary is drawn at the **smallest unit that is independently declarable in a blueprint AND carries a distinct shared identity or cross-construct join** — not at the delivery pipeline.

- **Distinct fixture + distinct declaration + distinct join → separate construct**, sharing a mechanic library. `ec2`, `rds`, and `elasticache` are separate because each hangs off its own fixture type (`fixture.Node`, `fixture.DB`, `fixture.Cache`) and joins to other constructs through that identity.
- **Same pipeline + same identity + declared together → one construct with config-gated sub-families.** `cw_infra` bundles ALB/NLB/EBS/NAT/EKS/S3/Firehose off one cloud identity, each family gated by a per-family switch. `k8s_cluster` gates OpenCost/Kepler/Alloy via `k8s_monitoring` config.
- **Engine/type variants of one resource → a config discriminator, not a new kind.** RDS Postgres vs Aurora Postgres share the `rds` construct; the engine selects the CloudWatch family variant.

For the emission switch that controls which constructs a declaration fans into, see [emission-switches.md](emission-switches.md).

## Kubernetes

Constructs in this group are **substrate-scoped** — they carry no `blueprint` label. The cluster name disambiguates across blueprints.

| Kind | Description | Signal area |
|---|---|---|
| `k8s_cluster` | Core k8s-monitoring substrate: kube-state-metrics, node-exporter, cAdvisor, kubelet. Gates OpenCost/Kepler and the Alloy collector via `k8s_monitoring` config. | [k8s](signal-areas.md) |
| `k8s_profiling` | Pyroscope-based continuous profiling substrate for workloads running in the cluster. | [profiles](signal-areas.md) |
| `ksm_ingress` | KSM ingress metrics (`kube_ingress_*`) with cluster disambiguation. | [k8s](signal-areas.md) |
| `etcd` | etcd cluster health and Raft metrics. | [k8s-addons](signal-areas.md) |

### Kubernetes add-ons

Add-ons are declared under `cluster.addons` in the blueprint. Each is substrate-scoped and emits pod-joined metrics + logs.

| Kind | Description | Signal area |
|---|---|---|
| `argocd` | Argo CD controller and application metrics. | [k8s-addons](signal-areas.md) |
| `cert_manager` | cert-manager certificate lifecycle metrics. | [k8s-addons](signal-areas.md) |
| `cluster_autoscaler` | Cluster Autoscaler node scaling and utilization metrics. | [k8s-addons](signal-areas.md) |
| `core_dns` | CoreDNS request and cache metrics. | [k8s-addons](signal-areas.md) |
| `ebs_csi` | AWS EBS CSI driver metrics (volume attach, provision, I/O). | [k8s-addons](signal-areas.md) |
| `envoy_gateway` | Envoy Gateway proxy metrics (upstream/downstream request rates, latency). | [k8s-addons](signal-areas.md) |
| `external_dns` | ExternalDNS DNS sync and registry metrics. | [k8s-addons](signal-areas.md) |
| `karpenter` | Karpenter node provisioner metrics (node lifecycle, disruption, scheduling). | [k8s-addons](signal-areas.md) |
| `load_balancer_controller` | AWS Load Balancer Controller metrics. | [k8s-addons](signal-areas.md) |
| `vpc_cni` | AWS VPC CNI IP allocation metrics. | [k8s-addons](signal-areas.md) |

### Alloy health

| Kind | Description | Signal area |
|---|---|---|
| `alloy_health` | Grafana Alloy / agent health and pipeline metrics. | [fm](signal-areas.md) |

## AWS infrastructure (CloudWatch)

These constructs are **blueprint-scoped** (they carry the `blueprint` selector label). They use `internal/cw` for the five-stat CloudWatch expansion (`_sum/_average/_maximum/_minimum/_sample_count`).

| Kind | Description | Signal area | Blueprint declaration |
|---|---|---|---|
| `ec2` | EC2 instance CloudWatch metrics (`aws_ec2_*`). Shares `fixture.Node` identity with the k8s cluster. | [cw](signal-areas.md) | Gated by `cluster.observability.cloudwatch` |
| `cw_infra` | CloudWatch infrastructure families: ALB, NLB, EBS, NAT Gateway, EKS control plane, S3, Kinesis Firehose. Each family is independently gated. | [cw](signal-areas.md) | `cloud.cloudwatch.*` switches |

## AWS databases and cache

| Kind | Description | Signal area | Blueprint declaration |
|---|---|---|---|
| `rds` | RDS CloudWatch metrics (`aws_rds_*`). Shares `fixture.DB` with dbo11y constructs. Supports Postgres, MySQL, Aurora variants. | [cw](signal-areas.md) | `databases[].observability.cloudwatch` |
| `elasticache` | ElastiCache CloudWatch metrics (`aws_elasticache_*`). | [cw](signal-areas.md) | `caches[].observability.cloudwatch` |
| `docdb` | Amazon DocumentDB CloudWatch metrics (`aws_docdb_*`). | [cw](signal-areas.md) | `databases[].engine: documentdb` |
| `neptune` | Amazon Neptune CloudWatch metrics (`aws_neptune_*`). | [cw](signal-areas.md) | `databases[].engine: neptune` |
| `aoss` | Amazon OpenSearch Serverless (AOSS) CloudWatch metrics. | [cw](signal-areas.md) | `integrations: aoss` |

### AWS managed services

| Kind | Description | Signal area |
|---|---|---|
| `glue` | AWS Glue job and crawler CloudWatch metrics. | [cw](signal-areas.md) |
| `mwaa` | Amazon Managed Workflows for Apache Airflow CloudWatch metrics. | [cw](signal-areas.md) |

## Database Observability (dbo11y)

Substrate-scoped. Shares `fixture.DB` identity with the corresponding CloudWatch construct.

| Kind | Description | Signal area | Blueprint declaration |
|---|---|---|---|
| `dbo11y_postgres` | Grafana Database Observability for PostgreSQL — query digests, connection pool, replication. | [dbo11y](signal-areas.md) | `databases[].observability.dbo11y: true` |
| `dbo11y_mysql` | Grafana Database Observability for MySQL. | [dbo11y](signal-areas.md) | `databases[].observability.dbo11y: true` |

## CSP: Azure and GCP

Both are substrate-scoped. Sub-families are gated by `sub_signals: [...]` in the integration declaration (empty/omitted = all emit).

| Kind | Description | Signal area |
|---|---|---|
| `csp_azure` | Grafana CSP Azure integration: VMs, App Service, SQL, Storage, Cosmos DB, Functions, and more. | [cspazure](signal-areas.md) |
| `csp_gcp` | Grafana CSP GCP integration: Compute, Cloud SQL, Cloud Storage, Pub/Sub, Cloud Run, BigTable. | [cspgcp](signal-areas.md) |

## AI & LLM

All AI/LLM constructs are **blueprint-scoped** and tech-generic. gen_ai metrics go via `sink/promrw` final names; spans via the hand-encoded OTLP seam. The OTel metrics SDK ban applies.

| Kind | Description | Signal area | Blueprint declaration |
|---|---|---|---|
| `bedrock` | Amazon Bedrock CloudWatch model invocation metrics (`aws_bedrock_*`). | [bedrock](signal-areas.md) | `integrations: bedrock` |
| `agentcore` | AWS Bedrock AgentCore CloudWatch metrics and Loki logs. | [agentcore](signal-areas.md) | `integrations: agentcore` |
| `portkey_gateway` | Portkey AI gateway: request/token/cost metrics scraped from the Portkey API. | [portkey](signal-areas.md) | `integrations: portkey_gateway` |
| `portkey_poller` | Portkey native scrape metrics (the separate Portkey Prometheus exporter path). | [portkey](signal-areas.md) | `integrations: portkey_poller` |
| `langsmith_eval` | LangSmith evaluation metrics: scores, pass rates, latency by evaluator. | [langsmith](signal-areas.md) | `integrations: langsmith_eval` |
| `langsmith_platform` | LangSmith platform-level metrics: run volumes, latency, token counts. | [langsmith](signal-areas.md) | `integrations: langsmith_platform` |
| `snowflake` | Snowflake Cortex usage and query metrics via the Snowflake Grafana integration. | [snowflake](signal-areas.md) | `integrations: snowflake` |
| `qualification_pipeline` | AI qualification pipeline metrics (model evaluation + selection flow). | [qualification](signal-areas.md) | `integrations: qualification_pipeline` |

## Network

| Kind | Description | Signal area | Blueprint declaration |
|---|---|---|---|
| `network_topology` | SNMP topology exporter metrics: device availability, interface I/O, link state, federation metrics. Mirrors the external `network-topology-exporter` signal contract. | [nettopo](signal-areas.md) | `integrations: network_topology` |

## Hosts

| Kind | Description | Signal area | Blueprint declaration |
|---|---|---|---|
| `host` | Bare-metal or VM host metrics (node_exporter-style) for non-k8s hosts. | [host](signal-areas.md) | `hosts:` |

## Integrations and Grafana products

These constructs model external systems Grafana Cloud ingests, or Grafana Cloud products you have enabled.

| Kind | Description | Signal area | Blueprint section |
|---|---|---|---|
| `cloudflare` | Cloudflare zone metrics via the Grafana Cloudflare integration (blueprint-scoped). | [cloudflare](signal-areas.md) | `integrations: cloudflare` |
| `beyla_agent` | Grafana Beyla eBPF instrumentation — application RED metrics without SDK instrumentation. | [beyla](signal-areas.md) | `integrations: beyla` |
| `synthetic_monitoring` | Grafana Synthetic Monitoring check data: probe results, latency, reachability. | [sm](signal-areas.md) | `features: synthetic_monitoring` |
| `fleet_management` | Grafana Fleet Management collector registration and health. | [fm](signal-areas.md) | `features: fleet_management` |

## Workloads

Workloads mint request-correlated telemetry. They share the blueprint's ledger for end-to-end correlation IDs. Both workload kinds may coexist in one blueprint.

| Kind | When to use | Description |
|---|---|---|
| `web_service` | A single service | Emits RED metrics, traces (browser → backend → DB hops), optional RUM/Faro beacons, optional gen_ai hops. The simple, common case. |
| `app` | A multi-service graph | A declared graph of typed service nodes (`services:`), each emitting its own custom metrics/logs/spans via the telemetry DSL. One ledger mint drives one correlated trace across the whole graph. Use when you need per-service incident targeting, per-service scaling, or custom metric families per node. |

The `app` workload's telemetry DSL supports typed nodes (`frontend/web/grpc/worker/job/stream/gateway/db/cache/llm/agent/tool/workflow/retrieval`), `ValueModel` one-of value generators (`const`, `enum`, `int_range`, `float_range`, `normal`, `shape`, `ref`), and reusable `Profile` bundles from the catalog in `internal/telemetryspec/profiles`.

For workload configuration and the telemetry DSL reference, see [workloads.md](workloads.md).
