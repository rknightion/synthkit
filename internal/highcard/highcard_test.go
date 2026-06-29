// SPDX-License-Identifier: AGPL-3.0-only

package highcard

import "testing"

func TestCanonicalSet(t *testing.T) {
	// The canonical high-card correlation fields — must include the AI golden-thread join key.
	want := []string{
		"trace_id", "span_id", "request_id", "session_id", "correlation_id",
		"run_id", "plan_id", "user_id", "portkey_trace_id",
	}
	got := Fields()
	if len(got) != len(want) {
		t.Fatalf("Fields() len=%d want %d (%v)", len(got), len(want), got)
	}
	for _, k := range want {
		if !Contains(k) {
			t.Errorf("Contains(%q)=false, want in canonical set", k)
		}
	}
	// bounded keys that are legal labels must NOT be in the set (M4 note: uid/model excluded).
	for _, k := range []string{"model", "uid", "cluster", "env", "service"} {
		if Contains(k) {
			t.Errorf("Contains(%q)=true, but it is a legal bounded label", k)
		}
	}
}

func TestFieldsIsCopy(t *testing.T) {
	a := Fields()
	if len(a) == 0 {
		t.Fatal("Fields() empty")
	}
	a[0] = "MUTATED"
	if Contains("MUTATED") || !Contains("trace_id") {
		t.Fatal("Fields() must return a defensive copy")
	}
}
