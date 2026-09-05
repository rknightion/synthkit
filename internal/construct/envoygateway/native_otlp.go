// SPDX-License-Identifier: AGPL-3.0-only

package envoygateway

// native_otlp.go models the two Envoy OpenTelemetry metric sinks. The data-plane
// descriptor table is transcribed from the captured OTLP wire inventory; a focused
// test reads that same record and compares the complete name/kind set, so a typo or
// accidental Prometheus un-mangling cannot enter this lane unnoticed.

import (
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/sink/otlp"
)

type nativeMetricSpec struct {
	Name string
	Kind otlp.MetricKind
}

// nativeDataPlaneMetrics is the exact EnvoyProxy OpenTelemetry stats-sink
// inventory captured on 2026-09-04. Do not derive these names from the scrape
// spelling: the dotted names are Envoy's native stat tree.
var nativeDataPlaneMetrics = []nativeMetricSpec{
	{Name: "cluster.circuit_breakers.cx_open", Kind: otlp.MetricGauge},
	{Name: "cluster.circuit_breakers.cx_pool_open", Kind: otlp.MetricGauge},
	{Name: "cluster.circuit_breakers.rq_open", Kind: otlp.MetricGauge},
	{Name: "cluster.circuit_breakers.rq_pending_open", Kind: otlp.MetricGauge},
	{Name: "cluster.client_ssl_socket_factory.ssl_context_update_by_sds", Kind: otlp.MetricSum},
	{Name: "cluster.external.upstream_rq", Kind: otlp.MetricSum},
	{Name: "cluster.external.upstream_rq_completed", Kind: otlp.MetricSum},
	{Name: "cluster.external.upstream_rq_time", Kind: otlp.MetricHistogram},
	{Name: "cluster.external.upstream_rq_xx", Kind: otlp.MetricSum},
	{Name: "cluster.http2.outbound_control_frames_active", Kind: otlp.MetricGauge},
	{Name: "cluster.http2.outbound_frames_active", Kind: otlp.MetricGauge},
	{Name: "cluster.http2.pending_send_bytes", Kind: otlp.MetricGauge},
	{Name: "cluster.http2.streams_active", Kind: otlp.MetricGauge},
	{Name: "cluster.internal.upstream_rq", Kind: otlp.MetricSum},
	{Name: "cluster.internal.upstream_rq_completed", Kind: otlp.MetricSum},
	{Name: "cluster.internal.upstream_rq_time", Kind: otlp.MetricHistogram},
	{Name: "cluster.internal.upstream_rq_xx", Kind: otlp.MetricSum},
	{Name: "cluster.lb_recalculate_zone_structures", Kind: otlp.MetricSum},
	{Name: "cluster.max_host_weight", Kind: otlp.MetricGauge},
	{Name: "cluster.membership_change", Kind: otlp.MetricSum},
	{Name: "cluster.membership_degraded", Kind: otlp.MetricGauge},
	{Name: "cluster.membership_excluded", Kind: otlp.MetricGauge},
	{Name: "cluster.membership_healthy", Kind: otlp.MetricGauge},
	{Name: "cluster.membership_total", Kind: otlp.MetricGauge},
	{Name: "cluster.ssl.certificate.expiration_unix_time_seconds", Kind: otlp.MetricGauge},
	{Name: "cluster.ssl.ciphers", Kind: otlp.MetricSum},
	{Name: "cluster.ssl.curves", Kind: otlp.MetricSum},
	{Name: "cluster.ssl.handshake", Kind: otlp.MetricSum},
	{Name: "cluster.ssl.sigalgs", Kind: otlp.MetricSum},
	{Name: "cluster.ssl.versions", Kind: otlp.MetricSum},
	{Name: "cluster.total_match_count", Kind: otlp.MetricSum},
	{Name: "cluster.update_attempt", Kind: otlp.MetricSum},
	{Name: "cluster.update_duration", Kind: otlp.MetricHistogram},
	{Name: "cluster.update_no_rebuild", Kind: otlp.MetricSum},
	{Name: "cluster.update_success", Kind: otlp.MetricSum},
	{Name: "cluster.update_time", Kind: otlp.MetricGauge},
	{Name: "cluster.upstream_cx_active", Kind: otlp.MetricGauge},
	{Name: "cluster.upstream_cx_connect_ms", Kind: otlp.MetricHistogram},
	{Name: "cluster.upstream_cx_http1_total", Kind: otlp.MetricSum},
	{Name: "cluster.upstream_cx_http2_total", Kind: otlp.MetricSum},
	{Name: "cluster.upstream_cx_rx_bytes_buffered", Kind: otlp.MetricGauge},
	{Name: "cluster.upstream_cx_rx_bytes_total", Kind: otlp.MetricSum},
	{Name: "cluster.upstream_cx_total", Kind: otlp.MetricSum},
	{Name: "cluster.upstream_cx_tx_bytes_total", Kind: otlp.MetricSum},
	{Name: "cluster.upstream_rq", Kind: otlp.MetricSum},
	{Name: "cluster.upstream_rq_active", Kind: otlp.MetricGauge},
	{Name: "cluster.upstream_rq_completed", Kind: otlp.MetricSum},
	{Name: "cluster.upstream_rq_pending_active", Kind: otlp.MetricGauge},
	{Name: "cluster.upstream_rq_pending_total", Kind: otlp.MetricSum},
	{Name: "cluster.upstream_rq_time", Kind: otlp.MetricHistogram},
	{Name: "cluster.upstream_rq_total", Kind: otlp.MetricSum},
	{Name: "cluster.upstream_rq_xx", Kind: otlp.MetricSum},
	{Name: "cluster.version", Kind: otlp.MetricGauge},
	{Name: "cluster.warming_state", Kind: otlp.MetricGauge},
	{Name: "cluster_manager.active_clusters", Kind: otlp.MetricGauge},
	{Name: "cluster_manager.cds.config_reload", Kind: otlp.MetricSum},
	{Name: "cluster_manager.cds.config_reload_time_ms", Kind: otlp.MetricGauge},
	{Name: "cluster_manager.cds.update_attempt", Kind: otlp.MetricSum},
	{Name: "cluster_manager.cds.update_duration", Kind: otlp.MetricHistogram},
	{Name: "cluster_manager.cds.update_success", Kind: otlp.MetricSum},
	{Name: "cluster_manager.cds.update_time", Kind: otlp.MetricGauge},
	{Name: "cluster_manager.cds.version", Kind: otlp.MetricGauge},
	{Name: "cluster_manager.cluster_added", Kind: otlp.MetricSum},
	{Name: "cluster_manager.cluster_updated", Kind: otlp.MetricSum},
	{Name: "cluster_manager.update_out_of_merge_window", Kind: otlp.MetricSum},
	{Name: "cluster_manager.warming_clusters", Kind: otlp.MetricGauge},
	{Name: "control_plane.connected_state", Kind: otlp.MetricGauge},
	{Name: "dns.cares.not_found", Kind: otlp.MetricSum},
	{Name: "dns.cares.pending_resolutions", Kind: otlp.MetricGauge},
	{Name: "dns.cares.resolve_total", Kind: otlp.MetricSum},
	{Name: "filesystem.flushed_by_timer", Kind: otlp.MetricSum},
	{Name: "filesystem.write_buffered", Kind: otlp.MetricSum},
	{Name: "filesystem.write_completed", Kind: otlp.MetricSum},
	{Name: "filesystem.write_total_buffered", Kind: otlp.MetricGauge},
	{Name: "http.downstream_cx_active", Kind: otlp.MetricGauge},
	{Name: "http.downstream_cx_destroy", Kind: otlp.MetricSum},
	{Name: "http.downstream_cx_destroy_remote", Kind: otlp.MetricSum},
	{Name: "http.downstream_cx_http1_active", Kind: otlp.MetricGauge},
	{Name: "http.downstream_cx_http1_total", Kind: otlp.MetricSum},
	{Name: "http.downstream_cx_length_ms", Kind: otlp.MetricHistogram},
	{Name: "http.downstream_cx_rx_bytes_buffered", Kind: otlp.MetricGauge},
	{Name: "http.downstream_cx_rx_bytes_total", Kind: otlp.MetricSum},
	{Name: "http.downstream_cx_total", Kind: otlp.MetricSum},
	{Name: "http.downstream_cx_tx_bytes_total", Kind: otlp.MetricSum},
	{Name: "http.downstream_rq_active", Kind: otlp.MetricGauge},
	{Name: "http.downstream_rq_completed", Kind: otlp.MetricSum},
	{Name: "http.downstream_rq_http1_total", Kind: otlp.MetricSum},
	{Name: "http.downstream_rq_time", Kind: otlp.MetricHistogram},
	{Name: "http.downstream_rq_total", Kind: otlp.MetricSum},
	{Name: "http.downstream_rq_xx", Kind: otlp.MetricSum},
	{Name: "http.health_check.ok", Kind: otlp.MetricSum},
	{Name: "http.health_check.request_total", Kind: otlp.MetricSum},
	{Name: "http.rds.config_reload", Kind: otlp.MetricSum},
	{Name: "http.rds.config_reload_time_ms", Kind: otlp.MetricGauge},
	{Name: "http.rds.update_attempt", Kind: otlp.MetricSum},
	{Name: "http.rds.update_duration", Kind: otlp.MetricHistogram},
	{Name: "http.rds.update_success", Kind: otlp.MetricSum},
	{Name: "http.rds.update_time", Kind: otlp.MetricGauge},
	{Name: "http.rds.version", Kind: otlp.MetricGauge},
	{Name: "http.rq_total", Kind: otlp.MetricSum},
	{Name: "http.tracing.health_check", Kind: otlp.MetricSum},
	{Name: "listener.admin.connections_accepted_per_socket_event", Kind: otlp.MetricHistogram},
	{Name: "listener.admin.downstream_cx_active", Kind: otlp.MetricGauge},
	{Name: "listener.admin.downstream_cx_total", Kind: otlp.MetricSum},
	{Name: "listener.admin.downstream_pre_cx_active", Kind: otlp.MetricGauge},
	{Name: "listener.admin.http.downstream_rq_completed", Kind: otlp.MetricSum},
	{Name: "listener.admin.http.downstream_rq_xx", Kind: otlp.MetricSum},
	{Name: "listener.admin.main_thread.downstream_cx_active", Kind: otlp.MetricGauge},
	{Name: "listener.admin.main_thread.downstream_cx_total", Kind: otlp.MetricSum},
	{Name: "listener.connections_accepted_per_socket_event", Kind: otlp.MetricHistogram},
	{Name: "listener.downstream_cx_active", Kind: otlp.MetricGauge},
	{Name: "listener.downstream_cx_destroy", Kind: otlp.MetricSum},
	{Name: "listener.downstream_cx_length_ms", Kind: otlp.MetricHistogram},
	{Name: "listener.downstream_cx_total", Kind: otlp.MetricSum},
	{Name: "listener.downstream_pre_cx_active", Kind: otlp.MetricGauge},
	{Name: "listener.http.downstream_rq_completed", Kind: otlp.MetricSum},
	{Name: "listener.http.downstream_rq_xx", Kind: otlp.MetricSum},
	{Name: "listener.server_ssl_socket_factory.ssl_context_update_by_sds", Kind: otlp.MetricSum},
	{Name: "listener.ssl.certificate.expiration_unix_time_seconds", Kind: otlp.MetricGauge},
	{Name: "listener.worker_downstream_cx_active", Kind: otlp.MetricGauge},
	{Name: "listener.worker_downstream_cx_total", Kind: otlp.MetricSum},
	{Name: "listener_manager.lds.update_attempt", Kind: otlp.MetricSum},
	{Name: "listener_manager.lds.update_duration", Kind: otlp.MetricHistogram},
	{Name: "listener_manager.lds.update_success", Kind: otlp.MetricSum},
	{Name: "listener_manager.lds.update_time", Kind: otlp.MetricGauge},
	{Name: "listener_manager.lds.version", Kind: otlp.MetricGauge},
	{Name: "listener_manager.listener_added", Kind: otlp.MetricSum},
	{Name: "listener_manager.listener_create_success", Kind: otlp.MetricSum},
	{Name: "listener_manager.total_listeners_active", Kind: otlp.MetricGauge},
	{Name: "listener_manager.total_listeners_warming", Kind: otlp.MetricGauge},
	{Name: "listener_manager.workers_started", Kind: otlp.MetricGauge},
	{Name: "runtime.load_success", Kind: otlp.MetricSum},
	{Name: "runtime.num_keys", Kind: otlp.MetricGauge},
	{Name: "runtime.num_layers", Kind: otlp.MetricGauge},
	{Name: "runtime.override_dir_not_exists", Kind: otlp.MetricSum},
	{Name: "sds.update_attempt", Kind: otlp.MetricSum},
	{Name: "sds.update_duration", Kind: otlp.MetricHistogram},
	{Name: "sds.update_success", Kind: otlp.MetricSum},
	{Name: "sds.update_time", Kind: otlp.MetricGauge},
	{Name: "sds.version", Kind: otlp.MetricGauge},
	{Name: "server.compilation_settings.fips_mode", Kind: otlp.MetricGauge},
	{Name: "server.concurrency", Kind: otlp.MetricGauge},
	{Name: "server.days_until_first_cert_expiring", Kind: otlp.MetricGauge},
	{Name: "server.dynamic_unknown_fields", Kind: otlp.MetricSum},
	{Name: "server.hot_restart_epoch", Kind: otlp.MetricGauge},
	{Name: "server.hot_restart_generation", Kind: otlp.MetricGauge},
	{Name: "server.initialization_time_ms", Kind: otlp.MetricHistogram},
	{Name: "server.live", Kind: otlp.MetricGauge},
	{Name: "server.memory_allocated", Kind: otlp.MetricGauge},
	{Name: "server.memory_heap_size", Kind: otlp.MetricGauge},
	{Name: "server.memory_physical_size", Kind: otlp.MetricGauge},
	{Name: "server.parent_connections", Kind: otlp.MetricGauge},
	{Name: "server.state", Kind: otlp.MetricGauge},
	{Name: "server.static_unknown_fields", Kind: otlp.MetricSum},
	{Name: "server.stats_recent_lookups", Kind: otlp.MetricGauge},
	{Name: "server.total_connections", Kind: otlp.MetricGauge},
	{Name: "server.uptime", Kind: otlp.MetricGauge},
	{Name: "server.version", Kind: otlp.MetricGauge},
	{Name: "server.wip_protos", Kind: otlp.MetricSum},
	{Name: "thread_local_cluster_manager.main_thread.clusters_inflated", Kind: otlp.MetricGauge},
	{Name: "thread_local_cluster_manager.worker_clusters_inflated", Kind: otlp.MetricGauge},
	{Name: "tracing.opentelemetry.timer_flushed", Kind: otlp.MetricSum},
}

// nativeGatewayMetrics is the exact EnvoyGateway controller OTLP inventory
// captured on 2026-09-04. The controller keeps its underscore names verbatim.
var nativeGatewayMetrics = []nativeMetricSpec{
	{Name: "resource_apply_duration_seconds", Kind: otlp.MetricHistogram},
	{Name: "resource_apply_total", Kind: otlp.MetricSum},
	{Name: "resource_delete_duration_seconds", Kind: otlp.MetricHistogram},
	{Name: "resource_delete_total", Kind: otlp.MetricSum},
	{Name: "status_update_duration_seconds", Kind: otlp.MetricHistogram},
	{Name: "status_update_total", Kind: otlp.MetricSum},
	{Name: "watchable_depth", Kind: otlp.MetricGauge},
	{Name: "watchable_event_total", Kind: otlp.MetricSum},
	{Name: "watchable_publish_total", Kind: otlp.MetricSum},
	{Name: "watchable_subscribe_duration_seconds", Kind: otlp.MetricHistogram},
	{Name: "watchable_subscribe_total", Kind: otlp.MetricSum},
	{Name: "xds_snapshot_create_total", Kind: otlp.MetricSum},
}

// tickOTLPMetrics intentionally withholds native datapoints until the capture
// contract is complete. The data-plane record retained only an aggregate resource
// key set and aggregate datapoint key counts, not resource values or the per-family
// attribute join table. Both captures omitted histogram bounds. The control-plane
// record likewise omitted resource, scope, and per-family attribute details. An
// empty Metric descriptor would not be emitted parity, so no empty descriptors are
// sent as a substitute. Once those PENDING details are captured, native cumulative
// counters must use internal/state rather than independent rate heuristics.
func (c *Construct) tickOTLPMetrics(w *core.World) error {
	if w == nil || w.OTLPMetrics == nil || (!c.proxyOTLPSink && !c.gatewayOTLPSink) {
		return nil
	}
	return nil
}
