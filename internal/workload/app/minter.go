// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rknightion/synthkit/internal/causal"
	"github.com/rknightion/synthkit/internal/genai"
	"github.com/rknightion/synthkit/internal/ledger"
	"github.com/rknightion/synthkit/internal/shape"
)

// minter is the app workload's contribution to the master clock: it mints the correlated
// narrative sample at the ENTRY node and walks the declared graph to build ONE request's hop tree
// (one trace across all reachable nodes). Volume + cadence-invariance mirror web_service's minter.
type minter struct {
	workloadName  string           // the app INSTANCE name — the ledger dispatch key (byWorkload)
	entry         string           // entry-node name
	pathNodes     []string         // every node reachable from the entry (incl entry) — the per-request incident scopes
	downstream    []downstreamRate // reachable non-entry services declaring a positive error_rate (opt-in; nil by default)
	env           string
	cluster       string
	weight        float64
	nonProd       bool
	rumEnabled    bool // true when the entry node carries rum_faro + binding.RUM is set
	traffic       Traffic
	load          *loadProfile  // shape_profile; nil uses the Engine's curve
	models        []ModelChoice // valid (model,provider) routings; one drawn per request → r.Model/r.Provider
	routes        []string      // entry-node request routes; one drawn per request → r.Route
	graph         *graph
	automation    *AutomationFlow // optional coherent app-local sequence; no graph calls
	agentPipeline *AgentPipeline
	tick          uint64
	// causal is the compiled causal derivation (internal/causal); nil keeps every request legacy.
	causal *causalPlan
	// retry is the entry client's re-send policy; nil re-sends nothing (the default).
	retry *ClientRetry
	// retryMu guards pending, the re-sends scheduled by earlier ticks.
	retryMu sync.Mutex
	pending []pendingRetry
}

// ClientRetry is the entry client's re-send policy: a failed entry request whose failure class is
// listed is sent again, after a backoff, as a NEW request of the same logical request (fresh
// trace, span and request IDs; the logical request's per-request draws repeat). The re-send
// draws its own outcome, so it may succeed; it is re-sent in turn while it fails a listed class
// and re-sends remain. Re-sends add to the declared request rate, as a real client's do.
type ClientRetry struct {
	// Retries is the number of re-sends after the first attempt, 1 to 3.
	Retries int `yaml:"retries"`
	// BackoffSeconds is the delay before re-send 1, doubled per further re-send (backoff x 2^k).
	BackoffSeconds float64 `yaml:"backoff_seconds"`
	// RetryOn lists the failure classes that are re-sent: 5xx (any 5xx status), throttle (429).
	RetryOn []string `yaml:"retry_on"`
}

// validate checks the policy (nil is valid: no re-sends) against the workload it applies to: only
// a graph request path that mints at its own rate re-sends.
func (c *ClientRetry) validate(cfg *Config) error {
	if c == nil {
		return nil
	}
	switch {
	case cfg.Automation != nil || cfg.AgentPipeline != nil:
		return fmt.Errorf("app: traffic.client_retry applies to graph requests, which an automation or agent_pipeline workload does not mint")
	case cfg.Traffic.FromResource != "":
		return fmt.Errorf("app: traffic.client_retry cannot combine with traffic.from_resource (a queue consumer mints exactly the messages its queue served)")
	case c.Retries < 1 || c.Retries > causal.MaxRetries:
		return fmt.Errorf("app: traffic.client_retry.retries %d must be in [1, %d]", c.Retries, causal.MaxRetries)
	case math.IsNaN(c.BackoffSeconds) || math.IsInf(c.BackoffSeconds, 0) || c.BackoffSeconds <= 0:
		return fmt.Errorf("app: traffic.client_retry.backoff_seconds must be a finite number > 0")
	case len(c.RetryOn) == 0:
		return fmt.Errorf("app: traffic.client_retry.retry_on must list at least one of 5xx, throttle")
	}
	seen := map[string]bool{}
	for _, class := range c.RetryOn {
		if class != causal.Retry5xx && class != causal.RetryThrottle {
			return fmt.Errorf("app: traffic.client_retry.retry_on %q must be one of 5xx, throttle", class)
		}
		if seen[class] {
			return fmt.Errorf("app: traffic.client_retry.retry_on %q repeats", class)
		}
		seen[class] = true
	}
	return nil
}

// resends reports whether a request answered with status is re-sent under the policy.
func (c *ClientRetry) resends(status int) bool {
	for _, class := range c.RetryOn {
		switch {
		case class == causal.Retry5xx && status >= 500 && status <= 599:
			return true
		case class == causal.RetryThrottle && status == 429:
			return true
		}
	}
	return false
}

// pendingRetry is one scheduled re-send of a logical request.
type pendingRetry struct {
	due     time.Time
	logical string // the logical request's seed unit (drawUnit of its first attempt)
	attempt int    // the re-send's LogicalAttempt
}

func newMinter(name, env, cluster string, weight float64, nonProd bool, traffic Traffic, models []ModelChoice, g *graph) *minter {
	pathNodes := reachableNodes(g)
	return &minter{
		workloadName: name,
		entry:        g.entry.decl.Name,
		pathNodes:    pathNodes,
		downstream:   downstreamRates(g, pathNodes),
		env:          env,
		cluster:      cluster,
		weight:       weight,
		nonProd:      nonProd,
		traffic:      traffic,
		models:       models,
		routes:       g.entry.decl.Routes,
		graph:        g,
	}
}

// drawIdx returns a uniform index into a set of length n (n>0); the per-request choice varies but
// never affects which series/labels appear (model/provider/route are body/attr/span-name FIELDS,
// not labels — the -dump inventory stays run-stable, I32).
func drawIdx(n int, eng *shape.Engine) int {
	i := int(eng.Float64() * float64(n))
	if i >= n {
		i = n - 1
	}
	return i
}

// drawRoute picks one entry route per request; default "GET /" when none declared (I13-safe default).
func (m *minter) drawRoute(eng *shape.Engine) string {
	if len(m.routes) == 0 {
		return "GET /"
	}
	return m.routes[drawIdx(len(m.routes), eng)]
}

// drawModel picks one valid (model,provider) routing per request, weighted by each model's
// genai.VolumeWeight (cheap/fast models run far more requests than flagships). Falls back to
// uniform draw for unknown IDs (VolumeWeight returns 1.0). Zero value when none declared
// (refs resolve empty → omitted, I13).
func (m *minter) drawModel(eng *shape.Engine) ModelChoice {
	if len(m.models) == 0 {
		return ModelChoice{}
	}
	if len(m.models) == 1 {
		return m.models[0]
	}
	// Build cumulative weight table.
	total := 0.0
	weights := make([]float64, len(m.models))
	for i, mc := range m.models {
		weights[i] = genai.VolumeWeight(mc.Model)
		total += weights[i]
	}
	r := eng.Float64() * total
	cumulative := 0.0
	for i, w := range weights {
		cumulative += w
		if r < cumulative {
			return m.models[i]
		}
	}
	return m.models[len(m.models)-1]
}

// reachableNodes returns every node reachable from the entry (spanning-tree order, incl the entry)
// — the set of per-request incident scopes (an incident on any node in the path shifts the request).
func reachableNodes(g *graph) []string {
	seen := map[string]bool{}
	var out []string
	var walk func(n *node)
	walk = func(n *node) {
		if seen[n.decl.Name] {
			return
		}
		seen[n.decl.Name] = true
		out = append(out, n.decl.Name)
		for _, c := range n.decl.Calls {
			if cn := g.byName[c]; cn != nil {
				walk(cn)
			}
		}
	}
	walk(g.entry)
	return out
}

// Workload implements ledger.Minter — the dispatch key (the app instance name). It MUST match the
// runner's byWorkload key so this ONE instance projects the whole graph (design §1/S5).
func (m *minter) Workload() string { return m.workloadName }

func (m *minter) rpsAt(now time.Time, eng *shape.Engine) float64 {
	if eng.Paused() { // explicit volume 0: no requests, not the off_peak_rps floor
		return 0
	}
	if m.causal != nil && m.causal.fromQueue != "" {
		return m.causal.servedRPS(now)
	}
	f := m.load.factor(now, eng, m.weight, m.nonProd)
	f = clampF(f, 0, 1)
	return m.traffic.OffPeakRPS + (m.traffic.PeakRPS-m.traffic.OffPeakRPS)*f
}

// expectedVolume = rps(now) × tickSec — FULL request volume, one trace per request (no
// narrative-sample clamp). Mirrors web_service's minter: trace volume tracks real RPS and
// matches the rps-derived metric volume, and is exactly linear in tickSec so cadence
// invariance (ARCHITECTURE I10) holds.
func (m *minter) expectedVolume(now time.Time, tickSec float64, eng *shape.Engine) float64 {
	return m.rpsAt(now, eng) * tickSec
}

// Mint fabricates this tick's correlated requests: StochasticRound of the expected volume, or
// for a from_resource consumer the whole messages its queue served in the window.
func (m *minter) Mint(now time.Time, tickSec float64, eng *shape.Engine) []*ledger.Request {
	// Empty ticks also consume an identity unit, matching the live tick sequence.
	tick := atomic.AddUint64(&m.tick, 1) - 1
	if m.causal != nil {
		m.causal.model.Advance(now, eng) // idempotent; no engine draw
	}
	var n int
	if m.causal != nil && m.causal.fromQueue != "" {
		// A queue consumer mints the whole messages the queue served in this window through a
		// deterministic carry: no engine draw, and the count matches a bound construct's.
		n = m.causal.mintServed(now, tickSec, eng.Paused())
	} else {
		n = ledger.StochasticRound(m.expectedVolume(now, tickSec, eng), eng.Float64())
	}
	if m.retry != nil {
		return m.mintWithRetries(now, eng, tick, n)
	}
	if n <= 0 {
		return nil
	}
	out := make([]*ledger.Request, 0, n)
	for requestIdx := range n {
		out = append(out, m.mintRequest(now, eng, tick, requestIdx))
	}
	return out
}

// mintWithRetries is Mint under a client re-send policy: the tick's n fresh requests, then every
// re-send due by now (request indexes continue after the fresh ones, so each attempt keeps its
// own fresh identity), then schedules a re-send of each that failed a listed class with re-sends
// left. A paused workload takes no traffic, so its due re-sends are dropped.
func (m *minter) mintWithRetries(now time.Time, eng *shape.Engine, tick uint64, n int) []*ledger.Request {
	m.retryMu.Lock()
	defer m.retryMu.Unlock()
	var due []pendingRetry
	kept := m.pending[:0]
	for _, p := range m.pending {
		if p.due.After(now) {
			kept = append(kept, p)
		} else if !eng.Paused() {
			due = append(due, p)
		}
	}
	m.pending = kept
	out := make([]*ledger.Request, 0, max(n, 0)+len(due))
	for requestIdx := range max(n, 0) {
		out = append(out, m.mintRequest(now, eng, tick, requestIdx))
	}
	for j, p := range due {
		r := m.mintRequest(now, eng, tick, max(n, 0)+j)
		r.Logical, r.LogicalAttempt = p.logical, p.attempt
		out = append(out, r)
	}
	for _, r := range out {
		if r.LogicalAttempt >= m.retry.Retries || !m.retry.resends(r.StatusCode) {
			continue
		}
		backoff := m.retry.BackoffSeconds * math.Pow(2, float64(r.LogicalAttempt))
		m.pending = append(m.pending, pendingRetry{
			due:     now.Add(time.Duration(backoff * float64(time.Second))),
			logical: drawUnit(r),
			attempt: r.LogicalAttempt + 1,
		})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// mintOne fabricates one fully-correlated request and walks the graph into a hop tree.
func (m *minter) mintOne(now time.Time, eng *shape.Engine) *ledger.Request {
	if m.causal != nil {
		m.causal.model.Advance(now, eng)
	}
	return m.mintRequest(now, eng, atomic.AddUint64(&m.tick, 1)-1, 0)
}

func (m *minter) mintRequest(now time.Time, eng *shape.Engine, tick uint64, requestIdx int) *ledger.Request {
	// Cross-trace join keys must be fresh even when an equivalent minter restarts.
	// Only the root SpanID is deterministic: span identity is scoped by TraceID,
	// and agent/tool selection (and automation planning) hashes this local key.
	// Keeping its binding/tick/request seed preserves fixed-input inventory without
	// making any live trace, session, request or cross-system join repeat.
	seed := fmt.Sprintf("app\x00%s\x00%s\x00%s\x00%d\x00%d", m.workloadName, m.env, m.cluster, tick, requestIdx)
	correlation := ledger.NewCorrelation()
	correlation.SpanID = ledger.CorrelationSpanIDFromSeed(seed)
	mc := m.drawModel(eng)
	r := &ledger.Request{
		Correlation: correlation,
		Workload:    m.workloadName,
		Env:         m.env,
		Cluster:     m.cluster,
		Route:       m.drawRoute(eng),
		Model:       mc.Model,
		Provider:    mc.Provider,
		Start:       now,
		// BrowserOrigin: ~60% of requests from a frontend entry originate in a RUM session
		// (only meaningful when RUM is configured — the beacon + browser span are only emitted
		// when b.RUM != nil; mirrors webservice minter's BrowserOrigin logic).
		BrowserOrigin: m.rumEnabled && eng.Float64() < 0.6,
	}

	if m.agentPipeline != nil {
		// Only metadata is minted. Actual attempts and safe content are executed
		// incrementally; ordinary spanning-tree DFS cannot represent revisions.
		r.Duration = time.Duration(drawDuration(m.traffic.RequestLatencyP95Ms, eng) * float64(time.Millisecond))
		if r.Duration < time.Second {
			r.Duration = time.Second
		}
		r.StatusCode = 200
		return r
	}
	if m.automation != nil {
		p := planAutomation(m.automation, r)
		r.Duration = p.duration
		r.StatusCode = 200
		if p.errorType != "" {
			r.Outcome = ledger.OutcomeServerError
			r.StatusCode = 503
			r.ErrorKind = p.errorType
			if p.errorType == "_OTHER" {
				r.StatusCode = 500
			}
		}
		return r
	}

	// Per-service incidents (§6.5): an error_spike / latency_storm on ANY node in the request path
	// shifts the request. errRate rises with the worst error_spike across the path; the request
	// duration scales with the worst latency_storm — so the targeted node's correlated SAMPLE
	// (trace + logs), not just its aggregate metrics, responds, while requests not touching it
	// are unaffected.
	base := m.baseErrorRate()
	errRate, entryRate := base, base
	for _, n := range m.pathNodes {
		if burst, inten := eng.Eval(now, "error_spike", n); burst {
			spike := 0.01 + (1-0.01)*0.5*inten // the legacy expression, kept verbatim for the default
			if base != defaultErrorRate {
				spike = base + (1-base)*0.5*inten
			}
			errRate = maxF(errRate, spike)
			if n == m.entry {
				entryRate = maxF(entryRate, spike)
			}
		}
	}
	var outcomeDraw float64
	r.Outcome, r.StatusCode, r.ErrorKind, outcomeDraw = m.drawOutcome(errRate, eng)
	// Opt-in downstream attribution: each reachable service declaring error_rate fails its own hop
	// independently, so a hop's failure share is exactly its declared rate and the request fails
	// with 1-(1-entry)(1-a)(1-b)... overall. The entry draw above is the same one the default path
	// makes; the extra draws (one per declared downstream service) exist only when a downstream
	// error_rate does. A request the entry did not fail itself, but a downstream hop did, surfaces
	// as an upstream 5xx; an entry-failed request keeps its drawn outcome.
	var failHops []string
	for _, d := range m.downstream {
		if eng.Float64() < d.rate {
			failHops = append(failHops, d.name)
		}
	}
	if len(failHops) > 0 && r.Outcome == ledger.OutcomeSuccess {
		r.Outcome, r.StatusCode, r.ErrorKind = ledger.OutcomeServerError, 500, "upstream_5xx"
	}

	// Base request duration = the entry's own processing (scaled only by the ENTRY's latency_storm).
	// p95 is blueprint-configurable (Traffic.RequestLatencyP95Ms; default 200ms HTTP-fast) — LLM/agentic
	// apps set it to seconds so the request + in-process gen_ai span windows reflect real LLM-wait time.
	dur := drawDuration(m.traffic.RequestLatencyP95Ms, eng) * eng.FailFactor(now, "latency_storm", m.entry, 4.0)
	r.Duration = time.Duration(dur * float64(time.Millisecond))

	// Per-hop durations apply each hop's OWN node latency factor (only the targeted node's hop is
	// slow — siblings unaffected). A slow downstream hop then SLOWS THE WHOLE REQUEST (it waits on
	// the hop), so grow r.Duration to cover the hop sum + overhead.
	var causes []string
	r.Calls, causes = m.drawGraphCallCauses(now, r.Duration, failHops, eng)
	if m.causal != nil {
		// Every legacy draw above ran exactly as on the legacy path; the overlay adds none.
		m.causal.overlay(m, r, now, eng, seed, causes, legacyOutcome{draw: outcomeDraw, base: base, entryRate: entryRate})
		m.causal.exchange(m, r) // a worker joins its message's trace; a publishing hop leaves a message
		return r
	}
	var hopSum time.Duration
	for i := range r.Calls {
		hopSum += r.Calls[i].Duration
	}
	if cover := hopSum + hopSum/4; cover > r.Duration {
		r.Duration = cover
	}
	return r
}

// defaultErrorRate is the entry request error fraction when no service declares error_rate.
const defaultErrorRate = 0.01

// baseErrorRate is the request's baseline error fraction: the entry's error_rate, or 0.01.
func (m *minter) baseErrorRate() float64 {
	if r := m.graph.entry.decl.ErrorRate; r != nil {
		return *r
	}
	return defaultErrorRate
}

// downstreamRate is one reachable non-entry service's declared error_rate.
type downstreamRate struct {
	name string
	rate float64
}

// downstreamRates lists the reachable non-entry services with a positive error_rate, in graph
// order. A zero rate contributes nothing, so it is left out and keeps the default path.
func downstreamRates(g *graph, pathNodes []string) []downstreamRate {
	var out []downstreamRate
	for _, name := range pathNodes {
		if name == g.entry.decl.Name {
			continue
		}
		if r := g.byName[name].decl.ErrorRate; r != nil && *r > 0 {
			out = append(out, downstreamRate{name: name, rate: *r})
		}
	}
	return out
}

// validateErrorRates rejects an error_rate outside [0,1], or on a service the entry cannot reach
// (it would never fire). A downstream rate is attributed to that service's own hop at mint time.
func validateErrorRates(services []ServiceNode, g *graph) error {
	reach := map[string]bool{}
	for _, n := range reachableNodes(g) {
		reach[n] = true
	}
	for _, s := range services {
		r := s.ErrorRate
		if r == nil {
			continue
		}
		if math.IsNaN(*r) || *r < 0 || *r > 1 {
			return fmt.Errorf("app service %q: error_rate %v out of [0,1]", s.Name, *r)
		}
		if !reach[s.Name] {
			return fmt.Errorf("app service %q: error_rate needs a service the entry can reach", s.Name)
		}
	}
	return nil
}

// drawGraphCallCauses walks the graph from the entry (spanning-tree DFS — each node visited at most
// once per request, bounding DAG diamonds + any cycle) and builds one ledger.Call per edge: a
// CLIENT span (call.SpanID) plus, when the callee is an instrumented service, its SERVER span
// (call.PeerSpanID). ParentHopIndex links each edge to the edge that reached its caller (-1 = the
// entry's own SERVER/root span).
//
// It also returns each hop's legacy failure cause ("error_spike", "error_rate" or ""), which the
// causal overlay attributes; the legacy path discards it.
func (m *minter) drawGraphCallCauses(now time.Time, reqDur time.Duration, failHops []string, eng *shape.Engine) ([]ledger.Call, []string) {
	var calls []ledger.Call
	visited := map[string]bool{m.entry: true}

	var walk func(caller *node, parentHop int)
	walk = func(caller *node, parentHop int) {
		for _, calleeName := range caller.decl.Calls {
			if visited[calleeName] {
				continue
			}
			visited[calleeName] = true
			callee := m.graph.byName[calleeName]
			idx := len(calls)
			c := ledger.Call{
				Kind:           callee.decl.Type,
				Target:         calleeName,
				SpanID:         ledger.NewSpanID(),
				ParentHopIndex: parentHop,
			}
			if callee.kind.serverSpan {
				c.PeerSpanID = ledger.NewSpanID()
			}
			calls = append(calls, c)
			walk(callee, idx)
		}
	}
	walk(m.graph.entry, -1)

	// Assign plausible sub-durations that occupy < ~70% of the request. Per-node incidents (§6.5):
	// a hop on a node with an active latency_storm is the slow one; a hop on a node with an active
	// error_spike fails — so the trace attributes failure to the INCIDENT node's hop, not a generic
	// "last hop blew up" (web_service's heuristic, which would mis-attribute under per-node
	// targeting). Sibling hops are unaffected. The request OUTCOME still reflects the raised error
	// rate (the entry span carries the error status).
	// failHops names the downstream services whose own error_rate failed this request; their hops
	// (and so their server spans and span metrics) fail without an extra draw.
	causes := make([]string, len(calls))
	if reqDur > 0 && len(calls) > 0 {
		budget := time.Duration(float64(reqDur) * 0.7)
		per := budget / time.Duration(len(calls))
		for i := range calls {
			frac := 0.3 + eng.Float64()*0.7
			d := time.Duration(float64(per) * frac)
			d = time.Duration(float64(d) * eng.FailFactor(now, "latency_storm", calls[i].Target, 4.0))
			if d < time.Millisecond {
				d = time.Millisecond
			}
			calls[i].Duration = d
			if active, inten := eng.Eval(now, "error_spike", calls[i].Target); active && eng.Float64() < 0.6*inten {
				calls[i].Failed = true
				causes[i] = "error_spike"
			}
		}
	}
	for _, name := range failHops {
		for i := range calls {
			if calls[i].Target == name {
				calls[i].Failed = true
				if causes[i] == "" {
					causes[i] = "error_rate"
				}
			}
		}
	}
	return calls, causes
}

func maxF(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

// drawOutcome draws the entry's baseline outcome: success (the declared success_status, default
// 200) when the first draw is at or above errRate, else one entry of the declared error_mix
// weighted by weight (default 50% 500 upstream_5xx, 30% 400 bad_request, 20% 429 rate_limited).
// It always takes one draw, plus one more on failure, whatever the mix. It also returns the first
// draw, which the causal overlay compares against the entry's own error rate.
func (m *minter) drawOutcome(errRate float64, eng *shape.Engine) (ledger.Outcome, int, string, float64) {
	u := eng.Float64()
	if u >= errRate {
		status := m.traffic.SuccessStatus
		if status == 0 {
			status = 200
		}
		return ledger.OutcomeSuccess, status, "", u
	}
	r := eng.Float64()
	if len(m.traffic.ErrorMix) == 0 {
		switch {
		case r < 0.5:
			return ledger.OutcomeServerError, 500, "upstream_5xx", u
		case r < 0.8:
			return ledger.OutcomeClientError, 400, "bad_request", u
		default:
			return ledger.OutcomeThrottled, 429, "rate_limited", u
		}
	}
	total := 0.0
	for _, e := range m.traffic.ErrorMix {
		total += e.Weight
	}
	pick := m.traffic.ErrorMix[len(m.traffic.ErrorMix)-1]
	cum := 0.0
	for _, e := range m.traffic.ErrorMix {
		cum += e.Weight
		if r*total < cum {
			pick = e
			break
		}
	}
	return outcomeOf(pick.Status), pick.Status, pick.errorCode(), u
}

// mixCode picks the error code of a failure answering status from the declared error_mix entries
// of that status, weighted by weight, with u a uniform draw in [0, 1) the caller supplies (a causal
// failure passes its request's hash draw, so no engine draw is taken). ok is false when no
// error_mix is declared or none of its entries has the status.
func (m *minter) mixCode(status int, u float64) (code string, ok bool) {
	total := 0.0
	var last ErrorOutcome
	for _, e := range m.traffic.ErrorMix {
		if e.Status == status {
			total += e.Weight
			last, ok = e, true
		}
	}
	if !ok {
		return "", false
	}
	cum := 0.0
	for _, e := range m.traffic.ErrorMix {
		if e.Status != status {
			continue
		}
		cum += e.Weight
		if u*total < cum {
			return e.errorCode(), true
		}
	}
	return last.errorCode(), true
}

// outcomeOf classifies an HTTP status into the request's outcome class.
func outcomeOf(status int) ledger.Outcome {
	switch {
	case status == 429:
		return ledger.OutcomeThrottled
	case status >= 500:
		return ledger.OutcomeServerError
	case status >= 400:
		return ledger.OutcomeClientError
	}
	return ledger.OutcomeSuccess
}

func drawDuration(p95ms float64, eng *shape.Engine) float64 {
	if p95ms <= 0 {
		p95ms = 200
	}
	const sigma = 0.5
	median := p95ms / math.Exp(1.645*sigma)
	v := median * math.Exp(sigma*eng.NormFloat64())
	if v < 1 {
		v = 1
	}
	return v
}

func clampF(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
