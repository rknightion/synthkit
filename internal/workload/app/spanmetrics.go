// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/semconv"
	"github.com/rknightion/synthkit/internal/state"
)

// apmLatencyBuckets are the classic histogram bounds for the synthesized spanmetric/service-graph
// latency families — identical to web_service so a dashboard built against either ports (the
// metrics-generator-equivalent schema).
var apmLatencyBuckets = []float64{
	0.0, 0.005, 0.01, 0.025, 0.05, 0.075, 0.1, 0.25, 0.5, 0.75, 1.0, 2.5, 5.0, 7.5, 10.0,
}

const (
	statusCodeOK    = "STATUS_CODE_OK"
	statusCodeError = "STATUS_CODE_ERROR"
	spanKindServer  = "SPAN_KIND_SERVER"
	spanKindClient  = "SPAN_KIND_CLIENT"
	tempoSource     = "tempo"

	// spanMetricErrFrac is the synthesized error fraction of calls (the OK/ERROR split on
	// calls_total + the service-graph failed edge), matching web_service's ~1% baseline.
	spanMetricErrFrac = 0.01
)

// tickSpanMetrics synthesizes the metrics-generator-DERIVED RED + service-graph families
// (traces_spanmetrics_* / traces_service_graph_*) from the app's OWN graph topology — ONLY when the
// per-blueprint EmitSpanMetrics opt-in is set (default off → Tempo's metrics-generator derives them
// from the emitted traces). This is the REQUIRED parity with web_service (Spec 4's dashgen.Derive
// runs with EmitSpanMetrics:true to pull these families into the dashboard manifest). Names, label
// schema, buckets, le-style and the native+classic dual emission match web_service so dashboards
// port unchanged. Volume is derived from the entry traffic envelope × the diurnal shape; exemplars
// are off by default (sourced from real ledger samples only — opt-in, like web_service).
func (w *Workload) tickSpanMetrics(now time.Time, world *core.World) {
	calls := w.spanMetricVolume(now, world)
	if calls <= 0 {
		return
	}
	latVal := func() float64 { return 0.02 + world.Shape.Float64()*0.18 } // 20–200ms typical APM latency

	okCalls := float64(calls) * (1 - spanMetricErrFrac)
	errCalls := float64(calls) * spanMetricErrFrac

	// SERVER rows: one per instrumented node (the entry + every serverSpan node). Both status_code
	// rows are emitted (OK + ERROR — the APM RED error dimension), matching web_service.
	for _, n := range w.graph.nodes {
		if !n.kind.serverSpan && n != w.graph.entry {
			continue // db/cache leaves have no SERVER span
		}
		id := w.identity(n)
		base := id.spanMetricBase()
		w.observeSpanCallsRow(base, spanKindServer, n.decl.Name, statusCodeOK, okCalls, id.sdkLang())
		w.observeSpanCallsRow(base, spanKindServer, n.decl.Name, statusCodeError, errCalls, id.sdkLang())
		w.st.ObserveDual("traces_spanmetrics_latency", spanLabels(base, spanKindServer, n.decl.Name, statusCodeOK),
			apmLatencyBuckets, state.LEDotZero, state.NativeSchemaSpanMetrics, latVal())
	}

	// CLIENT + service-graph rows: one per edge (caller → callee).
	for _, caller := range w.graph.nodes {
		callerID := w.identity(caller)
		for _, calleeName := range caller.decl.Calls {
			callee := w.graph.byName[calleeName]
			if callee == nil {
				continue
			}
			calleeID := w.identity(callee)
			// CLIENT span row (the caller's view of the call).
			cbase := callerID.spanMetricBase()
			clientName := "call " + calleeName
			w.observeSpanCallsRow(cbase, spanKindClient, clientName, statusCodeOK, float64(calls), callerID.sdkLang())
			w.st.ObserveDual("traces_spanmetrics_latency", spanLabels(cbase, spanKindClient, clientName, statusCodeOK),
				apmLatencyBuckets, state.LEDotZero, state.NativeSchemaSpanMetrics, latVal())
			// service-graph edge (incl the failed-edge counter that drives edge error-rate panels).
			// A db/cache leaf edge carries connection_type=database (mirrors web_service
			// tickServiceGraph); instrumented service + AI (HTTP/gRPC) edges keep "".
			connType := ""
			if callee.kind.leaf {
				connType = "database"
			}
			sg := sgLabels(callerID, calleeID, connType)
			w.st.Add("traces_service_graph_request_total", sg, float64(calls))
			w.st.Add("traces_service_graph_request_failed_total", sg, errCalls)
			w.st.ObserveDual("traces_service_graph_request_server_seconds", sg, apmLatencyBuckets, state.LEDotZero, state.NativeSchemaSpanMetrics, latVal())
			w.st.ObserveDual("traces_service_graph_request_client_seconds", sg, apmLatencyBuckets, state.LEDotZero, state.NativeSchemaSpanMetrics, latVal())
		}
	}
}

// spanMetricVolume is the per-tick call count derived from the entry traffic envelope × the diurnal
// shape (the aggregate metric VOLUME, separate from the correlated narrative sample).
func (w *Workload) spanMetricVolume(now time.Time, world *core.World) int {
	rps := w.cfg.Traffic.OffPeakRPS + (w.cfg.Traffic.PeakRPS-w.cfg.Traffic.OffPeakRPS)*clampF(world.Shape.BusinessFactor(now), 0, 1)
	calls := int(rps * interval.Seconds())
	if calls > 5000 {
		calls = 5000 // bounded per-tick budget (cumulative state carries the rest)
	}
	return calls
}

// observeSpanCallsRow adds a traces_spanmetrics_calls_total row (with telemetry_sdk_language) and a
// traces_spanmetrics_size_total row (without it) — matching the web_service trap that the language
// label is on calls_total/size... (size omits it; calls includes it).
func (w *Workload) observeSpanCallsRow(base map[string]string, kind, name, status string, n float64, lang string) {
	calls := spanLabels(base, kind, name, status)
	calls["telemetry_sdk_language"] = lang
	w.st.Add("traces_spanmetrics_calls_total", calls, n)
	w.st.Add("traces_spanmetrics_size_total", spanLabels(base, kind, name, status), n*256)
}

// spanLabels stamps span_kind/span_name/status_code onto a copy of the node's span-metric base.
func spanLabels(base map[string]string, kind, name, status string) map[string]string {
	b := make(map[string]string, len(base)+3)
	for k, v := range base {
		b[k] = v
	}
	b["span_kind"] = kind
	b["span_name"] = name
	b["status_code"] = status
	return b
}

// spanMetricBase is the node-identity label set for the spanmetric/service-graph families (the
// source="tempo" APM view — mirrors web_service spanMetricBase). deployment_environment_name
// is the standard form per the OTEL semconv (clean cutover, legacy dropped).
func (id nodeIdentity) spanMetricBase() map[string]string {
	return pruneEmpty(map[string]string{
		"service":                              id.service,
		"service_name":                         id.service,
		semconv.LabelDeploymentEnvironmentName: id.env,
		"service_namespace":                    id.namespace,
		"namespace":                            id.namespace,
		"k8s_namespace_name":                   id.namespace,
		"service_version":                      id.version,
		"cluster":                              id.cluster,
		"k8s_cluster_name":                     id.cluster,
		"job":                                  id.job(),
		"source":                               tempoSource,
		semconv.LabelContext:                   id.context,
		semconv.LabelUseCase:                   id.useCase,
	})
}

func (id nodeIdentity) sdkLang() string {
	if l := sdkLanguage(id.runtime); l != "" {
		return l
	}
	return "go"
}

// sgLabels builds the service-graph edge labels (client/server identity prefixes + the shared
// namespace/cluster/job/source), mirroring web_service sgLabels. connType is the edge's
// connection_type (a real metrics-generator dimension): "" for instrumented-service/AI edges,
// "database" for a db/cache leaf edge. An empty connection_type is a REAL dimension (not pruned).
func sgLabels(client, server nodeIdentity, connType string) map[string]string {
	l := map[string]string{
		"client":          client.service,
		"server":          server.service,
		"connection_type": connType,
		"source":          tempoSource,
	}
	addPrefixed(l, "client_", client)
	addPrefixed(l, "server_", server)
	// shared substrate identity (the server's, by convention) — match web_service's sgLabels,
	// incl the unprefixed service + k8s_cluster_name edge joins (review H2).
	l["service"] = server.service
	l["namespace"] = server.namespace
	l["cluster"] = server.cluster
	l["k8s_cluster_name"] = server.cluster
	l["job"] = server.job()
	conn := l["connection_type"]
	pruneEmpty(l)
	l["connection_type"] = conn // re-set after prune (an empty connection_type is a real dimension)
	return l
}

func addPrefixed(l map[string]string, prefix string, id nodeIdentity) {
	for k, v := range map[string]string{
		"cluster":                              id.cluster,
		semconv.LabelDeploymentEnvironmentName: id.env,
		"k8s_cluster_name":                     id.cluster,
		"k8s_namespace_name":                   id.namespace,
		"service_namespace":                    id.namespace,
		"service_version":                      id.version,
	} {
		if v != "" {
			l[prefix+k] = v
		}
	}
}
