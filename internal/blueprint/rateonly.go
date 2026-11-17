// SPDX-License-Identifier: AGPL-3.0-only

package blueprint

import (
	"fmt"
	"slices"
	"strings"

	"github.com/rknightion/synthkit/internal/causal"
	"github.com/rknightion/synthkit/internal/failuremode"
)

// requestPathRateModes is the explicit set of legacy failure modes that only scale a
// request-path rate, share, latency or throughput. Each one is a symptom with no cause behind it:
// the number moves because the scenario says so. A declared cause is a failure mode on
// causal.AxisResource (environments[].resources), from which the same symptom follows.
//
// The set is keyed by (axis, mode) because a mode name can live on several axes. It deliberately
// leaves out every mode that is not request-path rate scaling:
//   - cluster, gpu and network modes change infrastructure or integration state;
//   - database and cloud modes change a provider-side or poller signal;
//   - quality modes (eval_quality_*, provider_call_error) have no causal counterpart in v1;
//   - profile and runtime-pressure modes (cpu_hotspot, memory_leak, goroutine_leak,
//     lock_contention) distort runtime values rather than a request rate;
//   - web_vitals_degraded and pipeline_stage modes are RUM and batch signals.
var requestPathRateModes = map[failuremode.Axis][]string{
	failuremode.AxisService:  {"error_spike", "latency_storm", "throughput_drop", "retry_storm", "fallback_storm"},
	failuremode.AxisWorkload: {"error_burst", "latency_spike"},
	failuremode.AxisFunction: {"function_error_spike", "function_duration_storm", "function_throttle_storm"},
	failuremode.AxisQueue:    {"queue_consumer_stall", "queue_publish_drop"},
	failuremode.AxisTable:    {"table_throttle_storm"},
}

// effectAxes returns the axes one effect resolves on. The effect has already passed
// validateEffectAxes, so a named target resolves on the axes that declare the mode.
func effectAxes(e EffectDecl, axes map[string][]failuremode.Axis, vocab []failuremode.Mode) []failuremode.Axis {
	switch {
	case e.Target == "":
		if axis, ok := soleAxis(vocab, e.Mode); ok {
			return []failuremode.Axis{axis}
		}
		return nil
	case strings.HasSuffix(e.Target, ":*"):
		return []failuremode.Axis{failuremode.Axis(strings.TrimSuffix(e.Target, ":*"))}
	}
	var out []failuremode.Axis
	for _, axis := range axes[e.Target] {
		if modeOnAxis(vocab, e.Mode, axis) {
			out = append(out, axis)
		}
	}
	return out
}

// rateOnlyWarning reports a scenario or incident whose effects scale request-path rates
// (requestPathRateModes) and include no resource-axis effect. The warning names the modes, in
// declaration order and once each. A single-mode incident row cannot carry a second effect, so
// its remedy is a scenario. It is non-fatal: a rate-only story is legal, only visible.
func rateOnlyWarning(where string, single bool, effects []EffectDecl, axes map[string][]failuremode.Axis, vocab []failuremode.Mode) (string, bool) {
	var scaled []string
	for _, e := range effects {
		for _, axis := range effectAxes(e, axes, vocab) {
			if axis == causal.AxisResource {
				return "", false
			}
			if slices.Contains(requestPathRateModes[axis], e.Mode) && !slices.Contains(scaled, e.Mode) {
				scaled = append(scaled, e.Mode)
			}
		}
	}
	if len(scaled) == 0 {
		return "", false
	}
	remedy := "add a resource-axis effect on a declared environments[].resources target so the symptom follows from a cause"
	if single {
		remedy = "define a scenario that pairs it with a resource-axis effect on a declared environments[].resources target and schedule that scenario"
	}
	return fmt.Sprintf("%s is rate-only: %s scale request-path rates or latency with no declared cause; %s", where, strings.Join(scaled, ", "), remedy), true
}
