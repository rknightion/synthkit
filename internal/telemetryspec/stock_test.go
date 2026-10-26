// SPDX-License-Identifier: AGPL-3.0-only

package telemetryspec

import (
	"strings"
	"testing"
)

func stockGauge(so StockOf, labels map[string]ValueModel) MetricSpec {
	return MetricSpec{Name: "open_items", Instrument: InstrumentGauge, Labels: labels, StockOf: &so}
}

// TestStockOfItemValidation: the item stock (residence) is opt-in beside the fluid stock and its
// fields are rejected on the fluid form; a stock gauge may declare projection labels.
func TestStockOfItemValidation(t *testing.T) {
	res := &Lognormal{Median: 85000, P95: 150000}
	two := 2.0
	lane := map[string]ValueModel{"lane": {Enum: []EnumEntry{{Value: "a"}, {Value: "b"}}}}
	for name, tc := range map[string]struct {
		m    MetricSpec
		want string
	}{
		"fluid":           {stockGauge(StockOf{Metric: "in_total", DwellSeconds: 60}, nil), ""},
		"fluid labels":    {stockGauge(StockOf{Metric: "in_total", DwellSeconds: 60}, lane), ""},
		"items":           {stockGauge(StockOf{Metric: "in_total", Residence: res}, lane), ""},
		"items age":       {stockGauge(StockOf{Metric: "in_total", Residence: res, StallMode: "latency_storm", Field: StockFieldOldestAge}, nil), ""},
		"both":            {stockGauge(StockOf{Metric: "in_total", DwellSeconds: 60, Residence: res}, nil), "exactly one of dwell_seconds and residence"},
		"neither":         {stockGauge(StockOf{Metric: "in_total"}, nil), "dwell_seconds must be finite and > 0"},
		"fluid stall":     {stockGauge(StockOf{Metric: "in_total", DwellSeconds: 60, StallMode: "x"}, nil), "need stock_of.residence"},
		"fluid field":     {stockGauge(StockOf{Metric: "in_total", DwellSeconds: 60, Field: StockFieldCount}, nil), "need stock_of.residence"},
		"bad residence":   {stockGauge(StockOf{Metric: "in_total", Residence: &Lognormal{Median: 0, P95: 1}}, nil), "stock_of.residence: lognormal: median 0 must be > 0"},
		"bad field":       {stockGauge(StockOf{Metric: "in_total", Residence: res, Field: "depth"}, nil), "stock_of.field \"depth\" invalid"},
		"blank stall":     {stockGauge(StockOf{Metric: "in_total", Residence: res, StallMode: " "}, nil), "surrounding whitespace"},
		"self":            {stockGauge(StockOf{Metric: "open_items", Residence: res}, nil), "must name another counter"},
		"value_scale":     {stockGauge(StockOf{Metric: "in_total", Residence: res}, map[string]ValueModel{"lane": {Enum: []EnumEntry{{Value: "a", ValueScale: &two}}}}), "value_scale is not valid with stock_of"},
		"counter instrum": {MetricSpec{Name: "open_items", Instrument: InstrumentCounter, StockOf: &StockOf{Metric: "in_total", Residence: res}}, "gauge only"},
	} {
		err := tc.m.Validate()
		switch {
		case tc.want == "" && err != nil:
			t.Fatalf("%s: unexpected error %v", name, err)
		case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
			t.Fatalf("%s: err %v, want %q", name, err, tc.want)
		}
	}
}
