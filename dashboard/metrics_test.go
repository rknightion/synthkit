// SPDX-License-Identifier: AGPL-3.0-only

package dashboard

import (
	"strings"
	"testing"
)

func metricsFixture() *Manifest {
	return &Manifest{
		Blueprint: "initech", Label: "initech",
		Environments: []EnvRef{{Name: "prod"}},
		Metrics: []MetricSignal{
			{Name: "traces_spanmetrics_latency", Instrument: HistogramNative, Dual: true, Scope: ScopeBlueprint, LabelKeys: []string{"service", "span_name"}},
			{Name: "http_request_duration_seconds", Instrument: HistogramClassic, Scope: ScopeBlueprint, LabelKeys: []string{"route"}},
			{Name: "portkey_requests_total", Instrument: Counter, Scope: ScopeBlueprint, LabelKeys: []string{"model"}},
			{Name: "kube_pod_status_ready", Instrument: Gauge, Scope: ScopeSubstrate, LabelKeys: []string{"namespace"}},
		},
	}
}

func TestMetricsDashboardQueries(t *testing.T) {
	d, err := MetricsDashboard(metricsFixture())
	if err != nil {
		t.Fatal(err)
	}
	js, err := Render(d)
	if err != nil {
		t.Fatal(err)
	}
	s := string(js)

	// Native histogram quantiles — p50/p95/p99 all present (as 0.5, 0.95, 0.99 in PromQL)
	for _, q := range []string{"0.5", "0.95", "0.99"} {
		if !strings.Contains(s, q) {
			t.Errorf("native span histogram should render quantile q=%s", q)
		}
	}

	// Dual family: classic _bucket companion ALSO appears
	if !strings.Contains(s, `traces_spanmetrics_latency_bucket`) {
		t.Error("dual family must ALSO render a classic _bucket companion panel")
	}

	// Classic histogram: _bucket with le grouping
	if !strings.Contains(s, "http_request_duration_seconds_bucket") {
		t.Error("classic histogram should render the _bucket quantile")
	}
	if !strings.Contains(s, "by (le") {
		t.Error("classic histogram should render with le")
	}

	// Counter rate: name appears in rate expression
	if !strings.Contains(s, `portkey_requests_total`) {
		t.Error("counter should render a rate")
	}

	// Substrate gauge: NO blueprint= in selector
	// Find the kube_pod_status_ready expression and verify no blueprint= next to it
	idx := strings.Index(s, "kube_pod_status_ready")
	if idx >= 0 {
		// Grab a window around the metric name to check selector
		window := s[idx:min(idx+200, len(s))]
		if strings.Contains(window, `blueprint=`) {
			t.Error("substrate gauge kube_pod_status_ready must NOT have blueprint= in its selector")
		}
	}

	// Native histogram uses bare name (no _bucket suffix)
	if !strings.Contains(s, `histogram_quantile(0.5, sum`) {
		t.Error("native histogram should use bare histogram_quantile(0.5, sum...)")
	}

	// RefIds A/B/C for native quantile targets
	if !strings.Contains(s, `"refId": "A"`) {
		t.Error("expected refId A in rendered JSON")
	}
	if !strings.Contains(s, `"refId": "B"`) {
		t.Error("expected refId B in rendered JSON")
	}
	if !strings.Contains(s, `"refId": "C"`) {
		t.Error("expected refId C in rendered JSON")
	}
}

func TestMetricsDashboardUID(t *testing.T) {
	d, _ := MetricsDashboard(metricsFixture())
	if d.UID != "initech-metrics" {
		t.Errorf("UID = %q, want initech-metrics", d.UID)
	}
}

func TestMetricsDashboardEmptyMetricsStillValid(t *testing.T) {
	d, err := MetricsDashboard(&Manifest{Blueprint: "empty", Label: "empty"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Render(d); err != nil {
		t.Errorf("empty-metrics dashboard must still render (header + layout): %v", err)
	}
}

func TestMetricsDashboardDefaultUnchanged(t *testing.T) {
	d, err := MetricsDashboard(metricsFixture())
	if err != nil {
		t.Fatal(err)
	}
	js, _ := Render(d)
	s := string(js)
	if !strings.Contains(s, `histogram_quantile(0.95, sum (rate(traces_spanmetrics_latency{blueprint=\"initech\"}[$__rate_interval])))`) {
		t.Error("default (zero-opts) native query changed")
	}
	// Counter default unit pinned at "short" (changed from reqps); guard against regression.
	if !strings.Contains(s, `"unit": "short"`) {
		t.Error("default counter panel should carry unit short")
	}
	if strings.Contains(s, `"reqps"`) {
		t.Error("default render must NOT emit reqps (counter default is short)")
	}
}

func TestMetricsOptionsGroupBy(t *testing.T) {
	d, err := MetricsDashboard(metricsFixture(), MetricsOptions{
		GroupBy: map[string][]string{"traces_spanmetrics_latency": {"span_name"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	js, _ := Render(d)
	s := string(js)
	if !strings.Contains(s, `histogram_quantile(0.95, sum by (span_name) (rate(traces_spanmetrics_latency{blueprint=\"initech\"}[$__rate_interval])))`) {
		t.Error("GroupBy should produce `sum by (span_name)` for the native span panel")
	}
	if !strings.Contains(s, "{{span_name}}") {
		t.Error("grouped panel legend should include {{span_name}}")
	}
}

func TestMetricsOptionsGroupByUnknownDimDropped(t *testing.T) {
	d, _ := MetricsDashboard(metricsFixture(), MetricsOptions{
		GroupBy: map[string][]string{"traces_spanmetrics_latency": {"span_name", "nope"}},
	})
	js, _ := Render(d)
	s := string(js)
	if !strings.Contains(s, `sum by (span_name) (rate(traces_spanmetrics_latency`) {
		t.Error("unknown dim must be dropped, keeping only valid dims")
	}
	if strings.Contains(s, "nope") {
		t.Error("unknown dim must not appear in any query")
	}
}

func TestMetricsOptionsUnitOverrideAndExclude(t *testing.T) {
	d, _ := MetricsDashboard(metricsFixture(), MetricsOptions{
		UnitByFamily: map[string]string{"portkey_requests_total": "reqps"},
		Exclude:      []string{"kube_pod_status_ready"},
	})
	js, _ := Render(d)
	s := string(js)
	if !strings.Contains(s, `"reqps"`) {
		t.Error("UnitByFamily override should set portkey_requests_total unit to reqps")
	}
	if strings.Contains(s, "kube_pod_status_ready") {
		t.Error("Exclude should drop the kube_pod_status_ready panel")
	}
}

func TestSignalPanels(t *testing.T) {
	sig := MetricSignal{Name: "traces_spanmetrics_latency", Instrument: HistogramNative, Dual: true, Scope: ScopeBlueprint, LabelKeys: []string{"span_name"}}
	ps := SignalPanels(sig, "initech", MetricsOptions{})
	if len(ps) != 2 {
		t.Fatalf("dual family must yield 2 PanelSpecs (native + classic), got %d", len(ps))
	}
	if ps[0].ID == "" || ps[0].Panel == nil {
		t.Error("PanelSpec must carry an ID and a Panel")
	}
	if ps[0].ID != "m-traces_spanmetrics_latency-native" {
		t.Errorf("ps[0].ID = %q, want m-traces_spanmetrics_latency-native", ps[0].ID)
	}
	if ps[1].ID != "m-traces_spanmetrics_latency-classic" {
		t.Errorf("ps[1].ID = %q, want m-traces_spanmetrics_latency-classic", ps[1].ID)
	}
}
