// SPDX-License-Identifier: AGPL-3.0-only

package k8scluster_test

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
)

// canonicalSeries renders every captured series as one "name{sorted labels} value" line and sorts
// the lines, so the comparison is sensitive to a drawn VALUE changing (RNG draw order) but not to
// series arrival order.
func canonicalSeries(mc *coretest.MetricCapture) []string {
	var out []string
	for _, s := range mc.All() {
		keys := make([]string, 0, len(s.Labels))
		for k := range s.Labels {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var b strings.Builder
		b.WriteString(s.Name)
		b.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			fmt.Fprintf(&b, "%s=%q", k, s.Labels[k])
		}
		fmt.Fprintf(&b, "} %v", s.Value)
		out = append(out, b.String())
	}
	sort.Strings(out)
	return out
}

// TestRNGDrawOrderDeterministic runs the whole construct twice from a fresh state with the same
// seed and clock and requires the drawn values to be identical. cAdvisor container families and the
// kubelet prober families draw from the shared shape RNG once per workload pod, so their draw order
// must not follow Go map iteration over the namespace→deployments map.
func TestRNGDrawOrderDeterministic(t *testing.T) {
	// Many namespaces so a map-order draw sequence differs between runs with near certainty.
	const runs = 8
	var first []string
	for r := 0; r < runs; r++ {
		cl := usageRatioCluster(t)
		cl.K8sMonitoring.ControlPlane.KubeletProbes = true // admit the prober_* families
		for _, n := range []string{"alpha", "bravo", "charlie", "delta", "echo", "foxtrot"} {
			wl := fixture.Workload{Name: n, Namespace: n, Replicas: 2}
			for p := 0; p < wl.Replicas; p++ {
				wl.PodNames = append(wl.PodNames, fixture.PodName("test", n, p))
				wl.NodeIdx = append(wl.NodeIdx, 0)
			}
			cl.Workloads = append(cl.Workloads, wl)
		}
		got := canonicalSeries(captureTick(t, cl))
		if r == 0 {
			first = got
			continue
		}
		if len(got) != len(first) {
			t.Fatalf("run %d: %d series, run 0 had %d", r, len(got), len(first))
		}
		for i := range got {
			if got[i] != first[i] {
				t.Fatalf("run %d differs from run 0 (same seed, fresh state):\n got: %s\nwant: %s", r, got[i], first[i])
			}
		}
	}
}
