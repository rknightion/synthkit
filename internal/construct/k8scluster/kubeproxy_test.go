// SPDX-License-Identifier: AGPL-3.0-only

// kubeproxy_test.go — TDD for emitKubeProxy.
package k8scluster_test

import (
	"testing"

	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
)

// tickKubeProxy builds a construct with KubeProxy enabled and returns the MetricCapture.
func tickKubeProxy(t *testing.T) (*coretest.MetricCapture, *fixture.Cluster) {
	t.Helper()
	cl := coretest.Cluster()
	cl.K8sMonitoring.ControlPlane = fixture.ControlPlane{KubeProxy: true}
	c := buildConstruct(t, cl)
	mc := &coretest.MetricCapture{}
	lc := &coretest.LogCapture{}
	tick(t, c, mc, lc)
	return mc, cl
}

// ── Lane G: kube-proxy ────────────────────────────────────────────────────────────────

// TestKubeProxyBucketEmitted verifies the core histogram is emitted.
func TestKubeProxyBucketEmitted(t *testing.T) {
	mc, _ := tickKubeProxy(t)
	if !hasSeries(mc, "kubeproxy_sync_proxy_rules_duration_seconds_bucket") {
		t.Error("kubeproxy_sync_proxy_rules_duration_seconds_bucket: not emitted")
	}
}

// TestKubeProxyIPTablesTotal verifies the counter family is emitted with ip_family + table.
func TestKubeProxyIPTablesTotal(t *testing.T) {
	mc, _ := tickKubeProxy(t)
	series := mc.Find("kubeproxy_sync_proxy_rules_iptables_total")
	if len(series) == 0 {
		t.Fatal("kubeproxy_sync_proxy_rules_iptables_total: not emitted")
	}
	for _, s := range series {
		if _, ok := s.Labels["ip_family"]; !ok {
			t.Errorf("kubeproxy_sync_proxy_rules_iptables_total: missing ip_family (labels: %v)", s.Labels)
		}
		if _, ok := s.Labels["table"]; !ok {
			t.Errorf("kubeproxy_sync_proxy_rules_iptables_total: missing table (labels: %v)", s.Labels)
		}
	}
}

// TestKubeProxyRestClientRequests verifies rest_client_requests_total is emitted.
func TestKubeProxyRestClientRequests(t *testing.T) {
	mc, _ := tickKubeProxy(t)
	if !hasSeries(mc, "rest_client_requests_total") {
		t.Error("rest_client_requests_total: not emitted")
	}
}

// TestKubeProxyOneInstancePerNode verifies kubeproxy_sync_proxy_rules_iptables_last has
// one unique instance per node.
func TestKubeProxyOneInstancePerNode(t *testing.T) {
	mc, cl := tickKubeProxy(t)
	series := mc.Find("kubeproxy_sync_proxy_rules_iptables_last")
	if len(series) == 0 {
		t.Fatal("kubeproxy_sync_proxy_rules_iptables_last: not emitted")
	}

	// Collect distinct instances.
	instances := map[string]bool{}
	for _, s := range series {
		instances[s.Labels["instance"]] = true
	}
	// Each node gets one instance (with ip:10249), so we expect len(nodes) distinct instances.
	if len(instances) < len(cl.Nodes) {
		t.Errorf("kubeproxy_sync_proxy_rules_iptables_last: want %d distinct instances (one per node), got %d: %v",
			len(cl.Nodes), len(instances), instances)
	}
}

// TestKubeProxyIPTablesLastLabels verifies kubeproxy_sync_proxy_rules_iptables_last carries ip_family + table.
func TestKubeProxyIPTablesLastLabels(t *testing.T) {
	mc, _ := tickKubeProxy(t)
	for _, s := range mc.Find("kubeproxy_sync_proxy_rules_iptables_last") {
		if _, ok := s.Labels["ip_family"]; !ok {
			t.Errorf("kubeproxy_sync_proxy_rules_iptables_last: missing ip_family (labels: %v)", s.Labels)
		}
		if _, ok := s.Labels["table"]; !ok {
			t.Errorf("kubeproxy_sync_proxy_rules_iptables_last: missing table (labels: %v)", s.Labels)
		}
	}
}

// TestKubeProxyJob verifies all kube-proxy series carry the correct job.
func TestKubeProxyJob(t *testing.T) {
	mc, _ := tickKubeProxy(t)
	for _, s := range mc.All() {
		if s.Labels["job"] != "integrations/kubernetes/kube-proxy" {
			continue // other jobs in the capture, skip
		}
		// All kube-proxy series must carry cluster.
		if s.Labels["cluster"] == "" {
			t.Errorf("kube-proxy series %q missing cluster label", s.Name)
		}
	}
	// Specifically check the histogram.
	for _, s := range mc.Find("kubeproxy_sync_proxy_rules_duration_seconds_bucket") {
		if got := s.Labels["job"]; got != "integrations/kubernetes/kube-proxy" {
			t.Errorf("kubeproxy_sync_proxy_rules_duration_seconds_bucket: job=%q, want integrations/kubernetes/kube-proxy", got)
		}
	}
}

// TestKubeProxyDisabledWhenFlagOff verifies no kube-proxy series when ControlPlane.KubeProxy=false.
func TestKubeProxyDisabledWhenFlagOff(t *testing.T) {
	cl := coretest.Cluster()
	cl.K8sMonitoring.ControlPlane = fixture.ControlPlane{KubeProxy: false}
	c := buildConstruct(t, cl)
	mc := &coretest.MetricCapture{}
	lc := &coretest.LogCapture{}
	tick(t, c, mc, lc)

	for _, s := range mc.All() {
		if s.Labels["job"] == "integrations/kubernetes/kube-proxy" {
			t.Errorf("KubeProxy=false: unexpected kube-proxy series %q", s.Name)
		}
	}
}

// TestKubeProxyBuildInfo verifies kubernetes_build_info is emitted under the kube-proxy job.
func TestKubeProxyBuildInfo(t *testing.T) {
	mc, _ := tickKubeProxy(t)
	// kubernetes_build_info is emitted by both kubelet and kube-proxy.
	// At least one must carry the kube-proxy job.
	found := false
	for _, s := range mc.Find("kubernetes_build_info") {
		if s.Labels["job"] == "integrations/kubernetes/kube-proxy" {
			found = true
			break
		}
	}
	if !found {
		t.Error("kubernetes_build_info: no series with job=integrations/kubernetes/kube-proxy")
	}
}

// TestKubeProxyHistogramFamilies verifies that all five histogram families produce bucket series.
func TestKubeProxyHistogramFamilies(t *testing.T) {
	mc, _ := tickKubeProxy(t)
	for _, nm := range []string{
		"kubeproxy_sync_proxy_rules_duration_seconds_bucket",
		"kubeproxy_sync_full_proxy_rules_duration_seconds_bucket",
		"kubeproxy_sync_partial_proxy_rules_duration_seconds_bucket",
		"kubeproxy_network_programming_duration_seconds_bucket",
		"kubeproxy_conntrack_reconciler_sync_duration_seconds_bucket",
	} {
		if !hasSeries(mc, nm) {
			t.Errorf("kube-proxy histogram %q: not emitted", nm)
		}
	}
}

// TestKubeProxyConntrackReconNoIPFamily verifies kubeproxy_conntrack_reconciler_sync_duration_seconds
// does NOT carry ip_family (per spec).
func TestKubeProxyConntrackReconNoIPFamily(t *testing.T) {
	mc, _ := tickKubeProxy(t)
	for _, s := range mc.Find("kubeproxy_conntrack_reconciler_sync_duration_seconds_bucket") {
		if _, ok := s.Labels["ip_family"]; ok {
			t.Errorf("kubeproxy_conntrack_reconciler_sync_duration_seconds_bucket must NOT have ip_family (labels: %v)", s.Labels)
		}
	}
}
