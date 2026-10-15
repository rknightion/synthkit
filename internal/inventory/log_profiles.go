// SPDX-License-Identifier: AGPL-3.0-only

package inventory

// collectorProfileSynth selects only directly attributed OTLP pod-log evidence
// for the captured collector configuration. Unknown permutations and legacy
// inventories keep their full comparisons. Unknown and unattributed observations always stay:
// an incomplete attribution must never hide a contradiction.
func collectorProfileSynth(synth Schema, permutation string) Schema {
	var profile string
	switch permutation {
	case "alloy-default", "alloy-otlp-podlogs":
		profile = "k8s_monitoring"
	case "otel-receivers":
		profile = "otel_collector"
	default:
		return synth
	}
	isPodLog := func(log Log) bool {
		family, ok := ShapeLogFamily(log)
		return ok && family == LogFamilyPodLogs && log.Transport == TransportOTLPLogs
	}
	found := false
	for _, log := range synth.Logs {
		if isPodLog(log) && log.CollectorProfile == profile {
			found = true
			break
		}
	}
	if !found {
		// Keep the wrong profile visible, rather than turning a contradiction into
		// an absent-family gap by filtering away the entire family.
		return synth
	}
	out := cloneSchema(synth)
	out.Logs = nil
	for _, log := range synth.Logs {
		if !isPodLog(log) || (log.CollectorProfile != "k8s_monitoring" && log.CollectorProfile != "otel_collector") || log.CollectorProfile == profile {
			out.Logs = append(out.Logs, cloneLog(log))
		}
	}
	return out
}
