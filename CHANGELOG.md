# Changelog

All notable changes to synthkit. Generated from Conventional Commits.

## [1.3.0](https://github.com/rknightion/synthkit/compare/v1.2.0...v1.3.0) (2026-08-21)


### Features

* complete operational readiness wave 3 ([265ecd9](https://github.com/rknightion/synthkit/commit/265ecd93c6fd76d929b87a7afe344ae18af6bfcc))
* **docs:** take the fleet project icon for the site logo and favicon ([8388082](https://github.com/rknightion/synthkit/commit/8388082436b4586784be6275523f30ad256f7c67))
* **docs:** take the fleet-generated social card ([3872de1](https://github.com/rknightion/synthkit/commit/3872de1aef30082221e45f43eb71a4ca9da74634))
* expose delivery loss and queue pressure ([9488c2a](https://github.com/rknightion/synthkit/commit/9488c2af28f6a41fd2b238df8c6ddc4eb0e2806e))
* harden control plane exposure ([ef04f44](https://github.com/rknightion/synthkit/commit/ef04f44365ee4a6c961a7065d57b4d9b801578e5))
* improve operational onboarding wave 2 ([e98411b](https://github.com/rknightion/synthkit/commit/e98411b5cec4494be44497790117ca3ff91aa9c9))
* make deployment readiness explicit ([b6c4ea5](https://github.com/rknightion/synthkit/commit/b6c4ea576473230c1ec7fa91b647b96bd94cd76f))
* make optional Grafana lanes deployable ([189ecd5](https://github.com/rknightion/synthkit/commit/189ecd5c7e496412daa0e361180bf5325c0487b3))
* make upgrades reproducible and rollback testable ([729d4b1](https://github.com/rknightion/synthkit/commit/729d4b17a69736708882c7f6140e3ae610cabd74))
* mint release-please token from the OpenBao broker ([7316c0a](https://github.com/rknightion/synthkit/commit/7316c0a86c3d4c8c5bdfda9ecc0600030bbc0b97))
* mint the docs-sync token from the OpenBao broker ([68a661f](https://github.com/rknightion/synthkit/commit/68a661fe5092b98f55064d10213cbf35f0f9f170))
* require explicit blueprint selection ([9acd17f](https://github.com/rknightion/synthkit/commit/9acd17f85b7a98542a3d4d3a17a5343de3692cea))
* start operational readiness campaign ([9c93f5c](https://github.com/rknightion/synthkit/commit/9c93f5c94938cf6eb17ee275b54473f9c8dfe08d))


### Bug Fixes

* author is Rob Knight, not Rob Knighton ([9facdca](https://github.com/rknightion/synthkit/commit/9facdcac9f6c85dc74c230c7e34f251e68589c08))
* bind RC publisher dispatch to repository ([e0e87a9](https://github.com/rknightion/synthkit/commit/e0e87a97e700d5d68e918a3523ff2a410d3c3f2e))
* **deps:** update dependency @solidjs/router to v1 ([#66](https://github.com/rknightion/synthkit/issues/66)) ([1e776e7](https://github.com/rknightion/synthkit/commit/1e776e76c7603a2208e2022fb1e9ae562732fe94))
* **deps:** update module github.com/grafana/nanogit to v1.4.1 ([#60](https://github.com/rknightion/synthkit/issues/60)) ([465fa17](https://github.com/rknightion/synthkit/commit/465fa17ec2d068357206bc191f650eede9c2671d))
* **deps:** update module github.com/grafana/pyroscope-go to v1.4.1 ([#38](https://github.com/rknightion/synthkit/issues/38)) ([c0eb070](https://github.com/rknightion/synthkit/commit/c0eb070419567fe9bd3cad16ecda51c9ea4008d4))
* **deps:** update module github.com/grafana/pyroscope-go to v1.4.2 ([#82](https://github.com/rknightion/synthkit/issues/82)) ([7465974](https://github.com/rknightion/synthkit/commit/74659740f958b3b8bbf82159d836b9b8a7256010))
* **deps:** update module github.com/testcontainers/testcontainers-go to v0.44.0 ([#77](https://github.com/rknightion/synthkit/issues/77)) ([1a60e59](https://github.com/rknightion/synthkit/commit/1a60e59f923185d3e2b4f9136c7b2e3cc19ad69b))
* **deps:** update module go.opentelemetry.io/proto/otlp to v1.11.0 ([#59](https://github.com/rknightion/synthkit/issues/59)) ([ce1d4b3](https://github.com/rknightion/synthkit/commit/ce1d4b31187d5dd6d55018810aab9711b8a76ea6))
* **deps:** update module google.golang.org/protobuf to v1.36.12 ([#80](https://github.com/rknightion/synthkit/issues/80)) ([4be47d7](https://github.com/rknightion/synthkit/commit/4be47d788fcbcef6efd4bd59f8a8b4a0f7d306e5))
* make published Compose verification deterministic ([fedb517](https://github.com/rknightion/synthkit/commit/fedb517f451f829c1802fa4a7f8503c7a2e6b9dd))
* pass the JWT role explicitly for docs-sync ([6c20935](https://github.com/rknightion/synthkit/commit/6c209359f99fdf0b63f1c319f8464fe9c4bc2f8a))


### Refactor

* **agents:** replace the AGENTS.md symlink with the @AGENTS.md import ([a704865](https://github.com/rknightion/synthkit/commit/a704865eb1c134a01e69b141a1822c8793e7a7c9))


### Documentation

* add a "Why This Generator" positioning page ([830a9d3](https://github.com/rknightion/synthkit/commit/830a9d3247f20da0ea2fee169d12d6c6f08759bb))
* add an FAQ and a security page ([79ed716](https://github.com/rknightion/synthkit/commit/79ed716a5715c4de9c393a013a320e6695176037))
* adopt the m7kni.io inverted docs model ([3a8bb1e](https://github.com/rknightion/synthkit/commit/3a8bb1e272753e4d383eb509454b0860a793c14f))
* carry the AGPL note as license_note instead of overriding copyright ([50559d5](https://github.com/rknightion/synthkit/commit/50559d5c84c70f604a22b9b7d35b686d6bece1b4))
* close control-plane hardening task ([dae3302](https://github.com/rknightion/synthkit/commit/dae3302dd97792cb772c3a4e42f2bd79ff9ee6be))
* close fresh-start deployment task ([5e2be0c](https://github.com/rknightion/synthkit/commit/5e2be0cbb38692f814d936df3b7ffd1ae9591baa))
* delete issue [#26](https://github.com/rknightion/synthkit/issues/26) from GitHub, make the closed-work doc the record ([aada9f2](https://github.com/rknightion/synthkit/commit/aada9f2499680b98ef3b0814060ac81ff4d697f7))
* finalize operational readiness wave 2 ([1569d34](https://github.com/rknightion/synthkit/commit/1569d344ccab04addf301c43753c945b3a7f1892))
* finalize operational readiness wave 3 ([905cf21](https://github.com/rknightion/synthkit/commit/905cf21dcccf751d7eb9483fbe5253cafe28507b))
* finalize operational readiness wave A ([225cd62](https://github.com/rknightion/synthkit/commit/225cd624d98a2041ce117e850c796b5509b578a9))
* finalize optional Grafana lanes ([a707490](https://github.com/rknightion/synthkit/commit/a70749029c5805b4b9e1ec2318f57f23c015c1cb))
* finalize strict HTTPS e2e repair ([8ea671f](https://github.com/rknightion/synthkit/commit/8ea671f859c4a967ee0f5bef85171b04e4c6f426))
* fix broken internal links ([b690f69](https://github.com/rknightion/synthkit/commit/b690f6974cc98b045dd5c528c625feb4bcb42668))
* match the stated Go version to go.mod ([16f504a](https://github.com/rknightion/synthkit/commit/16f504a47f9c2555e2b5999b2916b03aa6032449))
* put a copy-paste quickstart on the landing page ([a75ec94](https://github.com/rknightion/synthkit/commit/a75ec94417236c1396e79a1f6a4e691c8c42fe6a))
* re-import fan-out protocol (context-cost rules) ([a529c8f](https://github.com/rknightion/synthkit/commit/a529c8f080e1e4719248ee1d118601f2dcbbb42c))
* re-render the fan-out protocol from agent-docs ([3108bb9](https://github.com/rknightion/synthkit/commit/3108bb944a8fab1fcfb3bbd3dd244cd32678ea56))
* re-render the fan-out protocol from agent-docs 711db6c ([99bdf51](https://github.com/rknightion/synthkit/commit/99bdf515fa217cb05d40393e9a8b010dfddb8721))
* re-render the fan-out protocol from agent-docs b0d76d8 ([65df024](https://github.com/rknightion/synthkit/commit/65df0245a9048cec0a9cc94376c3e95483934c91))
* **readme:** lead with what the project is ([1ccf84b](https://github.com/rknightion/synthkit/commit/1ccf84b1f1bce0b8e0ddc28ca44f1a2aec09ed6a))
* **readme:** link the documentation site ([a632a33](https://github.com/rknightion/synthkit/commit/a632a33662d8afdc0252d165d2ef11e19c8241bd))
* record live control-plane verification ([93f957f](https://github.com/rknightion/synthkit/commit/93f957f05a87dca776d2134d80fc3a628363771a))
* record live self-observability verification ([9b35176](https://github.com/rknightion/synthkit/commit/9b3517667d6243e6fad12890859f6de24cd7465b))
* remove deployment identifiers from public records ([d4d48c5](https://github.com/rknightion/synthkit/commit/d4d48c5c684b5533ef7b470c181e243ee89f4b1d))
* remove private host name from backlog ([5667903](https://github.com/rknightion/synthkit/commit/56679036c56815d02b4e0d9d3d0adad2fb131219))
* something ([f13da00](https://github.com/rknightion/synthkit/commit/f13da008eaf4db62ec0c377b97a22f8585598e86))
* **tracker:** align canonical fan-out protocol ([4ac48a2](https://github.com/rknightion/synthkit/commit/4ac48a26ae6bf697f2518439d7b4a2e1068de873))
* **tracker:** correct the canonical owner in the rendered header ([d7442cd](https://github.com/rknightion/synthkit/commit/d7442cd2bb58327ea411ffce6b898f438e0d31eb))
* **tracker:** normalise the closed-issues doc title ([cbd376a](https://github.com/rknightion/synthkit/commit/cbd376a4fc1e36326e68625ecd1e53c045f9cf00))
* **tracker:** re-import the fan-out protocol from canonical ([64d0de2](https://github.com/rknightion/synthkit/commit/64d0de29e597620aa240f4c21ec273ef675fb473))
* **tracker:** render agent documents from the canonical source ([f0b48d7](https://github.com/rknightion/synthkit/commit/f0b48d7964f12e7731703419a6983ee473f057ef))


### Build & CI

* add auto-rc, arm-automerge and ghcr-cleanup ([d698adb](https://github.com/rknightion/synthkit/commit/d698adbd6dd82fa4d91c5022931224222e4e558d))
* docs-sync and grafana-sync targets moved to the m7kni org ([6e37c0f](https://github.com/rknightion/synthkit/commit/6e37c0f346f59191dddffc8f2aa52a745e3f61db))
* repin the release-automation reusables to v1.8.0 ([31539d7](https://github.com/rknightion/synthkit/commit/31539d7c2e5f88cd426eb1882e49cd58326ac495))

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
