// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"encoding/json"
	"slices"
	"sort"
	"strconv"

	"github.com/rknightion/synthkit/internal/ledger"
	"github.com/rknightion/synthkit/internal/semconv"
	"github.com/rknightion/synthkit/internal/shape"
	"github.com/rknightion/synthkit/internal/state"
	"github.com/rknightion/synthkit/internal/telemetryspec"
)

// nodeIdentity is the resolved identity auto-stamped on EVERY series/stream/span of a node, BEFORE
// the author's DSL labels (B3 — without this two nodes sharing a profile emit byte-identical series
// and TestNoDuplicateSeries fails). Built from the workload binding + the node declaration.
type nodeIdentity struct {
	service   string
	namespace string
	cluster   string
	env       string
	runtime   string
	podName   string
	nodeName  string
	context   string // §5 canon (optional)
	useCase   string // §5 canon (optional)
	team      string // §5 canon (optional)
	version   string // service.version (declared override or serviceVersion default)
}

func (w *Workload) identity(n *node) nodeIdentity {
	ns := n.decl.Namespace
	if ns == "" {
		ns = n.decl.Name // default namespace = node name (one deployment per service)
	}
	id := nodeIdentity{
		service:   n.decl.Name,
		namespace: ns,
		cluster:   w.cluster,
		env:       w.env,
		runtime:   n.decl.Runtime,
		podName:   n.decl.Name + "-0",
		context:   n.decl.Context,
		useCase:   n.decl.UseCase,
		team:      n.decl.Team,
		version:   versionOr(n.decl.Version),
	}
	if w.b.Cluster != nil {
		for _, placed := range w.b.Cluster.Workloads {
			if placed.Name != n.decl.Name || n.decl.AgentObservability != nil && placed.Namespace != ns {
				continue
			}
			if len(placed.PodNames) > 0 {
				id.podName = placed.PodNames[0]
			}
			if len(placed.NodeIdx) > 0 && placed.NodeIdx[0] >= 0 && placed.NodeIdx[0] < len(w.b.Cluster.Nodes) {
				id.nodeName = w.b.Cluster.Nodes[placed.NodeIdx[0]].Hostname
			}
			break
		}
	}
	return id
}

func (id nodeIdentity) job() string { return id.namespace + "/" + id.service }

// metricBaseLabels stamps the node identity on every metric series (mirrors web_service
// spanMetricBase). Absent dims are omitted (I13).
func (id nodeIdentity) metricBaseLabels() map[string]string {
	return pruneEmpty(map[string]string{
		"service":                              id.service,
		"service_name":                         id.service,
		"namespace":                            id.namespace,
		"k8s_namespace_name":                   id.namespace,
		semconv.LabelDeploymentEnvironmentName: id.env,
		"cluster":                              id.cluster,
		"k8s_cluster_name":                     id.cluster,
		"job":                                  id.job(),
		semconv.LabelContext:                   id.context,
		semconv.LabelUseCase:                   id.useCase,
	})
}

// streamBaseLabels stamps the node identity on every Loki stream (low-card only; the high-card
// join keys ride in structured metadata — mirrors web_service logStreamLabels).
func (id nodeIdentity) streamBaseLabels(source, level string) map[string]string {
	return pruneEmpty(map[string]string{
		"service_name": id.service,
		"env":          id.env,
		"cluster":      id.cluster,
		"namespace":    id.namespace,
		"job":          id.job(),
		"source":       source,
		"level":        level,
	})
}

// resourceAttrs stamps the node identity on the OTLP resource block carrying its spans.
func (id nodeIdentity) resourceAttrs() map[string]any {
	a := map[string]any{
		semconv.AttrServiceName:               id.service,
		semconv.AttrServiceNamespace:          id.namespace,
		semconv.AttrServiceVersion:            id.version,
		semconv.AttrDeploymentEnvironmentName: id.env,
		"telemetry.sdk.name":                  "opentelemetry",
	}
	if id.cluster != "" {
		a["k8s.cluster.name"] = id.cluster
		a["k8s.namespace.name"] = id.namespace
		a["k8s.pod.name"] = id.podName
		a["k8s.deployment.name"] = id.service
	}
	if lang := sdkLanguage(id.runtime); lang != "" {
		a["telemetry.sdk.language"] = lang
	}
	if id.context != "" {
		a[semconv.AttrContext] = id.context
	}
	if id.useCase != "" {
		a[semconv.AttrUseCase] = id.useCase
	}
	if id.team != "" {
		a[semconv.AttrTeam] = id.team
	}
	return a
}

// routeMethod extracts the HTTP method token from a route string like "GET /api/v1/data".
// Returns the substring before the first space, or the whole string if no space is found.
func routeMethod(route string) string {
	for i := 0; i < len(route); i++ {
		if route[i] == ' ' {
			return route[:i]
		}
	}
	return route
}

// reqRefs builds the correlation field set the DSL `ref:` model pulls from. call may be nil (the
// entry node's own span/logs); when set, the span_id + AI fields reflect that hop.
func reqRefs(r *ledger.Request, call *ledger.Call) map[string]string {
	method := routeMethod(r.Route)
	refs := map[string]string{
		"trace_id":           r.TraceID,
		"span_id":            r.SpanID,
		"correlation_id":     r.CorrelationID,
		"request_id":         r.RequestID,
		"session_id":         r.SessionID,
		"portkey_trace_id":   r.PortkeyTraceID,
		"run_id":             r.RunID,
		"route":              r.Route,
		"method":             method,           // HTTP verb only (e.g. "GET", "POST")
		"original_span_name": "HTTP " + method, // browser span original_span_name attr (e.g. "HTTP GET")
		"env":                r.Env,
		"cluster":            r.Cluster,
		"status":             strconv.Itoa(r.StatusCode),
		"outcome":            r.Outcome.String(),
		// request-level gen_ai choice (the app minter draws one model/provider per request) — so a
		// node's gateway export log (reqRefs(r,nil)) and gen_ai span both carry the real model/provider.
		"model":    r.Model,
		"provider": r.Provider,
	}
	if call != nil {
		refs["span_id"] = call.SpanID
		if call.AI != nil { // a per-hop AI carrier overrides the request-level choice at that hop
			refs["model"] = call.AI.Model
			refs["provider"] = call.AI.Provider
		}
	}
	return refs
}

// observeMetric evaluates one metric spec into the established promrw state for a node: it stamps
// the node identity, then expands enum-label domains into the FULL cross-product (every label
// combination every run — determinism §3.4), and dispatches by instrument (gauge=Set,
// counter=Add-delta accumulates I3, histogram=Observe with the declared buckets + le-style).
func (w *Workload) observeMetric(st *state.State, id nodeIdentity, spec telemetryspec.MetricSpec, ctx telemetryspec.EvalCtx) {
	w.observeMetricStates(st, nil, id, spec, ctx, nil, nil)
}

// observeMetricStates evaluates a metric value once and writes it to the promrw state plus an
// optional native-OTLP state. Keeping the evaluation single-sourced means an enabled native lane
// mirrors the same inline instrument observation without changing the value draw sequence used by
// the established promrw lane. Native labels contain only the author-declared DSL dimensions;
// node identity is represented by the OTLP resource attributes.
//
// Enum rate_scale/value_scale multiply per label combination (default 1 keeps the legacy draw
// sequence and output). A histogram with a scaled rate observes a carry-rounded number of values
// per tick, each its own draw. incs, when non-nil, records each counter's per-combination
// increment for the node's derived specs (stock_of/count_of), which this function skips. sh,
// when non-nil, re-reads the shape load for the label combinations that declare their own clock.
func (w *Workload) observeMetricStates(st, native *state.State, id nodeIdentity, spec telemetryspec.MetricSpec, ctx telemetryspec.EvalCtx, incs map[string]float64, sh *seriesShape) {
	if isDerived(spec) {
		return
	}
	base := id.metricBaseLabels()
	for ci, combo := range scaledLabelCombos(spec.Labels) {
		labels := mergeLabels(base, combo.labels)
		ctx := sh.ctx(ctx, ci)
		if spec.Instrument == telemetryspec.InstrumentHistogram {
			n := 1
			if combo.rate != 1 {
				n = min(w.carryCount(spec.Name, labels, combo.rate), telemetryspec.MaxObservationsPerTick)
			}
			for range n {
				val, _ := spec.Value.Eval(ctx)
				w.observeHistogram(st, native, spec, labels, combo.labels, val*combo.value)
			}
			continue
		}
		val, _ := spec.Value.Eval(ctx)
		switch spec.Instrument {
		case telemetryspec.InstrumentGauge:
			val *= combo.value
			if st != nil {
				st.Set(spec.Name, labels, val)
			}
			if native != nil {
				native.Set(spec.Name, combo.labels, val)
			}
		case telemetryspec.InstrumentCounter:
			val *= combo.rate
			if st != nil {
				st.Add(spec.Name, labels, val)
			}
			if native != nil {
				native.Add(spec.Name, combo.labels, val)
			}
			if incs != nil {
				incs[incKey(spec.Name, combo.labels)] += val
			}
		}
	}
}

func (w *Workload) observeHistogram(st, native *state.State, spec telemetryspec.MetricSpec, labels, dsl map[string]string, val float64) {
	if st != nil {
		st.Observe(spec.Name, labels, spec.Buckets, leStyle(spec.LEStyle), val)
	}
	if native != nil {
		native.Observe(spec.Name, dsl, spec.Buckets, leStyle(spec.LEStyle), val)
	}
}

// observePausedMetricStates is observeMetricStates for a paused workload (explicit master volume
// 0). Counters and histograms are request-shaped event streams, so they observe nothing and stay
// flat. A gauge whose value is shape-scaled reads 0 (Base x a load factor of 0) rather than the
// evaluator's zero-means-1.0 fallback. Any other gauge describes the process itself (const,
// normal, float or int range) and keeps its usual evaluation. Derived stock gauges are stepped
// by observeDerived with zero inflow.
func (w *Workload) observePausedMetricStates(st, native *state.State, id nodeIdentity, spec telemetryspec.MetricSpec, ctx telemetryspec.EvalCtx) {
	if spec.Instrument != telemetryspec.InstrumentGauge || isDerived(spec) {
		return
	}
	base := id.metricBaseLabels()
	for _, combo := range scaledLabelCombos(spec.Labels) {
		labels := mergeLabels(base, combo.labels)
		var val float64
		if spec.Value.Kind() != telemetryspec.KindShape {
			val, _ = spec.Value.Eval(ctx)
			val *= combo.value
		}
		if st != nil {
			st.Set(spec.Name, labels, val)
		}
		if native != nil {
			native.Set(spec.Name, combo.labels, val)
		}
	}
}

func mergeLabels(base, combo map[string]string) map[string]string {
	labels := make(map[string]string, len(base)+len(combo))
	for k, v := range base {
		labels[k] = v
	}
	for k, v := range combo {
		labels[k] = v
	}
	return labels
}

// labelCombo is one label-value combination plus the product of its enum values' declared
// rate_scale and value_scale (1 when none is declared).
type labelCombo struct {
	labels map[string]string
	rate   float64
	value  float64
	// clocks are the shape_profile overrides of the chosen enum values, in sorted label-key
	// order (a later label's field wins). Empty when no chosen value declares one.
	clocks []*shape.Profile
}

// labelCombos returns every label-value-map combination for a metric's labels: const/const_str → a
// single value, enum → its full ordered domain. The cross-product guarantees the -dump inventory
// is run-stable (every series appears every tick). Capability matrix guarantees only
// const/const_str/enum reach here.
func labelCombos(labels map[string]telemetryspec.ValueModel) []map[string]string {
	scaled := scaledLabelCombos(labels)
	out := make([]map[string]string, len(scaled))
	for i, c := range scaled {
		out[i] = c.labels
	}
	return out
}

// scaledLabelCombos is labelCombos carrying each combination's per-value scale products.
func scaledLabelCombos(labels map[string]telemetryspec.ValueModel) []labelCombo {
	keys := sortedValueModelKeys(labels)
	combos := []labelCombo{{labels: map[string]string{}, rate: 1, value: 1}}
	for _, k := range keys {
		vm := labels[k]
		type choice struct {
			val         string
			rate, value float64
			clock       *shape.Profile
		}
		var vals []choice
		switch vm.Kind() {
		case telemetryspec.KindEnum:
			for _, e := range vm.Enum {
				c := choice{val: e.Value, rate: 1, value: 1}
				if e.RateScale != nil {
					c.rate = *e.RateScale
				}
				if e.ValueScale != nil {
					c.value = *e.ValueScale
				}
				c.clock = e.ShapeProfile
				vals = append(vals, c)
			}
		case telemetryspec.KindConstStr:
			_, s := vm.Eval(telemetryspec.EvalCtx{})
			vals = []choice{{val: s, rate: 1, value: 1}}
		case telemetryspec.KindConst:
			n, _ := vm.Eval(telemetryspec.EvalCtx{})
			vals = []choice{{val: formatNum(n), rate: 1, value: 1}}
		default:
			continue // unreachable: capability matrix rejects other kinds as labels
		}
		next := make([]labelCombo, 0, len(combos)*len(vals))
		for _, c := range combos {
			for _, v := range vals {
				nc := make(map[string]string, len(c.labels)+1)
				for kk, vv := range c.labels {
					nc[kk] = vv
				}
				nc[k] = v.val
				clocks := c.clocks
				if v.clock != nil {
					clocks = append(slices.Clone(c.clocks), v.clock)
				}
				next = append(next, labelCombo{labels: nc, rate: c.rate * v.rate, value: c.value * v.value, clocks: clocks})
			}
		}
		combos = next
	}
	return combos
}

func sortedValueModelKeys(values map[string]telemetryspec.ValueModel) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// evalAttr evaluates a value model to its natural Go type for a span attribute / log body field
// (string | bool | number).
func evalAttr(vm telemetryspec.ValueModel, ctx telemetryspec.EvalCtx) any {
	switch vm.Kind() {
	case telemetryspec.KindConstStr, telemetryspec.KindEnum, telemetryspec.KindRef:
		_, s := vm.Eval(ctx)
		return s
	case telemetryspec.KindBool:
		n, _ := vm.Eval(ctx)
		return n == 1
	default:
		n, _ := vm.Eval(ctx)
		return n
	}
}

// pruneEmpty deletes keys with empty-string values (absent dimension OMITTED, never sentinelled — I13).
func pruneEmpty(m map[string]string) map[string]string {
	for k, v := range m {
		if v == "" {
			delete(m, k)
		}
	}
	return m
}

// jsonBody marshals a log body map to a compact JSON string (deterministic key order).
func jsonBody(v map[string]any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func leStyle(s string) state.LEStyle {
	if s == telemetryspec.LEStyleDotZero {
		return state.LEDotZero
	}
	return state.LEBare
}

func formatNum(f float64) string {
	return strconv.FormatFloat(f, 'g', -1, 64)
}

func sdkLanguage(runtime string) string {
	switch runtime {
	case "go", "jvm", "node", "python", "dotnet", "ruby", "php", "webjs":
		if runtime == "jvm" {
			return "java"
		}
		return runtime
	}
	return ""
}

func logLevel(o ledger.Outcome) string {
	switch o {
	case ledger.OutcomeServerError, ledger.OutcomeThrottled:
		return "error"
	case ledger.OutcomeClientError:
		return "warn"
	default:
		return "info"
	}
}
