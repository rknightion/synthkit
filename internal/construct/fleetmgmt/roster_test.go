// SPDX-License-Identifier: AGPL-3.0-only

package fleetmgmt

import "testing"

// TestK8sCollectorLocalAttributes verifies LocalAttributes for k8s-sourced collectors
// (new fidelity fields) and standalone collectors (I13 omit-empty).
//
// NOTE: The plan placed this test in internal/fleet/fleet_test.go but that file is owned
// by another lane. Per the lane instructions, an equivalent test lives here and calls
// Collector{...}.LocalAttributes() directly (Collector is defined in this package).
func TestK8sCollectorLocalAttributes(t *testing.T) {
	// A k8s-mirror collector reproduces the chart's remotecfg attributes block verbatim
	// (camelCase workloadName/workloadType; workloadType = controller type, NOT the metric
	// "replicaset" projection) plus the two reserved Alloy system attributes.
	col := Collector{
		ID: "id-x", OS: "linux", Cluster: "c1", Version: "v1.10.0", Namespace: "monitoring",
		App: "alloy-singleton", Workload: "grafana-k8s-monitoring-alloy-singleton",
		Controller: "deployment", Pod: "grafana-k8s-monitoring-alloy-singleton-abc-12345",
		Release: "grafana-k8s-monitoring", ChartVersion: "4.1.5",
	}
	a := col.LocalAttributes()
	want := map[string]string{
		"collector.os":      "linux",
		"collector.version": "v1.10.0",
		"cluster":           "c1",
		"namespace":         "monitoring",
		"platform":          "kubernetes",
		"source":            "k8s-monitoring",
		"sourceVersion":     "4.1.5",
		"release":           "grafana-k8s-monitoring",
		"workloadName":      "alloy-singleton",
		"workloadType":      "deployment", // controller type, not the metric "replicaset"
	}
	for k, v := range want {
		if a[k] != v {
			t.Errorf("attr[%q] = %q, want %q", k, a[k], v)
		}
	}

	// A standalone collector (no k8s fields) must still omit empties (I13) and default version.
	std := Collector{ID: "s", OS: "windows"}
	sa := std.LocalAttributes()
	if sa["collector.os"] != "windows" {
		t.Errorf("std collector.os = %q", sa["collector.os"])
	}
	if v := sa["collector.version"]; v == "" || v[0] != 'v' {
		t.Errorf("std collector.version = %q, want v-prefixed default", v)
	}
	for _, k := range []string{"namespace", "platform", "source", "sourceVersion", "release", "workloadName", "workloadType", "cluster"} {
		if _, has := sa[k]; has {
			t.Errorf("std collector must omit %q (I13), got %q", k, sa[k])
		}
	}
}
