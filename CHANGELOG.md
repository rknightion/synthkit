# Changelog

All notable changes to synthkit. Generated from Conventional Commits.

## [1.2.1](https://github.com/rknightion/synthkit/compare/v1.2.0...v1.2.1) (2026-07-23)


### Bug Fixes

* **deps:** update module github.com/grafana/nanogit to v1.4.1 ([#60](https://github.com/rknightion/synthkit/issues/60)) ([465fa17](https://github.com/rknightion/synthkit/commit/465fa17ec2d068357206bc191f650eede9c2671d))
* **deps:** update module github.com/grafana/pyroscope-go to v1.4.1 ([#38](https://github.com/rknightion/synthkit/issues/38)) ([c0eb070](https://github.com/rknightion/synthkit/commit/c0eb070419567fe9bd3cad16ecda51c9ea4008d4))
* **deps:** update module go.opentelemetry.io/proto/otlp to v1.11.0 ([#59](https://github.com/rknightion/synthkit/issues/59)) ([ce1d4b3](https://github.com/rknightion/synthkit/commit/ce1d4b31187d5dd6d55018810aab9711b8a76ea6))

## [1.2.0](https://github.com/rknightion/synthkit/compare/v1.1.0...v1.2.0) (2026-07-03)


### Features

* **docs:** align docs site with m7kni.io brand + server-side SEO/LLM metadata ([67e2144](https://github.com/rknightion/synthkit/commit/67e21448e5c163f224a66d081742f6dd119bfca0)), closes [#26](https://github.com/rknightion/synthkit/issues/26)


### Bug Fixes

* **deps:** update module github.com/grafana/pyroscope-go to v1.4.0 ([#29](https://github.com/rknightion/synthkit/issues/29)) ([cfd6851](https://github.com/rknightion/synthkit/commit/cfd6851996cd8638b0fc4d2c90328c0c90aeb052))


### Documentation

* **geo:** content-shape pass for LLM/search retrievability ([da03919](https://github.com/rknightion/synthkit/commit/da03919070de710eabf96f5fbb54297edd0098a8))


### Build & CI

* add OpenSSF Scorecard via shared reusable workflow ([0ca0911](https://github.com/rknightion/synthkit/commit/0ca0911dcbfdb81e22737f8f7d038e3a17ba5272))
* drop CodeQL pull_request trigger to trim Actions fan-out ([f2ab6dc](https://github.com/rknightion/synthkit/commit/f2ab6dcc00349074efeb5582f50462ff2dd1547d))
* **renovate:** remove local pr limits + minimumReleaseAge pin ([9d740af](https://github.com/rknightion/synthkit/commit/9d740afe984f174f5df63ea4cc33edd6450292c0))

## [1.1.0](https://github.com/rknightion/synthkit/compare/v1.0.0...v1.1.0) (2026-06-30)


### Features

* initial public release ([58765c4](https://github.com/rknightion/synthkit/commit/58765c41ecdd840c690c40027ba3ed635e176619))
* sigil (Grafana AI Observability) signal + grafana-ai-o11y blueprint ([#21](https://github.com/rknightion/synthkit/issues/21)) ([379bce7](https://github.com/rknightion/synthkit/commit/379bce74b17fe75ea86433df9f719e860f4e0ffa))


### Bug Fixes

* **deps:** update module github.com/golang/snappy to v1 ([#17](https://github.com/rknightion/synthkit/issues/17)) ([1fa866f](https://github.com/rknightion/synthkit/commit/1fa866f61c39e59b868f3d03986cfa44d6a9e538))
* **deps:** update module github.com/grafana/nanogit to v1.4.0 ([#12](https://github.com/rknightion/synthkit/issues/12)) ([180ec23](https://github.com/rknightion/synthkit/commit/180ec23e8df473b71b324bdd00e26c9bc7439f45))
* **deps:** update module github.com/testcontainers/testcontainers-go to v0.43.0 ([#13](https://github.com/rknightion/synthkit/issues/13)) ([069c74d](https://github.com/rknightion/synthkit/commit/069c74d1ca2d2ebbc0df110d81027697cb6f17c3))
* **security:** rename theme localStorage key constant to clear Snyk secret heuristic ([389f658](https://github.com/rknightion/synthkit/commit/389f65896ba988c85f1a297707dac09da2d42e40))
* **security:** render sparklines as JSX &lt;svg&gt; instead of innerHTML ([7932d3f](https://github.com/rknightion/synthkit/commit/7932d3f652d437b7a35ab46331b7e76aa8d0dcd4))


### Refactor

* complete golden-thread scrub (routes/identifiers) + rename "Golden Path" → "Connected Gateway" ([f5e7d88](https://github.com/rknightion/synthkit/commit/f5e7d883896f0ebbd3de7111d4d3ca342a060b4a))


### Documentation

* add zensical documentation site + m7kni-net-site sync trigger ([c05c5b3](https://github.com/rknightion/synthkit/commit/c05c5b3d99c2a74aeaf606d7a01174a157877e50))
* fix runbook accuracy against source ([ea0726f](https://github.com/rknightion/synthkit/commit/ea0726fa5cd8d5000c6bab87b5636a002c41d10d))
* scrub "golden thread" jargon from the signal catalogue and docs ([3493bd7](https://github.com/rknightion/synthkit/commit/3493bd73ff3ca4e20a1700bcbd43986dab2081c5))


### Build & CI

* add aggregator job for branch rules ([08c24ee](https://github.com/rknightion/synthkit/commit/08c24ee012e0f264d8c5fdb404de2083c69128fe))
* add Snyk -&gt; Snyk Cloud monitor (SCA/SAST/IaC/container) ([1c8f09f](https://github.com/rknightion/synthkit/commit/1c8f09f12a54da529da2cd865ed25af9cf47d8c0))
* build + publish edge :main image on push to main ([712a0cc](https://github.com/rknightion/synthkit/commit/712a0ccbefdaab54848e07458407ef8f31a15e55))
* **codacy:** add Go coverage upload + tune repo-local exclusions ([fa67535](https://github.com/rknightion/synthkit/commit/fa675353a5de6f260d476bf1bfc0169fad75cd8e))
* drop internal/integration from the -race leg (OOM) ([6b9a4b0](https://github.com/rknightion/synthkit/commit/6b9a4b04f77b85300448b51e243c0f523d80f4c1))
* open the release-please PR under a PAT so CI runs without manual approval ([342f2e9](https://github.com/rknightion/synthkit/commit/342f2e9089b06849baa64645c06f02d9346fded9))
* pin shared rknightion reusables to v1.0.0 ([f370981](https://github.com/rknightion/synthkit/commit/f370981169f1bd7de477b3b9ce986fd69599c2c8))
* publish via shared container-publish reusable (guinea-pig) ([6510d91](https://github.com/rknightion/synthkit/commit/6510d91f631f31d97b15e8c48f7480d82221df74))

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
