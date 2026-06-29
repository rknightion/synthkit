# Changelog

All notable changes to synthkit. Generated from Conventional Commits.

## [1.0.0] - 2026-06-29

### Features

- Initial public release of synthkit — composable synthetic telemetry generator for Grafana Cloud.
  Declare infrastructure and applications in YAML blueprints; synthkit emits structurally-correct
  synthetic metrics (Prometheus Remote-Write v2), traces (OTLP), and logs (Loki) for whatever each
  declared construct supports.

### Constructs included

- AWS: EC2, RDS, ElastiCache, CloudWatch infrastructure (ALB/EBS/NAT/EKS/S3/Firehose)
- Azure: CSP Azure (VMs, App Service, SQL, Storage, Cosmos DB, Functions)
- GCP: CSP GCP (Compute, Cloud SQL, Cloud Storage, Pub/Sub, Cloud Run)
- Kubernetes: cluster metrics, k8s-monitoring, addon correlation (cert-manager, Karpenter, ArgoCD, Envoy Gateway, CoreDNS, AWS LBC, external-dns, cluster-autoscaler)
- AI/LLM: gen_ai request flow, Portkey gateway, LangSmith evaluation, Bedrock, AgentCore, Snowflake Cortex, LangGraph
- Grafana products: dbo11y, Fleet Management, Synthetic Monitoring, Faro RUM, Beyla eBPF
- Network: network_topology (SNMP topology)
- Application: web_service, web_vitals

### Build & CI

- AGPL-3.0-only license + full OSS governance apparatus (CONTRIBUTING.md, CODE_OF_CONDUCT.md, SECURITY.md)
- SPDX headers enforced on every `.go` file via `scripts/spdx-check.sh`
- Forbidden-words hygiene guard (`scripts/forbidden-words.sh`) — credential shapes always-on, deployment identifiers via CI secret
- release-please changelog automation + Renovate dependency management
- GitHub Actions: release-please, publish (GHCR multi-arch image), CodeQL, zizmor, actionlint, dependency review
