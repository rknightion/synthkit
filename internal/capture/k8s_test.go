// SPDX-License-Identifier: AGPL-3.0-only

package capture

import (
	"context"
	"strings"
	"testing"
)

func fakeRunner(responses map[string][]byte) KubectlRunner {
	return func(_ context.Context, args ...string) ([]byte, error) {
		key := strings.Join(args, " ")
		for substr, body := range responses {
			if strings.Contains(key, substr) {
				return body, nil
			}
		}
		return []byte(`{"items":[]}`), nil
	}
}

func TestK8sCollectorNodeGroups(t *testing.T) {
	nodesJSON := []byte(`{"items":[
	  {"metadata":{"labels":{"node.kubernetes.io/instance-type":"m6i.large","kubernetes.io/os":"linux","topology.kubernetes.io/region":"us-west-2","eks.amazonaws.com/nodegroup":"general"}}},
	  {"metadata":{"labels":{"node.kubernetes.io/instance-type":"m6i.large","kubernetes.io/os":"linux","topology.kubernetes.io/region":"us-west-2","eks.amazonaws.com/nodegroup":"general"}}},
	  {"metadata":{"labels":{"node.kubernetes.io/instance-type":"m6i.xlarge","kubernetes.io/os":"linux","karpenter.sh/nodepool":"default"}}}
	]}`)
	c := &K8sCollector{Run: fakeRunner(map[string][]byte{"get nodes": nodesJSON})}
	var inv Inventory
	if err := c.Collect(context.Background(), &inv, CaptureOpts{}); err != nil {
		t.Fatalf("collect: %v", err)
	}
	cl := inv.Clusters[0]
	if cl.Provider != "eks" || cl.Region != "us-west-2" {
		t.Fatalf("provider/region: %q/%q", cl.Provider, cl.Region)
	}
	if len(cl.NodeGroups) != 2 {
		t.Fatalf("want 2 node groups, got %d: %+v", len(cl.NodeGroups), cl.NodeGroups)
	}
	var general *NodeGroup
	for i := range cl.NodeGroups {
		if cl.NodeGroups[i].Name == "general" {
			general = &cl.NodeGroups[i]
		}
	}
	if general == nil || general.Count != 2 || general.Provisioner != "managed" {
		t.Fatalf("general group wrong: %+v", general)
	}
}

func TestK8sCollectorWorkloads(t *testing.T) {
	deployJSON := []byte(`{"items":[
	  {"kind":"Deployment","metadata":{"name":"checkout","namespace":"shop","labels":{"app":"checkout"}},
	   "spec":{"replicas":3,"template":{"spec":{"containers":[
	     {"image":"shop/checkout:1.2","ports":[{"containerPort":8080}],
	      "readinessProbe":{"httpGet":{"path":"/healthz"}}}]}}}}
	]}`)
	c := &K8sCollector{Run: fakeRunner(map[string][]byte{"get deployments": deployJSON})}
	var inv Inventory
	if err := c.Collect(context.Background(), &inv, CaptureOpts{}); err != nil {
		t.Fatalf("collect: %v", err)
	}
	w := findWorkload(t, inv.Clusters[0].Workloads, "checkout")
	if w.Replicas != 3 || w.Namespace != "shop" || len(w.Images) != 1 || w.Images[0] != "shop/checkout:1.2" {
		t.Fatalf("workload fields wrong: %+v", w)
	}
	if len(w.Ports) != 1 || w.Ports[0] != 8080 || len(w.ProbePaths) != 1 || w.ProbePaths[0] != "/healthz" {
		t.Fatalf("workload ports/probes wrong: %+v", w)
	}
}

func TestClusterNameFromContext(t *testing.T) {
	cases := map[string]string{
		"arn:aws:eks:us-west-2:123456789012:cluster/shopprod": "shopprod",
		"gke_my-project_us-central1_prod-cluster":             "gke-my-project-us-central1-prod-cluster",
		"  my-cluster  ": "my-cluster",
		`{"items":[]}`:   "", // junk → caller falls back to default
		"":               "",
	}
	for in, want := range cases {
		if got := clusterNameFromContext(in); got != want {
			t.Errorf("clusterNameFromContext(%q) = %q, want %q", in, got, want)
		}
	}
}

func findWorkload(t *testing.T, ws []Workload, name string) Workload {
	t.Helper()
	for _, w := range ws {
		if w.Name == name {
			return w
		}
	}
	t.Fatalf("workload %q not found in %+v", name, ws)
	return Workload{}
}

func TestK8sCollectorAddonDetection(t *testing.T) {
	// Karpenter and cert-manager deployed with well-known names; istiod is unmodelled.
	deployJSON := []byte(`{"items":[
	  {"kind":"Deployment","metadata":{"name":"karpenter","namespace":"karpenter"},"spec":{"replicas":1,"template":{"spec":{"containers":[{"image":"public.ecr.aws/karpenter/controller:v0.37.0"}]}}}},
	  {"kind":"Deployment","metadata":{"name":"cert-manager","namespace":"cert-manager"},"spec":{"replicas":1,"template":{"spec":{"containers":[{"image":"quay.io/jetstack/cert-manager-controller:v1.14.4"}]}}}},
	  {"kind":"Deployment","metadata":{"name":"istiod","namespace":"istio-system"},"spec":{"replicas":1,"template":{"spec":{"containers":[{"image":"docker.io/istio/pilot:1.20.0"}]}}}}
	]}`)

	c := &K8sCollector{Run: fakeRunner(map[string][]byte{"get deployments": deployJSON})}
	var inv Inventory
	if err := c.Collect(context.Background(), &inv, CaptureOpts{}); err != nil {
		t.Fatalf("collect: %v", err)
	}
	cl := inv.Clusters[0]

	// Find addon entries.
	addonByDetected := map[string]Addon{}
	for _, a := range cl.Addons {
		addonByDetected[a.Detected] = a
	}

	// karpenter should be recognised.
	if a, ok := addonByDetected["karpenter"]; !ok || a.Kind != "karpenter" {
		t.Errorf("expected karpenter addon with kind=karpenter, addons=%+v", cl.Addons)
	}
	// cert-manager should be recognised.
	if a, ok := addonByDetected["cert-manager"]; !ok || a.Kind != "cert_manager" {
		t.Errorf("expected cert-manager addon with kind=cert_manager, addons=%+v", cl.Addons)
	}
	// istiod is not in the table — it should be absent from addons entirely (no well-known match).
	if _, ok := addonByDetected["istiod"]; ok {
		t.Errorf("istiod should not appear as an addon (not in well-known table), addons=%+v", cl.Addons)
	}
}

func TestK8sCollectorEnvelopeCounts(t *testing.T) {
	nodesJSON := []byte(`{"items":[
	  {"metadata":{"labels":{"node.kubernetes.io/instance-type":"t3.medium","kubernetes.io/os":"linux"}}}
	]}`)
	c := &K8sCollector{Run: fakeRunner(map[string][]byte{"get nodes": nodesJSON})}
	var inv Inventory
	if err := c.Collect(context.Background(), &inv, CaptureOpts{}); err != nil {
		t.Fatalf("collect: %v", err)
	}
	if inv.Envelope.Counts["nodes"] != 1 {
		t.Errorf("expected nodes count=1, got %d", inv.Envelope.Counts["nodes"])
	}
	found := false
	for _, k := range inv.Envelope.ResourceKinds {
		if k == "nodes" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected 'nodes' in ResourceKinds, got %v", inv.Envelope.ResourceKinds)
	}
}

func TestK8sCollectorNamespaceFilter(t *testing.T) {
	deployJSON := []byte(`{"items":[
	  {"kind":"Deployment","metadata":{"name":"api","namespace":"prod"},"spec":{"replicas":2,"template":{"spec":{"containers":[{"image":"api:1.0"}]}}}},
	  {"kind":"Deployment","metadata":{"name":"worker","namespace":"dev"},"spec":{"replicas":1,"template":{"spec":{"containers":[{"image":"worker:1.0"}]}}}}
	]}`)
	c := &K8sCollector{Run: fakeRunner(map[string][]byte{"get deployments": deployJSON})}
	var inv Inventory
	opts := CaptureOpts{Namespaces: []string{"prod"}}
	if err := c.Collect(context.Background(), &inv, opts); err != nil {
		t.Fatalf("collect: %v", err)
	}
	ws := inv.Clusters[0].Workloads
	for _, w := range ws {
		if w.Namespace != "prod" {
			t.Errorf("namespace filter failed: got workload in namespace %q", w.Namespace)
		}
	}
}

func TestK8sCollectorMonitoringDetection(t *testing.T) {
	deployJSON := []byte(`{"items":[
	  {"kind":"DaemonSet","metadata":{"name":"alloy","namespace":"k8s-monitoring"},"spec":{"template":{"spec":{"containers":[{"image":"grafana/alloy:v1.2.0"}]}}}}
	]}`)
	c := &K8sCollector{Run: fakeRunner(map[string][]byte{"get deployments": deployJSON, "get daemonsets": deployJSON})}
	var inv Inventory
	if err := c.Collect(context.Background(), &inv, CaptureOpts{}); err != nil {
		t.Fatalf("collect: %v", err)
	}
	mon := inv.Clusters[0].Monitoring
	if !mon.Alloy {
		t.Errorf("expected Alloy=true, got %+v", mon)
	}
	if !mon.K8sMonitoring {
		t.Errorf("expected K8sMonitoring=true (alloy in k8s-monitoring ns), got %+v", mon)
	}
	if mon.AlloyVersion != "v1.2.0" {
		t.Errorf("expected AlloyVersion=v1.2.0, got %q", mon.AlloyVersion)
	}
}
