# Fleet Management + Alloy meta-health + content sentinel — ScopeSubstrate

## Optional gateway pool [slug: fm-gateway]

The `alloy_health.gateway` profile replaces the legacy two HA reporters, not the
standalone Fleet Management collector mirror. Omission preserves the legacy 46
series plus the optional six syslog health series. Each declared live process has
9 common series: target `up` (1), controller healthy count (1), peer states (3),
alive peers (1), and same-process remote configuration data (3). A central scrape
role adds 2; OTLP receive adds 3 counters per configured signal; cloud forwarding
adds 5 series per signal (2 queue gauges and 3 counters); syslog adds 3 using
[slug: fm-syslog-health]. Thus the three-signal, all-role fixture has
`9 + 2 + 9 + 15 + 3 = 38` per process, 76 per two-member pool. These are a selected
synthetic subset, not a full Alloy endpoint. A source gap removes exactly 3 from
one process; instance loss leaves only its independently observed target `up=0`.

### Pinned provenance

All names, instrument types and units below come from **Alloy v1.20.1**, its pinned
**Collector v0.161.0** (receiverhelper v1.67.0), and **ckit bbed30d6364e**:

- [Exporter instruments](https://github.com/open-telemetry/opentelemetry-collector/blob/v0.161.0/exporter/exporterhelper/internal/metadata/generated_telemetry.go#L121-L237):
  monotonic item counters with brace units `{span}`, `{datapoint}`, `{record}`;
  queue gauges have `{batch}` units. [Queue attributes](https://github.com/open-telemetry/opentelemetry-collector/blob/v0.161.0/exporter/exporterhelper/internal/queue/obs_queue.go#L44-L80)
  are `exporter` and `data_type` (`traces|metrics|logs`); item counters carry
  `exporter`, as in [sender](https://github.com/open-telemetry/opentelemetry-collector/blob/v0.161.0/exporter/exporterhelper/internal/obs_report_sender.go#L73-L105).
- [Receiver metadata](https://github.com/open-telemetry/opentelemetry-collector/blob/v0.161.0/receiver/receiverhelper/metadata.yaml#L8-L104)
  defines accepted/refused/failed counters for all three signals; [attributes](https://github.com/open-telemetry/opentelemetry-collector/blob/v0.161.0/receiver/receiverhelper/obsreport.go#L57-L72)
  are configured `receiver` and `transport`. OTLP uses configured gRPC transport;
  the syslog adapter's absent transport is omitted. Healthy intake has zero refused
  and failed, not fabricated parser errors.
- Final names: Alloy [receiver](https://github.com/grafana/alloy/blob/v1.20.1/internal/component/otelcol/receiver/receiver.go#L140-L143)
  and [exporter](https://github.com/grafana/alloy/blob/v1.20.1/internal/component/otelcol/exporter/exporter.go#L172-L175)
  use SDK Prometheus v0.68.0 with `WithRegisterer` and `WithoutTargetInfo` only.
  [SDK default](https://github.com/open-telemetry/opentelemetry-go/blob/exporters/prometheus/v0.68.0/exporters/prometheus/config.go#L34-L35)
  selects `UnderscoreEscapingWithSuffixes`. Alloy's pinned **otlptranslator v1.0.0**
  [metric namer](https://github.com/prometheus/otlptranslator/blob/v1.0.0/metric_namer.go#L235-L244)
  adds `_total` to monotonic counters; [unit namer](https://github.com/prometheus/otlptranslator/blob/v1.0.0/unit_namer.go#L100-L110)
  excludes brace-enclosed units from suffixes. [Label normalization](https://github.com/prometheus/otlptranslator/blob/v1.0.0/label_namer.go)
  uses underscores. No detailed error attributes are emitted.
- [ckit metrics](https://github.com/grafana/ckit/blob/bbed30d6364e/metrics.go#L58-L64)
  register peer gauge with `cluster_name,state`; [alive peers](https://github.com/grafana/ckit/blob/bbed30d6364e/metrics.go#L138-L146)
  includes the local process. [Peer enum](https://github.com/grafana/ckit/blob/bbed30d6364e/peer/state.go#L9-L40)
  is `viewer|participant|terminating`.
- [Remote config registration](https://github.com/grafana/alloy/blob/v1.20.1/internal/service/remotecfg/metrics.go#L41-L64)
  defines the three unlabelled instruments. [Load behavior](https://github.com/grafana/alloy/blob/v1.20.1/internal/service/remotecfg/config_manager.go#L317-L357)
  sets unsuccessful load state. Scrape identity attaches separately. No hash family
  or unregistered last-received hash is emitted.
- [Controller registration](https://github.com/grafana/alloy/blob/v1.20.1/internal/runtime/internal/controller/metrics.go#L112-L121)
  defines `controller_path,controller_id,health_type`; empty path is omitted.
  [Health enum](https://github.com/grafana/alloy/blob/v1.20.1/internal/component/component_health.go#L68-L81)
  explicitly returns lowercase `healthy`. The positive count is a simplified graph
  with one healthy component per declared role, not a discovered topology.
- [Scrape registrations](https://github.com/grafana/alloy/blob/v1.20.1/internal/component/prometheus/scrape/scrape.go#L352-L368)
  define target count and moved-target counter. Component-scoped `component_id`
  comes from [Alloy registerer](https://github.com/grafana/alloy/blob/v1.20.1/internal/runtime/internal/controller/node_builtin_component.go).
  [Target identity and up](https://github.com/grafana/alloy/blob/v1.20.1/docs/sources/reference/components/prometheus/prometheus.scrape.md#L263-L285)
  establish configured `job,instance` and 0/1 target health. `cluster` is the existing
  substrate identity, not a newly claimed vendor instrument dimension.

Pool/site/member roles and volumes are bounded operator config, not vendor-default
role/site labels. `site` is documentation-only. Component IDs identify configured
instances, never per-request values. The model selects queue `sizer=requests`,
capacity in batches, and fixed items/request. [Collector queue contract](https://github.com/open-telemetry/opentelemetry-collector/blob/v0.161.0/exporter/exporterhelper/README.md#L19-L37)
uses default capacity 1000; [storage](https://github.com/open-telemetry/opentelemetry-collector/blob/v0.161.0/exporter/exporterhelper/README.md#L78-L91)
is opt-in. Synthkit retains queue state for the emitter lifetime across modeled
losses, not disk WAL, actual crash durability, retry expiry or arbitrary-size batches.

```yaml signals
family: fm_gateway
scope: substrate
sink: promrw
labels:
  cluster: <declared-cluster>
  job: <bounded-configured-job>
  instance: <declared-host:port>
metrics:
  - {root: up, type: gauge, unit: bool, v: ok, note: "target reachability, not source freshness"}
  - {root: alloy_component_controller_running_components, type: gauge, unit: count, v: ok, note: "+controller_id,health_type; healthy count"}
  - {root: cluster_node_peers, type: gauge, unit: count, v: ok, note: "+cluster_name,state; three peer states"}
  - {root: cluster_node_gossip_alive_peers, type: gauge, unit: count, v: ok, note: "+cluster_name; includes local member"}
  - {root: remotecfg_last_load_successful, type: gauge, unit: bool, v: ok}
  - {root: remotecfg_load_attempts_total, type: counter, unit: count, v: ok}
  - {root: remotecfg_load_failures_total, type: counter, unit: count, v: ok}
  - {root: prometheus_scrape_targets_gauge, type: gauge, unit: count, v: ok, note: "+component_id; central_scrape only"}
  - {root: prometheus_scrape_targets_moved_total, type: counter, unit: count, v: ok, note: "+component_id; outgoing moves, not initial placement"}
  - {root: otelcol_receiver_accepted_spans_total, type: counter, unit: count, v: ok, note: "+receiver,transport; OTLP traces"}
  - {root: otelcol_receiver_refused_spans_total, type: counter, unit: count, v: ok, note: "+receiver,transport"}
  - {root: otelcol_receiver_failed_spans_total, type: counter, unit: count, v: ok, note: "+receiver,transport"}
  - {root: otelcol_receiver_accepted_metric_points_total, type: counter, unit: count, v: ok, note: "+receiver,transport; OTLP metrics"}
  - {root: otelcol_receiver_refused_metric_points_total, type: counter, unit: count, v: ok, note: "+receiver,transport"}
  - {root: otelcol_receiver_failed_metric_points_total, type: counter, unit: count, v: ok, note: "+receiver,transport"}
  - {root: otelcol_receiver_accepted_log_records_total, type: counter, unit: count, v: ok, note: "+receiver,transport; OTLP logs; syslog separate receiver"}
  - {root: otelcol_receiver_refused_log_records_total, type: counter, unit: count, v: ok, note: "+receiver,transport"}
  - {root: otelcol_receiver_failed_log_records_total, type: counter, unit: count, v: ok, note: "+receiver,transport"}
  - {root: otelcol_exporter_queue_size, type: gauge, unit: count, v: ok, note: "+exporter,data_type; batches"}
  - {root: otelcol_exporter_queue_capacity, type: gauge, unit: count, v: ok, note: "+exporter,data_type; batches"}
  - {root: otelcol_exporter_sent_spans_total, type: counter, unit: count, v: ok, note: "+exporter; items successfully delivered"}
  - {root: otelcol_exporter_send_failed_spans_total, type: counter, unit: count, v: ok, note: "+exporter; failed attempts, not lost items"}
  - {root: otelcol_exporter_enqueue_failed_spans_total, type: counter, unit: count, v: ok, note: "+exporter; rejected items"}
  - {root: otelcol_exporter_sent_metric_points_total, type: counter, unit: count, v: ok, note: "+exporter"}
  - {root: otelcol_exporter_send_failed_metric_points_total, type: counter, unit: count, v: ok, note: "+exporter"}
  - {root: otelcol_exporter_enqueue_failed_metric_points_total, type: counter, unit: count, v: ok, note: "+exporter"}
  - {root: otelcol_exporter_sent_log_records_total, type: counter, unit: count, v: ok, note: "+exporter"}
  - {root: otelcol_exporter_send_failed_log_records_total, type: counter, unit: count, v: ok, note: "+exporter"}
  - {root: otelcol_exporter_enqueue_failed_log_records_total, type: counter, unit: count, v: ok, note: "+exporter"}
```

No gateway logs, attributes, invented gap/expiry metric, guessed build revision or
Go version are added. Syslog counters retain the pinned mechanic contract below.
Generic failed sends do not uniquely diagnose expired credentials. Source absence
is deliberately injected into collection/publication of the same-process
`alloy_remotecfg` profile, **before** `World.Metrics.Write`, while polling state
continues. It is not a vendor claim that healthy services naturally erase metrics.
The expected cadence is 60 seconds. See the gateway page for absence queries and
failure limits.


Substrate-scoped families: no `blueprint` label. Disambiguated by `cluster` + `collector_id`
(FM / fake collectors) or `cluster` + `instance` (in-cluster Alloy pods). Global rules and
scoping invariants: see [`00-canon.md`](00-canon.md) `[slug: content-strip]`.

*Provenance: predecessor extract 06 + `internal/fleet/*`, `internal/emit/{fm_collectors,alloy_health}.go`.
**Live-validated** against the Grafana Fleet Management product stack (Rob) — `v: ok`, not assumed.*

---

## Fleet Management (fake collectors) [slug: fm-fleet]

A runtime controller goroutine owns all FM API side effects (register / heartbeat via GetConfig /
unregister); the emitter only reads the roster. **Gating:** `fleet.Enabled()` (not a feature key).

Roster: `OSTypes=[linux,windows,darwin]`, `CollectorVersion` config-driven. Deterministic IDs
`<bp>-{env}-{os}-{NN}`; `instance="alloy-{lower(env)}-{os}-{NN}"`; `cluster=ClusterForEnv(env)`;
round-robin across envs per OS. `LocalAttributes={"collector.version", "collector.os"}` on EVERY API
call (heartbeat is only recorded when `local_attributes` non-empty — omitting them makes collectors
go inactive).

FM API (CollectorService, connect-JSON over HTTP POST, basic auth `stackID:token` — ⚠ FM stackID ≠
Mimir push user, T9 — distinct `GC_FM_STACK_ID` credential, SK-23 resolved): `RegisterCollector`, `GetConfig` (heartbeat — `local_attributes`
MUST be non-empty), `UnregisterCollector`. Reconcile every 45s. ⚠ FM online status = heartbeat recency
(Redis TTL), NOT a metric; the FM page's red "unhealthy" badge requires a FIRING alert carrying
`collector_id` — metric-only simulation only affects dashboards, not the FM page badge (T6/T7; the
FM-page badge is de-scoped as cosmetic — SK-24, see cantfind notes).

**Alloy self-metrics per fake collector** (base labels `job="integrations/alloy"`, `namespace="infra"`,
`cluster`+`k8s_cluster_name`, `instance`, `collector_id`, `os`): `alloy_build_info` (G=1; +`version`
⚠ must start `"v"`, T10), `up` (G; 1 healthy / 0 unhealthy), `alloy_component_controller_running_components`
(G; `health_type` ∈ {healthy(24),unhealthy(0/4),unknown(0),exited(0)}), `alloy_component_evaluation_seconds`
(H; 24 obs/tick; buckets `[.005,.025,.1,.5,1,5,10,30,60,120,300,600]`),
`alloy_resources_process_cpu_seconds_total` (C), `alloy_resources_process_resident_memory_bytes` (G),
`alloy_resources_machine_{rx,tx}_bytes_total` (C), `alloy_config_hash` (G=1; +`hash=sha256hex(collectorID)`),
`go_goroutines` (G), `go_memstats_heap_inuse_bytes` (G), `scrape_duration_seconds` (G). ⚠ NOT emitted:
`remotecfg_*`, `prometheus_remote_storage_*` (real-Alloy remote_write internals — would misrepresent a
fake collector). ⚠ Stale-series leak guard: on roster shrink the emitter rebuilds State to drop departed
collectors (else `up=1` persists, contradicting unregistration, T8).

```yaml signals
family: fm_fleet
scope: substrate
sink: promrw
labels:
  job: integrations/alloy
  namespace: infra
  cluster: <cluster>
  k8s_cluster_name: <cluster>
  instance: alloy-<env>-<os>-<NN>
  collector_id: <bp>-<env>-<os>-<NN>
  os: linux|windows|darwin
  env: <env>
metrics:
  - {root: alloy_build_info, type: gauge, unit: bool, v: ok, note: "G=1; version label must start 'v' (T10)"}
  - {root: up, type: gauge, unit: bool, v: ok, note: "1 healthy / 0 unhealthy"}
  - {root: alloy_component_controller_running_components, type: gauge, unit: count, v: ok, note: "health_type ∈ {healthy(24),unhealthy(0/4),unknown(0),exited(0)}"}
  - {root: alloy_component_evaluation_seconds, type: histogram, unit: seconds, v: ok, note: "24 obs/tick; buckets [.005,.025,.1,.5,1,5,10,30,60,120,300,600]"}
  - {root: alloy_resources_process_cpu_seconds_total, type: counter, unit: seconds, v: ok}
  - {root: alloy_resources_process_resident_memory_bytes, type: gauge, unit: bytes, v: ok}
  - {root: alloy_resources_machine_rx_bytes_total, type: counter, unit: bytes, v: ok}
  - {root: alloy_resources_machine_tx_bytes_total, type: counter, unit: bytes, v: ok}
  - {root: alloy_config_hash, type: gauge, unit: bool, v: ok, note: "G=1; hash=sha256hex(collectorID)"}
  - {root: go_goroutines, type: gauge, unit: count, v: ok}
  - {root: go_memstats_heap_inuse_bytes, type: gauge, unit: bytes, v: ok}
  - {root: scrape_duration_seconds, type: gauge, unit: seconds, v: ok}
not_emitted: [remotecfg_*, prometheus_remote_storage_*, "real-Alloy remote_write internals — would misrepresent a fake collector"]
```

---

## FM k8s-monitoring collector mirror [slug: fm-k8s-mirror]

When a cluster sets `k8s_monitoring.fleet_management: true`, synthkit additionally registers the
exact Alloy collectors a real grafana/k8s-monitoring Helm deploy (v3) would create — feature-gated
and node-derived — *in addition to* any standalone `collectors_per_os` fleet. The collector set is
gated by `k8s_monitoring.features` (collector exists iff its feature is on, matching the chart's
"a feature requires its collector" rule):

| feature(s) | Alloy role | workload | per-node? |
|---|---|---|---|
| `cluster_metrics` | `alloy-metrics` | StatefulSet (`metrics_replicas`, default 1; NOT node-scaled) | no |
| `cluster_events` | `alloy-singleton` | Deployment (1) | no |
| `pod_logs` \| `node_logs` | `alloy-logs` | DaemonSet | **yes — 1 per live node** |
| `profiling` | `alloy-profiles` | DaemonSet | **yes — 1 per live node** |
| `application_observability` | `alloy-receiver` | Deployment (or DaemonSet if `receiver_as_daemonset`) | only if daemonset |

**Collector ID** = the chart's `GCLOUD_FM_COLLECTOR_ID` form from `_collector_remoteConfig.tpl`
(synthkit posts the bare id; FM prepends `resource-` server-side). With `R`=release
(`grafana-k8s-monitoring`), `C`=cluster, `N`=namespace (`monitoring`):
- DaemonSet: `<R>-<C>-<N>-alloy-<role>-<nodeHostname>` (one per live node — `LiveNodes`-derived, so
  it churns with scaling; the T8 stale-series guard drops departed nodes' collectors).
- StatefulSet: `<R>-<C>-<N>-<R>-alloy-metrics-<ordinal>` (release segment intentionally doubled).
- Deployment: `<R>-<C>-<N>-<R>-alloy-<role>-<rsHash>-<podSuffix>` (rsHash/podSuffix `fixture.HexID`-
  derived for determinism — real clusters get fresh hashes per rollout; synth keeps them stable).

**FM register `local_attributes`** (chart `_collector_remoteConfig.tpl` `attributes` block, verbatim,
camelCase): the two reserved Alloy keys `collector.os="linux"` + `collector.version=<cluster
AlloyVersion>` (drive the FM UI's "Operating System"/"Alloy version" columns) PLUS the chart's user
attributes — `cluster`, `platform="kubernetes"`, `source="k8s-monitoring"` (Chart.Name),
`sourceVersion=<chart_version>`, `release=<R>`, `namespace`, `workloadName=<role>`,
`workloadType=<controller.type>`. Note `workloadType` here is the **controller type**
(`deployment` for the singleton), distinct from the metric `workload_type` below.

**Self-metric labels** (kube service-discovery relabeling — these differ from the FM register attrs):
`job="integrations/alloy"`, `instance=<pod-ip>:12345`, `cluster`+`k8s_cluster_name`,
`namespace="monitoring"`, `app=<role>` (short, e.g. `alloy-logs`), `workload=<R>-<role>` (full),
`workload_type` ∈ {`daemonset`,`statefulset`,`replicaset`} (a Deployment's pods read **`replicaset`**,
the kube owner kind — NOT `deployment`), `pod=<podname>`, `source="kubernetes"`, `container="alloy"`,
`version` (on `alloy_build_info`). ⚠ NO `os` label (real k8s scrapes omit it; `goos` lives on
build_info only). `collector_id`=the FM id is retained as a synthkit-specific metrics↔registration
correlation label (real k8s collectors don't emit it) — it is the join the roster≡metrics coherence
guard uses. The registered roster is byte-identical to the emitted `collector_id` set at every settled
scaling snapshot (eventual coherence during a scale event).

> ✅ PROVENANCE (resolved 2026-06-15): FM register attribute keys + values confirmed against
> `k8s-monitoring-helm` chart source `charts/k8s-monitoring/templates/collectors/_collector_remoteConfig.tpl`
> (v4.1.5). Self-metric label set + the `instance=<ip>:12345` form +
> `workload_type=replicaset`-for-Deployment confirmed by a live capture of a reference EKS cluster
> k8s-monitoring deploy (`gcx metrics query 'alloy_build_info'` /
> `'alloy_resources_process_cpu_seconds_total'`). The earlier snake_case guesses were corrected to match.

---

## Alloy meta-health [slug: fm-alloy-health]

HA: 2 Alloy pod replicas in `namespace="infra"` on the cluster. Base labels `cluster`+
`k8s_cluster_name`, `namespace="infra"`, `job="integrations/alloy"`, `instance` (one of two pod IPs).
⚠ No `source="kubernetes"` (these are Alloy's OWN `/metrics`, not k8s-monitoring scrapes).

- otelcol receiver (`receiver` ∈ {otlp,prometheus}): `otelcol_receiver_accepted_spans_total` (C),
  `otelcol_receiver_refused_spans_total` (C; ≈0.05% normal, up to 15% under backpressure).
- otelcol exporter (`exporter` ∈ {otlphttp,prometheusremotewrite}): `otelcol_exporter_sent_spans_total`
  (C), `otelcol_exporter_send_failed_spans_total` (C; ≈0.03% normal, up to ~20% under span_drops),
  `otelcol_exporter_queue_size` (G; 2–10 normal, up to 100 under backpressure). Failure-mode extras:
  `prometheus_remote_write_sent_samples_total`/`_dropped_samples_total` (under remote_write_reject),
  `prometheus_remote_storage_wal_storage_size_bytes` + `prometheus_tsdb_wal_segment_current` (under
  wal_pressure; +`component="prometheus.remote_write"`).
- otelcol processor (`processor="batch"`): `otelcol_processor_batch_batch_send_size_count` (C),
  `otelcol_processor_batch_metadata_cardinality` (G).
- per-pipeline `up` (G; +`pipeline`). ⚠ **Pipeline enum is workload-derived, not AI-specific** — in
  v1 the pipelines map to the declared workload lanes (e.g. `app_otlp`); the predecessor's
  gateway/agent/eval pipeline names are NOT carried into synthkit.
- `alloy_build_info` (G=1; emitted for EVERY enabled cluster × 2 pods; `version` must start `"v"`,
  T12). All other Alloy series use the PRD cluster only.

Build-info constants (version/revision/goversion) are config-driven; ✅ live-observed defaults
`v1.16.3` / `1e2007e` / `go1.26.3` (SK-25 resolved).

```yaml signals
family: fm_alloy_health
scope: substrate
sink: promrw
labels:
  cluster: <cluster>
  k8s_cluster_name: <cluster>
  namespace: infra
  job: integrations/alloy
  instance: <pod-ip>
  env: <env>
metrics:
  - {root: alloy_build_info, type: gauge, unit: bool, v: ok, note: "G=1; version must start 'v' (T12); every enabled cluster × 2 pods"}
  - {root: up, type: gauge, unit: bool, v: ok, note: "per-pipeline; +pipeline label"}
  - {root: otelcol_receiver_accepted_spans_total, type: counter, unit: count, v: ok, note: "receiver ∈ {otlp,prometheus}"}
  - {root: otelcol_receiver_refused_spans_total, type: counter, unit: count, v: ok, note: "≈0.05% normal, up to 15% under backpressure"}
  - {root: otelcol_exporter_sent_spans_total, type: counter, unit: count, v: ok, note: "exporter ∈ {otlphttp,prometheusremotewrite}"}
  - {root: otelcol_exporter_send_failed_spans_total, type: counter, unit: count, v: ok, note: "≈0.03% normal, up to ~20% under span_drops"}
  - {root: otelcol_exporter_queue_size, type: gauge, unit: count, v: ok, note: "2–10 normal, up to 100 under backpressure"}
  - {root: otelcol_processor_batch_batch_send_size_count, type: counter, unit: count, v: ok, note: "processor=batch"}
  - {root: otelcol_processor_batch_metadata_cardinality, type: gauge, unit: count, v: ok}
  # failure-mode extras (emitted under specific failure modes only)
  - {root: prometheus_remote_write_sent_samples_total, type: counter, unit: count, v: ok, note: "under remote_write_reject"}
  - {root: prometheus_remote_write_dropped_samples_total, type: counter, unit: count, v: ok, note: "under remote_write_reject"}
  - {root: prometheus_remote_storage_wal_storage_size_bytes, type: gauge, unit: bytes, v: ok, note: "under wal_pressure; +component='prometheus.remote_write'"}
  - {root: prometheus_tsdb_wal_segment_current, type: gauge, unit: count, v: ok, note: "under wal_pressure"}
```

---

## Content sentinel [slug: fm-content-sentinel]

- **`synthkit_content_dropped_total`** (Counter, accumulates; labels `cluster`, `k8s_cluster_name`,
  `namespace="infra"`, `job="integrations/alloy"`, `pipeline`, `field_class`). Proves the strip is
  running. Always at least a trickle per series per tick. ⚠ `pipeline` and `field_class` values are
  **workload-derived in v1** (e.g. `pipeline="app_otlp"`, generic `field_class` such as `app_output`);
  the predecessor's gen_ai/LLM-specific pipelines and field-classes (`gen_ai_body`, `langsmith_io`,
  `bedrock_body`, etc.) are NOT carried into synthkit.
- **`synthkit_content_leak_test`** (Gauge = exactly **0.0**, one series per pipeline; same base labels
  minus `field_class`). Proves nothing leaks — a non-zero value is a test failure (T11). Single alert:
  `synthkit_content_leak_test > 0`.

```yaml signals
family: fm_content_sentinel
scope: substrate
sink: promrw
labels:
  cluster: <cluster>
  k8s_cluster_name: <cluster>
  namespace: infra
  job: integrations/alloy
  pipeline: <workload-derived, e.g. app_otlp>
  field_class: <workload-derived, e.g. app_output>   # dropped_total only
metrics:
  - {root: synthkit_content_dropped_total, type: counter, unit: count, v: ok, note: "accumulates; trickle per series per tick; proves strip is running"}
  - {root: synthkit_content_leak_test, type: gauge, unit: float, v: ok, note: "ALWAYS exactly 0.0; alert >0 (T11); one series per pipeline; field_class label absent"}
```

> ⚠ **Rename note:** the original names were `<prefix>_content_dropped_total` and
> `<prefix>_content_leak_test`. These are named `synthkit_content_*` in this repo. The original
> gen_ai/LLM-specific `pipeline` and `field_class` values (`gen_ai_body`, `langsmith_io`,
> `bedrock_body`, etc.) are NOT carried into synthkit — v1 uses only workload-derived values.

---

## Opt-in syslog receiver health [slug: fm-syslog-health]

The existing `alloy_health` cluster addon selects `syslog.receiver: loki|otel`.
Omission adds **zero** series and does not alter the existing health set. Enabled
profiles add **6 series per cluster = 2 existing HA scrape identities × 3 counters**.
Counters remain cumulative across ticks, including zero-valued error counters.
`syslog_records_per_min` is an operator's aggregate healthy-intake assumption,
default **0**, split evenly over the two HA targets and business-hours shaped.
No minimum-forcing, fabricated parse errors or invented receiver instrumentation.

Pinned sources:

- Alloy **v1.20.1** [metrics.go](https://github.com/grafana/alloy/blob/v1.20.1/internal/component/loki/source/syslog/internal/syslogtarget/metrics.go#L21-L39)
  defines `loki_source_syslog_entries_total`, `loki_source_syslog_parsing_errors_total`,
  `loki_source_syslog_empty_messages_total`, **without instrument labels**. The five
  existing addon scrape labels below are attached separately, not per-device labels.
- Collector core **v0.161.0**, receiverhelper module **v1.67.0** (Alloy v1.20.1 pin),
  [metadata](https://github.com/open-telemetry/opentelemetry-collector/blob/v0.161.0/receiver/receiverhelper/metadata.yaml#L8-L80)
  defines accepted/refused/failed log-record monotonic instruments. Prometheus
  exposition uses the final `_total` spellings below. [ObsReport](https://github.com/open-telemetry/opentelemetry-collector/blob/v0.161.0/receiver/receiverhelper/obsreport.go#L179-L196)
  uses actual downstream errors: default gate off sends **all errors to refused**,
  failed remains zero. Accepted is successful handoff, not successful parsing.
  The feature-gated request metric and alternate failed classification are not modeled.
- Contrib **v0.161.0** [adapter factory](https://github.com/open-telemetry/opentelemetry-collector-contrib/blob/v0.161.0/pkg/stanza/adapter/factory.go#L53-L58)
  supplies bounded receiver ID and leaves transport empty. synthkit **omits** this
  absent dimension per canon rather than emitting `transport=""`.
  [Adapter receiver](https://github.com/open-telemetry/opentelemetry-collector-contrib/blob/v0.161.0/pkg/stanza/adapter/receiver.go#L64-L76)
  reports the downstream consumer result. There is **no OTel syslog parse counter**;
  default `on_error=send` forwards malformed unparsed records, counted accepted
  if the writer succeeds. See [logs.md](logs.md) [slug: logs-syslog].
- Scrape identity `cluster`, `k8s_cluster_name`, `namespace`, `job`, `instance`
  is the existing addon contract [slug: fm-alloy-health], not new receiver attributes.

```yaml signals
family: fm_syslog_loki
scope: substrate
sink: promrw
labels:
  cluster: <cluster>
  k8s_cluster_name: <cluster>
  namespace: infra
  job: integrations/alloy
  instance: <existing-HA-target>
metrics:
  - {root: loki_source_syslog_entries_total, type: counter, unit: count, v: ok, note: "successful sends; unlabelled instrument"}
  - {root: loki_source_syslog_parsing_errors_total, type: counter, unit: count, v: ok, note: "decoder failures dropped; unlabelled instrument"}
  - {root: loki_source_syslog_empty_messages_total, type: counter, unit: count, v: ok, note: "empty input/MSG; unlabelled instrument"}
```

```yaml signals
family: fm_syslog_otel
scope: substrate
sink: promrw
labels:
  cluster: <cluster>
  k8s_cluster_name: <cluster>
  namespace: infra
  job: integrations/alloy
  instance: <existing-HA-target>
  receiver: <configured-component-ID>
metrics:
  - {root: otelcol_receiver_accepted_log_records_total, type: counter, unit: count, v: ok, note: "successful downstream handoff, including default-send malformed records"}
  - {root: otelcol_receiver_refused_log_records_total, type: counter, unit: count, v: ok, note: "downstream errors; default receiver-helper gate off"}
  - {root: otelcol_receiver_failed_log_records_total, type: counter, unit: count, v: ok, note: "zero with default gate off; NOT parse failures"}
```
