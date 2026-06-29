// SPDX-License-Identifier: AGPL-3.0-only

// Package pushstatus records the most recent synthetic-data push outcome per sink so the
// control plane can render sink readiness. It is a SECOND consumer of the pushhook seam
// (selfobs is the first); it imports only stdlib + pushhook, keeping the OTel-SDK ban intact.
package pushstatus

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/rknightion/synthkit/internal/pushhook"
)

// maxSamples bounds the per-sink sparkline ring: at one push every few seconds this keeps a
// couple of minutes of recent per-push item counts — enough for a trend, bounded in memory.
const maxSamples = 30

// SinkStat is the observed push history for one sink. The 5s tick cadence makes a single
// "last N items" reading near-meaningless, so the panel reads cumulative totals, a rolling
// rate, and a short sparkline of recent per-push item counts instead.
type SinkStat struct {
	Sink          string  `json:"sink"`
	LastSuccessMs int64   `json:"last_success_ms"`
	LastErrorMs   int64   `json:"last_error_ms"`
	LastError     string  `json:"last_error"`
	Pushes        int64   `json:"pushes"`
	Failures      int64   `json:"failures"`
	LastItems     int     `json:"last_items"`
	LastStatus    int     `json:"last_status"`
	TotalItems    int64   `json:"total_items"`  // cumulative items over real (non-dry-run) pushes
	RatePerMin    float64 `json:"rate_per_min"` // rolling items/min over the sample ring (0 when <2 samples)
	Spark         []int   `json:"spark"`        // recent per-push item counts, oldest→newest (cap maxSamples)
	DryRun        bool    `json:"dry_run"`
}

// sample is one real push's item count stamped with its wall-clock millis, kept in the ring
// so Snapshot can derive both the sparkline and a windowed rate.
type sample struct {
	ms    int64
	items int
}

// sinkAgg holds the folded stat plus the bounded sample ring for one sink.
type sinkAgg struct {
	stat    SinkStat
	samples []sample
}

// BlueprintEmission is the observed push history rolled up per blueprint name (cross-sink).
type BlueprintEmission struct {
	Blueprint  string  `json:"blueprint"`
	TotalItems int64   `json:"total_items"`
	RatePerMin float64 `json:"rate_per_min"` // rolling items/min over the sample ring (0 when <2 samples)
	Spark      []int   `json:"spark"`        // recent per-push item counts, oldest→newest (cap maxSamples)
}

// bpAgg holds folded per-blueprint stats plus the bounded sample ring.
type bpAgg struct {
	bp      string
	total   int64
	samples []sample
}

// Store folds push events into per-sink stats. Safe for concurrent observe/read.
type Store struct {
	mu          sync.Mutex
	bySink      map[string]*sinkAgg
	byBlueprint map[string]*bpAgg
	now         func() time.Time
}

// NewStore returns an empty store using the wall clock.
func NewStore() *Store {
	return &Store{bySink: map[string]*sinkAgg{}, byBlueprint: map[string]*bpAgg{}, now: time.Now}
}

// Observer returns a pushhook.Observer that folds each event into the per-sink stat.
func (s *Store) Observer() pushhook.Observer {
	return func(_ context.Context, ev pushhook.Event) {
		s.mu.Lock()
		defer s.mu.Unlock()
		a := s.bySink[ev.Sink]
		if a == nil {
			a = &sinkAgg{stat: SinkStat{Sink: ev.Sink}}
			s.bySink[ev.Sink] = a
		}
		st := &a.stat
		st.LastItems = ev.Items
		st.LastStatus = ev.Status
		st.DryRun = ev.DryRun
		if ev.DryRun {
			return // dry-run is not a real push; don't count, stamp, or sample
		}
		st.Pushes++
		st.TotalItems += int64(ev.Items)
		ms := s.now().UnixMilli()
		a.samples = append(a.samples, sample{ms: ms, items: ev.Items})
		if len(a.samples) > maxSamples {
			a.samples = a.samples[len(a.samples)-maxSamples:]
		}
		if ev.Err != nil {
			st.Failures++
			st.LastErrorMs = ms
			st.LastError = ev.Err.Error()
			return
		}
		st.LastSuccessMs = ms

		// Per-blueprint fold (additive; skipped when Blueprint is empty — substrate/unscoped).
		if ev.Blueprint != "" {
			b := s.byBlueprint[ev.Blueprint]
			if b == nil {
				b = &bpAgg{bp: ev.Blueprint}
				s.byBlueprint[ev.Blueprint] = b
			}
			b.total += int64(ev.Items)
			b.samples = append(b.samples, sample{ms: ms, items: ev.Items})
			if len(b.samples) > maxSamples {
				b.samples = b.samples[len(b.samples)-maxSamples:]
			}
		}
	}
}

// SnapshotByBlueprint returns per-blueprint emission totals, keyed by blueprint name.
// Purely additive: all existing per-sink behaviour is byte-identical.
func (s *Store) SnapshotByBlueprint() map[string]BlueprintEmission {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]BlueprintEmission, len(s.byBlueprint))
	for bp, b := range s.byBlueprint {
		spark := make([]int, len(b.samples))
		for i, sm := range b.samples {
			spark[i] = sm.items
		}
		out[bp] = BlueprintEmission{
			Blueprint:  bp,
			TotalItems: b.total,
			RatePerMin: ratePerMin(b.samples),
			Spark:      spark,
		}
	}
	return out
}

// Snapshot returns a copy of every sink's current stat (with sparkline + rolling rate filled),
// sorted by sink name.
func (s *Store) Snapshot() []SinkStat {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]SinkStat, 0, len(s.bySink))
	for _, a := range s.bySink {
		st := a.stat
		st.Spark = make([]int, len(a.samples))
		for i, sm := range a.samples {
			st.Spark[i] = sm.items
		}
		st.RatePerMin = ratePerMin(a.samples)
		out = append(out, st)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Sink < out[j].Sink })
	return out
}

// ratePerMin derives an items-per-minute rate from the ring over the span between the first and
// last sample. Only the items delivered WITHIN that span count — i.e. samples[1:], the pushes that
// occurred after the window opened (the first sample's items were delivered before first.ms, in an
// interval the window doesn't cover). Counting all N samples over the N-1 intervals would
// over-report by N/(N-1). Returns 0 with fewer than two samples (no span) or a non-positive span.
func ratePerMin(samples []sample) float64 {
	if len(samples) < 2 {
		return 0
	}
	spanMs := samples[len(samples)-1].ms - samples[0].ms
	if spanMs <= 0 {
		return 0
	}
	var total int64
	for _, sm := range samples[1:] {
		total += int64(sm.items)
	}
	return float64(total) / (float64(spanMs) / 1000) * 60
}
