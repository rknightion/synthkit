// SPDX-License-Identifier: AGPL-3.0-only

package telemetryspec

import (
	"bytes"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func resVal(name, field string) ValueModel {
	return ValueModel{Resource: &ResourceValue{Name: name, Field: field}}
}

func outVal(measure string, status ...string) ValueModel {
	return ValueModel{Outcomes: &OutcomeValue{Status: status, Measure: measure}}
}

func statusEnum(values ...string) ValueModel {
	vm := ValueModel{}
	for _, v := range values {
		vm.Enum = append(vm.Enum, EnumEntry{Value: v, Weight: 1})
	}
	return vm
}

func wantErr(t *testing.T, err error, sub string) {
	t.Helper()
	if err == nil {
		t.Fatalf("want an error containing %q, got nil", sub)
	}
	if !strings.Contains(err.Error(), sub) {
		t.Fatalf("error %q does not contain %q", err, sub)
	}
}

func TestCausalKindsAndOneOf(t *testing.T) {
	if got := resVal("pool", "utilization").Kind(); got != KindResource {
		t.Fatalf("Kind()=%q want %q", got, KindResource)
	}
	if got := outVal("count", "5xx").Kind(); got != KindOutcomes {
		t.Fatalf("Kind()=%q want %q", got, KindOutcomes)
	}
	both := resVal("pool", "utilization")
	both.Const = ptr(1.0)
	wantErr(t, both.Validate(), "2 models set")
	both = outVal("count", "5xx")
	both.Resource = &ResourceValue{Name: "p", Field: "rejected"}
	wantErr(t, both.Validate(), "2 models set")
}

func TestResourceValueAcceptance(t *testing.T) {
	gauges := []string{"demand_rps", "capacity", "utilization", "in_use", "waiting", "wait_seconds",
		"backlog", "oldest_age_seconds", "latency_factor", "hold_seconds", "served_rps", "failed_share"}
	for _, f := range gauges {
		m := MetricSpec{Name: "g", Instrument: InstrumentGauge, Value: resVal("pool", f)}
		if err := m.Validate(); err != nil {
			t.Errorf("gauge field %q on a gauge must validate: %v", f, err)
		}
	}
	for _, f := range []string{"rejected", "admitted", "failed"} {
		m := MetricSpec{Name: "c", Instrument: InstrumentCounter, Value: resVal("pool", f)}
		if err := m.Validate(); err != nil {
			t.Errorf("counter field %q on a counter must validate: %v", f, err)
		}
	}
}

func TestResourceValueRejections(t *testing.T) {
	cases := []struct {
		name string
		spec MetricSpec
		sub  string
	}{
		{"empty name", MetricSpec{Name: "g", Instrument: InstrumentGauge, Value: resVal("  ", "utilization")}, "resource: name"},
		{"unknown field", MetricSpec{Name: "g", Instrument: InstrumentGauge, Value: resVal("pool", "bogus")}, "unknown field"},
		{"empty field", MetricSpec{Name: "g", Instrument: InstrumentGauge, Value: resVal("pool", "")}, "unknown field"},
		{"counter field on a gauge", MetricSpec{Name: "g", Instrument: InstrumentGauge, Value: resVal("pool", "rejected")}, "counter field"},
		{"gauge field on a counter", MetricSpec{Name: "c", Instrument: InstrumentCounter, Value: resVal("pool", "utilization")}, "gauge field"},
		{"resource on a histogram", MetricSpec{Name: "h", Instrument: InstrumentHistogram, Buckets: []float64{1, 2}, Value: resVal("pool", "utilization")}, "histogram"},
		{"counter field on a histogram", MetricSpec{Name: "h", Instrument: InstrumentHistogram, Buckets: []float64{1, 2}, Value: resVal("pool", "rejected")}, "histogram"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { wantErr(t, c.spec.Validate(), c.sub) })
	}
}

func TestCausalKindsMetricValueOnly(t *testing.T) {
	for kind, vm := range map[string]ValueModel{
		KindResource: resVal("pool", "utilization"),
		KindOutcomes: outVal("share", "5xx"),
	} {
		for pos, label := range map[position]string{posLabel: "stream label", posMetricLabel: "metric label", posBody: "body", posAttr: "attr"} {
			t.Run(kind+" as "+label, func(t *testing.T) { wantErr(t, checkValue(vm, pos, "fixture"), "metric value only") })
		}
		if err := checkValue(vm, posMetricValue, "fixture"); err != nil {
			t.Errorf("%s as a metric value must pass checkValue: %v", kind, err)
		}
	}
	// Whole-spec routes: log body, span attribute, stream label, metric label.
	wantErr(t, LogSpec{Source: "s", Body: map[string]ValueModel{"x": resVal("pool", "utilization")}}.Validate(), "metric value only")
	wantErr(t, LogSpec{Source: "s", Body: map[string]ValueModel{"x": outVal("count", "5xx")}}.Validate(), "metric value only")
	wantErr(t, SpanSpec{NameTemplate: "n", Attributes: map[string]ValueModel{"x": resVal("pool", "utilization")}}.Validate(), "metric value only")
	wantErr(t, SpanSpec{NameTemplate: "n", Attributes: map[string]ValueModel{"x": outVal("count", "5xx")}}.Validate(), "metric value only")
	wantErr(t, LogSpec{Source: "s", StreamLabels: map[string]ValueModel{"x": resVal("pool", "utilization")}}.Validate(), "metric value only")
	wantErr(t, MetricSpec{Name: "g", Instrument: InstrumentGauge, Value: ValueModel{Const: ptr(1.0)},
		Labels: map[string]ValueModel{"x": outVal("count", "5xx")}}.Validate(), "metric value only")
}

func TestOutcomesValueAcceptance(t *testing.T) {
	cases := map[string]MetricSpec{
		"count counter":            {Name: "c", Instrument: InstrumentCounter, Value: outVal("count", "5xx")},
		"default measure is count": {Name: "c", Instrument: InstrumentCounter, Value: outVal("", "503", "success")},
		"share gauge":              {Name: "g", Instrument: InstrumentGauge, Value: outVal("share", "4xx")},
		"latency histogram":        {Name: "h", Instrument: InstrumentHistogram, Buckets: []float64{0.1, 1}, Value: outVal("latency", "success")},
		"status_label": {Name: "c", Instrument: InstrumentCounter,
			Labels: map[string]ValueModel{"code": statusEnum("200", "5xx", "success")},
			Value:  ValueModel{Outcomes: &OutcomeValue{StatusLabel: "code", Measure: "count"}}},
	}
	for name, m := range cases {
		if err := m.Validate(); err != nil {
			t.Errorf("%s must validate: %v", name, err)
		}
	}
}

func TestOutcomesValueRejections(t *testing.T) {
	enumCode := map[string]ValueModel{"code": statusEnum("200", "503")}
	cases := []struct {
		name string
		spec MetricSpec
		sub  string
	}{
		{"unknown measure", MetricSpec{Name: "c", Instrument: InstrumentCounter, Value: outVal("rate", "5xx")}, "measure"},
		{"count on a gauge", MetricSpec{Name: "g", Instrument: InstrumentGauge, Value: outVal("count", "5xx")}, "counter"},
		{"count on a histogram", MetricSpec{Name: "h", Instrument: InstrumentHistogram, Buckets: []float64{1}, Value: outVal("count", "5xx")}, "counter"},
		{"default count on a gauge", MetricSpec{Name: "g", Instrument: InstrumentGauge, Value: outVal("", "5xx")}, "counter"},
		{"share on a counter", MetricSpec{Name: "c", Instrument: InstrumentCounter, Value: outVal("share", "5xx")}, "gauge"},
		{"share on a histogram", MetricSpec{Name: "h", Instrument: InstrumentHistogram, Buckets: []float64{1}, Value: outVal("share", "5xx")}, "gauge"},
		{"latency on a counter", MetricSpec{Name: "c", Instrument: InstrumentCounter, Value: outVal("latency", "5xx")}, "histogram"},
		{"latency on a gauge", MetricSpec{Name: "g", Instrument: InstrumentGauge, Value: outVal("latency", "5xx")}, "histogram"},
		{"no status and no status_label", MetricSpec{Name: "c", Instrument: InstrumentCounter, Value: outVal("count")}, "status"},
		{"bad status token", MetricSpec{Name: "c", Instrument: InstrumentCounter, Value: outVal("count", "6xx")}, "status"},
		{"blank status token", MetricSpec{Name: "c", Instrument: InstrumentCounter, Value: outVal("count", " ")}, "status"},
		{"status and status_label together", MetricSpec{Name: "c", Instrument: InstrumentCounter, Labels: enumCode,
			Value: ValueModel{Outcomes: &OutcomeValue{Status: []string{"5xx"}, StatusLabel: "code"}}}, "either status or status_label"},
		{"status_label names no label", MetricSpec{Name: "c", Instrument: InstrumentCounter,
			Value: ValueModel{Outcomes: &OutcomeValue{StatusLabel: "code"}}}, "status_label"},
		{"status_label names another label", MetricSpec{Name: "c", Instrument: InstrumentCounter, Labels: enumCode,
			Value: ValueModel{Outcomes: &OutcomeValue{StatusLabel: "other"}}}, "status_label"},
		{"status_label is not an enum", MetricSpec{Name: "c", Instrument: InstrumentCounter,
			Labels: map[string]ValueModel{"code": {ConstStr: ptr("503")}},
			Value:  ValueModel{Outcomes: &OutcomeValue{StatusLabel: "code"}}}, "enum"},
		{"status_label enum value is not a status", MetricSpec{Name: "c", Instrument: InstrumentCounter,
			Labels: map[string]ValueModel{"code": statusEnum("200", "bogus")},
			Value:  ValueModel{Outcomes: &OutcomeValue{StatusLabel: "code"}}}, "status"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { wantErr(t, c.spec.Validate(), c.sub) })
	}
}

func TestCausalValidateCustomAndYAML(t *testing.T) {
	src := `
- name: pool_utilization
  source: design
  instrument: gauge
  value: { resource: { name: db-pool, field: utilization } }
- name: pool_rejected_total
  source: design
  instrument: counter
  value: { resource: { name: db-pool, field: rejected } }
- name: requests_total
  source: design
  instrument: counter
  labels: { code: { enum: [ { value: "200" }, { value: 5xx } ] } }
  value: { outcomes: { status_label: code, measure: count } }
- name: err_share
  source: design
  instrument: gauge
  value: { outcomes: { status: [ 5xx, "503" ], measure: share } }
- name: hop_seconds
  source: design
  instrument: histogram
  buckets: [0.1, 1]
  value: { outcomes: { status: [ success ], measure: latency } }
`
	var cm CustomMetrics
	if err := yaml.Unmarshal([]byte(src), &cm); err != nil {
		t.Fatalf("custom metrics must decode and validate: %v", err)
	}
	if len(cm) != 5 || cm[0].Value.Resource == nil || cm[2].Value.Outcomes == nil || cm[2].Value.Outcomes.StatusLabel != "code" {
		t.Fatalf("decoded shape wrong: %+v", cm)
	}
	// Strict decode still rejects a stray key inside the new structs.
	bad := `
- name: x
  source: design
  instrument: gauge
  value: { resource: { name: db-pool, field: utilization, extra: 1 } }
`
	dec := yaml.NewDecoder(bytes.NewReader([]byte(bad)))
	dec.KnownFields(true)
	var specs []MetricSpec
	if err := dec.Decode(&specs); err == nil {
		t.Fatal("unknown key inside resource must fail a strict decode")
	}
	// A rejection through the YAML route reaches the loader.
	var cm2 CustomMetrics
	wantErr(t, yaml.Unmarshal([]byte(`
- name: x
  source: design
  instrument: counter
  value: { resource: { name: db-pool, field: utilization } }
`), &cm2), "gauge field")
}

func TestNilProvidersEvaluateToZero(t *testing.T) {
	for name, vm := range map[string]ValueModel{
		"resource":         resVal("pool", "utilization"),
		"outcomes count":   outVal("count", "5xx"),
		"outcomes share":   outVal("share", "5xx"),
		"outcomes latency": outVal("latency", "5xx"),
	} {
		n, s := vm.Eval(EvalCtx{})
		if n != 0 || s != "" {
			t.Errorf("%s with nil providers = (%v, %q), want (0, \"\")", name, n, s)
		}
		if obs := vm.Observations(EvalCtx{}); obs != nil {
			t.Errorf("%s Observations with nil providers = %v, want none", name, obs)
		}
	}
}

func TestProvidersFeedEval(t *testing.T) {
	var gotName, gotField string
	var gotOut OutcomeValue
	var gotLabels map[string]string
	ctx := EvalCtx{
		Labels: map[string]string{"code": "503"},
		Resource: func(name, field string) (float64, bool) {
			gotName, gotField = name, field
			if name == "known" {
				return 0.75, true
			}
			return 99, false
		},
		Outcomes: func(v OutcomeValue, labels map[string]string) float64 {
			gotOut, gotLabels = v, labels
			return 7
		},
		OutcomeLatencies: func(v OutcomeValue, labels map[string]string) []float64 {
			gotOut, gotLabels = v, labels
			return []float64{0.1, 0.2}
		},
	}
	if n, _ := resVal("known", "utilization").Eval(ctx); n != 0.75 || gotName != "known" || gotField != "utilization" {
		t.Fatalf("resource eval = %v (%q,%q)", n, gotName, gotField)
	}
	// An unknown resource (provider reports false) reads 0, not the provider's stray value.
	if n, _ := resVal("missing", "utilization").Eval(ctx); n != 0 {
		t.Fatalf("unknown resource must read 0, got %v", n)
	}
	for _, measure := range []string{"count", "share"} {
		vm := outVal(measure, "503")
		if n, _ := vm.Eval(ctx); n != 7 || gotOut.Measure != measure || gotLabels["code"] != "503" {
			t.Fatalf("outcomes %s eval = %v (%+v, %v)", measure, n, gotOut, gotLabels)
		}
	}
	lat := outVal("latency", "503")
	if n, _ := lat.Eval(ctx); n != 0 {
		t.Fatalf("latency Eval must read 0 (observations come from Observations), got %v", n)
	}
	if obs := lat.Observations(ctx); len(obs) != 2 || obs[0] != 0.1 || gotOut.Measure != "latency" {
		t.Fatalf("latency observations = %v (%+v)", obs, gotOut)
	}
	// Observations is empty for every non-latency model.
	if obs := outVal("count", "503").Observations(ctx); obs != nil {
		t.Fatalf("count Observations = %v, want none", obs)
	}
	if obs := (ValueModel{Const: ptr(1.0)}).Observations(ctx); obs != nil {
		t.Fatalf("const Observations = %v, want none", obs)
	}
}

func TestCausalValueRejectsLabelScales(t *testing.T) {
	scaled := func(rate, value *float64) map[string]ValueModel {
		return map[string]ValueModel{"code": {Enum: []EnumEntry{
			{Value: "200", Weight: 1, RateScale: rate, ValueScale: value},
			{Value: "503", Weight: 1},
		}}}
	}
	vals := []struct {
		name       string
		instrument string
		value      ValueModel
	}{
		{"resource gauge", InstrumentGauge, resVal("pool", "utilization")},
		{"resource counter", InstrumentCounter, resVal("pool", "rejected")},
		{"outcomes count", InstrumentCounter, outVal("count", "5xx")},
		{"outcomes share", InstrumentGauge, outVal("share", "5xx")},
		{"outcomes latency", InstrumentHistogram, outVal("latency", "5xx")},
	}
	for _, v := range vals {
		base := MetricSpec{Name: "m", Instrument: v.instrument, Value: v.value}
		if v.instrument == InstrumentHistogram {
			base.Buckets = []float64{1, 2}
		}
		// Sanity: the unscaled declaration is valid.
		base.Labels = scaled(nil, nil)
		if err := base.Validate(); err != nil {
			t.Fatalf("%s: unscaled labels must validate: %v", v.name, err)
		}
		rs := base
		rs.Labels = scaled(ptr(2.0), nil)
		wantErr(t, rs.Validate(), "rate_scale is not valid with a resource or outcomes value")
		vs := base
		vs.Labels = scaled(nil, ptr(2.0))
		wantErr(t, vs.Validate(), "value_scale is not valid with a resource or outcomes value")
	}
}
