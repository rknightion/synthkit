// SPDX-License-Identifier: AGPL-3.0-only

package telemetryspec

import (
	"bytes"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/rknightion/synthkit/internal/highcard"
	"github.com/rknightion/synthkit/internal/shape"
	"gopkg.in/yaml.v3"
)

// Instrument kinds (the `instrument` key on a metric).
const (
	InstrumentGauge     = "gauge"
	InstrumentCounter   = "counter"
	InstrumentHistogram = "histogram"
)

// le_style values — how a histogram's `le` bucket-bound label is rendered. They map to
// state.LEStyle at emit time (the workload, F4): "bare" → state.LEBare (a producer that
// remote-writes its own series with NO scrape in between — this stopped being the Prometheus
// scrape convention at v3, which normalises `le` on ingestion), "dotzero" → state.LEDotZero
// (OTLP→Prometheus span-metric translation). Default "" ⇒ bare.
//
// A family modelling an Alloy / Prometheus-v3 scrape wants state.LEPromV3, which this spec
// surface cannot yet express — see SKT-0010.11. Adding a "promv3" token is the natural
// follow-up; until then a blueprint-declared histogram on a scraped surface renders the
// pre-v3 form.
const (
	LEStyleBare    = "bare"
	LEStyleDotZero = "dotzero"
)

// Metric scopes (ARCHITECTURE §5). Empty ⇒ the owning node's default.
const (
	ScopeBlueprint = "blueprint"
	ScopeSubstrate = "substrate"
)

// Span kinds (the `kind` key on a span).
const (
	SpanKindServer   = "server"
	SpanKindClient   = "client"
	SpanKindInternal = "internal"
)

// MetricSpec declares one metric family the owning service node emits on the metric tick. Per
// instrument: gauge → the value model is the instantaneous reading (state.Set); counter → the
// value model is a per-tick INCREMENT the interpreter accumulates (state.Add — cumulative, I3);
// histogram → the value model is a per-observation value, with required Buckets + LEStyle (classic
// `_bucket`, I4). Labels are series dimensions (capability matrix: const/const_str/enum only).
type MetricSpec struct {
	// Name is the metric family name, emitted as declared.
	Name string `yaml:"name"`
	// Source cites where the metric's name and behaviour come from (documentation or an observed
	// capture). It is required on a blueprint-owned custom metric and is never emitted.
	Source string `yaml:"source"`
	// Instrument is the metric kind: gauge, counter or histogram.
	Instrument string `yaml:"instrument"`
	// Unit is the unit string carried on a native OTLP metric; it does not rename the series.
	Unit string `yaml:"unit"`
	// Scope selects whether the series carries the blueprint selector (blueprint) or is a shared
	// substrate series with none (substrate). Omitted uses the owning node's default.
	Scope string `yaml:"scope"`
	// Labels are the series dimensions, one value model per label name. Only constant and enum
	// values are allowed, and a label's enum expands to every value on every tick.
	Labels map[string]ValueModel `yaml:"labels"`
	// Value is the value model: the reading of a gauge, the per-tick increment of a counter or one
	// observation of a histogram. Omit it only on a stock_of gauge.
	Value ValueModel `yaml:"value"`
	// Buckets are the histogram's upper bounds in increasing order (histogram only).
	Buckets []float64 `yaml:"buckets"`
	// LEStyle renders the histogram's le bucket-bound label: bare (default) or dotzero, which
	// writes a whole bound with a trailing .0 as the OTLP to Prometheus translation does.
	LEStyle string `yaml:"le_style"`
	// StockOf makes a gauge a derived stock (a level such as items in progress or a queue depth)
	// of a counter on the same service node, instead of reading its own value model. Gauge only;
	// value must be omitted. Without labels the gauge inherits the counter's label combinations
	// (across every same-named counter spec on the node); with labels it is a projection: each
	// label must be a counter label, each value one the counter takes, and a gauge series stocks
	// the arrivals of every counter series that matches it. Labels may not declare value_scale.
	StockOf *StockOf `yaml:"stock_of"`
	// CountOf makes a histogram observe, per label combination and tick, as many values as the
	// named counter on the same service node incremented (fractions carry deterministically), so
	// the histogram's _count tracks the counter. Histogram only; its labels must be a subset of
	// the counter's, each value one the counter takes, and a series observes the summed
	// increments of the counter series that match it (the whole counter when it has no labels).
	// Its labels may not declare rate_scale (the counter owns the rate). A histogram label may
	// select a subset of a counter label's values, so a blueprint that declares one counter spec
	// per series can pair each histogram spec with its own counter series (every combination must
	// match at least one counter series).
	CountOf string `yaml:"count_of"`
	// CountOfService names the service node whose counter count_of reads, for a counter declared
	// on another node of the same app workload (for example a counter a scenario targets by its
	// own service). Omitted reads the histogram's own node. The named service must be declared
	// before the histogram's service in the workload, so its increments are recorded the same
	// tick; there is no reference across workloads. Valid only with count_of.
	CountOfService string `yaml:"count_of_service"`
	// ShapeProfile gives every series of this metric its own business clock and curve, laid over
	// the node's inherited profile field by field (an enum value's shape_profile lays over this
	// in turn). It moves only a `shape` value, so any other value model rejects it; stock_of and
	// count_of read the counter's series and take none of their own. Omitted keeps the node's
	// load profile and the output unchanged.
	ShapeProfile *shape.Profile `yaml:"shape_profile"`
}

// hasClock reports whether the metric or any of its enum label values declares a shape_profile.
func (m MetricSpec) hasClock() bool {
	if m.ShapeProfile != nil {
		return true
	}
	for _, vm := range m.Labels {
		for _, e := range vm.Enum {
			if e.ShapeProfile != nil {
				return true
			}
		}
	}
	return false
}

// MaxObservationsPerTick bounds the histogram observations one series makes in one metric tick
// through rate_scale or count_of. Validation rejects a declaration whose nominal per-tick count
// exceeds it; the interpreter also clamps at it (excess observations are dropped, never carried)
// for rates that only grow at runtime, such as a master volume above 1 or an unbounded model.
const MaxObservationsPerTick = 10000

// MaxRateScale returns the largest per-combination product of the labels' rate_scale values
// (1 when none is declared).
func MaxRateScale(labels map[string]ValueModel) float64 {
	prod := 1.0
	for _, k := range sortedKeys(labels) {
		best := 0.0
		for _, e := range labels[k].Enum {
			r := 1.0
			if e.RateScale != nil {
				r = *e.RateScale
			}
			best = math.Max(best, r)
		}
		if len(labels[k].Enum) > 0 {
			prod *= best
		}
	}
	return prod
}

// StockOf derives a gauge as the stock of a counter, per gauge label combination. Set exactly
// one of DwellSeconds and Residence.
//
// DwellSeconds is the fluid stock: a first-order lag that settles at counter rate × DwellSeconds
// (Little's law). Each tick it moves toward that target by 1 - exp(-tick/DwellSeconds); the first
// tick seeds the target directly, so a restart shows no ramp. A paused workload adds no inflow and
// the stock drains.
//
// Residence is the item stock: the counter's increments arrive as whole items (fractions carry
// deterministically), each item stays for its own residence draw and then completes, so the
// gauge is an integer count that settles near counter rate × the residence mean. Draws hash the
// series and arrival tick, never the shape engine or the wall clock, so a run is a function of
// its tick sequence. The first tick seeds the steady-state population for that tick's rate (no
// ramp after a restart). A paused workload adds no items and completions drain the stock.
//
// StallMode and Field apply to the item stock only (a fluid stock rejects both). StallMode names
// an incident mode that slows completions: while it is active on the service at intensity i,
// every item's residence advances at (1 - i) of real time, so intensity 1 stops completions (the
// count and the oldest age grow) and recovery drains the stock as the frozen items finish. Unlike
// a now or source_read stall it is graded by intensity, and the mode name is not checked against
// the failure-mode registry. Field selects the reading: count (default) or oldest_age_seconds, the
// real seconds since the oldest item still in the stock arrived (0 when empty), which has no
// horizon. Two gauges with the same counter, labels, residence and stall mode share one item
// population. One series holds at most MaxStockItems items; arrivals above it are dropped, never
// carried. The population is in memory only: a restart re-seeds the steady state.
type StockOf struct {
	// Metric is the name of the counter, declared on the same service node, whose arrivals the
	// stock holds.
	Metric string `yaml:"metric"`
	// DwellSeconds selects the fluid stock: the mean seconds an item stays, so the level settles
	// at the counter rate times this value. Must be finite and above 0; set exactly one of
	// dwell_seconds and residence.
	DwellSeconds float64 `yaml:"dwell_seconds"`
	// Residence selects the item stock: each arriving item stays for its own residence draw
	// (median and 95th percentile, in seconds), so the gauge is a whole-number count.
	Residence *Lognormal `yaml:"residence"`
	// StallMode (item stock only) names the incident mode that slows completions: at intensity i
	// every item's residence advances at (1 - i) of real time, so intensity 1 stops completions.
	StallMode string `yaml:"stall_mode"`
	// Field (item stock only) selects the reading: count (default) or oldest_age_seconds, the
	// seconds since the oldest item still in the stock arrived (0 when empty).
	Field string `yaml:"field"`
}

// Item-stock readings (StockOf.Field).
const (
	StockFieldCount     = "count"
	StockFieldOldestAge = "oldest_age_seconds"
)

// MaxStockItems bounds the items one item-stock series holds. Arrivals above it are dropped.
const MaxStockItems = 100000

// EffectiveField is the item-stock reading, defaulting to count.
func (s StockOf) EffectiveField() string {
	if s.Field == "" {
		return StockFieldCount
	}
	return s.Field
}

// CustomMetrics is the sourced, blueprint-owned variant of inline metric declarations.
// Decode-time validation lets the public blueprint loader reject missing provenance without
// depending on any workload. Build-time callers must also validate programmatic declarations.
type CustomMetrics []MetricSpec

func (m *CustomMetrics) UnmarshalYAML(node *yaml.Node) error {
	data, err := yaml.Marshal(node)
	if err != nil {
		return err
	}
	var specs []MetricSpec
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&specs); err != nil {
		return err
	}
	for _, spec := range specs {
		if err := spec.ValidateCustom(); err != nil {
			return err
		}
	}
	*m = specs
	return nil
}

// ValidateCustom validates a blueprint-owned metric. Catalog/legacy specs retain Validate's
// existing contract; custom declarations require provenance without putting it on the wire.
func (m MetricSpec) ValidateCustom() error {
	if strings.TrimSpace(m.Source) == "" {
		return fmt.Errorf("metric %q: custom metric requires source citation", m.Name)
	}
	for _, k := range sortedKeys(m.Labels) {
		if highcard.Contains(k) {
			return fmt.Errorf("metric %q: high-card label key %q is forbidden", m.Name, k)
		}
	}
	return m.Validate()
}

// Validate enforces the metric's structure + the capability matrix on its labels + value.
func (m MetricSpec) Validate() error {
	if strings.TrimSpace(m.Name) == "" {
		return fmt.Errorf("metric: empty name")
	}
	switch m.Instrument {
	case InstrumentGauge, InstrumentCounter, InstrumentHistogram:
	default:
		return fmt.Errorf("metric %q: instrument %q invalid (want gauge|counter|histogram)", m.Name, m.Instrument)
	}
	switch m.Scope {
	case "", ScopeBlueprint, ScopeSubstrate:
	default:
		return fmt.Errorf("metric %q: scope %q invalid (want blueprint|substrate)", m.Name, m.Scope)
	}
	derivedValue := m.Value.Resource != nil || m.Value.Outcomes != nil
	for _, k := range sortedKeys(m.Labels) {
		if strings.TrimSpace(k) == "" {
			return fmt.Errorf("metric %q: empty label key", m.Name)
		}
		if err := checkValue(m.Labels[k], posMetricLabel, fmt.Sprintf("metric %q label %q", m.Name, k)); err != nil {
			return err
		}
		for _, e := range m.Labels[k].Enum {
			switch {
			case e.RateScale != nil && derivedValue:
				return fmt.Errorf("metric %q label %q value %q: rate_scale is not valid with a resource or outcomes value (it would distort the model's counts)", m.Name, k, e.Value)
			case e.ValueScale != nil && derivedValue:
				return fmt.Errorf("metric %q label %q value %q: value_scale is not valid with a resource or outcomes value (it would distort the model's readings)", m.Name, k, e.Value)
			case e.RateScale != nil && m.Instrument == InstrumentGauge:
				return fmt.Errorf("metric %q label %q value %q: rate_scale is not valid on a gauge (use value_scale)", m.Name, k, e.Value)
			case e.RateScale != nil && m.CountOf != "":
				return fmt.Errorf("metric %q label %q value %q: rate_scale is not valid with count_of (the counter owns the rate)", m.Name, k, e.Value)
			case e.ValueScale != nil && m.Value.Now != nil:
				return fmt.Errorf("metric %q label %q value %q: value_scale is not valid on a now gauge (it would scale the timestamp)", m.Name, k, e.Value)
			case e.ValueScale != nil && m.Instrument == InstrumentCounter:
				return fmt.Errorf("metric %q label %q value %q: value_scale is not valid on a counter (use rate_scale)", m.Name, k, e.Value)
			}
		}
	}
	if m.Value.Now != nil && m.Instrument != InstrumentGauge {
		return fmt.Errorf("metric %q: now is valid on a gauge value only", m.Name)
	}
	if err := m.validateCausalValue(); err != nil {
		return err
	}
	if err := m.validateStall(); err != nil {
		return err
	}
	if m.hasClock() {
		switch {
		case m.StockOf != nil || m.CountOf != "":
			return fmt.Errorf("metric %q: shape_profile is not valid with stock_of or count_of (the counter's series own the clock)", m.Name)
		case m.Value.Kind() != KindShape:
			return fmt.Errorf("metric %q: shape_profile needs a shape value (only a shape value reads the business clock)", m.Name)
		}
	}
	if m.Value.Shape != nil && m.Value.Shape.Batch != nil {
		switch {
		case m.Instrument != InstrumentCounter:
			return fmt.Errorf("metric %q: shape batch is valid on a counter only (a gauge reads a level, not arrivals)", m.Name)
		case m.StockOf != nil || m.CountOf != "":
			return fmt.Errorf("metric %q: shape batch is not valid with stock_of or count_of (they follow the counter's batches)", m.Name)
		case m.Value.Shape.IncidentValue != nil:
			return fmt.Errorf("metric %q: shape batch is not valid with incident_value (the incident intensity is read at the release tick, not per accumulated tick)", m.Name)
		}
	}
	if m.StockOf != nil {
		if err := m.validateStock(); err != nil {
			return err
		}
	} else if err := checkValue(m.Value, posMetricValue, fmt.Sprintf("metric %q value", m.Name)); err != nil {
		return err
	}
	if m.Instrument == InstrumentHistogram {
		if r := MaxRateScale(m.Labels); r > MaxObservationsPerTick {
			return fmt.Errorf("metric %q: rate_scale product %v exceeds %d observations per series per tick", m.Name, r, MaxObservationsPerTick)
		}
	}
	if m.CountOf != "" {
		if m.Instrument != InstrumentHistogram {
			return fmt.Errorf("metric %q: count_of is valid on a histogram only", m.Name)
		}
		if strings.TrimSpace(m.CountOf) == "" || m.CountOf == m.Name {
			return fmt.Errorf("metric %q: count_of must name another counter", m.Name)
		}
	}
	if m.CountOfService != "" && m.CountOf == "" {
		return fmt.Errorf("metric %q: count_of_service requires count_of", m.Name)
	}
	if m.Instrument == InstrumentHistogram {
		if len(m.Buckets) == 0 {
			return fmt.Errorf("metric %q: histogram requires buckets", m.Name)
		}
		for i := 1; i < len(m.Buckets); i++ {
			if m.Buckets[i] <= m.Buckets[i-1] {
				return fmt.Errorf("metric %q: buckets must be strictly ascending", m.Name)
			}
		}
		switch m.LEStyle {
		case "", LEStyleBare, LEStyleDotZero:
		default:
			return fmt.Errorf("metric %q: le_style %q invalid (want bare|dotzero)", m.Name, m.LEStyle)
		}
	} else if len(m.Buckets) > 0 {
		return fmt.Errorf("metric %q: buckets only valid for a histogram", m.Name)
	}
	return nil
}

// validateCausalValue enforces the capability matrix of the resource and outcomes values against
// the metric's instrument and labels (causal design 7.3).
func (m MetricSpec) validateCausalValue() error {
	switch m.Value.Kind() {
	case KindResource:
		f := m.Value.Resource.Field
		switch {
		case m.Instrument == InstrumentHistogram:
			return fmt.Errorf("metric %q: a resource value is not valid on a histogram (use a gauge field on a gauge or a counter field on a counter)", m.Name)
		case IsResourceCounterField(f) && m.Instrument != InstrumentCounter:
			return fmt.Errorf("metric %q: resource counter field %q is valid on a counter only", m.Name, f)
		case IsResourceGaugeField(f) && m.Instrument != InstrumentGauge:
			return fmt.Errorf("metric %q: resource gauge field %q is valid on a gauge only", m.Name, f)
		}
	case KindOutcomes:
		o := m.Value.Outcomes
		switch measure := o.EffectiveMeasure(); {
		case measure == MeasureCount && m.Instrument != InstrumentCounter:
			return fmt.Errorf("metric %q: outcomes measure count is valid on a counter only", m.Name)
		case measure == MeasureShare && m.Instrument != InstrumentGauge:
			return fmt.Errorf("metric %q: outcomes measure share is valid on a gauge only", m.Name)
		case measure == MeasureLatency && m.Instrument != InstrumentHistogram:
			return fmt.Errorf("metric %q: outcomes measure latency is valid on a histogram only", m.Name)
		}
		if o.StatusLabel != "" {
			lbl, ok := m.Labels[o.StatusLabel]
			if !ok || lbl.Kind() != KindEnum {
				return fmt.Errorf("metric %q: outcomes status_label %q must name an enum label of the same metric", m.Name, o.StatusLabel)
			}
			for _, e := range lbl.Enum {
				if !IsOutcomeStatus(e.Value) {
					return fmt.Errorf("metric %q: outcomes status_label %q value %q is not a status (want success|4xx|5xx or a three-digit HTTP status)", m.Name, o.StatusLabel, e.Value)
				}
			}
		}
	}
	return nil
}

func (m MetricSpec) validateStock() error {
	if m.Instrument != InstrumentGauge {
		return fmt.Errorf("metric %q: stock_of is valid on a gauge only", m.Name)
	}
	if m.Value.setCount() != 0 {
		return fmt.Errorf("metric %q: stock_of replaces the value model; omit value", m.Name)
	}
	for _, k := range sortedKeys(m.Labels) {
		for _, e := range m.Labels[k].Enum {
			if e.ValueScale != nil {
				return fmt.Errorf("metric %q label %q value %q: value_scale is not valid with stock_of (the stock counts items)", m.Name, k, e.Value)
			}
		}
	}
	so := m.StockOf
	if strings.TrimSpace(so.Metric) == "" || so.Metric == m.Name {
		return fmt.Errorf("metric %q: stock_of.metric must name another counter", m.Name)
	}
	if so.Residence == nil {
		if d := so.DwellSeconds; math.IsNaN(d) || math.IsInf(d, 0) || d <= 0 {
			return fmt.Errorf("metric %q: stock_of.dwell_seconds must be finite and > 0", m.Name)
		}
		if so.StallMode != "" || so.Field != "" {
			return fmt.Errorf("metric %q: stock_of.stall_mode and stock_of.field need stock_of.residence (the item stock)", m.Name)
		}
		return nil
	}
	if so.DwellSeconds != 0 {
		return fmt.Errorf("metric %q: stock_of sets exactly one of dwell_seconds and residence", m.Name)
	}
	if err := (ValueModel{Lognormal: so.Residence}).Validate(); err != nil {
		return fmt.Errorf("metric %q: stock_of.residence: %w", m.Name, err)
	}
	if md := so.StallMode; md != "" && strings.TrimSpace(md) != md {
		return fmt.Errorf("metric %q: stock_of.stall_mode %q must be a nonblank incident mode name without surrounding whitespace", m.Name, md)
	}
	switch so.EffectiveField() {
	case StockFieldCount, StockFieldOldestAge:
	default:
		return fmt.Errorf("metric %q: stock_of.field %q invalid (want count|oldest_age_seconds)", m.Name, so.Field)
	}
	return nil
}

// LogSpec declares one Loki log stream. StreamLabels are low-card stream dimensions (capability
// matrix: const/const_str/enum only — the sink also asserts I14/I15). Body fields are the JSON
// line content (any value model, including a high-card ref — the correlation glue rides here).
type LogSpec struct {
	// Source names the log line's stream within the service node; required and non-blank.
	Source string `yaml:"source"`
	// StreamLabels are the log stream's indexed labels. Only constant and enum values are allowed,
	// and a high-cardinality correlation ref is rejected here.
	StreamLabels map[string]ValueModel `yaml:"stream_labels"`
	// Body holds the fields of the JSON log line, one value model per field. Any value model is
	// allowed, including a high-cardinality correlation ref.
	Body map[string]ValueModel `yaml:"body"`
	// When, when set, writes this line only for the requests whose per-request draw falls in the
	// listed values, so one bundle's line set follows a drawn route or outcome. Omitted writes the
	// line for every request.
	When *LogCondition `yaml:"when"`
	// DwellSeconds, when set, gives the line its own event time. It is the seconds the request spent
	// before this line: the line's time is the previous written line's time plus its dwell, and the
	// bundle's last written line keeps the request's completion time, so earlier lines are
	// backdated and none is future-dated. The first written line's dwell places nothing (no line
	// precedes it). A line without a dwell shares the time of the line before it, and a bundle with
	// no dwell on any line keeps every line at the request's completion. A gated-out line adds no
	// dwell. A negative or NaN draw counts as 0 and a draw above 86400 (including +Inf) is capped at
	// 86400. The model is evaluated after the line's body, so it never shifts a body draw. Numeric
	// models only (const, int_range, float_range, normal, lognormal); it is a separate draw from any
	// body field that reports the same figure.
	//
	// A bundle is the lines of one request on the legacy path and one node's attempt on a causal
	// request. Only log timestamps move: spans and metric durations are unchanged. The bundle's
	// summed dwell is not validated; keep it well inside the log backend's out-of-order acceptance
	// window (about an hour by default for Loki), since the 86400 cap only stops a runaway model.
	DwellSeconds *ValueModel `yaml:"dwell_seconds"`
}

// MaxLogDwellSeconds caps one log line's dwell draw, so a runaway model cannot backdate a line out
// of any backend's accepted time window.
const MaxLogDwellSeconds = 86400

// DwellSecondsOf evaluates the line's dwell for ctx and returns it clamped to
// [0, MaxLogDwellSeconds] seconds. A line without a dwell reports (0, false) and draws nothing.
func (l LogSpec) DwellSecondsOf(ctx EvalCtx) (float64, bool) {
	if l.DwellSeconds == nil {
		return 0, false
	}
	n, _ := l.DwellSeconds.Eval(ctx)
	if math.IsNaN(n) || n < 0 {
		return 0, true
	}
	return math.Min(n, MaxLogDwellSeconds), true
}

// LogCondition gates one log line on a per-request draw. RequestDraw is an enum request_draw
// declared as the bundle's body fields declare the same key (same key, entries and weights;
// ValidateLogBundle enforces the weights), so the gate and those fields read the one value the
// request drew; the line is written
// when that value is one of In. The gate reads the request's seeded draw only, never the shape
// engine, and a gated-out line still evaluates its body, so gating never shifts another field's
// values.
type LogCondition struct {
	// RequestDraw is the per-request enum draw the gate reads, declared exactly as the bundle's
	// body fields declare the same key.
	RequestDraw RequestDraw `yaml:"request_draw"`
	// In lists the drawn values for which the line is written; at least one, each one of the
	// draw's enum values, none repeated.
	In []string `yaml:"in"`
}

func (c LogCondition) validate() error {
	d := c.RequestDraw
	if d.Format != "" {
		return fmt.Errorf("when: request_draw %q must be an enum (a formatted ID cannot select a line)", d.Key)
	}
	if err := d.validate(); err != nil {
		return fmt.Errorf("when: %w", err)
	}
	if len(c.In) == 0 {
		return fmt.Errorf("when: request_draw %q: in must list at least one drawn value", d.Key)
	}
	domain := make(map[string]bool, len(d.Enum))
	for _, e := range d.Enum {
		domain[e.Value] = true
	}
	seen := make(map[string]bool, len(c.In))
	for _, v := range c.In {
		if !domain[v] {
			return fmt.Errorf("when: request_draw %q: in value %q is not one of its enum values", d.Key, v)
		}
		if seen[v] {
			return fmt.Errorf("when: request_draw %q: in value %q repeats", d.Key, v)
		}
		seen[v] = true
	}
	return nil
}

// ValidateLogBundle checks that every when gate in one bundle (all the log lines a request can
// write, across nodes) selects on the value its bundle reports: every request_draw of the gate's
// key, in a body field or another gate, must be an enum with the gate's weights in the same order,
// so the same request draw picks the same entry index everywhere. Values may differ (two enums of
// one key and one weight list pair their values by index, such as a rule id and its token).
func ValidateLogBundle(logs []LogSpec) error {
	gates := map[string]LogCondition{}
	var keys []string
	for _, l := range logs {
		if l.When == nil {
			continue
		}
		if _, ok := gates[l.When.RequestDraw.Key]; !ok {
			keys = append(keys, l.When.RequestDraw.Key)
		}
		gates[l.When.RequestDraw.Key] = *l.When
	}
	if len(gates) == 0 {
		return nil
	}
	sort.Strings(keys)
	for _, key := range keys {
		gate := gates[key].RequestDraw
		check := func(d RequestDraw, where string) error {
			if d.Key != key {
				return nil
			}
			same := d.Format == "" && len(d.Enum) == len(gate.Enum)
			for i := 0; same && i < len(d.Enum); i++ {
				same = d.Enum[i].Weight == gate.Enum[i].Weight
			}
			if !same {
				return fmt.Errorf("log %s: request_draw %q must be an enum with the same weights in the same order as the when gate on that key", where, key)
			}
			return nil
		}
		for _, l := range logs {
			if l.When != nil {
				if err := check(l.When.RequestDraw, fmt.Sprintf("%q when", l.Source)); err != nil {
					return err
				}
			}
			for _, bk := range sortedKeys(l.Body) {
				if d := l.Body[bk].RequestDraw; d != nil {
					if err := check(*d, fmt.Sprintf("%q body %q", l.Source, bk)); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

// Writes reports whether the line is written for the request whose draw ctx carries: always
// without a When, otherwise when the request's drawn value is one of When.In.
func (l LogSpec) Writes(ctx EvalCtx) bool {
	if l.When == nil {
		return true
	}
	v := l.When.RequestDraw.eval(ctx.Draw)
	for _, in := range l.When.In {
		if v == in {
			return true
		}
	}
	return false
}

// Validate enforces the log's structure + the capability matrix on its stream labels + body.
func (l LogSpec) Validate() error {
	if strings.TrimSpace(l.Source) == "" {
		return fmt.Errorf("log: empty source")
	}
	for _, k := range sortedKeys(l.StreamLabels) {
		if err := checkValue(l.StreamLabels[k], posLabel, fmt.Sprintf("log %q stream_label %q", l.Source, k)); err != nil {
			return err
		}
	}
	for _, k := range sortedKeys(l.Body) {
		if err := checkValue(l.Body[k], posBody, fmt.Sprintf("log %q body %q", l.Source, k)); err != nil {
			return err
		}
	}
	if l.When != nil {
		if err := l.When.validate(); err != nil {
			return fmt.Errorf("log %q %w", l.Source, err)
		}
	}
	if d := l.DwellSeconds; d != nil {
		where := fmt.Sprintf("log %q dwell_seconds", l.Source)
		if err := checkValue(*d, posBody, where); err != nil {
			return err
		}
		switch d.Kind() {
		case KindConst, KindIntRange, KindFloatRange, KindNormal, KindLognormal:
		default:
			return fmt.Errorf("%s: must be numeric (const|int_range|float_range|normal|lognormal), got %q", where, d.Kind())
		}
	}
	return nil
}

// SpanSpec declares one span a service node emits as its participation in the one request trace.
// Attributes are span attributes (any value model, including a high-card ref).
type SpanSpec struct {
	// NameTemplate is the span name; {{attr.key}} placeholders are filled from the span's own
	// evaluated attributes. Required and non-blank. When the span merges into the node's own span
	// (see Kind) a non-empty result renames that span.
	NameTemplate string `yaml:"name_template"`
	// Kind is the span kind: server, client or internal. Omitted, or the node's own span kind,
	// merges this declaration's name and attributes into the node's own span; any other kind adds
	// a child span.
	Kind string `yaml:"kind"`
	// Attributes are extra span attributes, one value model per key. Any value model is allowed,
	// including a high-cardinality correlation ref; keys the workload stamps itself are rejected.
	Attributes map[string]ValueModel `yaml:"attributes"`
}

// Validate enforces the span's structure + the capability matrix on its attributes.
func (s SpanSpec) Validate() error {
	if strings.TrimSpace(s.NameTemplate) == "" {
		return fmt.Errorf("span: empty name_template")
	}
	switch s.Kind {
	case "", SpanKindServer, SpanKindClient, SpanKindInternal:
	default:
		return fmt.Errorf("span %q: kind %q invalid (want server|client|internal)", s.NameTemplate, s.Kind)
	}
	for _, k := range sortedKeys(s.Attributes) {
		if err := checkValue(s.Attributes[k], posAttr, fmt.Sprintf("span %q attr %q", s.NameTemplate, k)); err != nil {
			return err
		}
	}
	return nil
}

// Profile is a named, reusable bundle of metrics+logs+spans (the catalog ships generic, real-named
// profiles; blueprints compose them on a node and/or add inline custom declarations).
type Profile struct {
	// Name identifies the profile; required and non-blank.
	Name string `yaml:"name"`
	// Metrics are the metric families the profile adds to a node.
	Metrics []MetricSpec `yaml:"metrics"`
	// Logs are the log lines the profile adds to a node.
	Logs []LogSpec `yaml:"logs"`
	// Spans are the spans the profile adds to a node.
	Spans []SpanSpec `yaml:"spans"`
}

// Validate enforces the profile names something and every contained spec validates.
func (p Profile) Validate() error {
	if strings.TrimSpace(p.Name) == "" {
		return fmt.Errorf("profile: empty name")
	}
	if len(p.Metrics) == 0 && len(p.Logs) == 0 && len(p.Spans) == 0 {
		return fmt.Errorf("profile %q: declares no metrics/logs/spans", p.Name)
	}
	for i := range p.Metrics {
		if err := p.Metrics[i].Validate(); err != nil {
			return fmt.Errorf("profile %q: %w", p.Name, err)
		}
	}
	for i := range p.Logs {
		if err := p.Logs[i].Validate(); err != nil {
			return fmt.Errorf("profile %q: %w", p.Name, err)
		}
	}
	if err := ValidateLogBundle(p.Logs); err != nil {
		return fmt.Errorf("profile %q: %w", p.Name, err)
	}
	for i := range p.Spans {
		if err := p.Spans[i].Validate(); err != nil {
			return fmt.Errorf("profile %q: %w", p.Name, err)
		}
	}
	return nil
}

// sortedKeys returns the map keys in deterministic order (stable validation error reporting).
func sortedKeys(m map[string]ValueModel) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
