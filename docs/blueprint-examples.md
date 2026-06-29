---
title: Blueprint Examples
description: A tour of every bundled blueprint — what each models, which constructs it exercises, and which to copy for your use case.
---

# Blueprint Examples

Synthkit ships 25 ready-to-run blueprints in the `blueprints/` directory. Each is independent: loading or deleting any one file affects only its own telemetry. They are loaded automatically at startup from the `BLUEPRINTS` directory (default `./blueprints`; see [Configuration](configuration.md)).

To start from an example, copy the file you want into your `BLUEPRINT_DATA_DIR` (or upload it via the control plane), change the name and any identifiers, then restart. The new blueprint is fully independent.

---

## Kubernetes

### [k8s-minimal.yaml](https://github.com/rknightion/synthkit/blob/main/blueprints/k8s-minimal.yaml)

Cheapest k8s-monitoring footprint: `cluster_metrics` only (KSM + cAdvisor + kubelet + node-exporter), no logs, events, profiling, OpenCost, or Kepler. Use this as the starting point for any Kubernetes blueprint. Exercises: `k8s_cluster`, `ec2`.

### [k8s-full-stack.yaml](https://github.com/rknightion/synthkit/blob/main/blueprints/k8s-full-stack.yaml)

Maximal k8s observability: every collector feature enabled — cluster metrics, events, pod logs, node logs, profiling, application observability — plus OpenCost cost allocation, Kepler energy monitoring, Fleet Management, control-plane deep monitoring, full addon set, Karpenter autoscaler, and Bottlerocket nodes. The reference for teams wanting everything at once. Exercises: `k8s_cluster`, `ec2`, `k8s_profiling`, `karpenter`, `cert_manager`, `coredns`, `vpc_cni`, `ebs_csi`, `argocd`, `envoy_gateway`, `external_dns`, `load_balancer_controller`, `fleetmgmt`.

### [k8s-cost-power.yaml](https://github.com/rknightion/synthkit/blob/main/blueprints/k8s-cost-power.yaml)

FinOps focus: OpenCost workload cost allocation + Kepler per-pod energy consumption on a standard EKS cluster. No logs or profiling overhead. Exercises: `k8s_cluster`, `k8s_profiling` (Kepler), OpenCost sub-family.

### [k8s-control-plane.yaml](https://github.com/rknightion/synthkit/blob/main/blueprints/k8s-control-plane.yaml)

EKS control-plane deep monitoring: all five control-plane component metric families (API server, kube-proxy, scheduler, controller-manager, kubelet probes) with cluster metrics. Reference for teams focused on k8s internals. Exercises: `k8s_cluster` control-plane sub-families.

### [k8s-logs-events.yaml](https://github.com/rknightion/synthkit/blob/main/blueprints/k8s-logs-events.yaml)

Logs-centric monitoring: pod logs + node logs + cluster events shipped to Loki via Alloy DaemonSet and singleton collectors. No profiling, OpenCost, or Kepler. Exercises: `k8s_cluster` logs + events features.

### [k8s-windows-mixed.yaml](https://github.com/rknightion/synthkit/blob/main/blueprints/k8s-windows-mixed.yaml)

Mixed Linux + Windows EKS node groups: exercises both the windows-exporter signal path (windows-pool) and the standard Linux node-exporter path (linux-general) with node-level log collection. Reference for teams running .NET or legacy workloads on Windows nodes. Exercises: `k8s_cluster` mixed-OS node groups.

---

## AWS / CloudWatch

### [cw-infra-aws.yaml](https://github.com/rknightion/synthkit/blob/main/blueprints/cw-infra-aws.yaml)

AWS CloudWatch infrastructure showcase: explicit sub-family toggles covering ALB/NLB/EBS/NAT/EKS/S3/Firehose/PrivateLink, plus RDS and ElastiCache CloudWatch lanes. Demonstrates every `cw_infra` sub-family switch. Exercises: `cw_infra`, `rds`, `elasticache`, `ec2`.

### [aws-cloud-services.yaml](https://github.com/rknightion/synthkit/blob/main/blueprints/aws-cloud-services.yaml)

AWS managed data/ETL services: OpenSearch Serverless (AOSS), Managed Workflows for Apache Airflow (MWAA), Glue ETL, DocumentDB, and Neptune. Focused on the cloud-service constructs that rarely appear in a basic k8s blueprint. Exercises: `aoss`, `mwaa`, `glue`, `docdb`, `neptune`.

---

## Databases

### [dbo11y-mysql.yaml](https://github.com/rknightion/synthkit/blob/main/blueprints/dbo11y-mysql.yaml)

Demonstrates the Database Observability MySQL lane: an RDS MySQL instance emitting `database_observability_*` + `mysql_*` metric families, log ops, replication (slave-status metrics), and the `query_data_locks` op (which appears only while a `lock_contention` incident is active). Pair with an incident targeting the db name. Exercises: `dbo11y_mysql`, `rds`.

---

## CSP Azure / GCP

### [csp-azure.yaml](https://github.com/rknightion/synthkit/blob/main/blueprints/csp-azure.yaml)

CSP Azure integration: `azure_microsoft_*` window-gauge metrics across compute, databases, storage, networking, messaging, and Event Hubs logs, via the serverless managed scraper or `azure_exporter` path. Demonstrates all `sub_signals` families and the `ingestion_path` discriminator. Exercises: `csp_azure`.

---

## AI / LLM

### [acme-ai-platform.yaml](https://github.com/rknightion/synthkit/blob/main/blueprints/acme-ai-platform.yaml)

The API-poll view of an AI assistant estate across multiple environments: AgentCore-vended AWS metrics (7 runtimes, no in-account Bedrock model inference), LLM gateway observed via the Portkey analytics poller (`portkey_api_*`) + LangSmith eval bridge, full traced estate across 8 EKS deployments and 4 request journeys. Exercises: `agentcore`, `portkeypoller`, `langsmithplatform`, `langsmitheval`, `rds` (Aurora PostgreSQL), `docdb`, `neptune`, `elasticache`, `k8s_cluster`, `app` workload with `gen_ai_client` + `agentic_flow`.

### [acme-ai-platform-eval.yaml](https://github.com/rknightion/synthkit/blob/main/blueprints/acme-ai-platform-eval.yaml)

The same assistant estate as `acme-ai-platform` but with the AI evaluation gateway exposed as a connected trace (Path-B gateway span), modelling the per-tenant gateway slice. Designed to run concurrently with `acme-ai-platform` using disjoint identities. Exercises: `portkeygateway` (connected trace), `bedrock`, `agentcore`, `app` workload with `gateway_export_log` + `gateway_native_scrape` profiles.

### [acme-ai-eval.yaml](https://github.com/rknightion/synthkit/blob/main/blueprints/acme-ai-eval.yaml)

The AI-gateway platform operator's view across an 8-cell estate (4 AWS account roles × 2 regions): gateway health, LangSmith platform health, per-cell AWS estate, multi-cloud LLM endpoints, edge, and the qualification pipeline. No single-tenant app traces. Exercises: `portkeygateway`, `portkeypoller`, `langsmithplatform`, `langsmitheval`, `qualificationpipeline`, `bedrock`, `csp_azure` (LLM-access footprint), `csp_gcp` (LLM-access footprint).

---

## Hosts

### [hostfleet.yaml](https://github.com/rknightion/synthkit/blob/main/blueprints/hostfleet.yaml)

Mixed-OS host fleet: Linux/Windows/macOS machines running Grafana Alloy's node/windows/macos exporter, plus optional Docker cAdvisor. Integration and full metric profiles. Exercises: `host` construct across all three OSes.

### [hosts-bare.yaml](https://github.com/rknightion/synthkit/blob/main/blueprints/hosts-bare.yaml)

Bare hosts with no container runtime: demonstrates the `docker: false` dimension and `observability.logs: false` (metrics-only, no log streams) across Linux/Windows/macOS. Exercises: `host`, logs-off configuration.

### [hosts-linux-docker.yaml](https://github.com/rknightion/synthkit/blob/main/blueprints/hosts-linux-docker.yaml)

Linux container hosts running node_exporter + Docker cAdvisor: container CPU/memory/network/filesystem metrics plus container log streams. Exercises: `host` with `docker: true` lane.

### [hosts-macos.yaml](https://github.com/rknightion/synthkit/blob/main/blueprints/hosts-macos.yaml)

macOS endpoint and developer fleet: macos-node exporter metrics (cpu/disk/net/fs + battery/power) on developer laptops and a CI runner. No Docker (unsupported on macOS in v1). Exercises: `host` macOS OS path.

### [hosts-windows.yaml](https://github.com/rknightion/synthkit/blob/main/blueprints/hosts-windows.yaml)

Windows Server estate: windows_exporter metrics + Application/System event log streams. Domain Controller, app server, and SQL Server roles on Server 2022/2025. No Docker. Exercises: `host` Windows path, event log streams.

---

## Network Topology

### [netobs-enterprise.yaml](https://github.com/rknightion/synthkit/blob/main/blueprints/netobs-enterprise.yaml)

"Average enterprise" archetype: one `network_topology` exporter watching a 2-spine / 6-leaf access fabric (mixed Arista + Cisco), standalone mode, LLDP/CDP/BGP, prod-realistic cold-start discovery churn. Exercises: `nettopo` construct, standalone sub-families.

### [netobs-global.yaml](https://github.com/rknightion/synthkit/blob/main/blueprints/netobs-global.yaml)

Maximal network topology: a federation HUB aggregating a 6-spine / 24-leaf Clos fabric across four vendors (Arista, Cisco, Juniper, Nokia), five spoke sites, all seven discovery protocols, OTLP push. Exercises: `nettopo`, federation sub-families (`federation_spoke_*`, `boundary_observation_info`), OTLP push families.

### [netobs-spoke.yaml](https://github.com/rknightion/synthkit/blob/main/blueprints/netobs-spoke.yaml)

A remote-site SPOKE `network_topology` exporter pushing its local graph to the federation hub (`netobs-global`). Exercises the spoke-side liveness families a hub/standalone deployment never emits. Exercises: `nettopo` spoke sub-families.

---

## Synthetic Monitoring / Fleet Management

### [synthetic-monitoring.yaml](https://github.com/rknightion/synthkit/blob/main/blueprints/synthetic-monitoring.yaml)

Grafana Synthetic Monitoring estate: HTTP probe checks (`probe_success` / `probe_duration_seconds` families) plus a Fleet Management collector roster across linux/windows/darwin. No cloud infrastructure — all telemetry flows from the `features:` block. Exercises: `sm`, `fleetmgmt`.

### [fleet-management.yaml](https://github.com/rknightion/synthkit/blob/main/blueprints/fleet-management.yaml)

Standalone Fleet Management showcase: a roster of synthetic Alloy collectors across linux/windows/darwin, emitting the Alloy self-metric set and registering with the Fleet Management API when `GC_FM_*` credentials are present. Exercises: `fleetmgmt`.

---

## Profiling

### [profiling-demo.yaml](https://github.com/rknightion/synthkit/blob/main/blueprints/profiling-demo.yaml)

End-to-end Pyroscope profiling: k8s eBPF process_cpu per pod, a `web_service` with SDK-push profiles + span profiles, and an `app` service-graph with per-node profiles. Exercises: `k8s_profiling`, `web_service` with `pyroscope:` block, `app` workload with per-node `pyroscope:` blocks.

---

## Native OTLP

### [otlp-native.yaml](https://github.com/rknightion/synthkit/blob/main/blueprints/otlp-native.yaml)

Native OTLP application-metrics showcase: two `web_service` workloads (one in `k8s_monitoring` mode, one in `naked` mode) emit `http.server.request.duration` and `http.server.active_requests` as OTLP/HTTP to `/v1/metrics`, letting the Grafana Cloud OTLP gateway own the Prometheus translation. Exercises: `web_service` with `otel.metrics: true`, both `mode: k8s_monitoring` and `mode: naked`.
