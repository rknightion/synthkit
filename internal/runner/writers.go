// SPDX-License-Identifier: AGPL-3.0-only

package runner

import (
	"context"
	"log"
	"maps"
	"sort"
	"sync"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/sink/loki"
	"github.com/rknightion/synthkit/internal/sink/otlp"
	"github.com/rknightion/synthkit/internal/sink/promrw"
	pyroscope "github.com/rknightion/synthkit/internal/sink/pyroscope"
)

// BlueprintLabel is the selector label key stamped on blueprint-scoped series/streams/
// spans. Stamped HERE and only here (ARCHITECTURE I17); constructs never stamp it.
const BlueprintLabel = "blueprint"

// seriesBudget is the per-blueprint series budget for one tick window (I7). The sink's
// global SERIES_CAP remains the backstop underneath.
type seriesBudget struct {
	mu   sync.Mutex
	cap  int // <=0 = unlimited
	used int
}

func newSeriesBudget(cap int) *seriesBudget { return &seriesBudget{cap: cap} }

// take reserves up to n series, returning how many are allowed this window.
func (b *seriesBudget) take(n int) int {
	if b == nil || b.cap <= 0 {
		return n
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	remain := b.cap - b.used
	if remain <= 0 {
		return 0
	}
	if n > remain {
		n = remain
	}
	b.used += n
	return n
}

func (b *seriesBudget) reset() {
	if b == nil {
		return
	}
	b.mu.Lock()
	b.used = 0
	b.mu.Unlock()
}

// stampedMetrics is the scoped metric writer: clones labels before stamping (Collect()
// output aliases live cumulative state — I17) and enforces the per-blueprint budget.
type stampedMetrics struct {
	sink   core.MetricWriter
	label  string // "" = substrate: never stamp
	bp     string // blueprint name, for log attribution
	budget *seriesBudget
	inv    *constructInv
}

func (w *stampedMetrics) Write(ctx context.Context, batch []promrw.Series) error {
	// Record inventory from the UNSTAMPED source (before blueprint label is added) so the
	// blueprint label this writer adds is not counted as part of the construct's own labels.
	for _, s := range batch {
		w.inv.recordMetric(s.Name, sortedLabelPairs(s.Labels))
	}
	if w.label != "" {
		stamped := make([]promrw.Series, len(batch))
		for i, s := range batch {
			lbls := make(map[string]string, len(s.Labels)+1)
			maps.Copy(lbls, s.Labels)
			lbls[BlueprintLabel] = w.label
			// Exemplars pass through unstamped — never aliased; no blueprint label on exemplars.
			stamped[i] = promrw.Series{Name: s.Name, Labels: lbls, Value: s.Value, T: s.T, Kind: s.Kind, Exemplars: s.Exemplars, Native: s.Native}
		}
		batch = stamped
	}
	if allowed := w.budget.take(len(batch)); allowed < len(batch) {
		log.Printf("runner: blueprint %q over series budget — dropping %d of %d series this window", w.bp, len(batch)-allowed, len(batch))
		batch = batch[:allowed]
	}
	if len(batch) == 0 {
		return nil
	}
	return w.sink.Write(ctx, batch)
}

// stampedLogs stamps the blueprint label onto STREAM labels for blueprint-scoped
// instances (cloned); substrate streams pass through untouched.
type stampedLogs struct {
	sink  core.LogWriter
	label string
	inv   *constructInv
}

func (w *stampedLogs) Write(ctx context.Context, streams []loki.Stream) error {
	// Record inventory from the UNSTAMPED source before blueprint label is added.
	for _, st := range streams {
		w.inv.recordLog(st.Labels["source"], sortedLabelKeys(st.Labels))
	}
	if w.label != "" {
		stamped := make([]loki.Stream, len(streams))
		for i, st := range streams {
			lbls := make(map[string]string, len(st.Labels)+1)
			maps.Copy(lbls, st.Labels)
			lbls[BlueprintLabel] = w.label
			stamped[i] = loki.Stream{Labels: lbls, Lines: st.Lines}
		}
		streams = stamped
	}
	return w.sink.Write(ctx, streams)
}

// stampedTraces stamps the blueprint label as a RESOURCE attribute (cloned).
type stampedTraces struct {
	sink  core.TraceWriter
	label string
	inv   *constructInv
}

func (w *stampedTraces) Write(ctx context.Context, resources []otlp.Resource) error {
	// Record inventory from the UNSTAMPED source before blueprint label is added.
	for _, res := range resources {
		svc, _ := res.Attrs["service.name"].(string) // "" if absent/non-string — safe
		spanNames := make([]string, 0, len(res.Spans))
		attrKeys := sortedAnyKeys(res.Attrs) // resource attr keys
		seen := map[string]struct{}{}
		for _, k := range attrKeys {
			seen[k] = struct{}{}
		}
		for _, sp := range res.Spans { // otlp.Span.Name (string), otlp.Span.Attrs (map[string]any)
			spanNames = append(spanNames, sp.Name)
			for k := range sp.Attrs {
				if _, ok := seen[k]; !ok {
					seen[k] = struct{}{}
					attrKeys = append(attrKeys, k)
				}
			}
		}
		sort.Strings(attrKeys)
		w.inv.recordTrace(svc, spanNames, attrKeys)
	}
	if w.label != "" {
		stamped := make([]otlp.Resource, len(resources))
		for i, r := range resources {
			attrs := make(map[string]any, len(r.Attrs)+1)
			maps.Copy(attrs, r.Attrs)
			attrs[BlueprintLabel] = w.label
			stamped[i] = otlp.Resource{Attrs: attrs, Spans: r.Spans}
		}
		resources = stamped
	}
	return w.sink.Write(ctx, resources)
}

// sortedLabelKeys returns sorted keys of a map[string]string. Used for recording
// UNSTAMPED log label keys into the inventory.
func sortedLabelKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// sortedLabelPairs returns [key,value] pairs sorted by key, used for stable metric
// signature hashing (values are hashed but never stored/exposed).
func sortedLabelPairs(m map[string]string) [][2]string {
	out := make([][2]string, 0, len(m))
	for k, v := range m {
		out = append(out, [2]string{k, v})
	}
	sort.Slice(out, func(i, j int) bool { return out[i][0] < out[j][0] })
	return out
}

// sortedAnyKeys returns sorted keys of a map[string]any. Used for recording
// UNSTAMPED trace resource and span attr keys into the inventory.
func sortedAnyKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// stampedOTLPMetrics stamps the blueprint label as a RESOURCE attribute on OTLP metric blocks
// (cloned before stamping; the emitter's resource attrs alias nothing the writer mutates).
// Substrate instances (label "") pass through untouched. Mirrors stampedTraces.
type stampedOTLPMetrics struct {
	sink  core.OTLPMetricWriter
	label string
}

func (w *stampedOTLPMetrics) Write(ctx context.Context, resources []otlp.MetricResource) error {
	if w.label != "" {
		stamped := make([]otlp.MetricResource, len(resources))
		for i, r := range resources {
			attrs := make(map[string]any, len(r.Attrs)+1)
			maps.Copy(attrs, r.Attrs)
			attrs[BlueprintLabel] = w.label
			stamped[i] = otlp.MetricResource{Attrs: attrs, Scope: r.Scope, Metrics: r.Metrics}
		}
		resources = stamped
	}
	return w.sink.Write(ctx, resources)
}

// stampedProfiles stamps the blueprint label onto Pyroscope series labels for blueprint-scoped
// instances (cloned); substrate series pass through untouched.
type stampedProfiles struct {
	sink  core.PyroscopeWriter
	label string
}

func (s *stampedProfiles) Write(ctx context.Context, series []pyroscope.Series) error {
	if s.label == "" {
		return s.sink.Write(ctx, series)
	}
	out := make([]pyroscope.Series, len(series))
	for i, ser := range series {
		labels := make([]pyroscope.LabelPair, len(ser.Labels), len(ser.Labels)+1)
		copy(labels, ser.Labels) // clone before stamping (don't alias caller's slice)
		labels = append(labels, pyroscope.LabelPair{Name: BlueprintLabel, Value: s.label})
		out[i] = pyroscope.Series{Labels: labels, Profile: ser.Profile}
	}
	return s.sink.Write(ctx, out)
}
