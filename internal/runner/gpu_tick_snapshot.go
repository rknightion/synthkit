// SPDX-License-Identifier: AGPL-3.0-only

package runner

import (
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/rknightion/synthkit/internal/control"
	"github.com/rknightion/synthkit/internal/failuremode"
	"github.com/rknightion/synthkit/internal/shape"
)

// gpuTickSnapshot freezes the control inputs read by the shape Live seam. Other
// control APIs retain their current read-at-request-time behavior.
type gpuTickSnapshot struct {
	now     time.Time
	state   control.State
	runtime *shape.Engine
}

func (r *Runner) captureGPUControl(bp *bpRuntime, now time.Time) *gpuTickSnapshot {
	capture := &gpuTickSnapshot{now: now}
	if st := r.ctl.Load(); st != nil {
		capture.state = control.State{
			Failures:         maps.Clone(st.Failures),
			ActiveScenarios:  slices.Clone(st.ActiveScenarios),
			RuntimeIncidents: slices.Clone(st.RuntimeIncidents),
		}
		// Build from the SAME state revision. Loading rtEng separately could pair
		// old incident windows with new control state during ApplyControl.
		if specs := runtimeSpecsFor(bp.name, capture.state.RuntimeIncidents); len(specs) > 0 {
			capture.runtime = shape.New(bp.eng.Loc().String(), specs)
		}
	}
	return capture
}

func (r *Runner) prepareGPUTick(bp *bpRuntime, now time.Time) {
	bp.gpuTick.Store(r.captureGPUControl(bp, now))
}

func (bp *bpRuntime) gpuLiveFailures(capture *gpuTickSnapshot, mode string) []shape.LiveFailure {
	st := &capture.state
	var out []shape.LiveFailure
	expand := func(scope string, intensity float64) {
		for _, name := range bp.liveScopes(mode, scope) {
			out = append(out, shape.LiveFailure{Enabled: true, Intensity: intensity, Scope: name})
		}
	}
	if f, ok := st.Failures[mode]; ok && f.Enabled {
		expand(f.Scope, f.Intensity)
	}
	for _, sc := range bp.scenarios {
		if !slices.Contains(st.ActiveScenarios, bp.name+"/"+sc.Name) {
			continue
		}
		for _, effect := range sc.Effects {
			if effect.Mode != mode {
				continue
			}
			intensity := effect.Intensity
			if intensity <= 0 {
				intensity = 1
			}
			expand(effect.Target, intensity)
		}
	}
	if capture.runtime != nil {
		seen := map[string]bool{}
		for _, ri := range st.RuntimeIncidents {
			if ri.Blueprint != bp.name || ri.Mode != mode || seen[ri.Target] {
				continue
			}
			seen[ri.Target] = true
			// The runtime engine holds the incident's literal target, so it is evaluated there
			// and the verdict is then expanded to the concrete scopes the shape engine matches.
			if active, intensity := capture.runtime.Eval(capture.now, mode, ri.Target); active {
				for _, name := range bp.liveScopes(mode, ri.Target) {
					out = append(out, shape.LiveFailure{Enabled: true, Intensity: intensity, Scope: name})
				}
			}
		}
	}
	return out
}

// liveScopes turns a live failure's target into the concrete scopes the shape engine matches
// exactly, with the loader's scheduled expansion: "" and a bare name stay as they are, and
// "<axis>:*" becomes every declared target of that axis. A "resource:*" of a resource-axis mode
// is the declared resources of the mode's kind alone. An axis with no target yields none.
func (bp *bpRuntime) liveScopes(mode, scope string) []string {
	if !strings.HasSuffix(scope, ":*") {
		return []string{scope}
	}
	if bp.res != nil {
		return bp.res.ExpandEffectScopes(mode, scope)
	}
	return bp.axisScopes(failuremode.Axis(strings.TrimSuffix(scope, ":*")))
}
