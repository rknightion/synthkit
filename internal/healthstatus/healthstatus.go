// SPDX-License-Identifier: AGPL-3.0-only

// Package healthstatus folds the runner's tick + cycle observations into an in-process health
// snapshot for the control plane. It is a SECOND consumer of the runner's tick/cycle seams (selfobs
// is the first); it imports only stdlib, keeping the OTel-SDK ban intact — mirroring how
// internal/pushstatus consumes internal/pushhook.
package healthstatus

import (
	"context"
	"sort"
	"sync"
	"time"
)

const maxSamples = 30 // bounded duration ring per construct → p95 + sparkline

// ConstructHealth is per-instance tick health, keyed by blueprint/kind/name.
type ConstructHealth struct {
	Blueprint   string  `json:"blueprint"`
	Kind        string  `json:"kind"`
	Name        string  `json:"name"`
	Ticks       int64   `json:"ticks"`
	Errors      int64   `json:"errors"`
	LastOutcome string  `json:"last_outcome"` // "ok" | "error"
	LastError   string  `json:"last_error"`
	LastTickMs  int64   `json:"last_tick_ms"`
	LastDurMs   float64 `json:"last_dur_ms"`
	P95DurMs    float64 `json:"p95_dur_ms"`
	Spark       []int   `json:"spark"` // recent dur in ms, oldest→newest
}

// BlueprintHealth is per-blueprint cycle health (incl. a bounded cycle-duration ring → sparkline,
// per spec §4.2).
type BlueprintHealth struct {
	Blueprint    string  `json:"blueprint"`
	DroppedTicks int64   `json:"dropped_ticks"`
	LastCycleMs  float64 `json:"last_cycle_ms"`
	Cycles       int64   `json:"cycles"`
	CycleSpark   []int   `json:"cycle_spark"` // recent cycle durations in ms, oldest→newest (cap maxSamples)
}

// Report is the full health snapshot served at GET /control/health (process metrics are added by the
// handler, not stored here).
type Report struct {
	Constructs []ConstructHealth `json:"constructs"`
	Blueprints []BlueprintHealth `json:"blueprints"`
}

type constructAgg struct {
	h       ConstructHealth
	samples []float64 // dur ms ring
}

type bpAgg struct {
	h       BlueprintHealth
	samples []float64 // cycle dur ms ring
}

// Store folds tick + cycle observations into per-construct and per-blueprint health stats.
// Safe for concurrent observe/read.
type Store struct {
	mu     sync.Mutex
	byInst map[string]*constructAgg
	byBp   map[string]*bpAgg
	now    func() time.Time
}

// NewStore returns an empty store using the wall clock.
func NewStore() *Store {
	return &Store{byInst: map[string]*constructAgg{}, byBp: map[string]*bpAgg{}, now: time.Now}
}

func key(bp, kind, name string) string { return bp + "\x00" + kind + "\x00" + name }

// TickFunc returns a runner.TickFunc-shaped closure (untyped here to avoid importing runner) that
// times fn, records the outcome via RecordOutcome, and returns fn's error UNCHANGED. fn runs exactly
// once. NOTE: the composition root does NOT use this — it calls RecordOutcome directly because it must
// nest the tick inside selfobs.ObserveTick (one fn call only). TickFunc() exists for standalone use +
// the unit test.
func (s *Store) TickFunc() func(ctx context.Context, blueprint, kind, name string, fn func(context.Context) error) error {
	return func(ctx context.Context, blueprint, kind, name string, fn func(context.Context) error) error {
		start := s.now()
		err := fn(ctx)
		s.RecordOutcome(blueprint, kind, name, s.now().Sub(start), err)
		return err
	}
}

// RecordOutcome folds one completed tick (duration + outcome) into the per-construct stat. Exported
// so the composition root's tick fan-out can record AFTER selfobs.ObserveTick runs fn (avoiding a
// double fn call). Safe for concurrent use.
func (s *Store) RecordOutcome(bp, kind, name string, dur time.Duration, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := key(bp, kind, name)
	a := s.byInst[k]
	if a == nil {
		a = &constructAgg{h: ConstructHealth{Blueprint: bp, Kind: kind, Name: name}}
		s.byInst[k] = a
	}
	a.h.Ticks++
	a.h.LastTickMs = s.now().UnixMilli()
	a.h.LastDurMs = float64(dur.Microseconds()) / 1000
	if err != nil {
		a.h.Errors++
		a.h.LastOutcome = "error"
		a.h.LastError = err.Error()
	} else {
		a.h.LastOutcome = "ok"
		a.h.LastError = ""
	}
	a.samples = append(a.samples, a.h.LastDurMs)
	if len(a.samples) > maxSamples {
		a.samples = a.samples[len(a.samples)-maxSamples:]
	}
}

// ObserveCycle is the runner.CycleFunc-shaped consumer.
func (s *Store) ObserveCycle(_ context.Context, blueprint string, dur time.Duration, dropped int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b := s.byBp[blueprint]
	if b == nil {
		b = &bpAgg{h: BlueprintHealth{Blueprint: blueprint}}
		s.byBp[blueprint] = b
	}
	b.h.Cycles++
	b.h.LastCycleMs = float64(dur.Microseconds()) / 1000
	b.h.DroppedTicks += int64(dropped)
	b.samples = append(b.samples, b.h.LastCycleMs)
	if len(b.samples) > maxSamples {
		b.samples = b.samples[len(b.samples)-maxSamples:]
	}
}

// Snapshot returns a copy of every construct's and blueprint's current health stat, with sparklines
// and p95 filled. Sorted by blueprint then name (constructs) and by blueprint (blueprints).
func (s *Store) Snapshot() Report {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := Report{}
	for _, a := range s.byInst {
		h := a.h
		h.Spark = make([]int, len(a.samples))
		for i, d := range a.samples {
			h.Spark[i] = int(d)
		}
		h.P95DurMs = p95(a.samples)
		out.Constructs = append(out.Constructs, h)
	}
	for _, b := range s.byBp {
		h := b.h
		h.CycleSpark = make([]int, len(b.samples))
		for i, d := range b.samples {
			h.CycleSpark[i] = int(d)
		}
		out.Blueprints = append(out.Blueprints, h)
	}
	sort.Slice(out.Constructs, func(i, j int) bool {
		if out.Constructs[i].Blueprint != out.Constructs[j].Blueprint {
			return out.Constructs[i].Blueprint < out.Constructs[j].Blueprint
		}
		return out.Constructs[i].Name < out.Constructs[j].Name
	})
	sort.Slice(out.Blueprints, func(i, j int) bool { return out.Blueprints[i].Blueprint < out.Blueprints[j].Blueprint })
	return out
}

// p95 returns the 95th-percentile of a small sample (nearest-rank). 0 when empty.
func p95(in []float64) float64 {
	if len(in) == 0 {
		return 0
	}
	cp := append([]float64(nil), in...)
	sort.Float64s(cp)
	idx := int(float64(len(cp)-1) * 0.95)
	return cp[idx]
}
