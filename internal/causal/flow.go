// SPDX-License-Identifier: AGPL-3.0-only

package causal

import (
	"fmt"
	"math"
	"slices"
	"time"
)

// Flow is one workload's declared request tree, so the model can derive how much of a resource's
// demand the workload's requests actually deliver to it. Nodes are the services in the order the
// workload walks them (depth-first pre-order of its spanning tree): Nodes[0] is the entry, every
// caller precedes its callees, and a caller's callees appear in call order.
//
// A request meets a node's Uses in order and stops at the first rejection; it then calls the
// node's callees one after another under each call's Policy and stops at the first call that
// fails once its retries are spent; a node that gets that far can still fail on its own
// (HopError). A callee's failure reaches its caller mapped by CallerStatus. Only the service that
// calls the rule's node never resends a rule rejection; a caller further up sees the mapped status
// and resends it like any other. A call with a timeout (Policy.TimeoutMS) also fails as a timeout
// when the attempt runs past it (see timedOut). A healthy tree (no resource rejecting, no hop
// error, no timeout declared) delivers exactly one arrival per request to every resource it uses.
//
// Every attempt of a call re-runs the callee's whole subtree, so a resource receives one arrival
// per evaluation of its node that gets past the node's earlier uses: a caller resending a
// failure anywhere below it (the resource's own rejection, a later use, a deeper callee's
// mapped failure) resends the arrival too (SKT-0195).
type Flow struct {
	Nodes []FlowNode
	// Legacy supplies the app minter's existing shared request-duration budget for nodes
	// without Duration. Nil means this flow declares no fallback-duration distribution.
	Legacy *LegacyTiming
}

// LegacyTiming describes the existing app timing sampler, not a new blueprint option. The
// request draw is lognormal with sigma 0.5 and median P95MS/exp(1.645*0.5), floored at 1 ms.
// After EntryStretch, 70% of it is divided over every non-entry node; each baseline hop gets
// an independent uniform [0.3,1) share, its node stretch and a 1 ms floor. Those baseline
// values persist across the whole request. Resends multiply the SAME value by independent
// uniform [0.8,1.2) jitter. Sharing only a marginal duration would lose retry/sibling correlation.
type LegacyTiming struct {
	P95MS        float64
	EntryStretch FaultScale
}

// FlowNode is one service of a Flow.
type FlowNode struct {
	// Parent is the caller's index in Nodes, -1 for the entry (Nodes[0] only).
	Parent int
	// Policy is the caller's client policy on the call into this node; zero for the entry.
	Policy CallPolicy
	// Uses are the declared resource names the node meets on arrival, in order.
	Uses []string
	// Duration names the use (pool, concurrency or dependency) whose effective hold or latency is
	// the mean of the node's own work, drawn lognormal with DurationSigma; "" when the node's
	// work uses Flow.Legacy's existing app duration sampler, if supplied.
	Duration string
	// Stretch scales the node's own work while its fault is active; zero for none.
	Stretch FaultScale
	// HopError is the node's own failure once its uses admit it and its calls succeed; zero for
	// none. The entry has none (nothing calls it).
	HopError HopError
}

// FaultScale is a fault that stretches a duration: active on Scope at intensity i, the factor is
// 1 + (AtFull - 1) x i. An empty Mode is none (factor 1).
type FaultScale struct {
	Mode, Scope string
	AtFull      float64
}

// HopError is a node's own failure after its work, independent of any resource: Rate of its
// evaluations fail with Status, and an active Mode fault on Scope at intensity i first fails
// PerIntensity x i of them (the rest still fail at Rate). The failure is an HTTP status, so a
// caller's 5xx policy resends it.
type HopError struct {
	Rate         float64
	Mode, Scope  string
	PerIntensity float64
	Status       int
}

// DurationSigma is the lognormal spread of a duration drawn around a declared hold or latency
// (its mean): the app workload's evaluator draws a node's own work with it, and the flow model
// reads the same spread for the share of attempts that outlast a timeout.
const DurationSigma = 0.5

func (s FaultScale) validate() error {
	if s.Mode == "" {
		if s.Scope != "" || s.AtFull != 0 {
			return fmt.Errorf("stretch has a scope or factor but no mode")
		}
		return nil
	}
	if math.IsNaN(s.AtFull) || math.IsInf(s.AtFull, 0) || s.AtFull < 1 {
		return fmt.Errorf("stretch %s factor %v must be a finite number >= 1", s.Mode, s.AtFull)
	}
	return nil
}

func (h HopError) validate() error {
	if h == (HopError{}) {
		return nil
	}
	for _, v := range []float64{h.Rate, h.PerIntensity} {
		if math.IsNaN(v) || v < 0 || v > 1 {
			return fmt.Errorf("hop error rate %v and per-intensity share %v must be in [0,1]", h.Rate, h.PerIntensity)
		}
	}
	if h.Mode == "" && (h.PerIntensity != 0 || h.Scope != "") {
		return fmt.Errorf("hop error has a per-intensity share or scope but no mode")
	}
	if h.Status < 500 || h.Status > 599 {
		return fmt.Errorf("hop error status %d must be a 5xx", h.Status)
	}
	return nil
}

func validateFlow(f *Flow, byName map[string]Resource) error {
	if f.Legacy != nil {
		if math.IsNaN(f.Legacy.P95MS) || math.IsInf(f.Legacy.P95MS, 0) {
			return fmt.Errorf("legacy request p95 must be finite")
		}
		if err := f.Legacy.EntryStretch.validate(); err != nil {
			return err
		}
	}
	if len(f.Nodes) == 0 {
		return fmt.Errorf("flow has no nodes")
	}
	for i, n := range f.Nodes {
		switch {
		case i == 0 && n.Parent != -1:
			return fmt.Errorf("flow node 0 is the entry and has no parent")
		case i > 0 && (n.Parent < 0 || n.Parent >= i):
			return fmt.Errorf("flow node %d: parent %d must precede it", i, n.Parent)
		}
		if err := n.Policy.Validate(); err != nil {
			return fmt.Errorf("flow node %d: %w", i, err)
		}
		for j, u := range n.Uses {
			if _, ok := byName[u]; !ok {
				return fmt.Errorf("flow node %d uses unknown resource %q", i, u)
			}
			if slices.Contains(n.Uses[:j], u) {
				return fmt.Errorf("flow node %d uses %q twice", i, u)
			}
		}
		if n.Duration != "" {
			switch r, ok := byName[n.Duration]; {
			case !slices.Contains(n.Uses, n.Duration):
				return fmt.Errorf("flow node %d: duration %q is not one of its uses", i, n.Duration)
			case !ok || (r.Kind != KindPool && r.Kind != KindConcurrency && r.Kind != KindDependency):
				return fmt.Errorf("flow node %d: duration %q must be a pool, concurrency or dependency", i, n.Duration)
			}
		}
		if err := n.Stretch.validate(); err != nil {
			return fmt.Errorf("flow node %d: %w", i, err)
		}
		if err := n.HopError.validate(); err != nil {
			return fmt.Errorf("flow node %d: %w", i, err)
		}
		if i == 0 && n.HopError != (HopError{}) {
			return fmt.Errorf("flow node 0 is the entry and has no hop error")
		}
	}
	return nil
}

// outcomeKey is how one failure looks to the caller that receives it: class and status, and
// whether a rule rejected it (never resent).
type outcomeKey struct {
	class  FailureClass
	status int
	rule   bool
}

type failShare struct {
	key outcomeKey
	p   float64
}

// dist is one service evaluation's outcome: the share that succeeds and the shares failing by key.
type dist struct {
	ok    float64
	fails []failShare
}

func (d *dist) add(k outcomeKey, p float64) {
	if p <= 0 {
		return
	}
	for i := range d.fails {
		if d.fails[i].key == k {
			d.fails[i].p += p
			return
		}
	}
	d.fails = append(d.fails, failShare{k, p})
}

type flowNode struct {
	policy  CallPolicy
	uses    []*compiled
	kids    []int
	dur     *compiled   // the Duration use; nil = none
	extra   []*compiled // used dependencies other than dur and dur's held_across (their slowing adds to the work)
	stretch FaultScale
	hopErr  HopError
	hopKey  outcomeKey

	// Per step (prepare): the mean of the node's own work, the slowed extra dependencies' added
	// latency, the stretch factor and the hop error share.
	mean, shift, storm, hop float64
	// covered is the direct child supplying dur's held-across latency, -1 otherwise.
	covered      int
	elapsedCache struct {
		inputs      []float64
		bins        int
		materialize bool
		out         []elapsed
	}
}

// cflow is a compiled Flow, shared by every load that names it.
type cflow struct {
	nodes           []flowNode
	legacy          *LegacyTiming
	needsLegacy     bool
	legacyRootScale float64
}

// gate places one load in its compiled flow: the path from the entry to the node and the index
// of the resource in the node's uses.
type gate struct {
	flow        *cflow
	path        []int // node indices from the entry to the load's node
	use         int
	legacyCache []legacyCacheEntry
	legacyNext  int
}

type legacyCacheEntry struct {
	inputs       []float64
	points, bins int
	value        float64
}

func compileFlow(f *Flow, byName map[string]*compiled) *cflow {
	cf := &cflow{nodes: make([]flowNode, len(f.Nodes))}
	if f.Legacy != nil {
		legacy := *f.Legacy
		cf.legacy = &legacy
		for i, n := range f.Nodes {
			if i == 0 || n.Duration != "" {
				continue
			}
			for a := i; a > 0; a = f.Nodes[a].Parent {
				if f.Nodes[a].Policy.TimeoutMS > 0 {
					cf.needsLegacy = true
					break
				}
			}
		}
	}
	for i, n := range f.Nodes {
		fn := &cf.nodes[i]
		fn.policy = n.Policy
		for _, u := range n.Uses {
			fn.uses = append(fn.uses, byName[u])
		}
		fn.dur = byName[n.Duration] // nil for ""
		fn.covered = -1
		if fn.dur != nil && fn.dur.ref != nil {
			for k, child := range f.Nodes {
				if child.Parent == i && child.Duration == fn.dur.ref.r.Name {
					fn.covered = k
					break
				}
			}
		}
		for _, u := range fn.uses {
			if u.r.Kind == KindDependency && u != fn.dur && (fn.dur == nil || u != fn.dur.ref) {
				fn.extra = append(fn.extra, u)
			}
		}
		fn.stretch, fn.hopErr = n.Stretch, n.HopError
		fn.hopKey = outcomeKey{class: ClassHTTP, status: n.HopError.Status}
		fn.storm = 1
		if i > 0 {
			cf.nodes[n.Parent].kids = append(cf.nodes[n.Parent].kids, i)
		}
	}
	return cf
}

// prepare reads the step's inputs every evaluation of the flow shares: each node's mean own work
// (its Duration's effective hold or latency, from the dependencies already solved this step), the
// added latency of its slowed extra dependencies, its stretch factor and its hop error share.
// Faults are read only for a node that declares them, so a flow declaring none reads no Clock.
func (cf *cflow) prepare(clk Clock, t time.Time) {
	cf.legacyRootScale = 1
	if cf.needsLegacy {
		if s := cf.legacy.EntryStretch; s.Mode != "" {
			if active, intensity := clk.Eval(t, s.Mode, s.Scope); active {
				cf.legacyRootScale = 1 + float64((s.AtFull-1)*clamp01(intensity))
			}
		}
	}
	for i := range cf.nodes {
		fn := &cf.nodes[i]
		fn.mean, fn.shift, fn.storm, fn.hop = 0, 0, 1, 0
		if d := fn.dur; d != nil {
			if d.r.Kind == KindDependency {
				fn.mean = d.r.LatencyMS * math.Max(1, d.latency())
			} else {
				lf, _ := d.refState()
				fn.mean = d.hold(lf)
			}
		}
		for _, x := range fn.extra {
			if lf := x.latency(); lf > 1 {
				fn.shift += float64(x.r.LatencyMS * (lf - 1))
			}
		}
		if s := fn.stretch; s.Mode != "" && (fn.dur != nil || cf.needsLegacy) {
			if a, in := clk.Eval(t, s.Mode, s.Scope); a {
				fn.storm = 1 + float64((s.AtFull-1)*clamp01(in))
			}
		}
		if h := fn.hopErr; h.Rate > 0 || h.Mode != "" {
			spike := 0.0
			if h.Mode != "" {
				if a, in := clk.Eval(t, h.Mode, h.Scope); a {
					spike = h.PerIntensity * clamp01(in)
				}
			}
			fn.hop = clamp01(spike + float64((1-spike)*h.Rate))
		}
	}
}

// over is the share of the node's evaluations whose elapsed time, fixed + frac x its own work,
// runs past timeout: the own work is the Duration's draw (lognormal with DurationSigma around
// mean x storm) plus the slowed extra dependencies' latency, as the app evaluator draws it. A
// node without a Duration has no own work here, so only fixed can outlast the timeout.
func (fn *flowNode) over(timeout, fixed, frac float64) float64 {
	y := (timeout-fixed)/frac - fn.shift // the lognormal part must exceed y
	if fn.mean <= 0 {
		if y < 0 {
			return 1
		}
		return 0
	}
	if y <= 0 {
		return 1
	}
	median := fn.mean * fn.storm / math.Exp(DurationSigma*DurationSigma/2)
	return 0.5 * math.Erfc(math.Log(y/median)/(DurationSigma*math.Sqrt2))
}

func (cf *cflow) gateFor(f *Flow, node int, resource string) *gate {
	g := &gate{flow: cf, use: slices.Index(f.Nodes[node].Uses, resource)}
	for n := node; n >= 0; n = f.Nodes[n].Parent {
		g.path = append(g.path, n)
	}
	slices.Reverse(g.path)
	return g
}

// rejectFn is a resource's reject share for one evaluation: the resource being solved is
// overridden with its iterate, every other resource reads its latest record (this step's: the
// model solves the step to a fixed point, see Model.step; zero before the first step).
type rejectFn func(*compiled) float64

func latestReject(self *compiled, selfReject float64) rejectFn {
	return func(c *compiled) float64 {
		if c == self {
			return selfReject
		}
		if !c.last.valid {
			return 0
		}
		return c.last.st.RejectShare
	}
}

// serve is the outcome of one evaluation of node n as the caller of n sees it: its uses in order,
// then its calls in order, then its own hop error; with a timeout on the call into n, the share
// of each outcome whose elapsed time runs past it fails as a timeout instead (timedOut).
func (cf *cflow) serve(n int, rej rejectFn) dist {
	fn := &cf.nodes[n]
	hasWait := false
	for _, u := range fn.uses {
		hasWait = hasWait || u.waitMS() > 0
	}
	if fn.policy.TimeoutMS > 0 && (len(fn.kids) > 0 || hasWait) {
		out := dist{}
		for _, e := range cf.elapsedServe(n, rej, elapsedBins, false) {
			if e.key == (outcomeKey{}) {
				out.ok += e.p
			} else {
				out.add(e.key, e.p)
			}
		}
		return out
	}
	out := dist{ok: 1}
	timeout := fn.policy.TimeoutMS // the call into n; zero for the entry
	late := 0.0                    // nonzero admitted waits with observable timing use elapsedServe above
	fail := func(k outcomeKey, p, tau float64) {
		out.add(k, float64(p*(1-tau)))
		late += float64(p * tau)
	}
	for _, u := range fn.uses {
		if p := rej(u); p > 0 {
			tau := 0.0
			if timeout > 0 && !u.transport {
				tau = fn.timedOut(u, timeout, 0)
			}
			fail(u.key, float64(out.ok*p), tau)
			out.ok *= 1 - p
		}
	}
	tau := 0.0 // a failed call ends the node after its pre-work and the calls so far
	if timeout > 0 {
		tau = fn.over(timeout, 0, PreWorkFraction)
	}
	for _, k := range fn.kids {
		e := cf.edge(k, rej)
		for _, f := range e.fails {
			status, class := CallerStatus(cf.nodes[k].policy, f.key.class, f.key.status, n == 0)
			// Mapped, a rule rejection is an ordinary status: only the direct caller (edge) knows
			// it was a rule, so a caller further up resends it if its policy retries the status.
			fail(outcomeKey{class: class, status: status}, float64(out.ok*f.p), tau)
		}
		out.ok *= e.ok
	}
	tau = 0 // completed work (and a hop error after it) takes the whole own work
	if timeout > 0 {
		tau = fn.over(timeout, 0, 1)
	}
	if fn.hop > 0 {
		fail(fn.hopKey, float64(out.ok*fn.hop), tau)
		out.ok *= 1 - fn.hop
	}
	if tau > 0 {
		late += float64(out.ok * tau)
		out.ok *= 1 - tau
	}
	out.add(outcomeKey{class: ClassTimeout}, late)
	return out
}

// elapsedBins bounds the conditional duration quadrature. Outcome masses and a node's own
// lognormal timeout tail are integrated analytically; child elapsed distributions are represented
// by probability-cell centroids, separately for each outcome (finer cells in the tails). Sequential sums, retry/backoff
// time and timeout clipping are retained, not replaced by a mean or nested inside own work.
// This is numerical quadrature, not an exact convolution. See the refinement test for its error.
const elapsedBins = 256

// elapsed is a conditional duration atom. The zero key is success. held is used only while
// accumulating a node's children: the elapsed part supplied by its held-across child.
type elapsed struct {
	key         outcomeKey
	p, ms, held float64
}

// compactElapsed preserves each outcome's mass and mean, bounding its atoms independently.
// At most bins+1 atoms remain per outcome (the last can be a roundoff remainder).
// Stable sorting and encounter-ordered keys keep the quadrature deterministic.
func compactElapsed(in []elapsed, bins int) []elapsed {
	var keys []outcomeKey
	for _, e := range in {
		if e.p > 0 && !slices.Contains(keys, e.key) {
			keys = append(keys, e.key)
		}
	}
	var out []elapsed
	for _, key := range keys {
		var group []elapsed
		total := 0.0
		for _, e := range in {
			if e.key == key && e.p > 0 {
				group = append(group, e)
				total += e.p
			}
		}
		if len(group) <= bins {
			out = append(out, group...)
			continue
		}
		slices.SortStableFunc(group, func(a, b elapsed) int {
			if a.ms < b.ms {
				return -1
			}
			if a.ms > b.ms {
				return 1
			}
			return 0
		})
		cell := 0
		width := total * probabilityCell(0, bins)
		bucket := elapsed{key: key}
		for _, e := range group {
			for e.p > 0 {
				take := math.Min(e.p, width-bucket.p)
				bucket.ms += float64(take * e.ms)
				bucket.held += float64(take * e.held)
				bucket.p += take
				e.p -= take
				if bucket.p >= width {
					bucket.ms /= bucket.p
					bucket.held /= bucket.p
					out = append(out, bucket)
					bucket = elapsed{key: key}
					cell++
					width = total * probabilityCell(cell, bins)
				}
			}
		}
		if bucket.p > 0 {
			bucket.ms /= bucket.p
			bucket.held /= bucket.p
			out = append(out, bucket)
		}
	}
	return out
}

// durationTerm is a*X+b, X lognormal with mean 1. A service's completed time is the
// maximum of its own hold (including any covered call) and pre-work + sequential calls.
type durationTerm struct{ a, b float64 }

func unitCDF(x float64) float64 {
	if x <= 0 {
		return 0
	}
	return 0.5 * math.Erfc(-(math.Log(x)+DurationSigma*DurationSigma/2)/(DurationSigma*math.Sqrt2))
}

// emitElapsed integrates timeout mass exactly conditional on the child atoms. Below the
// deadline it samples conditional quantiles only when a caller needs this elapsed distribution.
func emitElapsed(out []elapsed, key outcomeKey, p float64, terms []durationTerm, timeout float64, transport, materialize bool, bins int) []elapsed {
	return emitElapsedLaw(out, key, p, terms, timeout, transport, materialize, bins, workLognormal)
}

type workLaw uint8

const (
	workLognormal workLaw = iota
	workFixed
	workJitter
)

func (law workLaw) cdf(x float64) float64 {
	switch law {
	case workFixed:
		if x < 1 {
			return 0
		}
		return 1
	case workJitter:
		return clamp01((x - 0.8) / 0.4)
	default:
		return unitCDF(x)
	}
}
func (law workLaw) quantile(u float64) float64 {
	switch law {
	case workFixed:
		return 1
	case workJitter:
		return 0.8 + float64(0.4*u)
	default:
		return math.Exp(float64(DurationSigma*math.Sqrt2*math.Erfinv(float64(2*u)-1)) - DurationSigma*DurationSigma/2)
	}
}

func emitElapsedLaw(out []elapsed, key outcomeKey, p float64, terms []durationTerm, timeout float64, transport, materialize bool, bins int, law workLaw) []elapsed {
	if p <= 0 {
		return out
	}
	cdf := 1.0
	if timeout > 0 && !transport {
		for _, term := range terms {
			q := 1.0
			if term.a > 0 {
				q = law.cdf((timeout - term.b) / term.a)
			} else if term.b > timeout {
				q = 0
			}
			cdf = math.Min(cdf, q)
		}
		if cdf < 1 {
			out = append(out, elapsed{key: outcomeKey{class: ClassTimeout}, p: p * (1 - cdf), ms: timeout})
		}
	}
	if cdf == 0 {
		return out
	}
	if !materialize {
		return append(out, elapsed{key: key, p: p * cdf})
	}
	count := bins
	variable := false
	for _, term := range terms {
		variable = variable || term.a > 0
	}
	if !variable || law == workFixed {
		count = 1
	}
	for j := range count {
		lo, hi := probabilityBoundary(j, count), probabilityBoundary(j+1, count)
		u := cdf * (lo + hi) / 2
		x := law.quantile(u)
		ms := 0.0
		for _, term := range terms {
			ms = math.Max(ms, float64(term.a*x)+term.b)
		}
		out = append(out, elapsed{key: key, p: p * cdf * (hi - lo), ms: ms})
	}
	return out
}

// Cosine-spaced probability cells resolve the small tail masses that an ordinary equal-mass
// grid loses when two slow children's sum is almost certain to exceed the parent's timeout.
func probabilityBoundary(i, bins int) float64 {
	if i >= bins {
		return 1
	}
	return (1 - math.Cos(math.Pi*float64(i)/float64(bins))) / 2
}

func probabilityCell(i, bins int) float64 {
	if i >= bins {
		return 1
	} // a possible roundoff remainder, not an extra nominal cell
	return probabilityBoundary(i+1, bins) - probabilityBoundary(i, bins)
}

// Cache by all numerical subtree inputs, not by clock step or rejectFn identity. A capacity
// solve often asks the same question many times, and a steady step has identical inputs.
func (cf *cflow) elapsedInputs(n int, rej rejectFn, in []float64) []float64 {
	fn := &cf.nodes[n]
	in = append(in, fn.mean, fn.shift, fn.storm, fn.hop)
	for _, u := range fn.uses {
		in = append(in, rej(u), u.waitMS())
	}
	for _, k := range fn.kids {
		in = cf.elapsedInputs(k, rej, in)
	}
	return in
}

func (cf *cflow) elapsedServe(n int, rej rejectFn, bins int, materialize bool) []elapsed {
	fn := &cf.nodes[n]
	cache := &fn.elapsedCache
	inputs := cf.elapsedInputs(n, rej, nil)
	if cache.bins == bins && cache.materialize == materialize && slices.Equal(cache.inputs, inputs) {
		return cache.out
	}
	out := cf.computeElapsed(n, rej, bins, materialize)
	cache.inputs, cache.bins, cache.materialize, cache.out = inputs, bins, materialize, out
	return out
}

func (cf *cflow) computeElapsed(n int, rej rejectFn, bins int, materialize bool) []elapsed {
	return cf.computeTimed(n, rej, bins, materialize, nil, false)
}

func (cf *cflow) computeTimed(n int, rej rejectFn, bins int, materialize bool, legacy *legacyEval, fresh bool) []elapsed {
	fn := &cf.nodes[n]
	law := workLognormal
	work := fn.mean * fn.storm
	if legacy != nil && n > 0 && fn.dur == nil {
		work, law = legacy.base[n], workFixed
		if fresh {
			law = workJitter
		}
	}
	var waits waitTiming
	emit := func(out []elapsed, key outcomeKey, p float64, terms []durationTerm, timeout float64, transport, materialize bool, bins int) []elapsed {
		return waits.emit(out, key, p, terms, timeout, transport, materialize, bins, law)
	}
	var out []elapsed
	remaining := 1.0
	for _, u := range fn.uses {
		p := rej(u)
		if p > 0 {
			frac, fixed := PreWorkFraction, 0.0
			if u.r.Kind == KindRule {
				frac = 1
			}
			if u.r.Kind == KindPool {
				fixed += float64(msDuration(u.r.AcquireTimeoutMS)) / float64(time.Millisecond)
			}
			out = emit(out, u.key, remaining*p,
				[]durationTerm{{work * frac, float64(fn.shift*frac) + fixed}},
				fn.policy.TimeoutMS, u.transport, materialize, bins)
			remaining *= 1 - p
		}
		waits.add(u.admittedWait(), bins)
	}
	pre, preShift, own := work, fn.shift, work
	if fn.covered >= 0 {
		pre = fn.dur.r.HoldMS * fn.storm
		preShift = 0
		own = math.Max(fn.dur.r.HoldMS-fn.dur.ref.r.LatencyMS, PreWorkFraction*fn.dur.r.HoldMS) * fn.storm
	}
	paths := []elapsed{{p: remaining}}
	for _, k := range fn.kids {
		edge := cf.computeEdge(k, rej, bins, legacy, fresh)
		var next []elapsed
		for _, path := range paths {
			for _, e := range edge {
				p := path.p * e.p
				ms := path.ms + e.ms
				if e.key != (outcomeKey{}) {
					status, class := CallerStatus(cf.nodes[k].policy, e.key.class, e.key.status, n == 0)
					out = emit(out, outcomeKey{class: class, status: status}, p,
						[]durationTerm{{PreWorkFraction * pre, float64(PreWorkFraction*preShift) + ms}},
						fn.policy.TimeoutMS, false, materialize, bins)
				} else {
					held := path.held
					if fn.covered >= 0 && cf.nodes[k].dur == fn.dur.ref {
						held += e.ms
					}
					next = append(next, elapsed{p: p, ms: ms, held: held})
				}
			}
		}
		paths = compactElapsed(next, bins)
	}
	for _, path := range paths {
		terms := []durationTerm{
			{own, fn.shift + path.held},
			{PreWorkFraction * pre, float64(PreWorkFraction*preShift) + path.ms},
		}
		out = emit(out, outcomeKey{}, path.p*(1-fn.hop), terms, fn.policy.TimeoutMS, false, materialize, bins)
		out = emit(out, fn.hopKey, path.p*fn.hop, terms, fn.policy.TimeoutMS, false, materialize, bins)
	}
	return compactElapsed(out, bins)
}

// elapsedEdge keeps the elapsed duration of all failed attempts and exponential backoffs,
// as well as the final outcome. A timeout clips an attempt's client time, not its subtree's
// demand: the evaluator still evaluates that subtree before reporting the timeout.
func (cf *cflow) elapsedEdge(n int, rej rejectFn, bins int) []elapsed {
	return cf.computeEdge(n, rej, bins, nil, false)
}

func (cf *cflow) computeEdge(n int, rej rejectFn, bins int, legacy *legacyEval, fresh bool) []elapsed {
	pol := cf.nodes[n].policy
	active := []elapsed{{p: 1}}
	var out []elapsed
	for j := 0; j <= pol.Retries; j++ {
		var attempt []elapsed
		if legacy == nil {
			attempt = cf.elapsedServe(n, rej, bins, true)
		} else {
			attempt = legacy.serve(n, fresh || j > 0, true)
		}
		backoff := 0.0
		if j > 0 {
			backoff = pol.BackoffMS * math.Pow(2, float64(j-1))
		}
		var next []elapsed
		for _, prefix := range active {
			for _, a := range attempt {
				e := elapsed{key: a.key, p: prefix.p * a.p, ms: prefix.ms + backoff + a.ms}
				if j < pol.Retries && a.key != (outcomeKey{}) && !a.key.rule && pol.Retryable(a.key.class, a.key.status) {
					e.key = outcomeKey{}
					next = append(next, e)
				} else {
					out = append(out, e)
				}
			}
		}
		active = compactElapsed(next, bins)
	}
	return compactElapsed(out, bins)
}

// timedOut is the share of the node's evaluations rejected at u that run past timeout: a rule
// examines the record (the whole own work), any other rejection ends after the pre-work, a pool's
// after its acquire timeout. wait is a conditional realized earlier wait (zero on the
// analytic fast path; random waits use computeTimed). A transport failure
// (a refused connection or handshake) never reaches the timeout and is not asked.
func (fn *flowNode) timedOut(u *compiled, timeout, wait float64) float64 {
	if u.r.Kind == KindRule {
		return fn.over(timeout, wait, 1)
	}
	if u.r.Kind == KindPool {
		wait += u.r.AcquireTimeoutMS
	}
	return fn.over(timeout, wait, PreWorkFraction)
}

// edge is the outcome of the call into node k under its policy, as the caller sees it before
// status mapping. Attempts are independent: a retryable failure (share r) is resent up to Retries
// times, so the call succeeds with ok x sum_{j<=Retries} r^j.
func (cf *cflow) edge(k int, rej rejectFn) dist {
	pol := cf.nodes[k].policy
	a := cf.serve(k, rej)
	attempts, last := retrySeries(pol, a)
	out := dist{ok: a.ok * attempts}
	for _, f := range a.fails {
		if !f.key.rule && pol.Retryable(f.key.class, f.key.status) {
			out.add(f.key, float64(f.p*last))
		} else {
			out.add(f.key, float64(f.p*attempts))
		}
	}
	return out
}

// retrySeries is the expected number of attempts of a call whose single attempt has outcome a:
// sum_{j<=Retries} r^j, r being the share failing with a status the policy resends (a rule
// rejection from the callee's own uses never is). last is r^Retries, the share reaching the
// final attempt.
func retrySeries(pol CallPolicy, a dist) (attempts, last float64) {
	r := 0.0
	for _, f := range a.fails {
		if !f.key.rule && pol.Retryable(f.key.class, f.key.status) {
			r += f.p
		}
	}
	rk := 1.0
	last = 1.0
	for range pol.Retries + 1 {
		last = rk
		attempts += rk
		rk *= r
	}
	return attempts, last
}

// arrivals is the expected number of arrivals at the gated resource per entry request. Along the
// path, each node is evaluated once per attempt of the call into it: an evaluation gets past
// every use of the node and every earlier call of it (retries included) with their success
// shares, and the call into the next node on the path is then attempted sum_{j<=Retries} r^j
// times, r being the share of that node's evaluations failing with a status the caller resends:
// anywhere in its subtree, on its own (HopError) or by running past the call's timeout, each of
// which the app resends with the whole subtree. At the node itself the arrival needs the earlier
// uses to admit it. Exactly 1 when nothing on the tree rejects, fails or times out.
//
// The resource's own rejections are among the failures the call into its node resends, so a
// load with a Flow needs no separate Policy-only amplification: with nothing else failing,
// arrivals is that closed loop (design §3.3) gated by the upstream path.
func (g *gate) arrivals(rej rejectFn) float64 {
	cf := g.flow
	if cf.needsLegacy {
		return g.legacyArrivals(rej, legacyPoints, legacyBins)
	}
	v := 1.0
	for i, n := range g.path {
		fn := &cf.nodes[n]
		if i == len(g.path)-1 {
			for _, u := range fn.uses[:g.use] {
				v *= 1 - rej(u)
			}
			return v
		}
		for _, u := range fn.uses {
			v *= 1 - rej(u)
		}
		next := g.path[i+1]
		for _, k := range fn.kids {
			if k == next {
				break
			}
			v *= cf.edge(k, rej).ok
		}
		attempts, _ := retrySeries(cf.nodes[next].policy, cf.serve(next, rej))
		v *= attempts
	}
	return v
}

// Legacy integration conditions on the per-request random variables that the app retains:
// one shared request duration and one baseline fraction per hop. Only resend jitter and
// resource-backed own-work draws are integrated afresh inside an attempt. In particular,
// integrating a marginal timeout share and THEN taking its geometric retry series is wrong.
const (
	legacyPoints = 2048
	legacyBins   = 32
)

type legacyEval struct {
	flow  *cflow
	rej   rejectFn
	base  []float64
	bins  int
	cache [][4][]elapsed // first/fresh x outcome-only/materialized
}

func (e *legacyEval) serve(n int, fresh, materialize bool) []elapsed {
	i := 0
	if fresh {
		i = 2
	}
	if materialize {
		i++
	}
	if e.cache[n][i] == nil {
		if !materialize && e.cache[n][i+1] != nil {
			return e.cache[n][i+1]
		}
		e.cache[n][i] = e.flow.computeTimed(n, e.rej, e.bins, materialize, e, fresh)
	}
	return e.cache[n][i]
}

func (e *legacyEval) summary(n int, fresh bool) dist {
	d := dist{}
	for _, a := range e.serve(n, fresh, false) {
		if a.key == (outcomeKey{}) {
			d.ok += a.p
		} else {
			d.add(a.key, a.p)
		}
	}
	return d
}

func retryProbability(pol CallPolicy, d dist) float64 {
	r := 0.0
	for _, f := range d.fails {
		if !f.key.rule && pol.Retryable(f.key.class, f.key.status) {
			r += f.p
		}
	}
	return r
}

// resendWeight is the expected number of FRESH attempts after the first attempt. Conditional
// on the persisted baseline, first attempts have no jitter and resends have uniform jitter.
func (e *legacyEval) resendWeight(n int, fresh bool) float64 {
	pol := e.flow.nodes[n].policy
	if pol.Retries == 0 {
		return 0
	}
	r0 := retryProbability(pol, e.summary(n, fresh))
	r := retryProbability(pol, e.summary(n, true))
	weight, power := 0.0, r0
	for range pol.Retries {
		weight += power
		power *= r
	}
	return weight
}

func (e *legacyEval) edgeOK(n int, fresh bool) float64 {
	d := e.summary(n, fresh)
	if e.flow.nodes[n].policy.Retries == 0 {
		return d.ok
	}
	return d.ok + float64(e.resendWeight(n, fresh)*e.summary(n, true).ok)
}

func (g *gate) conditionalArrivals(e *legacyEval, pos int, fresh bool, memo [][2]float64) float64 {
	f := 0
	if fresh {
		f = 1
	}
	if !math.IsNaN(memo[pos][f]) {
		return memo[pos][f]
	}
	fn := &g.flow.nodes[g.path[pos]]
	v := 1.0
	if pos == len(g.path)-1 {
		for _, u := range fn.uses[:g.use] {
			v *= 1 - e.rej(u)
		}
	} else {
		for _, u := range fn.uses {
			v *= 1 - e.rej(u)
		}
		next := g.path[pos+1]
		for _, k := range fn.kids {
			if k == next {
				break
			}
			v *= e.edgeOK(k, fresh)
		}
		first := g.conditionalArrivals(e, pos+1, fresh, memo)
		weight := e.resendWeight(next, fresh)
		if weight > 0 {
			first += float64(weight * g.conditionalArrivals(e, pos+1, true, memo))
		}
		v *= first
	}
	memo[pos][f] = v
	return v
}

func radicalInverse(index, base int) float64 {
	inv, out := 1/float64(base), 0.0
	for index > 0 {
		out += float64(float64(index%base) * inv)
		index /= base
		inv /= float64(base)
	}
	return out
}

func timingPrimes(count int) []int {
	out := make([]int, 0, count)
	for candidate := 2; len(out) < count; candidate++ {
		prime := true
		for _, p := range out {
			if p*p > candidate {
				break
			}
			if candidate%p == 0 {
				prime = false
				break
			}
		}
		if prime {
			out = append(out, candidate)
		}
	}
	return out
}

func (cf *cflow) legacyBase(point int, primes []int, reflection, points int) []float64 {
	p95 := cf.legacy.P95MS
	if p95 <= 0 {
		p95 = 200
	}
	coordinate := func(base int) float64 {
		u := radicalInverse(point, base)
		if base == primes[0] {
			u = radicalInverse(point-1, base) + 0.5/float64(points)
		}
		flip := reflection&2 != 0
		if base == primes[0] {
			flip = reflection&1 != 0
		}
		if flip {
			return 1 - u
		}
		return u
	}
	u := coordinate(primes[0])
	z := math.Sqrt2 * math.Erfinv(float64(2*u)-1)
	request := math.Max(1, p95/math.Exp(1.645*DurationSigma)*math.Exp(DurationSigma*z))
	// Mirror the minter's nanosecond truncation, integer division and floors, rather than
	// inventing a mean hop duration. No engine RNG is called by numerical integration.
	reqDur := time.Duration(request * cf.legacyRootScale * float64(time.Millisecond))
	budget := time.Duration(float64(reqDur) * 0.7)
	per := budget / time.Duration(len(cf.nodes)-1)
	base := make([]float64, len(cf.nodes))
	for n := 1; n < len(cf.nodes); n++ {
		fraction := 0.3 + float64(0.7*coordinate(primes[n]))
		d := time.Duration(float64(per) * fraction)
		d = time.Duration(float64(d) * cf.nodes[n].storm)
		if d < time.Millisecond {
			d = time.Millisecond
		}
		base[n] = float64(d) / float64(time.Millisecond)
	}
	return base
}

// A deterministic joint Halton quadrature covers the persisted legacy variables. Conditional
// rejection probabilities remain analytic, so resource fixed-point iteration is not replaced
// by a discontinuous sampled admission decision. Cache keys include every numerical flow input.
// points is a power of two: the shared request coordinate uses stratum midpoints, and the
// request/hop coordinates are independently reflected (four deterministic folds).
func (g *gate) legacyArrivals(rej rejectFn, points, bins int) float64 {
	cf := g.flow
	inputs := cf.elapsedInputs(0, rej, []float64{cf.legacyRootScale})
	for _, c := range g.legacyCache {
		if c.points == points && c.bins == bins && slices.Equal(c.inputs, inputs) {
			return c.value
		}
	}
	primes := timingPrimes(len(cf.nodes))
	value := 0.0
	for point := 1; point <= points; point++ {
		for reflection := range 4 {
			e := &legacyEval{flow: cf, rej: rej, base: cf.legacyBase(point, primes, reflection, points), bins: bins, cache: make([][4][]elapsed, len(cf.nodes))}
			memo := make([][2]float64, len(g.path))
			for i := range memo {
				memo[i] = [2]float64{math.NaN(), math.NaN()}
			}
			value += g.conditionalArrivals(e, 0, false, memo)
		}
	}
	value /= float64(4 * points)
	entry := legacyCacheEntry{inputs: inputs, points: points, bins: bins, value: value}
	// Retain solver iterates too: otherwise every steady grid step would repeat the same
	// expensive quadrature from its initial capacity guess. Bound memory independently of time.
	if len(g.legacyCache) < 2*maxFixedPoint {
		g.legacyCache = append(g.legacyCache, entry)
	} else {
		g.legacyCache[g.legacyNext] = entry
		g.legacyNext = (g.legacyNext + 1) % len(g.legacyCache)
	}
	return value
}

// touched reports the resources whose reject share gates the path to the resource (the solved
// resource excluded by the caller): the uses on the path and the subtrees of earlier calls. The
// solve order follows these reads. arrivals also reads the subtree below the path for resends
// (reads); those cannot be ordered too (a resource downstream reads this one to gate, so ordering
// both ways would be circular), so the model solves each step to a fixed point instead: a
// capacity resource read before it is solved makes the step solve again until every such read
// equals the value solved (Model.step). Dependencies, credentials and rules are always solved
// first.
func (g *gate) touched(visit func(*compiled)) {
	cf := g.flow
	var subtree func(n int)
	subtree = func(n int) {
		for _, u := range cf.nodes[n].uses {
			visit(u)
		}
		for _, k := range cf.nodes[n].kids {
			subtree(k)
		}
	}
	for i, n := range g.path {
		fn := &cf.nodes[n]
		if i == len(g.path)-1 {
			for _, u := range fn.uses[:g.use] {
				visit(u)
			}
			return
		}
		for _, u := range fn.uses {
			visit(u)
		}
		next := g.path[i+1]
		for _, k := range fn.kids {
			if k == next {
				break
			}
			subtree(k)
		}
	}
}

// reads reports every resource arrivals can read: the uses on the path, the subtrees of the
// path nodes' calls up to and including the call into the next path node, and the node's own
// uses before the resource.
func (g *gate) reads(visit func(*compiled)) {
	cf := g.flow
	var subtree func(n int)
	subtree = func(n int) {
		for _, u := range cf.nodes[n].uses {
			visit(u)
		}
		for _, k := range cf.nodes[n].kids {
			subtree(k)
		}
	}
	for i, n := range g.path {
		fn := &cf.nodes[n]
		if i == len(g.path)-1 {
			for _, u := range fn.uses[:g.use] {
				visit(u)
			}
			return
		}
		for _, u := range fn.uses {
			visit(u)
		}
		next := g.path[i+1]
		for _, k := range fn.kids {
			subtree(k)
			if k == next {
				break
			}
		}
	}
}

// heldCall is the policy of the call the load's node makes into a node using c's held-across
// dependency (the first such call), nil when the gate is nil or the node makes no such call.
func (g *gate) heldCall(c *compiled) *CallPolicy {
	if g == nil || c.ref == nil {
		return nil
	}
	cf := g.flow
	for _, k := range cf.nodes[g.path[len(g.path)-1]].kids {
		if slices.Contains(cf.nodes[k].uses, c.ref) {
			return &cf.nodes[k].policy
		}
	}
	return nil
}

// failKey is the outcome key of this resource's rejection, exactly as Apply reports it.
func (c *compiled) failKey() outcomeKey {
	e := Effect{}
	c.failArrival(&e)
	return outcomeKey{class: e.Class, status: e.Status, rule: c.r.Kind == KindRule}
}
