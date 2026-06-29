// SPDX-License-Identifier: AGPL-3.0-only

// nodeexporter.go — node-exporter emission for k8scluster.
// job="integrations/node_exporter" (⚠ NO "kubernetes/" segment).
// instance = node hostname; DaemonSet pod labels on EVERY series.
//
// This file is now a THIN ADAPTER over the shared internal/nodeexp mechanic lib:
// it builds the k8s DaemonSet identity base (nodeExporterLabels) + a nodeexp.HostTopology
// per node (representative device set + per-node vCPU/mem + cluster OS identity), then
// delegates the value-generation physics to nodeexp.EmitLinux with nodeexp.ProfileK8s.
// The ProfileK8s keepset reproduces EXACTLY the node-exporter name set this file emitted
// before the migration (series-identity-preserving; values are representative).
//
// CARDINALITY DISCIPLINE (unchanged from the pre-migration source): we pass a
// REPRESENTATIVE device set — network {lo, eth0, eth1, eth2}; disk {nvme0n1, nvme1n1, dm-0} —
// to replicate the `device` label KEY faithfully while bounding series count (no eni* fan-out).
// Per-CPU metrics use the node's actual vCPU count (instance type), not a fixed 2.
package k8scluster

import (
	"strings"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/nodeexp"
	"github.com/rknightion/synthkit/internal/state"
)

// kubeletHistoBounds are the prom-client default seconds histogram bounds (LEBare style).
// Defined here (alongside the node lane) and consumed by kubelet.go.
var kubeletHistoBounds = []float64{
	0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10,
}

// ── Platform identity (derived from cluster.Platform — consistent with kube_node_info) ──
// osDetail is the node OS identity for node_uname_info + node_os_info. It expands the
// cluster's resolved Platform (blueprint-declared, defaulted) into the real field set:
// Bottlerocket carries variant_id/build_id; Amazon Linux omits them (absent ⇒ OMIT, I13).
type osDetail struct {
	id, name, pretty, version, versionID, variantID, buildID string
	kernelRelease, kernelVersion                             string
}

func osDetailFor(p fixture.Platform) osDetail {
	d := osDetail{kernelRelease: p.KernelVersion, kernelVersion: "#1 SMP PREEMPT_DYNAMIC"}
	switch p.OSID {
	case "bottlerocket":
		d.id, d.name, d.pretty = "bottlerocket", "Bottlerocket", p.OSImage
		d.version = "1.62.0 (aws-k8s-" + p.KubernetesVersion + ")"
		d.versionID, d.variantID, d.buildID = "1.62.0", "aws-k8s-"+p.KubernetesVersion, "49f1c7d2"
	default: // Amazon Linux (amzn): AL2 / AL2023
		d.id, d.name, d.pretty = "amzn", "Amazon Linux", p.OSImage
		if strings.Contains(p.OSImage, "2023") {
			d.version, d.versionID = "2023", "2023"
		} else {
			d.version, d.versionID = "2", "2"
		}
	}
	return d
}

// k8sNodeNICs is the REPRESENTATIVE network device set passed to nodeexp.EmitLinux.
// Real nodes add ~25 eni* + pod-id-link0; we omit those to bound cardinality but keep the
// `device` label key faithful. lo exposes no node_network_speed_bytes (SpeedBytes 0);
// eth* expose 25 GbE (matching the live capture).
var k8sNodeNICs = []nodeexp.NIC{
	{Name: "lo", SpeedBytes: 0},
	{Name: "eth0", SpeedBytes: 25_000_000_000},
	{Name: "eth1", SpeedBytes: 25_000_000_000},
	{Name: "eth2", SpeedBytes: 25_000_000_000},
}

// k8sNodeDisks is the REPRESENTATIVE disk device set passed to nodeexp.EmitLinux.
var k8sNodeDisks = []string{"nvme0n1", "nvme1n1", "dm-0"}

// nodeTopology builds the nodeexp.HostTopology for a k8s node, preserving the exact
// device/fs/os identity the pre-migration emitter used so series identity is unchanged.
func nodeTopology(n fixture.Node, osd osDetail) nodeexp.HostTopology {
	spec := fixture.LookupInstanceSpec(n.InstanceType)
	return nodeexp.HostTopology{
		Hostname: n.Hostname,
		NumCPU:   vcpusForNode(n),
		MemTotal: memBytesForNode(n),
		Disks:    k8sNodeDisks,
		NICs:     k8sNodeNICs,
		FS: nodeexp.FSMount{
			Device:     "/dev/nvme0n1p1",
			FSType:     "ext4",
			Mountpoint: "/",
			SizeBytes:  100.0 * 1024 * 1024 * 1024,
		},
		OS: nodeexp.OSInfo{
			ID:         osd.id,
			Name:       osd.name,
			PrettyName: osd.pretty,
			Version:    osd.version,
			VersionID:  osd.versionID,
			Kernel:     osd.kernelRelease,
			Machine:    spec.UnameMachine(),
			// Bottlerocket carries variant_id/build_id; Amazon Linux leaves them ""
			// (osDetailFor sets them only for OSID=="bottlerocket") → omitted by EmitLinux.
			VariantID: osd.variantID,
			BuildID:   osd.buildID,
		},
		BootTime: float64(clusterCreatedUnix),
	}
}

// emitNodeExporter delegates the node-exporter value physics to nodeexp.EmitLinux,
// once per node, under the k8s DaemonSet identity base and ProfileK8s keepset.
func emitNodeExporter(
	st *state.State,
	cluster string,
	cl *fixture.Cluster,
	nodes []fixture.Node,
	factor, tickSec, scale float64,
	w *core.World,
) {
	osd := osDetailFor(platformOr(cl.Platform))
	for ni, n := range nodes {
		base := nodeExporterLabels(cluster, n.Hostname, ni)
		top := nodeTopology(n, osd)
		nodeexp.EmitLinux(st, base, top, nodeexp.ProfileK8s, factor, tickSec, scale, w.Shape)
	}
}
