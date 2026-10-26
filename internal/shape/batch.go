// SPDX-License-Identifier: AGPL-3.0-only

package shape

import (
	"fmt"
	"time"
)

// MaxBatchPeriod bounds a batch cadence: a source that delivers less often than daily has no
// daily curve to conserve.
const MaxBatchPeriod = 24 * time.Hour

// Batch is the opt-in batch arrival form: a source that delivers in batches instead of
// continuously. Arrivals accumulate through each cadence window at the same smooth load a
// continuous source would see, and the whole window is released at once at its end, so the
// emitted series is zero between releases and a spike per cadence. The release of one window
// sums the smooth per-tick arrivals of the window, so over any run of whole windows the batched
// source delivers exactly what the smooth curve would have.
//
// Release boundaries sit at Phase + k*Period counted from the Unix epoch, so they are
// timezone-independent and deterministic: no random draw, no state. A Batch is a value; the
// zero Batch is invalid (a nil *Batch declares no batching).
type Batch struct {
	// Period is the release cadence (for example 15m or 30m). Required, in (0, 24h].
	Period time.Duration `yaml:"period"`
	// Phase offsets the release boundaries from the epoch-aligned Period grid, in [0, Period).
	// Default 0: releases land on the wall-clock multiples of Period.
	Phase time.Duration `yaml:"phase"`
}

// Validate rejects a non-positive or over-long period and a phase outside [0, Period).
func (b Batch) Validate() error {
	if b.Period <= 0 || b.Period > MaxBatchPeriod {
		return fmt.Errorf("batch: period %v out of (0, %v]", b.Period, MaxBatchPeriod)
	}
	if b.Phase < 0 || b.Phase >= b.Period {
		return fmt.Errorf("batch: phase %v out of [0, period %v)", b.Phase, b.Period)
	}
	return nil
}

// cycle is the index of the cadence window holding t: windows are [Phase+k*Period, Phase+(k+1)*Period).
func (b Batch) cycle(t time.Time) int64 {
	n := t.UnixNano() - int64(b.Phase)
	p := int64(b.Period)
	q := n / p
	if n%p < 0 {
		q--
	}
	return q
}

// Release returns the load released at the tick `now`, for ticks spaced step apart: 0 unless a
// window boundary lies in (now-step, now], and then the sum of load over the ticks of the window
// that just closed. load is the smooth per-tick arrival level (the same value a continuous
// source would emit per tick, before the Base multiplier); it must be a pure function of time so
// the release is reproducible.
//
// Each tick's arrival belongs to the window holding it and is released at the first tick at or
// after that window's end, so a run of ticks covering whole windows loses nothing; a tick still
// inside an open window is delivered by a later release. A tick spacing the cadence does not
// exceed (period <= step) degenerates to continuous delivery and returns load(now). A gap in the
// ticks longer than step skips the boundaries inside it, and those windows are not released.
func (b Batch) Release(now time.Time, step time.Duration, load func(time.Time) float64) float64 {
	if step <= 0 || b.Period <= step {
		return load(now)
	}
	prev := b.cycle(now.Add(-step))
	if b.cycle(now) == prev {
		return 0
	}
	var sum float64
	for t := now.Add(-step); b.cycle(t) == prev; t = t.Add(-step) {
		sum += load(t)
	}
	return sum
}
