// SPDX-License-Identifier: AGPL-3.0-only

package engine

import (
	"strings"
	"testing"
)

func lintCtx() LintContext {
	return LintContext{
		MaxChars:     120,
		OperatorID:   "U0OPERATOR",
		OperatorName: "Casey",
		KnownNames:   []string{"Alex", "Alex Moreno", "Jordan", "Jordan Pike", "Casey"},
		Evidence:     "alert group firing: p95 latency 2400ms on acme-backend-api-content-gen; error rate 12% see https://example.invalid/d/abc",
	}
}

func TestLintLine(t *testing.T) {
	cases := []struct {
		name  string
		text  string
		cited bool
		rule  string // "" means the line must pass
	}{
		{"plain", "ok. casey, can you ack that and take a first look?", false, ""},
		{"empty", "   ", false, "length"},
		{"too long", strings.Repeat("a", 121), false, "length"},
		{"here", "@here latency is up", false, "broadcast"},
		{"special mention", "<!channel> heads up", false, "broadcast"},
		{"app mention", "@Grafana can you look", false, "mention"},
		{"user mention", "<@U0SOMEONE> thoughts?", false, "mention"},
		{"operator mention ok", "<@U0OPERATOR> thoughts?", false, ""},
		{"operator at-name ok", "@Casey thoughts?", false, ""},
		{"slash command", "/grafana incident new", false, "slash"},
		{"as an ai", "As an AI I can't see dashboards", false, "ai"},
		{"url from ledger ok", "dashboard is https://example.invalid/d/abc", false, ""},
		{"invented url", "see https://example.invalid/other", false, "url"},
		{"irm ack claim", "i acked it already", false, "irm_claim"},
		{"irm ack claim even cited", "I've acknowledged the alert", true, "irm_claim"},
		{"irm future claim", "let me resolve the alert group", false, "irm_claim"},
		{"irm elided subject", "Escalated to the next tier.", false, "irm_claim"},
		{"asking operator ok", "casey, can you resolve the alert group?", false, ""},
		{"ops claim uncited", "I restarted the pods", false, "claim"},
		{"ops claim cited", "I restarted the pods", true, ""},
		{"ops claim we", "we've rolled back the deploy", false, "claim"},
		{"ops third person ok", "did anyone restart it?", false, ""},
		{"number from ledger", "p95 is at 2400ms right now", false, ""},
		{"number with space from ledger", "p95 is at 2400 ms right now", false, ""},
		{"percent from ledger", "errors at 12%", false, ""},
		{"invented number", "p95 is at 900ms", false, "number"},
		{"invented percent", "error rate 40%", false, "number"},
		{"invented errors", "seeing 50 errors a minute", false, "number"},
		{"plain count ok", "two users said it felt slow", false, ""},
		{"known vocative", "Jordan, can you check the pods?", false, ""},
		{"unknown vocative", "Priya, can you check the pods?", false, "unknown_name"},
		{"unknown greeting", "thanks Morgan", false, "unknown_name"},
		{"stoplist ok", "thanks All", false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lc := lintCtx()
			lc.Cited = tc.cited
			err := LintLine(tc.text, lc)
			if tc.rule == "" {
				if err != nil {
					t.Fatalf("LintLine(%q) = %v, want pass", tc.text, err)
				}
				return
			}
			le, ok := err.(*LintError)
			if !ok || le.Rule != tc.rule {
				t.Fatalf("LintLine(%q) = %v, want rule %q", tc.text, err, tc.rule)
			}
		})
	}
}
