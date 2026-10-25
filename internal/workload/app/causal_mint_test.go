// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"bytes"
	"context"
	"math"
	"strconv"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/rknightion/synthkit/internal/causal"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/ledger"
	"github.com/rknightion/synthkit/internal/shape"
)

// TestCausalTimelineRoundsPreWorkBeforeAddingStart exercises the evaluator, not a copy of its
// arithmetic. These synthetic durations put the child at a half-nanosecond boundary: rounding
// 0.1*legacyMS to binary64 before adding start gives 3099467568 ns, whereas a fused multiply-add
// gives 3099467567 ns. arm64 used to fuse the cursor expression; baseline amd64 did not.
func TestCausalTimelineRoundsPreWorkBeforeAddingStart(t *testing.T) {
	ev := evaluator{
		p:        &causalPlan{nodes: map[string]*causalNode{"parent": {}, "child": {}}},
		legacy:   []ledger.Call{{Target: "child", Duration: time.Millisecond}},
		causes:   []string{""},
		children: map[int][]int{-1: {0}},
	}
	ev.serve("parent", "parent#0", -1, -1, 2766.078404020539, 3333.891634794606, false)
	if got, want := ev.out[0].Offset, time.Duration(3099467568); got != want {
		t.Fatalf("child offset = %d ns, want %d ns (pre-work must round before addition)", got, want)
	}
}

// causalNow is a Monday on the business plateau (UTC), inside every test incident window.
var causalNow = time.Date(2026, 6, 15, 13, 0, 0, 0, time.UTC)

// causalResources mirrors the design §2.1 sketch with demands sized for one 60 rps intake app
// (§2.3: a resource's declared demand covers every caller through it).
const causalResources = `
- {name: intake-db-pool, kind: pool, size: 10, hold_ms: 20, held_across: records-db, acquire_timeout_ms: 3000,
   demand: {off_peak_rps: 5, peak_rps: 60},
   error: {status: 503, type: SQLTransientConnectionException, message: "intake-db-pool: connection is not available, request timed out after 3000ms"}}
- {name: records-db, kind: dependency, latency_ms: 15, slow_factor: 12}
- {name: intake-quota, kind: quota, limit_rps: 80, demand: {off_peak_rps: 5, peak_rps: 60}}
- {name: partner-api, kind: dependency, latency_ms: 120}
- {name: partner-api-cert, kind: credential, of: partner-api, credential_type: server_certificate,
   error: {type: SSLHandshakeException, message: "partner-api: PKIX path validation failed: certificate expired"}}
- {name: enrich-fn-concurrency, kind: concurrency, size: 80, hold_ms: 900, held_across: partner-api,
   demand: {off_peak_rps: 5, peak_rps: 60},
   error: {status: 429, type: TooManyRequestsException, message: "Rate Exceeded."}}
- {name: validation-queue, kind: queue, size: 40, hold_ms: 400, held_across: external-validator,
   demand: {off_peak_rps: 5, peak_rps: 60}}
- {name: external-validator, kind: dependency, latency_ms: 350, slow_factor: 3}
- {name: completeness-rule, kind: rule, reject_share: 0.35,
   error: {status: 422, message: "rule completeness rejected record: mandatory field missing"}}
`

const intakeApp = `
traffic: {off_peak_rps: 5, peak_rps: 60}
services:
  - name: intake-api
    entry: true
    uses: [intake-quota, intake-db-pool, completeness-rule]
    calls: [records-db, enrich-fn, validation-publisher]
    call_policies:
      enrich-fn: {timeout_ms: 10000, retries: 2, backoff_ms: 200, retry_on: [throttle, 5xx]}
  - {name: records-db, type: db, uses: [records-db]}
  - name: enrich-fn
    type: function
    external: true
    uses: [enrich-fn-concurrency]
    calls: [partner-gateway]
    call_policies:
      partner-gateway: {timeout_ms: 2000, retries: 1, retry_on: [5xx, timeout, connect]}
  - {name: partner-gateway, external: true, uses: [partner-api]}
  - {name: validation-publisher, uses: [validation-queue]}
`

const validatorApp = `
traffic: {from_resource: validation-queue}
services:
  - name: validation-worker
    entry: true
    worker: true
    calls: [external-validator-node]
  - {name: external-validator-node, external: true, uses: [external-validator]}
`

// legacyIntakeApp is intakeApp without any causal field: the same graph and the same legacy draws.
const legacyIntakeApp = `
traffic: {off_peak_rps: 5, peak_rps: 60}
services:
  - {name: intake-api, entry: true, calls: [records-db, enrich-fn, validation-publisher]}
  - {name: records-db, type: db}
  - {name: enrich-fn, type: function, external: true, calls: [partner-gateway]}
  - {name: partner-gateway, external: true}
  - {name: validation-publisher}
`

func decodeResources(t *testing.T, doc string) []causal.Resource {
	t.Helper()
	var rs []causal.Resource
	dec := yaml.NewDecoder(bytes.NewReader([]byte(doc)))
	dec.KnownFields(true)
	if err := dec.Decode(&rs); err != nil {
		t.Fatalf("decode resources: %v", err)
	}
	return rs
}

// causalApps resolves the set the way the blueprint resolver does (declared set, every app's
// loads, Validate, injection) and builds each app in env prod.
func causalApps(t *testing.T, resources string, docs ...string) []*Workload {
	t.Helper()
	ws, err := tryCausalApps(t, resources, docs...)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	return ws
}

func tryCausalApps(t *testing.T, resources string, docs ...string) ([]*Workload, error) {
	t.Helper()
	declared := causal.Set{Envs: []causal.Declaration{{Env: "prod", Weight: 1, Resources: decodeResources(t, resources)}}}
	set := declared
	cfgs := make([]*Config, len(docs))
	for i, d := range docs {
		cfgs[i] = decodeStrict(t, d)
		loads, err := cfgs[i].CausalLoads(declared, "prod")
		if err != nil {
			return nil, err
		}
		set.Loads = append(set.Loads, loads...)
	}
	if err := causal.Validate(set); err != nil {
		return nil, err
	}
	var out []*Workload
	for i, cfg := range cfgs {
		cfg.UseResources(set)
		w, err := build(cfg, core.Binding{Name: "app" + string(rune('a'+i)), Env: &fixture.Env{Name: "prod", Weight: 1}, Cluster: coretest.Cluster()})
		if err != nil {
			return nil, err
		}
		out = append(out, w.(*Workload))
	}
	return out, nil
}

// incident schedules one resource fault from 12:00 for two hours.
func incident(mode, target string, intensity float64) *shape.Engine {
	return shape.New("UTC", []string{mode + "@2026-06-15T12:00:00Z/2h#" + strconv.FormatFloat(intensity, 'f', -1, 64) + "@" + target})
}

func mintAt(w *Workload, eng *shape.Engine, now time.Time) []*ledger.Request {
	return w.m.Mint(now, 60, eng)
}

func callsTo(r *ledger.Request, target string) []ledger.Call {
	var out []ledger.Call
	for _, c := range r.Calls {
		if c.Target == target {
			out = append(out, c)
		}
	}
	return out
}

func meanHop(reqs []*ledger.Request, target string) float64 {
	var sum, n float64
	for _, r := range reqs {
		for _, c := range callsTo(r, target) {
			sum += float64(c.Duration) / float64(time.Millisecond)
			n++
		}
	}
	return sum / math.Max(n, 1)
}

// Story 5: a slow records database stretches the intake pool's hold past capacity, so a share of
// requests fail at the entry with the pool's 503 after the acquire timeout; with the fault
// inactive no request meets the pool.
func TestCausalStoryPoolExhaustion(t *testing.T) {
	w := causalApps(t, causalResources, intakeApp)[0]
	healthy := mintAt(w, shape.New("UTC", nil), causalNow)
	for _, r := range healthy {
		if r.Fault != nil && r.Fault.Resource == "intake-db-pool" {
			t.Fatalf("healthy request met the pool: %+v", r.Fault)
		}
		if !r.Causal {
			t.Fatal("causal request not marked")
		}
	}
	if got := meanHop(healthy, "records-db"); got < 10 || got > 20 {
		t.Errorf("healthy records-db hop mean %.1f ms, want about latency_ms 15", got)
	}

	w = causalApps(t, causalResources, intakeApp)[0]
	reqs := mintAt(w, incident(causal.ModeDependencySlow, "records-db", 1), causalNow)
	pool := 0
	for _, r := range reqs {
		if r.Fault == nil || r.Fault.Resource != "intake-db-pool" || r.StatusCode != 503 {
			continue
		}
		pool++
		if len(r.Calls) != 0 {
			t.Fatalf("an entry rejection made calls: %+v", r.Calls)
		}
		if r.Duration < 3*time.Second {
			t.Fatalf("pool rejection lasted %v, want at least the 3000 ms acquire timeout", r.Duration)
		}
		if r.ErrorKind != "SQLTransientConnectionException" || r.Outcome != ledger.OutcomeServerError {
			t.Fatalf("pool rejection facts: kind %q outcome %v", r.ErrorKind, r.Outcome)
		}
		if !strings.Contains(r.Fault.Message, "connection is not available") {
			t.Fatalf("fault message %q", r.Fault.Message)
		}
	}
	// h = 20 + 15 x 11 = 185 ms; offered 60 x 0.185 = 11.1 of 10: reject 1 - 1/1.11 = 9.9%.
	if share := float64(pool) / float64(len(reqs)); math.Abs(share-0.099) > 0.02 {
		t.Errorf("pool rejection share %.3f, want about 0.099", share)
	}
	if got := meanHop(reqs, "records-db"); got < 150 || got > 220 {
		t.Errorf("slowed records-db hop mean %.1f ms, want about 180", got)
	}
}

// Story 1: an expired partner certificate fails every handshake at the caller (no server span);
// enrich-fn answers 502, intake-api retries it under its 5xx policy, and the request is a 502
// carrying the certificate fault.
func TestCausalStoryCertificateExpired(t *testing.T) {
	w := causalApps(t, causalResources, intakeApp)[0]
	reqs := mintAt(w, incident(causal.ModeCredentialExpired, "partner-api-cert", 1), causalNow)
	seen := 0
	for _, r := range reqs {
		if r.Fault == nil || r.Fault.Resource != "partner-api-cert" {
			continue
		}
		seen++
		if r.StatusCode != 502 || r.ErrorKind != "SSLHandshakeException" {
			t.Fatalf("cert request status %d kind %q", r.StatusCode, r.ErrorKind)
		}
		if r.Fault.Exception != "SSLHandshakeException" || !strings.Contains(r.Fault.Message, "certificate expired") {
			t.Fatalf("cert fault %+v", r.Fault)
		}
		enrich := callsTo(r, "enrich-fn")
		if len(enrich) != 3 {
			t.Fatalf("enrich-fn attempts %d, want 3 (retries 2 on 5xx)", len(enrich))
		}
		for k, c := range enrich {
			if c.Attempt != k || !c.Failed || c.StatusCode != 502 {
				t.Fatalf("enrich-fn attempt %d: %+v", k, c)
			}
			if k > 0 && c.Offset <= enrich[k-1].Offset+enrich[k-1].Duration {
				t.Fatalf("attempt %d starts at %v, before attempt %d ends plus backoff", k, c.Offset, k-1)
			}
		}
		for _, c := range callsTo(r, "partner-gateway") {
			if !c.Failed || c.PeerSpanID != "" || c.StatusCode != 0 {
				t.Fatalf("handshake failure hop %+v, want failed, no server span, no status", c)
			}
			if c.Attempt != 0 {
				t.Fatalf("credential failure retried: %+v", c)
			}
		}
		if len(callsTo(r, "validation-publisher")) != 0 {
			t.Fatal("call after an unabsorbed failure was not dropped")
		}
		var end time.Duration
		for _, c := range r.Calls {
			if e := c.Offset + c.Duration; e > end {
				end = e
			}
		}
		if end > r.Duration {
			t.Fatalf("a hop ends at %v, after the request (%v)", end, r.Duration)
		}
	}
	// Requests the entry rejects first (rule) never reach enrich-fn; every other one fails.
	if seen < len(reqs)/2 {
		t.Fatalf("only %d of %d requests carry the certificate fault", seen, len(reqs))
	}
}

// Story 2: reduced function concurrency throttles a share of attempts; the 429s are retried, some
// absorbed (success with a failed first attempt), some returned as 503.
func TestCausalStoryConcurrencyThrottle(t *testing.T) {
	w := causalApps(t, causalResources, intakeApp)[0]
	reqs := mintAt(w, incident(causal.ModeConcurrencyReduced, "enrich-fn-concurrency", 0.7), causalNow)
	absorbed, returned := 0, 0
	for _, r := range reqs {
		enrich := callsTo(r, "enrich-fn")
		if len(enrich) < 2 {
			continue
		}
		if enrich[0].StatusCode != 429 || enrich[0].Fault == nil || enrich[0].Fault.ErrorType != "TooManyRequestsException" {
			t.Fatalf("first attempt %+v", enrich[0])
		}
		last := enrich[len(enrich)-1]
		switch {
		case !last.Failed:
			absorbed++
			if r.Fault != nil && r.Fault.Resource == "enrich-fn-concurrency" && r.Outcome != ledger.OutcomeSuccess {
				t.Fatalf("absorbed throttle failed the request: %+v", r)
			}
		case len(enrich) == 3:
			returned++
			if r.StatusCode != 503 || r.Fault.Resource != "enrich-fn-concurrency" {
				t.Fatalf("unabsorbed throttle: status %d fault %+v", r.StatusCode, r.Fault)
			}
		}
	}
	if absorbed == 0 || returned == 0 {
		t.Fatalf("absorbed %d, returned %d; want both", absorbed, returned)
	}
}

// Story 3: a regressed rule rejects its share of records at the entry with its 422.
func TestCausalStoryRuleRegression(t *testing.T) {
	w := causalApps(t, causalResources, intakeApp)[0]
	reqs := mintAt(w, incident(causal.ModeRuleRegression, "completeness-rule", 1), causalNow)
	n := 0
	for _, r := range reqs {
		if r.Fault != nil && r.Fault.Mode == causal.ModeRuleRegression {
			n++
			if r.StatusCode != 422 || r.Outcome != ledger.OutcomeClientError || len(r.Calls) != 0 {
				t.Fatalf("rule rejection %d %v calls %d", r.StatusCode, r.Outcome, len(r.Calls))
			}
		}
	}
	if share := float64(n) / float64(len(reqs)); math.Abs(share-0.35) > 0.03 {
		t.Errorf("rule rejection share %.3f, want 0.35", share)
	}
}

// Rule failures are never resent, whatever status they carry (root ruling): a 500 rule on a
// callee with a 5xx retry policy makes one attempt.
func TestCausalRuleFailureNeverRetried(t *testing.T) {
	res := causalResources + `- {name: bad-deploy, kind: rule, reject_share: 1, error: {status: 500}}
`
	app := `
traffic: {off_peak_rps: 5, peak_rps: 60}
services:
  - name: front
    entry: true
    calls: [back]
    call_policies: {back: {retries: 3, retry_on: [5xx]}}
  - {name: back, uses: [bad-deploy]}
`
	w := causalApps(t, res, app)[0]
	reqs := mintAt(w, incident(causal.ModeRuleRegression, "bad-deploy", 1), causalNow)
	for _, r := range reqs {
		if got := len(callsTo(r, "back")); got != 1 {
			t.Fatalf("rule failure attempted %d times, want 1", got)
		}
		if r.StatusCode != 502 || r.Calls[0].StatusCode != 500 {
			t.Fatalf("request %d hop %d, want 502 over a 500", r.StatusCode, r.Calls[0].StatusCode)
		}
	}
	if len(reqs) == 0 {
		t.Fatal("no requests")
	}
}

// Only the service that calls the rule's node holds a rule rejection back (docs/workloads.md,
// retry rules): the next caller up sees the mapped 502 and resends it under its 5xx policy, and
// each resend meets the rule once more at the direct caller.
func TestCausalMappedRuleRejectionResentAboveDirectCaller(t *testing.T) {
	res := causalResources + `- {name: bad-deploy, kind: rule, reject_share: 1, error: {status: 500}}
`
	app := `
traffic: {off_peak_rps: 5, peak_rps: 60}
services:
  - name: front
    entry: true
    calls: [mid]
    call_policies: {mid: {retries: 2, retry_on: [5xx]}}
  - name: mid
    calls: [back]
    call_policies: {back: {retries: 3, retry_on: [5xx]}}
  - {name: back, uses: [bad-deploy]}
`
	w := causalApps(t, res, app)[0]
	reqs := mintAt(w, incident(causal.ModeRuleRegression, "bad-deploy", 1), causalNow)
	if len(reqs) == 0 {
		t.Fatal("no requests")
	}
	for _, r := range reqs {
		mid, back := callsTo(r, "mid"), callsTo(r, "back")
		if len(mid) != 3 || len(back) != 3 {
			t.Fatalf("mid attempts %d, back attempts %d; want 3 each (front resends the mapped 502, mid never resends the rule)", len(mid), len(back))
		}
		for _, c := range mid {
			if c.StatusCode != 502 {
				t.Fatalf("mid attempt status %d, want 502", c.StatusCode)
			}
		}
		if r.StatusCode != 502 || r.Fault == nil || r.Fault.Resource != "bad-deploy" {
			t.Fatalf("request %d fault %+v, want 502 carrying the rule", r.StatusCode, r.Fault)
		}
	}
}

// Story 4: a slow external validator cuts the queue's service rate; the consumer app's rate is
// the queue's served rate, its requests record the backlog wait, and its validator hop slows.
func TestCausalStoryQueueConsumer(t *testing.T) {
	ws := causalApps(t, causalResources, intakeApp, validatorApp)
	v := ws[1]
	healthyEng := shape.New("UTC", nil)
	v.AdvanceCausal(causalNow, healthyEng)
	healthyRPS := v.m.rpsAt(causalNow, healthyEng)
	if math.Abs(healthyRPS-60) > 1 {
		t.Errorf("healthy consumer rate %.2f, want the 60 rps demand fully served", healthyRPS)
	}
	healthy := mintAt(v, healthyEng, causalNow)
	if got := meanHop(healthy, "external-validator-node"); got < 280 || got > 420 {
		t.Errorf("healthy validator hop mean %.1f ms, want about 350", got)
	}
	for _, r := range healthy {
		// A bound consumer's baseline failure is its background error_rate cause (SKT-0204); a
		// successful healthy request carries no fault.
		if r.Outcome != ledger.OutcomeSuccess {
			if r.Fault == nil || r.Fault.Mode != "error_rate" || r.Fault.Resource != "" {
				t.Fatalf("background consumer failure carries %+v, want the error_rate cause", r.Fault)
			}
			continue
		}
		if r.Fault != nil {
			t.Fatalf("healthy consumer request carries %+v", r.Fault)
		}
	}

	v = causalApps(t, causalResources, intakeApp, validatorApp)[1]
	eng := incident(causal.ModeDependencySlow, "external-validator", 1)
	v.AdvanceCausal(causalNow, eng)
	// h = 400 + 350 x 2 = 1100 ms: mu = 40 / 1.1 = 36.4 msg/s, below the 60 rps demand.
	if got := v.m.rpsAt(causalNow, eng); math.Abs(got-36.36) > 0.5 {
		t.Errorf("slowed consumer rate %.2f, want the served rate 36.4", got)
	}
	reqs := mintAt(v, eng, causalNow)
	if got := meanHop(reqs, "external-validator-node"); got < 900 || got > 1200 {
		t.Errorf("slowed validator hop mean %.1f ms, want about 1050", got)
	}
	for _, r := range reqs {
		if r.Outcome != ledger.OutcomeSuccess {
			continue // the baseline error_rate draw; a backlog wait never fails a request
		}
		if r.Fault == nil || r.Fault.Resource != "validation-queue" || !strings.Contains(r.Fault.Message, "queue backlog") || r.Fault.ErrorType != "" {
			t.Fatalf("consumer request fault %+v, want the queue backlog", r.Fault)
		}
	}
}

// Bound services take their duration from the declared hold or latency (M3): the function's
// mean follows its 900 ms hold, and a slowed held dependency stretches it.
func TestCausalBoundDurations(t *testing.T) {
	w := causalApps(t, causalResources, intakeApp)[0]
	reqs := mintAt(w, shape.New("UTC", nil), causalNow)
	if got := meanHop(reqs, "enrich-fn"); got < 800 || got > 1000 {
		t.Errorf("enrich-fn hop mean %.1f ms, want about the 900 ms hold", got)
	}
	if got := meanHop(reqs, "partner-gateway"); got < 100 || got > 140 {
		t.Errorf("partner-gateway hop mean %.1f ms, want about latency_ms 120", got)
	}
	w = causalApps(t, causalResources, intakeApp)[0]
	reqs = mintAt(w, incident(causal.ModeDependencySlow, "partner-api", 1), causalNow)
	// slow_factor 4 (default): h = 900 + 120 x 3 = 1260 ms; partner latency 480 ms.
	if got := meanHop(reqs, "partner-gateway"); got < 420 || got > 540 {
		t.Errorf("slowed partner-gateway mean %.1f ms, want about 480", got)
	}
	enrich := 0.0
	n := 0.0
	for _, r := range reqs {
		for _, c := range callsTo(r, "enrich-fn") {
			enrich += float64(c.Duration) / float64(time.Millisecond)
			n++
		}
	}
	if got := enrich / n; got < 1150 || got > 1400 {
		t.Errorf("slowed enrich-fn mean %.1f ms, want about 1260", got)
	}
}

// A healthy causal request keeps the legacy facts of every unbound service, and the causal path
// adds no engine draw (the next draw of each engine agrees). Its hops sit on the engine's timeline
// (SKT-0200; a baseline-failed request too, SKT-0206, keeping its drawn outcome, status and error
// kind): each carries the engine's Offset and a serving node's 200, and a caller's duration covers
// its calls in sequence, so only an unbound leaf keeps its legacy duration exactly.
func TestCausalHealthyIdentityAndDrawCount(t *testing.T) {
	for _, tc := range []struct {
		name string
		eng  func() *shape.Engine
	}{
		{"healthy", func() *shape.Engine { return shape.New("UTC", nil) }},
		{"fault", func() *shape.Engine { return incident(causal.ModeConcurrencyReduced, "enrich-fn-concurrency", 0.7) }},
		{"spike", func() *shape.Engine {
			return shape.New("UTC", []string{"error_spike@2026-06-15T12:00:00Z/2h#1@enrich-fn", "error_spike@2026-06-15T12:00:00Z/2h#0.5@intake-api"})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cw := causalApps(t, causalResources, intakeApp)[0]
			lw := buildApp(t, decodeStrict(t, legacyIntakeApp))
			ce, le := tc.eng(), tc.eng()
			var creqs, lreqs []*ledger.Request
			for i := range 3 {
				at := causalNow.Add(time.Duration(i) * time.Minute)
				creqs = append(creqs, mintAt(cw, ce, at)...)
				lreqs = append(lreqs, mintAt(lw, le, at)...)
			}
			if len(creqs) != len(lreqs) {
				t.Fatalf("causal minted %d, legacy %d", len(creqs), len(lreqs))
			}
			if a, b := ce.Float64(), le.Float64(); a != b {
				t.Fatalf("engine draw sequences diverged: next draw %v vs %v", a, b)
			}
			if tc.name != "healthy" {
				return
			}
			bound := map[string]bool{"records-db": true, "enrich-fn": true, "partner-gateway": true}
			for i, c := range creqs {
				l := lreqs[i]
				if c.Route != l.Route || c.Outcome != l.Outcome || c.StatusCode != l.StatusCode || c.ErrorKind != l.ErrorKind || c.BrowserOrigin != l.BrowserOrigin || c.Fault != nil {
					t.Fatalf("request %d facts differ: causal %+v legacy %+v", i, c, l)
				}
				if len(c.Calls) != len(l.Calls) {
					t.Fatalf("request %d: %d calls vs %d", i, len(c.Calls), len(l.Calls))
				}
				// Every request, a baseline-failed one included, is on the engine's timeline.
				caller := map[int]bool{}
				for _, cc := range c.Calls {
					caller[cc.ParentHopIndex] = true
				}
				for j := range c.Calls {
					cc, lc := c.Calls[j], l.Calls[j]
					cc.SpanID, cc.PeerSpanID, lc.SpanID, lc.PeerSpanID = "", "", "", ""
					if j > 0 && cc.Offset <= 0 || cc.StatusCode != 0 && cc.StatusCode != 200 {
						t.Fatalf("request %d call %d: offset %v status %d, want an engine offset and no failure status", i, j, cc.Offset, cc.StatusCode)
					}
					cc.Offset, cc.StatusCode = 0, 0
					if bound[cc.Target] || caller[j] {
						cc.Duration, lc.Duration = 0, 0
					}
					if cc != lc {
						t.Fatalf("request %d call %d differs:\n causal %+v\n legacy %+v", i, j, cc, lc)
					}
				}
			}
		})
	}
}

// SKT-0168.09.14: in a causal workload a downstream error_spike fails its hop and the request,
// and a retried attempt redraws it, so a retry can recover the spike.
func TestCausalSpikePropagatesAndRetryRecovers(t *testing.T) {
	w := causalApps(t, causalResources, intakeApp)[0]
	eng := shape.New("UTC", []string{"error_spike@2026-06-15T12:00:00Z/2h#1@enrich-fn"})
	reqs := mintAt(w, eng, causalNow)
	failedAbsorbed, failedReturned := 0, 0
	for _, r := range reqs {
		enrich := callsTo(r, "enrich-fn")
		if len(enrich) == 0 || !enrich[0].Failed {
			continue
		}
		if enrich[0].StatusCode != 500 || enrich[0].Fault == nil || enrich[0].Fault.Mode != "error_spike" {
			t.Fatalf("spiked hop %+v", enrich[0])
		}
		if enrich[len(enrich)-1].Failed {
			failedReturned++
			if r.StatusCode != 502 || r.Outcome != ledger.OutcomeServerError {
				t.Fatalf("unabsorbed spike: request %d", r.StatusCode)
			}
		} else {
			failedAbsorbed++
		}
	}
	if failedAbsorbed == 0 || failedReturned == 0 {
		t.Fatalf("absorbed %d returned %d; want both (spike 0.6 per attempt)", failedAbsorbed, failedReturned)
	}
}

// CausalLoads returns the app's traffic share of each capacity resource it reaches, with the
// policy of the call into the service that uses it.
func TestCausalLoads(t *testing.T) {
	declared := causal.Set{Envs: []causal.Declaration{{Env: "prod", Weight: 1, Resources: decodeResources(t, causalResources)}}}
	loads, err := decodeStrict(t, intakeApp).CausalLoads(declared, "prod")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]causal.Load{}
	for _, l := range loads {
		got[l.Resource] = l
	}
	if len(loads) != 4 {
		t.Fatalf("loads %+v, want quota, pool, concurrency and queue", loads)
	}
	for _, name := range []string{"intake-quota", "intake-db-pool", "enrich-fn-concurrency", "validation-queue"} {
		if got[name].Share != 1 {
			t.Errorf("%s share %v, want 60/60", name, got[name].Share)
		}
	}
	if p := got["enrich-fn-concurrency"].Policy; p.Retries != 2 || len(p.RetryOn) != 2 {
		t.Errorf("enrich-fn-concurrency policy %+v, want the intake-api -> enrich-fn policy", p)
	}
	if p := got["intake-db-pool"].Policy; p.Retries != 0 {
		t.Errorf("entry resource carries policy %+v", p)
	}
	half := strings.Replace(intakeApp, "peak_rps: 60}", "peak_rps: 30}", 1)
	loads, _ = decodeStrict(t, half).CausalLoads(declared, "prod")
	if loads[0].Share != 0.5 {
		t.Errorf("share at 30 of 60 rps = %v", loads[0].Share)
	}
}

func TestCausalLoadValidation(t *testing.T) {
	for _, tc := range []struct{ name, app, want string }{
		{"unknown", strings.Replace(intakeApp, "uses: [records-db]", "uses: [nope]", 1), `"nope" is not declared`},
		{"entry dependency", strings.Replace(intakeApp, "uses: [intake-quota,", "uses: [records-db, intake-quota,", 1), "nothing calls the entry"},
		{"demand", strings.Replace(intakeApp, "peak_rps: 60}", "peak_rps: 70}", 1), "below this app's peak traffic"},
		{"policy key", strings.Replace(intakeApp, "enrich-fn: {timeout_ms", "nobody: {timeout_ms", 1), "not in calls"},
		{"worker", strings.Replace(intakeApp, "entry: true\n", "entry: true\n    worker: true\n", 1), "worker needs traffic.from_resource"},
		{"shape", intakeApp + "shape_profile: {floor: 0.2}\n", "shape_profile"},
		{"retries within bound", strings.Replace(strings.Replace(intakeApp, "retries: 2,", "retries: 3,", 1), "retries: 1,", "retries: 3,", 1), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tryCausalApps(t, causalResources, tc.app)
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
	// Causal fields without declared resources are rejected at build.
	cfg := decodeStrict(t, intakeApp)
	if _, err := build(cfg, core.Binding{Name: "x", Env: coretest.Env(), Cluster: coretest.Cluster()}); err == nil || !strings.Contains(err.Error(), "need declared environments[].resources") {
		t.Fatalf("no resources: %v", err)
	}
}

// The worst-case call expansion (retries x subtree) is bounded.
func TestCausalExpansionBound(t *testing.T) {
	app := `
traffic: {off_peak_rps: 1, peak_rps: 1}
services:
  - {name: a, entry: true, calls: [b], call_policies: {b: {retries: 3}}}
  - {name: b, calls: [c], call_policies: {c: {retries: 3}}}
  - {name: c, calls: [d], call_policies: {d: {retries: 3}}}
  - {name: d, calls: [e], call_policies: {e: {retries: 3}}}
  - {name: e}
`
	_, err := tryCausalApps(t, causalResources, app)
	if err == nil || !strings.Contains(err.Error(), "more than 256") {
		t.Fatalf("expansion: %v", err)
	}
	// A diamond: the minter reaches c, d and e down the a>b>c>d>e chain (DFS), so the bound
	// counts that chain under its retry multipliers: 4 x (1 + 4 x (1 + 4 x (1 + 4))) = 340.
	diamond := `
traffic: {off_peak_rps: 1, peak_rps: 1}
services:
  - {name: a, entry: true, calls: [b, c, d, e], call_policies: {b: {retries: 3}}}
  - {name: b, calls: [c], call_policies: {c: {retries: 3}}}
  - {name: c, calls: [d], call_policies: {d: {retries: 3}}}
  - {name: d, calls: [e], call_policies: {e: {retries: 3}}}
  - {name: e}
`
	_, err = tryCausalApps(t, causalResources, diamond)
	if err == nil || !strings.Contains(err.Error(), "to 340 calls") {
		t.Fatalf("diamond expansion: %v", err)
	}
}

// A pool held across a dependency that the node also calls spends the hold's stretch inside that
// call, so the node slows by the dependency's factor and not by more (SKT-0168.09.36). Real
// connection pools behave this way: the connection is held while the query runs, so a slower
// query is the stretch of the hold, not a second delay beside it.
const heldCallResources = `
- {name: store-pool, kind: pool, size: 400, hold_ms: 400, held_across: store-db, acquire_timeout_ms: 3000,
   demand: {off_peak_rps: 5, peak_rps: 60}}
- {name: store-db, kind: dependency, latency_ms: 400, slow_factor: 4}
`

// heldCallApp is the layout: svc holds store-pool (across store-db) and calls store, which uses
// store-db.
const heldCallApp = `
traffic: {off_peak_rps: 5, peak_rps: 60}
services:
  - name: gateway
    entry: true
    calls: [svc]
  - {name: svc, uses: [store-pool], calls: [store]}
  - {name: store, type: db, uses: [store-db]}
`

func TestCausalHeldDependencyCountedOnce(t *testing.T) {
	mean := func(eng *shape.Engine, target string) float64 {
		w := causalApps(t, heldCallResources, heldCallApp)[0]
		var all []*ledger.Request
		for i := range 10 {
			all = append(all, mintAt(w, eng, causalNow.Add(time.Duration(i)*time.Minute))...)
		}
		return meanHop(all, target)
	}
	healthy := shape.New("UTC", nil)
	slow := incident(causal.ModeDependencySlow, "store-db", 1) // lf = 4
	storeH, storeS := mean(healthy, "store"), mean(slow, "store")
	svcH, svcS := mean(healthy, "svc"), mean(slow, "svc")
	depFactor := storeS / storeH
	t.Logf("store hop %.1f -> %.1f ms (x%.3f), svc hop %.1f -> %.1f ms", storeH, storeS, depFactor, svcH, svcS)
	if math.Abs(depFactor-4) > 0.25 {
		t.Fatalf("store-db slowed the store hop x%.2f, want about x4", depFactor)
	}
	// Healthy and slowed svc hops are both on the engine timeline (SKT-0200): the hold's pre-call
	// work plus the held call. Counted once, svc slows by exactly the store hop's extra time; counted
	// twice (the hold stretched beside the call) it would slow by about twice that.
	if extra, dep := svcS-svcH, storeS-storeH; math.Abs(extra-dep) > 0.06*dep {
		t.Errorf("svc held store-pool across store-db and called it: slowed by %.1f ms for a %.1f ms slower store hop, want within 6%%", extra, dep)
	}
}

// With a hold much longer than the held dependency's baseline latency, and a second call inside
// the hold, the node follows the hold rule h = hold_ms + latency_ms x (lf - 1): the other call
// is part of the hold and adds nothing, and only the held call carries the stretch.
func TestCausalHeldDependencyBesideAnotherCall(t *testing.T) {
	const resources = `
- {name: batch-pool, kind: pool, size: 400, hold_ms: 900, held_across: batch-db, acquire_timeout_ms: 3000,
   demand: {off_peak_rps: 5, peak_rps: 60}}
- {name: batch-db, kind: dependency, latency_ms: 100, slow_factor: 4}
`
	const app = `
traffic: {off_peak_rps: 5, peak_rps: 60}
services:
  - {name: gateway, entry: true, calls: [svc]}
  - {name: svc, uses: [batch-pool], calls: [store, audit]}
  - {name: store, type: db, uses: [batch-db]}
  - {name: audit}
`
	mean := func(eng *shape.Engine) float64 {
		w := causalApps(t, resources, app)[0]
		var all []*ledger.Request
		for i := range 10 {
			all = append(all, mintAt(w, eng, causalNow.Add(time.Duration(i)*time.Minute))...)
		}
		return meanHop(all, "svc")
	}
	healthy := mean(shape.New("UTC", nil))
	slow := mean(incident(causal.ModeDependencySlow, "batch-db", 1)) // lf = 4: h = 900 + 100 x 3
	t.Logf("healthy svc %.1f ms, slowed svc %.1f ms", healthy, slow)
	if healthy < 850 || healthy > 950 {
		t.Fatalf("healthy svc hop mean %.1f ms, want about the 900 ms hold", healthy)
	}
	if slow < 1140 || slow > 1260 {
		t.Errorf("slowed svc hop mean %.1f ms, want about the 1200 ms hold (900 + 100 x 3)", slow)
	}
}

// heldCallRun mints and ticks one healthy warm-up window and then the window under test for the
// held-call layout, the way faultRowsRun does for its own layout.
func heldCallRun(t *testing.T, eng *shape.Engine) faultRun {
	t.Helper()
	w := causalApps(t, heldCallResources, heldCallApp)[0]
	led := ledger.New(eng, 0, 0)
	led.AddMinter(w.Minter())
	run := faultRun{warm: &coretest.MetricCapture{}}
	before := causalNow.Add(-90 * time.Minute) // the incident starts at 12:00
	led.Mint(before)
	world := coretest.World(run.warm, nil, nil)
	world.Shape, world.Ledger = eng, led
	if err := w.Tick(context.Background(), before, world); err != nil {
		t.Fatalf("warm-up Tick: %v", err)
	}
	run.reqs = led.Mint(causalNow)
	run.mc = &coretest.MetricCapture{}
	world = coretest.World(run.mc, nil, nil)
	world.Shape, world.Ledger = eng, led
	if err := w.Tick(context.Background(), causalNow, world); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	return run
}

// The span-metric rows of the held-call layout follow the same factors as the request facts: the
// held dependency's slowdown is counted once on the service's SERVER row too.
func TestCausalHeldDependencySpanMetricRows(t *testing.T) {
	healthy := heldCallRun(t, shape.New("UTC", nil))
	slow := heldCallRun(t, incident(causal.ModeDependencySlow, "store-db", 1)) // lf = 4
	store := rowMeanLatency(slow, "svc", spanKindClient, "call store") / rowMeanLatency(healthy, "svc", spanKindClient, "call store")
	svc := rowMeanLatency(slow, "svc", spanKindServer, "") / rowMeanLatency(healthy, "svc", spanKindServer, "")
	t.Logf("span-metric rows: svc CLIENT call store x%.3f, svc SERVER x%.3f", store, svc)
	if math.IsNaN(store) || math.IsNaN(svc) {
		t.Fatalf("no rows: store %v svc %v", store, svc)
	}
	if math.Abs(store-4) > 0.4 {
		t.Fatalf("store row slowed x%.2f, want about x4", store)
	}
	if math.Abs(svc-store) > 0.1*store {
		t.Errorf("svc row slowed x%.2f for a x%.2f dependency, want within 10%%", svc, store)
	}
}
