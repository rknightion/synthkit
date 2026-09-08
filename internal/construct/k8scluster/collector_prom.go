// SPDX-License-Identifier: AGPL-3.0-only

package k8scluster

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/sink/otlp"
	"github.com/rknightion/synthkit/internal/sink/promrw"
	"github.com/rknightion/synthkit/internal/state"
)

// projectCollectorProm applies the observed target-family projection. The process-wide
// metric sink still uses RW2; this reproduces labels, not the captured RW1 encoding.
func projectCollectorProm(batch []promrw.Series) []promrw.Series {
	out := make([]promrw.Series, 0, len(batch))
	for _, s := range batch {
		family, ok := collectorPromFamily(s.Name)
		if !ok {
			continue
		}
		keys := collectorPromLabels[family]
		switch s.Labels["job"] {
		case jobKSM, jobKubelet, jobCAdvisor, jobNodeExporter:
		default:
			continue
		}
		labels := make(map[string]string)
		for _, key := range strings.Fields(keys) {
			switch key {
			case "otel_scope_name":
				labels[key] = "github.com/open-telemetry/opentelemetry-collector-contrib/receiver/prometheusreceiver"
			case "otel_scope_version":
				labels[key] = "0.158.0"
			default:
				if value := s.Labels[key]; value != "" {
					labels[key] = value
				}
			}
		}
		s.Labels = labels
		out = append(out, s)
	}
	return out
}

// collectorPromFamily resolves the base family for the classic histogram
// components materialized by state.State. The retained P3 inventory records
// histogram roots (for example storage_operation_duration_seconds), while the
// RW2 batch carries the Prometheus _bucket/_sum/_count series names.
func collectorPromFamily(name string) (string, bool) {
	if _, ok := collectorPromLabels[name]; ok {
		return name, true
	}
	for _, suffix := range []string{"_bucket", "_count", "_sum"} {
		if strings.HasSuffix(name, suffix) {
			family := strings.TrimSuffix(name, suffix)
			if _, ok := collectorPromLabels[family]; ok {
				return family, true
			}
		}
	}
	return "", false
}

// emitCollectorPromTargetInfo adds the four target_info producers retained by
// the P3 capture. The capture elides target values, so these are deterministic
// target identities shaped from the same scrape endpoints as their producers.
func emitCollectorPromTargetInfo(st *state.State, cluster string, nodes []fixture.Node) {
	for ni, n := range nodes {
		node := n.Hostname
		address := n.PrivateIP
		if address == "" {
			address = nodeInternalIP(node)
		}

		// cAdvisor is scraped from the kubelet endpoint on each node.
		st.Set("target_info", merge(cadvisorLabels(cluster, node), map[string]string{
			"k8s_node_name":  node,
			"server_address": address,
			"server_port":    "10250",
			"url_scheme":     "https",
		}), 1)

		// KSM has one stable service target in the synthetic cluster.
		if ni == 0 {
			ksmPod := "kube-state-metrics-" + hex16(cluster)[:10]
			st.Set("target_info", merge(ksmLabels(cluster), map[string]string{
				"k8s_container_name":  "kube-state-metrics",
				"k8s_namespace_name":  "kube-system",
				"k8s_pod_name":        ksmPod,
				"k8s_pod_uid":         podUID(cluster, "kube-system", ksmPod),
				"k8s_replicaset_name": "kube-state-metrics",
				"server_address":      strings.TrimSuffix(ksmInstance, ":8080"),
				"server_port":         "8080",
				"url_scheme":          "http",
			}), 1)
		}

		// Kubelet is a node target rather than a pod target.
		st.Set("target_info", merge(kubeletLabels(cluster, node), map[string]string{
			"k8s_node_name":  node,
			"server_address": address,
			"server_port":    "10250",
			"url_scheme":     "https",
		}), 1)

		// node-exporter is a DaemonSet pod target.
		pod := nodeExporterPodName(ni)
		st.Set("target_info", merge(nodeExporterLabels(cluster, node, ni), map[string]string{
			"k8s_container_name": "node-exporter",
			"k8s_daemonset_name": nodeExporterDS,
			"k8s_namespace_name": "monitoring",
			"k8s_node_name":      node,
			"k8s_pod_name":       pod,
			"k8s_pod_uid":        podUID(cluster, "monitoring", pod),
			"server_address":     address,
			"server_port":        "9100",
			"url_scheme":         "http",
		}), 1)
	}
}

// emitCollectorPromLogs uses the P3 resource/record split documented in signals/k8s.md.
func emitCollectorPromLogs(ctx context.Context, now time.Time, cl *fixture.Cluster, w *core.World) error {
	if w.OTLPLogs == nil {
		return nil
	}
	var resources []otlp.LogResource
	for _, stream := range buildEventStreams(now, cl.Name, cl) {
		if stream.Labels["job"] != "integrations/kubernetes/eventhandler" {
			continue
		}
		attrs := map[string]any{"k8s.cluster.name": cl.Name, "service.name": "integrations/kubernetes/eventhandler"}
		if ns := stream.Labels["namespace"]; ns != "" {
			attrs["k8s.namespace.name"] = ns
		}
		var records []otlp.LogRecord
		for _, line := range stream.Lines {
			eventName := fmt.Sprintf("%s.%x", line.Meta["name"], deterministicRV(cl.Name, line.Meta["name"], stream.Labels["reason"]))
			records = append(records, otlp.LogRecord{ObservedTime: line.T,
				BodyMap: map[string]any{"type": "ADDED", "object": map[string]any{
					"apiVersion": "v1", "kind": "Event", "metadata": map[string]any{"name": eventName, "namespace": stream.Labels["namespace"]},
					"reason": stream.Labels["reason"], "message": line.Body,
				}},
				Attrs: map[string]any{"event.domain": "k8s", "event.name": eventName, "k8s.resource.name": "events"}})
		}
		resources = append(resources, otlp.LogResource{Attrs: attrs, Records: records})
	}
	if cl.K8sMonitoring.Features["pod_logs"] {
		workloads := podWorkloadByName(cl)
		for _, e := range buildPodLogEntries(now, cl) {
			uid := podUID(cl.Name, e.Namespace, e.Pod)
			if wl := workloads[e.Deployment]; wl != nil {
				for i, pod := range wl.PodNames {
					if pod == e.Pod {
						uid = resolvedPodUID(cl.Name, e.Namespace, e.Pod, wl, i)
						break
					}
				}
			}
			resources = append(resources, otlp.LogResource{Attrs: map[string]any{
				"k8s.cluster.name": cl.Name, "k8s.namespace.name": e.Namespace, "k8s.pod.name": e.Pod,
				"k8s.pod.uid": uid, "k8s.container.name": e.Container, "k8s.container.restart_count": int64(0),
			}, Records: []otlp.LogRecord{{Time: e.Time, ObservedTime: e.Time, Body: e.Body, Attrs: map[string]any{
				"log.file.path": fmt.Sprintf("/var/log/pods/%s_%s_%s/%s/0.log", e.Namespace, e.Pod, uid, e.Container),
				"log.iostream":  e.IOStream, "logtag": e.LogTag,
			}}}})
		}
	}
	return w.OTLPLogs.Write(ctx, resources)
}
