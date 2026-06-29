// SPDX-License-Identifier: AGPL-3.0-only

package profiles

import "testing"

func TestRumFaroProfileRegistered(t *testing.T) {
	p, ok := Lookup("rum_faro")
	if !ok {
		t.Fatal("rum_faro profile not registered")
	}
	if err := p.Validate(); err != nil {
		t.Fatalf("rum_faro profile invalid: %v", err)
	}
}

func TestRumFaroProfileShape(t *testing.T) {
	p, _ := Lookup("rum_faro")

	// 2026-06-16: web-vital gauges REMOVED (SK-56/57/58 resolved — vitals are Loki
	// measurement log events via the Faro collector, NOT Prometheus gauge series).
	// faro_rum LogSpec REMOVED — RUM now flows via projectRUM → faro.Sink (collector);
	// the direct-to-Loki path was bypassing the collector and stamping wrong labels.
	if len(p.Metrics) != 0 {
		t.Fatalf("expected 0 metrics (web-vital gauges removed), got %d", len(p.Metrics))
	}
	if len(p.Logs) != 0 {
		t.Fatalf("expected 0 log specs (faro_rum LogSpec removed), got %d", len(p.Logs))
	}
	if len(p.Spans) != 1 {
		t.Fatalf("expected 1 span spec (browser CLIENT), got %d", len(p.Spans))
	}

	// Span checks (browser CLIENT root, signals/logs.md [slug: logs-browser-spans]).
	s := p.Spans[0]
	if s.Kind != "client" {
		t.Errorf("span kind: want client, got %q", s.Kind)
	}

	// NameTemplate must be "{{http.method}}" — browser CLIENT spans are named by HTTP method
	// only (e.g. "GET", "POST") per signals/logs.md [slug: logs-browser-spans] (M3). The template
	// references the "http.method" attr KEY (interpolateName resolves against attr keys, and the
	// method-only token is carried by the http.method attr = reqRefs "method").
	if s.NameTemplate != "{{http.method}}" {
		t.Errorf("span NameTemplate = %q, want %q (method-only per live reference capture)", s.NameTemplate, "{{http.method}}")
	}

	// Required span attributes (from signals/logs.md [slug: logs-browser-spans]).
	// app.correlation_id and request_id are auto-stamped by the workload (reserved keys,
	// review H1) — not in the profile.
	requiredAttrs := []string{
		"session.id", "enduser.id", "component",
		"http.method", "http.scheme", "http.status_code",
		"url.template", "app.user_action_id", "original_span_name",
	}
	for _, k := range requiredAttrs {
		if _, ok := s.Attributes[k]; !ok {
			t.Errorf("span: missing attribute %q", k)
		}
	}

	// reserved keys must NOT be author-declared (the workload auto-stamps them).
	for _, k := range []string{"app.correlation_id", "request_id"} {
		if _, bad := s.Attributes[k]; bad {
			t.Errorf("span must not declare reserved auto-stamped attr %q", k)
		}
	}

	// component must be const "fetch".
	if s.Attributes["component"].ConstStr == nil || *s.Attributes["component"].ConstStr != "fetch" {
		t.Errorf("span attr component: want const_str=fetch, got %v", s.Attributes["component"].ConstStr)
	}

	// session.id must be Ref (not a static value).
	if s.Attributes["session.id"].Ref != "session_id" {
		t.Errorf("span attr session.id: want Ref=session_id, got %q", s.Attributes["session.id"].Ref)
	}

	// http.method must be Ref:"method" (the HTTP verb token, not the full route string).
	// M3: reqRefs now exposes "method" = leading token of the route (e.g. "GET").
	if s.Attributes["http.method"].Ref != "method" {
		t.Errorf("span attr http.method: want Ref=%q, got %q (must be method-only, not full route)", "method", s.Attributes["http.method"].Ref)
	}

	// http.status_code must be Ref (correlated to request outcome).
	if s.Attributes["http.status_code"].Ref == "" {
		t.Errorf("span attr http.status_code: want Ref (correlated), got non-Ref (Enum/const is forbidden)")
	}

	// original_span_name must be Ref:"original_span_name" (pre-formatted "HTTP "+method).
	// M3: reqRefs exposes "original_span_name" = "HTTP "+method (e.g. "HTTP GET").
	if s.Attributes["original_span_name"].Ref != "original_span_name" {
		t.Errorf("span attr original_span_name: want Ref=%q, got %q", "original_span_name", s.Attributes["original_span_name"].Ref)
	}
}
