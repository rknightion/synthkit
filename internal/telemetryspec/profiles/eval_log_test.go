// SPDX-License-Identifier: AGPL-3.0-only

package profiles

import "testing"

func TestEvalLogProfileRegistered(t *testing.T) {
	p, ok := Lookup("eval_log")
	if !ok {
		t.Fatal("eval_log profile not registered")
	}
	if err := p.Validate(); err != nil {
		t.Fatalf("eval_log profile invalid: %v", err)
	}
}

func TestEvalLogProfileShape(t *testing.T) {
	p, _ := Lookup("eval_log")

	if len(p.Logs) != 1 {
		t.Fatalf("expected 1 log spec, got %d", len(p.Logs))
	}
	l := p.Logs[0]
	if l.Source != "langsmith-runs" {
		t.Errorf("expected source=langsmith-runs, got %q", l.Source)
	}

	// Stream labels (env/service_name/cluster/level/source) are auto-stamped by the workload, not
	// author-declared — the profile must NOT carry reserved stream-label keys.
	if len(l.StreamLabels) != 0 {
		t.Errorf("profile must not declare reserved stream labels, got %v", l.StreamLabels)
	}

	// Verify required body fields.
	requiredBody := []string{
		"msg", "aws_env",
		"trace_id", "span_id", "run_id",
		"portkey_trace_id", "correlation_id", "request_id", "session_id",
	}
	for _, k := range requiredBody {
		if _, ok := l.Body[k]; !ok {
			t.Errorf("missing body field: %q", k)
		}
	}

	// Verify high-card refs resolve to correct correlation field names.
	highCardRefs := map[string]string{
		"trace_id":         "trace_id",
		"span_id":          "span_id",
		"run_id":           "run_id",
		"portkey_trace_id": "portkey_trace_id",
		"correlation_id":   "correlation_id",
		"request_id":       "request_id",
		"session_id":       "session_id",
	}
	for field, wantRef := range highCardRefs {
		vm := l.Body[field]
		if vm.Ref != wantRef {
			t.Errorf("body[%q]: want Ref=%q, got Ref=%q", field, wantRef, vm.Ref)
		}
	}

	// msg must be the fixed const string.
	if l.Body["msg"].ConstStr == nil || *l.Body["msg"].ConstStr != "run indexed" {
		t.Errorf("body[msg]: want const_str=%q, got %v", "run indexed", l.Body["msg"].ConstStr)
	}

	// No spans or metrics in this profile.
	if len(p.Metrics) != 0 {
		t.Errorf("expected 0 metrics, got %d", len(p.Metrics))
	}
	if len(p.Spans) != 0 {
		t.Errorf("expected 0 spans, got %d", len(p.Spans))
	}
}
