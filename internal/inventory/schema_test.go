// SPDX-License-Identifier: AGPL-3.0-only

package inventory

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func TestMetricValueCapIsExplicitAndArrivalOrderIndependent(t *testing.T) {
	build := func(reverse bool) Schema {
		s := New()
		for i := 0; i < DefaultValueLimit+9; i++ {
			n := i
			if reverse {
				n = DefaultValueLimit + 8 - i
			}
			s.AddMetric("kube_pod_labels", "prometheus_remote_write_v1", InstrumentGauge,
				map[string]string{"label_team": fmt.Sprintf("team-%03d", n)}, nil)
		}
		return s
	}
	a, b := build(false), build(true)
	attr := a.Metrics[0].Labels[0]
	if len(attr.Values) != DefaultValueLimit || !attr.ValuesElided {
		t.Fatalf("values=%d elided=%v, want %d/true", len(attr.Values), attr.ValuesElided, DefaultValueLimit)
	}
	var aj, bj bytes.Buffer
	if err := a.WriteJSON(&aj); err != nil {
		t.Fatal(err)
	}
	if err := b.WriteJSON(&bj); err != nil {
		t.Fatal(err)
	}
	if aj.String() != bj.String() {
		t.Fatalf("arrival order changed JSON\nforward:\n%s\nreverse:\n%s", aj.String(), bj.String())
	}
}

func TestHistogramCanBeClassicAndNative(t *testing.T) {
	s := New()
	s.AddMetric("traces_spanmetrics_latency", "prometheus_remote_write_v2", InstrumentHistogram, nil,
		&Histogram{Classic: true, BucketBounds: []float64{0.1, 0.5}})
	s.AddMetric("traces_spanmetrics_latency", "prometheus_remote_write_v2", InstrumentHistogram, nil,
		&Histogram{Native: true, NativeSchemas: []int32{3}})
	got := s.Metrics[0].Histogram
	if got == nil || !got.Classic || !got.Native || len(got.BucketBounds) != 2 || len(got.NativeSchemas) != 1 {
		t.Fatalf("histogram=%+v", got)
	}
}

func TestNewSchemaWritesEmptyArrays(t *testing.T) {
	var out bytes.Buffer
	if err := New().WriteJSON(&out); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"metrics": []`, `"logs": []`, `"traces": []`, `"profiles": []`, `"sigil": []`, `"receipts": []`} {
		if !strings.Contains(out.String(), field) {
			t.Errorf("JSON missing %s: %s", field, out.String())
		}
	}
}

func TestStructuralProjectionExcludesValuesAndReceipts(t *testing.T) {
	a, b := New(), New()
	a.AddMetric("up", TransportPrometheusRW2, InstrumentGauge, map[string]string{"instance": "one"}, nil)
	b.AddMetric("up", TransportPrometheusRW2, InstrumentGauge, map[string]string{"instance": "two"}, nil)
	a.AddReceipt("promrw", 1)
	b.AddReceipt("promrw", 99)
	var aj, bj bytes.Buffer
	if err := a.Project().WriteJSON(&aj); err != nil {
		t.Fatal(err)
	}
	if err := b.Project().WriteJSON(&bj); err != nil {
		t.Fatal(err)
	}
	if aj.String() != bj.String() {
		t.Fatalf("non-structural values changed projection\n%s\n%s", aj.String(), bj.String())
	}
}

func TestNormalizeCapsExternallyConstructedAttributes(t *testing.T) {
	values := make([]string, DefaultValueLimit+1)
	for i := range values {
		values[i] = fmt.Sprintf("value-%03d", DefaultValueLimit-i)
	}
	s := Schema{Metrics: []Metric{{Name: "external", Labels: []Attribute{{Key: "label_name", Values: values}}}}}
	s.Normalize()
	got := s.Metrics[0].Labels[0]
	if len(got.Values) != DefaultValueLimit || !got.ValuesElided {
		t.Fatalf("values=%d elided=%v", len(got.Values), got.ValuesElided)
	}
	if got.Values[0] != "value-000" || got.Values[len(got.Values)-1] != "value-063" {
		t.Fatalf("normalized values range=%q..%q", got.Values[0], got.Values[len(got.Values)-1])
	}
}
