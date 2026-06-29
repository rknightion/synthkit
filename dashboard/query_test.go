// SPDX-License-Identifier: AGPL-3.0-only

package dashboard

import (
	"strings"
	"testing"
)

func TestSelectorBlueprintScoped(t *testing.T) {
	got := Selector(MetricSignal{Name: "http_requests_total", Scope: ScopeBlueprint}, "initech", map[string]string{"route": "$route"})
	want := `{blueprint="initech",route="$route"}`
	if got != want {
		t.Errorf("Selector = %q, want %q", got, want)
	}
}

func TestSelectorSubstrateOmitsBlueprint(t *testing.T) {
	got := Selector(MetricSignal{Name: "kube_pod_info", Scope: ScopeSubstrate}, "initech", map[string]string{"cluster": "$cluster"})
	want := `{cluster="$cluster"}`
	if got != want {
		t.Errorf("Selector = %q, want %q", got, want)
	}
}

func TestRateExpr(t *testing.T) {
	got := RateExpr("http_requests_total", `{blueprint="initech"}`, []string{"route"})
	want := `sum by (route) (rate(http_requests_total{blueprint="initech"}[$__rate_interval]))`
	if got != want {
		t.Errorf("RateExpr = %q, want %q", got, want)
	}
}

func TestClassicHistogramQuantile(t *testing.T) {
	got := ClassicHistogramQuantile(0.95, "http_request_duration_seconds", `{blueprint="initech"}`, []string{"route"})
	want := `histogram_quantile(0.95, sum by (le, route) (rate(http_request_duration_seconds_bucket{blueprint="initech"}[$__rate_interval])))`
	if got != want {
		t.Errorf("ClassicHistogramQuantile = %q, want %q", got, want)
	}
}

func TestCWGauge(t *testing.T) {
	got := CWGauge("aws_rds_cpuutilization", "average", `{dimension_DBInstanceIdentifier="$db"}`, []string{"dimension_DBInstanceIdentifier"})
	want := `avg by (dimension_DBInstanceIdentifier) (aws_rds_cpuutilization_average{dimension_DBInstanceIdentifier="$db"})`
	if got != want {
		t.Errorf("CWGauge = %q, want %q", got, want)
	}
}

func TestNativeHistogramQuantile(t *testing.T) {
	got := NativeHistogramQuantile(0.95, "traces_spanmetrics_latency", `{blueprint="initech"}`, []string{"span_name"})
	want := `histogram_quantile(0.95, sum by (span_name) (rate(traces_spanmetrics_latency{blueprint="initech"}[$__rate_interval])))`
	if got != want {
		t.Errorf("NativeHistogramQuantile = %q, want %q", got, want)
	}
}

func TestNativeHistogramQuantileNoGroup(t *testing.T) {
	got := NativeHistogramQuantile(0.95, "traces_spanmetrics_latency", `{blueprint="initech"}`, nil)
	want := `histogram_quantile(0.95, sum (rate(traces_spanmetrics_latency{blueprint="initech"}[$__rate_interval])))`
	if got != want {
		t.Errorf("no-group native = %q, want %q", got, want)
	}
}

func TestGaugeExpr(t *testing.T) {
	got := GaugeExpr("kube_pod_status_ready", `{blueprint="initech"}`, []string{"namespace"})
	want := `avg by (namespace) (kube_pod_status_ready{blueprint="initech"})`
	if got != want {
		t.Errorf("GaugeExpr = %q, want %q", got, want)
	}
	gotNo := GaugeExpr("kube_pod_status_ready", `{blueprint="initech"}`, nil)
	wantNo := `avg (kube_pod_status_ready{blueprint="initech"})`
	if gotNo != wantNo {
		t.Errorf("GaugeExpr no-group = %q, want %q", gotNo, wantNo)
	}
}

func TestRateExprNoGroup(t *testing.T) {
	got := RateExpr("portkey_requests_total", `{blueprint="initech"}`, nil)
	want := `sum (rate(portkey_requests_total{blueprint="initech"}[$__rate_interval]))`
	if got != want {
		t.Errorf("RateExpr no-group = %q, want %q", got, want)
	}
}

// F3: NativeHistogramRate emits the bare series rate (no _bucket, no le).
func TestNativeHistogramRate(t *testing.T) {
	cases := []struct {
		name, selector, want string
	}{
		{
			"traces_spanmetrics_latency",
			`{blueprint="initech"}`,
			`sum(rate(traces_spanmetrics_latency{blueprint="initech"}[$__rate_interval]))`,
		},
		{
			"traces_service_graph_request_server_seconds",
			`{client="api-gw"}`,
			`sum(rate(traces_service_graph_request_server_seconds{client="api-gw"}[$__rate_interval]))`,
		},
		{
			// Empty selector still produces valid PromQL.
			"my_native_histogram",
			"{}",
			`sum(rate(my_native_histogram{}[$__rate_interval]))`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := NativeHistogramRate(c.name, c.selector)
			if got != c.want {
				t.Errorf("NativeHistogramRate(%q, %q)\n got  %q\nwant %q", c.name, c.selector, got, c.want)
			}
			// Must NOT contain _bucket or le.
			if strings.Contains(got, "_bucket") {
				t.Errorf("NativeHistogramRate must not contain _bucket: %q", got)
			}
			if strings.Contains(got, `"le"`) || strings.Contains(got, `,le`) {
				t.Errorf("NativeHistogramRate must not contain le label: %q", got)
			}
		})
	}
}
