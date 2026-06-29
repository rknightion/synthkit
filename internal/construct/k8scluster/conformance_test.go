// SPDX-License-Identifier: AGPL-3.0-only

// conformance_test.go — label-fidelity tests for emitOpenCost enrichments.
// Asserts that the enriched label sets match a live reference cluster OpenCost capture
// documented in signals/k8s.md [slug: k8s-conformance].
package k8scluster

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/sink/promrw"
)

// ocCluster returns a coretest cluster with K8sMonitoring.OpenCost=true and
// nodes that have InstanceType set (needed for arch/instance_type labels).
func ocCluster() *fixture.Cluster {
	// coretest.Cluster() already sets K8sMonitoring.OpenCost=true and Enabled=true.
	// Node InstanceType is "m6i.xlarge" in coretest; UID is intentionally unset to
	// exercise the fallback path.
	cl := coretest.Cluster()
	// Post-SubstrateWorkloads migration: coredns/metrics-server are no longer synthesized
	// name-only inside the construct — they (and other addon/baseline pods) arrive via
	// cl.SubstrateWorkloads with resolver-minted PodNames. The opencost allocation basis still
	// varies per workload by size class, so populate the baseline substrate (coredns size-class
	// 0.25 + metrics-server 0.5, distinct from test-api's 0.1) to keep the mix the dashboards see.
	if cl.Seed == "" {
		cl.Seed = cl.Name
	}
	subs := fixture.BaselineWorkloads()
	for i := range subs {
		subs[i].PodNames = fixture.WorkloadPodNames(cl.Seed, subs[i], nil)
	}
	cl.SubstrateWorkloads = subs
	return cl
}

// runOpenCost ticks the cluster and returns all emitted metrics as a map name→[]Series.
func runOpenCost(t *testing.T, cl *fixture.Cluster) map[string][]promrw.Series {
	t.Helper()
	c, err := New(nil, &fixture.Set{Cluster: cl, Env: cl.Env, Cloud: cl.Cloud})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	mc := &coretest.MetricCapture{}
	lc := &coretest.LogCapture{}
	w := coretest.World(mc, lc, nil)
	now := time.Date(2026, 6, 14, 10, 0, 0, 0, time.UTC)
	if err := c.Tick(context.Background(), now, w); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	out := map[string][]promrw.Series{}
	for _, s := range mc.All() {
		out[s.Name] = append(out[s.Name], s)
	}
	return out
}

// ── node cost series carry arch/instance_type/provider_id/region/uid ─────────────────

func TestNodeCostSeriesEnrichedLabels(t *testing.T) {
	cl := ocCluster()
	series := runOpenCost(t, cl)

	for _, metricName := range []string{
		"node_cpu_hourly_cost",
		"node_ram_hourly_cost",
		"node_total_hourly_cost",
		"node_gpu_count",
		"node_gpu_hourly_cost",
		"kubecost_node_is_spot",
	} {
		ss, ok := series[metricName]
		if !ok || len(ss) == 0 {
			t.Errorf("%s: not emitted", metricName)
			continue
		}
		for _, s := range ss {
			for _, key := range []string{"arch", "instance_type", "provider_id", "region", "uid"} {
				v, present := s.Labels[key]
				if !present {
					t.Errorf("%s: missing label key %q", metricName, key)
					continue
				}
				if v == "" {
					t.Errorf("%s: label %q is empty", metricName, key)
				}
			}
			// provider_id must look like aws:///az/instance-id
			pid := s.Labels["provider_id"]
			if !strings.HasPrefix(pid, "aws:///") {
				t.Errorf("%s: provider_id=%q does not start with aws:///", metricName, pid)
			}
			// arch must be one of the valid kube arch values
			arch := s.Labels["arch"]
			if arch != "amd64" && arch != "arm64" {
				t.Errorf("%s: arch=%q, want amd64 or arm64", metricName, arch)
			}
		}
	}
}

// ── kubecost_load_balancer_cost carries ingress_ip/namespace/service_name/uid ─────────

func TestLoadBalancerCostLabels(t *testing.T) {
	cl := ocCluster()
	series := runOpenCost(t, cl)

	ss, ok := series["kubecost_load_balancer_cost"]
	if !ok || len(ss) == 0 {
		t.Fatal("kubecost_load_balancer_cost: not emitted")
	}

	required := []string{"ingress_ip", "namespace", "service_name", "uid"}
	for _, s := range ss {
		for _, key := range required {
			v, present := s.Labels[key]
			if !present {
				t.Errorf("kubecost_load_balancer_cost: missing label key %q", key)
				continue
			}
			if v == "" {
				t.Errorf("kubecost_load_balancer_cost: label %q is empty", key)
			}
		}
		// ingress_ip must be in the 100.x.x.x range (RFC 6598 / live reference cluster pattern)
		ip := s.Labels["ingress_ip"]
		if !strings.HasPrefix(ip, "100.") {
			t.Errorf("kubecost_load_balancer_cost: ingress_ip=%q, want 100.x.x.x range", ip)
		}
	}
}

// ── container allocation series carry uid ────────────────────────────────────────────

func TestContainerAllocationUID(t *testing.T) {
	cl := ocCluster()
	series := runOpenCost(t, cl)

	for _, metricName := range []string{
		"container_cpu_allocation",
		"container_memory_allocation_bytes",
		"container_gpu_allocation",
	} {
		ss, ok := series[metricName]
		if !ok || len(ss) == 0 {
			t.Errorf("%s: not emitted", metricName)
			continue
		}
		for _, s := range ss {
			uid, present := s.Labels["uid"]
			if !present {
				t.Errorf("%s: missing label key uid", metricName)
				continue
			}
			if uid == "" {
				t.Errorf("%s: uid label is empty", metricName)
			}
			// uid must look like a UUID (8-4-4-4-12 hex groups)
			parts := strings.Split(uid, "-")
			if len(parts) != 5 {
				t.Errorf("%s: uid=%q does not look like a UUID (5 dash-separated groups)", metricName, uid)
			}
		}
	}
}

// TestContainerCPUAllocationVaries locks the OpenCost CPU allocation to the per-workload size-class
// basis (resolveCPURequest) — i.e. NOT a uniform hardcoded 0.25 — so the efficiency dashboard's
// CPU-waste panels stay consistent with the KSM kube_pod_container_resource_requests series. A
// regression back to a single hardcoded value would collapse this to one distinct value.
func TestContainerCPUAllocationVaries(t *testing.T) {
	cl := ocCluster()
	series := runOpenCost(t, cl)
	vals := map[float64]bool{}
	for _, s := range series["container_cpu_allocation"] {
		vals[s.Value] = true
	}
	if len(vals) < 2 {
		t.Errorf("container_cpu_allocation should vary per workload (size-class basis), got %d distinct value(s): %v", len(vals), vals)
	}
}

// ── kubecost_cluster_management_cost carries provisioner_name=EKS ────────────────────

func TestClusterManagementCostProvisionerName(t *testing.T) {
	cl := ocCluster()
	series := runOpenCost(t, cl)

	ss, ok := series["kubecost_cluster_management_cost"]
	if !ok || len(ss) == 0 {
		t.Fatal("kubecost_cluster_management_cost: not emitted")
	}
	for _, s := range ss {
		v, present := s.Labels["provisioner_name"]
		if !present {
			t.Errorf("kubecost_cluster_management_cost: missing provisioner_name label")
			continue
		}
		if v != "EKS" {
			t.Errorf("kubecost_cluster_management_cost: provisioner_name=%q, want EKS", v)
		}
	}
}

// ── kubecost_http_requests_total carries handler label ────────────────────────────────

func TestHTTPRequestsTotalHandlerLabel(t *testing.T) {
	cl := ocCluster()
	series := runOpenCost(t, cl)

	ss, ok := series["kubecost_http_requests_total"]
	if !ok || len(ss) == 0 {
		t.Fatal("kubecost_http_requests_total: not emitted")
	}

	handlers := map[string]bool{}
	for _, s := range ss {
		v, present := s.Labels["handler"]
		if !present {
			t.Errorf("kubecost_http_requests_total: missing handler label")
			continue
		}
		if v == "" {
			t.Errorf("kubecost_http_requests_total: handler label is empty")
		}
		handlers[v] = true
		// code and method must still be present
		if s.Labels["code"] == "" {
			t.Errorf("kubecost_http_requests_total: code label missing/empty")
		}
		if s.Labels["method"] == "" {
			t.Errorf("kubecost_http_requests_total: method label missing/empty")
		}
	}
	// A real cluster has /metrics and /healthz handlers
	for _, want := range []string{"/metrics", "/healthz"} {
		if !handlers[want] {
			t.Errorf("kubecost_http_requests_total: handler=%q not found (got %v)", want, handlers)
		}
	}
}

// ── deployment_match_labels / service_selector_labels carry uid + label_* bag ─────────

func TestDeploymentAndServiceMatchLabelsEnriched(t *testing.T) {
	cl := ocCluster()
	series := runOpenCost(t, cl)

	for _, metricName := range []string{"deployment_match_labels", "service_selector_labels"} {
		ss, ok := series[metricName]
		if !ok || len(ss) == 0 {
			t.Errorf("%s: not emitted", metricName)
			continue
		}
		for _, s := range ss {
			// uid must be present
			if _, present := s.Labels["uid"]; !present {
				t.Errorf("%s: missing uid label", metricName)
			}
			// at least one label_* key must be present
			hasLabelKey := false
			for k := range s.Labels {
				if strings.HasPrefix(k, "label_") {
					hasLabelKey = true
					break
				}
			}
			if !hasLabelKey {
				t.Errorf("%s: no label_* key found (expected representative label bag)", metricName)
			}
			// label_app_kubernetes_io_name must be present and non-empty
			if v := s.Labels["label_app_kubernetes_io_name"]; v == "" {
				t.Errorf("%s: label_app_kubernetes_io_name is missing or empty", metricName)
			}
		}
	}
}

// ── statefulSet_match_labels emitted with namespace/statefulSet/uid + label_ key ──────

func TestStatefulSetMatchLabels(t *testing.T) {
	cl := ocCluster()
	series := runOpenCost(t, cl)

	ss, ok := series["statefulSet_match_labels"]
	if !ok || len(ss) == 0 {
		t.Fatal("statefulSet_match_labels: not emitted (new family missing from emitOpenCost)")
	}

	for _, s := range ss {
		for _, key := range []string{"namespace", "statefulSet", "uid"} {
			v, present := s.Labels[key]
			if !present {
				t.Errorf("statefulSet_match_labels: missing label key %q", key)
				continue
			}
			if v == "" {
				t.Errorf("statefulSet_match_labels: label %q is empty", key)
			}
		}
		// at least one label_* key must be present
		hasLabelKey := false
		for k := range s.Labels {
			if strings.HasPrefix(k, "label_") {
				hasLabelKey = true
				break
			}
		}
		if !hasLabelKey {
			t.Errorf("statefulSet_match_labels: no label_* key found (expected representative label bag)")
		}
		// value must be 1 (match-label gauge)
		if s.Value != 1 {
			t.Errorf("statefulSet_match_labels: value=%.0f, want 1", s.Value)
		}
	}
}

// ── deferred series must remain absent ───────────────────────────────────────────────

func TestDeferredSeriesAbsent(t *testing.T) {
	cl := ocCluster()
	series := runOpenCost(t, cl)

	// pv_hourly_cost + pod_pvc_allocation are now emitted (storage-metrics fidelity wave);
	// see TestPVCStorageJoin. The remaining two have no fixture seam yet.
	for _, absent := range []string{
		"kubecost_http_response_size_bytes",
		"kubecost_http_response_time_seconds",
	} {
		if _, ok := series[absent]; ok {
			t.Errorf("deferred series %q must NOT be emitted", absent)
		}
	}
}
