// SPDX-License-Identifier: AGPL-3.0-only

// Package beyla is the Grafana Beyla (eBPF auto-instrumentation) VOCABULARY mechanic
// lib — the shared home of Beyla's RED metric names, network-flow + TCP-stat names, its
// own self/internal metric names, the advisory histogram buckets, the OTLP→Prometheus
// label/attribute keys, the per-mode resource-label sets, the eBPF distro marker, the
// feature flags, and the per-deployment-context emission matrix. It is a peer to
// internal/cw and internal/genai (a mechanic lib, not a construct): the Beyla observation
// lane on the web_service workload and the beyla_agent construct build their series FROM
// these constants and emit them via the existing promrw (metrics) + hand-encoded OTLP
// (trace) seams. This package emits NOTHING itself and imports stdlib only — it is NOT an
// OTel SDK (the synthetic-path SDK ban is unchanged; see ARCHITECTURE §6.1).
//
// Names are LAW. The metric names, bucket arrays, attribute keys, distro marker, and
// per-signal avoidance behavior are sourced VERBATIM from Beyla (validated 2026-06-15
// against ~/repos/beyla, which vendors go.opentelemetry.io/obi and sets
// attr.VendorPrefix = "beyla" so every obi_* surfaces as beyla_*) and the
// k8s-monitoring Helm chart's autoInstrumentation feature, and RECONCILED against a LIVE
// capture of a real Beyla 3.20.0 DaemonSet on a reference cluster (OBI vendored;
// telemetry_sdk_version="v1.43.0"; raw /metrics + /internal/metrics exposition, 2026-06-15).
// RED metric names are the Prometheus form Beyla exports (the k8s path scrapes Prometheus).
// Histogram bucket boundaries are Beyla's compile-time defaults (bucket.go).
//
// ⚠ ABSENT-DIM TRAP: Beyla emits absent k8s/cloud dimensions as EMPTY STRING (e.g.
// k8s_job_name="", k8s_cronjob_name="") — NOT omitted. This is an EXCEPTION to synthkit's
// "an absent dimension is OMITTED" invariant and applies to ALL Beyla families. The full
// k8s/cloud envelope keys below are present on every series; the consuming lane emits "" for
// absent dims rather than dropping the label. See signals/beyla.md.
package beyla

// Mode is the deployment substrate Beyla observes within. It switches the resource/label
// decoration (k8s_* attributes vs host_* attributes).
type Mode string

const (
	ModeKubernetes Mode = "kubernetes"
	ModeStandalone Mode = "standalone"
)

// Context is Beyla's per-service instrumentation context. It switches WHICH signals Beyla
// emits, mirroring Beyla's real per-signal avoidance (ExcludeOTelInstrumentedServices).
type Context string

const (
	// ContextEBPFOnly: the observed service is NOT otherwise instrumented, so Beyla is the
	// sole telemetry source (RED + span + service-graph + traces + network + target_info).
	ContextEBPFOnly Context = "ebpf_only"
	// ContextCoexistSDK: the observed service already runs an OTel SDK, so Beyla suppresses
	// the SDK-covered signals (RED/span/service-graph/traces) per signal and emits ONLY
	// network-flow + target_info + beyla_avoided_services.
	ContextCoexistSDK Context = "coexist_sdk"
)

// RED metric names (Prometheus form; metric.go:59-79). gen_ai RED instruments are NOT
// duplicated here — they reuse internal/genai (MetricClientOpDuration / MetricClientTokenUsage).
const (
	MetricHTTPServerDuration     = "http_server_request_duration_seconds"
	MetricHTTPServerReqBodySize  = "http_server_request_body_size_bytes"
	MetricHTTPServerRespBodySize = "http_server_response_body_size_bytes"
	MetricHTTPClientDuration     = "http_client_request_duration_seconds"
	MetricHTTPClientReqBodySize  = "http_client_request_body_size_bytes"
	MetricHTTPClientRespBodySize = "http_client_response_body_size_bytes"
	MetricRPCServerDuration      = "rpc_server_duration_seconds"
	MetricRPCClientDuration      = "rpc_client_duration_seconds"
	MetricDBClientDuration       = "db_client_operation_duration_seconds"
)

// Network-flow + TCP-stat metric names (metric.go, attr_defs.go). The flow counter is
// emitted in v1 (it is NEVER per-signal-gated); the TCP stats are modelled but feature-gated
// (off in the chart default — see FeatureStats).
const (
	MetricNetworkFlowBytes   = "beyla_network_flow_bytes_total"
	MetricStatTCPRTT         = "beyla_stat_tcp_rtt_seconds"
	MetricStatTCPFailedConns = "beyla_stat_tcp_failed_connections"
	MetricStatTCPRetransmits = "beyla_stat_tcp_retransmits"
	MetricStatTCPIO          = "beyla_stat_tcp_io_bytes_total"
)

// Self / internal metric names (beyla_ prefix; iprom.go). Emitted by the beyla_agent
// construct (substrate-scoped), NOT by the observation lane.
const (
	// MetricInternalBuildInfo is on /internal/metrics (labels: goarch, goos, goversion,
	// revision, version). MetricBuildInfo is on /metrics (NOT /internal) and additionally
	// carries target_lang. Live-confirmed distinct surfaces (reference cluster Beyla 3.20.0).
	MetricInternalBuildInfo     = "beyla_internal_build_info"
	MetricBuildInfo             = "beyla_build_info" // /metrics (not /internal); +target_lang label
	MetricInstrumentedProcesses = "beyla_instrumented_processes"
	MetricAvoidedServices       = "beyla_avoided_services"
	MetricInstrumentationErrors = "beyla_instrumentation_errors_total"
	MetricBPFProbeExecutions    = "beyla_bpf_probe_executions_total"
	MetricBPFProbeLatency       = "beyla_bpf_probe_latency_seconds_total"
	MetricBPFMapEntries         = "beyla_bpf_map_entries_total"
	MetricBPFMapMaxEntries      = "beyla_bpf_map_max_entries_total"
	MetricBPFNetworkPackets     = "beyla_bpf_network_packets_total"         // no labels
	MetricBPFNetworkIgnoredPkts = "beyla_bpf_network_ignored_packets_total" // no labels
	MetricEBPFTracerFlushes     = "beyla_ebpf_tracer_flushes"               // histogram
	MetricKubeCacheForwardLag   = "beyla_kube_cache_forward_lag_seconds"    // histogram
	MetricOTelMetricExports     = "beyla_otel_metric_exports_total"
	MetricOTelTraceExports      = "beyla_otel_trace_exports_total"
	MetricPromHTTPRequests      = "beyla_prometheus_http_requests_total"

	// MetricHostInfo (traces_host_info) carries grafana_host_id (e.g. "i-..."). Live-confirmed.
	MetricHostInfo = "traces_host_info"
)

// Auto-injection webhook metrics (pkg/webhook/metrics.go:18-25). DOCUMENTED-ONLY, NOT
// emitted in v1: Beyla flags the SDK auto-injection webhook "purely experimental, may be
// removed" and it overlaps the OTel Operator. Kept here so the names are recorded; the lane
// and construct never emit them.
const (
	MetricSDKInjectionRequests = "beyla_sdk_injection_requests_total" // documented-only, not emitted
	MetricSDKInjectionRestarts = "beyla_sdk_injection_restarts_total" // documented-only, not emitted
)

// Histogram buckets (Beyla compile-time defaults; bucket.go:22-25).
var (
	// DurationBuckets is Beyla's default bucket set for the *_duration_seconds RED histograms.
	DurationBuckets = []float64{0, 0.005, 0.01, 0.025, 0.05, 0.075, 0.1, 0.25, 0.5, 0.75, 1, 2.5, 5, 7.5, 10}
	// SizeBuckets is Beyla's default bucket set for the *_body_size_bytes RED histograms.
	SizeBuckets = []float64{0, 32, 64, 128, 256, 512, 1024, 2048, 4096, 8192}
)

// Metric label KEYS (the semconv attribute keys, OTLP→Prom mangled to underscores). All
// spellings below are LIVE-CONFIRMED against reference cluster Beyla 3.20.0 capture (2026-06-15)
// unless a comment marks them v: assumed (source-confirmed, no live instance).
const (
	LabelHTTPRequestMethod      = "http_request_method"
	LabelHTTPResponseStatusCode = "http_response_status_code"
	LabelHTTPRoute              = "http_route"
	LabelServerAddress          = "server_address"
	LabelServerPort             = "server_port"
	LabelURLScheme              = "url_scheme"
	LabelRPCMethod              = "rpc_method"
	LabelRPCGRPCStatusCode      = "rpc_grpc_status_code"
	LabelDBOperationName        = "db_operation_name"
	LabelDBSystemName           = "db_system_name"
	LabelDBCollectionName       = "db_collection_name"
	LabelDBNamespace            = "db_namespace"
	LabelErrorType              = "error_type"
	LabelServiceName            = "service_name"
	LabelServiceNamespace       = "service_namespace"
	LabelServiceVersion         = "service_version"
	LabelServiceInstanceID      = "service_instance_id"
	LabelInstance               = "instance"
	LabelJob                    = "job"
	LabelSource                 = "source"
	LabelDirection              = "direction"
	LabelTelemetryType          = "telemetry_type"

	// Full k8s resource envelope (RED + target_info). ⚠ ALL present on every series; absent
	// dims emitted as "" (NOT omitted) — see ABSENT-DIM TRAP in the package doc.
	LabelK8sClusterName     = "k8s_cluster_name"
	LabelK8sNamespaceName   = "k8s_namespace_name"
	LabelK8sNodeName        = "k8s_node_name"
	LabelK8sPodName         = "k8s_pod_name"
	LabelK8sPodUID          = "k8s_pod_uid"
	LabelK8sPodStartTime    = "k8s_pod_start_time"
	LabelK8sContainerName   = "k8s_container_name"
	LabelK8sKind            = "k8s_kind"
	LabelK8sOwnerName       = "k8s_owner_name"
	LabelK8sDeploymentName  = "k8s_deployment_name"
	LabelK8sReplicaSetName  = "k8s_replicaset_name"
	LabelK8sDaemonSetName   = "k8s_daemonset_name"
	LabelK8sStatefulSetName = "k8s_statefulset_name"
	LabelK8sJobName         = "k8s_job_name"
	LabelK8sCronJobName     = "k8s_cronjob_name"

	// Network-flow k8s src/dst keys (chart-curated allow-list; default-on in k8s mode).
	// Dst fields often "" for egress to non-k8s/virtual targets.
	LabelK8sSrcName      = "k8s_src_name"
	LabelK8sSrcNamespace = "k8s_src_namespace"
	LabelK8sSrcOwnerName = "k8s_src_owner_name"
	LabelK8sSrcOwnerType = "k8s_src_owner_type"
	LabelK8sDstName      = "k8s_dst_name"
	LabelK8sDstNamespace = "k8s_dst_namespace"
	LabelK8sDstOwnerName = "k8s_dst_owner_name"
	LabelK8sDstOwnerType = "k8s_dst_owner_type"

	// Network-flow standalone src/dst name keys (opt-in upstream; used in standalone mode here).
	LabelSrcName = "src_name"
	LabelDstName = "dst_name"

	// Span-metric keys (traces_spanmetrics_calls_total / _latency). source="beyla" confirmed.
	LabelSpanKind                  = "span_kind"
	LabelSpanName                  = "span_name"
	LabelStatusCode                = "status_code"
	LabelDeploymentEnvironmentName = "deployment_environment_name"
	LabelCloudAvailabilityZone     = "cloud_availability_zone"
	LabelCloudRegion               = "cloud_region"
	LabelTelemetrySDKLanguage      = "telemetry_sdk_language"

	// Service-graph keys (traces_service_graph_*). ⚠ Uses client_k8s_*/server_k8s_* PREFIXED
	// keys, NOT bare k8s_*; client/server are the service-pair endpoints.
	LabelClient                 = "client"
	LabelServer                 = "server"
	LabelConnectionType         = "connection_type"
	LabelClientServiceNamespace = "client_service_namespace"
	LabelServerServiceNamespace = "server_service_namespace"
	LabelClientK8sClusterName   = "client_k8s_cluster_name"
	LabelClientK8sNamespaceName = "client_k8s_namespace_name"
	LabelServerK8sClusterName   = "server_k8s_cluster_name"
	LabelServerK8sNamespaceName = "server_k8s_namespace_name"

	// target_info / traces_target_info cloud + host + telemetry keys.
	LabelCloudAccountID      = "cloud_account_id"
	LabelCloudPlatform       = "cloud_platform"
	LabelCloudProvider       = "cloud_provider"
	LabelHostID              = "host_id"
	LabelHostName            = "host_name"
	LabelHostImageID         = "host_image_id"
	LabelHostType            = "host_type"
	LabelOSType              = "os_type"
	LabelTelemetrySDKName    = "telemetry_sdk_name"
	LabelTelemetrySDKVersion = "telemetry_sdk_version"
	LabelTelemetryDistroName = "telemetry_distro_name"
	LabelTelemetryDistroVer  = "telemetry_distro_version"

	// traces_host_info key (NOT cloud_host_id).
	LabelGrafanaHostID = "grafana_host_id"

	// beyla_build_info / beyla_internal_build_info label.
	LabelTargetLang = "target_lang" // /metrics beyla_build_info only
)

// Direction label VALUES for LabelDirection (network-flow). Live-confirmed value set.
const (
	DirectionRequest  = "request"
	DirectionResponse = "response"
	DirectionUnknown  = "unknown"
)

// Connection-type label VALUES for LabelConnectionType (service-graph). Live-confirmed.
const (
	ConnectionTypeVirtualNode = "virtual_node"
	ConnectionTypeEmpty       = "" // emitted as "" for non-virtual edges
)

// OTLP dotted span-attribute KEYS for boundary traces (semconv dotted form, not mangled).
const (
	AttrHTTPRequestMethod      = "http.request.method"
	AttrHTTPResponseStatusCode = "http.response.status_code"
	AttrHTTPRoute              = "http.route"
	AttrServerAddress          = "server.address"
	AttrServerPort             = "server.port"
	AttrRPCMethod              = "rpc.method"
	AttrRPCGRPCStatusCode      = "rpc.grpc.status_code"
	AttrDBOperationName        = "db.operation.name"
	AttrDBSystemName           = "db.system.name"
	AttrErrorType              = "error.type"
	// AttrSpanMetricsSkip is stamped by Beyla on its own spans so the trace-derived
	// spanmetrics generator (chart Alloy span_metrics_prefilter) drops them; Beyla then
	// self-emits the span/service-graph families (tracesgen.go:336,1265).
	AttrSpanMetricsSkip = "span.metrics.skip"
)

// Resource origin markers. AttrDistroName/DistroName is the load-bearing "came from Beyla"
// flag (attrs.go:121-124; otelcfg/common.go:102-113; prom.go:94-97 target_info), distinct
// from an SDK's own telemetry.sdk.*. ⚠ LIVE-CONFIRMED target_info values (reference cluster Beyla
// 3.20.0, OBI v1.43.0, 2026-06-15): telemetry_sdk_name="beyla" (NOT "opentelemetry"),
// telemetry_distro_name="opentelemetry-ebpf-instrumentation", telemetry_distro_version="unset".
const (
	AttrDistroName     = "telemetry.distro.name"
	DistroName         = "opentelemetry-ebpf-instrumentation"
	DistroVersionUnset = "unset" // telemetry_distro_version value (live-confirmed)
	// SDKNameBeyla is the target_info telemetry_sdk_name value Beyla actually emits.
	SDKNameBeyla = "beyla"
	// SDKName retained for back-compat; its Beyla value is "beyla" (was "opentelemetry").
	SDKName = SDKNameBeyla
)

// Feature flags (k8s-monitoring chart prometheus_export.features list).
const (
	FeatureApplication  = "application"
	FeatureNetwork      = "network"
	FeatureServiceGraph = "application_service_graph"
	FeatureAppSpan      = "application_span"
	FeatureAppHost      = "application_host"
	FeatureStats        = "application_stats" // TCP stats; off by chart default
)

// ResourceLabelKeys returns the resource-identity label keys for the given mode. k8s mode
// carries the FULL k8s_* envelope (live-confirmed against reference cluster Beyla 3.20.0 — the RED /
// target_info key set); standalone carries host_* and never any k8s_* key. ⚠ Absent k8s
// dims (e.g. k8s_job_name, k8s_cronjob_name) are still present as keys and emitted "" — see
// the ABSENT-DIM TRAP in the package doc.
func ResourceLabelKeys(m Mode) []string {
	if m == ModeStandalone {
		return []string{"service_name", "service_namespace", "host_name", "host_id"}
	}
	return []string{
		"service_name", "service_namespace",
		"k8s_cluster_name", "k8s_namespace_name", "k8s_node_name",
		"k8s_pod_name", "k8s_pod_uid", "k8s_pod_start_time",
		"k8s_container_name", "k8s_kind", "k8s_owner_name",
		"k8s_deployment_name", "k8s_replicaset_name", "k8s_daemonset_name",
		"k8s_statefulset_name", "k8s_job_name", "k8s_cronjob_name",
	}
}

// ServerDurationMetric returns the server-side RED duration instrument name (HTTP server).
func ServerDurationMetric() string { return MetricHTTPServerDuration }

// ClientDurationMetric maps a ledger hop kind to its client-side RED duration instrument:
// db/cache → db_client_operation_duration_seconds; everything else → the HTTP client
// duration. (RPC/gRPC hops are selected explicitly by the lane, not by this kind switch.)
func ClientDurationMetric(hopKind string) string {
	switch hopKind {
	case "db", "cache":
		return MetricDBClientDuration
	default:
		return MetricHTTPClientDuration
	}
}

// DefaultFeatures returns the chart-default feature list for the given mode. Both modes
// default to the k8s-monitoring autoInstrumentation list; standalone drops application_host
// (no k8s host-network model off-cluster).
func DefaultFeatures(m Mode) []string {
	if m == ModeStandalone {
		return []string{FeatureApplication, FeatureNetwork, FeatureServiceGraph, FeatureAppSpan}
	}
	return []string{FeatureApplication, FeatureNetwork, FeatureServiceGraph, FeatureAppSpan, FeatureAppHost}
}

// SourceValue returns the span-metric / service-graph source label value for Beyla-emitted
// families ("beyla"; live-confirmed against reference cluster Beyla 3.20.0, 2026-06-15).
func SourceValue() string { return "beyla" }

// EmissionSet records, per deployment context, which signal families Beyla emits.
type EmissionSet struct {
	RED            bool
	SpanMetrics    bool
	ServiceGraph   bool
	Traces         bool
	Network        bool
	TargetInfo     bool
	AvoidedService bool
}

// Emission returns the per-signal emission matrix for a deployment context, encoding
// Beyla's verified per-signal avoidance so the lane stays declarative:
//   - ebpf_only  → Beyla is the sole source: everything on EXCEPT AvoidedService.
//   - coexist_sdk → SDK covers RED/span/service-graph/traces: only Network + TargetInfo +
//     AvoidedService on (network-flow is never gated).
//
// An unrecognised context defaults to the safe ebpf_only (sole-source) set.
func Emission(c Context) EmissionSet {
	switch c {
	case ContextCoexistSDK:
		return EmissionSet{
			Network:        true,
			TargetInfo:     true,
			AvoidedService: true,
		}
	default: // ContextEBPFOnly and any unknown context
		return EmissionSet{
			RED:          true,
			SpanMetrics:  true,
			ServiceGraph: true,
			Traces:       true,
			Network:      true,
			TargetInfo:   true,
		}
	}
}
