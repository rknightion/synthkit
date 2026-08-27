// SPDX-License-Identifier: AGPL-3.0-only

package inventory

import (
	"math"
	"sort"
	"strconv"
	"strings"
)

// FindingKind identifies the shape of a difference between a synthetic
// inventory and a reality inventory.
type FindingKind string

const (
	KindMissingMetric           FindingKind = "missing_metric"
	KindExtraMetric             FindingKind = "extra_metric"
	KindUnexpectedLabelKey      FindingKind = "unexpected_label_key"
	KindLabelValueContradiction FindingKind = "label_value_contradiction"
	KindInstrumentMismatch      FindingKind = "instrument_mismatch"
	KindBucketBoundMismatch     FindingKind = "bucket_bound_mismatch"
	KindExtraLog                FindingKind = "extra_log"
	KindExtraTrace              FindingKind = "extra_trace"
	KindExtraProfile            FindingKind = "extra_profile"
	KindExtraSigil              FindingKind = "extra_sigil"
)

// Disposition says which side of the comparison is unsupported by the other
// side. A contradiction is a synthetic claim absent from reality; a coverage
// gap is a reality shape not represented by synthkit.
type Disposition string

const (
	DispositionContradiction Disposition = "contradiction"
	DispositionCoverageGap   Disposition = "coverage_gap"
)

// Finding is one typed difference between a synthetic inventory and a reality
// inventory. Values are normalized and sorted by Diff; empty value lists are
// represented as empty slices rather than nil slices.
type Finding struct {
	Kind          FindingKind `json:"kind"`
	Disposition   Disposition `json:"disposition"`
	Signal        string      `json:"signal"`
	Field         string      `json:"field"`
	SynthValues   []string    `json:"synth_values"`
	RealityValues []string    `json:"reality_values"`
}

// Diff compares every signal class carried by synth and reality. The inventory
// is a structural contract for the signal catalogue, so families are matched
// by their logical identity independently of transport details. The reality
// inventory is the comparison scope: a family absent from reality is not
// evidence of drift, while a reality-only family is a coverage gap.
//
// Absent evidence is never a contradiction. Attribute values marked
// ValuesElided are deliberately treated as open-ended, an instrument-type set
// that is exactly the unknown sentinel carries no evidence about instrument
// shape, and label values compare as a subset because a capture observes only
// part of the value space an emitter models. Diff reports only differences that
// remain certain after accounting for what the reality producer could observe.
func Diff(synth, reality Schema) []Finding {
	out := make([]Finding, 0)
	diffMetrics(&out, synth.Metrics, reality.Metrics)
	diffLogs(&out, synth.Logs, reality.Logs)
	diffTraces(&out, synth.Traces, reality.Traces)
	diffProfiles(&out, synth.Profiles, reality.Profiles)
	diffSigil(&out, synth.Sigil, reality.Sigil)
	sortFindings(out)
	return out
}

func diffMetrics(out *[]Finding, synthMetrics, realityMetrics []Metric) {
	synthIndex := indexMetrics(synthMetrics)
	realityIndex := indexMetrics(realityMetrics)

	names := sortedMetricNames(realityIndex)

	for _, name := range names {
		synthMetric, inSynth := synthIndex[name]
		realityMetric := realityIndex[name]
		if !inSynth {
			appendFamilyCoverage(out, KindExtraMetric, name, "name")
		} else {
			diffMetric(out, synthMetric, realityMetric)
		}
	}
}

func sortedMetricNames(metrics map[string]metricView) []string {
	names := make([]string, 0, len(metrics))
	for name := range metrics {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func appendFamilyCoverage(out *[]Finding, kind FindingKind, signal, field string, values ...string) {
	if len(values) == 0 {
		values = []string{signal}
	}
	*out = append(*out, Finding{
		Kind:          kind,
		Disposition:   DispositionCoverageGap,
		Signal:        signal,
		Field:         field,
		SynthValues:   []string{},
		RealityValues: cloneStrings(values),
	})
}

type metricView struct {
	name        string
	instruments map[string]struct{}
	labels      map[string]attributeView
	histogram   histogramView
}

type attributeView struct {
	values map[string]struct{}
	elided bool
}

type histogramView struct {
	classic       bool
	native        bool
	bounds        map[uint64]float64
	nativeSchemas map[int32]struct{}
}

func indexMetrics(metrics []Metric) map[string]metricView {
	out := make(map[string]metricView, len(metrics))
	for _, metric := range metrics {
		view, ok := out[metric.Name]
		if !ok {
			view = metricView{
				name:        metric.Name,
				instruments: make(map[string]struct{}),
				labels:      make(map[string]attributeView),
			}
		}
		for _, instrument := range metric.InstrumentTypes {
			view.instruments[instrument] = struct{}{}
		}
		for _, label := range metric.Labels {
			attribute, ok := view.labels[label.Key]
			if !ok {
				attribute = attributeView{values: make(map[string]struct{})}
			}
			for _, value := range label.Values {
				attribute.values[value] = struct{}{}
			}
			attribute.elided = attribute.elided || label.ValuesElided
			view.labels[label.Key] = attribute
		}
		if metric.Histogram != nil {
			view.histogram.classic = view.histogram.classic || metric.Histogram.Classic
			view.histogram.native = view.histogram.native || metric.Histogram.Native
			if view.histogram.bounds == nil {
				view.histogram.bounds = make(map[uint64]float64)
			}
			for _, bound := range metric.Histogram.BucketBounds {
				view.histogram.bounds[floatKey(bound)] = canonicalFloat(bound)
			}
			if view.histogram.nativeSchemas == nil {
				view.histogram.nativeSchemas = make(map[int32]struct{})
			}
			for _, schema := range metric.Histogram.NativeSchemas {
				view.histogram.nativeSchemas[schema] = struct{}{}
			}
		}
		out[metric.Name] = view
	}
	return out
}

type logIdentity struct {
	// A recorded Source is deliberately absent: capture-specific source names are
	// provenance exemplars.
	//
	// family is the SHAPE-derived family name, and it is the identity whenever a shape rule
	// recognises the entry. It has to be, because the raw key set cannot both identify a
	// family and be compared within one: while it was the identity, two recorded shapes of
	// one family — a pod with a Deployment owner and a pod without — were two families, so a
	// pod-log entry could never contradict and could never be confirmed, and the label-key and
	// metadata-key comparisons below were unreachable for it.
	//
	// transport and structural key shape still identify every entry no shape rule recognises,
	// so an unclassified lane behaves exactly as before.
	transport string
	family    string
	labelKeys string
	metadata  string
}

func identifyLog(log Log, labels map[string]attributeView, metadata map[string]struct{}) logIdentity {
	if family, ok := ShapeLogFamily(log); ok {
		return logIdentity{transport: log.Transport, family: family}
	}
	return logIdentity{
		transport: log.Transport,
		labelKeys: encodeKeys(sortedAttributeKeys(labels)),
		metadata:  encodeKeys(sortedStrings(metadata)),
	}
}

type logView struct {
	signal       string
	streamLabels map[string]attributeView
	metadata     map[string]struct{}
}

func indexLogs(logs []Log) map[logIdentity]logView {
	out := make(map[logIdentity]logView, len(logs))
	for _, log := range logs {
		labels := indexAttributes(log.StreamLabels)
		metadata := indexStrings(log.StructuredMetadataKeys)
		key := identifyLog(log, labels, metadata)
		view, ok := out[key]
		if !ok {
			view = logView{
				signal:       logSignal(key),
				streamLabels: make(map[string]attributeView),
				metadata:     make(map[string]struct{}),
			}
		}
		mergeAttributeViews(view.streamLabels, labels)
		for value := range metadata {
			view.metadata[value] = struct{}{}
		}
		out[key] = view
	}
	return out
}

// logSignal is the readable identity used in findings. A shape-recognised family reads by
// name; otherwise empty structural shapes retain the transport-only signal and populated
// shapes add their sorted schema field keys so two families sharing a transport remain
// distinguishable.
func logSignal(key logIdentity) string {
	if key.family != "" {
		return key.transport + "[family=" + key.family + "]"
	}
	parts := make([]string, 0, 2)
	if key.labelKeys != "" {
		parts = append(parts, "stream_labels="+strings.ReplaceAll(key.labelKeys, "\x00", ","))
	}
	if key.metadata != "" {
		parts = append(parts, "structured_metadata_keys="+strings.ReplaceAll(key.metadata, "\x00", ","))
	}
	if len(parts) == 0 {
		return key.transport
	}
	return key.transport + "[" + strings.Join(parts, ";") + "]"
}

type traceView struct {
	signal            string
	resourceAttrs     map[string]attributeView
	spanNames         map[string]struct{}
	spanAttributeKeys map[string]struct{}
}

func indexTraces(traces []Trace) map[string]traceView {
	out := make(map[string]traceView, len(traces))
	for _, trace := range traces {
		view, ok := out[trace.Service]
		if !ok {
			view = traceView{
				signal:            trace.Service,
				resourceAttrs:     make(map[string]attributeView),
				spanNames:         make(map[string]struct{}),
				spanAttributeKeys: make(map[string]struct{}),
			}
		}
		mergeAttributeViews(view.resourceAttrs, indexAttributes(trace.ResourceAttributes))
		mergeStringSet(view.spanNames, trace.SpanNames)
		mergeStringSet(view.spanAttributeKeys, trace.SpanAttributeKeys)
		out[trace.Service] = view
	}
	return out
}

type profileView struct {
	signal string
	labels map[string]attributeView
}

func indexProfiles(profiles []Profile) map[string]profileView {
	out := make(map[string]profileView, len(profiles))
	for _, profile := range profiles {
		view, ok := out[profile.ProfileType]
		if !ok {
			view = profileView{signal: profile.ProfileType, labels: make(map[string]attributeView)}
		}
		mergeAttributeViews(view.labels, indexAttributes(profile.Labels))
		out[profile.ProfileType] = view
	}
	return out
}

type sigilView struct {
	signal         string
	operationNames map[string]struct{}
}

func indexSigil(sigil []Sigil) map[string]sigilView {
	out := make(map[string]sigilView, len(sigil))
	for _, value := range sigil {
		view, ok := out[value.IngestKind]
		if !ok {
			view = sigilView{signal: value.IngestKind, operationNames: make(map[string]struct{})}
		}
		mergeStringSet(view.operationNames, value.OperationNames)
		out[value.IngestKind] = view
	}
	return out
}

func diffLogs(out *[]Finding, synthLogs, realityLogs []Log) {
	synthIndex := indexLogs(synthLogs)
	realityIndex := indexLogs(realityLogs)
	keys := sortedLogIdentities(realityIndex)
	for _, key := range keys {
		reality := realityIndex[key]
		synth, ok := synthIndex[key]
		if !ok {
			appendFamilyCoverage(out, KindExtraLog, reality.signal, "transport")
			continue
		}
		diffAttributes(out, reality.signal, "stream_labels", synth.streamLabels, reality.streamLabels)
		appendDirectional(
			out,
			KindUnexpectedLabelKey,
			reality.signal,
			"structured_metadata_keys",
			sortedStrings(synth.metadata),
			sortedStrings(reality.metadata),
			true,
			true,
		)
	}
}

func diffTraces(out *[]Finding, synthTraces, realityTraces []Trace) {
	synthIndex := indexTraces(synthTraces)
	realityIndex := indexTraces(realityTraces)
	for _, signal := range sortedTraceSignals(realityIndex) {
		reality := realityIndex[signal]
		synth, ok := synthIndex[signal]
		if !ok {
			appendFamilyCoverage(out, KindExtraTrace, reality.signal, "service")
			continue
		}
		diffAttributes(out, reality.signal, "resource_attributes", synth.resourceAttrs, reality.resourceAttrs)
		appendDirectional(
			out,
			KindInstrumentMismatch,
			reality.signal,
			"span_names",
			sortedStrings(synth.spanNames),
			sortedStrings(reality.spanNames),
			true,
			true,
		)
		appendDirectional(
			out,
			KindUnexpectedLabelKey,
			reality.signal,
			"span_attribute_keys",
			sortedStrings(synth.spanAttributeKeys),
			sortedStrings(reality.spanAttributeKeys),
			true,
			true,
		)
	}
}

func diffProfiles(out *[]Finding, synthProfiles, realityProfiles []Profile) {
	synthIndex := indexProfiles(synthProfiles)
	realityIndex := indexProfiles(realityProfiles)
	for _, signal := range sortedProfileSignals(realityIndex) {
		reality := realityIndex[signal]
		synth, ok := synthIndex[signal]
		if !ok {
			appendFamilyCoverage(out, KindExtraProfile, reality.signal, "profile_type")
			continue
		}
		diffAttributes(out, reality.signal, "labels", synth.labels, reality.labels)
	}
}

func diffSigil(out *[]Finding, synthSigil, realitySigil []Sigil) {
	synthIndex := indexSigil(synthSigil)
	realityIndex := indexSigil(realitySigil)
	for _, signal := range sortedSigilSignals(realityIndex) {
		reality := realityIndex[signal]
		synth, ok := synthIndex[signal]
		if !ok {
			appendFamilyCoverage(out, KindExtraSigil, reality.signal, "ingest_kind")
			continue
		}
		appendDirectional(
			out,
			KindInstrumentMismatch,
			reality.signal,
			"operation_names",
			sortedStrings(synth.operationNames),
			sortedStrings(reality.operationNames),
			true,
			true,
		)
	}
}

func diffAttributes(out *[]Finding, signal, field string, synth, reality map[string]attributeView) {
	synthKeys := sortedAttributeKeys(synth)
	realityKeys := sortedAttributeKeys(reality)
	appendDirectional(out, KindUnexpectedLabelKey, signal, field, synthKeys, realityKeys, true, true)
	for _, key := range intersection(synthKeys, realityKeys) {
		appendLabelValueSubset(out, signal, field+"."+key, synth[key], reality[key])
	}
}

// appendLabelValueSubset reports label-value evidence in one direction only. A capture
// observes one account, region and moment, so it sees a subset of the value space an emitter
// deliberately models: synth covering more values than reality observed is correct and stays
// silent. Only a reality value synthkit cannot emit is a contradiction. An elided or empty
// value set on either side is absent evidence and is not compared at all.
func appendLabelValueSubset(out *[]Finding, signal, field string, synth, reality attributeView) {
	if synth.elided || reality.elided || len(synth.values) == 0 || len(reality.values) == 0 {
		return
	}
	synthValues := sortedStrings(synth.values)
	realityValues := sortedStrings(reality.values)
	if len(difference(realityValues, synthValues)) == 0 {
		return
	}
	*out = append(*out, Finding{
		Kind:          KindLabelValueContradiction,
		Disposition:   DispositionContradiction,
		Signal:        signal,
		Field:         field,
		SynthValues:   synthValues,
		RealityValues: realityValues,
	})
}

// instrumentEvidenceAbsent reports whether an instrument-type set is exactly the unknown
// sentinel. A producer that could not observe an instrument type records that sentinel, so the
// set carries no evidence about instrument shape and must never contradict. A set recording any
// real type, including one mixed with the sentinel, is evidence and is compared normally.
func instrumentEvidenceAbsent(instruments []string) bool {
	return len(instruments) == 1 && instruments[0] == InstrumentUnknown
}

func indexAttributes(attributes []Attribute) map[string]attributeView {
	out := make(map[string]attributeView, len(attributes))
	for _, attribute := range attributes {
		view, ok := out[attribute.Key]
		if !ok {
			view = attributeView{values: make(map[string]struct{})}
		}
		for _, value := range attribute.Values {
			view.values[value] = struct{}{}
		}
		view.elided = view.elided || attribute.ValuesElided
		out[attribute.Key] = view
	}
	return out
}

func mergeAttributeViews(dst, src map[string]attributeView) {
	for key, source := range src {
		target, ok := dst[key]
		if !ok {
			target = attributeView{values: make(map[string]struct{})}
		}
		for value := range source.values {
			target.values[value] = struct{}{}
		}
		target.elided = target.elided || source.elided
		dst[key] = target
	}
}

func indexStrings(values []string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		out[value] = struct{}{}
	}
	return out
}

func mergeStringSet(dst map[string]struct{}, values []string) {
	for _, value := range values {
		dst[value] = struct{}{}
	}
}

func encodeKeys(values []string) string {
	return strings.Join(values, "\x00")
}

func sortedLogIdentities(logs map[logIdentity]logView) []logIdentity {
	keys := make([]logIdentity, 0, len(logs))
	for key := range logs {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].transport != keys[j].transport {
			return keys[i].transport < keys[j].transport
		}
		if keys[i].family != keys[j].family {
			return keys[i].family < keys[j].family
		}
		if keys[i].labelKeys != keys[j].labelKeys {
			return keys[i].labelKeys < keys[j].labelKeys
		}
		return keys[i].metadata < keys[j].metadata
	})
	return keys
}

func sortedTraceSignals(traces map[string]traceView) []string {
	signals := make([]string, 0, len(traces))
	for signal := range traces {
		signals = append(signals, signal)
	}
	sort.Strings(signals)
	return signals
}

func sortedProfileSignals(profiles map[string]profileView) []string {
	signals := make([]string, 0, len(profiles))
	for signal := range profiles {
		signals = append(signals, signal)
	}
	sort.Strings(signals)
	return signals
}

func sortedSigilSignals(sigil map[string]sigilView) []string {
	signals := make([]string, 0, len(sigil))
	for signal := range sigil {
		signals = append(signals, signal)
	}
	sort.Strings(signals)
	return signals
}

func diffMetric(out *[]Finding, synth, reality metricView) {
	synthInstruments := sortedStrings(synth.instruments)
	realityInstruments := sortedStrings(reality.instruments)
	instrumentEvidence := !instrumentEvidenceAbsent(synthInstruments) && !instrumentEvidenceAbsent(realityInstruments)
	appendDirectional(out, KindInstrumentMismatch, synth.name, "instrument_types", synthInstruments, realityInstruments, instrumentEvidence, true)
	appendDirectional(
		out,
		KindInstrumentMismatch,
		synth.name,
		"histogram.representations",
		histogramRepresentations(synth.histogram),
		histogramRepresentations(reality.histogram),
		true,
		true,
	)
	appendDirectional(
		out,
		KindInstrumentMismatch,
		synth.name,
		"histogram.native_schemas",
		formatInt32s(synth.histogram.nativeSchemas),
		formatInt32s(reality.histogram.nativeSchemas),
		true,
		true,
	)

	synthKeys := sortedAttributeKeys(synth.labels)
	realityKeys := sortedAttributeKeys(reality.labels)
	appendDirectional(out, KindUnexpectedLabelKey, synth.name, "labels", synthKeys, realityKeys, true, true)

	for _, key := range intersection(synthKeys, realityKeys) {
		appendLabelValueSubset(out, synth.name, "labels."+key, synth.labels[key], reality.labels[key])
	}

	// An empty bucket-bound set on either side is absent evidence, not a claim: a classic
	// histogram always has bounds, so recording none means the producer could not observe them.
	// A corpus document elides the `le` label values, so a family folded out of already-recorded
	// component series proves the classic representation and carries no bounds at all.
	synthBounds := sortedBounds(synth.histogram.bounds)
	realityBounds := sortedBounds(reality.histogram.bounds)
	if len(synthBounds) > 0 && len(realityBounds) > 0 {
		appendDirectional(out, KindBucketBoundMismatch, synth.name, "histogram.bucket_bounds", formatBounds(synthBounds), formatBounds(realityBounds), true, true)
	}
}

func histogramRepresentations(histogram histogramView) []string {
	representations := make([]string, 0, 2)
	if histogram.classic {
		representations = append(representations, "classic")
	}
	if histogram.native {
		representations = append(representations, "native")
	}
	return representations
}

// appendDirectional emits one finding for each certain direction of a set
// mismatch. synthCanProveMissing and realityCanProveMissing allow callers to
// suppress a direction when the opposite inventory is explicitly elided.
func appendDirectional(out *[]Finding, kind FindingKind, signal, field string, synthValues, realityValues []string, synthCanProveMissing, realityCanProveMissing bool) {
	synthOnly := difference(synthValues, realityValues)
	realityOnly := difference(realityValues, synthValues)
	if len(synthOnly) > 0 && synthCanProveMissing {
		*out = append(*out, Finding{
			Kind:          kind,
			Disposition:   DispositionContradiction,
			Signal:        signal,
			Field:         field,
			SynthValues:   cloneStrings(synthValues),
			RealityValues: cloneStrings(realityValues),
		})
	}
	if len(realityOnly) > 0 && realityCanProveMissing {
		*out = append(*out, Finding{
			Kind:          kind,
			Disposition:   DispositionCoverageGap,
			Signal:        signal,
			Field:         field,
			SynthValues:   cloneStrings(synthValues),
			RealityValues: cloneStrings(realityValues),
		})
	}
}

func sortedAttributeKeys(labels map[string]attributeView) []string {
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedStrings(values map[string]struct{}) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func difference(a, b []string) []string {
	bSet := make(map[string]struct{}, len(b))
	for _, value := range b {
		bSet[value] = struct{}{}
	}
	out := make([]string, 0, len(a))
	for _, value := range a {
		if _, found := bSet[value]; !found {
			out = append(out, value)
		}
	}
	return out
}

func intersection(a, b []string) []string {
	bSet := make(map[string]struct{}, len(b))
	for _, value := range b {
		bSet[value] = struct{}{}
	}
	out := make([]string, 0)
	for _, value := range a {
		if _, found := bSet[value]; found {
			out = append(out, value)
		}
	}
	return out
}

func sortedBounds(bounds map[uint64]float64) []float64 {
	out := make([]float64, 0, len(bounds))
	for _, bound := range bounds {
		out = append(out, bound)
	}
	sort.Slice(out, func(i, j int) bool {
		leftNaN, rightNaN := math.IsNaN(out[i]), math.IsNaN(out[j])
		if leftNaN != rightNaN {
			return leftNaN
		}
		return out[i] < out[j]
	})
	return out
}

func formatBounds(bounds []float64) []string {
	out := make([]string, 0, len(bounds))
	for _, bound := range bounds {
		out = append(out, strconv.FormatFloat(bound, 'g', -1, 64))
	}
	return out
}

func formatInt32s(values map[int32]struct{}) []string {
	ordered := make([]int32, 0, len(values))
	for value := range values {
		ordered = append(ordered, value)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	out := make([]string, 0, len(ordered))
	for _, value := range ordered {
		out = append(out, strconv.FormatInt(int64(value), 10))
	}
	return out
}

func floatKey(value float64) uint64 {
	return math.Float64bits(canonicalFloat(value))
}

func canonicalFloat(value float64) float64 {
	if value == 0 {
		return 0
	}
	return value
}

func cloneStrings(values []string) []string {
	if len(values) == 0 {
		return []string{}
	}
	return append([]string{}, values...)
}

func sortFindings(findings []Finding) {
	sort.SliceStable(findings, func(i, j int) bool {
		left, right := findings[i], findings[j]
		if left.Signal != right.Signal {
			return left.Signal < right.Signal
		}
		if left.Field != right.Field {
			return left.Field < right.Field
		}
		if left.Kind != right.Kind {
			return left.Kind < right.Kind
		}
		if left.Disposition != right.Disposition {
			return left.Disposition < right.Disposition
		}
		if cmp := compareStrings(left.SynthValues, right.SynthValues); cmp != 0 {
			return cmp < 0
		}
		return compareStrings(left.RealityValues, right.RealityValues) < 0
	})
}

func compareStrings(a, b []string) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] < b[i] {
			return -1
		}
		if a[i] > b[i] {
			return 1
		}
	}
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	default:
		return 0
	}
}
