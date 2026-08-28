// SPDX-License-Identifier: AGPL-3.0-only

package control

import (
	"strings"
	"testing"

	"github.com/rknightion/synthkit/internal/pushstatus"
)

func TestEvaluateReadinessFreshLaneIsRedUntilFirstSuccess(t *testing.T) {
	got := EvaluateReadiness(ReadinessInput{
		ProcessRunning:       true,
		HTTPServing:          true,
		Blueprints:           BlueprintReadiness{Loaded: 2, Skipped: 1, Active: 2},
		PersistedState:       PersistedStateReadiness{Writable: true},
		LiveDeliveryExpected: true,
		RequiredLanes:        []string{"promrw"},
		Lanes: []pushstatus.LaneStatus{{
			Name: "promrw", Configured: true, State: pushstatus.LaneNotAttempted,
		}},
	})
	if got.Ready || got.LiveReady {
		t.Fatalf("fresh lane must not make readiness green: %+v", got)
	}
	if got.Blueprints.Loaded != 2 || got.Blueprints.Skipped != 1 || got.Blueprints.Active != 2 {
		t.Fatalf("blueprint counts lost: %+v", got.Blueprints)
	}
	if !containsReason(got.Reasons, "promrw") || !containsReason(got.Reasons, "not_attempted") {
		t.Fatalf("expected first-attempt reason, got %v", got.Reasons)
	}
}

func TestEvaluateReadinessBecomesGreenAfterAllLiveLanesSucceed(t *testing.T) {
	got := EvaluateReadiness(ReadinessInput{
		ProcessRunning:       true,
		HTTPServing:          true,
		Blueprints:           BlueprintReadiness{Loaded: 1, Active: 1},
		PersistedState:       PersistedStateReadiness{Writable: true},
		LiveDeliveryExpected: true,
		RequiredLanes:        []string{"loki", "promrw"},
		Lanes: []pushstatus.LaneStatus{
			{Name: "promrw", Configured: true, State: pushstatus.LaneSuccess, LiveReady: true},
			{Name: "loki", Configured: true, State: pushstatus.LaneSuccess, LiveReady: true},
		},
	})
	if !got.Ready || !got.LiveReady || len(got.Reasons) != 0 {
		t.Fatalf("all live lanes succeeded: %+v", got)
	}
}

func TestEvaluateReadinessSetupRequiredIsOperationalButNotLive(t *testing.T) {
	got := EvaluateReadiness(ReadinessInput{
		ProcessRunning:       true,
		HTTPServing:          true,
		SetupRequired:        true,
		Blueprints:           BlueprintReadiness{},
		PersistedState:       PersistedStateReadiness{Writable: true},
		LiveDeliveryExpected: true,
	})
	if !got.Ready || got.LiveReady || !got.SetupRequired {
		t.Fatalf("setup mode must be operational but not live-delivery-ready: %+v", got)
	}
	if got.Lanes == nil {
		t.Fatal("setup readiness lanes must serialize as an empty array, not null")
	}
	if !containsReason(got.Reasons, "no blueprints selected") {
		t.Fatalf("setup mode must explain the operator action: %v", got.Reasons)
	}
}

func TestEvaluateReadinessRejectsNoActiveBlueprintAndUnwritableState(t *testing.T) {
	got := EvaluateReadiness(ReadinessInput{
		ProcessRunning:       true,
		HTTPServing:          true,
		Blueprints:           BlueprintReadiness{Loaded: 3, Skipped: 2, Active: 0},
		PersistedState:       PersistedStateReadiness{Writable: false, Error: "permission denied"},
		LiveDeliveryExpected: true,
		RequiredLanes:        []string{"promrw"},
		Lanes:                []pushstatus.LaneStatus{{Name: "promrw", Configured: true, State: pushstatus.LaneSuccess, LiveReady: true}},
	})
	if got.Ready {
		t.Fatalf("no active blueprint and unwritable state must fail: %+v", got)
	}
	if !containsReason(got.Reasons, "no intended blueprint") || !containsReason(got.Reasons, "permission denied") {
		t.Fatalf("missing hard-gate reasons: %v", got.Reasons)
	}
}

func TestEvaluateReadinessMarksDryRunAsConfiguredButNotLiveReady(t *testing.T) {
	got := EvaluateReadiness(ReadinessInput{
		ProcessRunning:       true,
		HTTPServing:          true,
		Blueprints:           BlueprintReadiness{Loaded: 1, Active: 1},
		PersistedState:       PersistedStateReadiness{Writable: true},
		LiveDeliveryExpected: false,
		RequiredLanes:        []string{"promrw"},
		Lanes: []pushstatus.LaneStatus{{
			Name: "promrw", Configured: true, Disabled: true, DisabledReason: "dry_run", State: pushstatus.LaneDisabled,
		}},
	})
	if got.Ready || got.LiveReady || !containsReason(got.Reasons, "live delivery is disabled") {
		t.Fatalf("dry run must not be live-ready: %+v", got)
	}
}

func TestEvaluateReadinessUsesActiveBlueprintFeedsAsDenominator(t *testing.T) {
	got := EvaluateReadiness(ReadinessInput{
		ProcessRunning:       true,
		HTTPServing:          true,
		Blueprints:           BlueprintReadiness{Loaded: 1, Active: 1},
		PersistedState:       PersistedStateReadiness{Writable: true},
		LiveDeliveryExpected: true,
		RequiredLanes:        []string{"promrw"},
		Lanes: []pushstatus.LaneStatus{
			{Name: "promrw", Configured: true, State: pushstatus.LaneSuccess, LiveReady: true},
			{Name: "otlplogs", Configured: true, State: pushstatus.LaneNotAttempted},
			{Name: "historical", Configured: false, State: pushstatus.LaneUnconfigured},
		},
	})
	if !got.Ready || !got.LiveReady || containsReason(got.Reasons, "otlplogs") || containsReason(got.Reasons, "historical") {
		t.Fatalf("lane outside the current active-blueprint denominator held readiness red: %+v", got)
	}
}

func containsReason(reasons []string, want string) bool {
	for _, reason := range reasons {
		if strings.Contains(reason, want) {
			return true
		}
	}
	return false
}
