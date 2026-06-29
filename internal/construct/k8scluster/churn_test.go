// SPDX-License-Identifier: AGPL-3.0-only

// churn_test.go — unit tests for the pod startup-churn model (startingUp).
// Internal package test so we can call unexported startingUp directly.
package k8scluster

import (
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/core/coretest"
)

// allPods collects every (ns, pod) pair from the coretest cluster.
func allTestPods() [][2]string {
	cl := coretest.Cluster()
	var pods [][2]string
	for ns, deploys := range workloadDeployments(cl) {
		for _, deploy := range deploys {
			var reps int
			if ns == "kube-system" {
				reps = addonDeployReplicas(deploy)
			} else {
				reps = 2 // coretest.Cluster has 2-replica workload; substrate gets nodeCount (3)
			}
			for ri := 0; ri < reps; ri++ {
				pod := synthPodName(deploy, ri)
				pods = append(pods, [2]string{ns, pod})
			}
		}
	}
	// Also include the explicit coretest workload pod names.
	wl := cl.Workloads[0]
	for _, p := range wl.PodNames {
		pods = append(pods, [2]string{wl.Namespace, p})
	}
	return pods
}

// collectStarting returns the set of pod keys that startingUp returns true for.
func collectStarting(pods [][2]string, now time.Time) map[string]string {
	out := map[string]string{}
	for _, p := range pods {
		reason, up := startingUp(p[0], p[1], now)
		if up {
			out[p[0]+"/"+p[1]] = reason
		}
	}
	return out
}

// TestStartingUpFractionSmall verifies that the fraction of pods mid-startup is >0 and <5%.
// We use a larger synthetic pod population so the hash distribution is visible.
func TestStartingUpFractionSmall(t *testing.T) {
	now := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)

	// Generate a large synthetic pod population for robust fraction testing.
	var pods [][2]string
	namespaces := []string{"default", "production", "staging", "monitoring", "kube-system"}
	deploys := []string{"frontend", "backend", "api-gateway", "worker", "scheduler",
		"metrics", "controller", "auth", "cache-proxy", "db-proxy"}
	for _, ns := range namespaces {
		for _, d := range deploys {
			for ri := 0; ri < 10; ri++ {
				pods = append(pods, [2]string{ns, synthPodName(d, ri)})
			}
		}
	}
	total := len(pods) // 500 pods

	starting := collectStarting(pods, now)
	n := len(starting)

	// Must be >0 (some pods mid-startup) and <5% (small churn, not a wave).
	if n == 0 {
		t.Errorf("startingUp: 0 pods starting out of %d — expected >0 (startupPerMille=%d/1000)", total, startupPerMille)
	}
	maxAllowed := total * 5 / 100
	if n > maxAllowed {
		t.Errorf("startingUp: %d/%d pods starting (%.1f%%) — expected <5%%", n, total, float64(n)*100/float64(total))
	}
	t.Logf("startingUp: %d/%d pods (%.2f%%) mid-startup at %v", n, total, float64(n)*100/float64(total), now)
}

// TestStartingUpStableInBucket verifies that the SAME set is returned for two times
// within the same 5-min bucket.
func TestStartingUpStableInBucket(t *testing.T) {
	pods := allTestPods()
	if len(pods) == 0 {
		t.Fatal("allTestPods: no pods")
	}

	// Two times inside the same bucket (bucket boundary = every 300s).
	base := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	// Align to bucket start, then pick two times within same bucket.
	bucketStart := base.Unix() / startupBucketSec * startupBucketSec
	t1 := time.Unix(bucketStart+10, 0).UTC()
	t2 := time.Unix(bucketStart+280, 0).UTC() // still in same 300s bucket

	set1 := collectStarting(pods, t1)
	set2 := collectStarting(pods, t2)

	if len(set1) != len(set2) {
		t.Errorf("in-bucket stability: %d starting at t1, %d at t2 — should be identical", len(set1), len(set2))
	}
	for k := range set1 {
		if _, ok := set2[k]; !ok {
			t.Errorf("in-bucket stability: pod %q was starting at t1 but not at t2", k)
		}
	}
	for k := range set2 {
		if _, ok := set1[k]; !ok {
			t.Errorf("in-bucket stability: pod %q was starting at t2 but not at t1", k)
		}
	}
}

// TestStartingUpRotatesAcrossBuckets verifies that different 5-min buckets produce different sets.
func TestStartingUpRotatesAcrossBuckets(t *testing.T) {
	// Use the large population to maximise the chance that at least one pod differs.
	var pods [][2]string
	namespaces := []string{"default", "production", "staging", "monitoring", "kube-system"}
	deploys := []string{"frontend", "backend", "api-gateway", "worker", "scheduler",
		"metrics", "controller", "auth", "cache-proxy", "db-proxy"}
	for _, ns := range namespaces {
		for _, d := range deploys {
			for ri := 0; ri < 10; ri++ {
				pods = append(pods, [2]string{ns, synthPodName(d, ri)})
			}
		}
	}

	// Compare two distinct buckets far apart (1 hour apart = 12 buckets).
	base := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	bucketStart := base.Unix() / startupBucketSec * startupBucketSec
	t1 := time.Unix(bucketStart, 0).UTC()
	t2 := time.Unix(bucketStart+int64(startupBucketSec)*12, 0).UTC() // 12 buckets later

	set1 := collectStarting(pods, t1)
	set2 := collectStarting(pods, t2)

	// Sets must differ (at least one element different).
	differs := false
	for k := range set1 {
		if _, ok := set2[k]; !ok {
			differs = true
			break
		}
	}
	if !differs {
		for k := range set2 {
			if _, ok := set1[k]; !ok {
				differs = true
				break
			}
		}
	}
	if !differs && len(set1) != len(set2) {
		differs = true
	}
	if !differs {
		t.Errorf("startingUp: set did not rotate after %d buckets (t1=%v, t2=%v) — hash may not include bucket", 12, t1, t2)
	}
	t.Logf("sets differ: t1=%d starting, t2=%d starting", len(set1), len(set2))
}

// TestStartingUpReasonEnum verifies that startingUp only returns reasons in the allowed set.
func TestStartingUpReasonEnum(t *testing.T) {
	allowed := map[string]bool{
		"ContainerCreating": true,
		"PodInitializing":   true,
	}

	var pods [][2]string
	namespaces := []string{"default", "production", "staging", "monitoring", "kube-system"}
	deploys := []string{"frontend", "backend", "api-gateway", "worker", "scheduler",
		"metrics", "controller", "auth", "cache-proxy", "db-proxy"}
	for _, ns := range namespaces {
		for _, d := range deploys {
			for ri := 0; ri < 10; ri++ {
				pods = append(pods, [2]string{ns, synthPodName(d, ri)})
			}
		}
	}

	// Check across multiple buckets.
	for bucketOffset := 0; bucketOffset < 50; bucketOffset++ {
		now := time.Unix(int64(1_750_000_000+bucketOffset*startupBucketSec), 0).UTC()
		for _, p := range pods {
			reason, up := startingUp(p[0], p[1], now)
			if up {
				if !allowed[reason] {
					t.Errorf("startingUp(%q, %q): returned reason %q — not in allowed set %v", p[0], p[1], reason, allowed)
				}
			} else if reason != "" {
				t.Errorf("startingUp(%q, %q): up=false but reason=%q (should be empty)", p[0], p[1], reason)
			}
		}
	}
}

// TestStartingUpNotStartingReturnsEmpty verifies that when up=false, reason is "".
func TestStartingUpNotStartingReturnsEmpty(t *testing.T) {
	now := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	// Try many pods; for those where startingUp=false, reason must be "".
	var pods [][2]string
	for ri := 0; ri < 200; ri++ {
		pods = append(pods, [2]string{"default", synthPodName("myapp", ri)})
	}
	for _, p := range pods {
		reason, up := startingUp(p[0], p[1], now)
		if !up && reason != "" {
			t.Errorf("startingUp(up=false): reason=%q, want \"\"", reason)
		}
	}
}
