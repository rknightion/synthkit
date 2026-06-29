// SPDX-License-Identifier: AGPL-3.0-only

// churn.go — deterministic pod startup-churn model for the k8scluster construct.
//
// Background: real-cluster capture from a live reference cluster Karpenter stack (24h, 2026-06-16) showed:
//   - OOMKill / ImagePull / CrashLoop error-state counts genuinely ~0/day — leaving those
//     at 0 is correct. Errors stay incident-driven (failuremodes.go + shape.Engine).
//   - Normal pod-lifecycle churn IS real: ContainerCreating=3, PodInitializing=1,
//     Pending peak ~2% of pods at a point-in-time, average ~0 across the day.
//
// Design goal: model a small, slowly-rotating set of pods that are mid-startup so that
// the k8s Overview dashboard shows meaningful (non-zero) churn KPIs without fabricating
// errors. The rotation must be slow enough to be visible at scrape intervals (30s) but
// distinct across 5-min windows so dashboards show variation.
//
// No math/rand is used — global rand would flicker across ticks (shape.Engine has no seedable
// RNG on the synthetic-data path). Instead, a stable FNV hash of (ns, pod, 5-min bucket)
// makes each pod's startup state fully deterministic and reproducible.
package k8scluster

import (
	"hash/fnv"
	"strconv"
	"time"
)

// startupPerMille is the fraction of pods that are mid-startup at any instant, in per-mille
// (parts per 1000). 10 ≈ 1%, grounded in live reference data (24h): Pending peak ~2%, mean ~0 → 1% is
// conservative. Tunable: increase to raise the baseline Pending count.
const startupPerMille = 10

// startupBucketSec is the time-bucket width in seconds. The startup set rotates once per
// bucket, so at 300s (5 min) the set changes slowly — no per-tick flicker — but dashboards
// see a different subset every scrape window. Tunable: decrease to rotate faster.
const startupBucketSec = 300

// startingUp reports whether this pod is in a (synthetic) startup transient right now, and
// which waiting reason applies. Deterministic: a stable hash of (ns, pod, 5-min bucket)
// gives each pod a stable per-bucket slot; approximately startupPerMille/1000 of pods are
// mid-startup at any bucket. Returns ("", false) for the ~99% healthy pods.
//
// reason ∈ {"ContainerCreating", "PodInitializing"} — the two normal-transient KSM reasons
// confirmed real on a live reference Karpenter capture 2026-06-16.
func startingUp(ns, pod string, now time.Time) (reason string, up bool) {
	bucket := now.Unix() / startupBucketSec
	h := fnv32(ns + "/" + pod + "/" + strconv.FormatInt(bucket, 10))
	if h%1000 < startupPerMille {
		// Choose reason: even hash → ContainerCreating, odd → PodInitializing.
		if (h>>10)%2 == 0 {
			return "ContainerCreating", true
		}
		return "PodInitializing", true
	}
	return "", false
}

// fnv32 returns a uint32 FNV-1a hash of s. Used by startingUp; consistent with the
// FNV-1a 64-bit variant used by hex16 in ksm.go and the polynomial hash in helpers.go.
func fnv32(s string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(s))
	return h.Sum32()
}
