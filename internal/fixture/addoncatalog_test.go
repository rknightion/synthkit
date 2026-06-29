// SPDX-License-Identifier: AGPL-3.0-only

package fixture

import "testing"

func TestAddonWorkloadsCertManager(t *testing.T) {
	wls := AddonWorkloads("cert_manager")
	if len(wls) != 3 {
		t.Fatalf("cert_manager: want 3 workloads, got %d", len(wls))
	}
	names := map[string]Workload{}
	for _, w := range wls {
		names[w.Name] = w
	}
	w, ok := names["cert-manager-webhook"]
	if !ok || w.Namespace != "cert-manager" || w.Replicas != 1 || w.Container != "cert-manager-webhook" {
		t.Errorf("bad webhook workload: %+v", w)
	}
	cm, ok := names["cert-manager"]
	if !ok || cm.Namespace != "cert-manager" || cm.Replicas != 2 || cm.Container != "cert-manager-controller" {
		t.Errorf("bad cert-manager workload: %+v", cm)
	}
	ca, ok := names["cert-manager-cainjector"]
	if !ok || ca.Namespace != "cert-manager" || ca.Replicas != 1 || ca.Container != "cert-manager-cainjector" {
		t.Errorf("bad cainjector workload: %+v", ca)
	}
}

func TestAddonWorkloadsArgocdStatefulSet(t *testing.T) {
	for _, w := range AddonWorkloads("argocd") {
		if w.Name == "argocd-application-controller" && w.Controller != "statefulset" {
			t.Errorf("app-controller must be statefulset, got %q", w.Controller)
		}
	}
}

func TestAddonWorkloadsArgocdCount(t *testing.T) {
	wls := AddonWorkloads("argocd")
	if len(wls) != 7 {
		t.Fatalf("argocd: want 7 workloads, got %d", len(wls))
	}
}

func TestAddonWorkloadsKarpenterContainer(t *testing.T) {
	wls := AddonWorkloads("karpenter")
	if len(wls) != 1 {
		t.Fatalf("karpenter: want 1 workload, got %d", len(wls))
	}
	if wls[0].Container != "controller" {
		t.Errorf("karpenter container: want controller, got %q", wls[0].Container)
	}
	if wls[0].Replicas != 2 {
		t.Errorf("karpenter replicas: want 2, got %d", wls[0].Replicas)
	}
}

func TestBaselineWorkloads(t *testing.T) {
	wls := BaselineWorkloads()
	names := map[string]Workload{}
	for _, w := range wls {
		names[w.Name] = w
	}
	coredns, ok := names["coredns"]
	if !ok || coredns.Namespace != "kube-system" || coredns.Replicas != 2 {
		t.Errorf("coredns baseline wrong: %+v (found=%v)", coredns, ok)
	}
	ms, ok := names["metrics-server"]
	if !ok || ms.Namespace != "kube-system" || ms.Replicas != 2 {
		t.Errorf("metrics-server baseline wrong: %+v (found=%v)", ms, ok)
	}
}

func TestAddonWorkloadsUnknownKey(t *testing.T) {
	if got := AddonWorkloads("nope"); got != nil {
		t.Errorf("unknown key must return nil, got %v", got)
	}
}

// TestAddonWorkloadsReturnFreshCopies verifies the returned slices are independent copies.
func TestAddonWorkloadsReturnFreshCopies(t *testing.T) {
	a := AddonWorkloads("cert_manager")
	b := AddonWorkloads("cert_manager")
	if len(a) != len(b) {
		t.Fatalf("lengths differ: %d vs %d", len(a), len(b))
	}
	// Mutating one should not affect the other.
	a[0].Replicas = 999
	b2 := AddonWorkloads("cert_manager")
	if b2[0].Replicas == 999 {
		t.Errorf("AddonWorkloads must return fresh copies, not shared pointers")
	}
}
