// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"fmt"
	"math"
	"sync/atomic"
	"time"

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

// Mint fabricates this tick's correlated requests (StochasticRound of the expected volume).
func (m *minter) Mint(now time.Time, tickSec float64, eng *shape.Engine) []*ledger.Request {
	// Empty ticks also consume an identity unit, matching the live tick sequence.
	tick := atomic.AddUint64(&m.tick, 1) - 1
	n := ledger.StochasticRound(m.expectedVolume(now, tickSec, eng), eng.Float64())
	if n <= 0 {
		return nil
	}
	out := make([]*ledger.Request, 0, n)
	for requestIdx := range n {
		out = append(out, m.mintRequest(now, eng, tick, requestIdx))
	}
	return out
}

// mintOne fabricates one fully-correlated request and walks the graph into a hop tree.
func (m *minter) mintOne(now time.Time, eng *shape.Engine) *ledger.Request {
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
	correlation.SpanID = ledger.NewCorrelationFromSeed(seed).SpanID
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
	errRate := base
	for _, n := range m.pathNodes {
		if burst, inten := eng.Eval(now, "error_spike", n); burst {
			spike := 0.01 + (1-0.01)*0.5*inten // the legacy expression, kept verbatim for the default
			if base != defaultErrorRate {
				spike = base + (1-base)*0.5*inten
			}
			errRate = maxF(errRate, spike)
		}
	}
	r.Outcome, r.StatusCode, r.ErrorKind = drawOutcome(errRate, eng)
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
	r.Calls = m.drawGraphCalls(now, r.Duration, r.Outcome, failHops, eng)
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

// drawGraphCalls walks the graph from the entry (spanning-tree DFS — each node visited at most
// once per request, bounding DAG diamonds + any cycle) and builds one ledger.Call per edge: a
// CLIENT span (call.SpanID) plus, when the callee is an instrumented service, its SERVER span
// (call.PeerSpanID). ParentHopIndex links each edge to the edge that reached its caller (-1 = the
// entry's own SERVER/root span).
func (m *minter) drawGraphCalls(now time.Time, reqDur time.Duration, outcome ledger.Outcome, failHops []string, eng *shape.Engine) []ledger.Call {
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
	// rate (the entry span carries the error status); _outcome is accepted for that linkage.
	// failHops names the downstream services whose own error_rate failed this request; their hops
	// (and so their server spans and span metrics) fail without an extra draw.
	_ = outcome
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
			}
		}
	}
	for _, name := range failHops {
		for i := range calls {
			if calls[i].Target == name {
				calls[i].Failed = true
			}
		}
	}
	return calls
}

func maxF(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func drawOutcome(errRate float64, eng *shape.Engine) (ledger.Outcome, int, string) {
	if eng.Float64() >= errRate {
		return ledger.OutcomeSuccess, 200, ""
	}
	switch r := eng.Float64(); {
	case r < 0.5:
		return ledger.OutcomeServerError, 500, "upstream_5xx"
	case r < 0.8:
		return ledger.OutcomeClientError, 400, "bad_request"
	default:
		return ledger.OutcomeThrottled, 429, "rate_limited"
	}
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
