// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"strings"
	"testing"

	"github.com/rknightion/synthkit/internal/causal"
	"github.com/rknightion/synthkit/internal/sink/otlp"
)

// Exercise actual worker executions: a timeout, failed transport or token refusal must not
// inherit the gracefully-handled rule exception, even though a token refusal is also 4xx.
func TestHandledRuleDoesNotHideWorkerExecutionFailures(t *testing.T) {
	for _, tc := range []struct{ name, mode, target, policy string }{
		{"transport", causal.ModeDependencyUnavailable, "external-validator", ""},
		{"token4xx", causal.ModeCredentialExpired, "validator-token", ""},
		{"timeout", causal.ModeDependencySlow, "external-validator", "    call_policies: {external-validator-node: {timeout_ms: 1}}\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resources := causalResources + `
- {name: validator-token, kind: credential, of: external-validator, credential_type: token,
   error: {status: 401, type: TokenExpired, message: "token expired"}}
`
			doc := strings.Replace(handledWorker, "    uses: [completeness-rule]\n", "    uses: [completeness-rule]\n    calls: [external-validator-node]\n"+tc.policy, 1) + `
  - {name: external-validator-node, type: worker, uses: [external-validator], error_rate: 0}
`
			w := causalApps(t, resources, doc)[0]
			eng := incident(tc.mode, tc.target, 1)
			reqs := mintAt(w, eng, causalNow)
			if len(reqs) == 0 {
				t.Fatal("no executions")
			}
			p := project(t, w, eng, reqs)
			for _, r := range reqs {
				root := p.spans[r.SpanID]
				if root.Status != otlp.StatusError || root.Attrs[attrErrorType] == nil {
					t.Fatalf("execution failure hidden: %+v", root)
				}
				for _, c := range r.Calls {
					if c.PeerSpanID == "" {
						continue
					}
					server := p.spans[c.PeerSpanID]
					if server.Status != otlp.StatusError || server.Attrs[attrErrorType] == nil {
						t.Fatalf("worker server failure hidden: %+v", server)
					}
				}
			}
		})
	}
}
