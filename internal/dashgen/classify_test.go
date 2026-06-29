// SPDX-License-Identifier: AGPL-3.0-only

package dashgen

import (
	"reflect"
	"testing"

	"github.com/rknightion/synthkit/dashboard"
	"github.com/rknightion/synthkit/internal/sink/promrw"
)

func TestClassifyMetrics(t *testing.T) {
	inv := map[string][]string{
		"http_request_duration_seconds_bucket": {"blueprint", "le", "route"},
		"http_request_duration_seconds_sum":    {"blueprint", "route"},
		"http_request_duration_seconds_count":  {"blueprint", "route"},
		"http_requests_total":                  {"blueprint", "route", "code"},
		"kube_pod_info":                        {"namespace", "pod", "uid"},
		"aws_rds_cpuutilization_average":       {"dimension_DBInstanceIdentifier", "region"},
		"aws_rds_cpuutilization_maximum":       {"dimension_DBInstanceIdentifier", "region"},
		"aws_rds_cpuutilization_minimum":       {"dimension_DBInstanceIdentifier", "region"},
		"aws_rds_cpuutilization_sum":           {"dimension_DBInstanceIdentifier", "region"},
		"aws_rds_cpuutilization_sample_count":  {"dimension_DBInstanceIdentifier", "region"},
	}
	// Authoritative kinds (as state.Collect would stamp them): histogram components ⇒
	// KindHistogram, the request counter ⇒ KindCounter, the gauge ⇒ KindGauge, the CW
	// per-period stats ⇒ KindGauge (Set).
	kinds := map[string]promrw.Kind{
		"http_request_duration_seconds_bucket": promrw.KindHistogram,
		"http_request_duration_seconds_sum":    promrw.KindHistogram,
		"http_request_duration_seconds_count":  promrw.KindHistogram,
		"http_requests_total":                  promrw.KindCounter,
		"kube_pod_info":                        promrw.KindGauge,
		"aws_rds_cpuutilization_average":       promrw.KindGauge,
		"aws_rds_cpuutilization_maximum":       promrw.KindGauge,
		"aws_rds_cpuutilization_minimum":       promrw.KindGauge,
		"aws_rds_cpuutilization_sum":           promrw.KindGauge,
		"aws_rds_cpuutilization_sample_count":  promrw.KindGauge,
	}
	got := ClassifyMetrics(inv, kinds, nil)

	byName := map[string]dashboard.MetricSignal{}
	for _, s := range got {
		byName[s.Name] = s
	}

	want := map[string]dashboard.InstrumentKind{
		"http_request_duration_seconds": dashboard.HistogramClassic,
		"http_requests_total":           dashboard.Counter,
		"kube_pod_info":                 dashboard.Gauge,
		"aws_rds_cpuutilization":        dashboard.Gauge, // CW stat-set folds to one base
	}
	if len(byName) != len(want) {
		t.Fatalf("got %d signals %v, want %d", len(byName), keysOf(byName), len(want))
	}
	for name, kind := range want {
		s, ok := byName[name]
		if !ok {
			t.Errorf("missing signal %q", name)
			continue
		}
		if s.Instrument != kind {
			t.Errorf("%q instrument = %s, want %s", name, s.Instrument, kind)
		}
	}
	// scope from blueprint-label presence
	if byName["http_requests_total"].Scope != dashboard.ScopeBlueprint {
		t.Errorf("http_requests_total should be ScopeBlueprint")
	}
	if byName["kube_pod_info"].Scope != dashboard.ScopeSubstrate {
		t.Errorf("kube_pod_info should be ScopeSubstrate")
	}
	// classic-histogram base unions its component label keys, dropping le AND the
	// blueprint selector (which is consumed into Scope, not a query dimension)
	if !reflect.DeepEqual(byName["http_request_duration_seconds"].LabelKeys, []string{"route"}) {
		t.Errorf("classic hist label keys = %v, want [route] (le + blueprint dropped)", byName["http_request_duration_seconds"].LabelKeys)
	}
}

func keysOf(m map[string]dashboard.MetricSignal) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func findSignal(t *testing.T, sigs []dashboard.MetricSignal, name string) dashboard.MetricSignal {
	t.Helper()
	for _, s := range sigs {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("signal %q not found in output", name)
	return dashboard.MetricSignal{}
}

func countSignals(sigs []dashboard.MetricSignal, name string) int {
	n := 0
	for _, s := range sigs {
		if s.Name == name {
			n++
		}
	}
	return n
}

// TestClassifyUsesAuthoritativeKind: a Prometheus summary (_sum/_count with NO _bucket) and
// a _count-suffixed counter would both be guessed as gauges from their names alone. The
// authoritative kind (KindCounter, from state's Add origin) must classify them as Counter so
// the dashboard generator queries them with rate(); a genuine Set gauge stays Gauge.
func TestClassifyUsesAuthoritativeKind(t *testing.T) {
	inv := map[string][]string{
		"certmanager_http_request_duration_seconds_sum":   {"blueprint", "method"},
		"certmanager_http_request_duration_seconds_count": {"blueprint", "method"},
		"awscni_reconcile_count":                          {"cluster"},
		"node_queue_depth":                                {"cluster"},
	}
	kinds := map[string]promrw.Kind{
		"certmanager_http_request_duration_seconds_sum":   promrw.KindCounter,
		"certmanager_http_request_duration_seconds_count": promrw.KindCounter,
		"awscni_reconcile_count":                          promrw.KindCounter,
		"node_queue_depth":                                promrw.KindGauge,
	}
	byName := map[string]dashboard.MetricSignal{}
	for _, s := range ClassifyMetrics(inv, kinds, nil) {
		byName[s.Name] = s
	}
	for _, n := range []string{
		"certmanager_http_request_duration_seconds_sum",
		"certmanager_http_request_duration_seconds_count",
		"awscni_reconcile_count",
	} {
		if byName[n].Instrument != dashboard.Counter {
			t.Errorf("%q instrument = %s, want Counter (authoritative kind)", n, byName[n].Instrument)
		}
	}
	if byName["node_queue_depth"].Instrument != dashboard.Gauge {
		t.Errorf("node_queue_depth instrument = %s, want Gauge", byName["node_queue_depth"].Instrument)
	}
}

func TestClassifyDualFamilyIsNative(t *testing.T) {
	inv := map[string][]string{
		"traces_spanmetrics_latency":        {"blueprint", "service", "span_name"},
		"traces_spanmetrics_latency_bucket": {"blueprint", "le", "service", "span_name"},
		"traces_spanmetrics_latency_sum":    {"blueprint", "service", "span_name"},
		"traces_spanmetrics_latency_count":  {"blueprint", "service", "span_name"},
	}
	kinds := map[string]promrw.Kind{
		"traces_spanmetrics_latency":        promrw.KindHistogram,
		"traces_spanmetrics_latency_bucket": promrw.KindHistogram,
		"traces_spanmetrics_latency_sum":    promrw.KindHistogram,
		"traces_spanmetrics_latency_count":  promrw.KindHistogram,
	}
	natives := map[string]bool{"traces_spanmetrics_latency": true}
	out := ClassifyMetrics(inv, kinds, natives)
	sig := findSignal(t, out, "traces_spanmetrics_latency")
	if sig.Instrument != dashboard.HistogramNative {
		t.Errorf("dual family must classify as HistogramNative, got %v", sig.Instrument)
	}
	if !sig.Dual {
		t.Error("a family with both native bare series AND classic _bucket must be marked Dual")
	}
	for _, k := range sig.LabelKeys {
		if k == "le" {
			t.Error("le must be dropped from native family label keys")
		}
	}
	if n := countSignals(out, "traces_spanmetrics_latency"); n != 1 {
		t.Errorf("want 1 folded signal, got %d", n)
	}
}

func TestClassifyClassicHistogramUnaffected(t *testing.T) {
	inv := map[string][]string{
		"http_request_duration_seconds_bucket": {"le", "route"},
		"http_request_duration_seconds_sum":    {"route"},
		"http_request_duration_seconds_count":  {"route"},
	}
	kinds := map[string]promrw.Kind{
		"http_request_duration_seconds_bucket": promrw.KindHistogram,
		"http_request_duration_seconds_sum":    promrw.KindHistogram,
		"http_request_duration_seconds_count":  promrw.KindHistogram,
	}
	out := ClassifyMetrics(inv, kinds, map[string]bool{})
	sig := findSignal(t, out, "http_request_duration_seconds")
	if sig.Instrument != dashboard.HistogramClassic {
		t.Errorf("classic histogram must stay HistogramClassic, got %v", sig.Instrument)
	}
	if sig.Dual {
		t.Error("a classic-only family must NOT be marked Dual")
	}
}
