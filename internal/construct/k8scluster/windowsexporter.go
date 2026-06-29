// SPDX-License-Identifier: AGPL-3.0-only

// windowsexporter.go — windows_exporter metric families for Windows nodes.
// job="integrations/windows-exporter" (hyphenated — load-bearing).
// instance = node hostname; ambient labels {cluster, k8s_cluster_name, source, job, instance} ONLY.
// NO pod/namespace/container/workload labels (the host-metrics windows path is simpler than Linux).
//
// This file is now a THIN ADAPTER over the shared internal/nodeexp mechanic lib: it filters to
// Windows nodes, builds the windows-exporter identity base + a nodeexp.HostTopology (2 cores,
// C: volume, the AWS ENA NIC, Windows Server 2022 OS identity — matching the pre-migration source),
// and delegates the value physics to nodeexp.EmitWindows with nodeexp.ProfileK8s. The ProfileK8s
// windows keepset reproduces EXACTLY the windows name set this file emitted before the migration
// (series-identity-preserving; values are representative — note the lib also corrects the
// windows_logical_disk_free_bytes magnitude bug, a value change only).
//
// ✅ LIVE-CAPTURED 2026-06-15 from a real k8s-monitoring windows-exporter on a Windows Server 2022
// EKS node (reference cluster): default enabled collectors cpu,container,logical_disk,memory,net,os — so the
// cs/system collectors are NOT enabled and their families do NOT exist; windows_os_* exposes only
// windows_os_info / windows_os_hostname. The k8s windows keepset encodes exactly that set.
package k8scluster

import (
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/nodeexp"
	"github.com/rknightion/synthkit/internal/state"
)

const jobWindowsExporter = "integrations/windows-exporter"

// windowsNodeTopology builds the nodeexp.HostTopology for a Windows k8s node, preserving the
// pre-migration source's representative shape: 2 logical cores, one C: volume, the AWS Elastic
// Network Adapter NIC, ~8 GiB RAM, and the live-captured Windows Server 2022 OS identity.
func windowsNodeTopology(hostname string) nodeexp.HostTopology {
	return nodeexp.HostTopology{
		Hostname: hostname,
		NumCPU:   2, // source hardcodes 2 logical cores
		MemTotal: 8.0 * 1024 * 1024 * 1024,
		Disks:    []string{"C:"},
		NICs:     []nodeexp.NIC{{Name: "Amazon Elastic Network Adapter", SpeedBytes: 25_000_000_000}},
		OS: nodeexp.OSInfo{
			Product:      "Windows Server 2022 Datacenter",
			Version:      "10.0.20348",
			MajorVersion: "10",
			MinorVersion: "0",
			Build:        "20348",
			Revision:     "5139",
		},
	}
}

// emitWindowsExporter emits windows_exporter metrics for Windows nodes in the cluster by
// delegating to nodeexp.EmitWindows. Only nodes where n.OS == "windows" are processed; if none,
// this is a no-op. Retains the original signature for call-site + test parity (w may be nil; the
// windows physics are deterministic and do not consume the shape engine).
func emitWindowsExporter(st *state.State, cluster string, cl *fixture.Cluster, nodes []fixture.Node, factor, tickSec, scale float64, w *core.World) {
	_ = cl // not used (identity comes from cluster + hostname)
	_ = w  // not used — EmitWindows takes no shape noise; pass nil below

	for _, n := range nodes {
		if n.OS != "windows" {
			continue
		}
		base := map[string]string{
			"cluster":          cluster,
			"k8s_cluster_name": cluster,
			"source":           k8sSource,
			"job":              jobWindowsExporter,
			"instance":         n.Hostname,
		}
		top := windowsNodeTopology(n.Hostname)
		nodeexp.EmitWindows(st, base, top, nodeexp.ProfileK8s, factor, tickSec, scale, nil)
	}
}
