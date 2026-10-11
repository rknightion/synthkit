// SPDX-License-Identifier: AGPL-3.0-only

package runner

import (
	"cmp"
	"context"
	"log"
	"maps"
	"slices"
	"sort"
	"strings"
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

// seriesBudget is the per-blueprint data-point allowance for one fixed-minute window (I7). Each
// repeated high-DPM sample consumes the allowance again. The sink's global per-push SERIES_CAP
// remains the separate backstop underneath.
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
	sink              core.MetricWriter
	label             string // "" = substrate: never stamp
	bp                string // blueprint name, for log attribution
	budget            *seriesBudget
	inv               *constructInv
	producer          string
	allowListVersion  string
	allowListVariant  string
	recordSuppression func(MetricSuppression)
	labels            metricLabelCache
}

// These are cache admission limits, never telemetry limits. Each scoped writer
// retains at most 16K immutable label maps and 16 MiB of charged storage. Wide or
// oversized maps and a full cache use ordinary, uncached clones.
const (
	maxMetricLabelSets       = 16_384
	maxMetricLabelCacheBytes = 16 << 20
	maxCachedMetricLabels    = 128
)

type cachedMetricLabels struct {
	pairs  [][2]string       // immutable, sorted UNSTAMPED identity
	labels map[string]string // immutable stamped clone
	bytes  int
}

// The cache is scoped to a writer and bounded independently of emitted volume.
// A hash is only a hint: every hit compares the complete source identity. No
// pointer identity, sample value, timestamp or exemplar participates in lookup.
// Published clones are never cleared or repurposed, including on collision.
type metricLabelCache struct {
	mu    sync.Mutex
	sets  map[uint64]cachedMetricLabels
	bytes int
}

// Order-independent lookup avoids sorting the same label set on every tick.
// Pair boundaries need not be injective here because hits require exact equality.
func metricLabelsHash(source map[string]string) uint64 {
	h := uint64(len(source))
	for k, v := range source {
		pair := hashShardString(shardOffset64, k) * shardPrime64
		h += hashShardString(pair, v)
	}
	return h
}

func sameMetricLabelPairs(pairs [][2]string, source map[string]string) bool {
	if len(pairs) != len(source) {
		return false
	}
	for _, pair := range pairs {
		if v, ok := source[pair[0]]; !ok || v != pair[1] {
			return false
		}
	}
	return true
}

// Admission charges map slots, pair storage, allocation rounding and retained
// strings. Clone strings so a short substring cannot retain a large backing
// buffer. This is a conservative storage budget, not a heap/RSS measurement.
func metricLabelCharge(source map[string]string, label string) int {
	if len(source) > maxCachedMetricLabels {
		return 0
	}
	size := 512
	if len(label) > maxMetricLabelCacheBytes-size {
		return 0
	}
	size += len(label)
	for k, v := range source {
		if 160 > maxMetricLabelCacheBytes-size {
			return 0
		}
		size += 160
		if len(k) > maxMetricLabelCacheBytes-size {
			return 0
		}
		size += len(k)
		if len(v) > maxMetricLabelCacheBytes-size {
			return 0
		}
		size += len(v)
	}
	return size
}

// resolve requires c.mu. On a hit, reuse BOTH the sorted inventory identity and
// its stamped clone; the earlier cache still re-sorted and re-hashed every series.
// Fallback preserves all labels even after admission stops.
func (c *metricLabelCache) resolve(hash uint64, source map[string]string, label string) cachedMetricLabels {
	old, found := c.sets[hash]
	if found && sameMetricLabelPairs(old.pairs, source) && (label == "" || sameStampedMetricLabels(old.labels, source, label)) {
		return old
	}
	charge := metricLabelCharge(source, label)
	retain := charge > 0 && (found || len(c.sets) < maxMetricLabelSets) && charge <= maxMetricLabelCacheBytes-c.bytes+old.bytes
	entry := cachedMetricLabels{pairs: make([][2]string, 0, len(source))}
	for k, v := range source {
		if retain {
			k, v = strings.Clone(k), strings.Clone(v)
		}
		entry.pairs = append(entry.pairs, [2]string{k, v})
	}
	slices.SortFunc(entry.pairs, func(a, b [2]string) int { return cmp.Compare(a[0], b[0]) })
	if label != "" {
		entry.labels = make(map[string]string, len(source)+1)
		for _, pair := range entry.pairs {
			entry.labels[pair[0]] = pair[1]
		}
		if retain {
			label = strings.Clone(label)
		}
		entry.labels[BlueprintLabel] = label
	}
	if retain {
		if c.sets == nil {
			c.sets = make(map[uint64]cachedMetricLabels)
		}
		entry.bytes = charge
		c.sets[hash] = entry
		c.bytes += charge - old.bytes
	}
	return entry
}

func (w *stampedMetrics) Write(ctx context.Context, batch []promrw.Series) error {
	allowed := w.budget.take(len(batch))
	if allowed < len(batch) {
		log.Printf("runner: blueprint %q over series budget — dropping %d of %d series this window", w.bp, len(batch)-allowed, len(batch))
	}
	stamped := make([]promrw.Series, allowed)
	w.labels.mu.Lock()
	// Inventory still observes EVERY attempted series before stamping, including
	// rejected samples. A cache hit only removes redundant identity construction.
	producers := make(map[string]string)
	for i := range batch {
		s := &batch[i]
		var identity cachedMetricLabels
		if w.inv != nil || w.label != "" {
			identity = w.labels.resolve(metricLabelsHash(s.Labels), s.Labels, w.label)
			w.inv.recordMetric(s.Name, identity.pairs)
		}
		if i >= allowed {
			continue
		}
		stamped[i] = *s
		job := s.Labels["job"]
		producer, ok := producers[job]
		if !ok {
			producer = metricProducer(w.producer, job)
			if len(producers) < maxMetricLabelSets {
				producers[job] = producer
			}
		}
		stamped[i].Producer = producer
		stamped[i].ProducerAllowListVersion = w.allowListVersion
		stamped[i].ProducerAllowListVariant = w.allowListVariant
		if w.label != "" {
			stamped[i].Labels = identity.labels
		}
	}
	w.labels.mu.Unlock()
	if allowed == 0 {
		return nil
	}
	// No downstream I/O or callbacks under the cache mutex. Values, timestamps,
	// native histograms and exemplars always come from the current snapshot.
	return w.sink.Write(ctx, stamped)
}

func sameStampedMetricLabels(stamped, source map[string]string, label string) bool {
	if stamped == nil || stamped[BlueprintLabel] != label {
		return false
	}
	count := len(source)
	if _, ok := source[BlueprintLabel]; !ok {
		count++
	}
	if len(stamped) != count {
		return false
	}
	for k, v := range source {
		if k == BlueprintLabel {
			continue
		}
		if got, ok := stamped[k]; !ok || got != v {
			return false
		}
	}
	return true
}

// metricProducer uses the synthetic series' declared job, which is already
// public in the same dry-run inventory. This is not the live-capture privacy
// boundary: restricting these synthetic jobs to an unrelated reader vocabulary
// would erase valid configurable producer identities.
func metricProducer(transport, job string) string {
	transport = strings.TrimSpace(transport)
	job = strings.TrimSpace(job)
	if transport == "" || job == "" {
		return transport
	}
	return transport + "/" + job
}

// RecordMetricSuppression implements core.MetricSuppressionRecorder. The
// construct supplies only the direct allow-list decision; this composition-root
// writer adds the catalog-declared producer identity.
func (w *stampedMetrics) RecordMetricSuppression(name, allowListVersion, allowListVariant string) {
	if w.recordSuppression == nil {
		return
	}
	w.recordSuppression(MetricSuppression{
		Name: name, Producer: w.producer,
		AllowListVersion: allowListVersion, AllowListVariant: allowListVariant,
	})
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
	sink     core.OTLPMetricWriter
	label    string
	producer string
	inv      *constructInv
}

func (w *stampedOTLPMetrics) Write(ctx context.Context, resources []otlp.MetricResource) error {
	stamped := make([]otlp.MetricResource, len(resources))
	for i, r := range resources {
		r.Producer = w.producer
		if w.label != "" {
			attrs := make(map[string]any, len(r.Attrs)+1)
			maps.Copy(attrs, r.Attrs)
			attrs[BlueprintLabel] = w.label
			r.Attrs = attrs
		}
		stamped[i] = r
	}
	resources = stamped
	return w.sink.Write(ctx, resources)
}

// RecordCloudWatchMetricStreamReport keeps withheld CloudWatch bases visible in the live
// construct inventory. The report is source-side bookkeeping and is never stamped on the wire.
func (w *stampedOTLPMetrics) RecordCloudWatchMetricStreamReport(report core.CloudWatchMetricStreamReport) {
	if w == nil || w.inv == nil {
		return
	}
	w.inv.recordCloudWatchMetricStreamReport(report.SkippedBases)
}

// stampedOTLPLogs stamps the blueprint label as a RESOURCE attribute on OTLP log blocks
// (cloned before stamping). Substrate instances (label "") — which is every k8s_cluster —
// pass through untouched. Mirrors stampedOTLPMetrics.
type stampedOTLPLogs struct {
	sink  core.OTLPLogWriter
	label string
}

func (w *stampedOTLPLogs) Write(ctx context.Context, resources []otlp.LogResource) error {
	if w.label != "" {
		stamped := make([]otlp.LogResource, len(resources))
		for i, r := range resources {
			attrs := make(map[string]any, len(r.Attrs)+1)
			maps.Copy(attrs, r.Attrs)
			attrs[BlueprintLabel] = w.label
			stamped[i] = otlp.LogResource{Attrs: attrs, Scope: r.Scope, Records: r.Records}
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
