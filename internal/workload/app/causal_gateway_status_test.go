// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"testing"

	"github.com/rknightion/synthkit/internal/causal"
	"github.com/rknightion/synthkit/internal/ledger"
	"github.com/rknightion/synthkit/internal/shape"
)

// gatewayStatusResources is causalResources plus a release rule that rejects with a 500 (a
// release regression in miniature).
const gatewayStatusResources = causalResources + `
- {name: release-rule, kind: rule, reject_share: 0.5,
   error: {status: 500, message: "release rejected the request"}}
`

// gatewayStatusApp declares the gateway_export_log profile on the entry and on the mid-graph
// enrich-fn hop, so a failure behind either node fails the request through a gateway export line.
const gatewayStatusApp = `
traffic: {off_peak_rps: 5, peak_rps: 60}
services:
  - name: content-gen
    entry: true
    profiles: [gateway_export_log]
    uses: [release-rule]
    calls: [records-db, enrich-fn]
    call_policies:
      enrich-fn: {timeout_ms: 10000, retries: 2, backoff_ms: 200, retry_on: [throttle, 5xx]}
  - {name: records-db, type: db, uses: [records-db]}
  - name: enrich-fn
    type: function
    external: true
    profiles: [gateway_export_log]
    uses: [enrich-fn-concurrency]
    calls: [partner-gateway]
    call_policies:
      partner-gateway: {timeout_ms: 2000, retries: 1, retry_on: [5xx, timeout, connect]}
  - {name: partner-gateway, external: true, uses: [partner-api]}
`

// gatewayLines are a service's gateway export lines for one request, matched on the trace id the
// profile carries as structured metadata.
func gatewayLines(p projected, svc string, r *ledger.Request) []logLine {
	var out []logLine
	for _, l := range p.lines[svc] {
		if l.meta["trace_id"] == r.TraceID {
			out = append(out, l)
		}
	}
	return out
}

// healthyStatuses is the status vocabulary the profile draws for a request that did not fail.
var healthyStatuses = map[string]bool{"200": true, "429": true, "500": true}

// SKT-0190: a gateway export line of a request that failed through its node carries that node's
// failing status, not the profile's independent 200/429/500 draw.
func TestGatewayExportStatusFollowsCausalFailure(t *testing.T) {
	t.Run("release rule 500 at the gateway node", func(t *testing.T) {
		w := causalApps(t, gatewayStatusResources, gatewayStatusApp)[0]
		eng := incident(causal.ModeRuleRegression, "release-rule", 1)
		reqs := mintAt(w, eng, causalNow)
		p := project(t, w, eng, reqs)
		seen := 0
		for _, r := range reqs {
			if r.Fault == nil || r.Fault.Resource != "release-rule" {
				continue
			}
			seen++
			if r.StatusCode != 500 {
				t.Fatalf("release request status %d, want 500", r.StatusCode)
			}
			lines := gatewayLines(p, "content-gen", r)
			if len(lines) != 1 {
				t.Fatalf("content-gen gateway lines %d, want 1", len(lines))
			}
			if got := lines[0].body["response_status_code"]; got != "500" {
				t.Fatalf("failed request's gateway export status %q, want 500", got)
			}
		}
		if seen < 10 {
			t.Fatalf("only %d release rejections minted", seen)
		}
	})

	t.Run("failure behind the gateway hops", func(t *testing.T) {
		w := causalApps(t, gatewayStatusResources, gatewayStatusApp)[0]
		eng := incident(causal.ModeCredentialExpired, "partner-api-cert", 1)
		reqs := mintAt(w, eng, causalNow)
		p := project(t, w, eng, reqs)
		seen := 0
		for _, r := range reqs {
			if r.Fault == nil || r.Fault.Resource != "partner-api-cert" {
				continue
			}
			seen++
			entry := gatewayLines(p, "content-gen", r)
			if len(entry) != 1 || entry[0].body["response_status_code"] != "502" {
				t.Fatalf("entry gateway lines %+v, want one with status 502", entry)
			}
			attempts := gatewayLines(p, "enrich-fn", r)
			if len(attempts) != 3 {
				t.Fatalf("enrich-fn gateway lines %d, want one per attempt (3)", len(attempts))
			}
			for k, l := range attempts {
				if got := l.body["response_status_code"]; got != "502" {
					t.Fatalf("enrich-fn attempt %d gateway export status %q, want 502", k, got)
				}
			}
		}
		if seen < 10 {
			t.Fatalf("only %d certificate failures minted", seen)
		}
	})

	t.Run("healthy requests keep the profile's draw", func(t *testing.T) {
		w := causalApps(t, gatewayStatusResources, gatewayStatusApp)[0]
		eng := shape.New("UTC", nil)
		reqs := mintAt(w, eng, causalNow)
		p := project(t, w, eng, reqs)
		ok, lines := 0, 0
		for _, r := range reqs {
			if r.Outcome != ledger.OutcomeSuccess {
				continue
			}
			for _, svc := range []string{"content-gen", "enrich-fn"} {
				for _, l := range gatewayLines(p, svc, r) {
					lines++
					got := l.body["response_status_code"]
					if !healthyStatuses[got] {
						t.Fatalf("healthy request's gateway status %q is outside the profile vocabulary", got)
					}
					if got == "200" {
						ok++
					}
				}
			}
		}
		if lines == 0 || float64(ok) < 0.8*float64(lines) {
			t.Fatalf("healthy gateway lines %d, status 200 on %d, want mostly 200", lines, ok)
		}
	})
}
