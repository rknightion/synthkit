// SPDX-License-Identifier: AGPL-3.0-only

// cadvisor.go — cAdvisor emission for k8scluster.
// job="integrations/kubernetes/cadvisor", instance=node hostname.
//
// This file is now a THIN ADAPTER over the shared internal/nodeexp mechanic lib:
// it keeps the k8s per-pod iteration + the cadvisorLabels identity base, builds a
// nodeexp.Container per pod (container-scoped Labels {id,image,name,pod,namespace,node,container}
// + POD-scoped NetLabels {id,image,name,pod,namespace,node,interface} — NO container, WITH
// interface), and delegates the value physics to nodeexp.EmitContainer with nodeexp.CadvisorK8s.
// Per-node machine_memory_bytes is emitted via nodeexp.EmitMachine (base carries node=hostname).
// The CadvisorK8s keepset reproduces EXACTLY the cadvisor name set this file emitted before the
// migration (series-identity-preserving; values are representative).
//
// CPU: cpu="total" (aggregate) added by the lib onto the container series.
// Network: pod-scoped (NO container label), interface="eth0" (driven by NetLabels).
// Memory: instantaneous gauges. machine_memory_bytes carries node=<hostname>.
package k8scluster

import (
	"fmt"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/nodeexp"
	"github.com/rknightion/synthkit/internal/state"
)

func emitCAdvisor(
	st *state.State,
	cluster string,
	cl *fixture.Cluster,
	nodes []fixture.Node,
	replicas int,
	factor, tickSec, scale float64,
	now time.Time,
	w *core.World,
) {
	_ = now

	wlByName := podWorkloadByName(cl)

	for ns, deploys := range workloadDeployments(cl) {
		for _, deploy := range deploys {
			fwl := wlByName[deploy]
			reps := substrateReps(fwl, replicas, len(nodes))

			for ri := 0; ri < reps; ri++ {
				var pod string
				var nodeIdx int
				if fwl != nil && ri < len(fwl.PodNames) {
					pod = fwl.PodNames[ri]
				} else {
					pod = synthPodName(deploy, ri)
				}
				if fwl != nil && ri < len(fwl.NodeIdx) {
					nodeIdx = fwl.NodeIdx[ri]
				} else {
					nodeIdx = nodeAssignment(ns, deploy, ri, len(nodes))
				}
				if nodeIdx >= len(nodes) {
					nodeIdx = 0
				}

				node := nodes[nodeIdx].Hostname
				container := deploy
				if fwl != nil && fwl.Container != "" {
					container = fwl.Container
				}
				image := imageRepo(deploy) + ":latest"
				cgroupID := fmt.Sprintf("/kubepods/burstable/pod%s/%s", podUID(cluster, ns, pod)[:8], container[:4])
				rtName := fmt.Sprintf("%012x", nodeAssignment(ns, pod, ri, 0x7fffffff))

				base := cadvisorLabels(cluster, node)

				// Container-scoped labels (cpu/mem/fs series). cpu="total" + device are
				// added by the lib; the {container,id,image,name,pod,namespace,node} identity
				// is supplied here.
				ctrLabels := map[string]string{
					"container": container,
					"id":        cgroupID,
					"image":     image,
					"name":      rtName,
					"namespace": ns,
					"node":      node,
					"pod":       pod,
				}
				// Network labels (POD-scoped): NO container label, WITH interface="eth0".
				netLabels := map[string]string{
					"id":        cgroupID,
					"image":     image,
					"interface": "eth0",
					"name":      rtName,
					"namespace": ns,
					"node":      node,
					"pod":       pod,
				}

				c := nodeexp.Container{
					CPURequest: resolveCPUUsageBase(fwl, deploy),
					MemLimit:   resolveMemLimit(fwl, deploy),
					Labels:     ctrLabels,
					NetLabels:  netLabels,
				}
				nodeexp.EmitContainer(st, base, c, nodeexp.CadvisorK8s, factor, tickSec, scale, w.Shape)
			}
		}
	}

	// machine_memory_bytes (per node) — base carries node=<hostname> (CadvisorK8s).
	for _, n := range nodes {
		mbase := merge(cadvisorLabels(cluster, n.Hostname), map[string]string{"node": n.Hostname})
		nodeexp.EmitMachine(st, mbase, memBytesForNode(n), nodeexp.CadvisorK8s)
	}
}
