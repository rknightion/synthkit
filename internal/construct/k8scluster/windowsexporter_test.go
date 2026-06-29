// SPDX-License-Identifier: AGPL-3.0-only

// windowsexporter_test.go — tests for the windows-exporter emitter + OS-aware kube_node_labels.
package k8scluster

import (
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/state"
)

// windowsTestState creates a fresh state and calls emitWindowsExporter into it.
func windowsTestState(nodes []fixture.Node, cluster string) *state.State {
	st := state.NewState()
	cl := &fixture.Cluster{Name: cluster}
	emitWindowsExporter(st, cluster, cl, nodes, 0.5, 60, 2, nil)
	return st
}

// collectAll drains the state into a slice (uses a synthetic now).
func collectAll(st *state.State) map[string][]map[string]string {
	now := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)
	series := st.Collect(now)
	// name → list of label sets
	out := make(map[string][]map[string]string)
	for _, s := range series {
		out[s.Name] = append(out[s.Name], s.Labels)
	}
	return out
}

// TestWindowsExporterEmitsForWindowsNodes verifies that emitWindowsExporter produces
// windows_cpu_time_total with job="integrations/windows-exporter" for a windows node
// and ZERO windows_* series for a pure-linux cluster.
func TestWindowsExporterEmitsForWindowsNodes(t *testing.T) {
	cluster := "test-prod-use1"

	// Build a mixed cluster: two linux nodes + one windows node.
	linuxNodes := []fixture.Node{
		{Hostname: "ip-10-0-0-1.us-east-1.compute.internal", OS: "linux"},
		{Hostname: "ip-10-0-0-2.us-east-1.compute.internal", OS: ""},
	}
	winNode := fixture.Node{Hostname: "win-abc123", OS: "windows"}
	mixedNodes := append(linuxNodes, winNode)

	// ── mixed cluster: expect windows series ──────────────────────────────────────────
	stMixed := state.NewState()
	cl := &fixture.Cluster{Name: cluster}
	emitWindowsExporter(stMixed, cluster, cl, mixedNodes, 0.5, 60, 2, nil)
	seriesMixed := collectAll(stMixed)

	cpuSeries, ok := seriesMixed["windows_cpu_time_total"]
	if !ok || len(cpuSeries) == 0 {
		t.Fatal("mixed cluster: windows_cpu_time_total not emitted — should be present for windows node")
	}

	// Every windows_cpu_time_total series must carry job="integrations/windows-exporter".
	for _, lbls := range cpuSeries {
		if lbls["job"] != "integrations/windows-exporter" {
			t.Errorf("windows_cpu_time_total: job=%q, want integrations/windows-exporter", lbls["job"])
		}
	}

	// windows_cpu_time_total must NOT carry a namespace label (windows path is simpler).
	for _, lbls := range cpuSeries {
		if _, hasNS := lbls["namespace"]; hasNS {
			t.Error("windows_cpu_time_total: must NOT have a namespace label")
		}
	}

	// instance must be the windows node hostname.
	foundWinInstance := false
	for _, lbls := range cpuSeries {
		if lbls["instance"] == winNode.Hostname {
			foundWinInstance = true
			break
		}
	}
	if !foundWinInstance {
		t.Errorf("windows_cpu_time_total: no series with instance=%q", winNode.Hostname)
	}

	// core labels must be present (cluster + k8s_cluster_name + job + instance).
	for _, lbls := range cpuSeries {
		for _, k := range []string{"cluster", "k8s_cluster_name", "job", "instance"} {
			if lbls[k] == "" {
				t.Errorf("windows_cpu_time_total: label %q is absent or empty", k)
			}
		}
	}

	// Linux nodes must NOT appear in windows_cpu_time_total.
	for _, lbls := range cpuSeries {
		for _, ln := range linuxNodes {
			if lbls["instance"] == ln.Hostname {
				t.Errorf("windows_cpu_time_total: linux node %q must not appear in windows series", ln.Hostname)
			}
		}
	}

	// ── pure-linux cluster: expect ZERO windows series ────────────────────────────────
	stLinux := state.NewState()
	emitWindowsExporter(stLinux, cluster, cl, linuxNodes, 0.5, 60, 2, nil)
	seriesLinux := collectAll(stLinux)

	for name := range seriesLinux {
		if len(name) >= 8 && name[:8] == "windows_" {
			t.Errorf("pure-linux cluster: unexpected windows series %q", name)
		}
	}
}

// TestWindowsExporterExpectedFamilies verifies several representative metric families
// are emitted for a windows node, with expected labels.
func TestWindowsExporterExpectedFamilies(t *testing.T) {
	cluster := "c1"
	nodes := []fixture.Node{{Hostname: "win-node-1", OS: "windows"}}

	st := state.NewState()
	cl := &fixture.Cluster{Name: cluster}
	emitWindowsExporter(st, cluster, cl, nodes, 0.5, 60, 2, nil)
	collected := collectAll(st)

	// Live-verified family set (default k8s-monitoring collectors cpu/logical_disk/memory/net/os —
	// NO cs/system collectors, so no windows_cs_*/windows_system_*; windows_os_info not visible_memory).
	wantGauges := []string{
		"windows_cpu_logical_processor",
		"windows_memory_physical_total_bytes",
		"windows_memory_available_bytes",
		"windows_memory_committed_bytes",
		"windows_os_info",
	}
	for _, nm := range wantGauges {
		if _, ok := collected[nm]; !ok {
			t.Errorf("missing gauge series %q", nm)
		}
	}
	// These were emitted before but do NOT exist in the real default deployment — must be gone.
	for _, nm := range []string{"windows_cs_logical_processors", "windows_cs_physical_memory_bytes", "windows_os_visible_memory_bytes", "windows_system_processes", "windows_system_context_switches_total"} {
		if _, ok := collected[nm]; ok {
			t.Errorf("%q must NOT be emitted (collector not enabled by default k8s-monitoring)", nm)
		}
	}

	wantCounters := []string{
		"windows_cpu_time_total",
		"windows_net_bytes_received_total",
	}
	for _, nm := range wantCounters {
		if _, ok := collected[nm]; !ok {
			t.Errorf("missing counter series %q", nm)
		}
	}

	// windows_logical_disk_read_bytes_total should carry a volume label.
	if vols, ok := collected["windows_logical_disk_read_bytes_total"]; !ok {
		t.Error("missing windows_logical_disk_read_bytes_total")
	} else {
		for _, lbls := range vols {
			if lbls["volume"] == "" {
				t.Error("windows_logical_disk_read_bytes_total: volume label absent or empty")
			}
		}
	}

	// windows_net_bytes_received_total should carry a nic label.
	if nics, ok := collected["windows_net_bytes_received_total"]; !ok {
		t.Error("missing windows_net_bytes_received_total")
	} else {
		for _, lbls := range nics {
			if lbls["nic"] == "" {
				t.Error("windows_net_bytes_received_total: nic label absent or empty")
			}
		}
	}

	// windows_cpu_time_total should carry core and mode labels.
	for _, lbls := range collected["windows_cpu_time_total"] {
		if lbls["core"] == "" {
			t.Error("windows_cpu_time_total: core label absent or empty")
		}
		if lbls["mode"] == "" {
			t.Error("windows_cpu_time_total: mode label absent or empty")
		}
	}
}

// TestKSMNodeLabelsOSAware verifies that emitKSMNodeObjects stamps the correct
// label_kubernetes_io_os for windows and linux nodes, respectively.
func TestKSMNodeLabelsOSAware(t *testing.T) {
	cluster := "test-prod-use1"
	cl := coretest.Cluster()
	cl.Name = cluster

	// Override nodes: one linux, one windows (use explicit OS fields).
	linuxNode := cl.Nodes[0]
	linuxNode.OS = "linux"
	winNode := cl.Nodes[1]
	winNode.OS = "windows"
	emptyNode := cl.Nodes[2]
	emptyNode.OS = "" // should default to linux

	nodes := []fixture.Node{linuxNode, winNode, emptyNode}

	st := state.NewState()
	emitKSMNodeObjects(st, cluster, cl, nodes, 0.5)
	collected := collectAll(st)

	knsLabels, ok := collected["kube_node_labels"]
	if !ok || len(knsLabels) == 0 {
		t.Fatal("kube_node_labels not emitted")
	}

	osByNode := map[string]string{}
	for _, lbls := range knsLabels {
		osByNode[lbls["node"]] = lbls["label_kubernetes_io_os"]
	}

	if got := osByNode[linuxNode.Hostname]; got != "linux" {
		t.Errorf("linux node %q: label_kubernetes_io_os=%q, want linux", linuxNode.Hostname, got)
	}
	if got := osByNode[winNode.Hostname]; got != "windows" {
		t.Errorf("windows node %q: label_kubernetes_io_os=%q, want windows", winNode.Hostname, got)
	}
	if got := osByNode[emptyNode.Hostname]; got != "linux" {
		t.Errorf("empty-OS node %q: label_kubernetes_io_os=%q, want linux (default)", emptyNode.Hostname, got)
	}
}
