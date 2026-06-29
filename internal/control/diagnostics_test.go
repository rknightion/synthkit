// SPDX-License-Identifier: AGPL-3.0-only

package control

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestDiagnosticsSnapshotOrdersErrorsFirst(t *testing.T) {
	d := NewDiagnostics()
	d.Add("warning", "bp-a", "incident", "skipped daily 24h")
	d.Add("error", "bp-b", "load", "unknown target")
	d.Add("warning", "bp-c", "resolve", "zero-construct db")

	if d.Count() != 3 {
		t.Fatalf("Count = %d, want 3", d.Count())
	}
	snap := d.Snapshot()
	if snap[0].Level != "error" {
		t.Errorf("errors must sort first, got %q", snap[0].Level)
	}
	// Within the warning group, insertion order is preserved (stable sort).
	if snap[1].Source != "bp-a" || snap[2].Source != "bp-c" {
		t.Errorf("warning order not stable: %q then %q", snap[1].Source, snap[2].Source)
	}
	if snap[0].Time == "" {
		t.Error("Time must be stamped")
	}
}

func TestDiagnosticsEndpoint(t *testing.T) {
	store := NewStore("")
	d := NewDiagnostics()
	d.Add("error", "blueprints/bad.yaml", "load", "incident references unknown cluster")
	h := NewHandler(store, nil, "").SetDiagnostics(d)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/control/diagnostics", nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var got []Diagnostic
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 || got[0].Category != "load" || got[0].Source != "blueprints/bad.yaml" {
		t.Fatalf("unexpected payload: %+v", got)
	}

	// Unset diagnostics → 404.
	h2 := NewHandler(store, nil, "")
	rec2 := httptest.NewRecorder()
	h2.ServeHTTP(rec2, httptest.NewRequest("GET", "/control/diagnostics", nil))
	if rec2.Code != 404 {
		t.Errorf("unset diagnostics status = %d, want 404", rec2.Code)
	}
}
