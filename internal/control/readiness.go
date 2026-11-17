// SPDX-License-Identifier: AGPL-3.0-only

package control

import (
	"fmt"
	"slices"

	"github.com/rknightion/synthkit/internal/pushstatus"
)

// BlueprintReadiness is the bootstrap/load view used by operational readiness. Loaded and
// Skipped describe the load result; Active is the count still intended to emit after persisted
// control state is applied. Readiness requires Active to be non-zero.
type BlueprintReadiness struct {
	Loaded  int `json:"loaded"`
	Skipped int `json:"skipped"`
	Active  int `json:"active"`
}

// PersistedStateReadiness records the startup write probe for the persisted control-state
// directory. It is separate from PersistHealth because a new process must prove writability
// before the first operator mutation has had a chance to persist anything.
type PersistedStateReadiness struct {
	Writable bool   `json:"writable"`
	Error    string `json:"error"`
}

// ReadinessReasonCode is a stable, non-sensitive explanation suitable for the public probe.
// Detailed authenticated reasons may include lane names and errors; these codes never do.
type ReadinessReasonCode string

const (
	ReadinessHABootstrapIncomplete     ReadinessReasonCode = "ha_bootstrap_incomplete"
	ReadinessHATransitioning           ReadinessReasonCode = "ha_transitioning"
	ReadinessProcessNotRunning         ReadinessReasonCode = "process_not_running"
	ReadinessHTTPNotServing            ReadinessReasonCode = "http_not_serving"
	ReadinessSetupRequired             ReadinessReasonCode = "setup_required"
	ReadinessNoActiveBlueprint         ReadinessReasonCode = "no_active_blueprint"
	ReadinessPersistedStateNotWritable ReadinessReasonCode = "persisted_state_not_writable"
	ReadinessDeliveryNotReady          ReadinessReasonCode = "delivery_not_ready"
	ReadinessDeliveryNotAttempted      ReadinessReasonCode = "delivery_not_attempted"
	ReadinessDeliveryError             ReadinessReasonCode = "delivery_error"
	ReadinessDeliveryStale             ReadinessReasonCode = "delivery_stale"
	ReadinessLiveDeliveryDisabled      ReadinessReasonCode = "live_delivery_disabled"
	ReadinessNoLiveDeliveryLane        ReadinessReasonCode = "no_live_delivery_lane"
)

// HAReadiness records bootstrap and lifecycle facts for lease mode only. A nil HA input
// preserves non-HA readiness. Transitioning includes activation, termination, and revocation.
type HAReadiness struct {
	Standby         bool
	Transitioning   bool
	ConfigLoaded    bool
	RunnerBuilt     bool
	PreflightPassed bool
}

// ReadinessInput is assembled at the composition root. The evaluator intentionally does not
// depend on runner, HTTP, or store implementations: those layers supply their factual state and
// retain their existing ownership boundaries.
type ReadinessInput struct {
	HA                   *HAReadiness
	ProcessRunning       bool
	HTTPServing          bool
	SetupRequired        bool
	Blueprints           BlueprintReadiness
	PersistedState       PersistedStateReadiness
	Lanes                []pushstatus.LaneStatus
	RequiredLanes        []string
	LiveDeliveryExpected bool
}

// ReadinessReport is the body for a local HTTP readiness endpoint. HTTPReady means this process
// has assembled and is serving its HTTP handler; Ready adds blueprint, state-volume, and delivery
// gates for leaders and non-HA processes; a standby uses bootstrap gates only.
// LiveReady is kept explicit so dry-run can be reported as intentionally configured while
// never being presented as a live deployment.
type ReadinessReport struct {
	Running        bool                    `json:"running"`
	HTTPReady      bool                    `json:"http_ready"`
	Ready          bool                    `json:"ready"`
	LiveReady      bool                    `json:"live_ready"`
	SetupRequired  bool                    `json:"setup_required"`
	Blueprints     BlueprintReadiness      `json:"blueprints"`
	PersistedState PersistedStateReadiness `json:"persisted_state"`
	Lanes          []pushstatus.LaneStatus `json:"lanes"`
	Reasons        []string                `json:"reasons"`
	ReasonCodes    []ReadinessReasonCode   `json:"reason_codes"`
}

// ReadinessProbe is the credential-free healthcheck representation. It deliberately omits lane
// names, endpoint errors, filesystem paths, and detailed reason strings. Stable reason codes make
// a failed probe diagnosable without exposing operational detail; authenticated operators get the
// complete ReadinessReport through /control/status.
type ReadinessProbe struct {
	Running        bool               `json:"running"`
	HTTPReady      bool               `json:"http_ready"`
	Ready          bool               `json:"ready"`
	LiveReady      bool               `json:"live_ready"`
	SetupRequired  bool               `json:"setup_required"`
	Blueprints     BlueprintReadiness `json:"blueprints"`
	PersistedState struct {
		Writable bool `json:"writable"`
	} `json:"persisted_state"`
	ReasonCodes []ReadinessReasonCode `json:"reason_codes"`
}

// BlueprintLister names the blueprints loaded into the running process. The runner implements it.
type BlueprintLister interface {
	Blueprints() []string
}

// ReadinessProbeBlueprints is the public probe with the opt-in per-blueprint emitting flags
// (READINESS_BLUEPRINT_FLAGS). It embeds ReadinessProbe unchanged, so every existing field keeps
// its name, order and value and the new field is appended after them. BlueprintEmitting maps a
// loaded blueprint's plain name to one boolean; the value type cannot carry a reason, error, path,
// lane or secret. It is never nil when serialised, so an empty set is {} rather than null.
type ReadinessProbeBlueprints struct {
	ReadinessProbe
	BlueprintEmitting map[string]bool `json:"blueprint_emitting"`
}

// BlueprintEmittingFlags derives one emitting boolean per loaded blueprint.
//
// A blueprint is emitting when all of these hold: it is loaded into the running process (a
// blueprint skipped at load is not in loaded, so it is absent rather than false); it is not in
// state.DisabledBlueprints (the same test the runner applies to decide whether a blueprint
// ticks); the process is in a state that runs ticks:
// running, serving HTTP, not in setup mode, the HA gate leads (leader is false on a standby), and
// no HA bootstrap or transition reason is outstanding; and, when a schema source supplies
// constructs, at least one of the blueprint's constructs is enabled: its kind is not in
// state.DisabledKinds and its ConstructKey is not in state.DisabledConstructs. A loaded blueprint
// that declares no schema constructs (for example a workload-only one) cannot be judged at
// construct level and keeps its blueprint-level state; haveSchema false skips the construct test
// for every blueprint. Delivery health is deliberately not an input: a failing sink lane leaves a
// blueprint emitting and is visible as ready, live_ready and reason_codes. Names in
// state.DisabledBlueprints that are not loaded never appear.
func BlueprintEmittingFlags(report ReadinessReport, loaded []string, state State, constructs []ConstructInfo, haveSchema, leader bool) map[string]bool {
	runs := report.Running && report.HTTPReady && !report.SetupRequired && leader
	for _, code := range report.ReasonCodes {
		if code == ReadinessHABootstrapIncomplete || code == ReadinessHATransitioning {
			runs = false
		}
	}
	declared := map[string]int{}
	enabled := map[string]int{}
	if haveSchema {
		for _, c := range constructs {
			declared[c.Blueprint]++
			if !slices.Contains(state.DisabledKinds, c.Kind) &&
				!slices.Contains(state.DisabledConstructs, ConstructKey(c.Blueprint, c.Kind, c.Name)) {
				enabled[c.Blueprint]++
			}
		}
	}
	out := make(map[string]bool, len(loaded))
	for _, name := range loaded {
		constructsOK := !haveSchema || declared[name] == 0 || enabled[name] > 0
		out[name] = runs && constructsOK && !slices.Contains(state.DisabledBlueprints, name)
	}
	return out
}

func (r ReadinessReport) Probe() ReadinessProbe {
	p := ReadinessProbe{
		Running: r.Running, HTTPReady: r.HTTPReady, Ready: r.Ready, LiveReady: r.LiveReady,
		SetupRequired: r.SetupRequired,
		Blueprints:    r.Blueprints,
		ReasonCodes:   append([]ReadinessReasonCode{}, r.ReasonCodes...),
	}
	p.PersistedState.Writable = r.PersistedState.Writable
	return p
}

// EvaluateReadiness folds independently observed process, HTTP, blueprint, persisted-state, and
// lane-delivery facts into a strict readiness verdict. A missing first push remains red; only a
// LiveReady lane is green. pushstatus decides LiveReady: a scheduled lane needs a success within
// its missing-evidence bound, while a request-driven lane needs its last outcome to be a success
// and is never stale for silence. Disabled lanes are intentional and remain
// visible, but a mode with no expected live delivery (DRY_RUN) is never LiveReady.
func EvaluateReadiness(in ReadinessInput) ReadinessReport {
	report := ReadinessReport{
		Running:        in.ProcessRunning,
		HTTPReady:      in.HTTPServing,
		SetupRequired:  in.SetupRequired,
		Blueprints:     in.Blueprints,
		PersistedState: in.PersistedState,
		Lanes:          append([]pushstatus.LaneStatus{}, in.Lanes...),
		Reasons:        []string{},
		ReasonCodes:    []ReadinessReasonCode{},
	}
	seenCodes := map[ReadinessReasonCode]struct{}{}
	addReason := func(code ReadinessReasonCode, reason string) {
		report.Reasons = append(report.Reasons, reason)
		if _, ok := seenCodes[code]; ok {
			return
		}
		seenCodes[code] = struct{}{}
		report.ReasonCodes = append(report.ReasonCodes, code)
	}
	if !report.Running {
		addReason(ReadinessProcessNotRunning, "process is not running")
	}
	if !report.HTTPReady {
		addReason(ReadinessHTTPNotServing, "HTTP handler is not serving")
	}
	if in.HA != nil {
		bootstrapReady := in.HA.ConfigLoaded && in.HA.RunnerBuilt && in.HA.PreflightPassed
		if !bootstrapReady {
			addReason(ReadinessHABootstrapIncomplete, "HA configuration, runner build, or credential preflight is incomplete")
		}
		if in.HA.Transitioning {
			addReason(ReadinessHATransitioning, "HA lifecycle is transitioning")
		}
		if in.HA.Standby || !bootstrapReady || in.HA.Transitioning {
			// A standby has no delivery evidence and has not probed state writability.
			// Do not represent either as a successful live deployment or write probe.
			if in.HA.Standby {
				report.PersistedState = PersistedStateReadiness{}
			}
			report.Ready = len(report.ReasonCodes) == 0
			return report
		}
	}
	if report.SetupRequired {
		addReason(ReadinessSetupRequired, "no blueprints selected; setup required")
	} else if report.Blueprints.Active <= 0 {
		addReason(ReadinessNoActiveBlueprint, "no intended blueprint is active")
	}
	if !report.PersistedState.Writable {
		reason := "persisted control state is not writable"
		if report.PersistedState.Error != "" {
			reason += ": " + report.PersistedState.Error
		}
		addReason(ReadinessPersistedStateNotWritable, reason)
	}
	if report.SetupRequired {
		report.LiveReady = false
		report.Ready = report.Running && report.HTTPReady && report.PersistedState.Writable
		return report
	}

	liveLaneCount := 0
	allRequiredReady := true
	lanesByName := make(map[string]pushstatus.LaneStatus, len(report.Lanes))
	for _, lane := range report.Lanes {
		lanesByName[lane.Name] = lane
	}
	seenRequired := make(map[string]struct{}, len(in.RequiredLanes))
	for _, name := range in.RequiredLanes {
		if name == "" {
			continue
		}
		if _, seen := seenRequired[name]; seen {
			continue
		}
		seenRequired[name] = struct{}{}
		lane, present := lanesByName[name]
		if !present || !lane.Configured {
			liveLaneCount++
			allRequiredReady = false
			addReason(ReadinessDeliveryNotReady, fmt.Sprintf("delivery lane %q is unconfigured", name))
			continue
		}
		if lane.Disabled {
			continue
		}
		liveLaneCount++
		if !lane.LiveReady {
			allRequiredReady = false
			code := ReadinessDeliveryNotReady
			switch lane.State {
			case pushstatus.LaneNotAttempted:
				code = ReadinessDeliveryNotAttempted
			case pushstatus.LaneError:
				code = ReadinessDeliveryError
			case pushstatus.LaneStaleSuccess:
				code = ReadinessDeliveryStale
			}
			addReason(code, fmt.Sprintf("delivery lane %q is %s", lane.Name, lane.State))
		}
	}
	if !in.LiveDeliveryExpected {
		addReason(ReadinessLiveDeliveryDisabled, "live delivery is disabled")
	} else if liveLaneCount == 0 {
		addReason(ReadinessNoLiveDeliveryLane, "no live delivery lane is configured")
	}

	report.LiveReady = in.LiveDeliveryExpected && liveLaneCount > 0 && allRequiredReady
	report.Ready = report.Running && report.HTTPReady && report.Blueprints.Active > 0 && report.PersistedState.Writable && report.LiveReady
	return report
}
