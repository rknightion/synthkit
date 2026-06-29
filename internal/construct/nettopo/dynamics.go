// SPDX-License-Identifier: AGPL-3.0-only

package nettopo

import (
	"fmt"
	"math"
	"time"
)

// ── Constants (Frozen Seams — do not rename or change values) ──────────────────

const (
	// wrapCeilingSecs is the maximum uptime a device will ever report. Real SNMP sysUpTime
	// is a 32-bit TimeTicks counter that wraps at ~497 days (42949672.95 seconds). We clamp
	// below this ceiling so the synthetic value is always in [0, wrapCeilingSecs).
	wrapCeilingSecs = 42_949_672.0 // ~497 days, in seconds

	// uptimeBaseMinSecs / uptimeBaseMaxSecs bound the per-device initial uptime offset that
	// is applied before the first reboot fires. This sets the "age" of the device at cold-
	// start of the exporter, not at power-on.
	uptimeBaseMinSecs = float64(3 * 24 * 3600)   // 3 days
	uptimeBaseMaxSecs = float64(180 * 24 * 3600) // 180 days

	// rebootPerDayProb is the probability that a given device reboots on any calendar day.
	// 1/120 per day ≈ roughly once per 120 days on average — rare but observable over a year.
	rebootPerDayProb = 1.0 / 120.0

	// upgradeRebootFraction is the fraction of reboots that also advance the OS version
	// index. ~34% of reboots carry a firmware/OS upgrade.
	upgradeRebootFraction = 0.34

	// rebootDownDur is how long the device is considered "down / rebooting" after a reboot
	// instant fires. 2 minutes covers a typical network OS boot cycle.
	rebootDownDur = 2 * time.Minute

	// edgeFlapPerDayProb is the probability that a given edge flaps on any calendar day.
	// 1/200 per day ≈ roughly once per 200 days — rarer than originally modelled.
	edgeFlapPerDayProb = 1.0 / 200.0

	// edgeFlapDownDur is how long an edge is considered down after a flap fires.
	// 3 minutes covers typical link-layer reconvergence.
	edgeFlapDownDur = 3 * time.Minute
)

// dayBucket returns the calendar-day index for t (UTC days since Unix epoch).
func dayBucket(t time.Time) int64 {
	return t.Unix() / 86400
}

// rebootInstant returns (rebootTime, true) if the entity rebooted on the given day bucket,
// or (zero, false) if it did not. The reboot fires at a deterministic offset within the day.
func rebootInstant(id, seed string, day int64, prob float64) (time.Time, bool) {
	key := fmt.Sprintf("reboot|%s|%s|%d", id, seed, day)
	if hashUnit(key) >= prob {
		return time.Time{}, false
	}
	// Reboot time-of-day: deterministic offset within the 86400-second day.
	todKey := fmt.Sprintf("rbtod|%s|%s|%d", id, seed, day)
	offset := int64(hashUnit(todKey) * 86400)
	dayStartUnix := day * 86400
	return time.Unix(dayStartUnix+offset, 0), true
}

// mostRecentReboot scans day buckets backwards from dayBucket(now) looking for the most
// recent reboot instant tR that satisfies coldStart < tR <= now. It stops after finding the
// first one, or once the day bucket falls at or before dayBucket(coldStart) (no qualifying
// reboot can exist before coldStart), or after scanning back ceil(wrapCeilingSecs/86400)+1
// days (wrap-ceiling bound) — whichever limit is hit first.
// Returns (tR, true) if found, (zero, false) otherwise.
func mostRecentReboot(id, seed string, prob float64, coldStart, now time.Time) (time.Time, bool) {
	maxDays := int64(math.Ceil(wrapCeilingSecs/86400)) + 1
	startDay := dayBucket(now)
	coldDay := dayBucket(coldStart)
	for delta := int64(0); delta <= maxDays; delta++ {
		day := startDay - delta
		// Early exit: no reboot in (coldStart, now] can exist on or before the coldStart day.
		if day < coldDay {
			break
		}
		tR, fired := rebootInstant(id, seed, day, prob)
		if !fired {
			continue
		}
		// tR must be in (coldStart, now] — strictly after coldStart and at or before now.
		if tR.After(now) || !tR.After(coldStart) {
			// Keep scanning backwards; there might be an earlier qualifying reboot.
			continue
		}
		return tR, true
	}
	return time.Time{}, false
}

// deviceUptimeSecs returns the synthetic uptime (in seconds) for the device at time now.
//
// If a reboot instant tR exists in (coldStart, now], uptime = now − tR.
// Otherwise uptime = baseSecs + (now − coldStart).
// Result is always clamped to [0, wrapCeilingSecs).
func deviceUptimeSecs(id, seed string, baseSecs float64, coldStart, now time.Time) float64 {
	tR, found := mostRecentReboot(id, seed, rebootPerDayProb, coldStart, now)
	var uptime float64
	if found {
		uptime = now.Sub(tR).Seconds()
	} else {
		uptime = baseSecs + now.Sub(coldStart).Seconds()
	}
	if uptime < 0 {
		uptime = 0
	}
	return math.Min(uptime, wrapCeilingSecs-1)
}

// deviceRebooting returns true iff the device is currently in the reboot-down window:
// the most recent reboot instant tR satisfies tR <= now < tR + rebootDownDur.
func deviceRebooting(id, seed string, coldStart, now time.Time) bool {
	tR, found := mostRecentReboot(id, seed, rebootPerDayProb, coldStart, now)
	if !found {
		return false
	}
	return !now.Before(tR) && now.Before(tR.Add(rebootDownDur))
}

// osVersionIndex returns how many OS/firmware upgrades have occurred for the device in
// (coldStart, now]. It counts all reboot-firing days in that range whose upgrade-hash
// satisfies hashUnit("upg|...") < upgradeRebootFraction.
// Scanning stops once the day bucket falls below dayBucket(coldStart) (early-exit) or after
// the wrap-ceiling bound — whichever is hit first.
func osVersionIndex(id, seed string, coldStart, now time.Time) int {
	maxDays := int64(math.Ceil(wrapCeilingSecs/86400)) + 1
	startDay := dayBucket(now)
	coldDay := dayBucket(coldStart)
	count := 0
	for delta := int64(0); delta <= maxDays; delta++ {
		day := startDay - delta
		// Early exit: no qualifying reboot can exist on or before the coldStart day.
		if day < coldDay {
			break
		}
		tR, fired := rebootInstant(id, seed, day, rebootPerDayProb)
		if !fired {
			continue
		}
		// Only count reboots in (coldStart, now].
		if tR.After(now) || !tR.After(coldStart) {
			continue
		}
		// Does this reboot carry an OS upgrade?
		upgKey := fmt.Sprintf("upg|%s|%s|%d", id, seed, day)
		if hashUnit(upgKey) < upgradeRebootFraction {
			count++
		}
	}
	return count
}

// edgeDown returns true iff the edge is currently in a flap-down window at time now.
func edgeDown(edgeKey, seed string, coldStart, now time.Time) bool {
	tR, found := mostRecentReboot(edgeKey, seed, edgeFlapPerDayProb, coldStart, now)
	if !found {
		return false
	}
	return !now.Before(tR) && now.Before(tR.Add(edgeFlapDownDur))
}

// edgeFlapTransition detects a change in the edge's down/up state between the previous
// 60-second bucket and the current 60-second bucket. It compares membership-in-down-window
// at bucket(now) vs bucket(now-60s), keyed on the bucket index (now.Unix()/60) so the
// result is robust to unaligned tick intervals.
//
// Returns ("removed", true) when the edge just went down (was up, is now down).
// Returns ("added", true) when the edge just came back up (was down, is now up).
// Returns ("", false) when there is no state change.
func edgeFlapTransition(edgeKey, seed string, coldStart, now time.Time) (string, bool) {
	// Use the 60-second bucket index to define "current" and "previous" instants,
	// so the comparison is stable regardless of the exact tick alignment.
	bucketSecs := int64(60)
	curBucket := now.Unix() / bucketSecs
	prevBucket := curBucket - 1

	// Represent each bucket by its start time (deterministic anchor within the bucket).
	curTime := time.Unix(curBucket*bucketSecs, 0)
	prevTime := time.Unix(prevBucket*bucketSecs, 0)

	curDown := edgeDown(edgeKey, seed, coldStart, curTime)
	prevDown := edgeDown(edgeKey, seed, coldStart, prevTime)

	switch {
	case !prevDown && curDown:
		return "removed", true // edge went down
	case prevDown && !curDown:
		return "added", true // edge came back up
	default:
		return "", false
	}
}
