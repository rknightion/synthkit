// SPDX-License-Identifier: AGPL-3.0-only

package fleetmgmt

import (
	"strings"
	"testing"

	"github.com/rknightion/synthkit/internal/fixture"
)

func TestEnabledRolesFromFeatures(t *testing.T) {
	cases := []struct {
		name   string
		feat   map[string]bool
		recvDS bool
		want   []roleSpec // compared by name+workload
	}{
		{
			name: "logs+metrics only",
			feat: map[string]bool{"pod_logs": true, "cluster_metrics": true},
			want: []roleSpec{
				{name: "alloy-logs", workload: "daemonset", perNode: true},
				{name: "alloy-metrics", workload: "statefulset", perNode: false},
			},
		},
		{
			name: "node_logs alone still yields one logs collector",
			feat: map[string]bool{"node_logs": true},
			want: []roleSpec{{name: "alloy-logs", workload: "daemonset", perNode: true}},
		},
		{
			name: "profiling + events + appo11y",
			feat: map[string]bool{"profiling": true, "cluster_events": true, "application_observability": true},
			want: []roleSpec{
				{name: "alloy-profiles", workload: "daemonset", perNode: true},
				{name: "alloy-receiver", workload: "deployment", perNode: false},
				{name: "alloy-singleton", workload: "deployment", perNode: false},
			},
		},
		{
			name:   "receiver as daemonset",
			feat:   map[string]bool{"application_observability": true},
			recvDS: true,
			want:   []roleSpec{{name: "alloy-receiver", workload: "daemonset", perNode: true}},
		},
		{name: "no features", feat: map[string]bool{}, want: nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := enabledRoles(tc.feat, tc.recvDS)
			if len(got) != len(tc.want) {
				t.Fatalf("len(got)=%d want %d (%+v)", len(got), len(tc.want), got)
			}
			seen := map[string]roleSpec{}
			for _, r := range got {
				seen[r.name] = r
			}
			for _, w := range tc.want {
				g, ok := seen[w.name]
				if !ok {
					t.Fatalf("missing role %q in %+v", w.name, got)
				}
				if g.workload != w.workload || g.perNode != w.perNode {
					t.Errorf("role %q = %+v, want %+v", w.name, g, w)
				}
			}
		})
	}
}

func TestK8sRosterIdentityFormat(t *testing.T) {
	seed := "seed1"
	nodes := []fixture.Node{
		{Hostname: "ip-10-0-1-23.us-west-2.compute.internal"},
		{Hostname: "ip-10-0-1-24.us-west-2.compute.internal"},
	}
	km := fixture.K8sMonitoring{
		Enabled: true, Alloy: true, AlloyVersion: "v1.10.0",
		Features:        map[string]bool{"pod_logs": true, "cluster_metrics": true, "cluster_events": true},
		MetricsReplicas: 2,
	}
	cols := K8sRoster(seed, "grafana-k8s-monitoring", "demo-prod-usw2", "monitoring", nodes, km)

	by := map[string]Collector{}
	for _, c := range cols {
		by[c.ID] = c
	}

	// DaemonSet: one alloy-logs per node, id ends in the node hostname.
	wantLogs := "grafana-k8s-monitoring-demo-prod-usw2-monitoring-alloy-logs-ip-10-0-1-23.us-west-2.compute.internal"
	lc, ok := by[wantLogs]
	if !ok {
		t.Fatalf("missing daemonset id %q in %v", wantLogs, keys(by))
	}
	if lc.Controller != "daemonset" || lc.App != "alloy-logs" || lc.OS != "linux" {
		t.Errorf("logs collector attrs wrong: %+v", lc)
	}
	if lc.Version != "v1.10.0" || lc.Namespace != "monitoring" || lc.Workload != "grafana-k8s-monitoring-alloy-logs" {
		t.Errorf("logs collector fidelity attrs wrong: %+v", lc)
	}
	// DaemonSet pod name is release-role-<hash> (NOT the node-name that the FM id ends in).
	if !strings.HasPrefix(lc.Pod, "grafana-k8s-monitoring-alloy-logs-") || lc.Pod == "" {
		t.Errorf("logs collector pod name wrong: %q", lc.Pod)
	}

	// StatefulSet: metrics_replicas ordinals 0..1, doubled-release id.
	wantM0 := "grafana-k8s-monitoring-demo-prod-usw2-monitoring-grafana-k8s-monitoring-alloy-metrics-0"
	if _, ok := by[wantM0]; !ok {
		t.Fatalf("missing statefulset id %q in %v", wantM0, keys(by))
	}
	wantM1 := strings.Replace(wantM0, "metrics-0", "metrics-1", 1)
	if _, ok := by[wantM1]; !ok {
		t.Fatalf("missing statefulset ordinal-1 id %q", wantM1)
	}

	// Deployment singleton: exactly one alloy-singleton, deterministic hash suffix.
	var singles []Collector
	for _, c := range cols {
		if c.App == "alloy-singleton" {
			singles = append(singles, c)
		}
	}
	if len(singles) != 1 {
		t.Fatalf("want 1 singleton, got %d", len(singles))
	}
	if !strings.HasPrefix(singles[0].ID, "grafana-k8s-monitoring-demo-prod-usw2-monitoring-grafana-k8s-monitoring-alloy-singleton-") {
		t.Errorf("singleton id format wrong: %q", singles[0].ID)
	}

	// Determinism: a second call yields identical ids.
	again := K8sRoster(seed, "grafana-k8s-monitoring", "demo-prod-usw2", "monitoring", nodes, km)
	if len(again) != len(cols) {
		t.Fatalf("non-deterministic length")
	}
	for i := range cols {
		if again[i].ID != cols[i].ID {
			t.Errorf("non-deterministic id at %d: %q vs %q", i, again[i].ID, cols[i].ID)
		}
	}
}

func keys(m map[string]Collector) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestK8sRosterWindowsCollectorOS(t *testing.T) {
	nodes := []fixture.Node{
		{Hostname: "ip-10-0-0-1.eu-west-1.compute.internal", OS: "linux"},
		{Hostname: "win-abc123", OS: "windows"},
	}
	km := fixture.K8sMonitoring{Enabled: true, FleetManagement: true, Features: map[string]bool{"node_logs": true}}
	roster := K8sRoster("seed", "", "c1", "", nodes, km)
	var winSeen, linSeen bool
	for _, c := range roster {
		if c.App == "alloy-logs" && c.OS == "windows" {
			winSeen = true
		}
		if c.App == "alloy-logs" && c.OS == "linux" {
			linSeen = true
		}
	}
	if !winSeen {
		t.Fatal("windows node's alloy-logs collector must register OS=windows")
	}
	if !linSeen {
		t.Fatal("linux node's alloy-logs collector must register OS=linux")
	}
}
