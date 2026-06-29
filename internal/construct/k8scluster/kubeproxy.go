// SPDX-License-Identifier: AGPL-3.0-only

// kubeproxy.go — kube-proxy control-plane metrics for k8scluster.
// Gated at call-site by K8sMonitoring.ControlPlane.KubeProxy.
// Per node, instance = node.PrivateIP + ":10249", job = "integrations/kubernetes/kube-proxy".
//
// Live-evidenced families (ARCHITECTURE I24):
//
//	histograms: kubeproxy_sync_proxy_rules_duration_seconds,
//	            kubeproxy_sync_full_proxy_rules_duration_seconds,
//	            kubeproxy_sync_partial_proxy_rules_duration_seconds,
//	            kubeproxy_network_programming_duration_seconds,
//	            kubeproxy_conntrack_reconciler_sync_duration_seconds
//	gauges:     kubeproxy_sync_proxy_rules_iptables_last,
//	            kubeproxy_sync_proxy_rules_last_timestamp_seconds,
//	            kubeproxy_sync_proxy_rules_last_queued_timestamp_seconds,
//	            kubeproxy_sync_proxy_rules_endpoint_changes_pending,
//	            kubeproxy_sync_proxy_rules_service_changes_pending,
//	            kubernetes_build_info (=1)
//	counters:   kubeproxy_sync_proxy_rules_iptables_total,
//	            kubeproxy_sync_proxy_rules_no_local_endpoints_total,
//	            kubeproxy_sync_proxy_rules_endpoint_changes_total,
//	            kubeproxy_sync_proxy_rules_service_changes_total,
//	            kubeproxy_conntrack_reconciler_deleted_entries_total,
//	            kubeproxy_iptables_ct_state_invalid_dropped_packets_total,
//	            kubeproxy_iptables_localhost_nodeports_accepted_packets_total,
//	            rest_client_requests_total
package k8scluster

import (
	"fmt"
	"strings"

	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/state"
	statelib "github.com/rknightion/synthkit/internal/state"
)

const jobKubeProxy = "integrations/kubernetes/kube-proxy"

// kubeProxyHistoBounds are the default Prometheus seconds histogram bounds.
var kubeProxyHistoBounds = []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10}

// kubeProxyIPFamilies are the IP family label values on per-family metrics.
var kubeProxyIPFamilies = []string{"IPv4", "IPv6"}

// kubeProxyTables are the iptables table values.
var kubeProxyTables = []string{"filter", "nat"}

// kubeProxyTrafficPolicies are the traffic_policy label values for no_local_endpoints.
var kubeProxyTrafficPolicies = []string{"external", "internal"}

// emitKubeProxy emits kube-proxy control-plane metrics for all nodes.
// Called by Tick when K8sMonitoring.ControlPlane.KubeProxy is true.
func emitKubeProxy(
	st *state.State,
	cluster string,
	nodes []fixture.Node,
	tickSec, scale float64,
) {
	for _, n := range nodes {
		// instance is the kube-proxy metrics endpoint: <privateIP>:10249
		instance := fmt.Sprintf("%s:10249", n.PrivateIP)
		base := merge(k8sBase(cluster), map[string]string{
			"job":      jobKubeProxy,
			"instance": instance,
		})

		// ── Histograms ────────────────────────────────────────────────────────────

		// Histograms fanned over ip_family (except conntrack_reconciler which has no ip_family).
		for _, ipf := range kubeProxyIPFamilies {
			ipLbls := merge(base, map[string]string{"ip_family": ipf})

			// 4 histograms carry ip_family.
			for _, h := range []struct {
				name string
				mean float64
			}{
				{"kubeproxy_sync_proxy_rules_duration_seconds", 0.05},
				{"kubeproxy_sync_full_proxy_rules_duration_seconds", 0.1},
				{"kubeproxy_sync_partial_proxy_rules_duration_seconds", 0.02},
				{"kubeproxy_network_programming_duration_seconds", 0.08},
			} {
				st.Observe(h.name, ipLbls, kubeProxyHistoBounds, statelib.LEBare, h.mean*(0.5+float64(fnv1a32(instance+h.name)%100)/100.0))
			}
		}

		// conntrack_reconciler_sync_duration_seconds — NO ip_family.
		st.Observe("kubeproxy_conntrack_reconciler_sync_duration_seconds", base, kubeProxyHistoBounds, statelib.LEBare, 0.03)

		// ── Gauges ────────────────────────────────────────────────────────────────

		// iptables_last and timestamp gauges are fanned over ip_family × table (where applicable).
		for _, ipf := range kubeProxyIPFamilies {
			ipLbls := merge(base, map[string]string{"ip_family": ipf})

			// sync_proxy_rules_last_timestamp_seconds and last_queued_timestamp_seconds.
			st.Set("kubeproxy_sync_proxy_rules_last_timestamp_seconds", ipLbls, 1_748_736_000)
			st.Set("kubeproxy_sync_proxy_rules_last_queued_timestamp_seconds", ipLbls, 1_748_736_001)

			for _, tbl := range kubeProxyTables {
				tblLbls := merge(ipLbls, map[string]string{"table": tbl})
				st.Set("kubeproxy_sync_proxy_rules_iptables_last", tblLbls, float64(10+len(tbl)))
			}
		}

		// Scalar change-pending gauges (no extra labels).
		st.Set("kubeproxy_sync_proxy_rules_endpoint_changes_pending", base, 0)
		st.Set("kubeproxy_sync_proxy_rules_service_changes_pending", base, 0)

		// ── Counters ──────────────────────────────────────────────────────────────

		for _, ipf := range kubeProxyIPFamilies {
			ipLbls := merge(base, map[string]string{"ip_family": ipf})

			// iptables_total fanned over ip_family × table.
			for _, tbl := range kubeProxyTables {
				tblLbls := merge(ipLbls, map[string]string{"table": tbl})
				st.Add("kubeproxy_sync_proxy_rules_iptables_total", tblLbls, scale)
			}

			// no_local_endpoints_total fanned over ip_family × traffic_policy.
			for _, tp := range kubeProxyTrafficPolicies {
				tpLbls := merge(ipLbls, map[string]string{"traffic_policy": tp})
				st.Add("kubeproxy_sync_proxy_rules_no_local_endpoints_total", tpLbls, 0)
			}
		}

		st.Add("kubeproxy_sync_proxy_rules_endpoint_changes_total", base, scale)
		st.Add("kubeproxy_sync_proxy_rules_service_changes_total", base, scale)
		st.Add("kubeproxy_conntrack_reconciler_deleted_entries_total", base, 0)
		st.Add("kubeproxy_iptables_ct_state_invalid_dropped_packets_total", base, 0)
		st.Add("kubeproxy_iptables_localhost_nodeports_accepted_packets_total", base, 0)

		// rest_client_requests_total — kube-proxy's API server client metrics.
		apiHost := kubeProxyAPIHost(instance)
		st.Add("rest_client_requests_total", merge(base, map[string]string{
			"code":   "200",
			"method": "GET",
			"host":   apiHost,
		}), scale)

		// kubernetes_build_info (value=1, same label shape as kubelet but under kube-proxy job).
		st.Set("kubernetes_build_info", merge(base, map[string]string{
			"build_date":     "2024-11-15T00:00:00Z",
			"compiler":       "gc",
			"git_commit":     "f69f56f",
			"git_tree_state": "clean",
			"git_version":    "v1.31.2-eks-f69f56f",
			"go_version":     "go1.22.8",
			"major":          "1",
			"minor":          "31",
			"platform":       "linux/amd64",
		}), 1)
	}
}

// kubeProxyAPIHost derives the kube-apiserver host label for rest_client_requests_total.
// Uses the standard EKS API endpoint form derived from the cluster CIDR.
func kubeProxyAPIHost(instance string) string {
	// Derive a synthetic API server host from the node instance address.
	// In real EKS the API host is the cluster endpoint (a regional DNS name).
	// Use a stable form: replace port with :443.
	if i := strings.LastIndex(instance, ":"); i >= 0 {
		return instance[:i] + ":443"
	}
	return instance + ":443"
}
