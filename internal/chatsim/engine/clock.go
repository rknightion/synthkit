// SPDX-License-Identifier: AGPL-3.0-only

package engine

import (
	"sync"
	"time"
)

// RealClock is the wall clock.
type RealClock struct{}

// Now returns time.Now().
func (RealClock) Now() time.Time { return time.Now() }

// NewTimer wraps time.NewTimer.
func (RealClock) NewTimer(d time.Duration) Timer { return realTimer{time.NewTimer(d)} }

type realTimer struct{ t *time.Timer }

func (r realTimer) C() <-chan time.Time { return r.t.C }
func (r realTimer) Stop() bool          { return r.t.Stop() }

// FakeClock is a manually advanced clock for tests.
type FakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*fakeTimer
}

// NewFakeClock starts a fake clock at start.
func NewFakeClock(start time.Time) *FakeClock { return &FakeClock{now: start} }

// Now returns the fake time.
func (f *FakeClock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

// Set moves the clock to t (never backwards) and fires due timers.
func (f *FakeClock) Set(t time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if t.After(f.now) {
		f.now = t
	}
	kept := f.timers[:0]
	for _, tm := range f.timers {
		if tm.stopped {
			continue
		}
		if !tm.due.After(f.now) {
			tm.stopped = true
			tm.ch <- f.now
			continue
		}
		kept = append(kept, tm)
	}
	f.timers = kept
}

// Advance moves the clock forward by d.
func (f *FakeClock) Advance(d time.Duration) { f.Set(f.Now().Add(d)) }

// NewTimer returns a timer that fires when the clock reaches now+d.
func (f *FakeClock) NewTimer(d time.Duration) Timer {
	f.mu.Lock()
	tm := &fakeTimer{due: f.now.Add(d), ch: make(chan time.Time, 1), clock: f}
	f.timers = append(f.timers, tm)
	f.mu.Unlock()
	if d <= 0 {
		f.Set(f.Now())
	}
	return tm
}

type fakeTimer struct {
	due     time.Time
	ch      chan time.Time
	stopped bool
	clock   *FakeClock
}

func (t *fakeTimer) C() <-chan time.Time { return t.ch }

func (t *fakeTimer) Stop() bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	was := !t.stopped
	t.stopped = true
	return was
}
