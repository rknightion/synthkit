// SPDX-License-Identifier: AGPL-3.0-only

package runner

import (
	"context"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/core"
)

// TestPhaseOffsetDeterministicAndInRange: the per-instance start offset must be stable across calls
// (cadence is reproducible — no rand) and always within [0, interval) so it never pushes the first
// due time past one full interval.
func TestPhaseOffsetDeterministicAndInRange(t *testing.T) {
	const interval = 60 * time.Second
	for _, name := range []string{"initech-prod-use1", "initech-stg-use1", "newco-db", "a", ""} {
		o1, o2 := phaseOffset(name, interval), phaseOffset(name, interval)
		if o1 != o2 {
			t.Errorf("phaseOffset(%q) not deterministic: %v vs %v", name, o1, o2)
		}
		if o1 < 0 || o1 >= interval {
			t.Errorf("phaseOffset(%q)=%v out of [0,%v)", name, o1, interval)
		}
	}
}

// TestPhaseOffsetZeroIntervalSafe: a non-positive interval must not divide-by-zero — it yields a zero
// offset (the instance keeps the old nextDue=now behaviour).
func TestPhaseOffsetZeroIntervalSafe(t *testing.T) {
	for _, iv := range []time.Duration{0, -time.Second} {
		if got := phaseOffset("x", iv); got != 0 {
			t.Errorf("phaseOffset(x, %v)=%v, want 0", iv, got)
		}
	}
}

// TestPhaseOffsetSpreadsNamesAcrossMasterTicks: the whole point of staggering — instances sharing the
// DPM-floor interval must not all fall due on the same master-tick bucket. Deterministic names, so the
// assertion is stable (not flaky).
func TestPhaseOffsetSpreadsNamesAcrossMasterTicks(t *testing.T) {
	const interval = 60 * time.Second
	const master = 5 * time.Second
	names := []string{"c0", "c1", "c2", "c3", "c4", "c5", "c6", "c7", "c8", "c9"}
	buckets := map[int64]struct{}{}
	for _, n := range names {
		buckets[int64(phaseOffset(n, interval)/master)] = struct{}{}
	}
	// With 10 names over 12 buckets, a healthy hash spreads into several distinct buckets; demand at
	// least half to guard against a degenerate (constant) offset slipping in.
	if len(buckets) < len(names)/2 {
		t.Fatalf("phase offsets clustered: %d distinct master-tick buckets for %d names", len(buckets), len(names))
	}
}

func TestPhaseOffsetKeepsTenSecondHighDPMCadenceSpread(t *testing.T) {
	const interval = 10 * time.Second
	const master = 5 * time.Second
	buckets := map[int64]struct{}{}
	for _, name := range []string{"c0", "c1", "c2", "c3", "c4", "c5", "c6", "c7"} {
		buckets[int64(phaseOffset(name, interval)/master)] = struct{}{}
	}
	if len(buckets) != 2 {
		t.Fatalf("10s high-DPM offsets occupy %d master-tick buckets, want both available buckets", len(buckets))
	}
}

// Startup seeding uses the first epoch-anchored phase at or after the supplied wall clock,
// for constructs and workloads alike.
func TestSeedPhasesAppliesPerInstanceOffset(t *testing.T) {
	r := timingRunner(t, 5*time.Second, 0)
	if err := r.AddBlueprint(buildTestResolved("alpha")); err != nil {
		t.Fatal(err)
	}
	injectConstruct(t, r, "alpha", "inst-one", &sleepConstruct{}, 60*time.Second)
	injectConstruct(t, r, "alpha", "inst-two", &sleepConstruct{}, 60*time.Second)

	epoch := time.Unix(1_800_000_000, 0)
	now := epoch.Add(17 * time.Second)
	r.seedPhases(now)

	for _, bp := range r.bps {
		for _, bc := range bp.constructs {
			want := epoch.Add(phaseOffset(bc.name, bc.interval))
			if want.Before(now) {
				want = want.Add(bc.interval)
			}
			if !bc.nextDue.Equal(want) {
				t.Errorf("construct %q nextDue=%v, want epoch-anchored phase=%v", bc.name, bc.nextDue, want)
			}
			if bc.nextDue.Before(now) || !bc.nextDue.Before(now.Add(bc.interval)) {
				t.Errorf("construct %q nextDue=%v out of [%v, %v)", bc.name, bc.nextDue, now, now.Add(bc.interval))
			}
		}
		for _, bw := range bp.workloads {
			want := epoch.Add(phaseOffset(bw.workload.Name(), bw.interval))
			if want.Before(now) {
				want = want.Add(bw.interval)
			}
			if !bw.nextDue.Equal(want) {
				t.Errorf("workload %q nextDue=%v, want epoch-anchored phase=%v", bw.workload.Name(), bw.nextDue, want)
			}
		}
	}
}

func TestNextPhaseDueWallClock(t *testing.T) {
	for _, interval := range []time.Duration{time.Minute, 10 * time.Second, 7*time.Second + 13*time.Nanosecond, time.Nanosecond} {
		for _, name := range []string{"cadence-instance", "other-instance", ""} {
			phase := phaseOffset(name, interval)
			for _, cycle := range []int64{-10, 0, 10, 20_000_000} {
				boundary := time.Unix(0, cycle*int64(interval)+int64(phase))
				for _, delta := range []time.Duration{-time.Nanosecond, 0, time.Nanosecond, interval / 2} {
					now := boundary.Add(delta)
					want := boundary
					for want.Before(now) {
						want = want.Add(interval)
					}
					for !want.Add(-interval).Before(now) {
						want = want.Add(-interval)
					}
					got := nextPhaseDue(now, name, interval)
					if !got.Equal(want) || got.Before(now) || !got.Before(now.Add(interval)) {
						t.Errorf("nextPhaseDue(%v, %q, %v)=%v, want %v in [now, now+interval)", now, name, interval, got, want)
					}
					if localized := nextPhaseDue(now.In(time.FixedZone("test", 3600)), name, interval); !localized.Equal(got) {
						t.Errorf("location changed due time: %v vs %v", localized, got)
					}
				}
			}
		}
	}
	for _, interval := range []time.Duration{0, -time.Second} {
		now := time.Unix(123, 456)
		if got := nextPhaseDue(now, "cadence-instance", interval); !got.Equal(now) {
			t.Errorf("non-positive interval due=%v, want now=%v", got, now)
		}
	}
}

// Drive the actual metric dispatch with a fake wall clock, including a late tick,
// a mid-interval restart and a missed interval. Neither lane may move its cadence.
func TestPhaseRestartMidInterval(t *testing.T) {
	const interval = time.Minute
	const name = "cadence-instance"
	newRunner := func() (*Runner, *fakeConstruct, *fakeWorkload) {
		r := timingRunner(t, 5*time.Second, 0)
		c := &fakeConstruct{kind: "fake_scoped"}
		w := &fakeWorkload{name: name}
		r.bps = []*bpRuntime{{
			name:       "alpha",
			constructs: []*boundConstruct{{name: name, kind: c.kind, construct: c, interval: interval, world: &core.World{}}},
			workloads:  []*boundWorkload{{kind: w.Kind(), workload: w, interval: interval, world: &core.World{}}},
		}}
		return r, c, w
	}
	assertDue := func(r *Runner, want time.Time) {
		t.Helper()
		if got := r.bps[0].constructs[0].nextDue; !got.Equal(want) {
			t.Errorf("construct due=%v, want epoch cadence %v", got, want)
		}
		if got := r.bps[0].workloads[0].nextDue; !got.Equal(want) {
			t.Errorf("workload due=%v, want epoch cadence %v", got, want)
		}
	}

	// This epoch is divisible by the interval; advancing now is our fake clock.
	now := time.Unix(1_800_000_000, 0)
	firstDue := now.Add(phaseOffset(name, interval))
	running, c, w := newRunner()
	running.seedPhases(now)
	assertDue(running, firstDue)
	now = firstDue.Add(2 * time.Second) // master tick arrives late
	running.tickBlueprintInstances(context.Background(), running.bps[0], now)
	if c.ticks != 1 || w.ticks != 1 {
		t.Fatalf("late tick dispatched construct/workload %d/%d times, want 1/1", c.ticks, w.ticks)
	}
	assertDue(running, firstDue.Add(interval))

	now = firstDue.Add(interval / 2)
	restarted, rc, rw := newRunner()
	restarted.seedPhases(now)
	assertDue(restarted, firstDue.Add(interval))
	restarted.tickBlueprintInstances(context.Background(), restarted.bps[0], now)
	if rc.ticks != 0 || rw.ticks != 0 {
		t.Fatal("restart emitted before the next scheduled phase")
	}
	now = firstDue.Add(interval)
	for _, r := range []*Runner{running, restarted} {
		r.tickBlueprintInstances(context.Background(), r.bps[0], now)
		assertDue(r, firstDue.Add(2*interval))
	}
	if c.ticks != 2 || w.ticks != 2 || rc.ticks != 1 || rw.ticks != 1 {
		t.Fatalf("scheduled phase did not dispatch once: running=%d/%d restarted=%d/%d", c.ticks, w.ticks, rc.ticks, rw.ticks)
	}

	now = firstDue.Add(4*interval + 2*time.Second) // no backfill burst after a gap
	for _, r := range []*Runner{running, restarted} {
		r.tickBlueprintInstances(context.Background(), r.bps[0], now)
		assertDue(r, firstDue.Add(5*interval))
	}
	if c.ticks != 3 || w.ticks != 3 || rc.ticks != 2 || rw.ticks != 2 {
		t.Fatal("missed interval must dispatch only once")
	}
}
