// SPDX-License-Identifier: AGPL-3.0-only

package capture

import (
	"context"
	"errors"
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

func TestClusterNameFromEKSARN(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"arn:aws:eks:us-west-2:123456789012:cluster/shopprod", "shopprod", true},
		{"  arn:aws:eks:eu-west-1:123456789012:cluster/blue  ", "blue", true},
		{"gke_my-project_us-central1_prod-cluster", "", false},
		{"operator.example.ts.net", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		got, ok := clusterNameFromEKSARN(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("clusterNameFromEKSARN(%q) = %q,%v want %q,%v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestSlugifyClusterName(t *testing.T) {
	cases := map[string]string{
		"gke_my-project_us-central1_prod-cluster": "gke-my-project-us-central1-prod-cluster",
		"  my-cluster  ":          "my-cluster",
		"operator.example.ts.net": "operator-example-ts-net",
		"":                        "",
	}
	for in, want := range cases {
		if got := slugifyClusterName(in); got != want {
			t.Errorf("slugifyClusterName(%q) = %q, want %q", in, got, want)
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

// =============================================================================
// SKT-0012.01 — cluster identity comes from the cluster, not the operator kubeconfig
// =============================================================================

// TestClusterNameFromCollectorReleaseInfo pins the authoritative source: the cluster name the
// in-cluster collector stamps on every metric, log and trace. The k8s-monitoring chart publishes
// it in a `<fullname>-release-info` ConfigMap next to its collectors, so the capture must read it
// there and report that it did — not slugify a kubeconfig context and present that as the truth.
func TestClusterNameFromCollectorReleaseInfo(t *testing.T) {
	dsJSON := []byte(`{"items":[
	  {"kind":"DaemonSet","metadata":{"name":"synth-mon-alloy-daemon","namespace":"monitoring"},
	   "spec":{"template":{"spec":{"containers":[{"image":"grafana/alloy:v1.2.0"}]}}}}
	]}`)
	nsJSON := []byte(`{"items":[{"metadata":{"name":"monitoring"}}]}`)
	c := &K8sCollector{Run: fakeRunner(map[string][]byte{
		"get daemonsets": dsJSON,
		"get namespaces": nsJSON,
		"get configmap -n monitoring synth-mon-release-info": []byte("blue-fleet-eu\n"),
		// The kubeconfig would answer with something else entirely; it must lose.
		"config current-context": []byte("arn:aws:eks:eu-west-1:123456789012:cluster/arn-name"),
	})}
	var inv Inventory
	if err := c.Collect(context.Background(), &inv, CaptureOpts{}); err != nil {
		t.Fatalf("collect: %v", err)
	}
	cl := inv.Clusters[0]
	if cl.Name != "blue-fleet-eu" {
		t.Errorf("cluster name = %q, want the collector release-info value %q", cl.Name, "blue-fleet-eu")
	}
	if cl.NameSource != NameSourceCollector {
		t.Errorf("name source = %q, want %q", cl.NameSource, NameSourceCollector)
	}
}

// TestClusterNameFallsBackToEKSARN covers the second-choice source and proves it is labelled as
// such rather than silently presented as the collector identity.
func TestClusterNameFallsBackToEKSARN(t *testing.T) {
	c := &K8sCollector{Run: fakeRunner(map[string][]byte{
		"config current-context": []byte("arn:aws:eks:us-west-2:123456789012:cluster/shopprod\n"),
	})}
	var inv Inventory
	if err := c.Collect(context.Background(), &inv, CaptureOpts{}); err != nil {
		t.Fatalf("collect: %v", err)
	}
	cl := inv.Clusters[0]
	if cl.Name != "shopprod" || cl.NameSource != NameSourceEKSARN {
		t.Errorf("name/source = %q/%q, want %q/%q", cl.Name, cl.NameSource, "shopprod", NameSourceEKSARN)
	}
	if AuthoritativeNameSource(cl.NameSource) {
		t.Errorf("%q must not be reported as authoritative", cl.NameSource)
	}
}

// TestClusterNameFallsBackToKubeconfigContextAndSaysSo is the case the validation run hit: a
// tailscale/hostname context that has nothing to do with what the telemetry carries.
func TestClusterNameFallsBackToKubeconfigContextAndSaysSo(t *testing.T) {
	c := &K8sCollector{Run: fakeRunner(map[string][]byte{
		"config current-context": []byte("operator.example.ts.net\n"),
	})}
	var inv Inventory
	if err := c.Collect(context.Background(), &inv, CaptureOpts{}); err != nil {
		t.Fatalf("collect: %v", err)
	}
	cl := inv.Clusters[0]
	if cl.Name != "operator-example-ts-net" || cl.NameSource != NameSourceContext {
		t.Errorf("name/source = %q/%q, want %q/%q", cl.Name, cl.NameSource,
			"operator-example-ts-net", NameSourceContext)
	}
	if AuthoritativeNameSource(cl.NameSource) {
		t.Errorf("%q must not be reported as authoritative", cl.NameSource)
	}
}

// TestClusterNameDefaultIsLabelled proves the last-resort placeholder is not dressed up as a
// discovered identity.
func TestClusterNameDefaultIsLabelled(t *testing.T) {
	c := &K8sCollector{Run: func(_ context.Context, args ...string) ([]byte, error) {
		if strings.Contains(strings.Join(args, " "), "config current-context") {
			return nil, errors.New("no current context")
		}
		return []byte(`{"items":[]}`), nil
	}}
	var inv Inventory
	if err := c.Collect(context.Background(), &inv, CaptureOpts{}); err != nil {
		t.Fatalf("collect: %v", err)
	}
	cl := inv.Clusters[0]
	if cl.Name != "captured-cluster" || cl.NameSource != NameSourceDefault {
		t.Errorf("name/source = %q/%q, want %q/%q", cl.Name, cl.NameSource,
			"captured-cluster", NameSourceDefault)
	}
}

// =============================================================================
// SKT-0012.02 — real node pool identity
// =============================================================================

// TestNodeGroupsUseRealPoolIdentity pins the shape observed on a real mixed cluster: Karpenter
// NodePools of differing size, one of them spanning two instance types, alongside a genuinely
// managed nodegroup whose nodes also carry a `karpenter.sh/controller` scheduling label. Grouping
// by instance type collapses the pools; a `karpenter.sh/` prefix test mislabels the managed group.
func TestNodeGroupsUseRealPoolIdentity(t *testing.T) {
	nodesJSON := []byte(`{"items":[
	  {"metadata":{"labels":{"node.kubernetes.io/instance-type":"m6g.medium","kubernetes.io/os":"linux","topology.kubernetes.io/region":"eu-west-1","karpenter.sh/nodepool":"default"}}},
	  {"metadata":{"labels":{"node.kubernetes.io/instance-type":"m6g.medium","kubernetes.io/os":"linux","karpenter.sh/nodepool":"default"}}},
	  {"metadata":{"labels":{"node.kubernetes.io/instance-type":"m6g.medium","kubernetes.io/os":"linux","karpenter.sh/nodepool":"default"}}},
	  {"metadata":{"labels":{"node.kubernetes.io/instance-type":"c7g.2xlarge","kubernetes.io/os":"linux","karpenter.sh/nodepool":"runners"}}},
	  {"metadata":{"labels":{"node.kubernetes.io/instance-type":"c7g.4xlarge","kubernetes.io/os":"linux","karpenter.sh/nodepool":"runners"}}},
	  {"metadata":{"labels":{"node.kubernetes.io/instance-type":"c7g.4xlarge","kubernetes.io/os":"linux","karpenter.sh/nodepool":"runners"}}},
	  {"metadata":{"labels":{"node.kubernetes.io/instance-type":"c7g.2xlarge","kubernetes.io/os":"linux","karpenter.sh/nodepool":"runners-heavy"}}},
	  {"metadata":{"labels":{"node.kubernetes.io/instance-type":"t4g.large","kubernetes.io/os":"linux","eks.amazonaws.com/nodegroup":"platform-ng","karpenter.sh/controller":"true"}}},
	  {"metadata":{"labels":{"node.kubernetes.io/instance-type":"t4g.large","kubernetes.io/os":"linux","eks.amazonaws.com/nodegroup":"platform-ng","karpenter.sh/controller":"true"}}}
	]}`)
	c := &K8sCollector{Run: fakeRunner(map[string][]byte{"get nodes": nodesJSON})}
	var inv Inventory
	if err := c.Collect(context.Background(), &inv, CaptureOpts{}); err != nil {
		t.Fatalf("collect: %v", err)
	}
	got := map[string]NodeGroup{}
	for _, g := range inv.Clusters[0].NodeGroups {
		got[g.Name] = g
	}
	if len(got) != 4 {
		t.Fatalf("want 4 node groups (3 NodePools + 1 managed nodegroup), got %d: %+v",
			len(got), inv.Clusters[0].NodeGroups)
	}
	for _, want := range []struct {
		name        string
		count       int
		provisioner string
	}{
		{"default", 3, "karpenter"},
		{"runners", 3, "karpenter"},
		{"runners-heavy", 1, "karpenter"},
		{"platform-ng", 2, "managed"},
	} {
		g, ok := got[want.name]
		if !ok {
			t.Errorf("node group %q missing; got %+v", want.name, inv.Clusters[0].NodeGroups)
			continue
		}
		if g.Count != want.count || g.Provisioner != want.provisioner {
			t.Errorf("node group %q = count %d provisioner %q, want count %d provisioner %q",
				want.name, g.Count, g.Provisioner, want.count, want.provisioner)
		}
	}
	// A pool spanning two instance types must record both rather than silently claiming one.
	runners := got["runners"]
	if len(runners.InstanceTypes) != 2 {
		t.Errorf("runners instance_types = %v, want both observed types", runners.InstanceTypes)
	}
	if runners.InstanceType != "c7g.4xlarge" {
		t.Errorf("runners instance_type = %q, want the dominant type %q", runners.InstanceType, "c7g.4xlarge")
	}
}

// TestNodeGroupsWithoutPoolLabels covers a substrate with neither label family: the capture must
// not claim those nodes are EKS-managed.
func TestNodeGroupsWithoutPoolLabels(t *testing.T) {
	nodesJSON := []byte(`{"items":[
	  {"metadata":{"labels":{"kubernetes.io/os":"linux"}}},
	  {"metadata":{"labels":{"kubernetes.io/os":"linux"}}}
	]}`)
	c := &K8sCollector{Run: fakeRunner(map[string][]byte{"get nodes": nodesJSON})}
	var inv Inventory
	if err := c.Collect(context.Background(), &inv, CaptureOpts{}); err != nil {
		t.Fatalf("collect: %v", err)
	}
	gs := inv.Clusters[0].NodeGroups
	if len(gs) != 1 {
		t.Fatalf("want 1 group, got %+v", gs)
	}
	if gs[0].Provisioner != "unknown" {
		t.Errorf("provisioner = %q, want %q", gs[0].Provisioner, "unknown")
	}
	if gs[0].Name == "" {
		t.Errorf("node group name must never be empty: %+v", gs[0])
	}
	if gs[0].Count != 2 {
		t.Errorf("count = %d, want 2", gs[0].Count)
	}
}

// =============================================================================
// SKT-0012.03 — the zero-secret default must cover annotations
// =============================================================================

// TestAnnotationsAreAllowlisted proves a spec-embedding annotation never reaches a capture. This
// is the case the zero-secret claim was never tested against: `kubectl apply` records the whole
// object spec, container env values included, in an annotation on the object itself.
func TestAnnotationsAreAllowlisted(t *testing.T) {
	const leaked = "s3cr3t-database-password"
	deployJSON := []byte(`{"items":[
	  {"kind":"Deployment","metadata":{"name":"api","namespace":"shop","annotations":{
	     "kubectl.kubernetes.io/last-applied-configuration":"{\"spec\":{\"template\":{\"spec\":{\"containers\":[{\"env\":[{\"name\":\"DB_PASSWORD\",\"value\":\"` + leaked + `\"}]}]}}}}",
	     "meta.helm.sh/release-name":"shop-api",
	     "deployment.kubernetes.io/revision":"7",
	     "internal.vendor.example.com/detected-langs":"go,java"
	  }},"spec":{"replicas":1,"template":{"spec":{"containers":[{"image":"shop/api:1.0"}]}}}}
	]}`)
	c := &K8sCollector{Run: fakeRunner(map[string][]byte{"get deployments": deployJSON})}
	var inv Inventory
	if err := c.Collect(context.Background(), &inv, CaptureOpts{}); err != nil {
		t.Fatalf("collect: %v", err)
	}
	w := findWorkload(t, inv.Clusters[0].Workloads, "api")

	if _, ok := w.Annotations["kubectl.kubernetes.io/last-applied-configuration"]; ok {
		t.Errorf("last-applied-configuration reached the capture: %+v", w.Annotations)
	}
	if _, ok := w.Annotations["internal.vendor.example.com/detected-langs"]; ok {
		t.Errorf("unknown annotation key reached the capture (allowlist is not holding): %+v", w.Annotations)
	}
	if w.Annotations["meta.helm.sh/release-name"] != "shop-api" {
		t.Errorf("consumed annotation was dropped: %+v", w.Annotations)
	}
	if w.Annotations["deployment.kubernetes.io/revision"] != "7" {
		t.Errorf("allowlisted annotation was dropped: %+v", w.Annotations)
	}

	// The serialised inventory is what leaves the cluster; assert against that, not just the struct.
	wire, err := Marshal(&inv)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(wire), leaked) {
		t.Errorf("secret value from a spec-embedding annotation is present in the serialised inventory")
	}
}

// TestAllowedAnnotationKeysAreExplicit guards the allowlist against being widened into a prefix
// match: every entry must be a literal key some consumer reads.
func TestAllowedAnnotationKeysAreExplicit(t *testing.T) {
	if _, ok := allowedAnnotationKeys["meta.helm.sh/release-name"]; !ok {
		t.Errorf("the addon detector reads meta.helm.sh/release-name; it must stay allowlisted")
	}
	for k := range allowedAnnotationKeys {
		if strings.HasSuffix(k, "/") || strings.Contains(k, "*") {
			t.Errorf("allowlist entry %q looks like a prefix or glob; entries must be literal keys", k)
		}
	}
}

// TestAlloyImageVersionStripsDigest pins the tag-and-digest case. A reference carrying BOTH a
// tag and a digest (alloy:v1.19.0@sha256:…) splits on the ':' first, so without stripping the
// digest the reported version carries it — which is how a real registry-mirrored, digest-pinned
// deployment reports a version no operator would recognise.
func TestAlloyImageVersionStripsDigest(t *testing.T) {
	for _, tc := range []struct {
		name    string
		image   string
		version string
		ok      bool
	}{
		{"tag only", "grafana/alloy:v1.19.0", "v1.19.0", true},
		{"tag and digest", "grafana/alloy:v1.19.0@sha256:" + strings.Repeat("a", 64), "v1.19.0", true},
		{"digest only", "grafana/alloy@sha256:" + strings.Repeat("b", 64), "", true},
		{"private mirror, tag and digest", "registry.example.test/mirror/alloy:v1.20.1@sha256:" + strings.Repeat("c", 64), "v1.20.1", true},
		{"no tag", "grafana/alloy", "", true},
		{"not alloy", "grafana/alloy-operator:v1.0.0", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			version, ok := alloyImageVersion(tc.image)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.ok)
			}
			if version != tc.version {
				t.Fatalf("version = %q, want %q", version, tc.version)
			}
		})
	}
}
