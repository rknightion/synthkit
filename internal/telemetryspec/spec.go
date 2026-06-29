// SPDX-License-Identifier: AGPL-3.0-only

package telemetryspec

import (
	"fmt"
	"sort"
	"strings"
)

// Instrument kinds (the `instrument` key on a metric).
const (
	InstrumentGauge     = "gauge"
	InstrumentCounter   = "counter"
	InstrumentHistogram = "histogram"
)

// le_style values — how a histogram's `le` bucket-bound label is rendered. They map to
// state.LEStyle at emit time (the workload, F4): "bare" → state.LEBare (Prometheus-native scrape),
// "dotzero" → state.LEDotZero (OTLP→Prometheus span-metric translation). Default "" ⇒ bare.
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
	Instrument string                `yaml:"instrument"`
	Unit       string                `yaml:"unit"`
	Scope      string                `yaml:"scope"`
	Labels     map[string]ValueModel `yaml:"labels"`
	Value      ValueModel            `yaml:"value"`
	Buckets    []float64             `yaml:"buckets"`
	LEStyle    string                `yaml:"le_style"`
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
	for _, k := range sortedKeys(m.Labels) {
		if strings.TrimSpace(k) == "" {
			return fmt.Errorf("metric %q: empty label key", m.Name)
		}
		if err := checkValue(m.Labels[k], posLabel, fmt.Sprintf("metric %q label %q", m.Name, k)); err != nil {
			return err
		}
	}
	if err := checkValue(m.Value, posMetricValue, fmt.Sprintf("metric %q value", m.Name)); err != nil {
		return err
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
