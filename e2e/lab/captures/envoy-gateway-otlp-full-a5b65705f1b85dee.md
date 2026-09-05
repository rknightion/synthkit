# Envoy Gateway full OTLP metrics contract — 2026-09-05

Answers `cantfind.md` **SK-110**, **SK-111**, and **SK-112**. This record supersedes the
contract completeness of `envoy-gateway-otlp-fc9ca6cd2ec0eba6.md` and
`beyla-envoygateway-otlp-588571dc6a53c4e4.md`; those earlier observations remain
immutable evidence for their capture windows.

The adjacent JSON file is the normative machine-readable contract. For every family on
both planes it retains the exact name, instrument, temporality, unit, per-family datapoint
attribute keys and observed value sets, resource envelope, scope name/version, and every
observed histogram's exact explicit bounds.

## Evidence and cleanup

Raw collector log SHA-256: `a5b65705f1b85deecc011272c039499cee933cab824f6fd2310a6fffe6df40a4` (2,426,942 lines).
The raw log remains in the gitignored `codex/scratch/` evidence directory and is not part
of the repository record.

The first bounded attempt kept the sink open for more than ten minutes, but the receiver's
current log segment had rotated before collection. Its retained 46.734-second tail had only
the data plane, so it was insufficient. That new evidence triggered the goal's first allowed
retry. The retry streamed from receiver startup and retained both planes continuously for
713.242 seconds, from
`2026-09-05T22:14:14.970Z` through
`2026-09-05T22:26:08.212Z`.

Both attempts used a disposable OpenTelemetry Collector 0.137.0 debug exporter at detailed
verbosity. The data-plane sink carried host and port only; the controller sink additionally
carried `protocol: grpc`; the existing Prometheus surfaces stayed enabled. After capture, the
temporary configuration was reverted, live API read-back showed both metrics sinks absent,
and the receiver Deployment, Service, and terminating pod were proven gone.

## Contract overview

| plane | metrics | Gauge | Sum | Histogram |
| --- | ---: | ---: | ---: | ---: |
| `control_plane` | 16 | 2 | 9 | 5 |
| `data_plane` | 206 | 75 | 113 | 18 |

## Resource and scope envelopes

### `control_plane`

Resource attributes:

- `service.name` = `unknown_service:envoy-gateway`
- `telemetry.sdk.language` = `go`
- `telemetry.sdk.name` = `opentelemetry`
- `telemetry.sdk.version` = `1.45.0`

Scopes:

- name `envoy-gateway`, version `(empty)`

Resource schema URLs: `https://opentelemetry.io/schemas/1.43.0`

### `data_plane`

Resource attributes:

- `telemetry.sdk.language` = `cpp`
- `telemetry.sdk.name` = `envoy`
- `telemetry.sdk.version` = `<build-id-elided>/1.39.0/Clean/RELEASE/BoringSSL`

Scopes:

- name `(empty)`, version `(empty)`

Resource schema URLs: `(empty)`

## Attribute-value elisions

The JSON keeps low-cardinality protocol values verbatim and replaces values that are
names, namespaces, addresses, certificates, resource names, or IDs with these explicit
markers. The attribute keys remain exact and each marker stays attached to its family:

- `envoy.cluster_name` → `<cluster-name-elided>`
- `envoy.http_conn_manager_prefix` → `<http-connection-manager-name-elided>`
- `envoy.listener_address` → `<listener-address-elided>`
- `envoy.rds_route_config` → `<route-config-name-elided>`
- `envoy.tls_certificate` → `<certificate-name-elided>`
- `envoy.worker_id` → `<worker-id-elided>`
- `envoy.xds_resource_name` → `<xds-resource-name-elided>`
- `name` → `<resource-name-elided>`
- `namespace` → `<namespace-name-elided>`
- `nodeID` → `<node-id-elided>`
- `socket_match_name` → `<socket-match-name-elided>`
- `streamID` → `<stream-id-elided>`

## Exact histogram bounds

The prior records expected 19 histogram families. This longer current-version capture
observed 23: five on the control plane and 18 on the data plane. No captured histogram
had conflicting bounds across datapoints or batches.

| plane | family | unit | temporality | explicit bounds |
| --- | --- | --- | --- | --- |
| `control_plane` | `resource_apply_duration_seconds` | `1` | `Cumulative` | `0.001, 0.01, 0.1, 1.0, 5.0, 10.0` |
| `control_plane` | `resource_delete_duration_seconds` | `1` | `Cumulative` | `0.001, 0.01, 0.1, 1.0, 5.0, 10.0` |
| `control_plane` | `status_update_duration_seconds` | `1` | `Cumulative` | `0.001, 0.01, 0.1, 1.0, 5.0, 10.0` |
| `control_plane` | `watchable_subscribe_duration_seconds` | `1` | `Cumulative` | `0.001, 0.01, 0.1, 1.0, 5.0, 10.0` |
| `control_plane` | `xds_stream_duration_seconds` | `1` | `Cumulative` | `0.1, 10.0, 50.0, 100.0, 1000.0, 10000.0` |
| `data_plane` | `cluster.external.upstream_rq_time` | `(empty)` | `Cumulative` | `0.5, 1.0, 5.0, 10.0, 25.0, 50.0, 100.0, 250.0, 500.0, 1000.0, 2500.0, 5000.0, 10000.0, 30000.0, 60000.0, 300000.0, 600000.0, 1800000.0, 3600000.0` |
| `data_plane` | `cluster.internal.upstream_rq_time` | `(empty)` | `Cumulative` | `0.5, 1.0, 5.0, 10.0, 25.0, 50.0, 100.0, 250.0, 500.0, 1000.0, 2500.0, 5000.0, 10000.0, 30000.0, 60000.0, 300000.0, 600000.0, 1800000.0, 3600000.0` |
| `data_plane` | `cluster.update_duration` | `(empty)` | `Cumulative` | `0.5, 1.0, 5.0, 10.0, 25.0, 50.0, 100.0, 250.0, 500.0, 1000.0, 2500.0, 5000.0, 10000.0, 30000.0, 60000.0, 300000.0, 600000.0, 1800000.0, 3600000.0` |
| `data_plane` | `cluster.upstream_cx_connect_ms` | `(empty)` | `Cumulative` | `0.5, 1.0, 5.0, 10.0, 25.0, 50.0, 100.0, 250.0, 500.0, 1000.0, 2500.0, 5000.0, 10000.0, 30000.0, 60000.0, 300000.0, 600000.0, 1800000.0, 3600000.0` |
| `data_plane` | `cluster.upstream_cx_length_ms` | `(empty)` | `Cumulative` | `0.5, 1.0, 5.0, 10.0, 25.0, 50.0, 100.0, 250.0, 500.0, 1000.0, 2500.0, 5000.0, 10000.0, 30000.0, 60000.0, 300000.0, 600000.0, 1800000.0, 3600000.0` |
| `data_plane` | `cluster.upstream_rq_per_cx` | `(empty)` | `Cumulative` | `0.5, 1.0, 5.0, 10.0, 25.0, 50.0, 100.0, 250.0, 500.0, 1000.0, 2500.0, 5000.0, 10000.0, 30000.0, 60000.0, 300000.0, 600000.0, 1800000.0, 3600000.0` |
| `data_plane` | `cluster.upstream_rq_time` | `(empty)` | `Cumulative` | `0.5, 1.0, 5.0, 10.0, 25.0, 50.0, 100.0, 250.0, 500.0, 1000.0, 2500.0, 5000.0, 10000.0, 30000.0, 60000.0, 300000.0, 600000.0, 1800000.0, 3600000.0` |
| `data_plane` | `cluster_manager.cds.update_duration` | `(empty)` | `Cumulative` | `0.5, 1.0, 5.0, 10.0, 25.0, 50.0, 100.0, 250.0, 500.0, 1000.0, 2500.0, 5000.0, 10000.0, 30000.0, 60000.0, 300000.0, 600000.0, 1800000.0, 3600000.0` |
| `data_plane` | `http.downstream_cx_length_ms` | `(empty)` | `Cumulative` | `0.5, 1.0, 5.0, 10.0, 25.0, 50.0, 100.0, 250.0, 500.0, 1000.0, 2500.0, 5000.0, 10000.0, 30000.0, 60000.0, 300000.0, 600000.0, 1800000.0, 3600000.0` |
| `data_plane` | `http.downstream_rq_time` | `(empty)` | `Cumulative` | `0.5, 1.0, 5.0, 10.0, 25.0, 50.0, 100.0, 250.0, 500.0, 1000.0, 2500.0, 5000.0, 10000.0, 30000.0, 60000.0, 300000.0, 600000.0, 1800000.0, 3600000.0` |
| `data_plane` | `http.rds.update_duration` | `(empty)` | `Cumulative` | `0.5, 1.0, 5.0, 10.0, 25.0, 50.0, 100.0, 250.0, 500.0, 1000.0, 2500.0, 5000.0, 10000.0, 30000.0, 60000.0, 300000.0, 600000.0, 1800000.0, 3600000.0` |
| `data_plane` | `listener.admin.connections_accepted_per_socket_event` | `(empty)` | `Cumulative` | `0.5, 1.0, 5.0, 10.0, 25.0, 50.0, 100.0, 250.0, 500.0, 1000.0, 2500.0, 5000.0, 10000.0, 30000.0, 60000.0, 300000.0, 600000.0, 1800000.0, 3600000.0` |
| `data_plane` | `listener.connections_accepted_per_socket_event` | `(empty)` | `Cumulative` | `0.5, 1.0, 5.0, 10.0, 25.0, 50.0, 100.0, 250.0, 500.0, 1000.0, 2500.0, 5000.0, 10000.0, 30000.0, 60000.0, 300000.0, 600000.0, 1800000.0, 3600000.0` |
| `data_plane` | `listener.downstream_cx_length_ms` | `(empty)` | `Cumulative` | `0.5, 1.0, 5.0, 10.0, 25.0, 50.0, 100.0, 250.0, 500.0, 1000.0, 2500.0, 5000.0, 10000.0, 30000.0, 60000.0, 300000.0, 600000.0, 1800000.0, 3600000.0` |
| `data_plane` | `listener_manager.lds.update_duration` | `(empty)` | `Cumulative` | `0.5, 1.0, 5.0, 10.0, 25.0, 50.0, 100.0, 250.0, 500.0, 1000.0, 2500.0, 5000.0, 10000.0, 30000.0, 60000.0, 300000.0, 600000.0, 1800000.0, 3600000.0` |
| `data_plane` | `sds.update_duration` | `(empty)` | `Cumulative` | `0.5, 1.0, 5.0, 10.0, 25.0, 50.0, 100.0, 250.0, 500.0, 1000.0, 2500.0, 5000.0, 10000.0, 30000.0, 60000.0, 300000.0, 600000.0, 1800000.0, 3600000.0` |
| `data_plane` | `server.initialization_time_ms` | `(empty)` | `Cumulative` | `0.5, 1.0, 5.0, 10.0, 25.0, 50.0, 100.0, 250.0, 500.0, 1000.0, 2500.0, 5000.0, 10000.0, 30000.0, 60000.0, 300000.0, 600000.0, 1800000.0, 3600000.0` |
| `data_plane` | `tls_inspector.bytes_processed` | `(empty)` | `Cumulative` | `0.5, 1.0, 5.0, 10.0, 25.0, 50.0, 100.0, 250.0, 500.0, 1000.0, 2500.0, 5000.0, 10000.0, 30000.0, 60000.0, 300000.0, 600000.0, 1800000.0, 3600000.0` |

## Full family list

### `control_plane`

| name | instrument | unit | temporality |
| --- | --- | --- | --- |
| `resource_apply_duration_seconds` | `Histogram` | `1` | `Cumulative` |
| `resource_apply_total` | `Sum` | `1` | `Cumulative` |
| `resource_delete_duration_seconds` | `Histogram` | `1` | `Cumulative` |
| `resource_delete_total` | `Sum` | `1` | `Cumulative` |
| `status_update_duration_seconds` | `Histogram` | `1` | `Cumulative` |
| `status_update_total` | `Sum` | `1` | `Cumulative` |
| `topology_injector_webhook_events_total` | `Sum` | `1` | `Cumulative` |
| `wasm_cache_entries` | `Gauge` | `1` | `(empty)` |
| `watchable_depth` | `Gauge` | `1` | `(empty)` |
| `watchable_event_total` | `Sum` | `1` | `Cumulative` |
| `watchable_publish_total` | `Sum` | `1` | `Cumulative` |
| `watchable_subscribe_duration_seconds` | `Histogram` | `1` | `Cumulative` |
| `watchable_subscribe_total` | `Sum` | `1` | `Cumulative` |
| `xds_snapshot_create_total` | `Sum` | `1` | `Cumulative` |
| `xds_snapshot_update_total` | `Sum` | `1` | `Cumulative` |
| `xds_stream_duration_seconds` | `Histogram` | `1` | `Cumulative` |

### `data_plane`

| name | instrument | unit | temporality |
| --- | --- | --- | --- |
| `cluster.circuit_breakers.cx_open` | `Gauge` | `(empty)` | `(empty)` |
| `cluster.circuit_breakers.cx_pool_open` | `Gauge` | `(empty)` | `(empty)` |
| `cluster.circuit_breakers.rq_open` | `Gauge` | `(empty)` | `(empty)` |
| `cluster.circuit_breakers.rq_pending_open` | `Gauge` | `(empty)` | `(empty)` |
| `cluster.client_ssl_socket_factory.ssl_context_update_by_sds` | `Sum` | `(empty)` | `Cumulative` |
| `cluster.external.upstream_rq` | `Sum` | `(empty)` | `Cumulative` |
| `cluster.external.upstream_rq_completed` | `Sum` | `(empty)` | `Cumulative` |
| `cluster.external.upstream_rq_time` | `Histogram` | `(empty)` | `Cumulative` |
| `cluster.external.upstream_rq_xx` | `Sum` | `(empty)` | `Cumulative` |
| `cluster.http2.outbound_control_frames_active` | `Gauge` | `(empty)` | `(empty)` |
| `cluster.http2.outbound_frames_active` | `Gauge` | `(empty)` | `(empty)` |
| `cluster.http2.pending_send_bytes` | `Gauge` | `(empty)` | `(empty)` |
| `cluster.http2.rx_reset` | `Sum` | `(empty)` | `Cumulative` |
| `cluster.http2.streams_active` | `Gauge` | `(empty)` | `(empty)` |
| `cluster.http2.tx_reset` | `Sum` | `(empty)` | `Cumulative` |
| `cluster.internal.upstream_rq` | `Sum` | `(empty)` | `Cumulative` |
| `cluster.internal.upstream_rq_completed` | `Sum` | `(empty)` | `Cumulative` |
| `cluster.internal.upstream_rq_time` | `Histogram` | `(empty)` | `Cumulative` |
| `cluster.internal.upstream_rq_xx` | `Sum` | `(empty)` | `Cumulative` |
| `cluster.lb_recalculate_zone_structures` | `Sum` | `(empty)` | `Cumulative` |
| `cluster.max_host_weight` | `Gauge` | `(empty)` | `(empty)` |
| `cluster.membership_change` | `Sum` | `(empty)` | `Cumulative` |
| `cluster.membership_degraded` | `Gauge` | `(empty)` | `(empty)` |
| `cluster.membership_excluded` | `Gauge` | `(empty)` | `(empty)` |
| `cluster.membership_healthy` | `Gauge` | `(empty)` | `(empty)` |
| `cluster.membership_total` | `Gauge` | `(empty)` | `(empty)` |
| `cluster.ssl.certificate.expiration_unix_time_seconds` | `Gauge` | `(empty)` | `(empty)` |
| `cluster.ssl.ciphers` | `Sum` | `(empty)` | `Cumulative` |
| `cluster.ssl.curves` | `Sum` | `(empty)` | `Cumulative` |
| `cluster.ssl.handshake` | `Sum` | `(empty)` | `Cumulative` |
| `cluster.ssl.sigalgs` | `Sum` | `(empty)` | `Cumulative` |
| `cluster.ssl.versions` | `Sum` | `(empty)` | `Cumulative` |
| `cluster.total_match_count` | `Sum` | `(empty)` | `Cumulative` |
| `cluster.update_attempt` | `Sum` | `(empty)` | `Cumulative` |
| `cluster.update_duration` | `Histogram` | `(empty)` | `Cumulative` |
| `cluster.update_failure` | `Sum` | `(empty)` | `Cumulative` |
| `cluster.update_no_rebuild` | `Sum` | `(empty)` | `Cumulative` |
| `cluster.update_success` | `Sum` | `(empty)` | `Cumulative` |
| `cluster.update_time` | `Gauge` | `(empty)` | `(empty)` |
| `cluster.upstream_cx_active` | `Gauge` | `(empty)` | `(empty)` |
| `cluster.upstream_cx_connect_ms` | `Histogram` | `(empty)` | `Cumulative` |
| `cluster.upstream_cx_destroy` | `Sum` | `(empty)` | `Cumulative` |
| `cluster.upstream_cx_destroy_remote` | `Sum` | `(empty)` | `Cumulative` |
| `cluster.upstream_cx_destroy_remote_with_active_rq` | `Sum` | `(empty)` | `Cumulative` |
| `cluster.upstream_cx_destroy_with_active_rq` | `Sum` | `(empty)` | `Cumulative` |
| `cluster.upstream_cx_http1_total` | `Sum` | `(empty)` | `Cumulative` |
| `cluster.upstream_cx_http2_total` | `Sum` | `(empty)` | `Cumulative` |
| `cluster.upstream_cx_length_ms` | `Histogram` | `(empty)` | `Cumulative` |
| `cluster.upstream_cx_rx_bytes_buffered` | `Gauge` | `(empty)` | `(empty)` |
| `cluster.upstream_cx_rx_bytes_total` | `Sum` | `(empty)` | `Cumulative` |
| `cluster.upstream_cx_total` | `Sum` | `(empty)` | `Cumulative` |
| `cluster.upstream_cx_tx_bytes_total` | `Sum` | `(empty)` | `Cumulative` |
| `cluster.upstream_rq` | `Sum` | `(empty)` | `Cumulative` |
| `cluster.upstream_rq_active` | `Gauge` | `(empty)` | `(empty)` |
| `cluster.upstream_rq_completed` | `Sum` | `(empty)` | `Cumulative` |
| `cluster.upstream_rq_pending_active` | `Gauge` | `(empty)` | `(empty)` |
| `cluster.upstream_rq_pending_failure_eject` | `Sum` | `(empty)` | `Cumulative` |
| `cluster.upstream_rq_pending_total` | `Sum` | `(empty)` | `Cumulative` |
| `cluster.upstream_rq_per_cx` | `Histogram` | `(empty)` | `Cumulative` |
| `cluster.upstream_rq_time` | `Histogram` | `(empty)` | `Cumulative` |
| `cluster.upstream_rq_total` | `Sum` | `(empty)` | `Cumulative` |
| `cluster.upstream_rq_tx_reset` | `Sum` | `(empty)` | `Cumulative` |
| `cluster.upstream_rq_xx` | `Sum` | `(empty)` | `Cumulative` |
| `cluster.version` | `Gauge` | `(empty)` | `(empty)` |
| `cluster.warming_state` | `Gauge` | `(empty)` | `(empty)` |
| `cluster_manager.active_clusters` | `Gauge` | `(empty)` | `(empty)` |
| `cluster_manager.cds.config_reload` | `Sum` | `(empty)` | `Cumulative` |
| `cluster_manager.cds.config_reload_time_ms` | `Gauge` | `(empty)` | `(empty)` |
| `cluster_manager.cds.update_attempt` | `Sum` | `(empty)` | `Cumulative` |
| `cluster_manager.cds.update_duration` | `Histogram` | `(empty)` | `Cumulative` |
| `cluster_manager.cds.update_failure` | `Sum` | `(empty)` | `Cumulative` |
| `cluster_manager.cds.update_success` | `Sum` | `(empty)` | `Cumulative` |
| `cluster_manager.cds.update_time` | `Gauge` | `(empty)` | `(empty)` |
| `cluster_manager.cds.version` | `Gauge` | `(empty)` | `(empty)` |
| `cluster_manager.cluster_added` | `Sum` | `(empty)` | `Cumulative` |
| `cluster_manager.cluster_updated` | `Sum` | `(empty)` | `Cumulative` |
| `cluster_manager.cluster_updated_via_merge` | `Sum` | `(empty)` | `Cumulative` |
| `cluster_manager.update_merge_cancelled` | `Sum` | `(empty)` | `Cumulative` |
| `cluster_manager.update_out_of_merge_window` | `Sum` | `(empty)` | `Cumulative` |
| `cluster_manager.warming_clusters` | `Gauge` | `(empty)` | `(empty)` |
| `control_plane.connected_state` | `Gauge` | `(empty)` | `(empty)` |
| `dns.cares.not_found` | `Sum` | `(empty)` | `Cumulative` |
| `dns.cares.pending_resolutions` | `Gauge` | `(empty)` | `(empty)` |
| `dns.cares.resolve_total` | `Sum` | `(empty)` | `Cumulative` |
| `filesystem.flushed_by_timer` | `Sum` | `(empty)` | `Cumulative` |
| `filesystem.write_buffered` | `Sum` | `(empty)` | `Cumulative` |
| `filesystem.write_completed` | `Sum` | `(empty)` | `Cumulative` |
| `filesystem.write_total_buffered` | `Gauge` | `(empty)` | `(empty)` |
| `http.downstream_cx_active` | `Gauge` | `(empty)` | `(empty)` |
| `http.downstream_cx_destroy` | `Sum` | `(empty)` | `Cumulative` |
| `http.downstream_cx_destroy_local` | `Sum` | `(empty)` | `Cumulative` |
| `http.downstream_cx_destroy_remote` | `Sum` | `(empty)` | `Cumulative` |
| `http.downstream_cx_http1_active` | `Gauge` | `(empty)` | `(empty)` |
| `http.downstream_cx_http1_total` | `Sum` | `(empty)` | `Cumulative` |
| `http.downstream_cx_http2_active` | `Gauge` | `(empty)` | `(empty)` |
| `http.downstream_cx_http2_total` | `Sum` | `(empty)` | `Cumulative` |
| `http.downstream_cx_length_ms` | `Histogram` | `(empty)` | `Cumulative` |
| `http.downstream_cx_rx_bytes_buffered` | `Gauge` | `(empty)` | `(empty)` |
| `http.downstream_cx_rx_bytes_total` | `Sum` | `(empty)` | `Cumulative` |
| `http.downstream_cx_ssl_active` | `Gauge` | `(empty)` | `(empty)` |
| `http.downstream_cx_ssl_total` | `Sum` | `(empty)` | `Cumulative` |
| `http.downstream_cx_total` | `Sum` | `(empty)` | `Cumulative` |
| `http.downstream_cx_tx_bytes_total` | `Sum` | `(empty)` | `Cumulative` |
| `http.downstream_rq_active` | `Gauge` | `(empty)` | `(empty)` |
| `http.downstream_rq_completed` | `Sum` | `(empty)` | `Cumulative` |
| `http.downstream_rq_http1_total` | `Sum` | `(empty)` | `Cumulative` |
| `http.downstream_rq_http2_total` | `Sum` | `(empty)` | `Cumulative` |
| `http.downstream_rq_time` | `Histogram` | `(empty)` | `Cumulative` |
| `http.downstream_rq_total` | `Sum` | `(empty)` | `Cumulative` |
| `http.downstream_rq_xx` | `Sum` | `(empty)` | `Cumulative` |
| `http.health_check.ok` | `Sum` | `(empty)` | `Cumulative` |
| `http.health_check.request_total` | `Sum` | `(empty)` | `Cumulative` |
| `http.rds.config_reload` | `Sum` | `(empty)` | `Cumulative` |
| `http.rds.config_reload_time_ms` | `Gauge` | `(empty)` | `(empty)` |
| `http.rds.update_attempt` | `Sum` | `(empty)` | `Cumulative` |
| `http.rds.update_duration` | `Histogram` | `(empty)` | `Cumulative` |
| `http.rds.update_failure` | `Sum` | `(empty)` | `Cumulative` |
| `http.rds.update_success` | `Sum` | `(empty)` | `Cumulative` |
| `http.rds.update_time` | `Gauge` | `(empty)` | `(empty)` |
| `http.rds.version` | `Gauge` | `(empty)` | `(empty)` |
| `http.rq_reset_after_downstream_response_started` | `Sum` | `(empty)` | `Cumulative` |
| `http.rq_total` | `Sum` | `(empty)` | `Cumulative` |
| `http.tracing.health_check` | `Sum` | `(empty)` | `Cumulative` |
| `http.tracing.random_sampling` | `Sum` | `(empty)` | `Cumulative` |
| `http2.outbound_control_frames_active` | `Gauge` | `(empty)` | `(empty)` |
| `http2.outbound_frames_active` | `Gauge` | `(empty)` | `(empty)` |
| `http2.pending_send_bytes` | `Gauge` | `(empty)` | `(empty)` |
| `http2.streams_active` | `Gauge` | `(empty)` | `(empty)` |
| `listener.admin.connections_accepted_per_socket_event` | `Histogram` | `(empty)` | `Cumulative` |
| `listener.admin.downstream_cx_active` | `Gauge` | `(empty)` | `(empty)` |
| `listener.admin.downstream_cx_total` | `Sum` | `(empty)` | `Cumulative` |
| `listener.admin.downstream_pre_cx_active` | `Gauge` | `(empty)` | `(empty)` |
| `listener.admin.http.downstream_rq_completed` | `Sum` | `(empty)` | `Cumulative` |
| `listener.admin.http.downstream_rq_xx` | `Sum` | `(empty)` | `Cumulative` |
| `listener.admin.main_thread.downstream_cx_active` | `Gauge` | `(empty)` | `(empty)` |
| `listener.admin.main_thread.downstream_cx_total` | `Sum` | `(empty)` | `Cumulative` |
| `listener.connections_accepted_per_socket_event` | `Histogram` | `(empty)` | `Cumulative` |
| `listener.downstream_cx_active` | `Gauge` | `(empty)` | `(empty)` |
| `listener.downstream_cx_destroy` | `Sum` | `(empty)` | `Cumulative` |
| `listener.downstream_cx_length_ms` | `Histogram` | `(empty)` | `Cumulative` |
| `listener.downstream_cx_total` | `Sum` | `(empty)` | `Cumulative` |
| `listener.downstream_pre_cx_active` | `Gauge` | `(empty)` | `(empty)` |
| `listener.http.downstream_rq_completed` | `Sum` | `(empty)` | `Cumulative` |
| `listener.http.downstream_rq_xx` | `Sum` | `(empty)` | `Cumulative` |
| `listener.server_ssl_socket_factory.ssl_context_update_by_sds` | `Sum` | `(empty)` | `Cumulative` |
| `listener.ssl.certificate.expiration_unix_time_seconds` | `Gauge` | `(empty)` | `(empty)` |
| `listener.ssl.ciphers` | `Sum` | `(empty)` | `Cumulative` |
| `listener.ssl.curves` | `Sum` | `(empty)` | `Cumulative` |
| `listener.ssl.handshake` | `Sum` | `(empty)` | `Cumulative` |
| `listener.ssl.no_certificate` | `Sum` | `(empty)` | `Cumulative` |
| `listener.ssl.versions` | `Sum` | `(empty)` | `Cumulative` |
| `listener.worker_downstream_cx_active` | `Gauge` | `(empty)` | `(empty)` |
| `listener.worker_downstream_cx_total` | `Sum` | `(empty)` | `Cumulative` |
| `listener_manager.lds.update_attempt` | `Sum` | `(empty)` | `Cumulative` |
| `listener_manager.lds.update_duration` | `Histogram` | `(empty)` | `Cumulative` |
| `listener_manager.lds.update_failure` | `Sum` | `(empty)` | `Cumulative` |
| `listener_manager.lds.update_success` | `Sum` | `(empty)` | `Cumulative` |
| `listener_manager.lds.update_time` | `Gauge` | `(empty)` | `(empty)` |
| `listener_manager.lds.version` | `Gauge` | `(empty)` | `(empty)` |
| `listener_manager.listener_added` | `Sum` | `(empty)` | `Cumulative` |
| `listener_manager.listener_create_success` | `Sum` | `(empty)` | `Cumulative` |
| `listener_manager.total_listeners_active` | `Gauge` | `(empty)` | `(empty)` |
| `listener_manager.total_listeners_warming` | `Gauge` | `(empty)` | `(empty)` |
| `listener_manager.workers_started` | `Gauge` | `(empty)` | `(empty)` |
| `main_thread.watchdog_miss` | `Sum` | `(empty)` | `Cumulative` |
| `runtime.load_success` | `Sum` | `(empty)` | `Cumulative` |
| `runtime.num_keys` | `Gauge` | `(empty)` | `(empty)` |
| `runtime.num_layers` | `Gauge` | `(empty)` | `(empty)` |
| `runtime.override_dir_not_exists` | `Sum` | `(empty)` | `Cumulative` |
| `sds.update_attempt` | `Sum` | `(empty)` | `Cumulative` |
| `sds.update_duration` | `Histogram` | `(empty)` | `Cumulative` |
| `sds.update_failure` | `Sum` | `(empty)` | `Cumulative` |
| `sds.update_success` | `Sum` | `(empty)` | `Cumulative` |
| `sds.update_time` | `Gauge` | `(empty)` | `(empty)` |
| `sds.version` | `Gauge` | `(empty)` | `(empty)` |
| `server.compilation_settings.fips_mode` | `Gauge` | `(empty)` | `(empty)` |
| `server.concurrency` | `Gauge` | `(empty)` | `(empty)` |
| `server.days_until_first_cert_expiring` | `Gauge` | `(empty)` | `(empty)` |
| `server.dynamic_unknown_fields` | `Sum` | `(empty)` | `Cumulative` |
| `server.hot_restart_epoch` | `Gauge` | `(empty)` | `(empty)` |
| `server.hot_restart_generation` | `Gauge` | `(empty)` | `(empty)` |
| `server.initialization_time_ms` | `Histogram` | `(empty)` | `Cumulative` |
| `server.live` | `Gauge` | `(empty)` | `(empty)` |
| `server.main_thread.watchdog_miss` | `Sum` | `(empty)` | `Cumulative` |
| `server.memory_allocated` | `Gauge` | `(empty)` | `(empty)` |
| `server.memory_heap_size` | `Gauge` | `(empty)` | `(empty)` |
| `server.memory_physical_size` | `Gauge` | `(empty)` | `(empty)` |
| `server.parent_connections` | `Gauge` | `(empty)` | `(empty)` |
| `server.state` | `Gauge` | `(empty)` | `(empty)` |
| `server.static_unknown_fields` | `Sum` | `(empty)` | `Cumulative` |
| `server.stats_recent_lookups` | `Gauge` | `(empty)` | `(empty)` |
| `server.total_connections` | `Gauge` | `(empty)` | `(empty)` |
| `server.uptime` | `Gauge` | `(empty)` | `(empty)` |
| `server.version` | `Gauge` | `(empty)` | `(empty)` |
| `server.wip_protos` | `Sum` | `(empty)` | `Cumulative` |
| `server.worker_watchdog_miss` | `Sum` | `(empty)` | `Cumulative` |
| `thread_local_cluster_manager.main_thread.clusters_inflated` | `Gauge` | `(empty)` | `(empty)` |
| `thread_local_cluster_manager.worker_clusters_inflated` | `Gauge` | `(empty)` | `(empty)` |
| `tls_inspector.alpn_found` | `Sum` | `(empty)` | `Cumulative` |
| `tls_inspector.alpn_not_found` | `Sum` | `(empty)` | `Cumulative` |
| `tls_inspector.bytes_processed` | `Histogram` | `(empty)` | `Cumulative` |
| `tls_inspector.sni_found` | `Sum` | `(empty)` | `Cumulative` |
| `tls_inspector.tls_found` | `Sum` | `(empty)` | `Cumulative` |
| `tracing.opentelemetry.spans_sent` | `Sum` | `(empty)` | `Cumulative` |
| `tracing.opentelemetry.timer_flushed` | `Sum` | `(empty)` | `Cumulative` |
| `workers.watchdog_miss` | `Sum` | `(empty)` | `Cumulative` |
