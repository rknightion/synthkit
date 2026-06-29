// SPDX-License-Identifier: AGPL-3.0-only

package telemetryspec

import (
	"fmt"

	"github.com/rknightion/synthkit/internal/highcard"
)

// position is where a value model sits within a spec. It selects which value-model kinds are
// legal — the load-time capability matrix (design §3.4), the DSL's core safety seam.
type position int

const (
	posLabel       position = iota // metric label / Loki stream label (a series/stream dimension)
	posMetricValue                 // a metric magnitude
	posBody                        // a Loki log body field
	posAttr                        // a span attribute
)

// checkValue validates one value model at a position: first the model's own invariants
// (exactly-one-set + bounds + S1 non-empty), then the position's capability rule. This is the
// single place the DSL↔sink contract is enforced; it shares internal/highcard with both sinks so
// the DSL and the sinks agree by construction.
func checkValue(vm ValueModel, pos position, where string) error {
	if err := vm.Validate(); err != nil {
		return fmt.Errorf("%s: %w", where, err)
	}
	switch pos {
	case posLabel:
		// The high-card ref footgun gets a precise message (highcard is the single source of truth).
		if vm.Kind() == KindRef && highcard.Contains(vm.Ref) {
			return fmt.Errorf("%s: high-card ref %q is forbidden as a label/stream-label "+
				"(it rides in a log body / span attribute only) — see internal/highcard", where, vm.Ref)
		}
		// Determinism (§3.4/I32): a label value must enumerate a stable, total domain every run, so
		// only const/const_str/enum are legal label sources. Validate() already rejects empty
		// const_str / empty enum values, so a declared label key can never be dropped (I13/I32).
		switch vm.Kind() {
		case KindConst, KindConstStr, KindEnum:
		default:
			return fmt.Errorf("%s: label source must be const|const_str|enum for -dump determinism (got %q)", where, vm.Kind())
		}
	case posMetricValue:
		// A metric magnitude must be numeric-producing — string models (const_str/enum/ref) can't be
		// a metric value.
		switch vm.Kind() {
		case KindConst, KindIntRange, KindFloatRange, KindNormal, KindBool, KindShape:
		default:
			return fmt.Errorf("%s: metric value must be numeric (const|int_range|float_range|normal|bool|shape), got %q", where, vm.Kind())
		}
	case posBody, posAttr:
		// Any validated model is allowed here — including a high-card ref (the correlation glue:
		// trace_id/portkey_trace_id/run_id ride as structured metadata / span attrs, never labels).
	}
	return nil
}

// IsHighCardRef reports whether this model is a ref to a canonical high-card correlation field.
// The log interpreter uses it to route such fields into Loki STRUCTURED METADATA rather than the
// JSON body line (matching logMeta/aiHopMeta today — the golden-thread join keys ride as metadata).
func (v ValueModel) IsHighCardRef() bool {
	return v.Kind() == KindRef && highcard.Contains(v.Ref)
}
