// SPDX-License-Identifier: AGPL-3.0-only

package k8scluster

import (
	"strings"
	"testing"

	"github.com/rknightion/synthkit/internal/fixture"
)

// storageCluster returns a minimal Cluster carrying one workload with a single VolumeClaim, named
// and seeded as given — enough to drive volumesForCluster's PV/PVC/EBS identity derivation.
func storageCluster(name, seed string) *fixture.Cluster {
	return &fixture.Cluster{
		Name: name,
		Seed: seed,
		Workloads: []fixture.Workload{{
			Name:         "api",
			Namespace:    "api",
			Replicas:     1,
			VolumeClaims: []string{"data"},
		}},
	}
}

// TestVolSeedClusterScoped: PV name, PVC uid, and the backing EBS volume id must be globally unique
// across clusters — real k8s/AWS assign every PVC/PV/EBS volume a distinct id even when two clusters
// in one blueprint declare the same workload + volume_claim. volSeed folds the cluster name into the
// blueprint seed so all three identities (which route through it via volumesForCluster) differ; the
// derivation must still be deterministic for the same (cluster, ns, pvc). Mirrors the pod-identity
// fix (podUID is cluster-folded; storage identities were the same class of bug one level down).
func TestVolSeedClusterScoped(t *testing.T) {
	const seed = "bp-seed" // same blueprint seed for both clusters

	// volSeed itself must differ when the cluster name differs (with a shared blueprint seed).
	sa := volSeed(&fixture.Cluster{Name: "prod-eks-use1", Seed: seed})
	sb := volSeed(&fixture.Cluster{Name: "prod-eks-usw2", Seed: seed})
	if sa == sb {
		t.Errorf("volSeed must differ across clusters sharing a blueprint seed, both = %q", sa)
	}
	// Deterministic for the same cluster.
	if sa != volSeed(&fixture.Cluster{Name: "prod-eks-use1", Seed: seed}) {
		t.Error("volSeed must be deterministic for the same (seed, cluster)")
	}

	// The resulting PV name / PVC uid / EBS volume id (all via volumesForCluster) must differ too.
	clA := storageCluster("prod-eks-use1", seed)
	clB := storageCluster("prod-eks-usw2", seed)
	volsA := volumesForCluster(clA)
	volsB := volumesForCluster(clB)
	if len(volsA) != 1 || len(volsB) != 1 {
		t.Fatalf("expected one volume per cluster, got %d / %d", len(volsA), len(volsB))
	}
	a, b := volsA[0], volsB[0]

	if a.pv == b.pv {
		t.Errorf("PV name must differ across clusters, both = %q", a.pv)
	}
	if a.uid == b.uid {
		t.Errorf("PVC uid must differ across clusters, both = %q", a.uid)
	}
	if a.volumeID == b.volumeID {
		t.Errorf("EBS volume id must differ across clusters, both = %q", a.volumeID)
	}

	// Same (cluster, ns, pvc) ⇒ stable identities across calls.
	a2 := volumesForCluster(storageCluster("prod-eks-use1", seed))[0]
	if a.pv != a2.pv || a.uid != a2.uid || a.volumeID != a2.volumeID {
		t.Errorf("volume identity not deterministic: %+v vs %+v", a, a2)
	}

	// Shape sanity: PV name is "pvc-<uuid>" and the EBS id is "vol-…".
	if !strings.HasPrefix(a.pv, "pvc-") {
		t.Errorf("PV name %q must have pvc- prefix", a.pv)
	}
	if !strings.HasPrefix(a.volumeID, "vol-") {
		t.Errorf("EBS volume id %q must have vol- prefix", a.volumeID)
	}
}
