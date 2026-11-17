// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/config"
	"github.com/rknightion/synthkit/internal/control"
	"github.com/rknightion/synthkit/internal/ha"
	"github.com/rknightion/synthkit/internal/operationalerr"
	"github.com/rknightion/synthkit/internal/pushhook"
	"github.com/rknightion/synthkit/internal/pushstatus"
	"github.com/rknightion/synthkit/internal/runner"
)

// evaluateLanes folds observed pushes through the shared composition-root lane wiring and the
// control-plane verdict, as both the ordinary and HA readiness closures do.
func evaluateLanes(ps *pushstatus.Store, lanes []runner.DeliveryReadinessLane) control.ReadinessReport {
	required := make([]string, 0, len(lanes))
	for _, lane := range lanes {
		required = append(required, lane.Name)
	}
	return control.EvaluateReadiness(control.ReadinessInput{
		ProcessRunning: true, HTTPServing: true,
		Blueprints:     control.BlueprintReadiness{Loaded: 1, Active: 1},
		PersistedState: control.PersistedStateReadiness{Writable: true},
		Lanes:          ps.SnapshotLanes(), RequiredLanes: required, LiveDeliveryExpected: true,
	})
}

func push(ps *pushstatus.Store, sink string, code operationalerr.Code) {
	ev := pushhook.Event{Sink: sink, Status: 200, Items: 1}
	if code != "" {
		ev = pushhook.Event{Sink: sink, ErrorCode: code}
	}
	ps.Observer()(context.Background(), ev)
}

// waitPast blocks until a success recorded now is older than bound at millisecond resolution.
func waitPast(bound time.Duration) { time.Sleep(bound + 20*time.Millisecond) }

// SKT-0180 reproduction: a healthy scheduled lane plus a request-driven lane that pushed once and
// then went quiet (low request volume) reported 503 delivery_stale. Quiet request-driven delivery
// is not a fault; only scheduled lanes carry a missing-evidence bound.
func TestReadinessQuietRequestDrivenLaneWithHealthyScheduledLaneIsReady(t *testing.T) {
	lanes := []runner.DeliveryReadinessLane{
		{Name: "promrw", EmissionInterval: time.Hour, Freshness: runner.FreshnessScheduled},
		{Name: "otlp", EmissionInterval: time.Millisecond, Freshness: runner.FreshnessRequestDriven},
	}
	ps := pushstatus.NewStore()
	ps.ConfigureLanes(readinessLaneConfigs(lanes, 0, false))
	push(ps, "promrw", "")
	push(ps, "otlp", "")
	waitPast(time.Millisecond)
	if got := evaluateLanes(ps, lanes); !got.Ready || !got.LiveReady || len(got.ReasonCodes) != 0 {
		t.Fatalf("quiet request-driven lane held readiness red: ready=%v live=%v codes=%v reasons=%v", got.Ready, got.LiveReady, got.ReasonCodes, got.Reasons)
	}
}

func TestReadinessScheduledLaneStalenessStillFails(t *testing.T) {
	lanes := []runner.DeliveryReadinessLane{
		{Name: "promrw", EmissionInterval: time.Millisecond, Freshness: runner.FreshnessScheduled},
		{Name: "otlp", EmissionInterval: time.Hour, Freshness: runner.FreshnessRequestDriven},
	}
	ps := pushstatus.NewStore()
	ps.ConfigureLanes(readinessLaneConfigs(lanes, 0, false))
	push(ps, "promrw", "")
	push(ps, "otlp", "")
	waitPast(time.Millisecond)
	got := evaluateLanes(ps, lanes)
	if got.Ready || got.LiveReady || !slices.Contains(got.ReasonCodes, control.ReadinessDeliveryStale) {
		t.Fatalf("stale scheduled lane must fail readiness with delivery_stale: %+v", got.ReasonCodes)
	}
}

func TestReadinessRequestDrivenCurrentErrorAndFirstAttemptStillFail(t *testing.T) {
	lanes := []runner.DeliveryReadinessLane{
		{Name: "promrw", EmissionInterval: time.Hour, Freshness: runner.FreshnessScheduled},
		{Name: "otlp", EmissionInterval: time.Millisecond, Freshness: runner.FreshnessRequestDriven},
		{Name: "loki", EmissionInterval: time.Millisecond, Freshness: runner.FreshnessRequestDriven},
	}
	ps := pushstatus.NewStore()
	ps.ConfigureLanes(readinessLaneConfigs(lanes, 0, false))
	push(ps, "promrw", "")
	push(ps, "otlp", "")
	push(ps, "otlp", operationalerr.CodeTransport)
	waitPast(time.Millisecond)
	got := evaluateLanes(ps, lanes)
	if got.Ready || got.LiveReady {
		t.Fatalf("request-driven current error or missing first push became ready: %+v", got.ReasonCodes)
	}
	for _, code := range []control.ReadinessReasonCode{control.ReadinessDeliveryError, control.ReadinessDeliveryNotAttempted} {
		if !slices.Contains(got.ReasonCodes, code) {
			t.Fatalf("reason codes %v missing %s", got.ReasonCodes, code)
		}
	}
	if slices.Contains(got.ReasonCodes, control.ReadinessDeliveryStale) {
		t.Fatalf("request-driven lane reported stale: %v", got.ReasonCodes)
	}
}

// The runner's real manifest classification reaches the shared lane wiring: a workload trace lane
// is request-driven and a cluster metrics lane is scheduled.
func TestReadinessLaneConfigsFollowRunnerFreshness(t *testing.T) {
	configs := map[string]pushstatus.LaneConfig{}
	for _, lc := range readinessLaneConfigs(manifestRunner(t).DeliveryReadinessLanes(), 5*time.Second, false) {
		configs[lc.Name] = lc
	}
	if lc, ok := configs["otlp"]; !ok || !lc.RequestDriven {
		t.Fatalf("workload trace lane = %+v (present %v), want request-driven", lc, ok)
	}
	if lc, ok := configs["promrw"]; !ok || lc.RequestDriven || lc.FreshAfter <= 5*time.Second {
		t.Fatalf("metrics lane = %+v (present %v), want scheduled with its cadence bound", lc, ok)
	}
}

// The HA composition root configures its own pushstatus store; its leader status must carry the
// request-driven class from the runner manifest, not only the ordinary root.
func TestHAReadinessConfiguresRequestDrivenLanes(t *testing.T) {
	gate := ha.NewGate()
	v, _ := readinessHAViewWith(t, gate, func(cfg *config.Config) {
		dir := filepath.Join(t.TempDir(), "bp")
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "manifest.yaml"), []byte(manifestTestYAML), 0o644); err != nil {
			t.Fatal(err)
		}
		cfg.BlueprintsDir, cfg.BlueprintNames = dir, []string{"manifest"}
	})
	l := &haLifecycle{gate: gate}
	v.readinessFacts = l.readinessFacts
	if err := gate.Activate(context.Background(), func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	v.handler.ServeHTTP(response, httptest.NewRequest("GET", "/control/status", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status HTTP %d: %s", response.Code, response.Body.String())
	}
	var status control.StatusReport
	if err := json.Unmarshal(response.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status.Readiness == nil {
		t.Fatal("HA status carries no readiness report")
	}
	lanes := map[string]pushstatus.LaneStatus{}
	for _, lane := range status.Readiness.Lanes {
		lanes[lane.Name] = lane
	}
	if got, ok := lanes["otlp"]; !ok || !got.Configured || !got.RequestDriven {
		t.Fatalf("HA otlp lane = %+v (present %v), want configured request-driven", got, ok)
	}
	if got, ok := lanes["promrw"]; !ok || !got.Configured || got.RequestDriven {
		t.Fatalf("HA promrw lane = %+v (present %v), want configured scheduled", got, ok)
	}
}
