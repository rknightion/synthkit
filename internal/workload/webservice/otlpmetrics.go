// SPDX-License-Identifier: AGPL-3.0-only

package webservice

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/semconv"
	"github.com/rknightion/synthkit/internal/shape"
	"github.com/rknightion/synthkit/internal/sink/otlp"
	"github.com/rknightion/synthkit/internal/state"
)

// otlpDurationBuckets is the OTEL HTTP-semconv-recommended explicit bound set (seconds) for
// http.server.request.duration. The state layer appends the implicit +Inf bucket.
var otlpDurationBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.075, 0.1, 0.25, 0.5, 0.75, 1, 2.5, 5, 7.5, 10}

// tickOTLPMetrics emits the native OTLP application metrics (http.server.request.duration +
// http.server.active_requests) for this workload, when the OTLP-metrics lane is wired. Volume
// derives from the metric lane's configured per-tick totalCalls. Route/status allocation and
// bounded duration sampling are independent of the ledger sample and legacy spanmetrics.
// Cumulative; resource attrs naked or k8s_monitoring-enriched. No-op if the writer
// is nil (lane not declared/wired) — keeps stOTLP empty when the lane is off.
func (w *Workload) tickOTLPMetrics(ctx context.Context, now time.Time, world *core.World, totalCalls int, routes []routeStat) error {
	if world.OTLPMetrics == nil {
		return nil
	}
	if w.otlpColdStart.IsZero() {
		w.otlpColdStart = now
	}
	// Accumulate the cumulative request-duration histogram, conserving the full
	// configured volume when route and status populations do not divide evenly.
	if len(routes) > 0 && totalCalls > 0 {
		perRoute := totalCalls / len(routes)
		// The released native lane consumed this capped uniform-draw budget.
		// Preserve its exact rounding/caps ONLY for shared-RNG compatibility so
		// active requests, spanmetrics and later requests keep their released values.
		// Actual duration observations use the isolated stream and full counts below.
		for _, rs := range routes {
			legacyOK := max(0, min(200, int(float64(perRoute)*(1-rs.errorRate))))
			legacyErrors := max(0, min(200, int(float64(perRoute)*rs.errorRate)))
			for range legacyOK + legacyErrors {
				world.Shape.Float64()
			}
		}
		latencyFactor := world.Shape.FailFactor(now, "latency_spike", w.name, 4.0)
		for i, rs := range routes {
			calls := perRoute
			if i < totalCalls%len(routes) {
				calls++
			}
			method, route := splitRoute(rs.route)
			p95ms := 0.0
			for _, endpoint := range w.cfg.Endpoints {
				if endpoint.Route == rs.route {
					p95ms = endpoint.P95ms
					break
				}
			}
			errN := int(float64(calls) * rs.errorRate)
			w.observeOTLPDuration(method, route, 200, calls-errN, p95ms, latencyFactor)
			w.observeOTLPDuration(method, route, 500, errN, p95ms, latencyFactor)
		}
	}
	// Derive distinct HTTP methods from routes for per-method active_requests attribution.
	methods := distinctMethods(routes)
	res := otlp.MetricResource{
		Attrs: w.otelResourceAttrs(w.cfg.otelMode()),
		Scope: otlp.Scope{Name: otelHTTPScopeName, Version: otelHTTPScopeVersion},
		Metrics: []otlp.Metric{
			w.otlpDurationMetric(now),
			w.otlpActiveRequestsMetric(now, totalCalls, world.Shape, methods),
		},
	}
	return world.OTLPMetrics.Write(ctx, []otlp.MetricResource{res})
}

// observeOTLPDuration represents every request in a route/status cohort with at most
// 200 duration draws from the same endpoint and incident model as the request minter.
// Integer weights preserve exact counts and matching bucket/sum contributions without
// making observation work proportional to configured traffic. The two cadences sample
// independently; these aggregate samples are not request-correlated exemplars.
func (w *Workload) observeOTLPDuration(method, route string, status, n int, p95ms, latencyFactor float64) {
	if n <= 0 {
		return
	}
	draws := min(n, 200)
	labels := map[string]string{
		"http.request.method":       method,
		"http.route":                route,
		"http.response.status_code": strconv.Itoa(status),
	}
	for i := 0; i < draws; i++ {
		weight := n / draws
		if i < n%draws {
			weight++
		}
		seconds := drawDuration(p95ms, w.otlpDurationRNG) / 1000 * latencyFactor
		w.stOTLP.ObserveWeighted(otelDurationMetricName, labels, otlpDurationBuckets, state.LEDotZero, seconds, uint64(weight))
	}
}

// otlpDurationMetric materializes the cumulative duration histogram (one OTLP datapoint per
// route/status series) from stOTLP.
func (w *Workload) otlpDurationMetric(now time.Time) otlp.Metric {
	pts := w.stOTLP.CollectHistos()
	hps := make([]otlp.HistogramPoint, 0, len(pts))
	for _, p := range pts {
		hps = append(hps, otlp.HistogramPoint{
			Attrs:        labelsToAny(p.Labels),
			Start:        w.otlpColdStart,
			Time:         now,
			Count:        p.Count,
			Sum:          p.Sum,
			Bounds:       p.Bounds,
			BucketCounts: p.BucketCounts,
		})
	}
	return otlp.Metric{
		Name:        otelDurationMetricName,
		Unit:        "s",
		Kind:        otlp.MetricHistogram,
		Temporality: otlp.TemporalityCumulative,
		Histograms:  hps,
	}
}

// otlpActiveRequestsMetric emits the instantaneous in-flight count as a non-monotonic
// (UpDownCounter) cumulative Sum — the value each tick is the current concurrency (Little's
// law: rps × latency), not an accumulation. Gateway → a Prometheus gauge.
// One NumberPoint per distinct HTTP method (OTEL semconv requires http.request.method + url.scheme).
// The total active count is split evenly across methods.
func (w *Workload) otlpActiveRequestsMetric(now time.Time, totalCalls int, shp *shape.Engine, methods []string) otlp.Metric {
	rps := float64(totalCalls) / interval.Seconds()
	active := rps * (0.05 + shp.Float64()*0.15) // in-flight ≈ rps × ~50–200ms service time
	perMethod := active / float64(len(methods))
	pts := make([]otlp.NumberPoint, 0, len(methods))
	for _, m := range methods {
		pts = append(pts, otlp.NumberPoint{
			Attrs: map[string]any{
				"http.request.method": m,
				"url.scheme":          "https",
			},
			Start: w.otlpColdStart,
			Time:  now,
			Value: perMethod,
		})
	}
	return otlp.Metric{
		Name:        otelActiveReqMetricName,
		Unit:        "{request}",
		Kind:        otlp.MetricSum,
		Monotonic:   false,
		Temporality: otlp.TemporalityCumulative,
		Numbers:     pts,
	}
}

// distinctMethods extracts the set of distinct HTTP methods from routes.
// Falls back to ["GET"] when routes is empty.
func distinctMethods(routes []routeStat) []string {
	if len(routes) == 0 {
		return []string{"GET"}
	}
	seen := make(map[string]struct{}, len(routes))
	out := make([]string, 0, len(routes))
	for _, rs := range routes {
		m, _ := splitRoute(rs.route)
		if m == "" {
			m = "GET"
		}
		if _, ok := seen[m]; !ok {
			seen[m] = struct{}{}
			out = append(out, m)
		}
	}
	if len(out) == 0 {
		return []string{"GET"}
	}
	return out
}

// otelResourceAttrs builds the OTLP resource attributes for the chosen mode. naked = SDK
// defaults; k8s_monitoring adds the k8sattributes default metadata set (VALUES sourced from
// the bound pod's fixture identity — never minted) + the resourcedetection(system) host/os
// attrs. host.arch is the placed node's GOARCH (derived from its instance type, matching the
// node's kubernetes.io/arch); os.type is linux (every modelled EKS node). Absent dims omitted
// (I13). Legacy deployment.environment DROPPED — deployment.environment.name only (clean
// cutover). §5 canon attrs (context/use_case/team) added when non-empty (pruned per I13).
func (w *Workload) otelResourceAttrs(mode string) map[string]any {
	attrs := map[string]any{
		semconv.AttrServiceName:               w.name,
		semconv.AttrServiceNamespace:          w.namespace,
		semconv.AttrServiceVersion:            w.version,
		"service.instance.id":                 w.podName,
		semconv.AttrDeploymentEnvironmentName: w.env,
		"telemetry.sdk.name":                  "opentelemetry",
		"telemetry.sdk.language":              backendSDKLanguage,
		"telemetry.sdk.version":               otelSDKVersion,
	}
	// §5 canon attrs (§5): omit when empty (I13).
	if w.context != "" {
		attrs[semconv.AttrContext] = w.context
	}
	if w.useCase != "" {
		attrs[semconv.AttrUseCase] = w.useCase
	}
	if w.team != "" {
		attrs[semconv.AttrTeam] = w.team
	}
	if mode == otelModeK8sMonitoring {
		attrs["k8s.namespace.name"] = w.namespace
		attrs["k8s.pod.name"] = w.podName
		if w.podUID != "" {
			attrs["k8s.pod.uid"] = w.podUID
		}
		attrs["k8s.deployment.name"] = w.name
		attrs["k8s.cluster.name"] = w.cluster
		attrs["k8s.node.name"] = w.nodeName
		attrs["host.name"] = w.podName // resourcedetection(system): os hostname == pod name in-pod
		arch := w.hostArch             // from the placed node's instance type (matches kubernetes.io/arch)
		if arch == "" {
			arch = "amd64" // no node placement resolved — default to the x86 baseline
		}
		attrs["host.arch"] = arch
		attrs["os.type"] = "linux"
	}
	return pruneEmptyAny(attrs)
}

// pruneEmptyAny drops keys whose string value is "" so an absent dimension is OMITTED (I13).
// Non-string values are kept.
func pruneEmptyAny(m map[string]any) map[string]any {
	for k, v := range m {
		if s, ok := v.(string); ok && s == "" {
			delete(m, k)
		}
	}
	return m
}

// labelsToAny converts a string label map to the map[string]any OTLP attribute form.
func labelsToAny(m map[string]string) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// splitRoute splits an endpoint route "GET /v1/items" into ("GET", "/v1/items"). A route with
// no leading method returns ("", route).
func splitRoute(r string) (method, route string) {
	if i := strings.IndexByte(r, ' '); i >= 0 {
		return r[:i], r[i+1:]
	}
	return "", r
}
