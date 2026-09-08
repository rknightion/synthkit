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
)

// projectCollectorProm applies the observed target-family projection. The process-wide
// metric sink still uses RW2; this reproduces labels, not the captured RW1 encoding.
func projectCollectorProm(batch []promrw.Series) []promrw.Series {
	out := make([]promrw.Series, 0, len(batch))
	for _, s := range batch {
		keys, ok := collectorPromLabels[s.Name]
		if !ok {
			continue
		}
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
