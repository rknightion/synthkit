// SPDX-License-Identifier: AGPL-3.0-only

package k8scluster

import (
	"strconv"
	"strings"
	"time"

	"github.com/rknightion/synthkit/internal/fixture"
)

// podLifecycleKey is the namespace-qualified identity used while tracking active and retired
// pods. Pod names are only namespace-unique in Kubernetes; using the namespace here prevents a
// same-named workload in two namespaces from retiring the wrong metric series.
func podLifecycleKey(namespace, pod string) string {
	return namespace + "\x00" + pod
}

func podLifecycleIdentityKey(namespace, pod, uid string) string {
	return namespace + "\x00" + pod + "\x00" + uid
}

// initPodLifecycleState snapshots the resolved application-pod names before the first churn tick
// mutates its per-tick cluster copy. The snapshot is the generation-zero identity set and keeps
// the default resolved names byte-identical on the first tick, including hand-built test fixtures.
func initPodLifecycleState(cl *fixture.Cluster) (map[string][]string, map[string]int) {
	base := make(map[string][]string)
	spans := make(map[string]int)
	seed := clusterSeed(cl)
	for _, wl := range cl.Workloads {
		if fixture.IsDaemonSet(wl.Controller) {
			continue
		}
		key := podLifecycleKey(wl.Namespace, wl.Name)
		span := wl.Replicas
		if span < len(wl.PodNames) {
			span = len(wl.PodNames)
		}
		if span < 1 {
			span = 1
		}
		spans[key] = span
		if len(wl.PodNames) > 0 {
			base[key] = append([]string(nil), wl.PodNames...)
			continue
		}
		// Resolver-built fixtures normally carry PodNames. This fallback keeps a sparse fixture
		// deterministic and uses the same controller-aware identity minter as the resolver.
		if wl.Replicas > 0 {
			base[key] = fixture.WorkloadPodNames(seed, wl, nil)
		}
	}
	return base, spans
}

// applyPodLifecycleChurn rotates a bounded active identity set over declared application pods.
// Each logical replica has two deterministic identities: its resolved generation-zero name and a
// replacement generated with the same cluster-scoped seed plus a second slot. A minute boundary
// advances a contiguous window by the configured rate, so exactly rate slots change when possible,
// while the active pod count remains the live replica count. DaemonSet pods are excluded because
// their identity is node-scoped and must follow node lifecycle, not an independent pod churn knob.
//
// The returned namespace-qualified set is the set retired at this tick. Callers must drop it from
// the metric State after all current-generation emitters have run and before Collect. Log and OTLP
// projections receive the same per-tick cluster copy, so they naturally omit retired identities.
func (c *Construct) applyPodLifecycleChurn(cl *fixture.Cluster, now time.Time) map[string]bool {
	if c.podChurnPerMinute <= 0 {
		return nil
	}
	if c.podChurnStart.IsZero() {
		c.podChurnStart = now
	}

	total := 0
	for _, wl := range cl.Workloads {
		if fixture.IsDaemonSet(wl.Controller) || wl.Replicas <= 0 {
			continue
		}
		total += wl.Replicas
	}
	if total == 0 {
		retired := c.retiredPodSet(nil)
		c.podChurnActive = map[string]bool{}
		return retired
	}

	rate := c.podChurnPerMinute
	if rate > total {
		// A live scale-down can temporarily make a previously valid configured rate larger than
		// the current pod population. Replacing every live slot is the truthful bounded maximum;
		// the configuration is not allowed to grow the estate or mint an extra pod.
		rate = total
	}
	minute := int(now.Sub(c.podChurnStart) / time.Minute)
	if minute < 0 {
		minute = 0
	}

	active := make(map[string]bool, total)
	globalSlot := 0
	for wi := range cl.Workloads {
		wl := &cl.Workloads[wi]
		if fixture.IsDaemonSet(wl.Controller) || wl.Replicas <= 0 {
			continue
		}
		key := podLifecycleKey(wl.Namespace, wl.Name)
		span := c.podChurnSpans[key]
		if span < wl.Replicas {
			span = wl.Replicas
			c.podChurnSpans[key] = span
		}
		base := c.podChurnBaseNames[key]
		names := make([]string, wl.Replicas)
		uids := make([]string, wl.Replicas)
		for ordinal := 0; ordinal < wl.Replicas; ordinal++ {
			generation := ((minute*rate + globalSlot) / total) % 2
			name := ""
			if generation == 0 && ordinal < len(base) {
				name = base[ordinal]
			}
			if generation > 0 && wl.Controller == "statefulset" {
				// A StatefulSet replacement keeps its controller-owned ordinal name. Kubernetes
				// assigns the replacement object a new UID; the parallel PodUIDs slice carries it.
				if ordinal < len(base) {
					name = base[ordinal]
				} else {
					name = wl.Name + "-" + strconv.Itoa(ordinal)
				}
			}
			if name == "" {
				name = c.churnPodName(*wl, ordinal, generation, span)
			}
			uidName := name
			if generation > 0 {
				uidName += "#lifecycle-" + strconv.Itoa(generation)
			}
			uid := podUID(c.clust.Name, wl.Namespace, uidName)
			names[ordinal] = name
			uids[ordinal] = uid
			active[podLifecycleIdentityKey(wl.Namespace, name, uid)] = true
			globalSlot++
		}
		wl.PodNames = names
		wl.PodUIDs = uids
	}

	retired := c.retiredPodSet(active)
	c.podChurnActive = active
	return retired
}

// churnPodName derives a replacement identity without changing the cluster seed or any
// cluster-scoped identity input. Generation zero falls back to the same controller-aware minter
// used by the resolver; generation one uses a second deterministic PodName slot. The latter is
// intentionally based on fixture.PodName even for a StatefulSet: it represents the replacement
// object's fresh identity while its controller, volume ordinal, and all cluster seeds remain
// unchanged. The normal no-churn path never calls this function.
func (c *Construct) churnPodName(wl fixture.Workload, ordinal, generation, span int) string {
	if generation == 0 {
		names := fixture.WorkloadPodNames(clusterSeed(c.clust), wl, nil)
		if ordinal < len(names) {
			return names[ordinal]
		}
	}
	return fixture.PodName(clusterSeed(c.clust), wl.Name, ordinal+generation*span)
}

func (c *Construct) retiredPodSet(active map[string]bool) map[string]bool {
	retired := make(map[string]bool)
	for key := range c.podChurnActive {
		if !active[key] {
			retired[key] = true
		}
	}
	return retired
}

// retireChurned removes every metric series carrying a retired application pod identity. This
// includes counters and histograms as well as gauges: a retired pod must stop emitting entirely,
// while DeleteGauge semantics are covered by the gauge families in the same state drop.
func (c *Construct) retireChurned(retired map[string]bool) {
	if len(retired) == 0 {
		return
	}
	retiredUIDs := map[string]bool{}
	retiredNames := map[string]bool{}
	for key := range retired {
		parts := strings.SplitN(key, "\x00", 3)
		if len(parts) != 3 {
			continue
		}
		retiredUIDs[parts[2]] = true
		nameKey := podLifecycleKey(parts[0], parts[1])
		stillActive := false
		for activeKey := range c.podChurnActive {
			activeParts := strings.SplitN(activeKey, "\x00", 3)
			if len(activeParts) == 3 && podLifecycleKey(activeParts[0], activeParts[1]) == nameKey {
				stillActive = true
				break
			}
		}
		if !stillActive {
			retiredNames[nameKey] = true
		}
		c.otlpState.dropIdentity(parts[2])
	}
	c.st.DropWhere(func(_ string, labels map[string]string) bool {
		if uid := labels["uid"]; uid != "" && retiredUIDs[uid] {
			return true
		}
		pod := labels["pod"]
		if pod == "" {
			return false
		}
		return retiredNames[podLifecycleKey(labels["namespace"], pod)]
	})
}
