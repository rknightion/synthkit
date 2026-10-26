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
	Name       string                `yaml:"name"`
	Source     string                `yaml:"source"` // config-only citation; required by ValidateCustom
	Instrument string                `yaml:"instrument"`
	Unit       string                `yaml:"unit"`
	Scope      string                `yaml:"scope"`
	Labels     map[string]ValueModel `yaml:"labels"`
	Value      ValueModel            `yaml:"value"`
	Buckets    []float64             `yaml:"buckets"`
	LEStyle    string                `yaml:"le_style"`
	// StockOf makes a gauge a derived stock (a level such as items in progress or a queue depth)
	// of a counter on the same service node, instead of reading its own value model. Gauge only;
	// value and labels must be omitted (the gauge inherits the counter's label combinations).
	StockOf *StockOf `yaml:"stock_of"`
	// CountOf makes a histogram observe, per label combination and tick, as many values as the
	// named counter on the same service node incremented (fractions carry deterministically), so
	// the histogram's _count tracks the counter. Histogram only; its labels must be a subset of
	// the counter's, each with the counter's value domain, and a series observes the summed
	// increments of the counter series that match it (the whole counter when it has no labels).
	// Its labels may not declare rate_scale (the counter owns the rate).
	CountOf string `yaml:"count_of"`
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

// StockOf derives a gauge as the stock of a counter: a first-order lag that settles at
// counter rate × DwellSeconds (Little's law), per label combination. Each tick it moves toward
// that target by 1 - exp(-tick/DwellSeconds); the first tick seeds the target directly, so a
// restart shows no ramp. A paused workload adds no inflow and the stock drains.
type StockOf struct {
	Metric       string  `yaml:"metric"`        // counter name on the same service node
	DwellSeconds float64 `yaml:"dwell_seconds"` // mean time an item stays in the stock (> 0)
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
	if len(m.Labels) != 0 {
		return fmt.Errorf("metric %q: stock_of inherits the counter's labels; omit labels", m.Name)
	}
	if strings.TrimSpace(m.StockOf.Metric) == "" || m.StockOf.Metric == m.Name {
		return fmt.Errorf("metric %q: stock_of.metric must name another counter", m.Name)
	}
	if d := m.StockOf.DwellSeconds; math.IsNaN(d) || math.IsInf(d, 0) || d <= 0 {
		return fmt.Errorf("metric %q: stock_of.dwell_seconds must be finite and > 0", m.Name)
	}
	return nil
}

// LogSpec declares one Loki log stream. StreamLabels are low-card stream dimensions (capability
// matrix: const/const_str/enum only — the sink also asserts I14/I15). Body fields are the JSON
// line content (any value model, including a high-card ref — the correlation glue rides here).
type LogSpec struct {
	Source       string                `yaml:"source"`
	StreamLabels map[string]ValueModel `yaml:"stream_labels"`
	Body         map[string]ValueModel `yaml:"body"`
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
	return nil
}

// SpanSpec declares one span a service node emits as its participation in the one request trace.
// Attributes are span attributes (any value model, including a high-card ref).
type SpanSpec struct {
	NameTemplate string                `yaml:"name_template"`
	Kind         string                `yaml:"kind"`
	Attributes   map[string]ValueModel `yaml:"attributes"`
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
	Name    string       `yaml:"name"`
	Metrics []MetricSpec `yaml:"metrics"`
	Logs    []LogSpec    `yaml:"logs"`
	Spans   []SpanSpec   `yaml:"spans"`
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
