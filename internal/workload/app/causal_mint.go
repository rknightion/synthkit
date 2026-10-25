// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/rknightion/synthkit/internal/causal"
	"github.com/rknightion/synthkit/internal/ledger"
	"github.com/rknightion/synthkit/internal/shape"
	"github.com/rknightion/synthkit/internal/telemetryspec"
)

// The causal derivation (internal/causal, design §4): a workload whose services name declared
// resources (uses), declare client policies (call_policies) or consume a queue
// (traffic.from_resource, worker) derives each request's latency, status and failing hop from
// the shared resource model. Every legacy draw still runs first, in the same order; the overlay
// takes only hash draws of the request's seed unit, so the engine's draw sequence is unchanged.

const (
	// maxCausalCalls bounds a request's worst-case calls once retries expand every subtree.
	maxCausalCalls = 256
	// preWorkFraction is the share of a service's base duration spent before its first
	// downstream call, the same 10% the trace projection nests a child hop at. A request
	// rejected before its work (a capacity rejection, a token refusal, a failed connect or
	// handshake) also costs this share; the model holds a failed unit of work for the same share.
	preWorkFraction = causal.PreWorkFraction
	// causalDurationSigma is drawDuration's lognormal spread, reused for durations drawn from a
	// declared hold or latency (their mean).
	causalDurationSigma = 0.5
)

// causalOpted reports whether the config names any causal field.
func (c *Config) causalOpted() bool {
	if c.Traffic.FromResource != "" {
		return true
	}
	for _, s := range c.Services {
		if len(s.Uses) > 0 || len(s.CallPolicies) > 0 || s.Worker {
			return true
		}
	}
	return false
}

// UseResources implements causal.Consumer: the resolver injects the blueprint's declared
// resources (with every workload's retry loads) before Build.
func (c *Config) UseResources(s causal.Set) { c.resources = s }

// CausalLoads implements causal.LoadDeclarer: one load per capacity resource a reachable service
// uses, with this app's traffic share of the resource's declared demand and the client policy
// of the call into that service (none at the entry). A graph error is left for Build to report.
func (c *Config) CausalLoads(s causal.Set, env string) ([]causal.Load, error) {
	if !c.causalOpted() {
		return nil, nil
	}
	g, err := buildGraph(c.Services)
	if err != nil {
		return nil, nil
	}
	p, err := bindCausal(c, g, env, s)
	if err != nil {
		return nil, err
	}
	return p.loads, nil
}

// causalPlan is a workload's compiled causal derivation.
type causalPlan struct {
	model     *causal.Model
	res       map[string]causal.Resource
	entry     string
	fromQueue string // resolved traffic.from_resource queue; "" = declared traffic
	worker    bool
	nodes     map[string]*causalNode
	loads     []causal.Load

	served servedEvents

	// Trigger context across a queue (causal_trigger.go): the queues a hop publishes into, by
	// service, the queue channel a worker consumes, and the joined worker requests' triggers.

	publishes map[string][]*causal.Exchange
	consumes  *causal.Exchange
	triggers  triggerBook
}

// causalNode is one service's resolved resources and client policies.
type causalNode struct {
	uses      []string // resolved names in application order; a dependency's credentials follow it
	durRes    string   // resource whose hold or latency is the base duration; "" = the legacy draw
	extraDeps []string // used dependencies that are neither durRes nor its held_across
	leaf      bool
	server    bool
	policies  map[string]causal.CallPolicy
}

// maxPolicyMS bounds a call policy's timeout_ms and backoff_ms (ten minutes), so the durations
// derived from them (backoff x 2^k, summed over attempts) stay far inside time.Duration.
const maxPolicyMS = 600000

// buildCausalPlan validates the causal fields against the injected resources and compiles the
// model. A config naming no causal field gets nil: every request stays legacy.
func buildCausalPlan(cfg *Config, g *graph, env string) (*causalPlan, error) {
	if !cfg.causalOpted() {
		return nil, nil
	}
	if cfg.Automation != nil || cfg.AgentPipeline != nil {
		return nil, fmt.Errorf("app: uses, call_policies, worker and traffic.from_resource apply to graph requests, which an automation or agent_pipeline workload does not mint")
	}
	if cfg.resources.Empty() {
		return nil, fmt.Errorf("app: uses, call_policies, worker and traffic.from_resource need declared environments[].resources")
	}
	p, err := bindCausal(cfg, g, env, cfg.resources)
	if err != nil {
		return nil, err
	}
	if p.model, err = causal.Compile(cfg.resources); err != nil {
		return nil, fmt.Errorf("app: resources: %w", err)
	}
	p.bindExchanges(cfg.resources)
	return p, nil
}

// validateCausalValues rejects a custom or inline metric whose value reads the causal model
// (resource) or the node's causal hop outcomes (outcomes) on an app that derives no causal
// outcomes, where it would read 0 forever, and a resource value naming a resource not declared
// for the app's environment.
func validateCausalValues(g *graph, plan *causalPlan, env string, s causal.Set) error {
	for _, n := range g.nodes {
		for _, m := range n.metrics {
			kind := m.Value.Kind()
			if kind != telemetryspec.KindResource && kind != telemetryspec.KindOutcomes {
				continue
			}
			if plan == nil {
				return fmt.Errorf("app service %q: metric %q reads a %s value, but the app derives no causal outcomes (no uses, call_policies, worker or traffic.from_resource)", n.decl.Name, m.Name, kind)
			}
			if kind == telemetryspec.KindResource {
				if _, _, ok := s.Resolve(m.Value.Resource.Name, env); !ok {
					return fmt.Errorf("app service %q: metric %q: resource %q is not declared for environment %q", n.decl.Name, m.Name, m.Value.Resource.Name, env)
				}
			}
		}
	}
	return nil
}

// spanTree is the request's spanning tree (the minter's DFS order: each node reached once, from
// its first caller): parent by node, and nodes in visit order starting with the entry.
func spanTree(g *graph) (map[string]string, []string) {
	parent := map[string]string{}
	order := []string{g.entry.decl.Name}
	visited := map[string]bool{g.entry.decl.Name: true}
	var walk func(n *node)
	walk = func(n *node) {
		for _, c := range n.decl.Calls {
			if visited[c] {
				continue
			}
			visited[c] = true
			parent[c] = n.decl.Name
			order = append(order, c)
			walk(g.byName[c])
		}
	}
	walk(g.entry)
	return parent, order
}

// bindCausal resolves and validates every causal field against s for environment env.
func bindCausal(cfg *Config, g *graph, env string, s causal.Set) (*causalPlan, error) {
	p := &causalPlan{res: map[string]causal.Resource{}, entry: g.entry.decl.Name, nodes: map[string]*causalNode{}, worker: g.entry.decl.Worker}
	for _, d := range s.Envs {
		for _, r := range d.Resources {
			p.res[r.Name] = r
		}
	}
	resolve := func(name string) (causal.Resource, error) {
		r, _, ok := s.Resolve(name, env)
		if !ok {
			return causal.Resource{}, fmt.Errorf("resource %q is not declared for environment %q", name, env)
		}
		return r, nil
	}
	peak, off := cfg.Traffic.PeakRPS, cfg.Traffic.OffPeakRPS
	if peak <= 0 {
		peak = 50
	}
	off = math.Max(0, off)
	if q := cfg.Traffic.FromResource; q != "" {
		r, err := resolve(q)
		if err != nil {
			return nil, fmt.Errorf("app: traffic.from_resource: %w", err)
		}
		if r.Kind != causal.KindQueue {
			return nil, fmt.Errorf("app: traffic.from_resource %q is a %s, not a queue", q, r.Kind)
		}
		p.fromQueue = r.Name
		if peak, off, err = declaredRates(p.res, r); err != nil {
			return nil, fmt.Errorf("app: traffic.from_resource: %w", err)
		}
	}
	parent, order := spanTree(g)
	reach := map[string]bool{}
	for _, n := range order {
		reach[n] = true
	}
	for _, sn := range cfg.Services {
		if sn.Worker && sn.Name != p.entry {
			return nil, fmt.Errorf("app service %q: worker applies to the entry service only", sn.Name)
		}
		if sn.Worker && p.fromQueue == "" {
			return nil, fmt.Errorf("app service %q: worker needs traffic.from_resource (the queue it consumes)", sn.Name)
		}
		calls := map[string]bool{}
		for _, c := range sn.Calls {
			calls[c] = true
		}
		for _, callee := range sortedPolicyKeys(sn.CallPolicies) {
			if !calls[callee] {
				return nil, fmt.Errorf("app service %q: call_policies key %q is not in calls", sn.Name, callee)
			}
			if err := sn.CallPolicies[callee].Validate(); err != nil {
				return nil, fmt.Errorf("app service %q: call_policies %q: %w", sn.Name, callee, err)
			}
			pol := sn.CallPolicies[callee]
			if pol.TimeoutMS > maxPolicyMS {
				return nil, fmt.Errorf("app service %q: call_policies %q: timeout_ms %s must be at most %d (ten minutes)", sn.Name, callee, strconv.FormatFloat(pol.TimeoutMS, 'g', -1, 64), maxPolicyMS)
			}
			if pol.BackoffMS > maxPolicyMS {
				return nil, fmt.Errorf("app service %q: call_policies %q: backoff_ms %s must be at most %d (ten minutes)", sn.Name, callee, strconv.FormatFloat(pol.BackoffMS, 'g', -1, 64), maxPolicyMS)
			}
		}
		if len(sn.Uses) > 0 && !reach[sn.Name] {
			return nil, fmt.Errorf("app service %q: uses needs a service the entry can reach", sn.Name)
		}
	}
	peakSum, offSum := map[string]float64{}, map[string]float64{}
	usesCapacity := false
	// flow is the request tree the model derives each used resource's arrivals from (design §3.3,
	// SKT-0168.09.33): the spanning tree in the minter's DFS order, each node's resolved uses and
	// the policy of the call into it, so a fault upstream on the path lowers what reaches a
	// resource downstream exactly as the evaluator stops requests.
	flow := &causal.Flow{Nodes: make([]causal.FlowNode, len(order))}
	index := map[string]int{}
	for i, name := range order {
		index[name] = i
	}
	for i, name := range order {
		n := g.byName[name]
		fn := &flow.Nodes[i]
		fn.Parent = -1
		if i > 0 {
			fn.Parent = index[parent[name]]
			fn.Policy = g.byName[parent[name]].decl.CallPolicies[name]
		}
		cn := &causalNode{policies: n.decl.CallPolicies, leaf: n.kind.leaf, server: n.kind.serverSpan}
		seen, lit := map[string]bool{}, map[string]bool{}
		add := func(r causal.Resource) {
			if !seen[r.Name] {
				seen[r.Name] = true
				cn.uses = append(cn.uses, r.Name)
			}
		}
		for _, u := range n.decl.Uses {
			if lit[u] {
				return nil, fmt.Errorf("app service %q: uses lists %q twice", name, u)
			}
			lit[u] = true
			r, err := resolve(u)
			if err != nil {
				return nil, fmt.Errorf("app service %q: uses: %w", name, err)
			}
			if name == p.entry && (r.Kind == causal.KindDependency || r.Kind == causal.KindCredential) {
				return nil, fmt.Errorf("app service %q: uses %q: a %s applies to the call into a service, and nothing calls the entry; put it on the called service", name, u, r.Kind)
			}
			add(r)
			if r.Kind == causal.KindDependency {
				for _, cr := range s.CredentialsOf(r.Name) {
					add(cr)
				}
			}
			switch r.Kind {
			case causal.KindPool, causal.KindConcurrency, causal.KindDependency:
				if cn.durRes == "" {
					cn.durRes = r.Name
				}
			}
			if !r.Kind.Capacity() {
				continue
			}
			usesCapacity = true
			var pol causal.CallPolicy
			if name != p.entry {
				pol = g.byName[parent[name]].decl.CallPolicies[name]
			}
			share := 1.0
			if r.Demand != nil && r.Demand.From == "" {
				peakSum[r.Name] += peak
				offSum[r.Name] += off
				share = peak / r.Demand.PeakRPS
			}
			p.loads = append(p.loads, causal.Load{Resource: r.Name, Share: share, Policy: pol, Flow: flow, Node: i})
		}
		if cn.durRes == "" && name == p.entry && p.worker {
			cn.durRes = p.fromQueue // a consumer's processing time is the queue's hold
		}
		held := p.res[cn.durRes].HeldAcross
		for _, u := range cn.uses {
			if p.res[u].Kind == causal.KindDependency && u != cn.durRes && u != held {
				cn.extraDeps = append(cn.extraDeps, u)
			}
		}
		fn.Uses = cn.uses
		p.nodes[name] = cn
	}
	names := make([]string, 0, len(peakSum))
	for n := range peakSum {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		dm := p.res[n].Demand
		if dm.PeakRPS < peakSum[n]-1e-9 {
			return nil, fmt.Errorf("app: resource %q: demand.peak_rps %v is below this app's peak traffic through it (%v)", n, dm.PeakRPS, peakSum[n])
		}
		if dm.OffPeakRPS < offSum[n]-1e-9 {
			return nil, fmt.Errorf("app: resource %q: demand.off_peak_rps %v is below this app's off-peak traffic through it (%v)", n, dm.OffPeakRPS, offSum[n])
		}
	}
	if usesCapacity && (cfg.ShapeProfile != nil || g.entry.decl.ShapeProfile != nil) {
		return nil, fmt.Errorf("app: a shape_profile on the request path cannot drive traffic through a capacity resource, whose demand follows the environment curve")
	}
	if n := expansion(g, p.entry, map[string]bool{p.entry: true}); n > maxCausalCalls {
		return nil, fmt.Errorf("app: call_policies retries expand a request to %d calls in the worst case, more than %d", n, maxCausalCalls)
	}
	return p, nil
}

// declaredRates follows a queue's demand.from chain to the first declared demand curve.
func declaredRates(res map[string]causal.Resource, r causal.Resource) (peak, off float64, err error) {
	for range len(res) + 1 {
		dm := r.Demand
		if dm == nil {
			break
		}
		if dm.From == "" {
			return dm.PeakRPS, dm.OffPeakRPS, nil
		}
		r = res[dm.From]
	}
	return 0, 0, fmt.Errorf("queue %q has no declared demand to bound its served rate", r.Name)
}

// expansion is the worst-case number of calls under a node with every retry taken, over the
// spanning tree.
func expansion(g *graph, name string, visited map[string]bool) int {
	total := 0
	n := g.byName[name]
	for _, c := range n.decl.Calls {
		if visited[c] { // the minter's DFS reaches each node once, from its first caller
			continue
		}
		visited[c] = true
		total += (n.decl.CallPolicies[c].Retries + 1) * (1 + expansion(g, c, visited))
	}
	return total
}

func sortedPolicyKeys(m map[string]causal.CallPolicy) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// servedRPS is the from_resource queue's processed rate at now.
func (p *causalPlan) servedRPS(now time.Time) float64 {
	st, _ := p.model.State(p.fromQueue, now)
	return st.ServedRPS
}

// eventCarry holds the fractional remainder of a count of whole events, with a compensation term
// so a long run of tiny additions does not drift.
type eventCarry struct{ fraction, correction float64 }

// take adds amount to the carry and returns the whole events that have accrued. It is the same
// arithmetic as the lambda construct's carry, so a bound function counting a queue's window and
// an app minting from it agree on every whole event; TestFromResourceAppMintsTheBoundFunctionsInvocations
// pins the two together.
func (c *eventCarry) take(amount float64) float64 {
	if amount == 0 {
		return 0
	}
	y := amount - c.correction
	v := c.fraction + y
	c.correction = (v - c.fraction) - y
	n := math.Max(0, math.Floor(v))
	if n+1-v <= math.Max(1e-12, 8*(math.Nextafter(v, math.Inf(1))-v)) {
		n++
	}
	c.fraction = v - n
	return n
}

// servedEvents turns a from_resource queue's served messages into whole requests. The count is the
// model's window volume through a deterministic carry, never a random draw, so the requests a
// consumer app mints (its traces and logs) are exactly the executions a construct bound to the
// same flow counts. The observation window is (now - elapsed, now], elapsed capped at the tick
// length: a missed window is not replayed, and a repeated or backward timestamp mints nothing and
// consumes no carry.
type servedEvents struct {
	mu      sync.Mutex
	carry   eventCarry
	last    time.Time
	started bool
}

// mintServed returns the whole requests for the window ending at now. paused advances the observation
// clock without accruing, so the resumed tick does not claim the paused interval.
func (p *causalPlan) mintServed(now time.Time, tickSec float64, paused bool) int {
	s := &p.served
	s.mu.Lock()
	defer s.mu.Unlock()
	seconds := tickSec
	if s.started {
		if !now.After(s.last) {
			return 0
		}
		seconds = math.Min(seconds, now.Sub(s.last).Seconds())
	}
	s.last, s.started = now, true
	if paused || seconds <= 0 {
		return 0
	}
	win, ok := p.model.Window(p.fromQueue, now.Add(-time.Duration(seconds*float64(time.Second))), now)
	if !ok {
		return 0
	}
	return int(s.carry.take(win.Served))
}

// legacyOutcome is the entry's baseline outcome draw and the rates it was compared against.
type legacyOutcome struct {
	draw      float64 // the first drawOutcome draw
	base      float64 // the entry's baseline error rate
	entryRate float64 // base raised by an error_spike on the entry itself
}

// evaluator derives one request's causal facts.
type evaluator struct {
	p        *causalPlan
	m        *minter
	r        *ledger.Request
	now      time.Time
	eng      *shape.Engine
	draw     causal.Draw
	legacy   []ledger.Call
	causes   []string
	children map[int][]int // legacy hop index -> its legacy child hops; -1 is the entry
	out      []ledger.Call
	touched  bool // something failed, waited, slowed, timed out or retried
}

// result is one service's (or one attempt's) outcome as its caller sees it.
type result struct {
	ok        bool
	class     causal.FailureClass
	status    int
	fault     *ledger.Fault // origin fault of a failure
	slow      *ledger.Fault // the largest slowing cause in the subtree
	slowBy    float64       // ms it added
	durMS     float64       // server duration (serve) or client duration (attempt)
	serverMS  float64
	transport bool
	timedOut  bool
	ownRule   bool // the service's own rule rejected it; only its direct caller holds that back
}

func (res *result) noteSlow(f *ledger.Fault, by float64) {
	if f != nil && by > res.slowBy {
		res.slow, res.slowBy = f, by
	}
}

func faultOf(e causal.Effect) *ledger.Fault {
	return &ledger.Fault{Resource: e.Resource, Kind: string(e.Kind), Mode: e.Mode, ErrorType: e.ErrorType, Exception: e.Exception, Message: e.Message}
}

func msOf(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }

func durOf(ms float64) time.Duration {
	d := time.Duration(math.Round(ms * float64(time.Millisecond)))
	if d < time.Millisecond {
		d = time.Millisecond
	}
	return d
}

// overlay replaces the legacy request facts with the causal derivation (design §4.2). The
// legacy facts on entry hold the drawn outcome, the entry's own duration (before any hop cover)
// and the legacy hops with their causes.
func (p *causalPlan) overlay(m *minter, r *ledger.Request, now time.Time, eng *shape.Engine, seed string, causes []string, lo legacyOutcome) {
	r.Causal = true
	ev := &evaluator{p: p, m: m, r: r, now: now, eng: eng, draw: causal.UnitDraw(seed), legacy: r.Calls, causes: causes, children: map[int][]int{}}
	for i, c := range r.Calls {
		ev.children[c.ParentHopIndex] = append(ev.children[c.ParentHopIndex], i)
	}
	var consumer *ledger.Fault
	var consumerWait float64
	if p.worker {
		// The message's queueing time is recorded as the cause, not added to the consumer's own
		// span: the wait happened before this consumer received the message.
		if e := p.model.ApplyConsumer(p.fromQueue, now); e.Wait > 0 {
			ev.touched = true
			consumer, consumerWait = faultOf(e), msOf(e.Wait)
		}
	}
	entryMS := msOf(r.Duration)
	res := ev.serve(p.entry, p.entry+"#0", -1, -1, 0, entryMS, false)
	res.noteSlow(consumer, consumerWait)

	switch {
	case !res.ok:
		r.Outcome, r.StatusCode = outcomeOf(res.status), res.status
		r.Fault, r.ErrorKind = res.fault, res.fault.ErrorType
		if code, ok := m.mixCode(res.status, ev.draw("error_mix")); ok {
			r.ErrorKind = code
		}
	case lo.draw < lo.base && p.fromQueue != "":
		// A bound function's invocation (traffic drawn from a resource's served rate) failed by
		// its own background error_rate: a causal failure with the error_rate cause, like a
		// callee's error_rate hop, so it carries error.type, a status message and its log error
		// fields. It has no legacy twin (from_resource needs resources), so the opted-healthy
		// identity (D85) is not at stake. The message is the service and the drawn code.
		ev.touched = true
		r.Fault = &ledger.Fault{Mode: "error_rate", ErrorType: strconv.Itoa(r.StatusCode), Message: p.entry + ": " + r.ErrorKind}
	case lo.draw < lo.base:
		// The baseline draw failed the entry itself (its declared error_rate and error_mix): the
		// drawn outcome, status and error kind stand, with no Fault.
	case lo.draw < lo.entryRate:
		ev.touched = true
		r.Fault = &ledger.Fault{Mode: "error_spike", ErrorType: r.ErrorKind}
	default:
		// Success at the entry: a downstream error_spike fails its own hop and propagates
		// through the calls, instead of also raising the entry's rate.
		status := m.traffic.SuccessStatus
		if status == 0 {
			status = 200
		}
		r.Outcome, r.StatusCode, r.ErrorKind = ledger.OutcomeSuccess, status, ""
		if res.slow != nil {
			r.Fault = res.slow
		}
	}
	// Layout: every causal request takes the engine's timeline (offsets and durations from serve),
	// so a subtree the fault never reaches is laid out by one rule on healthy, faulted and
	// baseline-failed requests and its spans keep one duration distribution (SKT-0200, SKT-0206).
	// A request failed only by the entry's baseline draw keeps its drawn status and error code
	// above and carries no Fault; the projection gives it no causal decoration (causalDecorated,
	// root ruling D85 as re-ruled for layout only by D177).
	r.Calls = ev.out
	r.Duration = durOf(res.durMS)
	for _, c := range r.Calls { // rounding and the 1 ms floor must not push a hop past the request
		if end := c.Offset + c.Duration; end > r.Duration {
			r.Duration = end
		}
	}
}

// holdCovered reports whether a call from the legacy hop li runs a service whose duration is
// the dependency dep's latency. A pool, concurrency limit or queue held across dep keeps its
// slot while that call runs, so the call is where the hold's stretch by dep's extra latency is
// spent.
func (ev *evaluator) holdCovered(li int, dep string) bool {
	for _, ci := range ev.children[li] {
		if ev.p.nodes[ev.legacy[ci].Target].durRes == dep {
			return true
		}
	}
	return false
}

// baseDur is a service's base duration on a path. d is the duration. A service whose hold spans
// a call to the held dependency (covered, see holdCovered) also has the part of the duration
// that is its own: pre is the unstretched hold the work before its first call is a share of, and
// own the time outside the dependency's baseline latency, so the call supplies the rest. dep is
// the held dependency.
type baseDur struct {
	d, pre, own float64
	covered     bool
	dep         string
}

// baseMS is a service's base duration on a path: drawn around its bound resource's hold or
// latency (with the node's latency_storm factor) or the legacy duration (jittered on a resend
// path), plus the extra latency of slowed dependencies it uses beyond its duration source.
//
// A hold held across a dependency that the service also calls is not stretched twice: the
// connection is held while the query runs, so a slower query is the stretch of the hold. The
// stretch is left to the call (the hold is the sum of the service's own time and the call), and
// the service keeps only the rest of its hold, with the same draw spread.
func (ev *evaluator) baseMS(name, path string, legacyMS float64, fresh bool, li int) baseDur {
	cn := ev.p.nodes[name]
	var bd baseDur
	if cn.durRes == "" {
		bd.d = legacyMS
		if fresh {
			bd.d *= 0.8 + 0.4*ev.draw(path+"/jitter")
		}
	} else {
		st, _ := ev.p.model.State(cn.durRes, ev.now)
		r := ev.p.res[cn.durRes]
		mean := st.HoldMS
		if r.Kind == causal.KindDependency {
			mean = r.LatencyMS * math.Max(1, st.LatencyFactor)
		}
		u := math.Min(math.Max(ev.draw(path+"/"+cn.durRes+"/duration"), 1e-12), 1-1e-12)
		z := math.Sqrt2 * math.Erfinv(2*u-1)
		median := mean / math.Exp(causalDurationSigma*causalDurationSigma/2)
		spread := math.Exp(causalDurationSigma*z) * ev.eng.FailFactor(ev.now, "latency_storm", name, 4.0)
		bd.d = median * spread
		if r.Kind != causal.KindDependency && r.HeldAcross != "" && ev.holdCovered(li, r.HeldAcross) {
			// hold_ms includes the dependency's baseline latency (resource validation), so the
			// service's own time is the rest, at least the work before its first call.
			f := spread / math.Exp(causalDurationSigma*causalDurationSigma/2)
			bd.covered, bd.dep = true, r.HeldAcross
			bd.pre = r.HoldMS * f
			bd.own = math.Max(r.HoldMS-ev.p.res[r.HeldAcross].LatencyMS, preWorkFraction*r.HoldMS) * f
		}
	}
	for _, dep := range cn.extraDeps {
		if st, ok := ev.p.model.State(dep, ev.now); ok && st.LatencyFactor > 1 {
			x := ev.p.res[dep].LatencyMS * (st.LatencyFactor - 1)
			bd.d += x
			bd.own += x
		}
	}
	bd.d = math.Max(bd.d, 0)
	return bd
}

// serve evaluates one service on a path from its server start: its resources in order, its
// calls in sequence (each with its caller policy), and its legacy hop failure. hop is the out
// index of the call into it (-1 for the entry), li the matching legacy hop.
func (ev *evaluator) serve(name, path string, hop, li int, start, legacyMS float64, fresh bool) result {
	cn := ev.p.nodes[name]
	bd := ev.baseMS(name, path, legacyMS, fresh, li)
	d0, pre0 := bd.d, bd.d
	if bd.covered {
		pre0 = bd.pre
	}
	var res result
	wait := 0.0
	for _, u := range cn.uses {
		e := ev.p.model.Apply(u, ev.now, ev.draw.Scope(path+"/"+u))
		if e.Fail {
			ev.touched = true
			res.class, res.status, res.fault = e.Class, e.Status, faultOf(e)
			res.ownRule = e.Kind == causal.KindRule
			switch {
			case e.Transport:
				res.transport = true
				res.durMS = preWorkFraction * d0
			case e.Kind == causal.KindRule:
				res.durMS = msOf(e.Wait) + wait + d0 // the record is examined, then rejected
			default:
				res.durMS = msOf(e.Wait) + wait + preWorkFraction*d0
			}
			return res
		}
		if e.Slowed() {
			ev.touched = true
			by := msOf(e.Wait)
			if e.Kind == causal.KindDependency {
				by = ev.p.res[u].LatencyMS * (e.LatencyFactor - 1)
			}
			res.noteSlow(faultOf(e), by)
		}
		wait += msOf(e.Wait)
	}
	cursor := start + wait + preWorkFraction*pre0
	childStart := cursor
	heldSpan := 0.0 // elapsed time of the calls that run the held dependency
	for _, ci := range ev.children[li] {
		target := ev.legacy[ci].Target
		er := ev.edge(hop, ci, path, cursor, fresh, cn.policies[target])
		cursor += er.durMS
		if bd.covered && ev.p.nodes[target].durRes == bd.dep {
			heldSpan += er.durMS
		}
		res.noteSlow(er.slow, er.slowBy)
		if !er.ok {
			status, class := causal.CallerStatus(cn.policies[target], er.class, er.status, li < 0)
			res.class, res.status, res.fault = class, status, er.fault
			res.durMS = cursor - start
			return res
		}
	}
	res.ok = true
	if bd.covered {
		// The hold is the service's own time plus the held call, so the slowed dependency counts
		// once, inside that call; the hold still spans every other call.
		res.durMS = wait + math.Max(bd.own+heldSpan, preWorkFraction*pre0+(cursor-childStart))
	} else {
		res.durMS = wait + math.Max(d0, preWorkFraction*pre0+(cursor-childStart))
	}
	if li >= 0 {
		if cause := ev.hopCause(li, path, name, fresh); cause != "" {
			ev.touched = true
			res.ok = false
			res.class, res.status = causal.ClassHTTP, 500
			res.fault = &ledger.Fault{Mode: cause, ErrorType: "500"}
		}
	}
	return res
}

// hopCause is the legacy error_spike or error_rate failure of a hop: the legacy draw on the
// legacy path, a hash redraw on a resend path.
func (ev *evaluator) hopCause(li int, path, name string, fresh bool) string {
	if !fresh {
		return ev.causes[li]
	}
	if active, inten := ev.eng.Eval(ev.now, "error_spike", name); active && ev.draw(path+"/error_spike/fail") < 0.6*inten {
		return "error_spike"
	}
	for _, d := range ev.m.downstream {
		if d.name == name && ev.draw(path+"/error_rate/fail") < d.rate {
			return "error_rate"
		}
	}
	return ""
}

// edge runs the attempts of one call under the caller's policy; durMS is the edge's elapsed time
// from its first attempt's start to its last attempt's end (backoffs included).
func (ev *evaluator) edge(parentOut, li int, parentPath string, start float64, fresh bool, pol causal.CallPolicy) result {
	t := start
	target := ev.legacy[li].Target
	for k := 0; ; k++ {
		if k > 0 {
			ev.touched = true
			t += pol.BackoffMS * math.Pow(2, float64(k-1))
		}
		path := parentPath + ">" + target + "#" + strconv.Itoa(k)
		a := ev.attempt(parentOut, li, k, path, t, fresh || k > 0, pol)
		t += a.durMS
		// A rule rejection is never resent by the service that calls the rule's node; a caller
		// further up sees only the mapped status and resends it like any other.
		if a.ok || a.ownRule || k >= pol.Retries || !pol.Retryable(a.class, a.status) {
			a.durMS = t - start
			return a
		}
	}
}

// attempt appends one client call (and evaluates the callee's subtree after it, keeping DFS
// pre-order) and returns the call's result as the caller sees it.
func (ev *evaluator) attempt(parentOut, li, k int, path string, start float64, fresh bool, pol causal.CallPolicy) result {
	lc := ev.legacy[li]
	c := lc
	if fresh {
		c.SpanID = ledger.NewSpanID()
		if lc.PeerSpanID != "" {
			c.PeerSpanID = ledger.NewSpanID()
		}
	}
	c.ParentHopIndex = parentOut
	c.Attempt = k
	idx := len(ev.out)
	ev.out = append(ev.out, c)
	res := ev.serve(lc.Target, path, idx, li, start, msOf(lc.Duration), fresh)
	res.serverMS = res.durMS
	if !res.transport && pol.TimeoutMS > 0 && res.durMS > pol.TimeoutMS {
		ev.touched = true
		f := &ledger.Fault{ErrorType: "timeout"}
		if res.slow != nil {
			sf := *res.slow
			sf.ErrorType, sf.Exception = "timeout", ""
			f = &sf
		}
		res = result{class: causal.ClassTimeout, fault: f, slow: res.slow, slowBy: res.slowBy, durMS: pol.TimeoutMS, serverMS: res.serverMS, timedOut: true}
	}
	call := &ev.out[idx]
	// Round the hop's start and end, not its duration: a hop that ends with its caller (a hold
	// across the dependency it calls) then ends on the same nanosecond, never one past it. The
	// 1 ms floor still applies (durOf).
	call.Offset = time.Duration(math.Round(start * float64(time.Millisecond)))
	call.Duration = time.Duration(math.Round((start+res.durMS)*float64(time.Millisecond))) - call.Offset
	if call.Duration < time.Millisecond {
		call.Duration = durOf(res.durMS)
	}
	call.Failed = !res.ok
	call.TimedOut = res.timedOut
	if res.timedOut {
		call.ServerDuration = durOf(res.serverMS)
	}
	if res.transport {
		call.PeerSpanID = "" // the callee never received the request
	}
	switch {
	case res.ok && ev.p.nodes[lc.Target].server:
		call.StatusCode = 200
	case !res.ok:
		call.StatusCode = res.status
	}
	if !res.ok {
		call.Fault = res.fault
	} else {
		call.Fault = res.slow
	}
	return res
}

var (
	_ causal.Consumer     = (*Config)(nil)
	_ causal.LoadDeclarer = (*Config)(nil)
	_ causal.Advancer     = (*Workload)(nil)
)
