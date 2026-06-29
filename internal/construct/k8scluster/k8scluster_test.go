// SPDX-License-Identifier: AGPL-3.0-only

package k8scluster_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/construct/k8scluster"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/scale"
	"github.com/rknightion/synthkit/internal/shape"
	"github.com/rknightion/synthkit/internal/sink/promrw"
)

// ── helpers ───────────────────────────────────────────────────────────────────────

func buildConstruct(t *testing.T, cl *fixture.Cluster) core.Construct {
	t.Helper()
	c, err := k8scluster.New(nil, &fixture.Set{
		Cluster: cl,
		Env:     cl.Env,
		Cloud:   cl.Cloud,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func tick(t *testing.T, c core.Construct, mc *coretest.MetricCapture, lc *coretest.LogCapture) {
	t.Helper()
	ctx := context.Background()
	now := time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC)
	w := coretest.World(mc, lc, nil)
	if err := c.Tick(ctx, now, w); err != nil {
		t.Fatalf("Tick: %v", err)
	}
}

// findSeries returns every series whose Name matches nm.
func findSeries(mc *coretest.MetricCapture, nm string) []promrw.Series {
	return mc.Find(nm)
}

// liveWorld builds a test World wired to mc/lc with a shape.Engine whose Live hook returns the
// given mode→failures map, plus an optional scale.Source (nil ⇒ no overrides).
func liveWorld(mc *coretest.MetricCapture, lc *coretest.LogCapture, live map[string][]shape.LiveFailure, sc *scale.Source) *core.World {
	w := coretest.World(mc, lc, nil)
	if live != nil {
		w.Shape.Live = func(mode string) []shape.LiveFailure { return live[mode] }
	}
	w.Scaling = sc
	return w
}

// tickWorld runs one Tick with an explicit World (for failure/scaling tests).
func tickWorld(t *testing.T, c core.Construct, w *core.World) {
	t.Helper()
	now := time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC)
	if err := c.Tick(context.Background(), now, w); err != nil {
		t.Fatalf("Tick: %v", err)
	}
}

// scalableCluster returns the coretest cluster with the live-scaling fields populated as the
// resolver would (Seed=bare cluster name, Region, a single derive-on node group). coretest.Cluster()
// leaves these zero; the construct's liveCluster needs them to re-derive nodes/pods.
func scalableCluster() *fixture.Cluster {
	cl := coretest.Cluster()
	cl.Seed = cl.Name
	cl.Region = cl.Cloud.Region
	cl.NodeGroups = []fixture.NodeGroupSpec{{Name: "general", InstanceType: "m6i.xlarge"}}
	// Re-derive the baseline node set + pod placement via the same pure function the construct uses,
	// so the fixture is internally consistent with liveCluster at default scaling.
	total := 0
	for _, wl := range cl.Workloads {
		total += wl.Replicas
	}
	cl.Nodes = fixture.DeriveNodes(cl.Seed, cl.Name, cl.NodeGroups, cl.Region, total)
	for i := range cl.Workloads {
		wl := &cl.Workloads[i]
		wl.PodNames = nil
		wl.NodeIdx = nil
		for p := 0; p < wl.Replicas; p++ {
			wl.PodNames = append(wl.PodNames, clusterPodName(cl, wl.Name, p))
			wl.NodeIdx = append(wl.NodeIdx, p%len(cl.Nodes))
		}
	}
	return cl
}

// clusterPodName mirrors the resolver's CLUSTER-SCOPED pod minting (seed:cluster) — the same
// deployment on two clusters gets distinct hashes (real EKS fleets never share pod hashes). The
// construct's live re-mint (liveCluster) MUST reproduce this byte-for-byte under scaling overrides.
func clusterPodName(cl *fixture.Cluster, wl string, replica int) string {
	return fixture.PodName(cl.Seed+":"+cl.Name, wl, replica)
}

// TestLiveReMintClusterScoped asserts that when liveCluster re-derives pod names under a scaling
// override (live count ≠ declared), it uses the CLUSTER-SCOPED seed (seed:cluster), matching the
// resolver — never the bare seed (which would collide across clusters and diverge from the -dump
// baseline). This is the liveCluster half of the cluster-scoped pod-identity parity pair.
func TestLiveReMintClusterScoped(t *testing.T) {
	cl := scalableCluster()
	cl.Workloads[0].Replicas = 4
	cl.Workloads[0].PodNames = nil
	cl.Workloads[0].NodeIdx = nil
	for p := 0; p < 4; p++ {
		cl.Workloads[0].PodNames = append(cl.Workloads[0].PodNames, clusterPodName(cl, cl.Workloads[0].Name, p))
		cl.Workloads[0].NodeIdx = append(cl.Workloads[0].NodeIdx, p)
	}
	cl.Nodes = fixture.LiveNodes(cl, func(_ string, d int) int { return d })

	wl := cl.Workloads[0].Name
	sc := scale.New()
	sc.Set(map[string]int{wl: 8}) // 8 ≠ declared 4 → force the re-mint branch

	c := buildConstruct(t, cl)
	mc := &coretest.MetricCapture{}
	tickWorld(t, c, liveWorld(mc, &coretest.LogCapture{}, nil, sc))

	gotPods := distinctVals(mc, "kube_pod_info", "pod")
	for p := 0; p < 8; p++ {
		want := clusterPodName(cl, wl, p)       // seed:cluster form — must be present
		bare := fixture.PodName(cl.Seed, wl, p) // bare-seed form — must be ABSENT
		if !gotPods[want] {
			t.Errorf("re-mint: cluster-scoped pod %q missing (liveCluster must mint with seed:cluster)", want)
		}
		if bare != want && gotPods[bare] {
			t.Errorf("re-mint: bare-seed pod %q present (liveCluster minted with the bare seed — cross-cluster collision)", bare)
		}
	}
}

// distinctVals returns the distinct values of label key across all series named nm.
func distinctVals(mc *coretest.MetricCapture, nm, key string) map[string]bool {
	out := map[string]bool{}
	for _, s := range mc.Find(nm) {
		if v, ok := s.Labels[key]; ok {
			out[v] = true
		}
	}
	return out
}

// labelVal returns the value of key in the FIRST series matching nm, or "".
func labelVal(mc *coretest.MetricCapture, nm, key string) string {
	for _, s := range findSeries(mc, nm) {
		if v, ok := s.Labels[key]; ok {
			return v
		}
	}
	return ""
}

// hasSeries reports whether at least one series with the given name was captured.
func hasSeries(mc *coretest.MetricCapture, nm string) bool {
	return len(mc.Find(nm)) > 0
}

// ── (a) Job label exactness ───────────────────────────────────────────────────────

func TestJobLabels(t *testing.T) {
	cl := coretest.Cluster()
	c := buildConstruct(t, cl)
	mc := &coretest.MetricCapture{}
	lc := &coretest.LogCapture{}
	tick(t, c, mc, lc)

	tests := []struct {
		name    string
		wantJob string
	}{
		// KSM
		{"kube_node_info", "integrations/kubernetes/kube-state-metrics"},
		{"kube_pod_info", "integrations/kubernetes/kube-state-metrics"},
		{"kube_pod_status_phase", "integrations/kubernetes/kube-state-metrics"},
		// cAdvisor
		{"container_cpu_usage_seconds_total", "integrations/kubernetes/cadvisor"},
		{"container_memory_working_set_bytes", "integrations/kubernetes/cadvisor"},
		{"container_network_receive_bytes_total", "integrations/kubernetes/cadvisor"},
		// kubelet
		{"kubelet_running_pods", "integrations/kubernetes/kubelet"},
		{"kubelet_running_containers", "integrations/kubernetes/kubelet"},
		// node-exporter — ⚠ NO "kubernetes/" segment
		{"node_load1", "integrations/node_exporter"},
		{"node_memory_MemAvailable_bytes", "integrations/node_exporter"},
		{"node_cpu_seconds_total", "integrations/node_exporter"},
	}
	for _, tt := range tests {
		series := findSeries(mc, tt.name)
		if len(series) == 0 {
			t.Errorf("job label test: no series named %q", tt.name)
			continue
		}
		for _, s := range series {
			got := s.Labels["job"]
			if got != tt.wantJob {
				t.Errorf("series %q: got job=%q, want %q", tt.name, got, tt.wantJob)
				break
			}
		}
	}

	// node_exporter MUST NOT contain "kubernetes/"
	for _, s := range mc.Find("node_load1") {
		if strings.Contains(s.Labels["job"], "kubernetes/") {
			t.Errorf("node_load1: job %q must not contain 'kubernetes/'", s.Labels["job"])
		}
	}
}

// ── (b) Dual cluster + k8s_cluster_name, NO blueprint label ────────────────────────

func TestClusterLabels(t *testing.T) {
	cl := coretest.Cluster()
	wantCluster := cl.Name // "test-prod-use1"

	c := buildConstruct(t, cl)
	mc := &coretest.MetricCapture{}
	lc := &coretest.LogCapture{}
	tick(t, c, mc, lc)

	for _, s := range mc.All() {
		// Every series must carry cluster=wantCluster
		if got := s.Labels["cluster"]; got != wantCluster {
			t.Errorf("series %q: cluster=%q, want %q", s.Name, got, wantCluster)
		}
		// Every series must carry k8s_cluster_name=wantCluster
		if got := s.Labels["k8s_cluster_name"]; got != wantCluster {
			t.Errorf("series %q: k8s_cluster_name=%q, want %q", s.Name, got, wantCluster)
		}
		// No blueprint label (ScopeSubstrate — I21)
		if _, ok := s.Labels["blueprint"]; ok {
			t.Errorf("series %q: must not carry 'blueprint' label (ScopeSubstrate)", s.Name)
		}
	}
}

// ── (b2) kube_node_labels carries the per-node arch (sourced from the catalogue) ──

func TestNodeArchLabel(t *testing.T) {
	cl := coretest.Cluster()
	cl.Seed = cl.Name
	cl.Region = cl.Cloud.Region
	// mixed-arch cluster: x86_64 (→amd64) + Graviton (→arm64). Both pinned (desired>0).
	cl.NodeGroups = []fixture.NodeGroupSpec{
		{Name: "general", InstanceType: "m6i.xlarge", Desired: 2},
		{Name: "arm", InstanceType: "m8g.xlarge", Desired: 2},
	}
	total := 0
	for _, wl := range cl.Workloads {
		total += wl.Replicas
	}
	cl.Nodes = fixture.DeriveNodes(cl.Seed, cl.Name, cl.NodeGroups, cl.Region, total)
	for i := range cl.Workloads {
		wl := &cl.Workloads[i]
		wl.PodNames, wl.NodeIdx = nil, nil
		for p := 0; p < wl.Replicas; p++ {
			wl.PodNames = append(wl.PodNames, fixture.PodName(cl.Seed, wl.Name, p))
			wl.NodeIdx = append(wl.NodeIdx, p%len(cl.Nodes))
		}
	}

	c := buildConstruct(t, cl)
	mc := &coretest.MetricCapture{}
	lc := &coretest.LogCapture{}
	tick(t, c, mc, lc)

	arch := distinctVals(mc, "kube_node_labels", "label_kubernetes_io_arch")
	if !arch["amd64"] || !arch["arm64"] {
		t.Errorf("kube_node_labels.label_kubernetes_io_arch = %v, want both amd64 and arm64", arch)
	}
	// arch must never be empty/absent — real nodes always carry it.
	if arch[""] {
		t.Error("kube_node_labels carried an empty label_kubernetes_io_arch")
	}
}

// ── (c) provider_id embeds fixture InstanceIDs ───────────────────────────────────

func TestProviderIDCorrelation(t *testing.T) {
	cl := coretest.Cluster()
	c := buildConstruct(t, cl)
	mc := &coretest.MetricCapture{}
	lc := &coretest.LogCapture{}
	tick(t, c, mc, lc)

	// Collect all instance IDs from nodes
	instanceIDs := map[string]bool{}
	for _, n := range cl.Nodes {
		instanceIDs[n.InstanceID] = true
	}

	// Every kube_node_info.provider_id must contain one of the node InstanceIDs
	nodeInfoSeries := findSeries(mc, "kube_node_info")
	if len(nodeInfoSeries) == 0 {
		t.Fatal("no kube_node_info series found")
	}

	for _, s := range nodeInfoSeries {
		pid := s.Labels["provider_id"]
		if pid == "" {
			t.Error("kube_node_info: provider_id label is missing")
			continue
		}
		found := false
		for iid := range instanceIDs {
			if strings.Contains(pid, iid) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("kube_node_info: provider_id=%q does not contain any fixture InstanceID %v", pid, instanceIDs)
		}
	}
}

// ── (c2) kube_node_info has a UNIQUE (cluster,node) key — the app's backbone invariant ──

// TestNodeInfoUniqueKey guards the end-to-end contract the Grafana k8s-monitoring app depends on:
// no two kube_node_info series may share the same (cluster, node) key. Real k8s never has two
// nodes with the same name; if two synthetic nodes collide on PrivateIP they collide on Hostname,
// emit two kube_node_info series with the same (cluster,node), and the app's cluster-table backbone
// query — label_replace(max by(...,provider_id,...)(kube_node_info), provider_id→"aws") — rewrites
// both provider_ids to "aws", collapsing them to one labelset → Prometheus 422 → blank cluster row.
// We use a dense multi-AZ cluster (the live-confirmed shape) to stress the node-IP derivation.
func TestNodeInfoUniqueKey(t *testing.T) {
	cl := coretest.Cluster()
	cl.Seed = cl.Name
	cl.Region = cl.Cloud.Region
	// Two AZ node groups, densely populated — enough nodes to birthday-collide in the legacy
	// ~160-address space (the live-confirmed failure mode had two AZ groups).
	cl.NodeGroups = []fixture.NodeGroupSpec{
		{Name: "ng-use1a", InstanceType: "m6i.xlarge", Desired: 60},
		{Name: "ng-use1c", InstanceType: "m6i.xlarge", Desired: 60},
	}
	cl.Nodes = fixture.DeriveNodes(cl.Seed, cl.Name, cl.NodeGroups, cl.Region, 0)
	for i := range cl.Workloads {
		wl := &cl.Workloads[i]
		wl.PodNames, wl.NodeIdx = nil, nil
		for p := 0; p < wl.Replicas; p++ {
			wl.PodNames = append(wl.PodNames, fixture.PodName(cl.Seed, wl.Name, p))
			wl.NodeIdx = append(wl.NodeIdx, p%len(cl.Nodes))
		}
	}

	c := buildConstruct(t, cl)
	mc := &coretest.MetricCapture{}
	lc := &coretest.LogCapture{}
	tick(t, c, mc, lc)

	series := findSeries(mc, "kube_node_info")
	if len(series) == 0 {
		t.Fatal("no kube_node_info series found")
	}
	seenKey := map[string]int{} // cluster|node → first index
	seenUUID := map[string]string{}
	for i, s := range series {
		key := s.Labels["cluster"] + "|" + s.Labels["node"]
		if j, dup := seenKey[key]; dup {
			t.Errorf("duplicate kube_node_info (cluster,node) key %q: series %d and %d", key, j, i)
		}
		seenKey[key] = i
		// Once hostnames are unique, system_uuid (derived from the hostname hash) is unique too.
		if uuid := s.Labels["system_uuid"]; uuid != "" {
			if prevNode, dup := seenUUID[uuid]; dup && prevNode != s.Labels["node"] {
				t.Errorf("system_uuid %q shared by distinct nodes %q and %q", uuid, prevNode, s.Labels["node"])
			}
			seenUUID[uuid] = s.Labels["node"]
		}
	}
	if len(series) != 120 {
		t.Errorf("expected 120 kube_node_info series (60+60 nodes), got %d", len(series))
	}
}

// ── (d) pod names in kube_pod_info / cAdvisor == fixture PodNames ─────────────────

func TestPodNamesMatchFixture(t *testing.T) {
	cl := coretest.Cluster()
	c := buildConstruct(t, cl)
	mc := &coretest.MetricCapture{}
	lc := &coretest.LogCapture{}
	tick(t, c, mc, lc)

	// Collect fixture PodNames
	fixturePods := map[string]bool{}
	for _, wl := range cl.Workloads {
		for _, pn := range wl.PodNames {
			fixturePods[pn] = true
		}
	}

	if len(fixturePods) == 0 {
		t.Skip("no workloads with pod names in fixture")
	}

	// kube_pod_info: every fixture pod must appear
	podInfoPods := map[string]bool{}
	for _, s := range findSeries(mc, "kube_pod_info") {
		podInfoPods[s.Labels["pod"]] = true
	}
	for pod := range fixturePods {
		if !podInfoPods[pod] {
			t.Errorf("kube_pod_info: fixture pod %q not found", pod)
		}
	}

	// cAdvisor: fixture pods must appear in container_cpu_usage_seconds_total
	cadvisorPods := map[string]bool{}
	for _, s := range findSeries(mc, "container_cpu_usage_seconds_total") {
		cadvisorPods[s.Labels["pod"]] = true
	}
	for pod := range fixturePods {
		if !cadvisorPods[pod] {
			t.Errorf("container_cpu_usage_seconds_total: fixture pod %q not found", pod)
		}
	}
}

// ── (e) Conformance: alloy_build_info version, all build_info families ────────────

func TestConformanceWithFlagsOn(t *testing.T) {
	cl := coretest.Cluster()
	// coretest.Cluster() already has all flags enabled — verify
	km := cl.K8sMonitoring
	if !km.Enabled || !km.Alloy || !km.OpenCost || !km.Kepler {
		t.Skip("coretest.Cluster() does not have all K8sMonitoring flags on")
	}

	c := buildConstruct(t, cl)
	mc := &coretest.MetricCapture{}
	lc := &coretest.LogCapture{}
	tick(t, c, mc, lc)

	// alloy_build_info version must be VERBATIM "v1.16.3" (coretest fixture value)
	alloyInfoSeries := findSeries(mc, "alloy_build_info")
	if len(alloyInfoSeries) == 0 {
		t.Fatal("alloy_build_info not emitted when Alloy=true")
	}
	for _, s := range alloyInfoSeries {
		v := s.Labels["version"]
		if v != "v1.16.3" {
			t.Errorf("alloy_build_info: version=%q, want %q", v, "v1.16.3")
		}
		if !strings.HasPrefix(v, "v") {
			t.Errorf("alloy_build_info: version=%q must start with 'v' (conformance check)", v)
		}
	}

	// grafana_kubernetes_monitoring_build_info
	if !hasSeries(mc, "grafana_kubernetes_monitoring_build_info") {
		t.Error("grafana_kubernetes_monitoring_build_info not emitted when Enabled=true")
	}

	// opencost_build_info
	if !hasSeries(mc, "opencost_build_info") {
		t.Error("opencost_build_info not emitted when OpenCost=true")
	}

	// kepler_exporter_build_info
	if !hasSeries(mc, "kepler_exporter_build_info") {
		t.Error("kepler_exporter_build_info not emitted when Kepler=true")
	}
}

func TestConformanceAbsentWhenFlagsOff(t *testing.T) {
	cl := coretest.Cluster()
	cl.K8sMonitoring = fixture.K8sMonitoring{Enabled: false}

	c := buildConstruct(t, cl)
	mc := &coretest.MetricCapture{}
	lc := &coretest.LogCapture{}
	tick(t, c, mc, lc)

	for _, nm := range []string{
		"grafana_kubernetes_monitoring_build_info",
		"alloy_build_info",
		"opencost_build_info",
		"kepler_exporter_build_info",
	} {
		if hasSeries(mc, nm) {
			t.Errorf("conformance series %q must be absent when K8sMonitoring.Enabled=false", nm)
		}
	}
}

// ── (f) Cumulative counters monotone across ticks ─────────────────────────────────

func TestCountersMonotone(t *testing.T) {
	cl := coretest.Cluster()
	c := buildConstruct(t, cl)
	lc := &coretest.LogCapture{}
	ctx := context.Background()

	now := time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC)

	type seriesKey struct{ name, sig string }
	type sample struct{ t1, t2 float64 }
	prev := map[string]float64{} // sig → value after tick 1

	// Counters to check (from extract)
	counterNames := map[string]bool{
		"container_cpu_usage_seconds_total":        true,
		"container_network_receive_bytes_total":    true,
		"container_network_transmit_bytes_total":   true,
		"kube_pod_container_status_restarts_total": true,
		"kubelet_runtime_operations_total":         true,
		"node_cpu_seconds_total":                   true,
		"node_network_receive_bytes_total":         true,
	}

	// Tick 1
	mc1 := &coretest.MetricCapture{}
	w1 := coretest.World(mc1, lc, nil)
	if err := c.Tick(ctx, now, w1); err != nil {
		t.Fatalf("tick 1: %v", err)
	}
	for _, s := range mc1.All() {
		if counterNames[s.Name] {
			sig := s.Name + "|" + labelSig(s.Labels)
			prev[sig] = s.Value
		}
	}

	// Tick 2 (60s later)
	mc2 := &coretest.MetricCapture{}
	w2 := coretest.World(mc2, lc, nil)
	if err := c.Tick(ctx, now.Add(60*time.Second), w2); err != nil {
		t.Fatalf("tick 2: %v", err)
	}
	for _, s := range mc2.All() {
		if counterNames[s.Name] {
			sig := s.Name + "|" + labelSig(s.Labels)
			if v1, ok := prev[sig]; ok {
				if s.Value < v1 {
					t.Errorf("counter %q decreased: tick1=%.6f tick2=%.6f", s.Name, v1, s.Value)
				}
			}
		}
	}
}

// labelSig produces a stable string key from a label map (test helper).
func labelSig(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// simple sort
	for i := 0; i < len(keys); i++ {
		for j := i + 1; j < len(keys); j++ {
			if keys[i] > keys[j] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(m[k])
		b.WriteByte(';')
	}
	return b.String()
}

// ── (f2) Gauge series do NOT accumulate across ticks ─────────────────────────────

// TestGaugesDoNotAccumulateAcrossTicks pins that known GAUGE series (state.Set)
// retain their instantaneous value and do NOT double per tick. Regression for M5:
// if these were changed to state.Add the value would grow monotonically without
// bound rather than tracking the instantaneous cluster state.
func TestGaugesDoNotAccumulateAcrossTicks(t *testing.T) {
	cl := coretest.Cluster()
	c := buildConstruct(t, cl)
	lc := &coretest.LogCapture{}
	ctx := context.Background()

	now := time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC)

	gaugeNames := []string{
		"kube_node_status_condition",      // KSM node condition gauge
		"kube_deployment_status_replicas", // KSM deployment replicas gauge
		"kubelet_running_pods",            // kubelet pods gauge
	}

	// Tick 1
	mc1 := &coretest.MetricCapture{}
	w1 := coretest.World(mc1, lc, nil)
	if err := c.Tick(ctx, now, w1); err != nil {
		t.Fatalf("tick 1: %v", err)
	}
	type prevEntry struct{ name, sig string }
	prev := map[string]float64{}
	for _, s := range mc1.All() {
		for _, gn := range gaugeNames {
			if s.Name == gn {
				prev[s.Name+"|"+labelSig(s.Labels)] = s.Value
			}
		}
	}

	// Tick 2 (60s later — value must NOT double)
	mc2 := &coretest.MetricCapture{}
	w2 := coretest.World(mc2, lc, nil)
	if err := c.Tick(ctx, now.Add(60*time.Second), w2); err != nil {
		t.Fatalf("tick 2: %v", err)
	}
	checked := 0
	for _, s := range mc2.All() {
		for _, gn := range gaugeNames {
			if s.Name == gn {
				key := s.Name + "|" + labelSig(s.Labels)
				v1, ok := prev[key]
				if !ok {
					continue
				}
				// A gauge value must not be ≥2× tick1 value (which would indicate Add).
				// Allow 1.5× for noise headroom (realistic load-factor variance).
				//
				// Zero-valued series (v1==0, e.g. an inactive kube_node_status_condition)
				// are deliberately skipped: Set(0) and Add(0) are byte-for-byte
				// indistinguishable across ticks, so they carry no signal for this test.
				// The non-zero members of the same families (a node always has exactly one
				// condition at 1) exercise the Set-vs-Add discrimination.
				if v1 > 0 {
					checked++
					if s.Value >= v1*1.5 {
						t.Errorf("gauge %q appears to accumulate: tick1=%.4f tick2=%.4f (ratio=%.2fx, expect ≈1x for Set)",
							s.Name, v1, s.Value, s.Value/v1)
					}
				}
			}
		}
	}
	// Guard against a vacuous pass: if a future fixture/rename change drops every gauge
	// in gaugeNames the loop above would assert nothing and the test would still go green.
	if checked == 0 {
		t.Fatalf("no non-zero gauge series matched %v — test asserted nothing (fixture or naming drift?)", gaugeNames)
	}
}

// ── (g) kubelet inventory == 22-name allow-list ────────────────────────────────────

func TestKubeletAllowList(t *testing.T) {
	cl := coretest.Cluster()
	c := buildConstruct(t, cl)
	mc := &coretest.MetricCapture{}
	lc := &coretest.LogCapture{}
	tick(t, c, mc, lc)

	// kubelet allow-list (22 names, extract §2.3)
	// Histograms expand to _bucket and _count; include both in the check.
	want := map[string]bool{
		"kubelet_certificate_manager_server_ttl_seconds": true,
		"kubelet_cgroup_manager_duration_seconds_bucket": true,
		"kubelet_cgroup_manager_duration_seconds_count":  true,
		"kubelet_node_name":                           true,
		"kubelet_pleg_relist_duration_seconds_bucket": true,
		"kubelet_pleg_relist_duration_seconds_count":  true,
		"kubelet_pleg_relist_interval_seconds_bucket": true,
		"kubelet_pod_start_duration_seconds_bucket":   true,
		"kubelet_pod_start_duration_seconds_count":    true,
		"kubelet_pod_worker_duration_seconds_bucket":  true,
		"kubelet_pod_worker_duration_seconds_count":   true,
		"kubelet_running_containers":                  true,
		"kubelet_running_pods":                        true,
		"kubelet_runtime_operations_errors_total":     true,
		"kubelet_runtime_operations_total":            true,
		"kubelet_server_expiration_renew_errors":      true,
		"kubelet_volume_stats_available_bytes":        true,
		"kubelet_volume_stats_capacity_bytes":         true,
		"kubelet_volume_stats_inodes":                 true,
		"kubelet_volume_stats_inodes_free":            true,
		"kubelet_volume_stats_inodes_used":            true,
		"kubelet_volume_stats_used_bytes":             true,
	}

	// Collect all names emitted with job=integrations/kubernetes/kubelet
	emittedKubelet := map[string]bool{}
	for _, s := range mc.All() {
		if s.Labels["job"] == "integrations/kubernetes/kubelet" {
			emittedKubelet[s.Name] = true
		}
	}

	// Every name in the allow-list must be present
	for nm := range want {
		if !emittedKubelet[nm] {
			t.Errorf("kubelet allow-list: %q not emitted", nm)
		}
	}
}

// ── (h) Events streams labeled per contract ────────────────────────────────────────

func TestEventStreamLabels(t *testing.T) {
	cl := coretest.Cluster()
	c := buildConstruct(t, cl)
	mc := &coretest.MetricCapture{}
	lc := &coretest.LogCapture{}
	tick(t, c, mc, lc)

	clusterName := cl.Name

	if len(lc.Streams) == 0 {
		t.Fatal("no Loki streams emitted")
	}

	hadEventhandler := false
	hadManifests := false

	for _, st := range lc.Streams {
		lbls := st.Labels
		// Every stream must carry cluster and k8s_cluster_name
		if got := lbls["cluster"]; got != clusterName {
			t.Errorf("stream cluster=%q, want %q", got, clusterName)
		}
		if got := lbls["k8s_cluster_name"]; got != clusterName {
			t.Errorf("stream k8s_cluster_name=%q, want %q", got, clusterName)
		}

		switch lbls["job"] {
		case "integrations/kubernetes/eventhandler":
			hadEventhandler = true
			if lbls["service_name"] != "integrations/kubernetes/eventhandler" {
				t.Errorf("eventhandler: service_name=%q", lbls["service_name"])
			}
			if lbls["source"] != "kubernetes-events" {
				t.Errorf("eventhandler: source=%q, want kubernetes-events", lbls["source"])
			}
			// level must be Info or Warning (relaxed from Info-only to accept new vocab)
			level := lbls["level"]
			if level != "Info" && level != "Warning" {
				t.Errorf("eventhandler: level=%q, want Info or Warning", level)
			}
			// reason must be from the extended eventSpecs vocabulary
			reason := lbls["reason"]
			validReasons := map[string]bool{
				"Scheduled": true, "Pulling": true, "Pulled": true, "Created": true,
				"Started": true, "Killing": true, "BackOff": true,
				"FailedScheduling": true, "ScalingReplicaSet": true,
			}
			if !validReasons[reason] {
				t.Errorf("eventhandler: unexpected reason=%q", reason)
			}

		case "integrations/kubernetes/manifests":
			hadManifests = true
			if lbls["service_name"] != "integrations/kubernetes/manifests" {
				t.Errorf("manifests: service_name=%q", lbls["service_name"])
			}
		}
	}

	if !hadEventhandler {
		t.Error("no eventhandler stream found")
	}
	if !hadManifests {
		t.Error("no manifests stream found")
	}
}

// ── Additional invariants ─────────────────────────────────────────────────────────

// TestKindAndSignals verifies the construct metadata.
func TestKindAndSignals(t *testing.T) {
	cl := coretest.Cluster()
	c := buildConstruct(t, cl)
	if c.Kind() != "k8s_cluster" {
		t.Errorf("Kind()=%q, want k8s_cluster", c.Kind())
	}
	sigs := c.Signals()
	hasMetrics, hasLogs := false, false
	for _, s := range sigs {
		switch s {
		case core.Metrics:
			hasMetrics = true
		case core.Logs:
			hasLogs = true
		}
	}
	if !hasMetrics {
		t.Error("Signals() does not include Metrics")
	}
	if !hasLogs {
		t.Error("Signals() does not include Logs")
	}
	if c.Interval() != 60*time.Second {
		t.Errorf("Interval()=%v, want 60s", c.Interval())
	}
}

// TestBuildNilCluster verifies Build returns an error when Cluster is nil.
func TestBuildNilCluster(t *testing.T) {
	_, err := k8scluster.New(nil, &fixture.Set{})
	if err == nil {
		t.Error("expected error when fx.Cluster is nil, got nil")
	}
}

// TestAllSeriesCarrySource verifies source="kubernetes" on all metric series.
func TestAllSeriesCarrySource(t *testing.T) {
	cl := coretest.Cluster()
	c := buildConstruct(t, cl)
	mc := &coretest.MetricCapture{}
	lc := &coretest.LogCapture{}
	tick(t, c, mc, lc)

	for _, s := range mc.All() {
		if s.Labels["source"] != "kubernetes" {
			t.Errorf("series %q: source=%q, want kubernetes", s.Name, s.Labels["source"])
		}
	}
}

// TestKSMPodStatusPhaseAllFour verifies that all four phase values are emitted per pod.
func TestKSMPodStatusPhaseAllFour(t *testing.T) {
	cl := coretest.Cluster()
	c := buildConstruct(t, cl)
	mc := &coretest.MetricCapture{}
	lc := &coretest.LogCapture{}
	tick(t, c, mc, lc)

	// Gather {pod → set of phases}
	podPhases := map[string]map[string]bool{}
	for _, s := range mc.Find("kube_pod_status_phase") {
		pod := s.Labels["pod"]
		phase := s.Labels["phase"]
		if pod == "" || phase == "" {
			continue
		}
		if podPhases[pod] == nil {
			podPhases[pod] = map[string]bool{}
		}
		podPhases[pod][phase] = true
	}

	if len(podPhases) == 0 {
		t.Fatal("no kube_pod_status_phase series found")
	}

	required := []string{"Running", "Pending", "Failed", "Succeeded"}
	for pod, phases := range podPhases {
		for _, phase := range required {
			if !phases[phase] {
				t.Errorf("pod %q: missing kube_pod_status_phase{phase=%q}", pod, phase)
			}
		}
	}
}

// TestCAdvisorNetworkNOContainerLabel verifies container_network_* has no container label.
func TestCAdvisorNetworkNOContainerLabel(t *testing.T) {
	cl := coretest.Cluster()
	c := buildConstruct(t, cl)
	mc := &coretest.MetricCapture{}
	lc := &coretest.LogCapture{}
	tick(t, c, mc, lc)

	for _, nm := range []string{
		"container_network_receive_bytes_total",
		"container_network_transmit_bytes_total",
		"container_network_receive_packets_total",
		"container_network_transmit_packets_total",
	} {
		for _, s := range mc.Find(nm) {
			if _, ok := s.Labels["container"]; ok {
				t.Errorf("series %q must NOT have 'container' label (pod-scoped per extract §2.2)", nm)
				break
			}
			if s.Labels["interface"] != "eth0" {
				t.Errorf("series %q: interface=%q, want eth0", nm, s.Labels["interface"])
			}
		}
	}
}

// TestNodeExporterDaemonSetPodLabels checks that node-exporter series carry the DaemonSet labels.
func TestNodeExporterDaemonSetPodLabels(t *testing.T) {
	cl := coretest.Cluster()
	c := buildConstruct(t, cl)
	mc := &coretest.MetricCapture{}
	lc := &coretest.LogCapture{}
	tick(t, c, mc, lc)

	for _, s := range mc.Find("node_load1") {
		if s.Labels["app"] != "node-exporter" {
			t.Errorf("node_load1: app=%q, want node-exporter", s.Labels["app"])
		}
		if s.Labels["component"] != "metrics" {
			t.Errorf("node_load1: component=%q, want metrics", s.Labels["component"])
		}
		if s.Labels["container"] != "node-exporter" {
			t.Errorf("node_load1: container=%q, want node-exporter", s.Labels["container"])
		}
		if !strings.HasPrefix(s.Labels["pod"], "grafana-k8s-monitoring-node-exporter-") {
			t.Errorf("node_load1: pod=%q, should start with grafana-k8s-monitoring-node-exporter-", s.Labels["pod"])
		}
		if s.Labels["namespace"] != "monitoring" {
			t.Errorf("node_load1: namespace=%q, want monitoring", s.Labels["namespace"])
		}
		if s.Labels["workload"] != "DaemonSet/grafana-k8s-monitoring-node-exporter" {
			t.Errorf("node_load1: workload=%q, want DaemonSet/grafana-k8s-monitoring-node-exporter", s.Labels["workload"])
		}
	}
}

// ── Failure-mode physics (Lane A Task 2) ────────────────────────────────────────────

// sumRestarts returns the total kube_pod_container_status_restarts_total across all app pods.
func sumRestarts(mc *coretest.MetricCapture) float64 {
	var sum float64
	for _, s := range mc.Find("kube_pod_container_status_restarts_total") {
		sum += s.Value
	}
	return sum
}

// validPodStatusReasons is the complete set of reason values KSM exposes on kube_pod_status_reason
// (pod-level admission/eviction reasons). OOMKilled is NOT among them — it is a container
// termination reason, surfaced on kube_pod_container_status_last_terminated_reason.
var validPodStatusReasons = map[string]bool{
	"Evicted": true, "NodeAffinity": true, "NodeLost": true, "PreemptionByScheduler": true,
	"SchedulingGated": true, "Shutdown": true, "TerminationByKubelet": true,
	"UnexpectedAdmissionError": true,
}

// TestOOMKill asserts oom_kill (active for the cluster) raises restart counters above baseline and
// surfaces the documented container-termination INCIDENT series
// kube_pod_container_status_last_terminated_reason{reason="OOMKilled"} — NEVER on
// kube_pod_status_reason (where OOMKilled is not a valid reason value).
func TestOOMKill(t *testing.T) {
	cl := coretest.Cluster()

	// Baseline: no failure.
	base := buildConstruct(t, cl)
	mcBase := &coretest.MetricCapture{}
	tickWorld(t, base, liveWorld(mcBase, &coretest.LogCapture{}, nil, nil))
	baseRestarts := sumRestarts(mcBase)

	// At baseline the incident series must be ABSENT (real KSM only exposes it for terminated
	// containers), and kube_pod_status_reason must never carry an OOMKilled value.
	if hasSeries(mcBase, "kube_pod_container_status_last_terminated_reason") {
		t.Error("baseline: kube_pod_container_status_last_terminated_reason must be ABSENT (no incident)")
	}
	if distinctVals(mcBase, "kube_pod_status_reason", "reason")["OOMKilled"] {
		t.Error("baseline: kube_pod_status_reason must never carry OOMKilled (invalid reason value)")
	}

	// Active oom_kill scoped to the cluster name.
	c := buildConstruct(t, cl)
	mc := &coretest.MetricCapture{}
	live := map[string][]shape.LiveFailure{
		"oom_kill": {{Enabled: true, Intensity: 1, Scope: cl.Name}},
	}
	tickWorld(t, c, liveWorld(mc, &coretest.LogCapture{}, live, nil))

	if got := sumRestarts(mc); got <= baseRestarts {
		t.Errorf("oom_kill: restarts total %.2f did not climb above baseline %.2f", got, baseRestarts)
	}
	// OOMKilled must NOT appear on kube_pod_status_reason — only valid reasons there.
	for r := range distinctVals(mc, "kube_pod_status_reason", "reason") {
		if !validPodStatusReasons[r] {
			t.Errorf("oom_kill: kube_pod_status_reason carries invalid reason value %q", r)
		}
	}
	// The incident series must appear with reason=OOMKilled, value > 0, and carry the documented
	// KSM label keys (container, namespace, pod, reason, uid).
	ltrSeries := mc.Find("kube_pod_container_status_last_terminated_reason")
	if len(ltrSeries) == 0 {
		t.Fatal("oom_kill: kube_pod_container_status_last_terminated_reason not emitted")
	}
	oomActive := false
	for _, s := range ltrSeries {
		if s.Labels["reason"] != "OOMKilled" {
			t.Errorf("oom_kill: last_terminated_reason has unexpected reason=%q (want OOMKilled)", s.Labels["reason"])
		}
		for _, k := range []string{"container", "namespace", "pod", "reason", "uid"} {
			if _, ok := s.Labels[k]; !ok {
				t.Errorf("oom_kill: last_terminated_reason missing label key %q", k)
			}
		}
		if s.Labels["reason"] == "OOMKilled" && s.Value > 0 {
			oomActive = true
		}
	}
	if !oomActive {
		t.Error("oom_kill: no last_terminated_reason{reason=OOMKilled} series with value > 0")
	}
}

// TestOOMKillIncidentSeriesRetiredWhenCleared asserts DEFECT-2: the conditionally-created OOM
// incident series does NOT ghost at value 1 after the incident clears. A single construct instance
// ticks with oom_kill active (series present), then again with it cleared (series absent).
func TestOOMKillIncidentSeriesRetiredWhenCleared(t *testing.T) {
	cl := coretest.Cluster()
	c := buildConstruct(t, cl)

	// Tick 1: oom_kill active — incident series present.
	mc1 := &coretest.MetricCapture{}
	live := map[string][]shape.LiveFailure{
		"oom_kill": {{Enabled: true, Intensity: 1, Scope: cl.Name}},
	}
	tickWorld(t, c, liveWorld(mc1, &coretest.LogCapture{}, live, nil))
	if !hasSeries(mc1, "kube_pod_container_status_last_terminated_reason") {
		t.Fatal("tick1 (oom active): incident series should be present")
	}

	// Tick 2: oom_kill cleared (SAME construct instance) — incident series must be ABSENT, not
	// frozen at its last value by state's retain-and-re-emit behaviour.
	mc2 := &coretest.MetricCapture{}
	now := time.Date(2026, 6, 13, 10, 1, 0, 0, time.UTC)
	w2 := liveWorld(mc2, &coretest.LogCapture{}, nil, nil)
	if err := c.Tick(context.Background(), now, w2); err != nil {
		t.Fatalf("tick2: %v", err)
	}
	if hasSeries(mc2, "kube_pod_container_status_last_terminated_reason") {
		t.Error("tick2 (oom cleared): incident series must be ABSENT (ghosted at value 1?)")
	}
}

// TestPodCrashloop asserts pod_crashloop raises restarts and moves affected pods out of Running into
// Pending on the existing kube_pod_status_phase series.
func TestPodCrashloop(t *testing.T) {
	cl := coretest.Cluster()

	base := buildConstruct(t, cl)
	mcBase := &coretest.MetricCapture{}
	tickWorld(t, base, liveWorld(mcBase, &coretest.LogCapture{}, nil, nil))
	baseRestarts := sumRestarts(mcBase)
	basePending := phaseSum(mcBase, "Pending")
	baseRunning := phaseSum(mcBase, "Running")

	c := buildConstruct(t, cl)
	mc := &coretest.MetricCapture{}
	live := map[string][]shape.LiveFailure{
		"pod_crashloop": {{Enabled: true, Intensity: 1, Scope: cl.Name}},
	}
	tickWorld(t, c, liveWorld(mc, &coretest.LogCapture{}, live, nil))

	if got := sumRestarts(mc); got <= baseRestarts {
		t.Errorf("pod_crashloop: restarts %.2f did not climb above baseline %.2f", got, baseRestarts)
	}
	if phaseSum(mc, "Pending") <= basePending {
		t.Errorf("pod_crashloop: Pending phase sum did not rise (baseline %.0f, got %.0f)", basePending, phaseSum(mc, "Pending"))
	}
	if phaseSum(mc, "Running") >= baseRunning {
		t.Errorf("pod_crashloop: Running phase sum did not fall (baseline %.0f, got %.0f)", baseRunning, phaseSum(mc, "Running"))
	}
}

// phaseSum returns the sum of kube_pod_status_phase for a given phase value.
func phaseSum(mc *coretest.MetricCapture, phase string) float64 {
	var sum float64
	for _, s := range mc.Find("kube_pod_status_phase") {
		if s.Labels["phase"] == phase {
			sum += s.Value
		}
	}
	return sum
}

// TestNodeNotReady asserts node_not_ready flips one node's Ready condition (true→0, false→1) on the
// existing kube_node_status_condition series.
func TestNodeNotReady(t *testing.T) {
	cl := coretest.Cluster()

	base := buildConstruct(t, cl)
	mcBase := &coretest.MetricCapture{}
	tickWorld(t, base, liveWorld(mcBase, &coretest.LogCapture{}, nil, nil))
	baseReadyTrue := readyConditionSum(mcBase, "true")

	c := buildConstruct(t, cl)
	mc := &coretest.MetricCapture{}
	live := map[string][]shape.LiveFailure{
		"node_not_ready": {{Enabled: true, Intensity: 1, Scope: cl.Name}},
	}
	tickWorld(t, c, liveWorld(mc, &coretest.LogCapture{}, live, nil))

	gotReadyTrue := readyConditionSum(mc, "true")
	gotReadyFalse := readyConditionSum(mc, "false")
	if gotReadyTrue >= baseReadyTrue {
		t.Errorf("node_not_ready: Ready{status=true} sum did not fall (baseline %.0f, got %.0f)", baseReadyTrue, gotReadyTrue)
	}
	if gotReadyFalse < 1 {
		t.Errorf("node_not_ready: expected at least one Ready{status=false}=1, got sum %.0f", gotReadyFalse)
	}
}

// readyConditionSum returns the sum of kube_node_status_condition{condition=Ready,status=<status>}.
func readyConditionSum(mc *coretest.MetricCapture, status string) float64 {
	var sum float64
	for _, s := range mc.Find("kube_node_status_condition") {
		if s.Labels["condition"] == "Ready" && s.Labels["status"] == status {
			sum += s.Value
		}
	}
	return sum
}

// incidentSeries maps the signals/k8s.md-documented incident series (present only under an active failure,
// absent at baseline) to the set of valid VALUES for their discriminating label. A series NOT in
// baseInv that appears under failure is only acceptable if it is one of these AND its discriminating
// label value is valid. DEFECT-8: this lets the test catch an invalid reason VALUE (e.g. OOMKilled
// emitted on the wrong series, or an out-of-vocab reason), not just a name/key drift.
var incidentSeries = map[string]struct {
	labelKey  string
	validVals map[string]bool
}{
	"kube_pod_container_status_last_terminated_reason": {
		labelKey:  "reason",
		validVals: map[string]bool{"OOMKilled": true, "Error": true},
	},
}

// TestFailuresDoNotChangeSchema verifies that (a) at BASELINE the schema is the documented steady
// state, (b) under failure, the only series that may appear that were ABSENT at baseline are
// signals/k8s.md-documented incident series, each with a VALID discriminating label value, and no baseline
// series is dropped — and no NEW label keys appear on shared series. Failures bend VALUES and may
// introduce documented incident series; they never invent names, keys, or out-of-vocab values (I32).
func TestFailuresDoNotChangeSchema(t *testing.T) {
	cl := coretest.Cluster()

	base := buildConstruct(t, cl)
	mcBase := &coretest.MetricCapture{}
	tickWorld(t, base, liveWorld(mcBase, &coretest.LogCapture{}, nil, nil))

	c := buildConstruct(t, cl)
	mc := &coretest.MetricCapture{}
	live := map[string][]shape.LiveFailure{
		"oom_kill":       {{Enabled: true, Intensity: 1, Scope: cl.Name}},
		"pod_crashloop":  {{Enabled: true, Intensity: 1, Scope: cl.Name}},
		"node_not_ready": {{Enabled: true, Intensity: 1, Scope: cl.Name}},
	}
	tickWorld(t, c, liveWorld(mc, &coretest.LogCapture{}, live, nil))

	baseInv := schemaInventory(mcBase)
	failInv := schemaInventory(mc)

	// (a) Baseline must NOT already contain incident series (else this test proves nothing).
	for name := range incidentSeries {
		if _, ok := baseInv[name]; ok {
			t.Errorf("baseline tick unexpectedly contains incident series %q (should be absent at rest)", name)
		}
	}

	// (b) Any series present under failure but ABSENT at baseline must be a documented incident
	// series, and every one of its discriminating label VALUES must be in the valid set.
	for name, keys := range failInv {
		spec, isIncident := incidentSeries[name]
		if _, inBase := baseInv[name]; !inBase {
			if !isIncident {
				t.Errorf("failure tick introduced an UNDOCUMENTED series name %q (not a known incident series)", name)
				continue
			}
			// Validate the discriminating label value against the valid set.
			for v := range distinctVals(mc, name, spec.labelKey) {
				if !spec.validVals[v] {
					t.Errorf("incident series %q carries invalid %s value %q (valid: %v)", name, spec.labelKey, v, spec.validVals)
				}
			}
			continue
		}
		// A series shared with baseline must not gain new label keys.
		for k := range keys {
			if !baseInv[name][k] {
				t.Errorf("failure tick introduced a NEW label key %q on existing series %q (schema must be unchanged)", k, name)
			}
		}
	}

	// No baseline series may be dropped under failure.
	for name := range baseInv {
		if _, ok := failInv[name]; !ok {
			t.Errorf("failure tick DROPPED series name %q (schema must be unchanged)", name)
		}
	}

	// DEFECT-8 value guard: kube_pod_status_reason must NEVER carry an out-of-vocab reason value
	// (catches OOMKilled or any invalid reason being emitted on the wrong series).
	for v := range distinctVals(mc, "kube_pod_status_reason", "reason") {
		if !validPodStatusReasons[v] {
			t.Errorf("kube_pod_status_reason carries invalid reason value %q under failure", v)
		}
	}
}

// schemaInventory returns metric name → set of label keys.
func schemaInventory(mc *coretest.MetricCapture) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	for _, s := range mc.All() {
		if out[s.Name] == nil {
			out[s.Name] = map[string]bool{}
		}
		for k := range s.Labels {
			out[s.Name][k] = true
		}
	}
	return out
}

// ── Live topology: scale-up cascade + scale-down retirement (Lane A Task 3) ──────────

// TestScaleUpCrossesNodeBoundary overrides a workload from 4 to 49 replicas (crossing the
// per-8-pod node boundary from the prod floor), and asserts pod count, derived node count, and
// pod→node placement all reflect the live topology re-derived via fixture.LiveNodes.
// scalableCluster() has Env.Weight=1.0 → prod floor=6; ceil(49/8)=7 > 6, so scaling to 49 pods
// adds a 7th node (crossing from 6).
func TestScaleUpCrossesNodeBoundary(t *testing.T) {
	cl := scalableCluster()
	// Set the single workload's declared replicas to 4 (baseline ⇒ 6 nodes, prod floor).
	cl.Workloads[0].Replicas = 4
	cl.Workloads[0].PodNames = nil
	cl.Workloads[0].NodeIdx = nil
	for p := 0; p < 4; p++ {
		cl.Workloads[0].PodNames = append(cl.Workloads[0].PodNames, clusterPodName(cl, cl.Workloads[0].Name, p))
		cl.Workloads[0].NodeIdx = append(cl.Workloads[0].NodeIdx, p)
	}
	// Baseline nodes at declared count (floor=6 for prod weight=1.0).
	cl.Nodes = fixture.LiveNodes(cl, func(_ string, d int) int { return d })

	wl := cl.Workloads[0].Name
	sc := scale.New()
	sc.Set(map[string]int{wl: 49}) // ceil(49/8)=7 > floor 6 → node boundary crossed

	c := buildConstruct(t, cl)
	mc := &coretest.MetricCapture{}
	tickWorld(t, c, liveWorld(mc, &coretest.LogCapture{}, nil, sc))

	// (a) 49 pods of the scaled workload appear in kube_pod_info.
	wantPods := map[string]bool{}
	for p := 0; p < 49; p++ {
		wantPods[clusterPodName(cl, wl, p)] = true
	}
	gotPods := distinctVals(mc, "kube_pod_info", "pod")
	missing := 0
	for p := range wantPods {
		if !gotPods[p] {
			missing++
		}
	}
	if missing != 0 {
		t.Errorf("scale-up: %d of 49 scaled pods missing from kube_pod_info", missing)
	}

	// (b) Node count cascades to ceil(49/8)=7 derived nodes (crosses the 6-node prod floor).
	wantNodes := fixture.DeriveNodesFloor(cl.Seed, cl.Name, cl.NodeGroups, cl.Region, 49, fixture.EnvNodeFloor(cl.Env.Weight))
	if len(wantNodes) != 7 {
		t.Fatalf("test premise: 49 pods + prod floor should derive 7 nodes, got %d", len(wantNodes))
	}
	gotNodes := distinctVals(mc, "kube_node_info", "node")
	if len(gotNodes) != 7 {
		t.Errorf("scale-up: kube_node_info has %d distinct nodes, want 7: %v", len(gotNodes), gotNodes)
	}
	nodeHosts := map[string]bool{}
	for _, n := range wantNodes {
		nodeHosts[n.Hostname] = true
		if !gotNodes[n.Hostname] {
			t.Errorf("scale-up: derived node %q missing from kube_node_info", n.Hostname)
		}
	}

	// (c) Every scaled pod's node label is one of the 7 derived node hostnames.
	for _, s := range mc.Find("kube_pod_info") {
		if !wantPods[s.Labels["pod"]] {
			continue // skip substrate (alloy) pods placed by the same loop
		}
		if !nodeHosts[s.Labels["node"]] {
			t.Errorf("scale-up: pod %q placed on non-derived node %q", s.Labels["pod"], s.Labels["node"])
		}
	}
}

// TestScaleDownRetiresPods ticks at 49 replicas then at 4, and asserts the 45 retired pods are
// ABSENT from the second capture (not frozen at their last value via state's retain-and-re-emit
// behaviour). scalableCluster() has Env.Weight=1.0 → prod floor=6.
// Tick 1: 49 pods → ceil(49/8)=7 nodes (above floor). Tick 2: 4 pods → floor=6 nodes.
// The 7th node is retired.
func TestScaleDownRetiresPods(t *testing.T) {
	cl := scalableCluster()
	cl.Workloads[0].Replicas = 4
	cl.Workloads[0].PodNames = nil
	cl.Workloads[0].NodeIdx = nil
	for p := 0; p < 4; p++ {
		cl.Workloads[0].PodNames = append(cl.Workloads[0].PodNames, clusterPodName(cl, cl.Workloads[0].Name, p))
		cl.Workloads[0].NodeIdx = append(cl.Workloads[0].NodeIdx, p)
	}
	// Baseline with prod floor (Env.Weight=1.0 → floor=6).
	cl.Nodes = fixture.LiveNodes(cl, func(_ string, d int) int { return d })
	wl := cl.Workloads[0].Name

	c := buildConstruct(t, cl)

	// Tick 1: scale up to 49 (⇒ ceil(49/8)=7 nodes, crossing the 6-node prod floor).
	sc := scale.New()
	sc.Set(map[string]int{wl: 49})
	mc1 := &coretest.MetricCapture{}
	tickWorld(t, c, liveWorld(mc1, &coretest.LogCapture{}, nil, sc))
	if got := len(podsForWorkload(mc1, cl.Seed+":"+cl.Name, wl)); got != 49 {
		t.Fatalf("tick1: expected 49 scaled pods, got %d", got)
	}
	floor := fixture.EnvNodeFloor(cl.Env.Weight)
	nodesAt49 := fixture.DeriveNodesFloor(cl.Seed, cl.Name, cl.NodeGroups, cl.Region, 49, floor)
	if len(nodesAt49) != 7 {
		t.Fatalf("test premise: 49 pods + prod floor ⇒ 7 nodes, got %d", len(nodesAt49))
	}

	// Tick 2: scale down to 4 (⇒ floor=6 nodes; 60s later) into a FRESH capture.
	sc.Set(map[string]int{wl: 4})
	mc2 := &coretest.MetricCapture{}
	w2 := liveWorld(mc2, &coretest.LogCapture{}, nil, sc)
	now := time.Date(2026, 6, 13, 10, 1, 0, 0, time.UTC)
	if err := c.Tick(context.Background(), now, w2); err != nil {
		t.Fatalf("tick2: %v", err)
	}

	got := podsForWorkload(mc2, cl.Seed+":"+cl.Name, wl)
	if len(got) != 4 {
		t.Errorf("scale-down: expected exactly 4 live pods after retirement, got %d: %v", len(got), got)
	}
	// The 45 retired pod ordinals [4,49) must be ABSENT (dropped, not frozen).
	for p := 4; p < 49; p++ {
		retired := clusterPodName(cl, wl, p)
		if got[retired] {
			t.Errorf("scale-down: retired pod %q still present (frozen, not dropped)", retired)
		}
	}

	// Node cascade down: 7 nodes → 6 nodes (prod floor). The 7th node's kube_node_info must be
	// ABSENT from tick2.
	nodesAt4 := fixture.DeriveNodesFloor(cl.Seed, cl.Name, cl.NodeGroups, cl.Region, 4, floor)
	if len(nodesAt4) != 6 {
		t.Fatalf("test premise: 4 pods + prod floor ⇒ 6 nodes, got %d", len(nodesAt4))
	}
	live4 := map[string]bool{}
	for _, n := range nodesAt4 {
		live4[n.Hostname] = true
	}
	gotNodes := distinctVals(mc2, "kube_node_info", "node")
	if len(gotNodes) != 6 {
		t.Errorf("scale-down: kube_node_info has %d nodes after retirement, want 6: %v", len(gotNodes), gotNodes)
	}
	for _, n := range nodesAt49 {
		if !live4[n.Hostname] && gotNodes[n.Hostname] {
			t.Errorf("scale-down: retired node %q still present in kube_node_info (frozen, not dropped)", n.Hostname)
		}
	}
}

// podsForWorkload returns the set of kube_pod_info pod labels that belong to workload wl (ordinals
// 0..N), i.e. exactly the deterministic PodName forms — excludes substrate pods.
func podsForWorkload(mc *coretest.MetricCapture, seed, wl string) map[string]bool {
	owned := map[string]bool{}
	for p := 0; p < 200; p++ {
		owned[fixture.PodName(seed, wl, p)] = true
	}
	out := map[string]bool{}
	for _, s := range mc.Find("kube_pod_info") {
		if owned[s.Labels["pod"]] {
			out[s.Labels["pod"]] = true
		}
	}
	return out
}

// TestLiveClusterDefaultParity asserts that at DEFAULT scaling (nil Scaling source) the construct
// reproduces the resolved cluster's pods + nodes byte-for-byte — same pod names, same node set — so
// existing tests and the -dump inventory are unaffected by the live-topology machinery.
func TestLiveClusterDefaultParity(t *testing.T) {
	cl := scalableCluster()

	c := buildConstruct(t, cl)
	mc := &coretest.MetricCapture{}
	tickWorld(t, c, liveWorld(mc, &coretest.LogCapture{}, nil, nil))

	// Pods: exactly the fixture PodNames.
	wantPods := map[string]bool{}
	for _, wl := range cl.Workloads {
		for _, pn := range wl.PodNames {
			wantPods[pn] = true
		}
	}
	gotPods := podsForWorkloadAll(mc, cl)
	for pn := range wantPods {
		if !gotPods[pn] {
			t.Errorf("default parity: fixture pod %q missing", pn)
		}
	}
	for pn := range gotPods {
		if !wantPods[pn] {
			t.Errorf("default parity: unexpected extra app pod %q", pn)
		}
	}

	// Nodes: exactly the fixture node hostnames.
	wantNodes := map[string]bool{}
	for _, n := range cl.Nodes {
		wantNodes[n.Hostname] = true
	}
	gotNodes := distinctVals(mc, "kube_node_info", "node")
	if len(gotNodes) != len(wantNodes) {
		t.Errorf("default parity: %d nodes, want %d", len(gotNodes), len(wantNodes))
	}
	for h := range wantNodes {
		if !gotNodes[h] {
			t.Errorf("default parity: fixture node %q missing", h)
		}
	}
}

// ── SK-30: OpenCost HTTP response series dropped (not emitted by real OpenCost 1.120.x) ──────

// TestKubecostHTTPResponseSeriesDropped asserts that kubecost_http_response_size_bytes and
// kubecost_http_response_time_seconds are NOT emitted (they do not exist in OpenCost 1.120.3)
// while kubecost_http_requests_total IS emitted.
func TestKubecostHTTPResponseSeriesDropped(t *testing.T) {
	cl := coretest.Cluster()
	// coretest.Cluster() already has K8sMonitoring.Enabled + OpenCost = true; guard anyway.
	if !cl.K8sMonitoring.Enabled || !cl.K8sMonitoring.OpenCost {
		t.Skip("coretest.Cluster() does not have K8sMonitoring.Enabled + OpenCost on")
	}

	c := buildConstruct(t, cl)
	mc := &coretest.MetricCapture{}
	lc := &coretest.LogCapture{}
	tick(t, c, mc, lc)

	// These two series do not exist in OpenCost 1.120.x — must be absent.
	for _, absent := range []string{
		"kubecost_http_response_size_bytes",
		"kubecost_http_response_time_seconds",
	} {
		if hasSeries(mc, absent) {
			t.Errorf("series %q must NOT be emitted (not present in OpenCost 1.120.3)", absent)
		}
	}

	// The one real HTTP metric OpenCost emits — must be present.
	if !hasSeries(mc, "kubecost_http_requests_total") {
		t.Error("kubecost_http_requests_total must be emitted (OpenCost HTTP counter)")
	}
}

// podsForWorkloadAll returns the set of app-workload pods (any declared workload) present in
// kube_pod_info, excluding substrate (alloy) pods.
func podsForWorkloadAll(mc *coretest.MetricCapture, cl *fixture.Cluster) map[string]bool {
	owned := map[string]bool{}
	for _, wl := range cl.Workloads {
		for p := 0; p < 200; p++ {
			owned[clusterPodName(cl, wl.Name, p)] = true
		}
	}
	out := map[string]bool{}
	for _, s := range mc.Find("kube_pod_info") {
		if owned[s.Labels["pod"]] {
			out[s.Labels["pod"]] = true
		}
	}
	return out
}
