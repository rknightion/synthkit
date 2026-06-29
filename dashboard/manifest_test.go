// SPDX-License-Identifier: AGPL-3.0-only

package dashboard

import "testing"

func TestInstrumentKindString(t *testing.T) {
	cases := map[InstrumentKind]string{
		Counter: "counter", Gauge: "gauge", HistogramClassic: "histogram_classic", HistogramNative: "histogram_native",
	}
	for k, want := range cases {
		if got := k.String(); got != want {
			t.Errorf("InstrumentKind(%d).String() = %q, want %q", k, got, want)
		}
	}
}

func TestManifestMetricByName(t *testing.T) {
	m := &Manifest{Metrics: []MetricSignal{
		{Name: "http_requests_total", Instrument: Counter},
		{Name: "kube_pod_info", Instrument: Gauge},
	}}
	got, ok := m.Metric("kube_pod_info")
	if !ok || got.Instrument != Gauge {
		t.Fatalf("Metric(kube_pod_info) = %+v, %v", got, ok)
	}
	if _, ok := m.Metric("nope"); ok {
		t.Errorf("Metric(nope) should be absent")
	}
}
