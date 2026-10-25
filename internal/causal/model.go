// SPDX-License-Identifier: AGPL-3.0-only

package causal

import (
	"math"
	"slices"
	"sync"
	"time"
)

const (
	// ringSteps is the per-resource history kept for State and Window: 15 minutes of steps,
	// longer than any metric interval.
	ringSteps = int64(15 * time.Minute / Step)
	// Horizon is the warm-start span, the longest catch-up gap, and the cap on the
	// oldest-message age and on an unbounded wait.
	Horizon      = 24 * time.Hour
	horizonSteps = int64(Horizon / Step)
	stepSec      = float64(Step / time.Second)
	// liveSteps is the final stretch of a warm start that sees live faults: the retained history,
	// the span a State or Window can report. A live fault at restart has an unknown start, and
	// integrating it over the whole Horizon would invent up to a day of backlog (a 0.5
	// consumer_slowdown on a queue fed 8 msg/s and serving 10 gave 259k messages and a 9 h
	// oldest age on the first tick); bounded to the ring it can build at most 15 minutes of it.
	liveSteps = ringSteps
	// maxFixedPoint bounds the per-step retry-demand iteration (it converges monotonically).
	maxFixedPoint = 64
)

// State is one resource's state at one grid point.
//
// Field meaning by kind: Demand is the effective offered rps (capacity kinds: declared or
// upstream demand plus expected retry attempts). Capacity is effective connections (pool),
// effective concurrency (concurrency), admitted rps (quota) or service rate in messages per
// second (queue). Utilization is offered load over capacity (rho; reported as 1 when capacity is
// zero under load). RejectShare is the share of arrivals rejected (capacity kinds), of calls
// failing (dependency unavailable, credential) or of arrivals failing (rule). FailedShare is the
// share of admitted work that fails after admission: for pool, concurrency and queue the
// held-across dependency's unavailability combined with its credentials' failures; for a
// dependency, credential or rule it equals RejectShare. WaitMS is the mean acquisition wait
// (pool) or a new message's queueing time (queue). HoldMS is the effective hold, execution or
// processing time of work that completes, hold_ms + latency_ms(held_across) x (LatencyFactor -
// 1); for pool and concurrency, Utilization, WaitMS and InUse use the mean occupancy, in which
// the FailedShare holds only PreWorkFraction x hold_ms (see occupancy). LatencyFactor is
// the dependency's latency multiplier, or for pool, concurrency and queue the held-across
// dependency's (1 when none). InUse is busy connections, executions or consumers (quota:
// admitted rps). Waiting is requests waiting (pool) or messages queued (queue). ServedRPS is the
// rate leaving the resource (admitted for pool, concurrency and quota; processed for a queue).
type State struct {
	At                                 time.Time
	Demand, Capacity, Utilization      float64
	RejectShare, FailedShare           float64
	WaitMS, HoldMS, LatencyFactor      float64
	InUse, Waiting, Backlog, ServedRPS float64
	OldestAgeSec                       float64
	Mode                               string // dominant active fault, "" when none
	Intensity                          float64
}

type rec struct {
	k     int64
	valid bool
	st    State
	// per-step volumes (counts over the step)
	arrivals, admitted, rejected, served, failed float64
	// failedHold is the mean elapsed time of the step's failed work (pool, concurrency, queue;
	// see Occupancy).
	failedHold float64
}

type point struct {
	k int64
	a float64 // cumulative admitted arrivals at grid point k
}

type compiled struct {
	r       Resource
	decl    Declaration
	alias   string
	knee    float64
	slow    float64
	ref     *compiled     // held_across (pool, concurrency, queue) or of (credential)
	creds   []*compiled   // credentials of the held-across dependency
	from    *compiled     // demand.from upstream
	loads   []Load        // retrying caller edges into this resource
	gates   []*gate       // per load: its place in the caller's request tree; nil = Policy only
	held    []*CallPolicy // per load: the holder's policy on its call into the held-across dependency; nil = unknown
	key     outcomeKey    // how a rejection here looks to the caller (flow evaluation)
	ring    []rec
	last    rec // most recent step's record (for references within one step)
	backlog float64
	cumA    float64
	cumD    float64
	arr     []point // cumulative arrival breakpoints, oldest first (queue only)
}

// Model is one consumer's deterministic simulation of a blueprint's declared resources.
// It is goroutine-safe.
type Model struct {
	mu      sync.Mutex
	order   []*compiled // dependencies, credentials and rules, then capacity kinds in demand.from order
	byName  map[string]*compiled
	started bool
	last    int64
}

// Compile validates the set and builds a model with empty state.
func Compile(s Set) (*Model, error) {
	if err := Validate(s); err != nil {
		return nil, err
	}
	m := &Model{byName: map[string]*compiled{}}
	var decl []*compiled
	for _, d := range s.Envs {
		for _, r := range d.Resources {
			c := &compiled{r: r, decl: d, alias: EnvAlias(r.Name, d.Env), knee: r.Knee, slow: r.SlowFactor, ring: make([]rec, ringSteps)}
			if c.knee == 0 {
				c.knee = defaultKnee
			}
			if c.slow == 0 {
				c.slow = defaultSlowFactor
			}
			m.byName[r.Name] = c
			decl = append(decl, c)
		}
	}
	for _, c := range decl {
		ref := c.r.HeldAcross
		if ref == "" {
			ref = c.r.Of
		}
		if ref != "" {
			c.ref = m.byName[ref]
		}
		if c.r.Demand != nil && c.r.Demand.From != "" {
			c.from = m.byName[c.r.Demand.From]
		}
	}
	for _, c := range decl {
		if c.r.Kind == KindCredential && c.ref != nil {
			for _, h := range decl {
				if h.r.Kind.holds() && h.ref == c.ref {
					h.creds = append(h.creds, c)
				}
			}
		}
	}
	for _, c := range decl {
		c.key = c.failKey()
	}
	flows := map[*Flow]*cflow{}
	for _, l := range s.Loads {
		c := m.byName[l.Resource]
		var g *gate
		if l.Flow != nil {
			cf, ok := flows[l.Flow]
			if !ok {
				cf = compileFlow(l.Flow, m.byName)
				flows[l.Flow] = cf
			}
			g = cf.gateFor(l.Flow, l.Node, l.Resource)
		}
		c.loads = append(c.loads, l)
		c.gates = append(c.gates, g)
		c.held = append(c.held, g.heldCall(c))
	}
	// Dependencies first, then credentials and rules (they read nothing), then capacity kinds in
	// demand.from order (Validate rejects a cycle), declaration order breaking ties. A capacity
	// resource whose flow-derived demand reads another capacity resource's reject share follows
	// it too, so a chain along a request path (quota, pool, rule, function, queue) is solved in
	// one step; where request trees make that circular, the first waiting resource in declaration
	// order goes next and reads the others' previous step.
	for _, c := range decl {
		if c.r.Kind == KindDependency {
			m.order = append(m.order, c)
		}
	}
	for _, c := range decl {
		if c.r.Kind == KindCredential || c.r.Kind == KindRule {
			m.order = append(m.order, c)
		}
	}
	reads := map[*compiled][]*compiled{}
	for _, c := range decl {
		if !c.r.Kind.Capacity() || c.from != nil {
			continue
		}
		for _, g := range c.gates {
			if g == nil {
				continue
			}
			g.touched(func(u *compiled) {
				if u != c && u.r.Kind.Capacity() && !slices.Contains(reads[c], u) {
					reads[c] = append(reads[c], u)
				}
			})
		}
	}
	placed := map[*compiled]bool{}
	ready := func(c *compiled, strict bool) bool {
		if !c.r.Kind.Capacity() || placed[c] || (c.from != nil && !placed[c.from]) {
			return false
		}
		if strict {
			for _, u := range reads[c] {
				if !placed[u] {
					return false
				}
			}
		}
		return true
	}
	for {
		progressed := false
		for _, c := range decl {
			if ready(c, true) {
				placed[c] = true
				m.order = append(m.order, c)
				progressed = true
			}
		}
		if progressed {
			continue
		}
		// Circular reads: place the first resource whose demand.from upstream is placed.
		for _, c := range decl {
			if ready(c, false) {
				placed[c] = true
				m.order = append(m.order, c)
				progressed = true
				break
			}
		}
		if !progressed {
			break
		}
	}
	return m, nil
}

func floorDiv(a, b int64) int64 {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

func gridIndex(t time.Time) int64 { return floorDiv(t.UnixNano(), int64(Step)) }

func gridTime(k int64) time.Time { return time.Unix(0, k*int64(Step)).UTC() }

// Advance integrates every grid point in (last, now]; idempotent for now <= last.
//
// Warm start: the first call, and any call after a gap longer than Horizon, starts from empty
// queues at the grid point Horizon before floor(now) and integrates every step up to now, so the
// state at a time depends only on the scheduled inputs over the preceding Horizon, not on when a
// consumer started. Live faults are read through the Clock at call time, so steps integrated in
// one call share the live state of that call: the composition root advances every consumer once
// per master tick (Advancer) so all consumers sample live inputs at the same instants. A live
// fault says nothing about when it began, so a warm start applies it only to the final
// liveSteps (the retained history) and reads scheduled windows alone before that, when the Clock
// is a ScheduledClock; without one every step reads Eval, live state included. A catch-up after a
// gap shorter than Horizon splits the same way whenever more than liveSteps steps are due (a
// blueprint disabled for 12 h and re-enabled under a live fault builds no 12 h backlog); a call
// of at most liveSteps steps, every ordinary tick, reads Eval throughout.
func (m *Model) Advance(now time.Time, c Clock) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := gridIndex(now)
	hist := c // the Clock for steps before the live stretch of a warm start or a long catch-up
	if !m.started || k-m.last > horizonSteps {
		m.started = true
		m.last = k - horizonSteps - 1
		m.reset(m.last)
	}
	// More than liveSteps to integrate is a warm start or a catch-up over a gap: the live fault's
	// start is unknown for all but the final liveSteps, so the earlier steps read scheduled windows
	// alone. A continuous advance (at most liveSteps) never reaches hist.
	if k-m.last > liveSteps {
		if sc, ok := c.(ScheduledClock); ok {
			hist = scheduledView{Clock: c, sc: sc}
		}
	}
	for j := m.last + 1; j <= k; j++ {
		if j <= k-liveSteps {
			m.step(j, hist)
		} else {
			m.step(j, c)
		}
	}
	if k > m.last {
		m.last = k
	}
}

func (m *Model) reset(before int64) {
	for _, c := range m.order {
		c.backlog, c.cumA, c.cumD = 0, 0, 0
		c.arr = []point{{k: before, a: 0}}
		c.last = rec{}
	}
}

func clamp01(v float64) float64 {
	switch {
	case v < 0 || math.IsNaN(v):
		return 0
	case v > 1:
		return 1
	}
	return v
}

// fault reads one mode for a resource at t, through its exact name and its environment alias.
func (c *compiled) fault(clk Clock, t time.Time, mode string) (bool, float64) {
	a, i := clk.Eval(t, mode, c.r.Name)
	if !a {
		i = 0
	}
	if c.alias != "" {
		if a2, i2 := clk.Eval(t, mode, c.alias); a2 {
			a = true
			i = math.Max(i, i2)
		}
	}
	return a, clamp01(i)
}

// baseDemand is the declared demand at t, before retry amplification: the upstream's served
// rate for demand.from, else the business or constant curve with the app minter's pause and
// clamp rules.
func (c *compiled) baseDemand(clk Clock, t time.Time) float64 {
	dm := c.r.Demand
	if dm == nil {
		return 0
	}
	if c.from != nil {
		return c.from.last.st.ServedRPS
	}
	vm := clk.VolumeMultiplier()
	if vm == 0 {
		return 0
	}
	if dm.Load == LoadConstant {
		return dm.PeakRPS * vm
	}
	f := clamp01(clk.Factor(t, c.decl.Weight, c.decl.NonProd))
	return dm.OffPeakRPS + (dm.PeakRPS-dm.OffPeakRPS)*f
}

// refState is the referenced dependency's latency factor and the failed share of work held
// across it (its unavailability and its credentials' failures), at the current step.
func (c *compiled) refState() (lf, failed float64) {
	lf = 1
	if c.ref == nil || !c.ref.last.valid {
		return lf, 0
	}
	lf = c.ref.last.st.LatencyFactor
	ok := 1 - c.ref.last.st.RejectShare
	for _, cr := range c.creds {
		ok *= 1 - cr.last.st.RejectShare
	}
	return lf, clamp01(1 - ok)
}

// hold is the effective hold: hold_ms + latency_ms(held_across) x (lf - 1).
func (c *compiled) hold(lf float64) float64 {
	h := c.r.HoldMS
	if c.ref != nil {
		h += c.ref.r.LatencyMS * (lf - 1)
	}
	return h
}

// rejectClass is the failure class of this resource's rejections, for retry policies.
func (c *compiled) rejectClass() (FailureClass, int) {
	status := 503
	if c.r.Kind == KindConcurrency || c.r.Kind == KindQuota {
		status = 429
	}
	if c.r.Error != nil && c.r.Error.Status != 0 {
		status = c.r.Error.Status
	}
	if status == 429 {
		return ClassThrottle, status
	}
	return ClassHTTP, status
}

// amplified is the effective demand for base demand d when arrivals rejected with share reject
// are resent under the declared retrying edges: each edge's share of the demand arrives
// sum_{k=0..retries} p^k times, p being reject when the policy retries this resource's class.
// An edge placed in its caller's request tree (a flow gate) instead arrives as often as the tree
// delivers it (gate.arrivals): gated by the share of requests its upstream uses and calls admit,
// and resent with every attempt of a call above it that a failure in the callee's subtree
// triggers, this resource's own rejections included. A demand.from resource keeps the
// Policy-only rule (its upstream already carries the path).
func (c *compiled) amplified(d, reject float64) float64 {
	if len(c.loads) == 0 {
		return d
	}
	class, status := c.rejectClass()
	rest, out := 1.0, 0.0
	for i, l := range c.loads {
		rest -= l.Share
		if g := c.gates[i]; g != nil && c.from == nil {
			out += l.Share * g.arrivals(latestReject(c, reject))
			continue
		}
		p := 0.0
		if l.Policy.Retryable(class, status) {
			p = reject
		}
		attempts, pk := 0.0, 1.0
		for range l.Policy.Retries + 1 {
			attempts += pk
			pk *= p
		}
		out += l.Share * attempts
	}
	return d * (math.Max(0, rest) + out)
}

// PreWorkFraction is the request layout's one timing share, owned here so the model and the app
// workload's evaluator read the same value: a service's work before its first downstream call
// is this share of its duration (the trace projection nests the first child hop there), and a
// request rejected before its work (a capacity rejection, a refused connection, a failed TLS
// handshake, a refused token) ends after this share of the callee's duration. The model holds a
// failed unit of work for exactly the time the evaluator's failure timeline gives it (see
// failedHold), so the occupancy it reports matches the failed executions the traces show.
//
// Basis: a refused connection returns within one network round trip (TCP RST) and a failed
// certificate check after the handshake's one or two round trips, a small share of a call's full
// latency: for the stories' 120 ms partner API, 12 ms is an internet-scale round trip. The
// pre-call share is the trace layout every causal request already uses, not a new parameter.
const PreWorkFraction = 0.1

// occupancy is the mean time one admitted unit of work occupies a pool connection or a
// concurrency slot: the effective hold h for work that completes, and for the failed share
// (FailedShare: the held-across dependency unavailable or its credential failing) the failed
// execution's elapsed time on the evaluator's failure timeline (failedHold). Each load's request
// tree gives the holder's policy on the failing call; the loads are weighted by their declared
// shares. Healthy (failed 0) it is exactly h.
//
// Like h, which does not include the holder's other calls, the failed hold leaves out calls the
// holder makes before the dependency. The fast-failure hold is what keeps a caller's resends of
// that failure plausible: three attempts of a call that fails in a tenth of its hold occupy less
// than one healthy call did, so the resends do not saturate the limit they pass through. A queue
// keeps the full hold (a consumer whose work fails returns the message for redelivery, which
// the model does not represent, so a fast failure frees no throughput), and a quota has no hold:
// every attempt, resends included, counts against its rate.
func (c *compiled) occupancy(h, lf, failed float64) float64 {
	if failed <= 0 || c.ref == nil {
		return h
	}
	sum, w := c.failedHoldSum(lf, failed)
	return (1-failed)*h + failed*sum/w
}

// failedHoldMean is the mean elapsed time of a failed unit of work, the failed term of
// occupancy: h when nothing fails or nothing is held across.
func (c *compiled) failedHoldMean(h, lf, failed float64) float64 {
	if failed <= 0 || c.ref == nil {
		return h
	}
	sum, w := c.failedHoldSum(lf, failed)
	return sum / w
}

// failedHoldSum is the load-share-weighted sum of failedHold over the loads (and the undeclared
// rest), with its total weight.
func (c *compiled) failedHoldSum(lf, failed float64) (sum, w float64) {
	rest := 1.0
	for i, l := range c.loads {
		rest -= l.Share
		sum += l.Share * c.failedHold(c.held[i], lf, failed)
		w += l.Share
	}
	if rest = math.Max(0, rest); rest > 0 || w == 0 {
		sum += rest * c.failedHold(nil, lf, failed)
		w += rest
	}
	return sum, w
}

// failedHold is the elapsed time of a unit of work that fails at its held-across dependency, as
// the evaluator lays it out: the work before the call (PreWorkFraction x hold_ms; it never waits
// on the dependency's extra latency), then each attempt of the call, which fails after
// PreWorkFraction of the dependency's current latency (latency_ms x lf). A refused connection
// (the dependency's share of the failures) is resent under pol when it retries connect, with
// pol's backoffs (backoff_ms x 2^k) between attempts; a credential failure is never resent. pol
// nil (no request tree places the call) is one attempt. Every term is a declared quantity (hold_ms,
// latency_ms, the latency factor, retries, backoff_ms) or the layout share; none is tuned.
func (c *compiled) failedHold(pol *CallPolicy, lf, failed float64) float64 {
	per := PreWorkFraction * c.ref.r.LatencyMS * lf
	span := per
	if pol != nil && pol.Retryable(ClassConnect, 0) {
		connect := clamp01(c.ref.last.st.RejectShare / failed)
		resent := per
		for k := range pol.Retries {
			resent += pol.BackoffMS*math.Pow(2, float64(k)) + per
		}
		span = connect*resent + (1-connect)*per
	}
	return PreWorkFraction*c.r.HoldMS + span
}

// capResult is one capacity step's physics for an offered demand.
type capResult struct {
	capacity, rho, reject, wait, inUse float64
}

func (c *compiled) capPhysics(d, h, i float64) capResult {
	offered := d * h / 1000
	var res capResult
	switch c.r.Kind {
	case KindQuota:
		res.capacity = c.r.LimitRPS * (1 - i)
		if d > 0 {
			res.reject = math.Max(0, 1-res.capacity/d)
			res.rho = math.Inf(1)
			if res.capacity > 0 {
				res.rho = d / res.capacity
			}
		}
		res.inUse = math.Min(d, res.capacity)
		return res
	case KindPool, KindConcurrency:
		res.capacity = float64(c.r.Size) * (1 - i)
		if offered > 0 {
			res.rho = math.Inf(1)
			if res.capacity > 0 {
				res.rho = offered / res.capacity
			}
		}
		if c.r.Kind == KindPool {
			T := c.r.AcquireTimeoutMS
			switch {
			case res.rho <= c.knee:
			case res.rho < 1:
				res.wait = math.Min(T, h*(res.rho-c.knee)/(1-res.rho))
			default:
				res.wait = T
			}
			if res.rho >= 1 {
				res.reject = 1 - 1/res.rho // 1 when rho is infinite
			}
		} else if offered > 0 {
			res.reject = math.Max(0, 1-res.capacity/offered)
		}
		res.inUse = math.Min(offered, res.capacity)
	}
	return res
}

func (m *Model) step(k int64, clk Clock) {
	t := gridTime(k)
	for _, c := range m.order {
		r := rec{k: k, valid: true}
		st := &r.st
		st.At = t
		st.LatencyFactor = 1
		setMode := func(mode string, active bool, i float64) {
			if active && i > 0 && st.Mode == "" {
				st.Mode, st.Intensity = mode, i
			}
		}
		switch c.r.Kind {
		case KindDependency:
			aS, iS := c.fault(clk, t, ModeDependencySlow)
			aU, iU := c.fault(clk, t, ModeDependencyUnavailable)
			st.LatencyFactor = 1 + (c.slow-1)*iS
			st.RejectShare, st.FailedShare = iU, iU
			// Dominant: the stronger active fault; a tie goes to unavailability.
			if iU >= iS {
				setMode(ModeDependencyUnavailable, aU, iU)
				setMode(ModeDependencySlow, aS, iS)
			} else {
				setMode(ModeDependencySlow, aS, iS)
				setMode(ModeDependencyUnavailable, aU, iU)
			}
		case KindCredential:
			a, i := c.fault(clk, t, ModeCredentialExpired)
			st.RejectShare, st.FailedShare = i, i
			setMode(ModeCredentialExpired, a, i)
		case KindRule:
			a, i := c.fault(clk, t, ModeRuleRegression)
			st.RejectShare = i * c.r.RejectShare
			st.FailedShare = st.RejectShare
			setMode(ModeRuleRegression, a, i)
		case KindPool, KindConcurrency, KindQuota:
			mode := map[Kind]string{KindPool: ModePoolExhausted, KindConcurrency: ModeConcurrencyReduced, KindQuota: ModeQuotaReduced}[c.r.Kind]
			a, i := c.fault(clk, t, mode)
			setMode(mode, a, i)
			base := c.baseDemand(clk, t)
			lf, failed := 1.0, 0.0
			if c.r.Kind != KindQuota {
				lf, failed = c.refState()
			}
			h := c.hold(lf)
			occ := c.occupancy(h, lf, failed)
			d := base
			res := c.capPhysics(d, occ, i)
			for n := 0; n < maxFixedPoint && len(c.loads) > 0; n++ {
				next := c.amplified(base, res.reject)
				if math.Abs(next-d) <= 1e-12*math.Max(1, next) {
					break
				}
				d = next
				res = c.capPhysics(d, occ, i)
			}
			st.Demand, st.Capacity, st.Utilization = d, res.capacity, finiteUtil(res.rho)
			st.RejectShare, st.WaitMS, st.LatencyFactor, st.InUse = res.reject, res.wait, lf, res.inUse
			if c.r.Kind != KindQuota {
				st.HoldMS = h
				st.FailedShare = failed
			}
			st.Waiting = d * (1 - res.reject) * res.wait / 1000
			r.arrivals = d * stepSec
			r.rejected = r.arrivals * res.reject
			r.admitted = r.arrivals - r.rejected
			r.served = r.admitted
			r.failed = r.served * st.FailedShare
			if c.r.Kind != KindQuota {
				r.failedHold = c.failedHoldMean(h, lf, failed)
			}
			st.ServedRPS = r.served / stepSec
		case KindQueue:
			a, i := c.fault(clk, t, ModeConsumerSlowdown)
			setMode(ModeConsumerSlowdown, a, i)
			lf, failed := c.refState()
			h := c.hold(lf)
			mu := float64(c.r.Size) * 1000 / h * (1 - i)
			base := c.baseDemand(clk, t)
			c.queueStep(&r, k, base, mu)
			st.LatencyFactor, st.HoldMS, st.FailedShare = lf, h, failed
			r.failed = r.served * failed
			r.failedHold = h // failed work keeps the full hold (see occupancy)
		}
		c.ring[mod(k, ringSteps)] = r
		c.last = r
	}
}

func mod(a, b int64) int64 {
	x := a % b
	if x < 0 {
		x += b
	}
	return x
}

// finiteUtil reports rho, or 1 when capacity is zero under load (RejectShare carries the overload).
func finiteUtil(rho float64) float64 {
	if math.IsInf(rho, 1) {
		return 1
	}
	return rho
}

// queueOutcome integrates one fluid FIFO step from backlog b: inflow and service over Step.
func (c *compiled) queueOutcome(b, d, mu float64) (next, served, overflow float64) {
	inflow := d * stepSec
	raw := b + inflow - mu*stepSec
	if raw <= 0 {
		return 0, b + inflow, 0
	}
	next, served = raw, mu*stepSec
	if c.r.MaxDepth > 0 && raw > c.r.MaxDepth {
		overflow = raw - c.r.MaxDepth
		next = c.r.MaxDepth
	}
	return next, served, overflow
}

// queueStep integrates one step of a fluid FIFO queue (producer retries amplify arrivals by the
// overflow share) and records exact oldest-message age from the cumulative curves.
func (c *compiled) queueStep(r *rec, k int64, base, mu float64) {
	st := &r.st
	d := base
	next, served, overflow := c.queueOutcome(c.backlog, d, mu)
	for n := 0; n < maxFixedPoint && len(c.loads) > 0; n++ {
		share := 0.0
		if d > 0 {
			share = overflow / (d * stepSec)
		}
		nd := c.amplified(base, share)
		if math.Abs(nd-d) <= 1e-12*math.Max(1, nd) {
			break
		}
		d = nd
		next, served, overflow = c.queueOutcome(c.backlog, d, mu)
	}
	inflow := d * stepSec
	admitted := inflow - overflow
	c.backlog = next
	c.cumA += admitted
	c.cumD += served
	c.arr = append(c.arr, point{k: k, a: c.cumA})

	r.arrivals, r.admitted, r.rejected, r.served = inflow, admitted, overflow, served
	st.Demand, st.Capacity, st.Backlog, st.Waiting = d, mu, next, next
	st.ServedRPS = served / stepSec
	if inflow > 0 {
		st.RejectShare = overflow / inflow
	}
	switch {
	case mu > 0:
		st.Utilization = d / mu
		st.InUse = float64(c.r.Size) * math.Min(1, served/stepSec/mu)
		st.WaitMS = next / mu * 1000
	default:
		if d > 0 || next > 0 {
			st.Utilization = 1
			st.InUse = float64(c.r.Size)
		}
		st.WaitMS = float64(Horizon / time.Millisecond)
	}
	st.OldestAgeSec = c.oldestAge(k)
}

// oldestAge is now - A^-1(D(now)): the arrival time of the oldest message still queued, found on
// the cumulative arrival curve (linear within a step), capped at Horizon.
func (c *compiled) oldestAge(k int64) float64 {
	if c.backlog <= 0 || c.cumD >= c.cumA {
		c.arr = c.arr[len(c.arr)-1:]
		return 0
	}
	// Drop breakpoints no longer needed: keep the latest point with A <= D as the head.
	drop := 0
	for drop+1 < len(c.arr) && c.arr[drop+1].a <= c.cumD {
		drop++
	}
	// Bound memory by the horizon: anything older only ever yields the capped age.
	if lim := len(c.arr) - int(horizonSteps) - 2; lim > drop {
		drop = lim
	}
	if drop > 0 {
		c.arr = append(c.arr[:0], c.arr[drop:]...)
	}
	if len(c.arr) < 2 {
		return 0
	}
	p0, p1 := c.arr[0], c.arr[1]
	var tau float64 // seconds relative to grid point k
	if p0.a <= c.cumD && p1.a > p0.a {
		frac := (c.cumD - p0.a) / (p1.a - p0.a)
		tau = float64(p0.k-k)*stepSec + frac*float64(p1.k-p0.k)*stepSec
	} else {
		tau = float64(p0.k-k) * stepSec
	}
	return math.Min(-tau, Horizon.Seconds())
}

// State is the named resource's state at the last grid point at or before at.
func (m *Model) State(name string, at time.Time) (State, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.recAt(name, at)
	return r.st, ok
}

func (m *Model) recAt(name string, at time.Time) (rec, bool) {
	c, ok := m.byName[name]
	if !ok || !m.started {
		return rec{}, false
	}
	k := gridIndex(at)
	if k > m.last {
		k = m.last
	}
	r := c.ring[mod(k, ringSteps)]
	if !r.valid || r.k != k {
		return rec{}, false
	}
	return r, true
}
