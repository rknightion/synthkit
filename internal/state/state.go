// SPDX-License-Identifier: AGPL-3.0-only

// Package state is the sink-correctness layer (ARCHITECTURE §6, invariants I3/I4): a
// per-emitter store of cumulative series that makes what lands in Mimir behave exactly like
// a real cumulative counter/histogram.
//
// Prometheus counters and histograms are CUMULATIVE: each scrape exposes the running total
// since process start, and PromQL rate()/increase()/histogram_quantile() only work if
// successive samples are monotonic. A fabricator that pushed per-tick deltas would produce
// series that look like counters but break every rate query. This layer holds per-series
// monotonic state across ticks so the wire shape is correct.
//
// Ownership rule (ARCHITECTURE): each construct/workload owns its OWN *State — no
// cross-instance sharing — and an instance ticks on a single goroutine, so State needs no
// mutex. (The request ledger, which IS shared across goroutines, has its own locking; this
// does not.)
//
// Numeric behaviour:
//   - Add(...)     accumulates a running total. Use for true counters AND monotonic gauges
//     (cumulative *_sum/*_total series): both expose a cumulative value; the
//     counter-vs-gauge distinction only changes how a dashboard queries it (rate vs delta),
//     not the emitted number.
//   - Set(...)     stores an instantaneous value. Use for normal gauges (queue depth, replicas).
//   - Observe(...) accumulates a cumulative histogram (buckets + _sum + _count).
//   - Reset(...)   drops a counter's running total to zero — models a synthetic gateway
//     restart (rare, off-peak; delta() windows rarely span one).
package state

import (
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/rknightion/synthkit/internal/sink/promrw"
)

// LEStyle selects how a histogram's `le` bucket-bound label is rendered — a real fidelity
// split between the three ingestion paths (I4). Pick the one that matches how the modelled
// series actually reaches Mimir, not how the exporter prints it:
//
//   - LEPromV3: anything a Prometheus-v3 scraper ingests — which, in practice, is every
//     family Alloy scrapes off a /metrics endpoint. Prometheus v3 NORMALISES the `le` label
//     of classic histograms (and `quantile` on summaries) on ingestion, so what the exporter
//     printed is irrelevant: le="1" on the wire is stored as le="1.0". Alloy 1.x embeds the
//     Prometheus v3 scrape loop, so this covers kubelet, kube-proxy, the control plane, every
//     k8s addon, and every prometheus.exporter.* component. Rendering is byte-identical to
//     Prometheus' own labels.FormatOpenMetricsFloat: general notation, a forced ".0" when
//     that produced no "." or "e", and hardcoded "0.0"/"1.0"/"-1.0". Note the general
//     notation — a 2.592e6 bound renders "2.592e+06", NOT "2592000.0".
//   - LEDotZero: the OTLP→Prometheus translation (APM span-metrics, gen_ai client metrics).
//     Forces a trailing ".0" on integer-valued bounds but keeps FIXED notation at every
//     magnitude, so a 10000000 bound renders "10000000.0", never "1e+07". This differs from
//     LEPromV3 only at the notation extremes (|exponent| beyond general notation's switch
//     points) and the two must not be conflated there.
//   - LEBare: a producer that remote-writes its own series with NO scrape in between, so
//     nothing ever normalises the label — minimal decimals, no forced ".0", no scientific
//     notation: "1", "10", "10000000", "0.005". This is the EXCEPTION, not the default; it
//     was the Prometheus-native scrape convention only before Prometheus v3.
//
// Provenance: prometheus/prometheus model/textparse (promparse.go, openmetricsparse.go,
// protobufparse.go) all route bucket/quantile label values through
// labels.FormatOpenMetricsFloat (model/labels/float.go), read at v0.310.0, 2026-08-27.
// Corroborated by the reality corpus: 165 captured `le` values, zero bare integers.
type LEStyle int

const (
	LEBare LEStyle = iota
	LEDotZero
	LEPromV3
)

// MaxExemplarsPerSeries caps exemplars retained per series per emit. Mimir keeps a bounded
// per-series exemplar ring anyway; we only need a few representative real traces per tick.
const MaxExemplarsPerSeries = 5

// State holds one instance's cumulative series between ticks.
type State struct {
	counters map[string]*scalarState      // Add()          — counters + monotonic gauges (cumulative)
	gauges   map[string]*scalarState      // Set()          — instantaneous gauges
	histos   map[string]*histoState       // Observe()      — cumulative classic histograms
	natives  map[string]*nativeHistoState // ObserveNative() — cumulative native (exponential) histograms
}

type scalarState struct {
	name      string
	labels    map[string]string
	value     float64
	exemplars []promrw.Exemplar // drained each Collect (per-emit, not cumulative)
}

type histoState struct {
	name      string
	labels    map[string]string
	bounds    []float64 // finite upper bounds (the pinned `le` set; +Inf is implicit)
	style     LEStyle   // `le` rendering convention (fixed at first Observe for this series)
	counts    []float64 // cumulative count ≤ bounds[i]; len == len(bounds)
	sum       float64
	count     float64           // total observations (== the +Inf bucket)
	exemplars []promrw.Exemplar // drained each Collect; placed on the landing bucket by value
}

// nativeHistoState accumulates a cumulative exponential (native) histogram across ticks.
// buckets maps an exponential-bucket index → the cumulative count IN that bucket (native
// histograms are sparse; counts are per-bucket, not cumulative-across-buckets like classic).
type nativeHistoState struct {
	name      string
	labels    map[string]string
	schema    int32
	buckets   map[int]int64
	zeroCount uint64
	sum       float64
	count     uint64
}

// NewState returns an empty per-instance correctness layer.
func NewState() *State {
	return &State{
		counters: map[string]*scalarState{},
		gauges:   map[string]*scalarState{},
		histos:   map[string]*histoState{},
		natives:  map[string]*nativeHistoState{},
	}
}

// Add accumulates delta into a cumulative series (counter or monotonic gauge).
func (s *State) Add(name string, labels map[string]string, delta float64) {
	sig := seriesSig(name, labels)
	cs := s.counters[sig]
	if cs == nil {
		cs = &scalarState{name: name, labels: cloneLabels(labels)}
		s.counters[sig] = cs
	}
	cs.value += delta
}

// Set stores the instantaneous value of a normal gauge.
func (s *State) Set(name string, labels map[string]string, value float64) {
	sig := seriesSig(name, labels)
	gs := s.gauges[sig]
	if gs == nil {
		gs = &scalarState{name: name, labels: cloneLabels(labels)}
		s.gauges[sig] = gs
	}
	gs.value = value
}

// Observe records one histogram observation against the pinned bucket bounds. The bounds
// and style must be the same for a given (name,labels) series across its lifetime — both
// are fixed by the data contract. bounds is cloned defensively (the canon bucket arrays are
// shared package vars).
func (s *State) Observe(name string, labels map[string]string, bounds []float64, style LEStyle, value float64) {
	sig := seriesSig(name, labels)
	hs := s.histos[sig]
	if hs == nil {
		hs = &histoState{
			name:   name,
			labels: cloneLabels(labels),
			bounds: slices.Clone(bounds),
			style:  style,
			counts: make([]float64, len(bounds)),
		}
		s.histos[sig] = hs
	}
	hs.sum += value
	hs.count++
	// Cumulative buckets: an observation ≤ bounds[i] is also ≤ every larger bound, so
	// increment every boundary it satisfies — counts then stay monotonically
	// non-decreasing in i.
	for i, b := range hs.bounds {
		if value <= b {
			hs.counts[i]++
		}
	}
}

// ObserveExemplar records one histogram observation (exactly like Observe) AND retains an
// exemplar (typically a real request's {trace_id}) to be attached, in Collect, to the bucket
// the value lands in. Capped at MaxExemplarsPerSeries per series per emit. Use for the small
// sample of real ledger requests folded into an otherwise-synthetic histogram.
func (s *State) ObserveExemplar(name string, labels map[string]string, bounds []float64, style LEStyle, value float64, exLabels map[string]string, t time.Time) {
	s.Observe(name, labels, bounds, style, value)
	hs := s.histos[seriesSig(name, labels)]
	if hs == nil || len(hs.exemplars) >= MaxExemplarsPerSeries {
		return
	}
	hs.exemplars = append(hs.exemplars, promrw.Exemplar{Labels: cloneLabels(exLabels), Value: value, T: t})
}

// ObserveNative records one observation into a cumulative native (exponential) histogram.
// schema is fixed at first observe for the series (the data contract pins it). Values <=
// nativeZeroThreshold land in the zero bucket; synthkit latency draws are > 0 so that is
// effectively unused.
func (s *State) ObserveNative(name string, labels map[string]string, schema int32, value float64) {
	sig := seriesSig(name, labels)
	ns := s.natives[sig]
	if ns == nil {
		ns = &nativeHistoState{name: name, labels: cloneLabels(labels), schema: schema, buckets: map[int]int64{}}
		s.natives[sig] = ns
	}
	ns.sum += value
	ns.count++
	if value <= nativeZeroThreshold {
		ns.zeroCount++
		return
	}
	ns.buckets[nativeBucketIndex(value, ns.schema)]++
}

// ObserveDual records one observation into BOTH a classic histogram (bounds+style) AND a
// native histogram (schema), keyed by the same (name,labels). Use ONLY for the
// metrics-generator-derived span histograms, which a real Tempo metrics-generator emits in
// both forms simultaneously (signals/apm.md [slug: apm-latency], SK-28). The single
// observation feeds both, so classic _count/_sum and native Count/Sum stay identical.
func (s *State) ObserveDual(name string, labels map[string]string, bounds []float64, style LEStyle, schema int32, value float64) {
	s.Observe(name, labels, bounds, style, value)
	s.ObserveNative(name, labels, schema, value)
}

// ObserveDualExemplar is ObserveDual plus a classic-side exemplar on the landing bucket
// (the native series carries no exemplar in this build).
func (s *State) ObserveDualExemplar(name string, labels map[string]string, bounds []float64, style LEStyle, schema int32, value float64, exLabels map[string]string, t time.Time) {
	s.ObserveExemplar(name, labels, bounds, style, value, exLabels, t)
	s.ObserveNative(name, labels, schema, value)
}

// CounterExemplar attaches an exemplar to an EXISTING counter series (no value change — the
// bulk Add already happened). No-op if the counter has not been Added yet. Capped per emit.
func (s *State) CounterExemplar(name string, labels map[string]string, exLabels map[string]string, value float64, t time.Time) {
	cs := s.counters[seriesSig(name, labels)]
	if cs == nil || len(cs.exemplars) >= MaxExemplarsPerSeries {
		return
	}
	cs.exemplars = append(cs.exemplars, promrw.Exemplar{Labels: cloneLabels(exLabels), Value: value, T: t})
}

// landingBucket returns the index into bounds of the bucket an observation of `value` lands
// in (smallest bound ≥ value), or len(bounds) for the implicit +Inf bucket.
//
// PLACEMENT CONVENTION (OpenMetrics / Prometheus client_* libraries): an exemplar attaches
// to the SINGLE bucket whose count the observation increments at its boundary — i.e. the
// smallest le ≥ value — NOT to every cumulative bucket ≥ value. Mimir's query_exemplars keys
// on the per-bucket series, and Grafana resolves the link via the trace_id label. Ref:
// OpenMetrics spec "Exemplars" + prometheus/client_golang histogram exemplar behaviour.
// (The frozen test TestObserveExemplarAttachesToLandingBucket enforces single-bucket.)
func landingBucket(bounds []float64, value float64) int {
	for i, b := range bounds {
		if value <= b {
			return i
		}
	}
	return len(bounds)
}

// DeleteGauge removes an instantaneous gauge series from the tracked set so it no longer
// appears in subsequent Collect calls. Use for info gauges whose presence signals existence
// (e.g. edge_info) and that must become absent — not zero — when the entity disappears.
// No-op if the series was never Set.
func (s *State) DeleteGauge(name string, labels map[string]string) {
	delete(s.gauges, seriesSig(name, labels))
}

// Reset drops a cumulative series' running total to zero (synthetic counter/gateway
// restart). It has no effect on instantaneous gauges or histograms.
func (s *State) Reset(name string, labels map[string]string) {
	if cs := s.counters[seriesSig(name, labels)]; cs != nil {
		cs.value = 0
	}
}

// Collect materializes every tracked series at timestamp now. ⚠ The returned Series alias
// the live per-series label maps (not deep-copied), so callers MUST NOT mutate them — the
// scoped fan-out relies on this: the promrw/loki/otlp scoped writers CLONE labels before
// stamping the blueprint selector, so the same collected batch can be written without
// corrupting cumulative state. Histograms expand into explicit `_bucket{le=…}` (cumulative
// incl. +Inf) + `_sum` + `_count` series — the exact wire shape Mimir stores for an
// OTLP/Prometheus histogram.
func (s *State) Collect(now time.Time) []promrw.Series {
	out := make([]promrw.Series, 0, len(s.counters)+len(s.gauges)+len(s.histos)*4+len(s.natives))
	// Add()-tracked series (counters + cumulative _sum/_total monotonic gauges) are
	// rate-able; tag them KindCounter so the dashboard generator queries them with rate().
	for _, cs := range s.counters {
		out = append(out, promrw.Series{Name: cs.name, Labels: cs.labels, Value: cs.value, T: now, Kind: promrw.KindCounter, Exemplars: cs.exemplars})
		cs.exemplars = nil // drained: exemplars are per-emit, not cumulative
	}
	for _, gs := range s.gauges {
		out = append(out, promrw.Series{Name: gs.name, Labels: gs.labels, Value: gs.value, T: now, Kind: promrw.KindGauge})
	}
	for _, hs := range s.histos {
		// Pre-bin exemplars by landing bucket (index into bounds; len(bounds) == +Inf).
		var byBucket map[int][]promrw.Exemplar
		if len(hs.exemplars) > 0 {
			byBucket = make(map[int][]promrw.Exemplar, len(hs.exemplars))
			for _, ex := range hs.exemplars {
				idx := landingBucket(hs.bounds, ex.Value)
				byBucket[idx] = append(byBucket[idx], ex)
			}
		}
		for i, b := range hs.bounds {
			out = append(out, promrw.Series{
				Name:      hs.name + "_bucket",
				Labels:    withLE(hs.labels, formatLE(b, hs.style)),
				Value:     hs.counts[i],
				T:         now,
				Kind:      promrw.KindHistogram,
				Exemplars: byBucket[i],
			})
		}
		out = append(out, promrw.Series{
			Name:      hs.name + "_bucket",
			Labels:    withLE(hs.labels, "+Inf"),
			Value:     hs.count,
			T:         now,
			Kind:      promrw.KindHistogram,
			Exemplars: byBucket[len(hs.bounds)],
		})
		out = append(out, promrw.Series{Name: hs.name + "_sum", Labels: hs.labels, Value: hs.sum, T: now, Kind: promrw.KindHistogram})
		out = append(out, promrw.Series{Name: hs.name + "_count", Labels: hs.labels, Value: hs.count, T: now, Kind: promrw.KindHistogram})
		hs.exemplars = nil // drained
	}
	for _, ns := range s.natives {
		buckets, schema := coarsenToLimit(ns.buckets, ns.schema)
		spans, deltas := encodePositiveBuckets(buckets)
		out = append(out, promrw.Series{
			Name:   ns.name,
			Labels: ns.labels,
			T:      now,
			Kind:   promrw.KindHistogram,
			Native: &promrw.NativeHistogram{
				Schema:         schema,
				Count:          ns.count,
				Sum:            ns.sum,
				ZeroThreshold:  nativeZeroThreshold,
				ZeroCount:      ns.zeroCount,
				PositiveSpans:  spans,
				PositiveDeltas: deltas,
			},
		})
	}
	return out
}

// HistoPoint is a cumulative classic-histogram snapshot in OTLP HistogramDataPoint form:
// per-bucket counts (NON-cumulative), len == len(Bounds)+1 with the trailing element the
// +Inf overflow bucket. Distinct from the Prometheus _bucket{le} expansion in Collect.
// Labels alias live state (read-only) — the scoped OTLP writer clones before stamping.
type HistoPoint struct {
	Name         string
	Labels       map[string]string
	Bounds       []float64
	BucketCounts []uint64
	Sum          float64
	Count        uint64
}

// CollectHistos snapshots every Observe()-tracked classic histogram as an OTLP HistoPoint,
// converting the internal CUMULATIVE bucket counts (counts[i] = observations ≤ bounds[i]) to
// OTLP per-bucket counts. Does NOT drain — cumulative histograms persist across ticks (I3).
func (s *State) CollectHistos() []HistoPoint {
	out := make([]HistoPoint, 0, len(s.histos))
	for _, hs := range s.histos {
		bc := make([]uint64, len(hs.bounds)+1)
		var prev float64
		for i, c := range hs.counts { // c is cumulative (≤ bounds[i]); monotonic non-decreasing in i
			bc[i] = uint64(c - prev)
			prev = c
		}
		bc[len(hs.bounds)] = uint64(hs.count - prev) // +Inf overflow = total − (≤ last finite bound)
		out = append(out, HistoPoint{
			Name:         hs.name,
			Labels:       hs.labels,
			Bounds:       slices.Clone(hs.bounds),
			BucketCounts: bc,
			Sum:          hs.sum,
			Count:        uint64(hs.count),
		})
	}
	return out
}

// CapSeries enforces a per-push series budget (I7). cap<=0 means unlimited. Returns the
// (possibly truncated) batch and whether it was capped, so the caller can WARN. The promrw
// sink applies the same guard as a backstop.
func CapSeries(batch []promrw.Series, cap int) ([]promrw.Series, bool) {
	if cap <= 0 || len(batch) <= cap {
		return batch, false
	}
	return batch[:cap], true
}

// seriesSig is a stable identity for a (name, labels) series across ticks.
func seriesSig(name string, labels map[string]string) string {
	return name + "\x00" + LabelSig(labels)
}

// LabelSig builds a stable string key from a label map (for per-series cumulative state).
func LabelSig(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(m[k])
		b.WriteByte(';')
	}
	return b.String()
}

func cloneLabels(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	maps.Copy(out, m)
	return out
}

func withLE(base map[string]string, le string) map[string]string {
	out := make(map[string]string, len(base)+1)
	maps.Copy(out, base)
	out["le"] = le
	return out
}

// formatLE renders a bucket upper bound as the `le` label string per the metric's ingestion
// style (see LEStyle).
//
// LEPromV3 reproduces prometheus/prometheus labels.FormatOpenMetricsFloat byte for byte:
// the three hardcoded cases, then 'g',-1 (GENERAL notation — scientific past the switch
// points), then a ".0" suffix only when that output contains neither "." nor "e".
//
// LEBare and LEDotZero share 'f',-1 — minimal decimals with NO scientific notation at any
// magnitude (le="10000000", never "1e+07") — and LEDotZero then forces a trailing ".0" on
// integer-valued bounds. Do not "unify" these with LEPromV3: they agree on every bound in
// the current corpus and diverge deliberately at the notation extremes.
func formatLE(b float64, style LEStyle) string {
	if style == LEPromV3 {
		return formatOpenMetricsFloat(b)
	}
	s := strconv.FormatFloat(b, 'f', -1, 64)
	if style == LEDotZero && !strings.Contains(s, ".") {
		s += ".0"
	}
	return s
}

// formatOpenMetricsFloat mirrors prometheus/prometheus model/labels.FormatOpenMetricsFloat
// (v0.310.0). +Inf/-Inf/NaN are handled for completeness; State.Collect emits the overflow
// bucket as the literal "+Inf" and never reaches this path with an infinity.
func formatOpenMetricsFloat(f float64) string {
	switch {
	case f == 1:
		return "1.0"
	case f == 0:
		return "0.0"
	case f == -1:
		return "-1.0"
	case math.IsNaN(f):
		return "NaN"
	case math.IsInf(f, +1):
		return "+Inf"
	case math.IsInf(f, -1):
		return "-Inf"
	}
	s := strconv.FormatFloat(f, 'g', -1, 64)
	if strings.ContainsAny(s, "e.") {
		return s
	}
	return s + ".0"
}
