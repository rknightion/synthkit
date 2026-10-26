// SPDX-License-Identifier: AGPL-3.0-only

package shape

import (
	"math"
	"testing"
	"time"
)

func TestBatchValidate(t *testing.T) {
	for _, tc := range []struct {
		name string
		b    Batch
		ok   bool
	}{
		{"cadence", Batch{Period: 30 * time.Minute}, true},
		{"phase", Batch{Period: 15 * time.Minute, Phase: 5 * time.Minute}, true},
		{"daily", Batch{Period: MaxBatchPeriod}, true},
		{"zero", Batch{}, false},
		{"negative", Batch{Period: -time.Minute}, false},
		{"too long", Batch{Period: MaxBatchPeriod + time.Second}, false},
		{"phase at period", Batch{Period: time.Hour, Phase: time.Hour}, false},
		{"negative phase", Batch{Period: time.Hour, Phase: -time.Minute}, false},
	} {
		if err := tc.b.Validate(); (err == nil) != tc.ok {
			t.Errorf("%s: Validate = %v, want ok=%v", tc.name, err, tc.ok)
		}
	}
}

// TestBatchReleaseCadenceAndConservation: ticks one minute apart against a cadence release only
// on the cadence and conserve the smooth curve. The releases at the boundaries b1..bn deliver
// exactly the smooth ticks of [b0, bn), the day's whole windows, so the two totals agree up to
// float rounding (the same terms summed in a different order): the test allows 1e-9 relative.
func TestBatchReleaseCadenceAndConservation(t *testing.T) {
	c := DefaultCurve()
	load := func(at time.Time) float64 { return c.Diurnal(at.UTC()) }
	step := time.Minute
	day := time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC)
	for _, b := range []Batch{
		{Period: 30 * time.Minute},
		{Period: 15 * time.Minute, Phase: 4 * time.Minute},
		{Period: 45 * time.Minute, Phase: 10 * time.Minute}, // not a divisor of the day
	} {
		// b0 is the first boundary in the day; bn the last boundary within it.
		b0 := b.boundaryAtOrAfter(day)
		bn := b0
		for next := bn.Add(b.Period); !next.After(day.Add(24 * time.Hour)); next = next.Add(b.Period) {
			bn = next
		}
		var smooth, batched float64
		for tick := b0; tick.Before(bn); tick = tick.Add(step) {
			smooth += load(tick)
		}
		releases := 0
		for tick := b0.Add(step); !tick.After(bn); tick = tick.Add(step) {
			got := b.Release(tick, step, load)
			if got == 0 {
				continue
			}
			if off := tick.Sub(b0) % b.Period; off != 0 {
				t.Fatalf("%+v: release at %v is %v off the cadence", b, tick, off)
			}
			releases++
			batched += got
		}
		if want := int(bn.Sub(b0) / b.Period); releases != want {
			t.Fatalf("%+v: %d releases, want %d", b, releases, want)
		}
		if math.Abs(batched-smooth) > 1e-9*smooth {
			t.Fatalf("%+v: batched total %v, smooth total %v", b, batched, smooth)
		}
	}
}

// boundaryAtOrAfter is the first window boundary at or after t (test helper).
func (b Batch) boundaryAtOrAfter(t time.Time) time.Time {
	start := time.Unix(0, b.cycle(t)*int64(b.Period)+int64(b.Phase)).UTC()
	if start.Before(t) {
		start = start.Add(b.Period)
	}
	return start
}

// TestBatchReleaseZeroBetween: every tick between two releases reads exactly zero, and a release
// sums the whole closed window.
func TestBatchReleaseZeroBetween(t *testing.T) {
	b := Batch{Period: 15 * time.Minute, Phase: 3 * time.Minute}
	flat := func(time.Time) float64 { return 2 }
	start := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	var got []float64
	for m := 0; m < 40; m++ {
		got = append(got, b.Release(start.Add(time.Duration(m)*time.Minute), time.Minute, flat))
	}
	for m, v := range got {
		if m%15 == 3 { // 12:03, 12:18, 12:33: a release of fifteen ticks at load 2
			if v != 30 {
				t.Fatalf("minute %d: release %v, want 30", m, v)
			}
			continue
		}
		if v != 0 {
			t.Fatalf("minute %d: %v, want 0 between releases", m, v)
		}
	}
}

func TestBatchReleaseDegenerateIsSmooth(t *testing.T) {
	b := Batch{Period: time.Minute}
	flat := func(time.Time) float64 { return 0.7 }
	if got := b.Release(time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC), time.Minute, flat); got != 0.7 {
		t.Fatalf("period == step releases %v, want the smooth 0.7", got)
	}
}

// TestBatchReleaseDeterministic: the release is a pure function of its inputs.
func TestBatchReleaseDeterministic(t *testing.T) {
	b := Batch{Period: 30 * time.Minute}
	c := DefaultCurve()
	load := func(at time.Time) float64 { return c.Diurnal(at) }
	at := time.Date(2026, 6, 10, 9, 30, 0, 0, time.UTC)
	if a, z := b.Release(at, time.Minute, load), b.Release(at, time.Minute, load); a != z || a == 0 {
		t.Fatalf("release %v then %v, want equal and non-zero", a, z)
	}
}
