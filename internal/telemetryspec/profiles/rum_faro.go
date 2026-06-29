// SPDX-License-Identifier: AGPL-3.0-only

package profiles

// rum_faro: Grafana Faro / Frontend Observability (RUM) profile.
//
// Models the browser CLIENT span emitted by the Faro Web SDK via the OTLP sink (the
// "golden-thread root" — the browser fetch span that parents the backend SERVER span in
// Tempo). There is exactly ONE SpanSpec in this profile.
//
// REMOVED (SK-56/57/58 RESOLVED — 2026-06-16):
//   - The 3 gauge MetricSpecs (largest_contentful_paint / cumulative_layout_shift /
//     interaction_to_next_paint) — web vitals are Loki measurement log events, NOT
//     Prometheus metrics (live reference capture confirmed; SK-56/57/58 marked resolved in
//     signals/logs.md).
//   - The faro_rum LogSpec — RUM now flows via the collector beacon (projectRUM →
//     faro.Sink) which owns the Loki label mapping; the direct-to-Loki path was an
//     anti-pattern (bypassed the collector, stamps wrong labels, empty panel bodies).
//
// The SpanSpec below describes the browser CLIENT root span that the app workload's
// projectTraces emits for a frontend node (type=frontend) carrying this profile. It
// decorates the structural CLIENT span with the correlated http.* attributes from the
// ledger request (reqRefs Ref: lookups), matching the real @opentelemetry/instrumentation-
// fetch shape captured via live reference 2026-06-16.
//
// Signals sources:
//   - signals/logs.md [slug: logs-browser-spans]: span name, original_span_name, span attrs.
//   - internal/workload/webservice/rum.go: beaconFor, faro.tracing.fetch attrs.
//   - internal/sink/faro/faro.go: Payload/Meta/TraceContext.
//   - internal/workload/app/rum.go: projectRUM — the actual beacon emission.

import "github.com/rknightion/synthkit/internal/telemetryspec"

// rumStr is a file-local helper that returns a *string.
func rumStr(s string) *string { return &s }

func init() {
	register(telemetryspec.Profile{
		Name: "rum_faro",
		// No MetricSpecs — web vitals are NOT Prometheus metrics (SK-56/57/58 resolved;
		// live capture confirms: vitals arrive as Loki measurement log events via the
		// Faro collector, never as gauge series). The fabricated gauges are removed.
		Metrics: nil,
		// No LogSpecs — RUM now emits via the Faro collector beacon (projectRUM →
		// faro.Sink). Direct-to-Loki was bypassing the collector; the profile declares
		// no LogSpec so the app's projectLogs lane never emits a faro_rum stream.
		Logs: nil,
		Spans: []telemetryspec.SpanSpec{
			{
				// Browser CLIENT span — the golden-thread root / trace origin.
				//
				// signals/logs.md [slug: logs-browser-spans]:
				//   SPAN_KIND_CLIENT; parent_span_id="" (trace root);
				//   span name = HTTP method ONLY (e.g. "POST" / "GET") — NOT "METHOD path"
				//   (NameTemplate: "{{method}}" via reqRefs "method" key);
				//   the un-truncated name rides in original_span_name attr (= "HTTP POST",
				//   via reqRefs "original_span_name" = "HTTP "+method).
				//   Emitted by @opentelemetry/instrumentation-fetch scope.
				//
				// NameTemplate: method-only per live reference capture. Real Faro browser
				// spans (via @opentelemetry/instrumentation-fetch) are named by HTTP method
				// alone ("POST", "GET") — signals/logs.md [slug: logs-browser-spans].
				// interpolateName resolves {{token}} against the span's ATTRIBUTE KEYS, so the
				// template must reference an existing attr key — "http.method" (= reqRefs "method").
				NameTemplate: "{{http.method}}",
				Kind:         "client",
				Attributes: map[string]telemetryspec.ValueModel{
					// original_span_name: the un-truncated span name per signals/logs.md.
					// Live capture: "HTTP GET", "HTTP POST". reqRefs pre-formats "HTTP "+method
					// under the "original_span_name" key — correlated, not fabricated.
					"original_span_name": {Ref: "original_span_name"},

					// session.id: the browser session identifier — a span attribute (not a label).
					// signals/logs.md [slug: logs-browser-spans]: session.id span attr.
					"session.id": {Ref: "session_id"},

					// enduser.id: = session_id (the FEO end-user join key).
					// signals/logs.md [slug: logs-browser-spans]: enduser.id = session_id.
					"enduser.id": {Ref: "session_id"},

					// component: always "fetch" for the Faro tracing fetch event.
					// signals/logs.md [slug: logs-browser-spans]: component = "fetch".
					"component": {ConstStr: rumStr("fetch")},

					// http.method: HTTP verb, correlated from the ledger request route.
					// signals/logs.md [slug: logs-browser-spans]: http.method span attr.
					// Ref:"method" carries the method-only token (e.g. "GET", "POST").
					"http.method": {Ref: "method"},

					// http.status_code: the HTTP response status code, from the ledger.
					// signals/logs.md [slug: logs-browser-spans]: http.status_code span attr.
					// Ref:"status" carries the string-formatted status code from reqRefs.
					"http.status_code": {Ref: "status"},

					// http.scheme: always "https" for browser traffic.
					// signals/logs.md [slug: logs-browser-spans]: http.scheme span attr.
					"http.scheme": {ConstStr: rumStr("https")},

					// url.template: low-card route template (full "METHOD /path"); correlated from ledger.
					// signals/logs.md [slug: logs-browser-spans]: url.template span attr.
					"url.template": {Ref: "route"},

					// app.user_action_id: = request_id (cross-system join key for FEO actions).
					// signals/logs.md [slug: logs-browser-spans]: app.user_action_id = request_id.
					"app.user_action_id": {Ref: "request_id"},
				},
			},
		},
	})
}
