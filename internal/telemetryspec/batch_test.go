// SPDX-License-Identifier: AGPL-3.0-only

package telemetryspec

import (
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/shape"
)

func batchedShape(period time.Duration) ValueModel {
	return ValueModel{Shape: &ShapeModel{Base: 10, Batch: &shape.Batch{Period: period}}}
}

// A batch declares arrivals: it is valid on a counter's shape value only.
func TestShapeBatchCounterOnly(t *testing.T) {
	ok := MetricSpec{Name: "x", Source: "s", Instrument: InstrumentCounter, Value: batchedShape(30 * time.Minute)}
	if err := ok.Validate(); err != nil {
		t.Fatalf("counter: %v", err)
	}
	for _, inst := range []string{InstrumentGauge, InstrumentHistogram} {
		m := MetricSpec{Name: "x", Source: "s", Instrument: inst, Buckets: []float64{1}, Value: batchedShape(30 * time.Minute)}
		if err := m.Validate(); err == nil || !strings.Contains(err.Error(), "counter") {
			t.Fatalf("%s: want counter-only error, got %v", inst, err)
		}
	}
}

func TestShapeBatchRejectsIncidentValue(t *testing.T) {
	iv := 5.0
	v := batchedShape(30 * time.Minute)
	v.Shape.Mode, v.Shape.IncidentValue = "backlog", &iv
	m := MetricSpec{Name: "x", Source: "s", Instrument: InstrumentCounter, Value: v}
	if err := m.Validate(); err == nil || !strings.Contains(err.Error(), "incident_value") {
		t.Fatalf("want an incident_value rejection, got %v", err)
	}
}

func TestShapeBatchValidatesCadence(t *testing.T) {
	for _, d := range []time.Duration{0, -time.Minute, 25 * time.Hour} {
		m := MetricSpec{Name: "x", Source: "s", Instrument: InstrumentCounter, Value: batchedShape(d)}
		if err := m.Validate(); err == nil || !strings.Contains(err.Error(), "batch") {
			t.Fatalf("period %v: want a batch error, got %v", d, err)
		}
	}
}

// batch is a metric value only: no label, log body or span attribute reads arrivals.
func TestShapeBatchCapability(t *testing.T) {
	v := batchedShape(30 * time.Minute)
	if err := checkValue(v, posMetricValue, "w"); err != nil {
		t.Fatalf("metric value: %v", err)
	}
	for _, pos := range []position{posBody, posAttr} {
		if err := checkValue(v, pos, "w"); err == nil {
			t.Fatalf("position %d must reject batch", pos)
		}
	}
}
