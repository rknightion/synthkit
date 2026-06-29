// SPDX-License-Identifier: AGPL-3.0-only

package ledger

import (
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/shape"
)

// fakeMinter mints a fixed count per call and records the tickSec it was handed.
type fakeMinter struct {
	name     string
	perTick  int
	dur      time.Duration // optional per-request Duration (0 = unset)
	lastTick float64
	calls    int
}

func (f *fakeMinter) Workload() string { return f.name }

func (f *fakeMinter) Mint(now time.Time, tickSec float64, _ *shape.Engine) []*Request {
	f.lastTick = tickSec
	f.calls++
	out := make([]*Request, 0, f.perTick)
	for range f.perTick {
		r := &Request{Correlation: NewCorrelation(), Workload: f.name, Start: now, Duration: f.dur}
		out = append(out, r)
	}
	return out
}

func eng() *shape.Engine { return shape.New("", nil) }

func TestMintCollectsFromEveryMinterAndReturnsBatch(t *testing.T) {
	l := New(eng(), 0, 0)
	a := &fakeMinter{name: "api", perTick: 3}
	b := &fakeMinter{name: "web", perTick: 2}
	l.AddMinter(a)
	l.AddMinter(b)

	now := time.Now()
	batch := l.Mint(now)
	if len(batch) != 5 {
		t.Fatalf("Mint returned %d requests, want 5", len(batch))
	}
	if l.Len() != 5 {
		t.Fatalf("ring holds %d, want 5", l.Len())
	}
	if a.calls != 1 || b.calls != 1 {
		t.Fatalf("minter calls = %d,%d; want 1,1", a.calls, b.calls)
	}
}

func TestMintStampsRenderOffsetDeterministically(t *testing.T) {
	l := New(eng(), 0, 0)
	l.AddMinter(&fakeMinter{name: "api", perTick: 8})
	batch := l.Mint(time.Now())
	for _, r := range batch {
		if r.RenderOffset < 0 || r.RenderOffset >= RenderJitterWindow {
			t.Fatalf("RenderOffset %v outside [0,%v)", r.RenderOffset, RenderJitterWindow)
		}
		if got := renderOffsetFor(r.TraceID); got != r.RenderOffset {
			t.Fatalf("offset not deterministic: %v vs %v", got, r.RenderOffset)
		}
		if !r.RenderStart().Equal(r.Start.Add(-r.Duration).Add(-r.RenderOffset)) {
			t.Fatalf("RenderStart mismatch")
		}
	}
}

// TestRenderStartBackdatesToCompletion: a fabricated trace is BACKDATED so it ENDS at ~Start (the
// mint instant ≈ now), jittered back by RenderOffset, and STARTS a full Duration before that — i.e.
// RenderStart = Start − Duration − RenderOffset, and the trace end (RenderStart+Duration) never
// lands after Start.
func TestRenderStartBackdatesToCompletion(t *testing.T) {
	now := time.Date(2026, 6, 16, 13, 0, 0, 0, time.UTC)
	r := &Request{Start: now, Duration: 9 * time.Second, RenderOffset: 2 * time.Second}

	if want := now.Add(-11 * time.Second); !r.RenderStart().Equal(want) {
		t.Fatalf("RenderStart = %v, want %v (Start − Duration − RenderOffset)", r.RenderStart(), want)
	}
	end := r.RenderStart().Add(r.Duration)
	if want := now.Add(-2 * time.Second); !end.Equal(want) {
		t.Fatalf("trace end = %v, want %v (Start − RenderOffset)", end, want)
	}
	if end.After(now) {
		t.Fatalf("trace end %v is in the future (after now=%v) — backdating must end ≤ now", end, now)
	}
}

// TestActiveUnaffectedByBackdating: Active()/retention key on Start, NOT RenderStart. A request with
// a large Duration (so RenderStart is far in the past) still appears in the Active window keyed on
// its Start — backdating the render time must not shift windowing.
func TestActiveUnaffectedByBackdating(t *testing.T) {
	l := New(eng(), 0, 0)
	l.AddMinter(&fakeMinter{name: "api", perTick: 1, dur: time.Hour}) // huge dur → RenderStart ~1h ago
	now := time.Date(2026, 6, 16, 13, 0, 0, 0, time.UTC)
	l.Mint(now)

	got := l.Active(now, time.Minute)
	if len(got) != 1 {
		t.Fatalf("Active returned %d, want 1 (windowing keys on Start, not RenderStart)", len(got))
	}
	if got[0].RenderStart().After(now.Add(-time.Hour)) {
		t.Fatalf("expected RenderStart ~1h before now (backdated by Duration), got %v", got[0].RenderStart())
	}
}

func TestMintPassesConfiguredTickSeconds(t *testing.T) {
	l := New(eng(), 0, 0)
	m := &fakeMinter{name: "api", perTick: 1}
	l.AddMinter(m)
	l.SetTickSeconds(5)
	l.Mint(time.Now())
	if m.lastTick != 5 {
		t.Fatalf("minter handed tickSec=%v, want 5", m.lastTick)
	}
}

func TestMintDefaultsToReferenceTickSeconds(t *testing.T) {
	l := New(eng(), 0, 0)
	m := &fakeMinter{name: "api", perTick: 1}
	l.AddMinter(m)
	l.Mint(time.Now())
	if m.lastTick != ReferenceTickSeconds {
		t.Fatalf("minter handed tickSec=%v, want reference %v", m.lastTick, ReferenceTickSeconds)
	}
}

func TestActiveWindowing(t *testing.T) {
	l := New(eng(), 0, 0)
	m := &fakeMinter{name: "api", perTick: 1}
	l.AddMinter(m)

	t0 := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	l.Mint(t0)                       // in window at t0+60s? 60s window → from=t0 → Start.After(from) is FALSE → excluded
	l.Mint(t0.Add(30 * time.Second)) // in (t0, t0+60] → included
	l.Mint(t0.Add(70 * time.Second)) // after now → excluded

	now := t0.Add(60 * time.Second)
	got := l.Active(now, time.Minute)
	if len(got) != 1 {
		t.Fatalf("Active returned %d, want 1 (newest-inclusive, oldest-exclusive)", len(got))
	}
	if !got[0].Start.Equal(t0.Add(30 * time.Second)) {
		t.Fatalf("wrong request in window: start=%v", got[0].Start)
	}
}

func TestActiveForFiltersByWorkload(t *testing.T) {
	l := New(eng(), 0, 0)
	l.AddMinter(&fakeMinter{name: "api", perTick: 2})
	l.AddMinter(&fakeMinter{name: "web", perTick: 3})
	now := time.Now()
	l.Mint(now)
	got := l.ActiveFor("web", now.Add(time.Second), time.Minute)
	if len(got) != 3 {
		t.Fatalf("ActiveFor(web) returned %d, want 3", len(got))
	}
	for _, r := range got {
		if r.Workload != "web" {
			t.Fatalf("foreign workload %q leaked into ActiveFor(web)", r.Workload)
		}
	}
}

func TestRetentionTrimsOldRequests(t *testing.T) {
	l := New(eng(), 0, time.Minute)
	m := &fakeMinter{name: "api", perTick: 2}
	l.AddMinter(m)
	t0 := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	l.Mint(t0)
	l.Mint(t0.Add(2 * time.Minute)) // trim pass drops the t0 batch (older than 1m retain)
	if l.Len() != 2 {
		t.Fatalf("ring holds %d after retention trim, want 2", l.Len())
	}
}

func TestCapBackstopBoundsRing(t *testing.T) {
	l := New(eng(), 3, 0)
	m := &fakeMinter{name: "api", perTick: 2}
	l.AddMinter(m)
	now := time.Now()
	l.Mint(now)
	l.Mint(now.Add(time.Second))
	if l.Len() != 3 {
		t.Fatalf("ring holds %d with cap 3, want 3 (oldest evicted)", l.Len())
	}
}

func TestStochasticRoundConverges(t *testing.T) {
	// 0.3 expected → mean of draws should be ≈0.3 with draws u<0.3 → 1.
	if got := StochasticRound(2.0, 0.99); got != 2 {
		t.Fatalf("StochasticRound(2.0, .99)=%d, want 2", got)
	}
	if got := StochasticRound(2.3, 0.1); got != 3 {
		t.Fatalf("StochasticRound(2.3, .1)=%d, want 3 (fraction carries)", got)
	}
	if got := StochasticRound(2.3, 0.9); got != 2 {
		t.Fatalf("StochasticRound(2.3, .9)=%d, want 2", got)
	}
	if got := StochasticRound(-1, 0.5); got != 0 {
		t.Fatalf("StochasticRound(-1)=%d, want 0", got)
	}
}
