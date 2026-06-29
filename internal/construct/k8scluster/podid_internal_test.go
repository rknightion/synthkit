// SPDX-License-Identifier: AGPL-3.0-only

package k8scluster

import "testing"

// TestPodUIDClusterScoped: pod (and object) UIDs must be globally unique across clusters — real k8s
// assigns every pod a distinct UUID even when names collide. Ordinal pods (StatefulSet "postgres-0",
// addon "-0") share names across clusters, so without folding the cluster into the UID derivation a
// memory/identity join keyed on uid would conflate two different real pods. (Deployment pod names are
// already cluster-distinct, but the UID derivation must be cluster-aware uniformly.)
func TestPodUIDClusterScoped(t *testing.T) {
	const ns, pod = "db", "postgres-0" // ordinal name — identical across clusters
	a := podUID("prod-eks-use1", ns, pod)
	b := podUID("prod-eks-usw2", ns, pod)
	if a == b {
		t.Errorf("ordinal pod UID must differ across clusters, both = %q", a)
	}
	// Stable within a cluster (deterministic).
	if a != podUID("prod-eks-use1", ns, pod) {
		t.Error("podUID must be deterministic for the same (cluster, ns, pod)")
	}
	// Shape is still UUID-like (8-4-4-4-12 hex).
	if len(a) != 36 || a[8] != '-' || a[13] != '-' || a[18] != '-' || a[23] != '-' {
		t.Errorf("podUID %q not in UUID 8-4-4-4-12 form", a)
	}
}
