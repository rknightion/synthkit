// SPDX-License-Identifier: AGPL-3.0-only

package failuremode

import (
	"os"
	"path/filepath"
	"regexp"
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

// resourceAxisModes is the causal fault vocabulary on axis resource (design 3.2).
var resourceAxisModes = []string{
	"pool_exhausted", "concurrency_reduced", "quota_reduced", "consumer_slowdown",
	"dependency_slow", "dependency_unavailable", "credential_expired", "rule_regression",
}

func TestEveryResourceAxisModeHasAnAssertion(t *testing.T) {
	for _, mode := range resourceAxisModes {
		a, ok := AssertionFor([]string{mode})
		if !ok {
			t.Errorf("resource-axis mode %q has no covering assertion", mode)
			continue
		}
		if a.Mode != mode || a.Direction != DirectionIncrease {
			t.Errorf("%s assertion = %+v, want its own increase assertion", mode, a)
		}
		if strings.Contains(a.Query, "{{target}}") || strings.Contains(a.Query, "{{sibling}}") {
			t.Errorf("%s query = %q: a resource target names a declared resource, never a series label", mode, a.Query)
		}
		if a.SiblingQuery != "" {
			t.Errorf("%s declares a sibling query, but resource faults are not environment-scoped", mode)
		}
	}
}

// Span-metric and service-graph families are blueprint-scoped, so their assertions read only the
// scenario's own blueprint; CloudWatch and broker families are substrate-scoped and carry no
// blueprint label to filter on.
func TestBlueprintScopedResourceQueriesAreScopedToTheScenarioBlueprint(t *testing.T) {
	for _, mode := range resourceAxisModes {
		a, _ := AssertionFor([]string{mode})
		scoped := strings.Contains(a.Query, "traces_")
		has := strings.Contains(a.Query, `blueprint="{{blueprint}}"`) || strings.Contains(a.Query, `client_blueprint="{{blueprint}}"`)
		if scoped && !has {
			t.Errorf("%s reads a blueprint-scoped family without a blueprint selector: %q", mode, a.Query)
		}
		if !scoped && strings.Contains(a.Query, "{{blueprint}}") {
			t.Errorf("%s filters a substrate-scoped family by blueprint: %q", mode, a.Query)
		}
		if strings.Count(a.Query, "traces_") != strings.Count(a.Query, "{{blueprint}}") {
			t.Errorf("%s has a traces_ selector without its own blueprint scope: %q", mode, a.Query)
		}
	}
}

// Each assertion observes the signal family its mode moves (design 3.2 and 5).
func TestResourceAxisAssertionsObserveTheirSignalFamily(t *testing.T) {
	want := map[string][]string{
		"dependency_slow":        {"traces_spanmetrics_latency", `span_kind="SPAN_KIND_SERVER"`, "histogram_avg"},
		"pool_exhausted":         {"traces_spanmetrics_latency", `span_kind="SPAN_KIND_SERVER"`, "histogram_avg"},
		"rule_regression":        {"traces_spanmetrics_calls_total", `SPAN_KIND_SERVER|SPAN_KIND_CLIENT`, `status_code="STATUS_CODE_ERROR"`, "/ sum("},
		"dependency_unavailable": {"traces_spanmetrics_calls_total", `span_kind="SPAN_KIND_CLIENT"`, `status_code="STATUS_CODE_ERROR"`},
		"credential_expired":     {"traces_spanmetrics_calls_total", `span_kind="SPAN_KIND_CLIENT"`, `status_code="STATUS_CODE_ERROR"`},
		"quota_reduced":          {"traces_service_graph_request_failed_total", `{blueprint="{{blueprint}}"}`, `{client_blueprint="{{blueprint}}"}`},
		"concurrency_reduced":    {"aws_lambda_throttles_sum"},
		"consumer_slowdown":      {"aws_sqs_approximate_age_of_oldest_message_maximum", "solace_queue_spool_usage_msgs"},
	}
	for mode, parts := range want {
		a, ok := AssertionFor([]string{mode})
		if !ok {
			t.Errorf("%s has no assertion", mode)
			continue
		}
		for _, part := range parts {
			if !strings.Contains(a.Query, part) {
				t.Errorf("%s query = %q, missing %q", mode, a.Query, part)
			}
		}
	}
	// Latency is judged per series: a stack-wide mean would dilute one slowed node below the threshold.
	for _, mode := range []string{"dependency_slow", "pool_exhausted", "consumer_slowdown"} {
		if a, _ := AssertionFor([]string{mode}); !a.AnySeries {
			t.Errorf("%s must be judged per series", mode)
		}
	}
	for _, mode := range []string{"rule_regression", "concurrency_reduced", "quota_reduced", "dependency_unavailable", "credential_expired"} {
		if a, _ := AssertionFor([]string{mode}); a.AnySeries {
			t.Errorf("%s is a stack-wide aggregate and must not be judged per series", mode)
		}
	}
}

// A migrated scenario that mixes a legacy and a resource effect keeps selecting the observation
// the legacy effects selected.
func TestResourceAssertionPrioritiesKeepLegacySelection(t *testing.T) {
	cases := []struct {
		modes []string
		want  string
	}{
		{[]string{"cpu_hotspot", "memory_leak", "dependency_slow", "rule_regression", "pod_crashloop"}, "pod_crashloop"},
		{[]string{"rule_regression", "dependency_slow"}, "rule_regression"},
		{[]string{"error_spike", "latency_storm"}, "error_spike"},
	}
	for _, tc := range cases {
		got, ok := AssertionFor(tc.modes)
		if !ok || got.Mode != tc.want {
			t.Errorf("AssertionFor(%v) = %q, want %q", tc.modes, got.Mode, tc.want)
		}
	}
}

// Every metric a resource-mode query reads is a documented family: names come from signals/, never
// from the query author.
func TestResourceAxisQueriesReadOnlyDocumentedSeries(t *testing.T) {
	files, err := filepath.Glob("../../signals/*.md")
	if err != nil || len(files) == 0 {
		t.Fatalf("signals catalogue not found: %v", err)
	}
	var doc strings.Builder
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		doc.Write(b)
	}
	names := regexp.MustCompile(`\b(?:aws|traces|solace)_[a-z0-9_]+`)
	stats := []string{"_sum", "_average", "_maximum", "_minimum", "_sample_count"}
	for _, mode := range resourceAxisModes {
		a, _ := AssertionFor([]string{mode})
		found := names.FindAllString(a.Query, -1)
		if len(found) == 0 {
			t.Errorf("%s query %q reads no metric family", mode, a.Query)
		}
		for _, name := range found {
			documented := strings.Contains(doc.String(), name)
			for _, suffix := range stats {
				if strings.HasSuffix(name, suffix) && strings.Contains(doc.String(), strings.TrimSuffix(name, suffix)) {
					documented = true
				}
			}
			if !documented {
				t.Errorf("%s query reads %s, which signals/ does not document", mode, name)
			}
		}
	}
}

// Observations that are zero when healthy carry an absolute floor, so a 0 -> small flip from an
// unrelated series is not the mode; every other assertion stays proportional-only.
func TestOnlyZeroBaselineResourceAssertionsCarryAFloor(t *testing.T) {
	floored := map[string]bool{"consumer_slowdown": true, "concurrency_reduced": true}
	for _, a := range Assertions() {
		if floored[a.Mode] && a.MinActive <= 0 {
			t.Errorf("%s needs an absolute floor", a.Mode)
		}
		if !floored[a.Mode] && a.MinActive != 0 {
			t.Errorf("%s carries a floor %v it did not have before", a.Mode, a.MinActive)
		}
	}
}
