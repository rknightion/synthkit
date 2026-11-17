// SPDX-License-Identifier: AGPL-3.0-only

package telemetryspec

import (
	"strings"
	"testing"
	"time"
)

func scheduledNow(period, phase time.Duration) MetricSpec {
	return MetricSpec{
		Name: "x", Source: "s", Instrument: InstrumentGauge,
		Value: ValueModel{Now: &NowModel{Period: period, Phase: phase}},
	}
}

// TestNowScheduleReadsLastBoundary: with a period the reading is the most recent epoch-aligned
// boundary (plus phase) at or before the tick time, and the default reads the tick time unchanged.
func TestNowScheduleReadsLastBoundary(t *testing.T) {
	at := time.Date(2026, 6, 10, 13, 22, 41, 500_000_000, time.UTC)
	want := func(h, m, s int) float64 {
		return float64(time.Date(2026, 6, 10, h, m, s, 0, time.UTC).Unix())
	}
	for _, c := range []struct {
		name          string
		period, phase time.Duration
		want          float64
	}{
		{"15m", 15 * time.Minute, 0, want(13, 15, 0)},
		{"15m phase 7m", 15 * time.Minute, 7 * time.Minute, want(13, 22, 0)},
		{"15m phase 14m wraps to previous period", 15 * time.Minute, 14 * time.Minute, want(13, 14, 0)},
	} {
		got, _ := ValueModel{Now: &NowModel{Period: c.period, Phase: c.phase}}.Eval(EvalCtx{Now: at})
		if got != c.want {
			t.Errorf("%s = %v, want %v", c.name, got, c.want)
		}
	}
	if got, _ := (ValueModel{Now: &NowModel{}}).Eval(EvalCtx{Now: at}); got != float64(at.Unix())+0.5 {
		t.Errorf("default = %v, want the tick time %v", got, float64(at.Unix())+0.5)
	}
	exact := time.Date(2026, 6, 10, 13, 30, 0, 0, time.UTC)
	if got, _ := (ValueModel{Now: &NowModel{Period: 15 * time.Minute}}).Eval(EvalCtx{Now: exact}); got != float64(exact.Unix()) {
		t.Errorf("on a boundary = %v, want %v", got, exact.Unix())
	}
}

func TestNowScheduleValidation(t *testing.T) {
	for _, c := range []struct {
		name          string
		period, phase time.Duration
		wantErr       string
	}{
		{"ok", 15 * time.Minute, 5 * time.Minute, ""},
		{"24h ok", 24 * time.Hour, 0, ""},
		{"over 24h", 24*time.Hour + time.Second, 0, "period"},
		{"negative period", -time.Minute, 0, "period"},
		{"phase equals period", time.Minute, time.Minute, "phase"},
		{"phase over period", time.Minute, 2 * time.Minute, "phase"},
		{"negative phase", time.Minute, -time.Second, "phase"},
		{"phase without period", 0, time.Minute, "phase"},
	} {
		err := scheduledNow(c.period, c.phase).Validate()
		switch {
		case c.wantErr == "" && err != nil:
			t.Errorf("%s: %v", c.name, err)
		case c.wantErr != "" && (err == nil || !strings.Contains(err.Error(), c.wantErr)):
			t.Errorf("%s: err = %v, want it to mention %q", c.name, err, c.wantErr)
		}
	}
	m := scheduledNow(time.Minute, 0)
	m.Instrument = InstrumentCounter
	if err := m.Validate(); err == nil {
		t.Error("period on a counter must be rejected like any now")
	}
}
