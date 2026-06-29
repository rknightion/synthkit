// SPDX-License-Identifier: AGPL-3.0-only

// events.go — k8s Loki event streams for k8scluster (extract §5).
// Emits two stream families per cluster:
//  1. eventhandler (job="integrations/kubernetes/eventhandler")
//     — logfmt body, kubelet events are Pod-kind only; Warning events are sparse.
//  2. manifests (job="integrations/kubernetes/manifests")
//     — JSON body, action vocab: manifest|created|deleted|modified (NOT "sync")
//
// Live contract (live reference, job=integrations/kubernetes/eventhandler, logfmt body):
//   - kubelet events have kind=Pod (NEVER Deployment+kubelet)
//   - level ∈ {Info, Warning}
//   - body carries objectRV/eventRV/reportingcontroller/reportinginstance/sourcehost
//   - name+node are STRUCTURED METADATA (loki.Line.Meta), NOT stream labels
//   - namespace omitted for cluster-scoped (Node) objects
package k8scluster

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/sink/loki"
)

// eventSpec describes one kubelet/controller event reason.
type eventSpec struct {
	reason, kind, level, controller string
	kubelet                         bool
	msg                             string
}

// eventSpecs is the canonical event vocabulary derived from a live reference capture.
var eventSpecs = []eventSpec{
	{"Scheduled", "Pod", "Info", "default-scheduler", false, "Successfully assigned pod to node"},
	{"Pulling", "Pod", "Info", "kubelet", true, "Pulling image"},
	{"Pulled", "Pod", "Info", "kubelet", true, "Successfully pulled image"},
	{"Created", "Pod", "Info", "kubelet", true, "Created container"},
	{"Started", "Pod", "Info", "kubelet", true, "Started container"},
	{"Killing", "Pod", "Info", "kubelet", true, "Stopping container"},
	{"BackOff", "Pod", "Warning", "kubelet", true, "Back-off restarting failed container"},
	{"FailedScheduling", "Pod", "Warning", "default-scheduler", false, "0/N nodes are available"},
	{"ScalingReplicaSet", "Deployment", "Info", "deployment-controller", false, "Scaled up replica set"},
}

// manifestKinds is the vocab for manifest stream k8s_kind labels.
var manifestKinds = []string{"Pod", "Deployment", "StatefulSet", "DaemonSet"}

// emitEvents writes eventhandler and manifests log streams for the cluster.
func emitEvents(
	ctx context.Context,
	now time.Time,
	cluster string,
	cl *fixture.Cluster,
	w *core.World,
) error {
	streams := buildEventStreams(now, cluster, cl)
	for _, st := range streams {
		if err := w.Logs.Write(ctx, []loki.Stream{st}); err != nil {
			return fmt.Errorf("k8s_cluster events: %w", err)
		}
	}
	return nil
}

// buildEventStreams constructs the full set of Loki streams (pure, no I/O — testable).
func buildEventStreams(now time.Time, cluster string, cl *fixture.Cluster) []loki.Stream {
	var out []loki.Stream

	// Index workloads for pod→node lookup (includes substrate addon/baseline pods).
	wlByName := podWorkloadByName(cl)

	// Namespaces in a STABLE order: workloadDeployments returns a map (random Go iteration), but the
	// sparse-Warning emission below keys on the FIRST namespace processed, and determinism (I12)
	// requires the same pod to carry the warning every run — so sort the keys. Also keeps the emitted
	// stream order stable for the -dump inventory.
	wlDeps := workloadDeployments(cl)
	nsKeys := make([]string, 0, len(wlDeps))
	for ns := range wlDeps {
		nsKeys = append(nsKeys, ns)
	}
	sort.Strings(nsKeys)

	// ── eventhandler: iterate workloads, emit per pod ─────────────────────────

	// Warning events (BackOff/FailedScheduling) are SPARSE: exactly one pod carries them, as in the
	// live capture. `warned` flips true only AFTER a warning is actually emitted, so the warning lands
	// on the first pod that EXISTS in sorted-namespace order — never consumed by an empty namespace
	// (kube-system has no Deployments when no addons are declared, which previously swallowed the
	// warning and left zero Warnings some runs — the flake fixed here).
	warned := false
	for _, ns := range nsKeys {
		deploys := wlDeps[ns]
		for _, deploy := range deploys {
			wl := wlByName[deploy]
			// Resolve pod names and node placements for this workload.
			type podEntry struct {
				name     string
				nodeHost string
			}
			var pods []podEntry
			if wl != nil && len(wl.PodNames) > 0 {
				for ri, pn := range wl.PodNames {
					var nodeHost string
					if ri < len(wl.NodeIdx) && wl.NodeIdx[ri] < len(cl.Nodes) {
						nodeHost = cl.Nodes[wl.NodeIdx[ri]].Hostname
					} else if len(cl.Nodes) > 0 {
						nodeHost = cl.Nodes[ri%len(cl.Nodes)].Hostname
					}
					pods = append(pods, podEntry{pn, nodeHost})
				}
			} else {
				// Substrate workloads (alloy, etc.) — synthesise a single pod.
				var nodeHost string
				if len(cl.Nodes) > 0 {
					nodeHost = cl.Nodes[0].Hostname
				}
				pods = append(pods, podEntry{synthPodName(deploy, 0), nodeHost})
			}

			for pi, pod := range pods {
				nodeHost := pod.nodeHost

				// This pod carries the sparse Warning specs iff none has yet AND it is a replica-0 pod.
				emitWarn := !warned && pi == 0

				for _, spec := range eventSpecs {
					if spec.level == "Warning" && !emitWarn {
						continue
					}

					objectRV := deterministicRV(cl.Name, pod.name, spec.reason)
					eventRV := objectRV + 1

					evtType := "Normal"
					if spec.level == "Warning" {
						evtType = "Warning"
					}

					body := fmt.Sprintf(
						"kind=%s objectAPIversion=v1 objectRV=%d eventRV=%d reportingcontroller=%s sourcecomponent=%s reason=%s type=%s count=1 msg=%q",
						spec.kind, objectRV, eventRV, spec.controller, spec.controller,
						spec.reason, evtType, spec.msg,
					)
					if spec.kubelet {
						body += fmt.Sprintf(" reportinginstance=%s sourcehost=%s", nodeHost, nodeHost)
					}

					evtLabels := map[string]string{
						"cluster":          cluster,
						"k8s_cluster_name": cluster,
						"job":              "integrations/kubernetes/eventhandler",
						"service_name":     "integrations/kubernetes/eventhandler",
						"source":           "kubernetes-events",
						"namespace":        ns,
						"reason":           spec.reason,
						"level":            spec.level,
					}

					meta := map[string]string{
						"name": pod.name,
					}
					if spec.kubelet && nodeHost != "" {
						meta["node"] = nodeHost
					}

					out = append(out, loki.Stream{
						Labels: evtLabels,
						Lines:  []loki.Line{{T: now, Body: body, Meta: meta}},
					})
				}
				if emitWarn {
					warned = true // sparse: only this one pod carries the Warning specs
				}
			}
		}
	}

	// ── manifests: emit Deployment + Pod per workload ─────────────────────────
	for _, ns := range nsKeys {
		deploys := wlDeps[ns]
		for _, deploy := range deploys {
			wl := wlByName[deploy]

			// Deployment manifest.
			mfLabelsDeployment := map[string]string{
				"cluster":            cluster,
				"k8s_cluster_name":   cluster,
				"job":                "integrations/kubernetes/manifests",
				"service_name":       "integrations/kubernetes/manifests",
				"action":             "manifest",
				"k8s_kind":           "Deployment",
				"k8s_namespace_name": ns,
			}
			deployMeta := map[string]string{"k8s_deployment_name": deploy}
			deployBody := fmt.Sprintf(
				`{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":%q,"namespace":%q}}`,
				deploy, ns,
			)
			out = append(out, loki.Stream{
				Labels: mfLabelsDeployment,
				Lines:  []loki.Line{{T: now, Body: deployBody, Meta: deployMeta}},
			})

			// Pod manifests (one per pod replica).
			var podNames []string
			if wl != nil && len(wl.PodNames) > 0 {
				podNames = wl.PodNames
			} else {
				podNames = []string{synthPodName(deploy, 0)}
			}
			for _, pn := range podNames {
				mfLabelsPod := map[string]string{
					"cluster":            cluster,
					"k8s_cluster_name":   cluster,
					"job":                "integrations/kubernetes/manifests",
					"service_name":       "integrations/kubernetes/manifests",
					"action":             "manifest",
					"k8s_kind":           "Pod",
					"k8s_namespace_name": ns,
				}
				podMeta := map[string]string{"k8s_pod_name": pn}
				podBody := fmt.Sprintf(
					`{"apiVersion":"v1","kind":"Pod","metadata":{"name":%q,"namespace":%q}}`,
					pn, ns,
				)
				out = append(out, loki.Stream{
					Labels: mfLabelsPod,
					Lines:  []loki.Line{{T: now, Body: podBody, Meta: podMeta}},
				})
			}
		}
	}

	return out
}

// deterministicRV derives a deterministic uint32 resource-version-like value from
// the cluster seed, pod name, and event reason. Uses the first 4 bytes of the sha256 hash.
func deterministicRV(seed, pod, reason string) uint32 {
	h := fixture.Sum(seed, "evt", pod, reason)
	var raw uint32
	for i := 0; i < 4; i++ {
		var b byte
		hi := evtHexNibble(h[i*2])
		lo := evtHexNibble(h[i*2+1])
		b = hi<<4 | lo
		raw = raw<<8 | uint32(b)
	}
	return raw
}

// evtHexNibble converts a hex ASCII char to its 4-bit value.
func evtHexNibble(c byte) byte {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	default:
		return 0
	}
}
