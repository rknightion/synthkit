// SPDX-License-Identifier: AGPL-3.0-only

package ledger

import (
	"regexp"
	"testing"
)

var hex8 = regexp.MustCompile(`^[0-9a-f]{16}$`)

func TestNewSpanID_FormatAndUniqueness(t *testing.T) {
	seen := map[string]bool{}
	for range 1000 {
		id := NewSpanID()
		if !hex8.MatchString(id) {
			t.Fatalf("NewSpanID()=%q, want 16 hex chars", id)
		}
		if seen[id] {
			t.Fatalf("NewSpanID() collision on %q", id)
		}
		seen[id] = true
	}
}

func TestNewCorrelationFromSeedDeterministic(t *testing.T) {
	first := NewCorrelationFromSeed("fixed-tick-request")
	repeat := NewCorrelationFromSeed("fixed-tick-request")
	if first != repeat {
		t.Fatalf("same seed produced different correlations: %#v != %#v", first, repeat)
	}
	if different := NewCorrelationFromSeed("next-request"); first == different {
		t.Fatal("different seeds produced the same correlation")
	}
	if !hex8.MatchString(first.SpanID) || !hex8.MatchString(SpanIDFromSeed("fixed-tick-request", "child")) {
		t.Fatalf("deterministic span IDs are not W3C 64-bit hex: %#v", first)
	}
	if got := TraceIDFromSeed("fixed-tick-request", "trace"); len(got) != 32 {
		t.Fatalf("deterministic trace ID length = %d, want 32", len(got))
	}
}

func TestCall_TreeFields(t *testing.T) {
	c := Call{Kind: "service", Target: "svc-b", SpanID: NewSpanID(), PeerSpanID: NewSpanID(), ParentHopIndex: -1}
	if c.SpanID == c.PeerSpanID {
		t.Fatal("SpanID and PeerSpanID must differ")
	}
	if c.ParentHopIndex != -1 {
		t.Fatalf("ParentHopIndex=%d, want -1", c.ParentHopIndex)
	}
}
