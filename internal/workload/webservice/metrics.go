// SPDX-License-Identifier: AGPL-3.0-only

package webservice

import (
	"context"
	"sort"
	"time"

	"github.com/rknightion/synthkit/internal/beyla"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/genai"
	"github.com/rknightion/synthkit/internal/ledger"
	"github.com/rknightion/synthkit/internal/semconv"
	"github.com/rknightion/synthkit/internal/shape"
	"github.com/rknightion/synthkit/internal/state"
)

// apmLatencyBuckets is the pinned `le` boundary set for the APM latency + service-graph
// seconds histograms (signals/apm.md [slug: apm-latency]; predecessor canon.APMLatencySecondsBuckets — empirically
// verified, seconds despite no unit suffix; note the leading 0.0 boundary is real). The
// state layer appends the implicit +Inf bucket.
//
// WIRE-FORM NOTE: signals/apm.md [slug: apm-latency] (SK-28, live-verified 2026-06-13) records
// that a real Tempo metrics-generator emits BOTH a native histogram (bare name, no `le`) AND
// the classic _bucket{le}/_sum/_count series for traces_spanmetrics_latency and the
// service-graph seconds metrics. synthkit matches that: these three series are observed via
// state.ObserveDual (classic bounds below + native schema state.NativeSchemaSpanMetrics), so the
// same observation stream produces both forms. apmLatencyBuckets is the CLASSIC bound set; the
// native form uses the exponential schema. gen_ai_* and every other histogram stay classic.
var apmLatencyBuckets = []float64{
	0.0, 0.005, 0.01, 0.025, 0.05, 0.075, 0.1, 0.25, 0.5, 0.75, 1.0, 2.5, 5.0, 7.5, 10.0,
}

// statusCodeOK / statusCodeError / statusCodeUnset are the span-metrics status_code enum
// values (OTLP→Prometheus mangled).
const (
	statusCodeOK    = "STATUS_CODE_OK"
	statusCodeError = "STATUS_CODE_ERROR"
	statusCodeUnset = "STATUS_CODE_UNSET"
)

// span-kind enum values.
const (
	spanKindServer = "SPAN_KIND_SERVER"
	spanKindClient = "SPAN_KIND_CLIENT"
)

// connTypeDatabase is the service-graph connection_type for db/cache edges.
const connTypeDatabase = "database"

// Tick is the metric lane (span-metrics + service-graph + target_info). It reads
// w.Ledger.ActiveFor(name, now, Interval) for narrative consistency (so the metric story
// references the same active requests) BUT the emitted VOLUME comes from the traffic
// config (rps × shape × interval), mirroring the predecessor's app_apm.go which separates
// aggregate metric volume from the correlated ledger sample. Counters/histograms are
// cumulative across ticks via state.Add/Observe (I3).
func (w *Workload) Tick(ctx context.Context, now time.Time, world *core.World) error {
	if world.Metrics == nil {
		return nil
	}

	// Beyla observation lane (additive emission switch). The lane's tick is internally gated
	// by beyla.Emission(ctx): ebpf_only emits the full Beyla surface (RED + span/service-graph
	// source="beyla" + network + target_info + traces) and OWNS the metric story for this
	// service — so we return BEFORE the existing source="tempo" emission (Beyla replaces it).
	// coexist_sdk emits ONLY the additive network/target_info/avoided footprint, then falls
	// through to the unchanged tempo + exemplar lane below (the SDK still owns RED/span/traces).
	if w.cfg.beylaObserved() {
		w.beylaLane().tick(now, world)
		if w.cfg.beylaContext() == beyla.ContextEBPFOnly {
			return world.Metrics.Write(ctx, w.st.Collect(now))
		}
	}

	intervalSec := interval.Seconds()
	// Aggregate request VOLUME this tick from the traffic config (NOT the ledger sample):
	// expected = rps(now) × intervalSec, stochastically rounded. The shape factor is
	// already folded into rps(now) via the minter's interpolation.
	rps := w.m.rpsAt(now, world.Shape)
	totalCalls := ledger.StochasticRound(rps*intervalSec, world.Shape.Float64())
	if totalCalls <= 0 && !w.nonProd {
		totalCalls = 1 // keep prod series alive across troughs
	}

	// Narrative consistency: the active sample informs which routes are "live" so the metric
	// label sets line up with the trace sample. Fall back to config when the ledger is empty.
	routes := w.activeRoutes(now, world)

	// Native OTLP application-metrics lane (http.server.*). Placed AFTER routes is computed
	// so ebpf_only services (which returned early above) do not also emit SDK app metrics;
	// coexist_sdk falls through to here and emits, which is correct.
	if world.OTLPMetrics != nil {
		if err := w.tickOTLPMetrics(ctx, now, world, totalCalls, routes); err != nil {
			return err
		}
	}

	// Sample a few real ledger requests (real TraceIDs that ProjectBatch already shipped as
	// spans) to fold into the otherwise-synthetic histograms/counters as exemplars (so a metric
	// exemplar in Grafana clicks through to a trace that actually exists in Tempo).
	samples := w.sampleRequests(now, world)

	// world.EmitSpanMetrics gates synthkit's OWN backend spanmetrics + service-graph. It is set
	// per-tick by the runner from the per-blueprint control opt-in (control.State.SpanMetricsBlueprints,
	// default OFF). OFF ⇒ defer to Grafana Cloud metrics-generator / beyla (which then also own those
	// families' exemplars). gen_ai is NOT gated — metrics-generator/beyla never produce it.
	if world.EmitSpanMetrics {
		w.tickSpanMetrics(now, totalCalls, routes, world.Shape, samples)
		// The service graph reads the stable resolved call TEMPLATE (w.m.calls): ParentHop is an
		// index into it, so nested AI chains (e.g. llm via gateway) emit the correct parent→child
		// edges. (Per-request hop sets are identical to the template — the minter mints it whole.)
		w.tickServiceGraph(now, totalCalls, w.m.calls, world.Shape, samples)
	}
	w.tickGenAIMetrics(now, totalCalls, world.Shape, samples)
	w.tickTargetInfo(now)

	if err := world.Metrics.Write(ctx, w.st.Collect(now)); err != nil {
		return err
	}
	w.tickProfiles(ctx, now, world)
	return nil
}

// activeRoutes returns the distinct routes seen in the active ledger window, or the
// configured endpoints' routes when the window is empty.
func (w *Workload) activeRoutes(now time.Time, world *core.World) []routeStat {
	stats := map[string]*routeStat{}
	if world.Ledger != nil {
		for _, r := range world.Ledger.ActiveFor(w.name, now, interval) {
			rs := stats[r.Route]
			if rs == nil {
				rs = &routeStat{route: r.Route}
				stats[r.Route] = rs
			}
			rs.total++
			if r.Outcome != ledger.OutcomeSuccess {
				rs.errors++
			}
		}
	}
	if len(stats) == 0 {
		out := make([]routeStat, 0, len(w.cfg.Endpoints))
		for _, ep := range w.cfg.Endpoints {
			out = append(out, routeStat{route: ep.Route, errorRate: ep.ErrorRate})
		}
		return out
	}
	out := make([]routeStat, 0, len(stats))
	for _, rs := range stats {
		if rs.total > 0 {
			rs.errorRate = float64(rs.errors) / float64(rs.total)
		}
		out = append(out, *rs)
	}
	return out
}

// routeStat is the per-route aggregate used to split metric volume across routes.
type routeStat struct {
	route     string
	total     int
	errors    int
	errorRate float64
}

// maxExemplars bounds how many real-request exemplars we fold into a single series per tick
// (also backstopped by state.MaxExemplarsPerSeries). sampleBudget bounds how many recent
// requests we scan/sort for sourcing.
const (
	maxExemplars = 5
	sampleBudget = 50
)

// reqSample is a real ledger request reduced to what the metric lane needs to emit an
// exemplar: a trace_id that ProjectBatch already shipped as a span (so it exists in Tempo),
// the request's start time, and its route (so a route's latency bucket gets exemplars from
// requests on THAT route, not smeared across unrelated routes — M-3b).
type reqSample struct {
	traceID string
	start   time.Time
	route   string
}

// sampleRequests pulls the NEWEST real requests from this workload's ledger window for
// exemplar sourcing — newest because the metric tick (≥60s) reads a 60s window and the
// oldest entries are the ones most likely sampled-out / aged past Tempo retention (M-2).
// Empty when there is no ledger (constructs) or no recent traffic.
func (w *Workload) sampleRequests(now time.Time, world *core.World) []reqSample {
	if world.Ledger == nil {
		return nil
	}
	reqs := world.Ledger.ActiveFor(w.name, now, interval)
	out := make([]reqSample, 0, len(reqs))
	for _, r := range reqs {
		if r.TraceID == "" {
			continue
		}
		out = append(out, reqSample{traceID: r.TraceID, start: r.Start, route: r.Route})
	}
	// newest first; ActiveFor returns ring order (oldest-first), so sort by Start desc.
	sort.Slice(out, func(i, j int) bool { return out[i].start.After(out[j].start) })
	if len(out) > sampleBudget {
		out = out[:sampleBudget]
	}
	return out
}

// samplesForRoute returns the subset of samples whose route matches (M-3b). Pass route=""
// to accept all (used for client-hop + gen_ai series, which every request traverses, so any
// request's trace is a valid exemplar there).
func samplesForRoute(samples []reqSample, route string) []reqSample {
	if route == "" {
		return samples
	}
	out := make([]reqSample, 0, len(samples))
	for _, s := range samples {
		if s.route == route {
			out = append(out, s)
		}
	}
	return out
}

// observeWithExemplars records n histogram observations of values drawn by valFn; the first
// min(len(samples), maxExemplars) observations additionally carry a real {trace_id} exemplar
// (value = that observation's own value — the Prometheus exemplar contract). The remaining
// observations are plain. Keeps total volume == n while marking a few observations with real
// traces. Applies the per-call observation budget cap here so ALL callers inherit it.
// When dual is true, each observation is fed into BOTH the classic histogram (bounds+style)
// AND a native exponential histogram (schema state.NativeSchemaSpanMetrics), matching the
// wire shape emitted by a real Tempo metrics-generator (SK-28). Use dual=true ONLY for the
// three span-histogram families; all other callers must pass dual=false.
func (w *Workload) observeWithExemplars(name string, labels map[string]string, n int, bounds []float64, style state.LEStyle, samples []reqSample, valFn func() float64, dual bool) {
	if n <= 0 {
		return
	}
	if n > 200 {
		n = 200 // preserve the prior observeLatency per-call budget
	}
	k := len(samples)
	if k > maxExemplars {
		k = maxExemplars
	}
	if k > n {
		k = n
	}
	for j := 0; j < n; j++ {
		v := valFn()
		if j < k {
			if dual {
				w.st.ObserveDualExemplar(name, labels, bounds, style, state.NativeSchemaSpanMetrics, v, map[string]string{"trace_id": samples[j].traceID}, samples[j].start)
			} else {
				w.st.ObserveExemplar(name, labels, bounds, style, v, map[string]string{"trace_id": samples[j].traceID}, samples[j].start)
			}
		} else {
			if dual {
				w.st.ObserveDual(name, labels, bounds, style, state.NativeSchemaSpanMetrics, v)
			} else {
				w.st.Observe(name, labels, bounds, style, v)
			}
		}
	}
}

// spanMetricBase returns the universal span-metrics label set for the backend service.
// service + service_name dual, cluster + k8s_cluster_name dual, deployment_environment_name
// (semconv standard label; _name form everywhere per the OTEL semconv decision),
// namespace + service_namespace + k8s_namespace_name, job, source=tempo. §5 canon dims
// (context/use_case) are included when non-empty (pruned per I13). Each call returns an
// independent map (no aliasing). Empty-valued identity dimensions (e.g. cluster when the
// workload has no placement) are OMITTED, never sentinelled (I13).
func (w *Workload) spanMetricBase() map[string]string {
	return pruneEmpty(map[string]string{
		"service":                              w.name,
		semconv.LabelServiceName:               w.name,
		semconv.LabelDeploymentEnvironmentName: w.env,
		semconv.LabelServiceNamespace:          w.namespace,
		"namespace":                            w.namespace,
		"k8s_namespace_name":                   w.namespace,
		semconv.LabelServiceVersion:            w.version,
		"cluster":                              w.cluster,
		"k8s_cluster_name":                     w.cluster,
		"job":                                  w.job(),
		"source":                               source,
		semconv.LabelContext:                   w.context,
		semconv.LabelUseCase:                   w.useCase,
	})
}

// pruneEmpty drops keys with empty values so an absent dimension is OMITTED, never emitted
// as "" or "NA" (ARCHITECTURE I13). Returns the same map for chaining.
func pruneEmpty(m map[string]string) map[string]string {
	for k, v := range m {
		if v == "" {
			delete(m, k)
		}
	}
	return m
}

// tickSpanMetrics emits traces_spanmetrics_calls_total / _latency / _size_total for the
// backend SERVER span and one CLIENT row per downstream hop. Volume is split across routes.
func (w *Workload) tickSpanMetrics(now time.Time, totalCalls int, routes []routeStat, shp *shape.Engine, samples []reqSample) {
	if len(routes) == 0 {
		return
	}
	perRoute := float64(totalCalls) / float64(len(routes))
	latVal := func() float64 { return 0.02 + shp.Float64()*0.18 } // 20–200ms typical APM latency

	for _, rs := range routes {
		errFrac := rs.errorRate
		okFrac := 1 - errFrac

		// SERVER span row per status_code.
		serverName := rs.route
		w.emitCallsRow(spanKindServer, serverName, statusCodeOK, backendSDKLanguage, perRoute*okFrac)
		w.emitCallsRow(spanKindServer, serverName, statusCodeError, backendSDKLanguage, perRoute*errFrac)

		// latency + size for the SERVER span (no telemetry_sdk_language on these).
		// Exemplars from requests on THIS route only (M-3b: no cross-route smear).
		rsamples := samplesForRoute(samples, serverName)
		w.observeWithExemplars("traces_spanmetrics_latency", w.spanLabels(spanKindServer, serverName, statusCodeOK), int(perRoute), apmLatencyBuckets, state.LEDotZero, rsamples, latVal, true)
		w.addSize(spanKindServer, serverName, statusCodeOK, perRoute)
		// Counter exemplar: mark one representative real trace on the calls_total + size_total rows.
		// The exemplar VALUE (1) is nominal — Grafana links via the trace_id label, not the value,
		// and Mimir does not bucket counter exemplars; do NOT "fix" it to the real delta.
		if len(rsamples) > 0 {
			sl := w.spanLabels(spanKindServer, serverName, statusCodeOK)
			cll := w.spanLabels(spanKindServer, serverName, statusCodeOK)
			cll["telemetry_sdk_language"] = backendSDKLanguage
			w.st.CounterExemplar("traces_spanmetrics_calls_total", cll, map[string]string{"trace_id": rsamples[0].traceID}, 1, rsamples[0].start)
			w.st.CounterExemplar("traces_spanmetrics_size_total", sl, map[string]string{"trace_id": rsamples[0].traceID}, 1, rsamples[0].start)
		}
	}

	// CLIENT rows for downstream db/cache hops (one span_name per hop). Every request
	// traverses the hop template, so client-hop latency uses ALL samples (no route filter).
	for _, cs := range w.m.calls {
		clientName := clientSpanName(cs)
		w.emitCallsRow(spanKindClient, clientName, statusCodeOK, backendSDKLanguage, float64(totalCalls))
		w.observeWithExemplars("traces_spanmetrics_latency", w.spanLabels(spanKindClient, clientName, statusCodeOK), totalCalls, apmLatencyBuckets, state.LEDotZero, samples, latVal, true)
		w.addSize(spanKindClient, clientName, statusCodeOK, float64(totalCalls))
		if len(samples) > 0 {
			sl := w.spanLabels(spanKindClient, clientName, statusCodeOK)
			cll := w.spanLabels(spanKindClient, clientName, statusCodeOK)
			cll["telemetry_sdk_language"] = backendSDKLanguage
			w.st.CounterExemplar("traces_spanmetrics_calls_total", cll, map[string]string{"trace_id": samples[0].traceID}, 1, samples[0].start)
			w.st.CounterExemplar("traces_spanmetrics_size_total", sl, map[string]string{"trace_id": samples[0].traceID}, 1, samples[0].start)
		}
	}
}

// spanLabels returns the span-metrics label set WITHOUT telemetry_sdk_language (latency +
// size series; trap §8 — that label is on calls_total only).
func (w *Workload) spanLabels(kind, name, status string) map[string]string {
	b := w.spanMetricBase()
	b["span_kind"] = kind
	b["span_name"] = name
	b["status_code"] = status
	return b
}

// emitCallsRow adds to traces_spanmetrics_calls_total with telemetry_sdk_language.
func (w *Workload) emitCallsRow(kind, name, status, sdkLang string, delta float64) {
	if delta <= 0 {
		return
	}
	lbls := w.spanLabels(kind, name, status)
	lbls["telemetry_sdk_language"] = sdkLang
	w.st.Add("traces_spanmetrics_calls_total", lbls, delta)
}

// addSize adds to traces_spanmetrics_size_total (bytes; same labels as latency, no sdk lang).
func (w *Workload) addSize(kind, name, status string, calls float64) {
	if calls <= 0 {
		return
	}
	const avgSpanBytes = 512.0
	w.st.Add("traces_spanmetrics_size_total", w.spanLabels(kind, name, status), calls*avgSpanBytes)
}

// tickServiceGraph emits the directed-edge service-graph counters + seconds histograms.
// Edges: browser→service (when RUM), service→db/cache per call (connection_type=database).
// Promoted dims are prefixed client_/server_ — there is NO bare promoted dim and NO bare
// `blueprint` (the scoped writer's selector lands as client_blueprint/server_blueprint
// via the metrics generator; we never emit it here). instance is omitted.
func (w *Workload) tickServiceGraph(now time.Time, totalReqs int, calls []callSpec, shp *shape.Engine, samples []reqSample) {
	if totalReqs <= 0 {
		return
	}
	// browser→service edge when RUM is enabled (the front door of the golden thread).
	if w.rumEnabled() {
		w.emitServiceGraphEdge(now, w.browserServiceName(), frontendNamespace, w.name, w.namespace, "", float64(totalReqs), shp, samples)
	}
	// downstream edges, honoring ParentHop so nested chains emit parent→child (matching the
	// trace tree). Edge client = the parent hop's target (or the backend workload if -1).
	// db/cache hops are connection_type=database; service + AI (HTTP/gRPC) hops carry the
	// empty connection_type "" (signals/apm.md: connection_type ∈ {"",database,virtual_node}).
	for _, cs := range calls {
		connType := connTypeDatabase
		if cs.Kind == "service" || cs.AI != nil {
			connType = ""
		}
		client, clientNS := w.name, w.namespace
		if cs.ParentHop >= 0 && cs.ParentHop < len(calls) {
			p := calls[cs.ParentHop]
			client, clientNS = p.Target, p.Target // the parent peer node (same-cluster model)
		}
		w.emitServiceGraphEdge(now, client, clientNS, cs.Target, cs.Target, connType, float64(totalReqs), shp, samples)
	}
}

// tickGenAIMetrics emits the workload's gen_ai_client_*/gen_ai_server_* series from its AI
// hops (token usage, operation duration, TTFC; gateway hops add the server-side pair). Names
// + buckets + label keys come from internal/genai; histograms use the OTLP→Prom le form
// (LEDotZero). Volume scales with the aggregate call count (bounded observation budget). The
// OTel metrics SDK is NOT used — these go via promrw with the final names (the ban holds).
func (w *Workload) tickGenAIMetrics(now time.Time, totalCalls int, shp *shape.Engine, samples []reqSample) {
	n := totalCalls
	if n <= 0 {
		return
	}
	if n > 100 {
		n = 100 // bounded observation budget per tick (cumulative state carries the rest)
	}
	// gen_ai uses ALL samples (every request traverses the AI hops, so any request's trace is
	// a valid exemplar on every gen_ai series — no route filter).
	for _, cs := range w.m.calls {
		if cs.AI == nil {
			continue
		}
		// gen_ai.client.operation.duration — every AI operation.
		opLabels := w.genAILabels(cs)
		w.observeWithExemplars(genai.MetricClientOpDuration, opLabels, n, genai.OpDurationBuckets, state.LEDotZero, samples, func() float64 { return 0.2 + shp.Float64()*2.0 }, false)
		// Inference hops (model present): token usage (input+output) + time-to-first-chunk.
		if cs.AI.Model != "" {
			inLabels := w.genAILabels(cs)
			inLabels[genai.LabelTokenType] = genai.TokenTypeInput
			outLabels := w.genAILabels(cs)
			outLabels[genai.LabelTokenType] = genai.TokenTypeOutput
			ttfcLabels := w.genAILabels(cs)
			tpocLabels := w.genAILabels(cs)
			w.observeWithExemplars(genai.MetricClientTokenUsage, inLabels, n, genai.TokenUsageBuckets, state.LEDotZero, samples, func() float64 { return 200 + shp.Float64()*1800 }, false)
			w.observeWithExemplars(genai.MetricClientTokenUsage, outLabels, n, genai.TokenUsageBuckets, state.LEDotZero, samples, func() float64 { return 60 + shp.Float64()*600 }, false)
			w.observeWithExemplars(genai.MetricClientTTFC, ttfcLabels, n, genai.OpDurationBuckets, state.LEDotZero, samples, func() float64 { return 0.05 + shp.Float64()*0.5 }, false)
			w.observeWithExemplars(genai.MetricClientTimePerOutputChunk, tpocLabels, n, genai.OpDurationBuckets, state.LEDotZero, samples, func() float64 { return 0.005 + shp.Float64()*0.05 }, false)
		}
		// Gateway hops: the connected gateway-as-server view (Path-B) — server-side timing.
		if cs.Kind == fixture.KindLLMGateway {
			srvLabels := w.genAILabels(cs)
			tpotLabels := w.genAILabels(cs)
			w.observeWithExemplars(genai.MetricServerRequestDuration, srvLabels, n, genai.OpDurationBuckets, state.LEDotZero, samples, func() float64 { return 0.2 + shp.Float64()*2.0 }, false)
			w.observeWithExemplars(genai.MetricServerTimeToFirstToken, srvLabels, n, genai.OpDurationBuckets, state.LEDotZero, samples, func() float64 { return 0.05 + shp.Float64()*0.5 }, false)
			w.observeWithExemplars(genai.MetricServerTimePerOutputToken, tpotLabels, n, genai.OpDurationBuckets, state.LEDotZero, samples, func() float64 { return 0.005 + shp.Float64()*0.05 }, false)
		}
	}
}

// genAILabels is the gen_ai metric label set for an AI hop (operation/provider/model). A
// fresh map per call so the per-series state clone is never aliased. Absent dims OMITTED (I13).
func (w *Workload) genAILabels(cs callSpec) map[string]string {
	// deployment_environment_name carries the workload's env (as on spanmetrics/service-graph) so
	// that per-env-fanned AI workloads (for_each_env) emit distinct gen_ai series instead of
	// byte-identical duplicates. Always present (a workload always has an env), matching the
	// span-metric convention. §5 canon dims (context/use_case) are included when non-empty.
	m := pruneEmpty(map[string]string{
		genai.LabelOperationName:               genaiOp(cs.AI),
		semconv.LabelDeploymentEnvironmentName: w.env,
		semconv.LabelContext:                   w.context,
		semconv.LabelUseCase:                   w.useCase,
	})
	if cs.AI.Provider != "" {
		m[genai.LabelProviderName] = cs.AI.Provider
	}
	if cs.AI.Model != "" {
		m[genai.LabelRequestModel] = cs.AI.Model
		m[genai.LabelResponseModel] = cs.AI.Model
	}
	return m
}

// sgBase returns the universal (non-prefixed) service-graph labels. connection_type is
// "" on plain service→service edges — it is a REAL service-graph value (the empty
// connection type), so it is kept rather than pruned; identity dims that are absent are
// pruned (I13).
func (w *Workload) sgLabels(client, clientNS, server, serverNS, connType string) map[string]string {
	m := map[string]string{
		"client":                             client,
		"server":                             server,
		"connection_type":                    connType,
		"client_cluster":                     w.cluster,
		"client_deployment_environment_name": w.env,
		"client_k8s_cluster_name":            w.cluster,
		"client_k8s_namespace_name":          clientNS,
		"client_service_namespace":           clientNS,
		"client_service_version":             w.version,
		"server_cluster":                     w.cluster,
		"server_deployment_environment_name": w.env,
		"server_k8s_cluster_name":            w.cluster,
		"server_k8s_namespace_name":          serverNS,
		"server_service_namespace":           serverNS,
		"server_service_version":             w.version,
		"namespace":                          clientNS, // convention: client-side namespace
		"service":                            client,   // convention: client service name
		"source":                             source,
		"cluster":                            w.cluster,
		"k8s_cluster_name":                   w.cluster,
		"job":                                clientNS + "/" + client,
	}
	// connection_type is a real service-graph value when empty ("" = plain RPC edge) — keep
	// it across the prune; all other absent identity dims are omitted (I13).
	ct := m["connection_type"]
	pruneEmpty(m)
	m["connection_type"] = ct
	return m
}

// emitServiceGraphEdge accumulates request/failed counters + server/client seconds. The
// seconds histograms carry real {trace_id} exemplars (every request traverses the edge, so
// all samples apply); the request counter gets one representative exemplar.
func (w *Workload) emitServiceGraphEdge(now time.Time, client, clientNS, server, serverNS, connType string, reqs float64, shp *shape.Engine, samples []reqSample) {
	lbls := w.sgLabels(client, clientNS, server, serverNS, connType)
	w.st.Add("traces_service_graph_request_total", lbls, reqs)
	if len(samples) > 0 {
		w.st.CounterExemplar("traces_service_graph_request_total", lbls, map[string]string{"trace_id": samples[0].traceID}, 1, samples[0].start)
	}
	// A small failed fraction per edge.
	failed := reqs * 0.01
	if failed > 0 {
		w.st.Add("traces_service_graph_request_failed_total", lbls, failed)
	}
	n := int(reqs)
	if n > 100 {
		n = 100
	}
	// The server-side draw is correlated with the client-side draw (+network overhead), so we
	// draw both per observation rather than via two independent observeWithExemplars passes.
	k := len(samples)
	if k > maxExemplars {
		k = maxExemplars
	}
	if k > n {
		k = n
	}
	for j := 0; j < n; j++ {
		serverSec := 0.02 + shp.Float64()*0.18
		clientSec := serverSec + (0.001 + shp.Float64()*0.019) // +1–20ms network overhead
		if j < k {
			ex := map[string]string{"trace_id": samples[j].traceID}
			w.st.ObserveDualExemplar("traces_service_graph_request_server_seconds", lbls, apmLatencyBuckets, state.LEDotZero, state.NativeSchemaSpanMetrics, serverSec, ex, samples[j].start)
			w.st.ObserveDualExemplar("traces_service_graph_request_client_seconds", lbls, apmLatencyBuckets, state.LEDotZero, state.NativeSchemaSpanMetrics, clientSec, ex, samples[j].start)
		} else {
			w.st.ObserveDual("traces_service_graph_request_server_seconds", lbls, apmLatencyBuckets, state.LEDotZero, state.NativeSchemaSpanMetrics, serverSec)
			w.st.ObserveDual("traces_service_graph_request_client_seconds", lbls, apmLatencyBuckets, state.LEDotZero, state.NativeSchemaSpanMetrics, clientSec)
		}
	}
}

// tickTargetInfo emits target_info and traces_target_info for the backend service (and
// the browser service when RUM is enabled). k8s_pod_name = the placement's pod 0 so the
// service→pod entity-graph join holds (I12 / extract §2.5).
func (w *Workload) tickTargetInfo(now time.Time) {
	w.st.Set("target_info", w.targetInfoLabels(false), 1)
	w.st.Set("traces_target_info", w.tracesTargetInfoLabels(false), 1)
	if w.rumEnabled() {
		w.st.Set("target_info", w.targetInfoLabels(true), 1)
		w.st.Set("traces_target_info", w.tracesTargetInfoLabels(true), 1)
	}
}

// targetInfoLabels: service_name ONLY (no bare service), deployment_environment_name
// (semconv standard; legacy deployment_environment DROPPED — clean cutover), k8s_pod_name +
// k8s_deployment_name, telemetry_sdk_language, k8s_node_name + service_instance_id (OMIT
// each when empty, I13). §5 canon dims (context/use_case/team) are included when
// non-empty (pruned per I13). source is NOT emitted here — live OTLP-path target_info has
// no source label; source="tempo" belongs only on span-metric/service-graph series.
func (w *Workload) targetInfoLabels(browser bool) map[string]string {
	svc, ns, pod, dep, lang := w.resourceIdentity(browser)
	// For the backend resource, node name and service_instance_id (= pod name) come from
	// the resolved placement. Browser resource has no node placement — omit both (I13).
	nodeName := ""
	instanceID := ""
	if !browser {
		nodeName = w.nodeName
		instanceID = w.podName
	}
	return pruneEmpty(map[string]string{
		semconv.LabelServiceName:               svc,
		semconv.LabelServiceNamespace:          ns,
		semconv.LabelServiceVersion:            w.version,
		semconv.LabelDeploymentEnvironmentName: w.env,
		semconv.LabelContext:                   w.context,
		semconv.LabelUseCase:                   w.useCase,
		semconv.LabelTeam:                      w.team,
		"k8s_cluster_name":                     w.cluster,
		"k8s_namespace_name":                   ns,
		"k8s_pod_name":                         pod,
		"k8s_deployment_name":                  dep,
		"k8s_node_name":                        nodeName,
		"service_instance_id":                  instanceID,
		"cluster":                              w.cluster,
		"job":                                  ns + "/" + svc,
		"telemetry_sdk_language":               lang,
	})
}

// tracesTargetInfoLabels = target_info + telemetry_sdk_name; the browser service also
// carries the Faro/FEO discovery labels.
func (w *Workload) tracesTargetInfoLabels(browser bool) map[string]string {
	lbls := w.targetInfoLabels(browser)
	if browser {
		lbls["telemetry_sdk_name"] = "opentelemetry"
		lbls["telemetry_distro_name"] = faroDistroName
		lbls["gf_feo11y_app_id"] = w.feoAppID
		lbls["gf_feo11y_app_name"] = w.browserServiceName()
		lbls["browser_platform"] = "Win32"
		lbls["browser_language"] = "en-US"
		lbls["browser_mobile"] = "false"
	} else {
		lbls["telemetry_sdk_name"] = "opentelemetry"
	}
	return lbls
}

// resourceIdentity returns (service, namespace, pod, deployment, sdkLanguage) for the
// backend or browser resource.
func (w *Workload) resourceIdentity(browser bool) (svc, ns, pod, dep, lang string) {
	if browser {
		svc = w.browserServiceName()
		return svc, frontendNamespace, svc + "-0", svc, browserSDKLanguage
	}
	return w.name, w.namespace, w.podName, w.name, backendSDKLanguage
}

// browserServiceName is the frontend/RUM service.name (the backend name + "-frontend").
func (w *Workload) browserServiceName() string { return w.name + "-frontend" }

// clientSpanName is the CLIENT span name. AI hops use the gen_ai "{op} {subject}" form
// (e.g. "chat gpt-4o", "invoke_agent planner") — NEVER the db "SELECT <model>" form (C2);
// db/cache hops use the db semconv "{op} {target}" form.
func clientSpanName(cs callSpec) string {
	if cs.AI != nil {
		return genai.SpanName(genaiOp(cs.AI), genaiSubject(cs.AI))
	}
	return dbOperation(cs) + " " + cs.Target
}

// dbOperation returns a plausible db/cache operation verb for the hop.
func dbOperation(cs callSpec) string {
	if cs.Kind == "cache" {
		return "GET"
	}
	return "SELECT"
}
