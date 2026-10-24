// SPDX-License-Identifier: AGPL-3.0-only

package engine

import (
	"fmt"
	"regexp"
	"strings"
)

// LintContext is what a line is checked against.
type LintContext struct {
	MaxChars     int
	OperatorID   string   // the operator's platform user id; the only <@...> mention allowed
	OperatorName string   // the operator's first name; the only @name allowed
	KnownNames   []string // cast and operator names (first and full); vocatives must be one of these
	Evidence     string   // ledger and fact text: the only source of URLs and metrics
	Cited        bool     // the line cites a valid ledger entry or beat claim
}

// LintError names the rule a line broke.
type LintError struct {
	Rule   string
	Detail string
}

func (e *LintError) Error() string { return e.Rule + ": " + e.Detail }

func lintErr(rule, format string, a ...any) error {
	return &LintError{Rule: rule, Detail: fmt.Sprintf(format, a...)}
}

const subj = `(?:\b(?:i|we)(?:'ve|'d|'m| have| had| just| already| also)*\s+|^\s*|[.!?;]\s+)(?:just\s+|already\s+|also\s+)?`

var (
	broadcastRe = regexp.MustCompile(`(?i)<!|@(?:here|channel|everyone)\b`)
	userMention = regexp.MustCompile(`<@([A-Z0-9]+)(?:\|[^>]*)?>`)
	atNameRe    = regexp.MustCompile(`(?:^|[^\w<])@([\w.-]+)`)
	slashRe     = regexp.MustCompile(`^\s*/[a-z]`)
	aiRe        = regexp.MustCompile(`(?i)\bas an ai\b|\blanguage model\b|\bi(?:'m| am) an ai\b`)
	urlRe       = regexp.MustCompile(`(?i)\bhttps?://[^\s<>|]+|\bwww\.[^\s<>|]+`)

	// IRM actions belong to the operator alone: a persona claiming one is always rejected,
	// cited or not, past or future tense.
	irmClaimRe = regexp.MustCompile(`(?i)(?:` + subj + `(?:acked|ack'd|acknowledged|resolved|escalated|silenced|declared|paged|snoozed|unacked)\b` +
		`|\b(?:i'll|i will|let me|i'm|i am|we'll|we will|i'm going to|i am going to)\s+(?:go\s+(?:ahead\s+and\s+)?)?(?:ack|acknowledge|resolve|escalate|silence|declare|page|snooze|acking|resolving|escalating|silencing|declaring|paging)\b)`)

	// Completed operational actions need a citation.
	opsClaimRe = regexp.MustCompile(`(?i)` + subj + `(?:restarted|rolled (?:it |that |this )?back|scaled|deployed|redeployed|fixed|reverted|rebooted|failed (?:it )?over|patched|bounced|killed|drained|flushed|purged|bumped|pinged|mitigated)\b`)

	metricRe = regexp.MustCompile(`(?i)(?:^|[^\w.])(\d+(?:\.\d+)?)\s?(ms|rps|qps|req/s|errors?|secs?|s|%)(?:$|[^\w])`)

	vocativeStart = regexp.MustCompile(`^\s*([A-Z][a-z]+)\s*[,:]`)
	vocativeWord  = regexp.MustCompile(`(?i:\b(?:hey|hi|hello|thanks|thank you|thx|cc|ping|pinging|ask|asking|grab|grabbing|loop in|looping in|calling)\s+)([A-Z][a-z]+)\b`)
	nameStoplist  = map[string]bool{
		"all": true, "everyone": true, "folks": true, "team": true, "there": true, "me": true, "him": true,
		"her": true, "them": true, "you": true, "it": true, "us": true, "the": true, "this": true, "that": true,
		"so": true, "ok": true, "okay": true, "yes": true, "no": true, "yeah": true, "nope": true, "sure": true,
		"agreed": true, "right": true, "cool": true, "great": true, "nice": true, "update": true, "status": true,
		"heads": true, "quick": true, "again": true, "both": true, "guys": true,
	}
)

// LintLine checks one persona line. It returns a *LintError naming the first broken rule.
func LintLine(text string, lc LintContext) error {
	t := strings.TrimSpace(text)
	if t == "" {
		return lintErr("length", "empty line")
	}
	if lc.MaxChars > 0 && len([]rune(t)) > lc.MaxChars {
		return lintErr("length", "%d characters exceeds max_line_chars %d", len([]rune(t)), lc.MaxChars)
	}
	if broadcastRe.MatchString(t) {
		return lintErr("broadcast", "no @here, @channel, @everyone or <!...> mentions")
	}
	for _, m := range userMention.FindAllStringSubmatch(t, -1) {
		if m[1] != lc.OperatorID {
			return lintErr("mention", "only the operator may be mentioned, not <@%s>", m[1])
		}
	}
	for _, m := range atNameRe.FindAllStringSubmatch(t, -1) {
		if !strings.EqualFold(m[1], lc.OperatorName) {
			return lintErr("mention", "no @%s: personas never mention apps or people other than the operator", m[1])
		}
	}
	if slashRe.MatchString(t) {
		return lintErr("slash", "personas never type slash commands")
	}
	if aiRe.MatchString(t) {
		return lintErr("ai", "out-of-character AI reference")
	}
	for _, u := range urlRe.FindAllString(t, -1) {
		if lc.Evidence == "" || !strings.Contains(lc.Evidence, strings.TrimRight(u, ".,);")) {
			return lintErr("url", "URL %q is not in the ledger", u)
		}
	}
	if m := irmClaimRe.FindString(t); m != "" {
		return lintErr("irm_claim", "%q: only the operator acts in IRM; ask him instead", strings.TrimSpace(m))
	}
	if m := opsClaimRe.FindString(t); m != "" && !lc.Cited {
		return lintErr("claim", "%q claims a completed action without citing the ledger", strings.TrimSpace(m))
	}
	if len(metricRe.FindAllStringSubmatch(t, -1)) > 0 {
		have := metricSet(lc.Evidence)
		for _, m := range metricRe.FindAllStringSubmatch(t, -1) {
			if k := metricKey(m[1], m[2]); !have[k] {
				return lintErr("number", "metric %s%s is not in the ledger", m[1], m[2])
			}
		}
	}
	known := map[string]bool{}
	for _, n := range append([]string{lc.OperatorName}, lc.KnownNames...) {
		for _, f := range strings.Fields(n) {
			known[strings.ToLower(f)] = true
		}
	}
	var cands []string
	if m := vocativeStart.FindStringSubmatch(t); m != nil {
		cands = append(cands, m[1])
	}
	for _, m := range vocativeWord.FindAllStringSubmatch(t, -1) {
		cands = append(cands, m[1])
	}
	for _, c := range cands {
		lc := strings.ToLower(c)
		if !known[lc] && !nameStoplist[lc] {
			return lintErr("unknown_name", "%q is not in the cast", c)
		}
	}
	return nil
}

func metricKey(num, unit string) string {
	u := strings.ToLower(unit)
	switch u {
	case "sec", "secs":
		u = "s"
	case "error":
		u = "errors"
	}
	return num + "|" + u
}

func metricSet(evidence string) map[string]bool {
	out := map[string]bool{}
	for _, m := range metricRe.FindAllStringSubmatch(evidence, -1) {
		out[metricKey(m[1], m[2])] = true
	}
	return out
}
