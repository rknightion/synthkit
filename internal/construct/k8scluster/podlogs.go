// SPDX-License-Identifier: AGPL-3.0-only

// podlogs.go — pod-log Loki streams for k8scluster (I15: low-card stream labels).
// Gated on Features["pod_logs"] and PodLogsMethod.
//
// Two shapes (one stream per pod per container, one line each):
//   - otel  (method=="opentelemetry" or ""):
//     stream labels carry k8s-native otel form (k8s_pod_name, log_iostream, etc.)
//   - classic (method=="kubernetes_api" or "loki"):
//     stream labels carry the classic Alloy kubernetes_api form (namespace, pod, container, job=<ns>/<container>)
//   - objects: return nil (deferred)
package k8scluster

import (
	"context"
	"fmt"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/sink/loki"
)

// emitPodLogs writes pod-log streams; returns nil and writes nothing when gated off.
func emitPodLogs(
	ctx context.Context,
	now time.Time,
	cluster string,
	cl *fixture.Cluster,
	w *core.World,
) error {
	streams := buildPodLogStreams(now, cluster, cl)
	if len(streams) == 0 {
		return nil
	}
	return w.Logs.Write(ctx, streams)
}

// buildPodLogStreams constructs pod-log Loki streams (pure, no I/O).
// Returns nil (empty) when the feature is off or method=="none"|"objects".
func buildPodLogStreams(now time.Time, cluster string, cl *fixture.Cluster) []loki.Stream {
	km := cl.K8sMonitoring
	if !km.Features["pod_logs"] {
		return nil
	}
	method := km.PodLogsMethod
	if method == "" {
		method = "opentelemetry"
	}
	switch method {
	case "none", "objects":
		return nil
	case "opentelemetry":
		return buildPodLogStreamsOtel(now, cluster, cl)
	case "kubernetes_api", "loki":
		return buildPodLogStreamsClassic(now, cluster, cl)
	default:
		// Unknown method — emit nothing (safe degradation).
		return nil
	}
}

// podLogBody returns a realistic log line body.
func podLogBody(now time.Time) string {
	return fmt.Sprintf("%s level=info msg=\"request handled\" path=/healthz status=200",
		now.UTC().Format(time.RFC3339))
}

// ── OTel shape ────────────────────────────────────────────────────────────────────────

// buildPodLogStreamsOtel emits the Alloy opentelemetry-collector pod-log shape:
// stream labels follow the OTel k8s semantic conventions.
func buildPodLogStreamsOtel(now time.Time, cluster string, cl *fixture.Cluster) []loki.Stream {
	wlByName := podWorkloadByName(cl)

	var out []loki.Stream
	nodes := cl.Nodes

	for ns, deploys := range workloadDeployments(cl) {
		for _, deploy := range deploys {
			fwl := wlByName[deploy]
			reps := 1
			if fwl != nil {
				reps = fwl.Replicas
				if reps < 1 {
					reps = 1
				}
			}
			container := deploy
			if fwl != nil && fwl.Container != "" {
				container = fwl.Container
			}

			for ri := 0; ri < reps; ri++ {
				var podName string
				if fwl != nil && ri < len(fwl.PodNames) {
					podName = fwl.PodNames[ri]
				} else {
					podName = synthPodName(deploy, ri)
				}
				var nodeName string
				if len(nodes) > 0 {
					var nodeIdx int
					if fwl != nil && ri < len(fwl.NodeIdx) {
						nodeIdx = fwl.NodeIdx[ri]
					} else {
						nodeIdx = ri % len(nodes)
					}
					nodeName = nodes[nodeIdx].Hostname
				}

				svcInstanceID := fmt.Sprintf("%s.%s.%s", ns, podName, container)

				out = append(out, loki.Stream{
					Labels: map[string]string{
						"cluster":                cluster,
						"k8s_cluster_name":       cluster,
						"k8s_namespace_name":     ns,
						"k8s_pod_name":           podName,
						"k8s_container_name":     container,
						"k8s_node_name":          nodeName,
						"k8s_deployment_name":    deploy,
						"app_kubernetes_io_name": deploy,
						"service_name":           deploy,
						"service_namespace":      ns,
						"service_instance_id":    svcInstanceID,
						"log_iostream":           "stdout",
						"logtag":                 "F",
						"detected_level":         "info",
					},
					Lines: []loki.Line{{T: now, Body: podLogBody(now)}},
				})
			}
		}
	}
	return out
}

// ── Classic shape ─────────────────────────────────────────────────────────────────────

// buildPodLogStreamsClassic emits the Alloy kubernetes_api (or "loki") pod-log shape:
// classic stream labels with job=<ns>/<container>.
func buildPodLogStreamsClassic(now time.Time, cluster string, cl *fixture.Cluster) []loki.Stream {
	wlByName := podWorkloadByName(cl)

	var out []loki.Stream

	for ns, deploys := range workloadDeployments(cl) {
		for _, deploy := range deploys {
			fwl := wlByName[deploy]
			reps := 1
			if fwl != nil {
				reps = fwl.Replicas
				if reps < 1 {
					reps = 1
				}
			}
			container := deploy
			if fwl != nil && fwl.Container != "" {
				container = fwl.Container
			}

			for ri := 0; ri < reps; ri++ {
				var podName string
				if fwl != nil && ri < len(fwl.PodNames) {
					podName = fwl.PodNames[ri]
				} else {
					podName = synthPodName(deploy, ri)
				}

				svcInstanceID := fmt.Sprintf("%s.%s.%s", ns, podName, container)

				meta := map[string]string{
					"k8s_pod_name":        podName,
					"pod":                 podName,
					"service_instance_id": svcInstanceID,
				}

				out = append(out, loki.Stream{
					Labels: map[string]string{
						"cluster":                cluster,
						"k8s_cluster_name":       cluster,
						"namespace":              ns,
						"pod":                    podName,
						"container":              container,
						"job":                    ns + "/" + container,
						"app_kubernetes_io_name": deploy,
						"service_name":           deploy,
						"service_namespace":      ns,
						"service_instance_id":    svcInstanceID,
						"stream":                 "stdout",
						"detected_level":         "info",
					},
					Lines: []loki.Line{{T: now, Body: podLogBody(now), Meta: meta}},
				})
			}
		}
	}
	return out
}
