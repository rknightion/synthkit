# synthkit signal catalogue (data contract) — index

**Purpose.** This is the *frozen seam* between synthkit's construct/workload catalog and Grafana
Cloud. Every metric name/type, trace span/attribute, and log stream/field synthkit emits is the
exact contract recorded in the per-area files under [`signals/`](./signals/). It is reproduced
byte-exact from a proven private generator (the "predecessor"): synthkit emits the **real**
metric/label/field spellings of the technology each construct models, never invented names.
Anything still unverified lives in [`cantfind.md`](./cantfind.md) — it is NEVER asserted as fact.

> **Accuracy is the whole point.** If a real integration emits a value as a per-period *gauge*, the
> construct emits a gauge; if a CloudWatch namespace dimensions a metric only one way, the construct
> does not invent a per-app dimension. Where reality is "you can't get X", the synthetic data
> reproduces the *absence* so the dashboards teach the real constraint.

Read [ARCHITECTURE.md](./ARCHITECTURE.md) (frozen seams, §5 scoping, §7 invariants I1–I33) and
[CLAUDE.md](./CLAUDE.md) first. Mechanics (remote_write, hand-encoded OTLP, the two-cadence
scheduler, the ledger) are documented there and not repeated here.

---

## How to use this contract

The catalogue lives in [`signals/`](./signals/): cross-cutting rules in
[`signals/00-canon.md`](./signals/00-canon.md), one file per area below. **Each family has prose
(provenance + ⚠ traps) PLUS a fenced ` ```yaml signals ` block** — the machine-readable subset
(names, types, units, labels, verification). Read the prose for nuance; read the block for the
exact emitted contract.

- **Working on a construct?** Open the area file and jump to the family by its literal slug token,
  e.g. grep `[slug: cw-rds]` in [`signals/cw.md`](./signals/cw.md). Slugs are stable and never
  renumber; cite them from code as `signals/<area>.md [slug: <slug>]` (NOT a numeric `§N`).
- **`yaml signals` block schema:** `family` (series prefix), `scope` (blueprint|substrate),
  `sink` (promrw|loki|otlp|faro), `stats` (CW 5-stat expansion; omitted otherwise), `labels` (a
  key→value/shape **map** — `job` is just one label, not metadata), `metrics` (per-metric
  `{root,type,unit,v,note}`). Logs blocks use `stream_labels`/`structured_metadata`/`body_fields`;
  traces use `span_attributes`/`resource_attributes`/`correlation_fields`. The `v:` verification
  legend (`ok`=✅ / `assumed`=Ⓐ / `trap`=⚠) is defined in
  [`signals/00-canon.md`](./signals/00-canon.md). An absent dimension/label is **omitted** from the
  map (never `""`/`NA`).
- **Discovered a real signal via any pathway** (live capture, exporter/agent inspection,
  metric-stream output, vendor docs)? This catalogue is meant to GROW. Add/adjust the family's
  `yaml signals` block + prose in the right area file, resolve the matching `cantfind.md` SK-N
  (move it out of cantfind into the area file), and verify with the inventory diff:
  `DRY_RUN=true go run ./cmd/synthkit -once -dump`. Correct the synth to match observed reality,
  never the reverse.
- **NEVER invent a metric/label/value.** Source it from the area file (or the predecessor → vendor docs
  via `ctx7`); otherwise add a PENDING SK-N to [`cantfind.md`](./cantfind.md) and flag it.

## Global canon — [`signals/00-canon.md`](./signals/00-canon.md)

Cross-cutting rules that span ALL families (area files reference these by slug, never restate them):
`[slug: push-topology]` · `[slug: blueprint-label]` · `[slug: scoping]` · `[slug: env-label-keys]`
· `[slug: request-correlation]` · `[slug: cardinality]` · `[slug: content-strip]` · `[slug: shape-rules]`
· `[slug: cardinality-budget]`.

## Area index

| Area file | What it covers | Scope | Sink(s) | Family slugs |
|---|---|---|---|---|
| [`signals/cw.md`](./signals/cw.md) | AWS CloudWatch metric-stream families | blueprint | promrw | `cw-naming` (LAW), `cw-alb`, `cw-nlb`, `cw-ec2`, `cw-ebs`, `cw-natgw`, `cw-s3`, `cw-eks`, `cw-firehose`, `cw-rds`, `cw-elasticache`, `cw-mwaa`, `cw-docdb`, `cw-neptune`, `cw-aoss`, `cw-glue`, `cw-privatelink` |
| [`signals/k8s.md`](./signals/k8s.md) | k8s-monitoring substrate (KSM, node-exporter, cAdvisor, kubelet, conformance, events) + addon pod correlation | substrate | promrw (+ loki events) | `k8s-label-types`, `k8s-ksm`, `k8s-node-exporter`, `k8s-cadvisor`, `k8s-kubelet`, `k8s-conformance`, `k8s-events`, `k8s-addon-pod-correlation` |
| [`signals/k8s-addons.md`](./signals/k8s-addons.md) | k8s add-on controllers | substrate | promrw | `k8s-lbc`, `k8s-externaldns`, `k8s-coredns`, `k8s-vpc-cni`, `k8s-cert-manager`, `k8s-cluster-autoscaler`, `k8s-ebs-csi`, `k8s-ksm-ingress`, `k8s-karpenter`, `k8s-argocd`, `k8s-envoy-gateway` |
| [`signals/dbo11y.md`](./signals/dbo11y.md) | Database Observability (MySQL, Postgres) | substrate | promrw + loki | `dbo11y-identity`, `dbo11y-shared-labels`, `dbo11ymysql`, `dbo11ymysql-logs`, `dbo11ypg`, `dbo11ypg-logs` |
| [`signals/cspazure.md`](./signals/cspazure.md) | CSP Azure (dual-path: serverless scraper vs azure_exporter) | substrate | promrw | `cspazure` + per-service: `cspazure-compute`, `cspazure-sql`, `cspazure-postgres`, `cspazure-storage`, `cspazure-lb`, `cspazure-appgw`, `cspazure-frontdoor`, `cspazure-vnet`, `cspazure-eventhubs`, `cspazure-servicebus`, `cspazure-logs` |
| [`signals/cspgcp.md`](./signals/cspgcp.md) | CSP GCP | substrate | promrw | `cspgcp` + per-service: `cspgcp-compute`, `cspgcp-cloudsql`, `cspgcp-alloydb`, `cspgcp-storage`, `cspgcp-networking`, `cspgcp-loadbalancing`, `cspgcp-pubsub`, `cspgcp-cloudrun`, `cspgcp-bigtable` |
| [`signals/cloudflare.md`](./signals/cloudflare.md) | Cloudflare zone + tunnel | blueprint | promrw | `cloudflare-zone`, `cloudflare-tunnel` |
| [`signals/apm.md`](./signals/apm.md) | APM span-metrics / service-graph | blueprint | promrw | `apm-calls`, `apm-latency`, `apm-size`, `apm-service-graph`, `apm-service-graph-latency`, `apm-target-info` |
| [`signals/traces.md`](./signals/traces.md) | Traces (v1 span tree → OTLP → Tempo) | blueprint | otlp | `traces-span-tree`, `traces-resource-attrs`, `traces-correlation`, `traces-timing` |
| [`signals/logs.md`](./signals/logs.md) | Logs, Faro/RUM, user-actions, browser spans, k8s-addon logs | blueprint + substrate | loki / faro / otlp | `logs-app`, `logs-sm`, `logs-dbo11y-csp`, `logs-faro-rum`, `logs-user-actions`, `logs-browser-spans`, `logs-derived-cloud`, `logs-k8s-addons` |
| [`signals/sm.md`](./signals/sm.md) | Synthetic Monitoring (fake SM checks) | substrate | promrw + loki | `sm-checks`, `sm-logs` |
| [`signals/fm.md`](./signals/fm.md) | Fleet Management + Alloy meta-health + content sentinel | substrate | promrw | `fm-fleet`, `fm-alloy-health`, `fm-content-sentinel` |
| [`signals/genai.md`](./signals/genai.md) | gen_ai semconv attrs + `gen_ai_client_*`/`gen_ai_server_*` metrics (workload-emitted) | blueprint | promrw + otlp | `genai-spans`, `genai-metrics` |
| [`signals/genai-models.md`](./signals/genai-models.md) | Per-model LLM catalogue (IDs, families, cost in/out per 1M tokens, VolumeWeight) — mirror of `internal/genai/models.go`; all four platforms (bedrock / azure-openai / openai / vertex-ai) | — | — | `genai-models`, `genai-models-bedrock`, `genai-models-azure-openai`, `genai-models-openai`, `genai-models-vertex` |
| [`signals/portkey.md`](./signals/portkey.md) | Portkey LLM gateway (scrape) + Analytics poller + export log + derived rules | substrate | promrw + loki | `portkey-gateway`, `portkey-poller`, `portkey-logs`, `portkey-derived` |
| [`signals/bedrock.md`](./signals/bedrock.md) | AWS Bedrock CloudWatch (`AWS/Bedrock` + `/Agents` + `/Guardrails`) + invocation log | blueprint | promrw + loki | `bedrock-core`, `bedrock-agents`, `bedrock-guardrails`, `bedrock-logs` |
| [`signals/agentcore.md`](./signals/agentcore.md) | AWS Bedrock-AgentCore CloudWatch (invocation-class + resource-usage) + logs | blueprint | promrw + loki | `agentcore-invocation`, `agentcore-resource-usage`, `agentcore-logs` |
| [`signals/langsmith.md`](./signals/langsmith.md) | LangSmith platform self-metrics (scrape) + eval poll-gauges + runs log | substrate | promrw + loki | `langsmith-platform`, `langsmith-eval`, `langsmith-runs` |
| [`signals/snowflake.md`](./signals/snowflake.md) | Snowflake `prometheus.exporter.snowflake` (27 gauges) | substrate | promrw | `snowflake` |
| [`signals/nettopo.md`](./signals/nettopo.md) | Network topology exporter (SNMP-based; device inventory, reconciled edges, change/conflict events, discovery health, freshness, self-obs; gated: session-pool, federation hub/spoke, OTLP-push) | substrate | promrw + loki | `nettopo-identity`, `nettopo-inventory`, `nettopo-edges`, `nettopo-changes`, `nettopo-discovery-health`, `nettopo-freshness`, `nettopo-self-obs`, `nettopo-session-pool`, `nettopo-federation`, `nettopo-otlp`, `nettopo-logs` |
| [`signals/beyla.md`](./signals/beyla.md) | Grafana Beyla eBPF auto-instrumentation (RED + network-flow + span-metrics xref + internal agent; ebpf_only + coexist_sdk contexts) | blueprint + substrate | promrw + otlp | `beyla-red-application`, `beyla-network`, `beyla-spanmetrics`, `beyla-internal`, `beyla-traces` |
| [`signals/otlp-metrics.md`](./signals/otlp-metrics.md) | Native OTLP application metrics (web_service `otel:` lane) — emitted OTLP instruments + expected post-gateway Prometheus shape (`http_server_request_duration_seconds`, `http_server_active_requests`, `target_info`, promoted labels, `otel_scope_*`) for naked + k8s_monitoring modes | blueprint | otlp | `otlp-metrics-emitted`, `otlp-duration`, `otlp-active-requests`, `otlp-resource-attrs`, `otlp-gateway-prom`, `otlp-target-info` |
| [`signals/qualification.md`](./signals/qualification.md) | GitLab CI qualification/validation pipeline (`gitlab_ci_pipeline_*` exporter families + coined `qualification_*` suite signals) | substrate | promrw + loki | `qualification-pipeline` |
| [`signals/profiles.md`](./signals/profiles.md) | Grafana Pyroscope continuous-profiling types + labels (eBPF/pprof-scrape/java/SDK-push emitter shapes) | blueprint + substrate | pyroscope | `profiles-discriminators`, `profiles-runtime-map`, `profiles-ebpf`, `profiles-pprof`, `profiles-java`, `profiles-sdk-go`, `profiles-sdk-python`, `profiles-pending` |
| [`signals/host.md`](./signals/host.md) | Standalone (non-k8s) host exporters via Alloy integrations — Linux node_exporter, macOS macos-node, windows_exporter, Docker cAdvisor (+ per-OS log streams); `integration`/`full` profiles; shares `internal/nodeexp` with the k8s profile | substrate | promrw + loki | `host-identity`, `host-node-linux`, `host-node-macos`, `host-windows`, `host-docker`, `host-logs` |

**AI/LLM catalogue (Spec 2b).** The gen_ai / LLM-gateway / agent-workflow / eval-platform families
are admitted as generic, tech-native catalogue (see [`signals/00-canon.md`](./signals/00-canon.md)).
The trace tree admits typed AI hops (`gateway`/`model`/`agent`/`tool`/`workflow`/`retrieval`)
carrying `gen_ai.*` attrs from `internal/genai`; see the genai/portkey/bedrock/agentcore/langsmith/
snowflake family files below. Customer identity stays blueprint-only.
