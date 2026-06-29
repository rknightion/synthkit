// SPDX-License-Identifier: AGPL-3.0-only

package fixture

import (
	"regexp"
	"testing"
)

// Pod-name forms are sourced from a real EKS cluster (kubectl, live reference, 2026-06-16):
//   Deployment:  <name>-<rshash>-<podhash>   e.g. coredns-6db8d9dc49-6twzr
//   StatefulSet: <name>-<ordinal>            e.g. argocd-application-controller-0
//   DaemonSet:   <name>-<5char>              e.g. aws-node-2g5t7 (one per node)

func TestStatefulSetPodName(t *testing.T) {
	for _, ord := range []int{0, 1, 2} {
		got := StatefulSetPodName("alloy-metrics", ord)
		want := "alloy-metrics-" + map[int]string{0: "0", 1: "1", 2: "2"}[ord]
		if got != want {
			t.Errorf("StatefulSetPodName(ord=%d) = %q, want %q", ord, got, want)
		}
	}
}

func TestDaemonSetPodName(t *testing.T) {
	re := regexp.MustCompile(`^aws-node-[0-9a-f]{5}$`)
	n1 := DaemonSetPodName("seed", "aws-node", "ip-10-0-1-1.eu-west-1.compute.internal")
	n2 := DaemonSetPodName("seed", "aws-node", "ip-10-0-1-2.eu-west-1.compute.internal")
	if !re.MatchString(n1) {
		t.Errorf("DaemonSetPodName = %q, want <name>-<5hex>", n1)
	}
	if n1 == n2 {
		t.Errorf("DaemonSetPodName must differ per node: both %q", n1)
	}
	// deterministic for the same (seed, workload, node)
	if again := DaemonSetPodName("seed", "aws-node", "ip-10-0-1-1.eu-west-1.compute.internal"); again != n1 {
		t.Errorf("DaemonSetPodName not deterministic: %q vs %q", n1, again)
	}
}
