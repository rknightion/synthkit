// SPDX-License-Identifier: AGPL-3.0-only

package state

import (
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/sink/promrw"
)

// seriesView mirrors promrw.Series for terse test assertions. Collect returns
// []promrw.Series; we convert so helpers stay short.
type seriesView struct {
	Name   string
	Labels map[string]string
	Value  float64
}

func collect(s *State, now time.Time) []seriesView {
	out := []seriesView{}
	for _, x := range s.Collect(now) {
		out = append(out, seriesView{x.Name, x.Labels, x.Value})
	}
	return out
}

func find(s []seriesView, name string) (float64, bool) {
	for _, x := range s {
		if x.Name == name {
			return x.Value, true
		}
	}
	return 0, false
}

func seriesByNameLE(s []seriesView, name, le string) (float64, bool) {
	for _, x := range s {
		if x.Name == name && x.Labels["le"] == le {
			return x.Value, true
		}
	}
	return 0, false
}

func leSet(s []seriesView) []string {
	var out []string
	for _, x := range s {
		if le, ok := x.Labels["le"]; ok {
			out = append(out, le)
		}
	}
	return out
}

// TestCounterMonotonic proves a counter accumulates across ticks (running totals, never
// deltas) and Reset zeroes it (synthetic gateway restart). A fabricator that pushed
// per-tick deltas would break rate() — this is the gate (I3).
func TestCounterMonotonic(t *testing.T) {
	now := time.Now()
	s := NewState()
	lbl := map[string]string{"env": "prod", "code": "200"}

	s.Add("request_count", lbl, 5)
	if v, _ := find(collect(s, now), "request_count"); v != 5 {
		t.Fatalf("after first Add want 5, got %v", v)
	}
	s.Add("request_count", lbl, 3) // next tick
	if v, _ := find(collect(s, now), "request_count"); v != 8 {
		t.Fatalf("counter must accumulate across ticks: want 8, got %v", v)
	}
	s.Reset("request_count", lbl)
	if v, _ := find(collect(s, now), "request_count"); v != 0 {
		t.Fatalf("Reset must zero the running total: want 0, got %v", v)
	}
	// Distinct label sets are distinct series.
	s.Add("request_count", map[string]string{"env": "staging", "code": "200"}, 2)
	got := collect(s, now)
	if len(got) != 2 {
		t.Fatalf("two label sets → two series, got %d: %+v", len(got), got)
	}
}

// TestGaugeInstantaneous proves Set replaces (not accumulates).
func TestGaugeInstantaneous(t *testing.T) {
	now := time.Now()
	s := NewState()
	lbl := map[string]string{"pod": "alloy-0"}
	s.Set("otelcol_exporter_queue_size", lbl, 10)
	s.Set("otelcol_exporter_queue_size", lbl, 7)
	if v, _ := find(collect(s, now), "otelcol_exporter_queue_size"); v != 7 {
		t.Fatalf("gauge Set must be instantaneous: want 7, got %v", v)
	}
}

// TestHistogramExpansion proves the cumulative _bucket{le}/_sum/_count expansion against
// a pinned le set, with the implicit +Inf bucket equal to _count and monotonic-in-le
// bucket counts that accumulate across ticks.
func TestHistogramExpansion(t *testing.T) {
	now := time.Now()
	s := NewState()
	lbl := map[string]string{"endpoint": "/v1/chat"}
	bounds := []float64{0.1, 0.5, 1.0}
	for _, v := range []float64{0.05, 0.2, 0.7, 2.0} { // 4 observations
		s.Observe("req_dur", lbl, bounds, LEBare, v)
	}
	got := collect(s, now)

	// Cumulative bucket counts: ≤0.1→1, ≤0.5→2, ≤1.0→3, +Inf→4. LEBare → le="1" (bare integer).
	for le, want := range map[string]float64{"0.1": 1, "0.5": 2, "1": 3, "+Inf": 4} {
		if v, ok := seriesByNameLE(got, "req_dur_bucket", le); !ok || v != want {
			t.Fatalf("bucket le=%s want %v, got %v ok=%v", le, want, v, ok)
		}
	}
	if v, _ := find(got, "req_dur_sum"); v != 2.95 {
		t.Fatalf("_sum want 2.95, got %v", v)
	}
	if v, _ := find(got, "req_dur_count"); v != 4 {
		t.Fatalf("_count want 4, got %v", v)
	}
	// +Inf bucket must equal _count.
	inf, _ := seriesByNameLE(got, "req_dur_bucket", "+Inf")
	cnt, _ := find(got, "req_dur_count")
	if inf != cnt {
		t.Fatalf("+Inf bucket (%v) must equal _count (%v)", inf, cnt)
	}

	// Bucket counts must be monotonic non-decreasing in le, and cumulative across ticks.
	s.Observe("req_dur", lbl, bounds, LEBare, 0.05) // one more ≤0.1 observation, next tick
	got2 := collect(s, now)
	b01, _ := seriesByNameLE(got2, "req_dur_bucket", "0.1")
	b05, _ := seriesByNameLE(got2, "req_dur_bucket", "0.5")
	b1, _ := seriesByNameLE(got2, "req_dur_bucket", "1")
	if b01 != 2 || b05 != 3 {
		t.Fatalf("histogram must accumulate across ticks: le=0.1 want 2 got %v; le=0.5 want 3 got %v", b01, b05)
	}
	if !(b01 <= b05 && b05 <= b1) {
		t.Fatalf("bucket counts must be monotonic non-decreasing in le: %v,%v,%v", b01, b05, b1)
	}
}

// TestHistogramPinnedBounds proves observations far above the largest finite bound land
// only in +Inf, and that an exactly-equal observation lands in its bucket (le is "≤").
func TestHistogramPinnedBounds(t *testing.T) {
	now := time.Now()
	s := NewState()
	lbl := map[string]string{"x": "y"}
	bounds := []float64{1, 5, 10}
	// 10 lands in ≤10 (boundary inclusive); 1 lands in ≤1; 100 only in +Inf.
	for _, v := range []float64{1, 10, 100} {
		s.Observe("pinned", lbl, bounds, LEBare, v)
	}
	got := collect(s, now)
	for le, want := range map[string]float64{"1": 1, "5": 1, "10": 2, "+Inf": 3} {
		if v, ok := seriesByNameLE(got, "pinned_bucket", le); !ok || v != want {
			t.Fatalf("pinned bucket le=%s want %v got %v ok=%v", le, want, v, ok)
		}
	}
}

// TestHistogramLEStyles pins the two `le` rendering conventions against the contract (I4).
// LEDotZero must force a trailing ".0" on integer bounds (the OTLP→Prometheus translation:
// 0.0, 1.0, 7.5, 10.0) while fractional bounds stay minimal; LEBare must NOT add ".0" and
// must avoid scientific notation for large bounds (prom-client: le="10000000", never 1e+07).
func TestHistogramLEStyles(t *testing.T) {
	now := time.Now()

	// LEDotZero — APM/OTLP path.
	apm := NewState()
	apmBounds := []float64{0.0, 0.005, 0.025, 1.0, 7.5, 10.0}
	apm.Observe("traces_spanmetrics_latency", map[string]string{"service": "x"}, apmBounds, LEDotZero, 0.003)
	got := collect(apm, now)
	for _, want := range []string{"0.0", "0.005", "0.025", "1.0", "7.5", "10.0", "+Inf"} {
		if _, ok := seriesByNameLE(got, "traces_spanmetrics_latency_bucket", want); !ok {
			t.Fatalf("LEDotZero must emit le=%q; have %v", want, leSet(got))
		}
	}

	// LEBare — native scrape: bare integers, no scientific notation on 10000000.
	bare := NewState()
	bareBounds := []float64{0.1, 1, 3000, 10000000}
	bare.Observe("request_duration_milliseconds", map[string]string{"endpoint": "/v1"}, bareBounds, LEBare, 5)
	gotbare := collect(bare, now)
	for _, want := range []string{"0.1", "1", "3000", "10000000", "+Inf"} {
		if _, ok := seriesByNameLE(gotbare, "request_duration_milliseconds_bucket", want); !ok {
			t.Fatalf("LEBare must emit le=%q (prom-client form); have %v", want, leSet(gotbare))
		}
	}
}

// TestFormatLETrapCases pins formatLE directly on the documented trap cases: LEBare never
// produces scientific notation (le="10000000", not "1e+07"); LEDotZero forces ".0" on
// integer bounds (10.0, 1.0, 0.0) but leaves fractional bounds minimal.
func TestFormatLETrapCases(t *testing.T) {
	cases := []struct {
		bound float64
		style LEStyle
		want  string
	}{
		{10000000, LEBare, "10000000"},      // never 1e+07
		{10000000, LEDotZero, "10000000.0"}, // big integer → no scientific notation, forced ".0"
		{10, LEDotZero, "10.0"},
		{1, LEDotZero, "1.0"},
		{0, LEDotZero, "0.0"},
		{10, LEBare, "10"},
		{1, LEBare, "1"},
		{0, LEBare, "0"},
		{0.005, LEDotZero, "0.005"}, // fractional stays minimal under DotZero
		{0.005, LEBare, "0.005"},
		{0.1, LEBare, "0.1"},
	}
	for _, c := range cases {
		if got := formatLE(c.bound, c.style); got != c.want {
			t.Fatalf("formatLE(%v, %v) = %q, want %q", c.bound, c.style, got, c.want)
		}
	}
}

// TestFormatLEBigIntegerDotZero pins the subtle interaction: a big integer bound under
// LEDotZero must be "10000000.0" (a dot is forced) — and crucially NOT scientific notation.
func TestFormatLEBigIntegerDotZero(t *testing.T) {
	if got := formatLE(10000000, LEDotZero); got != "10000000.0" {
		t.Fatalf("LEDotZero big integer: got %q want %q (no scientific notation, forced .0)", got, "10000000.0")
	}
}

// TestResetOnlyAffectsCounters proves Reset zeroes counters but leaves gauges and
// histograms untouched.
func TestResetOnlyAffectsCounters(t *testing.T) {
	now := time.Now()
	s := NewState()
	lbl := map[string]string{"k": "v"}

	s.Add("c", lbl, 9)
	s.Set("g", lbl, 4)
	s.Observe("h", lbl, []float64{1, 2}, LEBare, 1.5)

	s.Reset("c", lbl)
	s.Reset("g", lbl) // no-op on gauges
	s.Reset("h", lbl) // no-op on histos

	got := collect(s, now)
	if v, _ := find(got, "c"); v != 0 {
		t.Fatalf("Reset must zero counter, got %v", v)
	}
	if v, _ := find(got, "g"); v != 4 {
		t.Fatalf("Reset must NOT touch gauge, got %v", v)
	}
	if v, _ := find(got, "h_count"); v != 1 {
		t.Fatalf("Reset must NOT touch histogram count, got %v", v)
	}
	if v, _ := find(got, "h_sum"); v != 1.5 {
		t.Fatalf("Reset must NOT touch histogram sum, got %v", v)
	}
}

// TestCapSeries proves the per-push series budget backstop: cap truncates and flags;
// cap<=0 is unlimited; under cap is unchanged.
func TestCapSeries(t *testing.T) {
	mk := func(n int) []promrw.Series { return make([]promrw.Series, n) }
	if got, capped := CapSeries(mk(10), 4); len(got) != 4 || !capped {
		t.Fatalf("cap 4 of 10 → len 4 capped true, got len=%d capped=%v", len(got), capped)
	}
	if got, capped := CapSeries(mk(10), 0); len(got) != 10 || capped {
		t.Fatalf("cap 0 = unlimited, got len=%d capped=%v", len(got), capped)
	}
	if got, capped := CapSeries(mk(3), 4); len(got) != 3 || capped {
		t.Fatalf("under cap unchanged, got len=%d capped=%v", len(got), capped)
	}
}

// TestLabelMapClonedAtAddTime proves State clones the caller's label map when a series is
// first created, so mutating the caller's map AFTER the call does not corrupt state.
func TestLabelMapClonedAtAddTime(t *testing.T) {
	now := time.Now()
	s := NewState()
	lbl := map[string]string{"env": "prod", "code": "200"}
	s.Add("c", lbl, 1)
	lbl["code"] = "500" // caller mutates AFTER the call
	lbl["env"] = "MUTATED"

	got := collect(s, now)
	if len(got) != 1 {
		t.Fatalf("expected 1 series, got %d", len(got))
	}
	if got[0].Labels["code"] != "200" || got[0].Labels["env"] != "prod" {
		t.Fatalf("state must hold a CLONE of labels at Add time, got %+v", got[0].Labels)
	}
}

// TestLabelMapClonedAtSetTime is the gauge analogue of the Add clone test.
func TestLabelMapClonedAtSetTime(t *testing.T) {
	now := time.Now()
	s := NewState()
	lbl := map[string]string{"pod": "alloy-0"}
	s.Set("g", lbl, 3)
	lbl["pod"] = "MUTATED"

	got := collect(s, now)
	if got[0].Labels["pod"] != "alloy-0" {
		t.Fatalf("Set must clone labels, got %+v", got[0].Labels)
	}
}

// TestLabelMapClonedAtObserveTime is the histogram analogue. Mutating the caller's map
// after Observe must not corrupt the base labels carried onto _bucket/_sum/_count.
func TestLabelMapClonedAtObserveTime(t *testing.T) {
	now := time.Now()
	s := NewState()
	lbl := map[string]string{"endpoint": "/v1"}
	s.Observe("h", lbl, []float64{1}, LEBare, 0.5)
	lbl["endpoint"] = "MUTATED"

	got := collect(s, now)
	for _, x := range got {
		if x.Labels["endpoint"] != "/v1" {
			t.Fatalf("Observe must clone labels, series %q got %+v", x.Name, x.Labels)
		}
	}
}

// TestBoundsClonedAtObserveTime proves the bounds slice is cloned defensively: mutating
// the caller's bounds array after Observe must not change the rendered le set (the canon
// bucket arrays are shared package vars in real callers).
func TestBoundsClonedAtObserveTime(t *testing.T) {
	now := time.Now()
	s := NewState()
	bounds := []float64{1, 5, 10}
	s.Observe("h", map[string]string{"k": "v"}, bounds, LEBare, 3)
	bounds[1] = 999 // caller mutates the shared array AFTER the call

	got := collect(s, now)
	if _, ok := seriesByNameLE(got, "h_bucket", "5"); !ok {
		t.Fatalf("Observe must clone bounds; le=5 missing, have %v", leSet(got))
	}
	if _, ok := seriesByNameLE(got, "h_bucket", "999"); ok {
		t.Fatal("mutating caller bounds after Observe must not change rendered le set")
	}
}

func TestObserveDualEmitsBothForms(t *testing.T) {
	s := NewState()
	bounds := []float64{0.05, 0.1, 0.25, 0.5, 1.0}
	lbls := map[string]string{"service": "checkout"}
	vals := []float64{0.02, 0.07, 0.3, 0.9}
	for _, v := range vals {
		s.ObserveDual("traces_spanmetrics_latency", lbls, bounds, LEDotZero, NativeSchemaSpanMetrics, v)
	}
	out := s.Collect(time.UnixMilli(1_700_000_000_000))

	var native *promrw.Series
	var classicCount, classicSum *promrw.Series
	classicBuckets := 0
	for i := range out {
		m := out[i]
		switch {
		case m.Name == "traces_spanmetrics_latency" && m.Native != nil:
			native = &out[i]
		case m.Name == "traces_spanmetrics_latency_bucket":
			classicBuckets++
		case m.Name == "traces_spanmetrics_latency_count":
			classicCount = &out[i]
		case m.Name == "traces_spanmetrics_latency_sum":
			classicSum = &out[i]
		}
	}
	if native == nil {
		t.Fatal("expected a bare native traces_spanmetrics_latency series")
	}
	if classicBuckets == 0 || classicCount == nil || classicSum == nil {
		t.Fatal("expected classic _bucket/_count/_sum series alongside native")
	}
	if native.Native.Count != uint64(len(vals)) {
		t.Errorf("native count: want %d got %d", len(vals), native.Native.Count)
	}
	if classicCount.Value != float64(len(vals)) {
		t.Errorf("classic count: want %d got %v", len(vals), classicCount.Value)
	}
	wantSum := 0.02 + 0.07 + 0.3 + 0.9
	if !floatsClose(native.Native.Sum, wantSum) || !floatsClose(classicSum.Value, wantSum) {
		t.Errorf("sum mismatch: native=%v classic=%v want=%v", native.Native.Sum, classicSum.Value, wantSum)
	}
	if native.Native.Schema != NativeSchemaSpanMetrics {
		t.Errorf("schema: want %d got %d", NativeSchemaSpanMetrics, native.Native.Schema)
	}
}

func floatsClose(a, b float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d < 1e-9
}

func TestNativeHistogramCumulativeAcrossTicks(t *testing.T) {
	s := NewState()
	lbls := map[string]string{"service": "x"}
	s.ObserveNative("m", lbls, NativeSchemaSpanMetrics, 0.1)
	c1 := nativeCountFromCollect(t, s, "m")
	s.ObserveNative("m", lbls, NativeSchemaSpanMetrics, 0.2)
	c2 := nativeCountFromCollect(t, s, "m")
	if c1 != 1 || c2 != 2 {
		t.Fatalf("native count must accumulate across ticks: c1=%d c2=%d", c1, c2)
	}
}

func nativeCountFromCollect(t *testing.T, s *State, name string) uint64 {
	t.Helper()
	for _, m := range s.Collect(time.UnixMilli(1)) {
		if m.Name == name && m.Native != nil {
			return m.Native.Count
		}
	}
	t.Fatalf("native series %q not found", name)
	return 0
}

// TestDeleteGauge proves the three correctness properties of the DeleteGauge primitive:
//  1. A gauge that was Set then DeleteGauge'd does NOT appear in the next Collect output.
//  2. DeleteGauge on a never-Set series is a no-op (no panic, no state change).
//  3. DeleteGauge does NOT remove counter (Add) or histogram (Observe) series with the
//     same name and labels — only the instantaneous gauge map is touched.
func TestDeleteGauge(t *testing.T) {
	now := time.Now()
	lbl := map[string]string{"src_device": "spine-01", "dst_device": "leaf-01", "proto": "lldp"}
	const gaugeName = "network_topology_edge_info"
	const counterName = "network_topology_edge_info" // same name, different store
	const histoName = "network_topology_edge_info"   // same name, different store

	// ── Property 1: Set then DeleteGauge → absent in next Collect ─────────────
	s1 := NewState()
	s1.Set(gaugeName, lbl, 1)
	// Verify it's present before deletion.
	if _, ok := find(collect(s1, now), gaugeName); !ok {
		t.Fatal("gauge should be present after Set, before DeleteGauge")
	}
	s1.DeleteGauge(gaugeName, lbl)
	if _, ok := find(collect(s1, now), gaugeName); ok {
		t.Error("gauge must be absent after DeleteGauge (property 1 failed)")
	}

	// ── Property 2: DeleteGauge on a never-Set series is a no-op ──────────────
	s2 := NewState()
	// Must not panic; state must remain empty.
	s2.DeleteGauge(gaugeName, lbl)
	if got := collect(s2, now); len(got) != 0 {
		t.Errorf("DeleteGauge on never-Set series must leave state empty, got %d series", len(got))
	}

	// ── Property 3: DeleteGauge does NOT remove counter or histogram series ────
	// with the same (name, labels) — only the gauge map is touched.
	s3 := NewState()
	s3.Add(counterName, lbl, 7)                                // counter series
	s3.Observe(histoName, lbl, []float64{1, 5, 10}, LEBare, 3) // histogram series
	s3.Set(gaugeName, lbl, 1)                                  // gauge series (same name+labels)

	// Sanity: all three should be present before deletion.
	before := collect(s3, now)
	// counter
	if v, ok := find(before, counterName); !ok || v != 7 {
		t.Fatalf("counter pre-delete: want 7 got %v ok=%v", v, ok)
	}
	// histogram _count
	if v, ok := find(before, histoName+"_count"); !ok || v != 1 {
		t.Fatalf("histogram _count pre-delete: want 1 got %v ok=%v", v, ok)
	}
	// gauge
	if _, ok := find(before, gaugeName); !ok {
		t.Fatal("gauge pre-delete: should be present")
	}

	// Delete only the gauge.
	s3.DeleteGauge(gaugeName, lbl)
	after := collect(s3, now)

	// Gauge must be gone.
	// NOTE: because the counter and histogram use the SAME name/labels, we must check
	// specifically for gauge-kind absence. Since Collect outputs gauges as KindGauge and
	// counters as KindCounter, we use the raw Collect (not the seriesView helper) to
	// distinguish by Kind. Rebuild from s3.Collect directly:
	raw := s3.Collect(now)
	var hasGauge, hasCounter, hasHisto bool
	for _, m := range raw {
		switch m.Kind {
		case promrw.KindGauge:
			if m.Name == gaugeName {
				hasGauge = true
			}
		case promrw.KindCounter:
			if m.Name == counterName {
				hasCounter = true
			}
		case promrw.KindHistogram:
			if m.Name == histoName+"_count" {
				hasHisto = true
			}
		}
	}
	if hasGauge {
		t.Error("gauge must be absent after DeleteGauge (property 3 failed: gauge still present)")
	}
	if !hasCounter {
		t.Error("counter must remain after DeleteGauge on gauge (property 3 failed: counter gone)")
	}
	if !hasHisto {
		t.Error("histogram must remain after DeleteGauge on gauge (property 3 failed: histogram gone)")
	}
	// Verify after-delete collection matches expectation.
	_ = after
}

func TestCollectHistosConvertsCumulativeToPerBucket(t *testing.T) {
	s := NewState()
	bounds := []float64{1, 2, 3}
	labels := map[string]string{"http.route": "/a"}
	// Observations: 0.5 (→bucket0), 1.5 (→bucket1), 2.5 (→bucket2), 10 (→+Inf overflow).
	for _, v := range []float64{0.5, 1.5, 2.5, 10} {
		s.Observe("http.server.request.duration", labels, bounds, LEDotZero, v)
	}
	pts := s.CollectHistos()
	if len(pts) != 1 {
		t.Fatalf("got %d points, want 1", len(pts))
	}
	p := pts[0]
	if p.Name != "http.server.request.duration" {
		t.Errorf("name = %q", p.Name)
	}
	if p.Count != 4 || p.Sum != 14.5 {
		t.Errorf("count/sum = %d/%v, want 4/14.5", p.Count, p.Sum)
	}
	// Per-bucket (non-cumulative): one in each finite bucket + one in +Inf overflow.
	want := []uint64{1, 1, 1, 1} // len == len(bounds)+1
	if len(p.BucketCounts) != 4 {
		t.Fatalf("BucketCounts len = %d, want 4 (bounds+1)", len(p.BucketCounts))
	}
	for i := range want {
		if p.BucketCounts[i] != want[i] {
			t.Errorf("BucketCounts[%d] = %d, want %d", i, p.BucketCounts[i], want[i])
		}
	}
	if len(p.Bounds) != 3 {
		t.Errorf("Bounds len = %d, want 3", len(p.Bounds))
	}
}

func TestCollectHistosDoesNotDrain(t *testing.T) {
	s := NewState()
	bounds := []float64{1}
	s.Observe("h", map[string]string{}, bounds, LEDotZero, 0.5)
	_ = s.CollectHistos()
	pts := s.CollectHistos() // second call must still see the observation (cumulative, not drained)
	if len(pts) != 1 || pts[0].Count != 1 {
		t.Fatalf("second CollectHistos lost state: %+v", pts)
	}
}

// TestCollectAliasesLabels documents and enforces the aliasing contract: Collect returns
// Series whose Labels alias the live per-series label maps (NOT deep-copied). The two
// scalar Series from the same logical series identity must be the SAME map header so the
// scoped writer's clone-before-stamp is load-bearing.
func TestCollectAliasesLabels(t *testing.T) {
	now := time.Now()
	s := NewState()
	s.Set("g", map[string]string{"a": "b"}, 1)
	first := s.Collect(now)
	second := s.Collect(now)
	// Two Collect calls return Series that alias the SAME live label map. Probe that by
	// mutating the map via one view and observing the change through the other.
	first[0].Labels["a"] = "STAMPED"
	if second[0].Labels["a"] != "STAMPED" {
		t.Fatal("Collect must ALIAS the live label map (mutating one collected view affects the live map); callers must clone before stamping")
	}
}
