// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/ledger"
	"github.com/rknightion/synthkit/internal/shape"
	"github.com/rknightion/synthkit/internal/sink/loki"
	"github.com/rknightion/synthkit/internal/telemetryspec"
)

func TestAppLogProjectionStableRandomDrawOrder(t *testing.T) {
	now := time.Date(2026, 9, 27, 9, 30, 0, 0, time.UTC)
	fields := map[string]telemetryspec.ValueModel{}
	for i := 0; i < 8; i++ {
		fields[string(rune('a'+i))] = telemetryspec.ValueModel{
			FloatRange: &telemetryspec.FloatRange{Min: 0, Max: 1},
		}
	}
	built, err := build(&Config{
		Traffic: Traffic{OffPeakRPS: 1, PeakRPS: 1},
		Services: []ServiceNode{{
			Name:  "service",
			Type:  "web",
			Entry: true,
			Logs: []telemetryspec.LogSpec{{
				Source: "app",
				Body:   fields,
			}},
		}},
	}, core.Binding{Name: "stable-log-projection"})
	if err != nil {
		t.Fatalf("build app workload: %v", err)
	}
	w := built.(*Workload)
	request := &ledger.Request{
		Correlation: ledger.NewCorrelationFromSeed("stable-log-projection"),
		Workload:    "stable-log-projection",
		Env:         "prod",
		Route:       "GET /",
		Start:       now,
		Duration:    time.Second,
		Outcome:     ledger.OutcomeSuccess,
		StatusCode:  200,
	}
	batch := []*ledger.Request{request}

	project := func() []byte {
		t.Helper()
		writer := &appDeterminismLogWriter{}
		world := &core.World{Shape: shape.New("", nil), Logs: writer}
		if err := w.projectLogs(context.Background(), world, batch); err != nil {
			t.Fatalf("project logs: %v", err)
		}
		encoded, err := json.Marshal(writer.batch)
		if err != nil {
			t.Fatalf("encode log batch: %v", err)
		}
		return encoded
	}

	first := project()
	for run := 2; run <= 5; run++ {
		got := project()
		if !bytes.Equal(first, got) {
			t.Fatalf("same seeded log projection changed on run %d:\nfirst: %s\n  got: %s", run, first, got)
		}
	}
}

type appDeterminismLogWriter struct {
	batch []loki.Stream
}

func (w *appDeterminismLogWriter) Write(_ context.Context, batch []loki.Stream) error {
	w.batch = append(w.batch, batch...)
	return nil
}
