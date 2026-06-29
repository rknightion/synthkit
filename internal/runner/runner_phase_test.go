// SPDX-License-Identifier: AGPL-3.0-only

package runner

import (
	"testing"
	"time"
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

// TestSeedPhasesAppliesPerInstanceOffset: Run's startup seeding must set each instance's first nextDue
// to now + its own phase offset (constructs and workloads alike), so the live loop starts already
// de-synchronised. Verifies the wiring exactly (no timing/flakiness).
func TestSeedPhasesAppliesPerInstanceOffset(t *testing.T) {
	r := timingRunner(t, 5*time.Second, 0)
	if err := r.AddBlueprint(buildTestResolved("alpha")); err != nil {
		t.Fatal(err)
	}
	injectConstruct(t, r, "alpha", "inst-one", &sleepConstruct{}, 60*time.Second)
	injectConstruct(t, r, "alpha", "inst-two", &sleepConstruct{}, 60*time.Second)

	now := time.Now()
	r.seedPhases(now)

	for _, bp := range r.bps {
		for _, bc := range bp.constructs {
			want := now.Add(phaseOffset(bc.name, bc.interval))
			if !bc.nextDue.Equal(want) {
				t.Errorf("construct %q nextDue=%v, want now+offset=%v", bc.name, bc.nextDue, want)
			}
			if bc.nextDue.Before(now) || !bc.nextDue.Before(now.Add(bc.interval)) {
				t.Errorf("construct %q nextDue=%v out of [%v, %v)", bc.name, bc.nextDue, now, now.Add(bc.interval))
			}
		}
		for _, bw := range bp.workloads {
			want := now.Add(phaseOffset(bw.workload.Name(), bw.interval))
			if !bw.nextDue.Equal(want) {
				t.Errorf("workload %q nextDue=%v, want now+offset=%v", bw.workload.Name(), bw.nextDue, want)
			}
		}
	}
}
