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
}

// ReadinessProbe is the credential-free healthcheck representation. It deliberately omits lane
// names, endpoint errors, filesystem paths, and reason strings; authenticated operators get the
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
}

func (r ReadinessReport) Probe() ReadinessProbe {
	p := ReadinessProbe{
		Running: r.Running, HTTPReady: r.HTTPReady, Ready: r.Ready, LiveReady: r.LiveReady,
		SetupRequired: r.SetupRequired,
		Blueprints:    r.Blueprints,
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
	}
	if !report.Running {
		report.Reasons = append(report.Reasons, "process is not running")
	}
	if !report.HTTPReady {
		report.Reasons = append(report.Reasons, "HTTP handler is not serving")
	}
	if report.SetupRequired {
		report.Reasons = append(report.Reasons, "no blueprints selected; setup required")
	} else if report.Blueprints.Active <= 0 {
		report.Reasons = append(report.Reasons, "no intended blueprint is active")
	}
	if !report.PersistedState.Writable {
		reason := "persisted control state is not writable"
		if report.PersistedState.Error != "" {
			reason += ": " + report.PersistedState.Error
		}
		report.Reasons = append(report.Reasons, reason)
	}
	if report.SetupRequired {
		report.LiveReady = false
		report.Ready = report.Running && report.HTTPReady && report.PersistedState.Writable
		return report
	}

	liveLaneCount := 0
	hasUnconfiguredLane := false
	for _, lane := range report.Lanes {
		if !lane.Configured {
			hasUnconfiguredLane = true
			report.Reasons = append(report.Reasons, fmt.Sprintf("delivery lane %q is unconfigured", lane.Name))
			continue
		}
		if lane.Disabled {
			continue
		}
		liveLaneCount++
		if !lane.LiveReady {
			report.Reasons = append(report.Reasons, fmt.Sprintf("delivery lane %q is %s", lane.Name, lane.State))
		}
	}
	if !in.LiveDeliveryExpected {
		report.Reasons = append(report.Reasons, "live delivery is disabled")
	} else if liveLaneCount == 0 {
		report.Reasons = append(report.Reasons, "no live delivery lane is configured")
	}

	report.LiveReady = in.LiveDeliveryExpected && liveLaneCount > 0 && !hasUnconfiguredLane
	for _, lane := range report.Lanes {
		if lane.Configured && !lane.Disabled && !lane.LiveReady {
			report.LiveReady = false
			break
		}
	}
	report.Ready = report.Running && report.HTTPReady && report.Blueprints.Active > 0 && report.PersistedState.Writable && report.LiveReady
	return report
}
