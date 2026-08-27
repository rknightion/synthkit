// SPDX-License-Identifier: AGPL-3.0-only

// podlogs.go — pod-log emission for k8scluster, over EITHER of the two real transports.
// Gated on Features["pod_logs"] and PodLogsMethod.
//
// The chart splits pod logs into podLogsViaLoki and podLogsViaOpenTelemetry (k8s-monitoring 4.x),
// and PodLogsMethod is the blueprint-declared, cluster-level selector between them — cluster-level
// because the real chart flag is collector-level and flips every pod log on the cluster:
//
//   - "opentelemetry" (or ""): NATIVE OTLP transport (podLogsViaOpenTelemetry). The collector ships
//     ResourceLogs to /v1/logs with RESOURCE attributes (dotted OTel k8s/service names, plus the flat
//     `cluster` and `app_kubernetes_io_name` the chart emits) and RECORD attributes (log.iostream,
//     logtag). The DESTINATION owns the shape: Loki promotes an allowlisted subset of the resource
//     attributes to stream labels — sanitising dots to underscores, which is why a Loki query shows
//     `k8s_pod_name` for the `k8s.pod.name` sent here — and everything else, record attributes
//     included, lands as structured metadata.
//   - "kubernetes_api" / "loki": LOKI-NATIVE push (podLogsViaLoki), classic Alloy shape with stream
//     labels + structured metadata ON THE WIRE and job=<ns>/<container>.
//   - "none" / "objects": emit nothing ("objects" deferred).
//
// Both transports project the SAME []podLogEntry, so switching transport changes the observable
// shape and never adds or drops a log line (SKT-0006.05 AC #6).
//
// Contract: signals/k8s.md [slug: k8s-pod-logs], signals/logs.md.
package k8scluster

import (
	"context"
	"fmt"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/sink/loki"
	"github.com/rknightion/synthkit/internal/sink/otlp"
)

// Pod-log transport selectors (fixture.K8sMonitoring.PodLogsMethod values).
const (
	podLogsOTel    = "opentelemetry"  // native OTLP (podLogsViaOpenTelemetry)
	podLogsK8sAPI  = "kubernetes_api" // Loki-native, classic Alloy shape
	podLogsLoki    = "loki"           // Loki-native, classic Alloy shape
	podLogsNone    = "none"
	podLogsObjects = "objects" // deferred
)

// podLogsMethod normalises the declared selector. "" with the feature on means the chart default,
// which since k8s-monitoring 4.x is the OpenTelemetry path; the feature being off means "none".
func podLogsMethod(cl *fixture.Cluster) string {
	km := cl.K8sMonitoring
	if !km.Features["pod_logs"] {
		return podLogsNone
	}
	if km.PodLogsMethod == "" {
		return podLogsOTel
	}
	return km.PodLogsMethod
}

// podLogsOTLPNative reports whether this cluster's declared pod-log transport is native OTLP.
// Read by Signals() so the runner only wires the OTLPLogs lane for a cluster that declared it.
func podLogsOTLPNative(cl *fixture.Cluster) bool { return podLogsMethod(cl) == podLogsOTel }

// emitPodLogs writes pod logs over the declared transport; writes nothing when gated off.
func emitPodLogs(
	ctx context.Context,
	now time.Time,
	cluster string,
	cl *fixture.Cluster,
	w *core.World,
) error {
	if podLogsOTLPNative(cl) {
		resources := buildPodLogResources(now, cluster, cl)
		if len(resources) == 0 || w.OTLPLogs == nil {
			return nil
		}
		return w.OTLPLogs.Write(ctx, resources)
	}
	streams := buildPodLogStreams(now, cluster, cl)
	if len(streams) == 0 {
		return nil
	}
	return w.Logs.Write(ctx, streams)
}

// ── Transport-independent content ────────────────────────────────────────────────────

// podLogEntry is ONE pod-log line before any transport shaping. Empty fields are ABSENT
// dimensions and are omitted by both projections, never emitted as "" (I17).
//
// Deployment is the owning Deployment, empty when the pod has no Deployment owner. Node is the
// scheduling node, empty when the cluster declares no nodes. IOStream is stdout or stderr;
// LogTag is F for a full line, P for a partial one (the CRI log-format fields the filelog
// container parser lifts onto the record).
//
// Field comments are deliberately kept out of this struct: the blueprint-schema generator
// harvests per-field comments from every construct package, and this is a private render type,
// not a declarable blueprint surface.
type podLogEntry struct {
	Namespace         string
	Pod               string
	Container         string
	Deployment        string
	Node              string
	ServiceInstanceID string
	IOStream          string
	LogTag            string
	Body              string
	Time              time.Time
}

// buildPodLogEntries enumerates one line per pod×container for the whole cluster. Pure; the
// single content source both transports project from.
func buildPodLogEntries(now time.Time, cl *fixture.Cluster) []podLogEntry {
	wlByName := podWorkloadByName(cl)
	nodes := cl.Nodes
	body := podLogBody(now)

	var out []podLogEntry
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

				out = append(out, podLogEntry{
					Namespace:         ns,
					Pod:               podName,
					Container:         container,
					Deployment:        deploy,
					Node:              nodeName,
					ServiceInstanceID: fmt.Sprintf("%s.%s.%s", ns, podName, container),
					IOStream:          "stdout",
					LogTag:            "F",
					Body:              body,
					Time:              now,
				})
			}
		}
	}
	return out
}

// podLogBody returns a realistic log line body.
func podLogBody(now time.Time) string {
	return fmt.Sprintf("%s level=info msg=\"request handled\" path=/healthz status=200",
		now.UTC().Format(time.RFC3339))
}

// ── native OTLP transport (podLogsViaOpenTelemetry) ──────────────────────────────────

// buildPodLogResources projects the entries as OTLP ResourceLogs — one resource per pod×container,
// carrying one record. Attribute names are EXACTLY as captured at collector egress in
// reality-corpus/k8s/k3d-lab.json (k8s-monitoring 4.4.0, podLogsViaOpenTelemetry.enabled: true,
// 2026-08-25): dotted OTel names alongside the chart's flat `cluster` and `app_kubernetes_io_name`.
// The resource/record split follows the filelog `container` parser, which puts the k8s identity on
// the RESOURCE and log.iostream/logtag on the RECORD.
//
// SeverityNumber is deliberately left unspecified: the captured pipeline has no severity parser
// (the container parser sets none), and Loki derives detected_level from the body.
func buildPodLogResources(now time.Time, cluster string, cl *fixture.Cluster) []otlp.LogResource {
	entries := buildPodLogEntries(now, cl)
	if len(entries) == 0 {
		return nil
	}
	out := make([]otlp.LogResource, 0, len(entries))
	for _, e := range entries {
		attrs := map[string]any{
			"cluster":             cluster,
			"k8s.cluster.name":    cluster,
			"k8s.namespace.name":  e.Namespace,
			"k8s.pod.name":        e.Pod,
			"k8s.container.name":  e.Container,
			"service.instance.id": e.ServiceInstanceID,
			"service.namespace":   e.Namespace,
		}
		// Absent dimensions are omitted, never "" (I17). The corpus carries a pod with no
		// Deployment owner and no node attribute, so this asymmetry is real.
		if e.Deployment != "" {
			attrs["k8s.deployment.name"] = e.Deployment
			attrs["app_kubernetes_io_name"] = e.Deployment
			attrs["service.name"] = e.Deployment
		} else if e.Container != "" {
			// OTel k8s service-name resolution falls back to the container name.
			attrs["service.name"] = e.Container
		}
		if e.Node != "" {
			attrs["k8s.node.name"] = e.Node
		}

		out = append(out, otlp.LogResource{
			Attrs: attrs,
			Records: []otlp.LogRecord{{
				Time:         e.Time,
				ObservedTime: e.Time,
				Body:         e.Body,
				Attrs: map[string]any{
					"log.iostream": e.IOStream,
					"logtag":       e.LogTag,
				},
			}},
		})
	}
	return out
}

// ── Loki-native transport (podLogsViaLoki) ───────────────────────────────────────────

// buildPodLogStreams constructs Loki-native pod-log streams (pure, no I/O). Returns nil for the
// native OTLP transport and for "none"/"objects".
func buildPodLogStreams(now time.Time, cluster string, cl *fixture.Cluster) []loki.Stream {
	switch podLogsMethod(cl) {
	case podLogsK8sAPI, podLogsLoki:
		return buildPodLogStreamsClassic(now, cluster, cl)
	default:
		// native OTLP, "none", "objects", or an unknown method — nothing on the Loki lane.
		return nil
	}
}

// buildPodLogStreamsClassic emits the Alloy kubernetes_api (or "loki") pod-log shape:
// classic stream labels with job=<ns>/<container>.
func buildPodLogStreamsClassic(now time.Time, cluster string, cl *fixture.Cluster) []loki.Stream {
	entries := buildPodLogEntries(now, cl)
	out := make([]loki.Stream, 0, len(entries))
	for _, e := range entries {
		labels := map[string]string{
			"cluster":             cluster,
			"k8s_cluster_name":    cluster,
			"namespace":           e.Namespace,
			"pod":                 e.Pod,
			"container":           e.Container,
			"job":                 e.Namespace + "/" + e.Container,
			"service_namespace":   e.Namespace,
			"service_instance_id": e.ServiceInstanceID,
			"stream":              e.IOStream,
			"detected_level":      "info",
		}
		if e.Deployment != "" {
			labels["app_kubernetes_io_name"] = e.Deployment
			labels["service_name"] = e.Deployment
		}
		out = append(out, loki.Stream{
			Labels: labels,
			Lines: []loki.Line{{T: e.Time, Body: e.Body, Meta: map[string]string{
				"k8s_pod_name":        e.Pod,
				"pod":                 e.Pod,
				"service_instance_id": e.ServiceInstanceID,
			}}},
		})
	}
	return out
}
