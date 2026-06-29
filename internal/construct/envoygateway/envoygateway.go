// SPDX-License-Identifier: AGPL-3.0-only

// Package envoygateway implements the "envoy_gateway" construct.
//
// Kind:     "envoy_gateway"
// Scope:    core.ScopeSubstrate (cluster disambiguates; no blueprint label)
// Signals:  []core.SignalClass{core.Metrics}
// Interval: 60s
//
// Build requires fx.Cluster (non-nil).
//
// Signal contract (signals/k8s-addons.md, live recon svc-group-b.md §3):
//
// TWO surfaces, both scraped on :19001:
//
// Surface 1 — Control Plane (job=gateway-helm, pod=envoy-gateway-*)
//
//	Workload: "envoy-gateway", namespace: envoy-gateway-system, 1 pod
//	Container: "envoy-gateway"
//	EG-specific: xds_*, watchable_*, resource_*, status_update_*, topology_*, wasm_*
//	Shared: controller_runtime_*, rest_client_*, workqueue_*, certwatcher_*,
//	        leader_election_master_status
//
//	CRITICAL: NO envoy_gateway_* prefix — that prefix query is empty in Mimir
//	(live recon confirms the control plane emits no such family).
//
// Surface 2 — Data Plane (job=envoy, pods=envoy-default-eg-proxy-*)
//
//	Workload: "envoy-default-eg-proxy", namespace: envoy-gateway-system, 2 pods
//	Container: "envoy"
//	Prefix: envoy_* (full Envoy proxy /stats/prometheus surface)
//	_time histograms are in MILLISECONDS (le values: 0.5,1,5,10,...,+Inf)
//	Extra node-topology labels: availability_zone, instance_type, nodepool
//
// Fallback: nil SubstrateWorkloads → cluster-scoped series (no pod labels).
//
// ARCHITECTURE invariants honoured:
//   - I3:  counters via state.Add (cumulative); gauges via state.Set
//   - I13: no empty/sentinel labels — absent dims are omitted
//   - I21: ScopeSubstrate — no blueprint label
package envoygateway

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/k8saddon"
	"github.com/rknightion/synthkit/internal/sink/loki"
	"github.com/rknightion/synthkit/internal/state"
)

const (
	kind     = "envoy_gateway"
	interval = 60 * time.Second

	// Both surfaces scrape on :19001 (prometheus.io/port=19001 annotation, live recon §3.B).
	portMetrics = 19001
)

// Config is the construct config struct (empty — all identity from fixtures).
type Config struct{}

// Construct renders envoy-gateway (control plane + data plane) metrics for one cluster.
type Construct struct {
	clust *fixture.Cluster
	st    *state.State
}

// Compile-time interface check.
var _ core.Construct = (*Construct)(nil)

// New builds a Construct from cfg and the resolved fixtures.
// Returns an error if fx.Cluster is nil.
func New(cfg any, fx *fixture.Set) (core.Construct, error) {
	if fx.Cluster == nil {
		return nil, errors.New("envoy_gateway: fixture.Cluster is required (nil)")
	}
	return &Construct{
		clust: fx.Cluster,
		st:    state.NewState(),
	}, nil
}

// Kind implements core.Construct.
func (c *Construct) Kind() string { return kind }

// Signals implements core.Construct — metrics + logs.
func (c *Construct) Signals() []core.SignalClass { return []core.SignalClass{core.Metrics, core.Logs} }

// Interval implements core.Construct.
func (c *Construct) Interval() time.Duration { return interval }

// Tick renders one envoy-gateway snapshot for the cluster (both surfaces).
func (c *Construct) Tick(ctx context.Context, now time.Time, w *core.World) error {
	cluster := c.clust.Name
	tickSec := interval.Seconds()
	scale := tickSec / 30.0
	factor := w.Shape.Factor(now, c.clust.Env.Weight, c.clust.Env.NonProd)

	// ── Base label maps for each surface ─────────────────────────────────────

	controlBase := map[string]string{
		"cluster":          cluster,
		"k8s_cluster_name": cluster,
		"job":              "gateway-helm",
	}

	dataBase := map[string]string{
		"cluster":          cluster,
		"k8s_cluster_name": cluster,
		"job":              "envoy",
	}

	// ── Pod-stamp helpers ─────────────────────────────────────────────────────
	//
	// StampPods returns nil when SubstrateWorkloads is absent — we fall back to
	// single cluster-scoped series (no pod/namespace/container/instance labels).
	controlMaps := k8saddon.StampPods(c.clust, "envoy-gateway", controlBase, portMetrics)
	dataMaps := k8saddon.StampPods(c.clust, "envoy-default-eg-proxy", dataBase, portMetrics)

	// emitControl emits a metric for each control-plane pod (or base if no pods).
	emitControl := func(extra map[string]string, emit func(lbls map[string]string)) {
		if len(controlMaps) > 0 {
			for _, pm := range controlMaps {
				emit(mergeLabels(pm, extra))
			}
		} else {
			lbls := mergeLabels(controlBase, extra)
			emit(lbls)
		}
	}

	// emitData emits a metric for each data-plane pod (or base if no pods),
	// with the extra node-topology labels stamped per-pod from the cluster nodes.
	emitData := func(extra map[string]string, emit func(lbls map[string]string)) {
		if len(dataMaps) > 0 {
			for i, pm := range dataMaps {
				topo := dataPlaneTopology(c.clust, i)
				lbls := mergeLabels(pm, topo)
				lbls = mergeLabels(lbls, extra)
				emit(lbls)
			}
		} else {
			lbls := mergeLabels(dataBase, extra)
			emit(lbls)
		}
	}

	// ── Surface 1: Control Plane ──────────────────────────────────────────────

	// EG-specific: xds_*
	emitControl(map[string]string{"status": "success"}, func(lbls map[string]string) {
		c.st.Add("xds_snapshot_create_total", lbls, scale)
		c.st.Add("xds_snapshot_update_total", lbls, scale)
	})

	// xds_stream_duration_seconds histogram (seconds — NOT ms, controller-runtime scale)
	for _, pm := range controlMaps {
		c.st.Observe("xds_stream_duration_seconds", pm, secondsBuckets, state.LEBare, 0.1+factor*0.1*w.Shape.Noise(0.3))
	}
	if len(controlMaps) == 0 {
		c.st.Observe("xds_stream_duration_seconds", controlBase, secondsBuckets, state.LEBare, 0.1+factor*0.05*w.Shape.Noise(0.3))
	}

	// watchable_*: depth (gauge) + events (counter per runner × event_type)
	for _, runner := range watchableRunners {
		for _, evType := range watchableEventTypes {
			emitControl(map[string]string{"runner": runner, "event_type": evType}, func(lbls map[string]string) {
				c.st.Add("watchable_event_total", lbls, scale)
			})
		}
		emitControl(map[string]string{"runner": runner}, func(lbls map[string]string) {
			c.st.Set("watchable_depth", lbls, 0)
			c.st.Add("watchable_publish_total", lbls, scale)
			c.st.Add("watchable_subscribe_total", lbls, scale)
		})
	}

	// resource_apply_total / resource_apply_duration_seconds_* / resource_delete_*
	for _, k := range resourceKinds {
		emitControl(map[string]string{"kind": k, "name": k + "-resource", "status": "success"}, func(lbls map[string]string) {
			c.st.Add("resource_apply_total", lbls, scale)
		})
		emitControl(map[string]string{"kind": k, "name": k + "-resource"}, func(lbls map[string]string) {
			c.st.Observe("resource_apply_duration_seconds", lbls, secondsBuckets, state.LEBare, 0.05+factor*0.05*w.Shape.Noise(0.4))
			c.st.Add("resource_delete_total", lbls, 0)
		})
	}

	// status_update_* + topology_injector_webhook_events_total + wasm_cache_entries
	emitControl(nil, func(lbls map[string]string) {
		c.st.Add("status_update_total", lbls, scale)
		c.st.Add("topology_injector_webhook_events_total", lbls, scale)
		c.st.Set("wasm_cache_entries", lbls, 0)
	})

	// controller_runtime_reconcile_total (per result)
	for _, result := range reconcileResults {
		emitControl(map[string]string{
			"controller": "gatewayapi-1781111929",
			"result":     result,
		}, func(lbls map[string]string) {
			c.st.Add("controller_runtime_reconcile_total", lbls, scale)
		})
	}
	emitControl(map[string]string{"controller": "gatewayapi-1781111929"}, func(lbls map[string]string) {
		c.st.Observe("controller_runtime_reconcile_time_seconds", lbls, secondsBuckets, state.LEBare, 0.01+factor*0.02*w.Shape.Noise(0.4))
		c.st.Set("controller_runtime_active_workers", lbls, 1)
		c.st.Set("controller_runtime_max_concurrent_reconciles", lbls, 1)
	})

	// controller_runtime_webhook_* (control plane has webhooks on 9443)
	emitControl(nil, func(lbls map[string]string) {
		c.st.Add("controller_runtime_webhook_requests_total", lbls, scale)
		c.st.Set("controller_runtime_webhook_requests_in_flight", lbls, 0)
	})

	// rest_client_requests_total (client-go calls to kube-apiserver)
	for _, method := range []string{"GET", "POST", "PUT", "PATCH"} {
		emitControl(map[string]string{
			"code":   "200",
			"host":   "172.20.0.1:443",
			"method": method,
		}, func(lbls map[string]string) {
			c.st.Add("rest_client_requests_total", lbls, scale)
		})
	}

	// workqueue_* (leader-election + reconcile queue)
	for _, qname := range controlWorkqueues {
		emitControl(map[string]string{"name": qname}, func(lbls map[string]string) {
			c.st.Add("workqueue_adds_total", lbls, scale)
			c.st.Set("workqueue_depth", lbls, 0)
			c.st.Set("workqueue_longest_running_processor_seconds", lbls, 0)
			c.st.Add("workqueue_retries_total", lbls, 0)
			c.st.Set("workqueue_unfinished_work_seconds", lbls, 0)
		})
	}

	// certwatcher_read_certificate_total (webhook cert hot-reload)
	emitControl(nil, func(lbls map[string]string) {
		c.st.Add("certwatcher_read_certificate_total", lbls, 0)
	})

	// leader_election_master_status
	emitControl(map[string]string{"name": "envoy-gateway-leader"}, func(lbls map[string]string) {
		c.st.Set("leader_election_master_status", lbls, 1)
	})

	// ── Surface 2: Data Plane ─────────────────────────────────────────────────

	// envoy_cluster_upstream_rq_total (per cluster name)
	for _, clName := range envoyClusterNames {
		emitData(map[string]string{"envoy_cluster_name": clName}, func(lbls map[string]string) {
			c.st.Add("envoy_cluster_upstream_rq_total", lbls, scale*10)
		})
	}

	// envoy_cluster_upstream_rq_time histogram (milliseconds)
	for _, clName := range envoyClusterNames {
		emitData(map[string]string{"envoy_cluster_name": clName}, func(lbls map[string]string) {
			c.st.Observe("envoy_cluster_upstream_rq_time", lbls, envoyMsBuckets, state.LEBare, 10.0+factor*15.0*w.Shape.Noise(0.4))
		})
	}

	// envoy_cluster_upstream_cx_active (gauge, per cluster)
	for _, clName := range envoyClusterNames {
		emitData(map[string]string{"envoy_cluster_name": clName}, func(lbls map[string]string) {
			c.st.Set("envoy_cluster_upstream_cx_active", lbls, 1)
		})
	}

	// envoy_http_downstream_rq_xx (per conn-manager prefix × response class)
	for _, mgr := range httpConnManagers {
		for _, cls := range responseCodeClasses {
			emitData(map[string]string{
				"envoy_http_conn_manager_prefix": mgr,
				"envoy_response_code_class":      cls,
			}, func(lbls map[string]string) {
				weight := 0.9 // 2xx most common
				if cls != "2" {
					weight = 0.02
				}
				c.st.Add("envoy_http_downstream_rq_xx", lbls, scale*50*weight)
			})
		}
	}

	// envoy_http_downstream_rq_time histogram (milliseconds, per conn-manager)
	for _, mgr := range httpConnManagers {
		emitData(map[string]string{"envoy_http_conn_manager_prefix": mgr}, func(lbls map[string]string) {
			c.st.Observe("envoy_http_downstream_rq_time", lbls, envoyMsBuckets, state.LEBare, 15.0+factor*20.0*w.Shape.Noise(0.4))
		})
	}

	// envoy_listener_downstream_cx_active (gauge, per listener address)
	for _, addr := range listenerAddresses {
		emitData(map[string]string{"envoy_listener_address": addr}, func(lbls map[string]string) {
			c.st.Set("envoy_listener_downstream_cx_active", lbls, 2)
		})
	}

	// envoy_control_plane_connected_state (gauge=1)
	emitData(nil, func(lbls map[string]string) {
		c.st.Set("envoy_control_plane_connected_state", lbls, 1)
	})

	// envoy_server_uptime (gauge, monotonically increasing)
	emitData(nil, func(lbls map[string]string) {
		c.st.Add("envoy_server_uptime", lbls, tickSec)
	})

	// envoy_server_live (gauge=1)
	emitData(nil, func(lbls map[string]string) {
		c.st.Set("envoy_server_live", lbls, 1)
	})

	// envoy_server_concurrency (gauge)
	emitData(nil, func(lbls map[string]string) {
		c.st.Set("envoy_server_concurrency", lbls, 4)
	})

	// envoy_server_days_until_first_cert_expiring (gauge)
	emitData(nil, func(lbls map[string]string) {
		c.st.Set("envoy_server_days_until_first_cert_expiring", lbls, 89)
	})

	// envoy_server_memory_allocated + envoy_server_memory_heap_size (gauges)
	emitData(nil, func(lbls map[string]string) {
		c.st.Set("envoy_server_memory_allocated", lbls, 1024*1024*32) // 32 MiB
		c.st.Set("envoy_server_memory_heap_size", lbls, 1024*1024*64) // 64 MiB
	})

	// envoy_cluster_upstream_cx_total (counter)
	for _, clName := range envoyClusterNames {
		emitData(map[string]string{"envoy_cluster_name": clName}, func(lbls map[string]string) {
			c.st.Add("envoy_cluster_upstream_cx_total", lbls, scale*2)
		})
	}

	// envoy_cluster_upstream_rq_pending_active (gauge)
	for _, clName := range envoyClusterNames {
		emitData(map[string]string{"envoy_cluster_name": clName}, func(lbls map[string]string) {
			c.st.Set("envoy_cluster_upstream_rq_pending_active", lbls, 0)
		})
	}

	// envoy_cluster_membership_total (gauge)
	for _, clName := range envoyClusterNames {
		emitData(map[string]string{"envoy_cluster_name": clName}, func(lbls map[string]string) {
			c.st.Set("envoy_cluster_membership_total", lbls, 2)
		})
	}

	// envoy_cluster_membership_healthy (gauge)
	for _, clName := range envoyClusterNames {
		emitData(map[string]string{"envoy_cluster_name": clName}, func(lbls map[string]string) {
			c.st.Set("envoy_cluster_membership_healthy", lbls, 2)
		})
	}

	// envoy_tracing_opentelemetry_spans_sent + spans_dropped (counters)
	emitData(nil, func(lbls map[string]string) {
		c.st.Add("envoy_tracing_opentelemetry_spans_sent", lbls, scale*50)
		c.st.Add("envoy_tracing_opentelemetry_spans_dropped", lbls, 0)
	})

	// envoy_filesystem_write_total (counter)
	emitData(nil, func(lbls map[string]string) {
		c.st.Add("envoy_filesystem_write_total", lbls, scale*5)
	})

	// envoy_runtime_load_success (gauge=1)
	emitData(nil, func(lbls map[string]string) {
		c.st.Set("envoy_runtime_load_success", lbls, 1)
	})

	// envoy_access_logs_grpc_access_log_entries_buffered (gauge)
	emitData(nil, func(lbls map[string]string) {
		c.st.Set("envoy_access_logs_grpc_access_log_entries_buffered", lbls, 0)
	})

	if err := w.Metrics.Write(ctx, c.st.Collect(now)); err != nil {
		return err
	}

	if w.Logs != nil {
		streams := c.emitLogs(cluster, now, controlMaps, dataMaps)
		if len(streams) > 0 {
			if err := w.Logs.Write(ctx, streams); err != nil {
				return err
			}
		}
	}

	return nil
}

// ─── log emission ─────────────────────────────────────────────────────────────

// accessLogMethods and accessLogPaths are deterministic pools for generating realistic
// JSON access log bodies (data-plane surface, recon svc-group-b.md §3.C).
var accessLogMethods = []string{"GET", "POST", "GET", "GET", "POST", "GET", "DELETE"}

var accessLogPaths = []string{
	"/api/v1/products",
	"/api/v1/cart",
	"/api/v1/checkout",
	"/healthz",
	"/api/v1/users",
	"/otlp-http/v1/traces",
	"/api/v1/orders",
}

// accessLogRouteName and accessLogUpstreamCluster are the high-card fields that go in
// the JSON body only (never in stream labels — ARCHITECTURE I14/I15).
var accessLogRouteNames = []string{
	"httproute/otel-demo/frontend/rule/0/match/0/frontend",
	"httproute/otel-demo/flagd-ui/rule/0/match/0/flagd",
	"httproute/monitoring/alloy-logs/rule/0/match/0/alloy",
	"httproute/otel-demo/frontend/rule/1/match/0/frontend",
	"httproute/monitoring/alloy-metrics/rule/0/match/0/alloy",
}

var accessLogUpstreamClusters = []string{
	"httproute/otel-demo/frontend/rule/0",
	"httproute/otel-demo/flagd-ui/rule/0",
	"httproute/monitoring/alloy-logs/rule/0",
	"tracing",
	"prometheus_stats",
}

// accessLogResponseCodeWeights defines the response code distribution:
// ~85% 200, ~10% 4xx (mix of 400/404/429), ~5% 5xx (502/503).
var accessLogResponseCodes = []int{200, 200, 200, 200, 200, 200, 200, 200, 200, 200, 200, 200, 200, 200, 200, 200, 200, 400, 404, 429, 502, 503}

// controlPlaneLogMessages are the sparse xDS reconciliation messages emitted by the
// envoy-gateway controller (recon svc-group-b.md §3.C — zap format).
var controlPlaneLogMessages = []struct {
	level   string
	logger  string
	message string
}{
	{"info", "xds", "open delta watch for ClusterLoadAssignment"},
	{"info", "gateway-api", "Reconciling HTTPRoute"},
	{"info", "infrastructure", "resource applied successfully"},
	{"info", "xds", "snapshot updated for node"},
	{"warn", "gateway-api", "HTTPRoute condition not met, retrying"},
}

// synthProxyPodName returns a synthetic fallback proxy pod name when SubstrateWorkloads
// are absent.
func synthProxyPodName(idx int) string {
	return fmt.Sprintf("envoy-default-eg-proxy-%08x-%05d", 0x899d26d2, idx)
}

// synthControllerPodName returns a synthetic fallback controller pod name.
func synthControllerPodName() string {
	return "envoy-gateway-6846bd4cc8-abcde"
}

// emitLogs constructs and returns the two Loki log surfaces for envoy-gateway:
//  1. Data-plane access logs (container=envoy, high volume, JSON)
//  2. Control-plane reconcile logs (container=envoy-gateway, sparse, zap tab-separated)
func (c *Construct) emitLogs(
	cluster string,
	now time.Time,
	controlMaps []map[string]string,
	dataMaps []map[string]string,
) []loki.Stream {
	var streams []loki.Stream

	// ── Base stream labels shared by both surfaces ────────────────────────────

	baseLabels := map[string]string{
		"cluster":            cluster,
		"k8s_cluster_name":   cluster,
		"k8s_namespace_name": "envoy-gateway-system",
		"service_name":       "envoy-gateway",
		"service_namespace":  "envoy-gateway-system",
	}

	// ── Surface 1: Data-plane access logs (container=envoy) ───────────────────
	//
	// Recon (svc-group-b.md §3.C): JSON access log, high volume, log_iostream=stdout.
	// route_name and upstream_cluster are HIGH-CARD — body only, never stream labels.
	// Number of log lines per tick: 3–6 (deterministic via tick second modulo).
	numLines := 3 + int(now.Unix()%4) // 3–6 lines

	// Determine proxy pod name: first data-plane pod, or synthetic fallback.
	proxyPodName := synthProxyPodName(0)
	proxyDeployName := "envoy-default-eg-proxy"
	if len(dataMaps) > 0 {
		if v, ok := dataMaps[0]["pod"]; ok && v != "" {
			proxyPodName = v
		}
		// Deployment name is the workload name (strip per-pod suffix from pod name heuristic).
		// We use the known fixture name directly.
	}

	dataLabels := cloneStringMap(baseLabels)
	dataLabels["k8s_container_name"] = "envoy"
	dataLabels["k8s_pod_name"] = proxyPodName
	dataLabels["k8s_deployment_name"] = proxyDeployName
	dataLabels["log_iostream"] = "stdout"
	dataLabels["detected_level"] = "unknown" // access logs — no level field; Alloy detects "unknown"

	var accessLines []loki.Line
	for i := 0; i < numLines; i++ {
		idx := int(now.Unix())%len(accessLogMethods) + i
		method := accessLogMethods[idx%len(accessLogMethods)]
		path := accessLogPaths[idx%len(accessLogPaths)]
		routeName := accessLogRouteNames[idx%len(accessLogRouteNames)]
		upstreamCluster := accessLogUpstreamClusters[idx%len(accessLogUpstreamClusters)]
		responseCode := accessLogResponseCodes[idx%len(accessLogResponseCodes)]
		duration := 2 + (idx%20)*3 // 2–61 ms, deterministic
		bytesSent := 200 + idx*13
		bytesReceived := 100 + idx*7
		xForwardedFor := fmt.Sprintf("10.0.%d.%d", idx%256, (idx*7)%256)
		userAgent := "Mozilla/5.0 (compatible; synthetic-client/1.0)"
		startTime := now.Add(-time.Duration(duration) * time.Millisecond).UTC().Format(time.RFC3339Nano)

		body := fmt.Sprintf(
			`{"start_time":%q,"method":%q,"path":%q,"protocol":"HTTP/2","response_code":%d,"duration":%d,"upstream_cluster":%q,"route_name":%q,"x_forwarded_for":%q,"user_agent":%q,"bytes_sent":%d,"bytes_received":%d}`,
			startTime, method, path, responseCode, duration, upstreamCluster, routeName, xForwardedFor, userAgent, bytesSent, bytesReceived,
		)
		accessLines = append(accessLines, loki.Line{T: now, Body: body})
	}

	streams = append(streams, loki.Stream{
		Labels: dataLabels,
		Lines:  accessLines,
	})

	// ── Surface 2: Control-plane reconcile logs (container=envoy-gateway) ────
	//
	// Recon (svc-group-b.md §3.C): zap tab-separated format, sparse (~1-2 lines/tick).
	// Format: <unix_float_ts>\t<level>\t<logger>\t<caller>\t<message>\t{json fields}
	// log_iostream=stdout per live recon (control-plane envoy-gateway writes to stdout).

	controllerPodName := synthControllerPodName()
	if len(controlMaps) > 0 {
		if v, ok := controlMaps[0]["pod"]; ok && v != "" {
			controllerPodName = v
		}
	}

	// Pick 1–2 log messages per tick deterministically.
	tickIdx := int(now.Unix() / 60) // changes each tick
	msg0 := controlPlaneLogMessages[tickIdx%len(controlPlaneLogMessages)]
	msg1 := controlPlaneLogMessages[(tickIdx+1)%len(controlPlaneLogMessages)]
	msgs := []struct {
		level   string
		logger  string
		message string
	}{msg0, msg1}

	unixFloat := float64(now.UnixNano()) / 1e9

	for _, msg := range msgs {
		cpLabels := cloneStringMap(baseLabels)
		cpLabels["k8s_container_name"] = "envoy-gateway"
		cpLabels["k8s_pod_name"] = controllerPodName
		cpLabels["k8s_deployment_name"] = "envoy-gateway"
		cpLabels["log_iostream"] = "stdout"
		cpLabels["detected_level"] = msg.level

		body := fmt.Sprintf("%.9e\t%s\t%s\tv3/simple.go:693\t%s\t{}",
			unixFloat, msg.level, msg.logger, msg.message)

		streams = append(streams, loki.Stream{
			Labels: cpLabels,
			Lines:  []loki.Line{{T: now, Body: body}},
		})
	}

	return streams
}

// cloneStringMap returns a shallow copy of m. Used for Loki stream label maps.
func cloneStringMap(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// ─── topology helper ──────────────────────────────────────────────────────────

// dataPlaneTopology derives the node-topology labels for the i-th data-plane pod.
// The live recon shows: availability_zone, instance_type, nodepool, os, region, architecture.
// We only stamp the three labels asserted by the tests; the others are added too for realism.
func dataPlaneTopology(cl *fixture.Cluster, podIdx int) map[string]string {
	// Determine the node this pod is placed on.
	wl, ok := k8saddon.LookupSubstrateWorkload(cl, "envoy-default-eg-proxy")
	if !ok || podIdx >= len(wl.NodeIdx) {
		return defaultTopology()
	}
	nodeIdx := wl.NodeIdx[podIdx]
	if nodeIdx < 0 || nodeIdx >= len(cl.Nodes) {
		return defaultTopology()
	}
	node := cl.Nodes[nodeIdx]

	// availability_zone: derive from the cluster region + a suffix based on pod index.
	az := cl.Region
	if cl.Region == "" {
		az = "eu-west-1a"
	} else {
		// Distribute across 1a/1b based on pod index.
		suffix := []string{"a", "b"}
		az = cl.Region + suffix[podIdx%len(suffix)]
	}

	instanceType := node.InstanceType
	if instanceType == "" {
		instanceType = "t4g.large"
	}

	nodepool := node.NodeGroup
	if nodepool == "" {
		nodepool = "default"
	}

	return map[string]string{
		"availability_zone": az,
		"instance_type":     instanceType,
		"nodepool":          nodepool,
		"os":                "linux",
		"architecture":      "arm64",
	}
}

// defaultTopology returns a stable topology label set when pod placement is unavailable.
func defaultTopology() map[string]string {
	return map[string]string{
		"availability_zone": "eu-west-1a",
		"instance_type":     "t4g.large",
		"nodepool":          "default",
		"os":                "linux",
		"architecture":      "arm64",
	}
}

// ─── enums ────────────────────────────────────────────────────────────────────

// watchableRunners are the runner values in watchable_event_total / watchable_publish_total
// (live recon svc-group-b.md §3.A).
var watchableRunners = []string{
	"gateway-api",
	"infrastructure",
	"xds",
}

// watchableEventTypes are the event_type values in watchable_event_total.
var watchableEventTypes = []string{"update", "add", "delete"}

// resourceKinds are the k8s resource kinds in resource_apply_total (recon §3.A).
var resourceKinds = []string{
	"ConfigMap",
	"Deployment",
	"PDB",
	"Service",
	"ServiceAccount",
}

// reconcileResults are the result values for controller_runtime_reconcile_total.
var reconcileResults = []string{"success", "error", "requeue", "requeue_after"}

// controlWorkqueues are the workqueue names in the control-plane process.
var controlWorkqueues = []string{
	"gateway-api",
	"infrastructure",
	"xds",
}

// envoyClusterNames are the upstream cluster names the Envoy proxy is configured with
// (live recon svc-group-b.md §3.A, envoy_cluster_upstream_rq_total labels).
var envoyClusterNames = []string{
	"httproute/otel-demo/frontend/rule/0",
	"prometheus_stats",
	"tracing",
	"xds_cluster",
}

// httpConnManagers are the envoy_http_conn_manager_prefix values (recon §3.A).
var httpConnManagers = []string{
	"admin",
	"eg-ready-http",
	"listener_0_0_0_0_10443",
}

// responseCodeClasses are the envoy_response_code_class values (recon §3.A, string).
var responseCodeClasses = []string{"1", "2", "3", "4", "5"}

// listenerAddresses are the envoy_listener_address values (recon §3.A).
var listenerAddresses = []string{
	"0.0.0.0_10443",
	"0.0.0.0_19001",
	"0.0.0.0_19003",
}

// ─── histogram bucket sets ────────────────────────────────────────────────────

// envoyMsBuckets are the millisecond histogram bucket boundaries for Envoy _time metrics
// (live recon svc-group-b.md §3.A: "milliseconds" le set).
// le=0.5 is the first finite bucket (confirms ms scale, not seconds).
var envoyMsBuckets = []float64{
	0.5, 1, 5, 10, 25, 50, 100, 250, 500, 1000, 2500, 5000,
	10000, 30000, 60000, 300000, 600000, 1.8e6, 3.6e6,
}

// secondsBuckets are standard Prometheus client default second-scale buckets
// used by controller_runtime_* / xds_stream_duration_seconds (these are in SECONDS).
var secondsBuckets = []float64{
	0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10,
}

// ─── utility ──────────────────────────────────────────────────────────────────

// mergeLabels merges b on top of a into a new map. Neither input is mutated.
func mergeLabels(a, b map[string]string) map[string]string {
	if b == nil {
		out := make(map[string]string, len(a))
		for k, v := range a {
			out[k] = v
		}
		return out
	}
	out := make(map[string]string, len(a)+len(b))
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}
