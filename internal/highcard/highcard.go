// SPDX-License-Identifier: AGPL-3.0-only

// Package highcard is the canonical, single-source-of-truth set of high-cardinality,
// request/session/correlation-scoped label keys that must NEVER appear as Mimir series labels
// (promrw, invariant M4) OR Loki stream labels (loki, I14/I15) — they ride in span attributes /
// Loki structured metadata instead. The custom-telemetry DSL capability matrix (internal/
// telemetryspec) consumes this same set so the DSL and the sinks agree by construction.
//
// It is a leaf: it imports nothing from other synthkit tiers (same tier as fixture/shape/state/
// failuremode), so the sinks and the DSL may all import it without violating the three-tier rule.
//
// NOTE on exclusions: "uid" (Kubernetes pod UID — bounded by pod count, required for kube_pod_info
// joins) and "model" (bounded model-name vocabulary) are deliberately NOT here — they are legal
// labels. Only truly request/session/correlation-scoped keys belong.
package highcard

// fields is the canonical ordered set. portkey_trace_id is the AI request-correlation join key (the
// Portkey export log ↔ gateway span join, Spec 2b/I9) — high-card, never a label.
var fields = []string{
	"trace_id", "span_id", "request_id", "session_id", "correlation_id",
	"run_id", "plan_id", "user_id", "portkey_trace_id",
}

// set is the membership index, built once from fields.
var set = func() map[string]struct{} {
	m := make(map[string]struct{}, len(fields))
	for _, k := range fields {
		m[k] = struct{}{}
	}
	return m
}()

// Fields returns a defensive copy of the canonical ordered key set.
func Fields() []string {
	out := make([]string, len(fields))
	copy(out, fields)
	return out
}

// Set returns a fresh membership map of the canonical keys (callers may store it without
// aliasing the package-internal index).
func Set() map[string]struct{} {
	out := make(map[string]struct{}, len(set))
	for k := range set {
		out[k] = struct{}{}
	}
	return out
}

// Contains reports whether key is a canonical high-card correlation field.
func Contains(key string) bool {
	_, ok := set[key]
	return ok
}
