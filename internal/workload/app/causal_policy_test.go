// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"strings"
	"testing"

	"github.com/rknightion/synthkit/internal/causal"
)

// A credential failure is never resent by the service that calls the credentialed dependency
// (the failure is its own, and a resend meets the same expired credential). The caller above
// it sees only the mapped 502, which a retry_on 5xx policy resends; each resend of that outer
// call re-serves the middle service, which again tries the credentialed call once. So three
// outer attempts produce three credentialed attempts, never nine.
func TestCausalCredentialFailureResentOnlyByAnUpstreamThatSeesTheMapped502(t *testing.T) {
	app := `
traffic: {off_peak_rps: 5, peak_rps: 60}
services:
  - name: front
    entry: true
    calls: [mid]
    call_policies: {mid: {retries: 2, retry_on: [5xx]}}
  - name: mid
    calls: [back]
    call_policies: {back: {retries: 2, retry_on: [5xx, timeout, connect]}}
  - {name: back, external: true, uses: [partner-api]}
`
	w := causalApps(t, causalResources, app)[0]
	reqs := mintAt(w, incident(causal.ModeCredentialExpired, "partner-api-cert", 1), causalNow)
	seen := 0
	for _, r := range reqs {
		if r.Fault == nil || r.Fault.Resource != "partner-api-cert" {
			continue
		}
		seen++
		mid, back := callsTo(r, "mid"), callsTo(r, "back")
		if len(mid) != 3 {
			t.Fatalf("front -> mid attempts %d, want 3 (the upstream sees a 502 and retry_on 5xx resends it)", len(mid))
		}
		for k, c := range mid {
			if c.Attempt != k || !c.Failed || c.StatusCode != 502 {
				t.Fatalf("front -> mid attempt %d: %+v", k, c)
			}
		}
		if len(back) != 3 {
			t.Fatalf("mid -> back attempts %d, want one per front -> mid attempt (3): the direct caller never resends a credential failure", len(back))
		}
		for _, c := range back {
			if c.Attempt != 0 || !c.Failed || c.Fault == nil || c.Fault.Resource != "partner-api-cert" {
				t.Fatalf("mid -> back attempt %+v, want a single failed attempt carrying the credential fault", c)
			}
		}
	}
	if seen == 0 {
		t.Fatal("no request carried the credential fault")
	}
}

// A rule rejection slower than the caller's timeout_ms reaches the caller as a timeout, not as
// a rule failure (a client that times out cannot know the cause), so retry_on timeout resends
// it. Without a timeout the same rejection is a rule failure and is made once.
func TestCausalRuleRejectionSlowerThanTimeoutIsResentAsATimeout(t *testing.T) {
	const app = `
traffic: {off_peak_rps: 5, peak_rps: 60}
services:
  - name: front
    entry: true
    calls: [back]
    call_policies: {back: {%s retries: 2, retry_on: [timeout]}}
  - {name: back, uses: [completeness-rule]}
`
	run := func(policy string) (attempts []int, codes []int) {
		w := causalApps(t, `- {name: completeness-rule, kind: rule, reject_share: 1, error: {status: 422, message: "rejected"}}
`, strings.Replace(app, "%s", policy, 1))[0]
		for _, r := range mintAt(w, incident(causal.ModeRuleRegression, "completeness-rule", 1), causalNow) {
			back := callsTo(r, "back")
			attempts = append(attempts, len(back))
			codes = append(codes, r.StatusCode)
		}
		return attempts, codes
	}

	attempts, codes := run("")
	if len(attempts) == 0 {
		t.Fatal("no requests")
	}
	for i := range attempts {
		if attempts[i] != 1 || codes[i] != 422 {
			t.Fatalf("no timeout: %d attempts ending %d, want one attempt ending in the rule's 422", attempts[i], codes[i])
		}
	}

	// 0.001 ms is below any hop's duration (a hop lasts at least one millisecond).
	attempts, codes = run("timeout_ms: 0.001,")
	for i := range attempts {
		if attempts[i] != 3 || codes[i] != 504 {
			t.Fatalf("timeout below the rejection time: %d attempts ending %d, want 3 attempts ending in a 504", attempts[i], codes[i])
		}
	}
}

// timeout_ms and backoff_ms are bounded at load (ten minutes each) so a duration derived from
// them cannot overflow time.Duration.
func TestCausalCallPolicyDurationsAreBounded(t *testing.T) {
	const app = `
traffic: {off_peak_rps: 5, peak_rps: 60}
services:
  - name: front
    entry: true
    calls: [back]
    call_policies: {back: {%s}}
  - {name: back}
`
	for _, tc := range []struct{ name, policy, want string }{
		{"timeout at the bound", "timeout_ms: 600000, backoff_ms: 600000, retries: 1, retry_on: [5xx]", ""},
		{"timeout over the bound", "timeout_ms: 600001", "timeout_ms 600001 must be at most 600000"},
		{"timeout overflowing", "timeout_ms: 1e300", "timeout_ms 1e+300 must be at most 600000"},
		{"backoff over the bound", "backoff_ms: 600001, retries: 1, retry_on: [5xx]", "backoff_ms 600001 must be at most 600000"},
		{"backoff overflowing", "backoff_ms: 1e300, retries: 1, retry_on: [5xx]", "backoff_ms 1e+300 must be at most 600000"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tryCausalApps(t, causalResources, strings.Replace(app, "%s", tc.policy, 1))
			if tc.want == "" {
				if err != nil {
					t.Fatalf("unexpected error %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %v, want %q", err, tc.want)
			}
		})
	}
}
