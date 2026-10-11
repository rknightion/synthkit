// SPDX-License-Identifier: AGPL-3.0-only

package runner

import (
	"context"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/sink/otlp"
	"github.com/rknightion/synthkit/internal/sink/promrw"
	pyroscope "github.com/rknightion/synthkit/internal/sink/pyroscope"
)

func TestStampedMetricsDerivesProducerFromSeriesJob(t *testing.T) {
	var got []promrw.Series
	w := &stampedMetrics{
		sink:     metricWriterFunc(func(_ context.Context, batch []promrw.Series) error { got = batch; return nil }),
		producer: producerPromRW,
		budget:   newSeriesBudget(0),
	}
	input := []promrw.Series{{Name: "up", Labels: map[string]string{"job": "integrations/kubernetes/kubelet"}}}
	if err := w.Write(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	if got[0].Producer != "promrw/integrations/kubernetes/kubelet" {
		t.Fatalf("producer=%q, want composite transport/job identity", got[0].Producer)
	}
	if input[0].Producer != "" {
		t.Fatalf("source series producer=%q, want clone-before-stamp", input[0].Producer)
	}
}

func TestStampedMetricsKeepsTransportProducerWhenJobIsAbsent(t *testing.T) {
	var got []promrw.Series
	w := &stampedMetrics{
		sink:     metricWriterFunc(func(_ context.Context, batch []promrw.Series) error { got = batch; return nil }),
		producer: producerPromRW,
		budget:   newSeriesBudget(0),
	}
	if err := w.Write(context.Background(), []promrw.Series{{Name: "up", Labels: map[string]string{"cluster": "test"}}}); err != nil {
		t.Fatal(err)
	}
	if got[0].Producer != producerPromRW {
		t.Fatalf("producer=%q, want transport-only identity %q", got[0].Producer, producerPromRW)
	}
}

// captureProfileSink implements core.PyroscopeWriter and captures the last Write call.
type captureProfileSink struct {
	last []pyroscope.Series
}

func (c *captureProfileSink) Write(_ context.Context, series []pyroscope.Series) error {
	c.last = series
	return nil
}

// hasProfLabel reports whether any series in the batch has a label with the given key and value.
func hasProfLabel(series []pyroscope.Series, key, value string) bool {
	for _, s := range series {
		for _, lp := range s.Labels {
			if lp.Name == key && lp.Value == value {
				return true
			}
		}
	}
	return false
}

// hasProfLabelKey reports whether any series in the batch has a label with the given key
// (regardless of value).
func hasProfLabelKey(series []pyroscope.Series, key string) bool {
	for _, s := range series {
		for _, lp := range s.Labels {
			if lp.Name == key {
				return true
			}
		}
	}
	return false
}

func TestStampedProfiles(t *testing.T) {
	// ScopeBlueprint: label must be stamped.
	cap := &captureProfileSink{}
	input := []pyroscope.Series{{Labels: []pyroscope.LabelPair{{Name: "service_name", Value: "x"}}}}
	if err := (&stampedProfiles{sink: cap, label: "acme"}).Write(context.Background(), input); err != nil {
		t.Fatalf("Write error: %v", err)
	}
	if !hasProfLabel(cap.last, BlueprintLabel, "acme") {
		t.Fatal("blueprint must be stamped for ScopeBlueprint")
	}

	// Verify clone-before-stamp: original input labels slice must not be mutated.
	if len(input[0].Labels) != 1 {
		t.Fatalf("original labels slice was mutated (len=%d, want 1)", len(input[0].Labels))
	}

	// ScopeSubstrate (label=""): blueprint label must NOT be stamped.
	cap.last = nil
	if err := (&stampedProfiles{sink: cap, label: ""}).Write(context.Background(), input); err != nil {
		t.Fatalf("Write error: %v", err)
	}
	if hasProfLabelKey(cap.last, BlueprintLabel) {
		t.Fatal("substrate must NOT stamp blueprint label")
	}
}

func TestStampedOTLPMetricsStampsBlueprintAttr(t *testing.T) {
	var got []otlp.MetricResource
	fake := otlpMetricWriterFunc(func(ctx context.Context, rs []otlp.MetricResource) error { got = rs; return nil })
	w := &stampedOTLPMetrics{sink: fake, label: "acme", producer: producerOTLPNative}
	in := []otlp.MetricResource{{Attrs: map[string]any{"service.name": "svc"}}}
	if err := w.Write(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if got[0].Attrs["blueprint"] != "acme" {
		t.Errorf("blueprint attr = %v, want acme", got[0].Attrs["blueprint"])
	}
	if got[0].Producer != producerOTLPNative {
		t.Errorf("producer = %q, want %q", got[0].Producer, producerOTLPNative)
	}
	if _, ok := in[0].Attrs["blueprint"]; ok {
		t.Error("source resource was mutated (must clone before stamp)")
	}
}

func TestStampedOTLPMetricsSubstrateNoStamp(t *testing.T) {
	var got []otlp.MetricResource
	fake := otlpMetricWriterFunc(func(ctx context.Context, rs []otlp.MetricResource) error { got = rs; return nil })
	w := &stampedOTLPMetrics{sink: fake, label: "", producer: producerOTLPNative}
	_ = w.Write(context.Background(), []otlp.MetricResource{{Attrs: map[string]any{"service.name": "svc"}}})
	if _, ok := got[0].Attrs["blueprint"]; ok {
		t.Error("substrate writer must not stamp blueprint")
	}
	if got[0].Producer != producerOTLPNative {
		t.Errorf("producer = %q, want %q", got[0].Producer, producerOTLPNative)
	}
}

func TestStampedMetricsRecordsAllowListSuppressionWithCatalogProducer(t *testing.T) {
	var got MetricSuppression
	w := &stampedMetrics{
		producer: producerPromRW,
		recordSuppression: func(suppression MetricSuppression) {
			got = suppression
		},
	}
	w.RecordMetricSuppression("node_disk_read_bytes_total", "4.5.0", "node-exporter=default")
	want := MetricSuppression{
		Name: "node_disk_read_bytes_total", Producer: producerPromRW,
		AllowListVersion: "4.5.0", AllowListVariant: "node-exporter=default",
	}
	if got != want {
		t.Fatalf("suppression=%+v, want %+v", got, want)
	}
}

func TestShardMetricResourceStableBySvc(t *testing.T) {
	a := otlp.MetricResource{Attrs: map[string]any{"service.name": "svc", "service.namespace": "ns", "blueprint": "bp"}}
	b := otlp.MetricResource{Attrs: map[string]any{"service.name": "svc", "service.namespace": "ns", "blueprint": "bp"}}
	c := otlp.MetricResource{Attrs: map[string]any{"service.name": "other", "service.namespace": "ns", "blueprint": "bp"}}
	if shardMetricResource(a) != shardMetricResource(b) {
		t.Error("same identity must shard identically")
	}
	if shardMetricResource(a) == shardMetricResource(c) {
		t.Error("different service must (almost always) shard differently")
	}
}

// otlpMetricWriterFunc adapts a func to core.OTLPMetricWriter for tests.
type otlpMetricWriterFunc func(context.Context, []otlp.MetricResource) error

func (f otlpMetricWriterFunc) Write(ctx context.Context, rs []otlp.MetricResource) error {
	return f(ctx, rs)
}

// Cached identity must never cache samples or hide a changed source map. A sink
// retaining an old snapshot must still see its original labels and values.
func TestStampedMetricsCachedSnapshotsAndInventory(t *testing.T) {
	var snapshots [][]promrw.Series
	inv := newConstructInv("test", "app", "svc", nil, nil)
	w := &stampedMetrics{label: "selected", producer: producerPromRW, inv: inv,
		budget: newSeriesBudget(0), sink: metricWriterFunc(func(_ context.Context, b []promrw.Series) error {
			snapshots = append(snapshots, b)
			return nil
		})}
	labels := map[string]string{"job": "service", "status": "200", BlueprintLabel: "source"}
	sample := []promrw.Series{{Name: "requests_total", Labels: labels, Value: 1, T: time.Unix(1, 0)}}
	for i := 0; i < 2; i++ {
		sample[0].Value = float64(i + 1)
		sample[0].T = time.Unix(int64(i+1), 0)
		if err := w.Write(context.Background(), sample); err != nil {
			t.Fatal(err)
		}
	}
	labels["status"] = "500"
	labels[BlueprintLabel] = "different source"
	if err := w.Write(context.Background(), sample); err != nil {
		t.Fatal(err)
	}
	if labels[BlueprintLabel] != "different source" || sample[0].Producer != "" {
		t.Fatal("writer mutated source")
	}
	for i, b := range snapshots {
		wantStatus := "200"
		if i == 2 {
			wantStatus = "500"
		}
		if b[0].Labels["status"] != wantStatus || b[0].Labels[BlueprintLabel] != "selected" {
			t.Fatalf("snapshot %d labels changed: %v", i, b[0].Labels)
		}
		wantValue := float64(i + 1)
		if i == 2 {
			wantValue = 2
		}
		if b[0].Value != wantValue || b[0].T != time.Unix(int64(wantValue), 0) {
			t.Fatalf("snapshot %d cached sample: %+v", i, b[0])
		}
	}
	if len(inv.sigs) != 2 {
		t.Fatalf("inventory distinct source identities=%d, want 2", len(inv.sigs))
	}
	// Rejected series still contribute their unstamped identities to inventory.
	w.budget = newSeriesBudget(1)
	if err := w.Write(context.Background(), []promrw.Series{
		{Name: "admitted", Labels: map[string]string{"first": "a"}},
		{Name: "rejected", Labels: map[string]string{"second": "b"}},
	}); err != nil {
		t.Fatal(err)
	}
	if len(snapshots[3]) != 1 || len(inv.sigs) != 4 {
		t.Fatalf("budget/inventory: delivered=%d distinct=%d", len(snapshots[3]), len(inv.sigs))
	}
	if _, ok := inv.metricLabels["second"]; !ok {
		t.Fatal("rejected source label missing from inventory")
	}
}

func TestMetricLabelCacheCollisionAndFallback(t *testing.T) {
	var cache metricLabelCache
	a := cache.resolve(7, map[string]string{"key": "first", BlueprintLabel: "source"}, "selected")
	b := cache.resolve(7, map[string]string{"other": "", BlueprintLabel: "source"}, "selected")
	if a.labels["key"] != "first" || b.labels["other"] != "" || len(b.labels) != 2 {
		t.Fatalf("collision merged or mutated identities: %v %v", a.labels, b.labels)
	}
	if _, ok := b.labels["key"]; ok {
		t.Fatal("collision retained old label")
	}
	if !sameMetricLabelPairs(b.pairs, map[string]string{"other": "", BlueprintLabel: "source"}) {
		t.Fatal("unstamped identity lost")
	}
	// Source blueprint values are overwritten on the wire but distinct in inventory.
	c := cache.resolve(7, map[string]string{"other": "", BlueprintLabel: "changed"}, "selected")
	if !sameMetricLabelPairs(c.pairs, map[string]string{"other": "", BlueprintLabel: "changed"}) {
		t.Fatal("source blueprint change hidden")
	}
	cache.bytes = maxMetricLabelCacheBytes
	d := cache.resolve(8, map[string]string{"uncached": "value"}, "selected")
	if d.labels["uncached"] != "value" || len(cache.sets) != 1 {
		t.Fatal("byte-budget fallback dropped data or retained entry")
	}
	wide := map[string]string{}
	for i := 0; i <= maxCachedMetricLabels; i++ {
		wide[strconv.Itoa(i)] = "v"
	}
	e := cache.resolve(9, wide, "selected")
	if len(e.labels) != len(wide)+1 || len(cache.sets) != 1 {
		t.Fatal("wide-map fallback narrowed labels")
	}
	// Entry-count saturation, independently of the byte allowance.
	cache = metricLabelCache{sets: make(map[uint64]cachedMetricLabels)}
	for i := 0; i < maxMetricLabelSets; i++ {
		cache.sets[uint64(i)] = cachedMetricLabels{}
	}
	f := cache.resolve(uint64(maxMetricLabelSets), map[string]string{"last": "v"}, "selected")
	if f.labels["last"] != "v" || len(cache.sets) != maxMetricLabelSets {
		t.Fatal("entry-limit fallback changed data or grew cache")
	}
}

func TestMetricLabelCacheLookupOrderAndChangedLabels(t *testing.T) {
	first := map[string]string{"a": "1", "b": "2"}
	second := map[string]string{"b": "2", "a": "1"}
	if metricLabelsHash(first) != metricLabelsHash(second) {
		t.Fatal("map iteration changed cache identity")
	}
	var cache metricLabelCache
	x := cache.resolve(metricLabelsHash(first), first, "bp")
	y := cache.resolve(metricLabelsHash(second), second, "bp")
	if !reflect.DeepEqual(x.pairs, y.pairs) || len(cache.sets) != 1 {
		t.Fatal("equal labels did not resolve equally")
	}
	delete(second, "a")
	second["c"] = "3"
	z := cache.resolve(metricLabelsHash(second), second, "bp")
	if !reflect.DeepEqual(z.labels, map[string]string{"b": "2", "c": "3", BlueprintLabel: "bp"}) {
		t.Fatal("changed keys reused stale labels")
	}
	if !reflect.DeepEqual(x.labels, map[string]string{"a": "1", "b": "2", BlueprintLabel: "bp"}) {
		t.Fatal("old snapshot mutated")
	}
}
