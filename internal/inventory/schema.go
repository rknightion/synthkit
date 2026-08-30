// SPDX-License-Identifier: AGPL-3.0-only

// Package inventory defines the canonical machine-readable telemetry shape shared by
// synthkit's dry-run exporter and the e2e capture receiver.
package inventory

import (
	"encoding/json"
	"io"
	"slices"
	"sort"
)

const (
	// SchemaVersion changes only when the JSON contract changes incompatibly.
	SchemaVersion = "synthkit.telemetry.inventory/v1alpha1"
	// DefaultValueLimit is the maximum number of distinct values retained for one
	// attribute key on one signal. The lexicographically smallest values win so the
	// result does not depend on arrival order.
	DefaultValueLimit = 64
)

const (
	TransportPrometheusRW1 = "prometheus_remote_write_v1"
	TransportPrometheusRW2 = "prometheus_remote_write_v2"
	TransportOTLPMetrics   = "otlp_metrics"
	TransportLoki          = "loki"
	TransportOTLPLogs      = "otlp_logs"
	TransportOTLPTraces    = "otlp_traces"
	TransportPyroscope     = "pyroscope"
	TransportSigil         = "sigil"
)

const (
	InstrumentUnknown   = "unknown"
	InstrumentGauge     = "gauge"
	InstrumentCounter   = "counter"
	InstrumentHistogram = "histogram"
	InstrumentSummary   = "summary"
)

// Schema is the canonical inventory document. Provenance, when present, applies to every
// entry in the document. A real capture records where and when it observed the estate; a
// synth-side export records the selector labels its own routing layer stamps.
type Schema struct {
	SchemaVersion string      `json:"schema_version"`
	Provenance    *Provenance `json:"provenance,omitempty"`
	Metrics       []Metric    `json:"metrics"`
	Logs          []Log       `json:"logs"`
	Traces        []Trace     `json:"traces"`
	Profiles      []Profile   `json:"profiles"`
	Sigil         []Sigil     `json:"sigil"`
	Receipts      []Receipt   `json:"receipts"`
}

type Provenance struct {
	Substrate    string `json:"substrate"`
	ChartVersion string `json:"chart_version"`
	CapturedAt   string `json:"captured_at"`
	// SelectorLabels are label keys the producing side stamps for its own routing rather
	// than because a vendor emits them. synthkit's composition root stamps the blueprint
	// selector on every blueprint-scoped series, stream and span, so no capture of collector
	// egress can ever carry it and it is not a name synthkit invented. The producer declares
	// the keys here — sourced from the constant the runner defines, never a literal written
	// twice — and the comparator removes them from that side before comparison. A synth-only
	// key that is NOT declared here is still a contradiction: that is the never-invent-a-name
	// rule and this field must not become a general suppression list.
	SelectorLabels []string `json:"selector_labels,omitempty"`
}

type Attribute struct {
	Key          string   `json:"key"`
	Values       []string `json:"values"`
	ValuesElided bool     `json:"values_elided"`
}

type Metric struct {
	Name            string      `json:"name"`
	Transports      []string    `json:"transports"`
	InstrumentTypes []string    `json:"instrument_types"`
	Labels          []Attribute `json:"labels"`
	Histogram       *Histogram  `json:"histogram,omitempty"`
}

type Histogram struct {
	Classic       bool      `json:"classic"`
	Native        bool      `json:"native"`
	BucketBounds  []float64 `json:"bucket_bounds"`
	NativeSchemas []int32   `json:"native_schemas"`
}

type Log struct {
	Source                 string      `json:"source"`
	Transport              string      `json:"transport"`
	StreamLabels           []Attribute `json:"stream_labels"`
	StructuredMetadataKeys []string    `json:"structured_metadata_keys"`
	// OptionalStreamLabelKeys and OptionalStructuredMetadataKeys record keys carried by
	// some, but not every, observed member of this logical log family. They preserve
	// a real ownerless or unscheduled-pod shape after family matching instead of letting
	// a union of all member keys silently make it look mandatory.
	OptionalStreamLabelKeys        []string `json:"optional_stream_label_keys,omitempty"`
	OptionalStructuredMetadataKeys []string `json:"optional_structured_metadata_keys,omitempty"`
	observations                   int
}

type Trace struct {
	Service            string      `json:"service"`
	ResourceAttributes []Attribute `json:"resource_attributes"`
	SpanNames          []string    `json:"span_names"`
	SpanAttributeKeys  []string    `json:"span_attribute_keys"`
}

type Profile struct {
	ProfileType string      `json:"profile_type"`
	Labels      []Attribute `json:"labels"`
}

type Sigil struct {
	IngestKind     string   `json:"ingest_kind"`
	OperationNames []string `json:"operation_names"`
}

type Receipt struct {
	Protocol string `json:"protocol"`
	Count    int    `json:"count"`
}

// New returns an empty, versioned schema whose collection fields encode as [] rather than null.
func New() Schema {
	return Schema{
		SchemaVersion: SchemaVersion,
		Metrics:       []Metric{},
		Logs:          []Log{},
		Traces:        []Trace{},
		Profiles:      []Profile{},
		Sigil:         []Sigil{},
		Receipts:      []Receipt{},
	}
}

// WriteJSON normalizes then writes stable, indented JSON with one trailing newline.
func (s Schema) WriteJSON(w io.Writer) error {
	s.Normalize()
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(s)
}

func addString(dst *[]string, value string) {
	if value == "" || slices.Contains(*dst, value) {
		return
	}
	*dst = append(*dst, value)
	sort.Strings(*dst)
}

func addInt32(dst *[]int32, value int32) {
	if slices.Contains(*dst, value) {
		return
	}
	*dst = append(*dst, value)
	slices.Sort(*dst)
}

func addFloat(dst *[]float64, value float64) {
	if slices.Contains(*dst, value) {
		return
	}
	*dst = append(*dst, value)
	slices.Sort(*dst)
}

func mergeAttribute(attrs *[]Attribute, key, value string) {
	if key == "" {
		return
	}
	i := slices.IndexFunc(*attrs, func(a Attribute) bool { return a.Key == key })
	if i < 0 {
		*attrs = append(*attrs, Attribute{Key: key, Values: []string{}})
		i = len(*attrs) - 1
	}
	a := &(*attrs)[i]
	if value != "" && !slices.Contains(a.Values, value) {
		a.Values = append(a.Values, value)
		sort.Strings(a.Values)
		if len(a.Values) > DefaultValueLimit {
			a.Values = a.Values[:DefaultValueLimit]
			a.ValuesElided = true
		}
	}
	sort.Slice(*attrs, func(i, j int) bool { return (*attrs)[i].Key < (*attrs)[j].Key })
}

// AddMetric merges one logical metric-family observation.
func (s *Schema) AddMetric(name, transport, instrument string, labels map[string]string, histogram *Histogram) {
	if name == "" {
		return
	}
	i := slices.IndexFunc(s.Metrics, func(m Metric) bool { return m.Name == name })
	if i < 0 {
		s.Metrics = append(s.Metrics, Metric{Name: name, Transports: []string{}, InstrumentTypes: []string{}, Labels: []Attribute{}})
		i = len(s.Metrics) - 1
	}
	m := &s.Metrics[i]
	addString(&m.Transports, transport)
	if instrument == "" {
		instrument = InstrumentUnknown
	}
	addString(&m.InstrumentTypes, instrument)
	for key, value := range labels {
		mergeAttribute(&m.Labels, key, value)
	}
	if histogram != nil {
		if m.Histogram == nil {
			m.Histogram = &Histogram{BucketBounds: []float64{}, NativeSchemas: []int32{}}
		}
		m.Histogram.Classic = m.Histogram.Classic || histogram.Classic
		m.Histogram.Native = m.Histogram.Native || histogram.Native
		for _, bound := range histogram.BucketBounds {
			addFloat(&m.Histogram.BucketBounds, bound)
		}
		for _, schema := range histogram.NativeSchemas {
			addInt32(&m.Histogram.NativeSchemas, schema)
		}
	}
	sort.Slice(s.Metrics, func(i, j int) bool { return s.Metrics[i].Name < s.Metrics[j].Name })
}

// AddLog merges one log-source observation. A source transported through Loki and OTLP is
// represented by two entries because their label and metadata contracts differ.
func (s *Schema) AddLog(source, transport string, streamLabels map[string]string, metadataKeys []string) {
	i := slices.IndexFunc(s.Logs, func(l Log) bool { return l.Source == source && l.Transport == transport })
	if i < 0 {
		s.Logs = append(s.Logs, Log{Source: source, Transport: transport, StreamLabels: []Attribute{}, StructuredMetadataKeys: []string{}})
		i = len(s.Logs) - 1
	}
	l := &s.Logs[i]
	if l.observations > 0 {
		existingLabels := make(map[string]struct{}, len(l.StreamLabels))
		for _, label := range l.StreamLabels {
			existingLabels[label.Key] = struct{}{}
			if _, present := streamLabels[label.Key]; !present {
				addString(&l.OptionalStreamLabelKeys, label.Key)
			}
		}
		for key := range streamLabels {
			if _, present := existingLabels[key]; !present {
				addString(&l.OptionalStreamLabelKeys, key)
			}
		}

		existingMetadata := indexStrings(l.StructuredMetadataKeys)
		currentMetadata := indexStrings(metadataKeys)
		for key := range existingMetadata {
			if _, present := currentMetadata[key]; !present {
				addString(&l.OptionalStructuredMetadataKeys, key)
			}
		}
		for key := range currentMetadata {
			if _, present := existingMetadata[key]; !present {
				addString(&l.OptionalStructuredMetadataKeys, key)
			}
		}
	}
	for key, value := range streamLabels {
		mergeAttribute(&l.StreamLabels, key, value)
	}
	for _, key := range metadataKeys {
		addString(&l.StructuredMetadataKeys, key)
	}
	l.observations++
	sort.Slice(s.Logs, func(i, j int) bool {
		if s.Logs[i].Transport == s.Logs[j].Transport {
			return s.Logs[i].Source < s.Logs[j].Source
		}
		return s.Logs[i].Transport < s.Logs[j].Transport
	})
}

func (s *Schema) AddTrace(service string, resourceAttrs map[string]string, spanName string, spanAttributeKeys []string) {
	i := slices.IndexFunc(s.Traces, func(t Trace) bool { return t.Service == service })
	if i < 0 {
		s.Traces = append(s.Traces, Trace{Service: service, ResourceAttributes: []Attribute{}, SpanNames: []string{}, SpanAttributeKeys: []string{}})
		i = len(s.Traces) - 1
	}
	t := &s.Traces[i]
	for key, value := range resourceAttrs {
		mergeAttribute(&t.ResourceAttributes, key, value)
	}
	addString(&t.SpanNames, spanName)
	for _, key := range spanAttributeKeys {
		addString(&t.SpanAttributeKeys, key)
	}
	sort.Slice(s.Traces, func(i, j int) bool { return s.Traces[i].Service < s.Traces[j].Service })
}

func (s *Schema) AddProfile(profileType string, labels map[string]string) {
	i := slices.IndexFunc(s.Profiles, func(p Profile) bool { return p.ProfileType == profileType })
	if i < 0 {
		s.Profiles = append(s.Profiles, Profile{ProfileType: profileType, Labels: []Attribute{}})
		i = len(s.Profiles) - 1
	}
	for key, value := range labels {
		mergeAttribute(&s.Profiles[i].Labels, key, value)
	}
	sort.Slice(s.Profiles, func(i, j int) bool { return s.Profiles[i].ProfileType < s.Profiles[j].ProfileType })
}

func (s *Schema) AddSigil(kind string, operationNames ...string) {
	if kind == "" {
		return
	}
	i := slices.IndexFunc(s.Sigil, func(v Sigil) bool { return v.IngestKind == kind })
	if i < 0 {
		s.Sigil = append(s.Sigil, Sigil{IngestKind: kind, OperationNames: []string{}})
		i = len(s.Sigil) - 1
	}
	for _, name := range operationNames {
		addString(&s.Sigil[i].OperationNames, name)
	}
	sort.Slice(s.Sigil, func(i, j int) bool { return s.Sigil[i].IngestKind < s.Sigil[j].IngestKind })
}

func (s *Schema) AddReceipt(protocol string, count int) {
	if protocol == "" || count == 0 {
		return
	}
	i := slices.IndexFunc(s.Receipts, func(r Receipt) bool { return r.Protocol == protocol })
	if i < 0 {
		s.Receipts = append(s.Receipts, Receipt{Protocol: protocol})
		i = len(s.Receipts) - 1
	}
	s.Receipts[i].Count += count
	sort.Slice(s.Receipts, func(i, j int) bool { return s.Receipts[i].Protocol < s.Receipts[j].Protocol })
}

// Normalize makes externally-constructed schemas deterministic and ensures nil collection
// fields encode as empty arrays. Add* already maintains this invariant incrementally.
func (s *Schema) Normalize() {
	if s.SchemaVersion == "" {
		s.SchemaVersion = SchemaVersion
	}
	if s.Metrics == nil {
		s.Metrics = []Metric{}
	}
	if s.Logs == nil {
		s.Logs = []Log{}
	}
	if s.Traces == nil {
		s.Traces = []Trace{}
	}
	if s.Profiles == nil {
		s.Profiles = []Profile{}
	}
	if s.Sigil == nil {
		s.Sigil = []Sigil{}
	}
	if s.Receipts == nil {
		s.Receipts = []Receipt{}
	}
	for i := range s.Metrics {
		normalizeStrings(&s.Metrics[i].Transports)
		normalizeStrings(&s.Metrics[i].InstrumentTypes)
		normalizeAttributes(&s.Metrics[i].Labels)
		if h := s.Metrics[i].Histogram; h != nil {
			slices.Sort(h.BucketBounds)
			h.BucketBounds = slices.Compact(h.BucketBounds)
			slices.Sort(h.NativeSchemas)
			h.NativeSchemas = slices.Compact(h.NativeSchemas)
			if h.BucketBounds == nil {
				h.BucketBounds = []float64{}
			}
			if h.NativeSchemas == nil {
				h.NativeSchemas = []int32{}
			}
		}
	}
	for i := range s.Logs {
		normalizeAttributes(&s.Logs[i].StreamLabels)
		normalizeStrings(&s.Logs[i].StructuredMetadataKeys)
		normalizeStrings(&s.Logs[i].OptionalStreamLabelKeys)
		normalizeStrings(&s.Logs[i].OptionalStructuredMetadataKeys)
	}
	for i := range s.Traces {
		normalizeAttributes(&s.Traces[i].ResourceAttributes)
		normalizeStrings(&s.Traces[i].SpanNames)
		normalizeStrings(&s.Traces[i].SpanAttributeKeys)
	}
	for i := range s.Profiles {
		normalizeAttributes(&s.Profiles[i].Labels)
	}
	for i := range s.Sigil {
		normalizeStrings(&s.Sigil[i].OperationNames)
	}
	sort.Slice(s.Metrics, func(i, j int) bool { return s.Metrics[i].Name < s.Metrics[j].Name })
	sort.Slice(s.Logs, func(i, j int) bool {
		if s.Logs[i].Transport == s.Logs[j].Transport {
			return s.Logs[i].Source < s.Logs[j].Source
		}
		return s.Logs[i].Transport < s.Logs[j].Transport
	})
	sort.Slice(s.Traces, func(i, j int) bool { return s.Traces[i].Service < s.Traces[j].Service })
	sort.Slice(s.Profiles, func(i, j int) bool { return s.Profiles[i].ProfileType < s.Profiles[j].ProfileType })
	sort.Slice(s.Sigil, func(i, j int) bool { return s.Sigil[i].IngestKind < s.Sigil[j].IngestKind })
	sort.Slice(s.Receipts, func(i, j int) bool { return s.Receipts[i].Protocol < s.Receipts[j].Protocol })
}

func normalizeStrings(values *[]string) {
	if *values == nil {
		*values = []string{}
		return
	}
	sort.Strings(*values)
	*values = slices.Compact(*values)
}

func normalizeAttributes(attrs *[]Attribute) {
	if *attrs == nil {
		*attrs = []Attribute{}
		return
	}
	for i := range *attrs {
		normalizeStrings(&(*attrs)[i].Values)
		if len((*attrs)[i].Values) > DefaultValueLimit {
			(*attrs)[i].Values = (*attrs)[i].Values[:DefaultValueLimit]
			(*attrs)[i].ValuesElided = true
		}
	}
	sort.Slice(*attrs, func(i, j int) bool { return (*attrs)[i].Key < (*attrs)[j].Key })
}

// Subset is the legacy text-dump correlation seam used by the Docker e2e harness. It checks
// signal identity only; the fidelity audit uses Diff for typed shape findings.
func (s Schema) Subset(of Schema) []string {
	var missing []string
	metricNames := make(map[string]struct{}, len(of.Metrics))
	for _, metric := range of.Metrics {
		metricNames[metric.Name] = struct{}{}
	}
	for _, metric := range s.Metrics {
		if _, ok := metricNames[metric.Name]; !ok {
			missing = append(missing, "metric: "+metric.Name)
		}
	}
	logSources := make(map[string]struct{}, len(of.Logs))
	for _, log := range of.Logs {
		logSources[log.Source] = struct{}{}
	}
	for _, log := range s.Logs {
		if _, ok := logSources[log.Source]; !ok {
			missing = append(missing, "log source: "+log.Source)
		}
	}
	traceServices := make(map[string]struct{}, len(of.Traces))
	for _, trace := range of.Traces {
		traceServices[trace.Service] = struct{}{}
	}
	for _, trace := range s.Traces {
		if _, ok := traceServices[trace.Service]; !ok {
			missing = append(missing, "trace service: "+trace.Service)
		}
	}
	sigilKinds := make(map[string]struct{}, len(of.Sigil))
	for _, sigil := range of.Sigil {
		sigilKinds[sigil.IngestKind] = struct{}{}
	}
	for _, sigil := range s.Sigil {
		if _, ok := sigilKinds[sigil.IngestKind]; !ok {
			missing = append(missing, "sigil: "+sigil.IngestKind)
		}
	}
	sort.Strings(missing)
	return missing
}
