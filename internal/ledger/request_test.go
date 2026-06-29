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

func TestCall_TreeFields(t *testing.T) {
	c := Call{Kind: "service", Target: "svc-b", SpanID: NewSpanID(), PeerSpanID: NewSpanID(), ParentHopIndex: -1}
	if c.SpanID == c.PeerSpanID {
		t.Fatal("SpanID and PeerSpanID must differ")
	}
	if c.ParentHopIndex != -1 {
		t.Fatalf("ParentHopIndex=%d, want -1", c.ParentHopIndex)
	}
}
