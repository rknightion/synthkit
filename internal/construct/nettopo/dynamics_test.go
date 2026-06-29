// SPDX-License-Identifier: AGPL-3.0-only

package nettopo

import (
	"fmt"
	"math"
	"testing"
	"time"
)

func TestDeviceUptime_BoundedBelowWrap(t *testing.T) {
	cold := time.Unix(1_700_000_100, 0)
	// far future to force long elapsed
	now := cold.Add(1000 * 24 * time.Hour)
	u := deviceUptimeSecs("spine-01", "seed", uptimeBaseMaxSecs, cold, now)
	if u < 0 || u >= wrapCeilingSecs {
		t.Fatalf("uptime %v out of [0, %v)", u, wrapCeilingSecs)
	}
}

func TestDeviceUptime_HonoursBaseAtColdStart(t *testing.T) {
	cold := time.Unix(1_750_000_000, 0)
	base := (100 * time.Hour).Seconds()
	// at cold start, no reboot has fired yet ⇒ uptime == base
	if got := deviceUptimeSecs("edge-fw-01", "seed", base, cold, cold); math.Abs(got-base) > 1 {
		t.Fatalf("cold-start uptime=%v want ~base %v", got, base)
	}
}

func TestDeviceUptime_DeterministicAndGrows(t *testing.T) {
	cold := time.Unix(1_750_000_000, 0)
	base := (10 * 24 * 3600.0)
	a := deviceUptimeSecs("leaf-03", "seed", base, cold, cold.Add(time.Hour))
	b := deviceUptimeSecs("leaf-03", "seed", base, cold, cold.Add(time.Hour))
	if a != b {
		t.Fatalf("non-deterministic: %v != %v", a, b)
	}
	later := deviceUptimeSecs("leaf-03", "seed", base, cold, cold.Add(2*time.Hour))
	if later <= a {
		t.Fatalf("uptime did not grow within a no-reboot window: %v !> %v", later, a)
	}
}

// TestRebootRarityAndReset checks that reboots are rare per-device but guaranteed to be
// observable across many devices over a multi-year window. With prob=1/120 per day and
// 50 devices × 400 days, the expected total is ~167 resets — observing zero would be a bug.
func TestRebootRarityAndReset(t *testing.T) {
	cold := time.Unix(1_750_000_000, 0)
	base := (50 * 24 * 3600.0)

	totalResets := 0
	maxResetsPerDevice := 0

	// 50 devices × 400 days, sampled per-day (fast).
	for devIdx := 0; devIdx < 50; devIdx++ {
		id := fmt.Sprintf("rtr-%03d", devIdx)
		deviceResets := 0
		prev := deviceUptimeSecs(id, "seed", base, cold, cold)
		for d := 1; d <= 400; d++ {
			now := cold.Add(time.Duration(d) * 24 * time.Hour)
			u := deviceUptimeSecs(id, "seed", base, cold, now)
			if u+30 < prev {
				deviceResets++
			}
			prev = u
		}
		totalResets += deviceResets
		if deviceResets > maxResetsPerDevice {
			maxResetsPerDevice = deviceResets
		}
	}

	if totalResets == 0 {
		t.Fatalf("no reboots observed across 50 devices × 400 days (expected ~167); prob constant may be broken")
	}
	// Per-device: 400 days at 1/120 ≈ 3.3 reboots expected; allow up to 15 (>>3σ).
	if maxResetsPerDevice > 15 {
		t.Fatalf("reboot rate too high for one device: %d reboots in 400 days (expected ~3)", maxResetsPerDevice)
	}
}

func TestOSVersionIndex_MonotonicNonNegative(t *testing.T) {
	cold := time.Unix(1_750_000_000, 0)
	last := 0
	for d := 0; d < 400; d++ {
		now := cold.Add(time.Duration(d) * 24 * time.Hour)
		idx := osVersionIndex("spine-02", "seed", cold, now)
		if idx < 0 || idx < last {
			t.Fatalf("osVersionIndex regressed: %d -> %d", last, idx)
		}
		last = idx
	}
}

// TestEdgeFlapTransition_PairsDownThenUp asserts that flap transitions are balanced
// (each "removed" is eventually paired with an "added") and that at least one flap is
// observed across many edge keys over a 400-day window.
//
// Strategy: scan 201 distinct edge keys × 400 days. For each day that might contain a
// flap, step per-minute within that day only (cheap). With prob=1/200 per day and
// 201 keys × 400 days, the expected flap count is ~402 — observing zero would be a bug.
func TestEdgeFlapTransition_PairsDownThenUp(t *testing.T) {
	cold := time.Unix(1_750_000_000, 0)

	totalDowns, totalUps := 0, 0

	for i := 0; i < 201; i++ {
		edgeKey := fmt.Sprintf("dev-%03d|Eth1|peer|Eth2|lldp", i)
		downs, ups := 0, 0

		// Outer loop: per-day to find flap days quickly.
		for d := 0; d < 400; d++ {
			dayStart := cold.Add(time.Duration(d) * 24 * time.Hour)
			// Check whether this day bucket can possibly contain a flap.
			dayBkt := dayBucket(dayStart)
			flapKey := fmt.Sprintf("reboot|%s|%s|%d", edgeKey, "seed", dayBkt)
			if hashUnit(flapKey) >= edgeFlapPerDayProb {
				continue // no flap on this day — skip per-minute scan
			}
			// Flap fires on this day: step per minute to catch transition bucket edges.
			for m := 0; m < 24*60; m++ {
				now := dayStart.Add(time.Duration(m) * time.Minute)
				if k, ok := edgeFlapTransition(edgeKey, "seed", cold, now); ok {
					switch k {
					case "removed":
						downs++
					case "added":
						ups++
					}
				}
			}
		}

		// Per-key invariant: ups must be within [downs-1, downs].
		if ups < downs-1 || ups > downs {
			t.Errorf("edge %s: down/up unbalanced: downs=%d ups=%d", edgeKey, downs, ups)
		}
		totalDowns += downs
		totalUps += ups
	}

	if totalDowns == 0 {
		t.Fatalf("no flaps observed across 201 edges × 400 days (expected ~402); prob constant may be broken")
	}
	// Global balance check.
	if totalUps < totalDowns-201 || totalUps > totalDowns {
		t.Fatalf("global down/up unbalanced: downs=%d ups=%d", totalDowns, totalUps)
	}
}
