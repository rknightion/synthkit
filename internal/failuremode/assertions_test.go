// SPDX-License-Identifier: AGPL-3.0-only

package failuremode

import (
	"strings"
	"testing"
)

func TestAssertionForPrefersReversibleObservation(t *testing.T) {
	got, ok := AssertionFor([]string{"cpu_hotspot", "pod_crashloop", "error_spike"})
	if !ok {
		t.Fatal("AssertionFor returned no assertion")
	}
	if got.Mode != "pod_crashloop" {
		t.Fatalf("AssertionFor mode = %q, want pod_crashloop", got.Mode)
	}
}

func TestScenarioModeAssertionsAreSourced(t *testing.T) {
	for _, a := range Assertions() {
		if a.Mode == "" || a.Query == "" || a.Source == "" || a.QueryAPI == "" {
			t.Fatalf("incomplete assertion: %+v", a)
		}
		if a.QueryAPI != "prometheus" && a.QueryAPI != "loki" {
			t.Fatalf("assertion %q has invalid query API %q", a.Mode, a.QueryAPI)
		}
		if a.Direction != DirectionIncrease && a.Direction != DirectionDecrease {
			t.Fatalf("assertion %q has invalid direction %q", a.Mode, a.Direction)
		}
	}
}

func TestScenarioAssertionsUseReversibleObservations(t *testing.T) {
	byMode := map[string]Assertion{}
	for _, a := range Assertions() {
		byMode[a.Mode] = a
	}
	if got := byMode["pod_crashloop"].Query; !strings.Contains(got, "kube_pod_status_phase") || strings.Contains(got, "restarts_total") {
		t.Fatalf("pod_crashloop query = %q, want reversible Pending gauge", got)
	}
	if got := byMode["pod_crashloop"].Query; !strings.Contains(got, "kube_pod_container_status_waiting") {
		t.Fatalf("pod_crashloop query = %q, want normal startup Pending pods excluded", got)
	}
	if got := byMode["oom_kill"].Query; !strings.Contains(got, "rate(kube_pod_container_status_restarts_total") || !strings.Contains(got, "[2m]") || strings.Contains(got, "last_terminated_reason") {
		t.Fatalf("oom_kill query = %q, want the two-emission bounded restart-counter rate", got)
	}
	for _, mode := range []string{"error_spike", "latency_storm"} {
		if got := byMode[mode].Query; !strings.Contains(got, "[2m]") {
			t.Fatalf("%s query = %q, want the two-emission post-mutation window", mode, got)
		}
	}
	if got := byMode["latency_storm"].Query; !strings.Contains(got, "histogram_count") || !strings.Contains(got, "> 0") {
		t.Fatalf("latency_storm query = %q, want zero-count native histograms filtered before averaging", got)
	}
	for _, mode := range []string{"error_spike", "latency_storm"} {
		if got := byMode[mode].Query; !strings.Contains(got, `service="{{target}}"`) || strings.Contains(got, `service_name="{{target}}"`) {
			t.Fatalf("%s query = %q, want the live Tempo-derived service label", mode, got)
		}
	}
	if got := byMode["web_vitals_degraded"].Query; !strings.HasPrefix(got, "avg(") || !strings.Contains(got, `| logfmt | app_name="{{target}}"`) || strings.Contains(got, `|= "app_name={{target}}"`) || !strings.Contains(got, "[75s]") {
		t.Fatalf("web_vitals_degraded query = %q, want one aggregated, exact parsed app_name Loki series", got)
	}
}

func TestSlowQueryStormCoversDocumentDBAndRDS(t *testing.T) {
	got, ok := AssertionFor([]string{"slow_query_storm"})
	if !ok {
		t.Fatal("slow_query_storm has no assertion")
	}
	for _, want := range []string{"aws_docdb_read_latency_average", "aws_rds_read_latency_average"} {
		if !strings.Contains(got.Query, want) {
			t.Fatalf("slow_query_storm query = %q, missing %s", got.Query, want)
		}
	}
}
