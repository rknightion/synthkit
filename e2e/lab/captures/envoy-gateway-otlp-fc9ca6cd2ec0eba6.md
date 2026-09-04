# Envoy Gateway OTLP metrics sink capture — 2026-09-04

Answers `cantfind.md` **SK-87** for the **data plane**. The question was whether the Envoy Gateway
OpenTelemetry stats sink ships the `envoy_*` / `xds_*` / `watchable_*` / `controller_runtime_*` names
verbatim or an OTel-shaped name set. It ships an **OTel-shaped set**, and the two surfaces share no
metric name at all.

Raw capture SHA-256: `fc9ca6cd2ec0eba6df29e6e9add36b5019981a3d5453892d132c113721f83f91`.

## How it was taken

An OpenTelemetry stats sink was added to the `EnvoyProxy` CR **alongside** the existing Prometheus
sink, pointed at a disposable `otel/opentelemetry-collector-contrib:0.137.0` debug exporter running
in-cluster at detailed verbosity. The window was ~4 minutes on the live EKS lab data plane, then the
sink was reverted and the receiver deleted. 372 metric log records, 82,224 log lines.

This is the **live EKS data plane**, not a k3d permutation: `telemetry.metrics.sinks` is an
`EnvoyProxy` field, so it covers the Envoy proxy Deployment only.

## What it authorises

162 distinct metric names, all in **dotted Envoy stat spelling**:

| Instrument | Count |
|---|---:|
| Sum | 78 |
| Gauge | 69 |
| Histogram | 15 |

First segment: `cluster` 54, `http` 27, `listener` 20, `server` 19, `cluster_manager` 12,
`listener_manager` 10, `sds` 5, `runtime` 4, `filesystem` 4, `dns` 3,
`thread_local_cluster_manager` 2, `control_plane` 1, `tracing` 1.

## The four findings that matter

**1. No `envoy_` prefix, and no overlap with the scrape surface.** Zero names match `envoy_`,
`xds_`, `watchable_` or `controller_runtime_`. The sink emits `cluster.circuit_breakers.cx_open`
where the Prometheus scrape (`signals/k8s-addons.md [slug: k8s-envoy-gateway]`) carries
`envoy_cluster_circuit_breakers_cx_open`. Deriving one spelling from the other is exactly the
un-mangling SK-87 rules out, and this capture is why: the OTLP form is not a transform of the
Prometheus form, it is Envoy's native stat tree.

**2. Unit is EMPTY on all 162.** Not `1`, not `s`, not `ms` — absent. Anything appending a unit
suffix from the OTel unit gets nothing to append, so a synthetic emitter must not invent one.

**3. InstrumentationScope is EMPTY.** No scope name and no scope version on any record.

**4. Resource attributes carry NO identity.** Only `telemetry.sdk.name`, `telemetry.sdk.language`
and `telemetry.sdk.version`. There is no `service.name`, no `k8s.*`, no cluster or namespace
attribute — unlike the tracing provider on the same CR, which sets `resourceAttributes` explicitly.
Identity must come from elsewhere on this path.

## Datapoint attributes

Dotted `envoy.*` keys, plus two bare ones. Observed counts across the window:

```text
envoy.cluster_name               3406
envoy.http_conn_manager_prefix    626
envoy.listener_address            214
envoy.xds_resource_name           182
socket_match_name                 182
priority                          144
envoy.response_code_class         112
envoy.rds_route_config             98
envoy.worker_id                    74
envoy.response_code                72
envoy.tls_certificate              42
```

`socket_match_name` and `priority` are bare, not `envoy.`-prefixed. That asymmetry is in the data,
not a transcription slip.

## What this does NOT cover

The **EnvoyGateway control plane**. `watchable_*` and `controller_runtime_*` belong to the
controller, which has its own telemetry configuration that this capture did not touch. SK-87 stays
open for that half.

## Full name and instrument list

```text
cluster.circuit_breakers.cx_open	Gauge
cluster.circuit_breakers.cx_pool_open	Gauge
cluster.circuit_breakers.rq_open	Gauge
cluster.circuit_breakers.rq_pending_open	Gauge
cluster.client_ssl_socket_factory.ssl_context_update_by_sds	Sum
cluster.external.upstream_rq	Sum
cluster.external.upstream_rq_completed	Sum
cluster.external.upstream_rq_time	Histogram
cluster.external.upstream_rq_xx	Sum
cluster.http2.outbound_control_frames_active	Gauge
cluster.http2.outbound_frames_active	Gauge
cluster.http2.pending_send_bytes	Gauge
cluster.http2.streams_active	Gauge
cluster.internal.upstream_rq	Sum
cluster.internal.upstream_rq_completed	Sum
cluster.internal.upstream_rq_time	Histogram
cluster.internal.upstream_rq_xx	Sum
cluster.lb_recalculate_zone_structures	Sum
cluster.max_host_weight	Gauge
cluster.membership_change	Sum
cluster.membership_degraded	Gauge
cluster.membership_excluded	Gauge
cluster.membership_healthy	Gauge
cluster.membership_total	Gauge
cluster.ssl.certificate.expiration_unix_time_seconds	Gauge
cluster.ssl.ciphers	Sum
cluster.ssl.curves	Sum
cluster.ssl.handshake	Sum
cluster.ssl.sigalgs	Sum
cluster.ssl.versions	Sum
cluster.total_match_count	Sum
cluster.update_attempt	Sum
cluster.update_duration	Histogram
cluster.update_no_rebuild	Sum
cluster.update_success	Sum
cluster.update_time	Gauge
cluster.upstream_cx_active	Gauge
cluster.upstream_cx_connect_ms	Histogram
cluster.upstream_cx_http1_total	Sum
cluster.upstream_cx_http2_total	Sum
cluster.upstream_cx_rx_bytes_buffered	Gauge
cluster.upstream_cx_rx_bytes_total	Sum
cluster.upstream_cx_total	Sum
cluster.upstream_cx_tx_bytes_total	Sum
cluster.upstream_rq	Sum
cluster.upstream_rq_active	Gauge
cluster.upstream_rq_completed	Sum
cluster.upstream_rq_pending_active	Gauge
cluster.upstream_rq_pending_total	Sum
cluster.upstream_rq_time	Histogram
cluster.upstream_rq_total	Sum
cluster.upstream_rq_xx	Sum
cluster.version	Gauge
cluster.warming_state	Gauge
cluster_manager.active_clusters	Gauge
cluster_manager.cds.config_reload	Sum
cluster_manager.cds.config_reload_time_ms	Gauge
cluster_manager.cds.update_attempt	Sum
cluster_manager.cds.update_duration	Histogram
cluster_manager.cds.update_success	Sum
cluster_manager.cds.update_time	Gauge
cluster_manager.cds.version	Gauge
cluster_manager.cluster_added	Sum
cluster_manager.cluster_updated	Sum
cluster_manager.update_out_of_merge_window	Sum
cluster_manager.warming_clusters	Gauge
control_plane.connected_state	Gauge
dns.cares.not_found	Sum
dns.cares.pending_resolutions	Gauge
dns.cares.resolve_total	Sum
filesystem.flushed_by_timer	Sum
filesystem.write_buffered	Sum
filesystem.write_completed	Sum
filesystem.write_total_buffered	Gauge
http.downstream_cx_active	Gauge
http.downstream_cx_destroy	Sum
http.downstream_cx_destroy_remote	Sum
http.downstream_cx_http1_active	Gauge
http.downstream_cx_http1_total	Sum
http.downstream_cx_length_ms	Histogram
http.downstream_cx_rx_bytes_buffered	Gauge
http.downstream_cx_rx_bytes_total	Sum
http.downstream_cx_total	Sum
http.downstream_cx_tx_bytes_total	Sum
http.downstream_rq_active	Gauge
http.downstream_rq_completed	Sum
http.downstream_rq_http1_total	Sum
http.downstream_rq_time	Histogram
http.downstream_rq_total	Sum
http.downstream_rq_xx	Sum
http.health_check.ok	Sum
http.health_check.request_total	Sum
http.rds.config_reload	Sum
http.rds.config_reload_time_ms	Gauge
http.rds.update_attempt	Sum
http.rds.update_duration	Histogram
http.rds.update_success	Sum
http.rds.update_time	Gauge
http.rds.version	Gauge
http.rq_total	Sum
http.tracing.health_check	Sum
listener.admin.connections_accepted_per_socket_event	Histogram
listener.admin.downstream_cx_active	Gauge
listener.admin.downstream_cx_total	Sum
listener.admin.downstream_pre_cx_active	Gauge
listener.admin.http.downstream_rq_completed	Sum
listener.admin.http.downstream_rq_xx	Sum
listener.admin.main_thread.downstream_cx_active	Gauge
listener.admin.main_thread.downstream_cx_total	Sum
listener.connections_accepted_per_socket_event	Histogram
listener.downstream_cx_active	Gauge
listener.downstream_cx_destroy	Sum
listener.downstream_cx_length_ms	Histogram
listener.downstream_cx_total	Sum
listener.downstream_pre_cx_active	Gauge
listener.http.downstream_rq_completed	Sum
listener.http.downstream_rq_xx	Sum
listener.server_ssl_socket_factory.ssl_context_update_by_sds	Sum
listener.ssl.certificate.expiration_unix_time_seconds	Gauge
listener.worker_downstream_cx_active	Gauge
listener.worker_downstream_cx_total	Sum
listener_manager.lds.update_attempt	Sum
listener_manager.lds.update_duration	Histogram
listener_manager.lds.update_success	Sum
listener_manager.lds.update_time	Gauge
listener_manager.lds.version	Gauge
listener_manager.listener_added	Sum
listener_manager.listener_create_success	Sum
listener_manager.total_listeners_active	Gauge
listener_manager.total_listeners_warming	Gauge
listener_manager.workers_started	Gauge
runtime.load_success	Sum
runtime.num_keys	Gauge
runtime.num_layers	Gauge
runtime.override_dir_not_exists	Sum
sds.update_attempt	Sum
sds.update_duration	Histogram
sds.update_success	Sum
sds.update_time	Gauge
sds.version	Gauge
server.compilation_settings.fips_mode	Gauge
server.concurrency	Gauge
server.days_until_first_cert_expiring	Gauge
server.dynamic_unknown_fields	Sum
server.hot_restart_epoch	Gauge
server.hot_restart_generation	Gauge
server.initialization_time_ms	Histogram
server.live	Gauge
server.memory_allocated	Gauge
server.memory_heap_size	Gauge
server.memory_physical_size	Gauge
server.parent_connections	Gauge
server.state	Gauge
server.static_unknown_fields	Sum
server.stats_recent_lookups	Gauge
server.total_connections	Gauge
server.uptime	Gauge
server.version	Gauge
server.wip_protos	Sum
thread_local_cluster_manager.main_thread.clusters_inflated	Gauge
thread_local_cluster_manager.worker_clusters_inflated	Gauge
tracing.opentelemetry.timer_flushed	Sum```
