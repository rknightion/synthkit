// SPDX-License-Identifier: AGPL-3.0-only

package k8scluster

import (
	"strings"

	"github.com/rknightion/synthkit/internal/sink/promrw"
)

// operatorRemoteWriteFamilies is the complete intersection of the 2026-09-04
// Prometheus Operator capture and the existing k8s_cluster catalogue. It is deliberately
// an explicit per-family map: do not widen this envelope from a job, prefix, or one-family
// predicate. Capture evidence: e2e/lab/captures/prom-operator-rw-f09d3e977594254c.md.
var operatorRemoteWriteFamilies = map[string]struct{}{
	"apiserver_current_inflight_requests":                           {},
	"apiserver_request_duration_seconds":                            {},
	"apiserver_request_total":                                       {},
	"kubelet_cgroup_manager_duration_seconds":                       {},
	"kubelet_node_name":                                             {},
	"kubelet_pleg_relist_duration_seconds":                          {},
	"kubelet_pleg_relist_interval_seconds":                          {},
	"kubelet_pod_start_duration_seconds":                            {},
	"kubelet_pod_worker_duration_seconds":                           {},
	"kubelet_running_containers":                                    {},
	"kubelet_running_pods":                                          {},
	"kubelet_runtime_operations_errors_total":                       {},
	"kubelet_runtime_operations_total":                              {},
	"kubeproxy_conntrack_reconciler_deleted_entries_total":          {},
	"kubeproxy_conntrack_reconciler_sync_duration_seconds":          {},
	"kubeproxy_iptables_ct_state_invalid_dropped_packets_total":     {},
	"kubeproxy_iptables_localhost_nodeports_accepted_packets_total": {},
	"kubeproxy_network_programming_duration_seconds":                {},
	"kubeproxy_sync_full_proxy_rules_duration_seconds":              {},
	"kubeproxy_sync_partial_proxy_rules_duration_seconds":           {},
	"kubeproxy_sync_proxy_rules_duration_seconds":                   {},
	"kubeproxy_sync_proxy_rules_endpoint_changes_pending":           {},
	"kubeproxy_sync_proxy_rules_endpoint_changes_total":             {},
	"kubeproxy_sync_proxy_rules_iptables_last":                      {},
	"kubeproxy_sync_proxy_rules_iptables_total":                     {},
	"kubeproxy_sync_proxy_rules_last_queued_timestamp_seconds":      {},
	"kubeproxy_sync_proxy_rules_last_timestamp_seconds":             {},
	"kubeproxy_sync_proxy_rules_no_local_endpoints_total":           {},
	"kubeproxy_sync_proxy_rules_service_changes_pending":            {},
	"kubeproxy_sync_proxy_rules_service_changes_total":              {},
}

func confPrometheusOperatorRemoteWrite(conf *Config) *PrometheusOperatorRemoteWrite {
	if conf == nil || conf.PrometheusOperatorRemoteWrite == nil {
		return nil
	}
	copy := *conf.PrometheusOperatorRemoteWrite
	return &copy
}

func (c *Construct) projectOperatorRemoteWrite(batch []promrw.Series) []promrw.Series {
	if c.operatorRemoteWrite == nil {
		return batch
	}
	out := make([]promrw.Series, 0, len(batch))
	for _, series := range batch {
		if !operatorRemoteWriteFamily(series.Name) {
			out = append(out, series)
			continue
		}
		labels := make(map[string]string, len(series.Labels)+2)
		for key, value := range series.Labels {
			labels[key] = value
		}
		// These values are the capture's reviewed per-family envelope. They are not
		// inferred from the existing Alloy source jobs.
		labels["job"] = "apiserver"
		labels["service"] = "kubernetes"
		labels["prometheus"] = c.operatorRemoteWrite.Prometheus
		labels["prometheus_replica"] = c.operatorRemoteWrite.PrometheusReplica
		series.Labels = labels
		out = append(out, series)
	}
	return out
}

// operatorRemoteWriteFamily treats the three remote-write series that represent one
// captured histogram family as that family. The capture inventory stores the histogram
// root name with `le`; the synthetic state emits the corresponding bucket, sum, and
// count series. No other suffix or family is projected.
func operatorRemoteWriteFamily(name string) bool {
	if _, observed := operatorRemoteWriteFamilies[name]; observed {
		return true
	}
	for _, suffix := range []string{"_bucket", "_sum", "_count"} {
		if strings.HasSuffix(name, suffix) {
			_, observed := operatorRemoteWriteFamilies[strings.TrimSuffix(name, suffix)]
			return observed
		}
	}
	return false
}
