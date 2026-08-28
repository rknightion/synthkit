// SPDX-License-Identifier: AGPL-3.0-only

package control

import (
	"fmt"

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

// ReadinessInput is assembled at the composition root. The evaluator intentionally does not
// depend on runner, HTTP, or store implementations: those layers supply their factual state and
// retain their existing ownership boundaries.
type ReadinessInput struct {
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
// gates. LiveReady is kept explicit so dry-run can be reported as intentionally configured while
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
// current success for every declared live lane is green. Disabled lanes are intentional and remain
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
