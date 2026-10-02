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
		if strings.HasSuffix(scope, ":*") {
			axis := failuremode.Axis(strings.TrimSuffix(scope, ":*"))
			for _, name := range bp.axisScopes(axis) {
				out = append(out, shape.LiveFailure{Enabled: true, Intensity: intensity, Scope: name})
			}
			return
		}
		out = append(out, shape.LiveFailure{Enabled: true, Intensity: intensity, Scope: scope})
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
			if active, intensity := capture.runtime.Eval(capture.now, mode, ri.Target); active {
				out = append(out, shape.LiveFailure{Enabled: true, Intensity: intensity, Scope: ri.Target})
			}
		}
	}
	return out
}
