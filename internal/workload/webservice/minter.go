// SPDX-License-Identifier: AGPL-3.0-only

package webservice

import (
	"math"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/ledger"
	"github.com/rknightion/synthkit/internal/shape"
)

// minter is the web_service contribution to the blueprint master clock. It mints the
// correlated NARRATIVE SAMPLE — a small, cadence-invariant volume of fully-correlated
// requests that drives the trace/log/RUM story. (The metric lane's aggregate VOLUME is
// derived separately from the traffic config in metrics.go; see ARCHITECTURE §4.)
type minter struct {
	name    string
	env     string
	cluster string
	weight  float64
	nonProd bool
	cfg     Config
	calls   []callSpec
}

// callSpec is the resolved downstream hop the workload makes on every request (one per
// binding.Calls entry — a CLIENT span + service-graph edge). ParentHop mirrors
// fixture.CallTarget.ParentHop (-1 = child of backend SERVER). AI is set for AI hops.
type callSpec struct {
	Kind      string         // "db" | "cache" | "service" | AI hop kinds
	Target    string         // resolved instance/service name (the cross-signal join key)
	Engine    string         // "postgres" | "mysql" | "redis"; "" for service/AI hops
	AI        *ledger.AICall // gen_ai identity for AI hops (nil otherwise)
	ParentHop int
}

// callSpecsFrom resolves the binding's call targets into the minter's per-request hop
// template. The workload never invents instance names — they come from resolved fixtures.
func callSpecsFrom(b core.Binding) []callSpec {
	out := make([]callSpec, 0, len(b.Calls))
	for _, ct := range b.Calls {
		switch {
		case ct.Kind == "db":
			if ct.DB != nil {
				out = append(out, callSpec{Kind: "db", Target: ct.DB.Name, Engine: ct.DB.Engine, ParentHop: ct.ParentHop})
			}
		case ct.Kind == "cache":
			if ct.Cache != nil {
				out = append(out, callSpec{Kind: "cache", Target: ct.Cache.Name, Engine: ct.Cache.Engine, ParentHop: ct.ParentHop})
			}
		case ct.Kind == "service":
			if ct.Service != "" {
				out = append(out, callSpec{Kind: "service", Target: ct.Service, ParentHop: ct.ParentHop})
			}
		case fixture.IsAIKind(ct.Kind):
			if ct.AI != nil {
				// Join target: prefer the SpanName subject (agent/tool/workflow/data-source
				// name), falling back to the model for a bare model hop.
				target := ct.AI.Subject
				if target == "" {
					target = ct.AI.Model
				}
				out = append(out, callSpec{
					Kind:      ct.Kind,
					Target:    target,
					AI:        &ledger.AICall{Op: ct.AI.Op, Provider: ct.AI.Provider, Model: ct.AI.Model, Subject: ct.AI.Subject},
					ParentHop: ct.ParentHop,
				})
			}
		}
	}
	return out
}

// newMinter builds a minter from the resolved identity + config. (callSpecs are wired
// from the binding by build via SetCalls; tests set them directly.)
func newMinter(name, env, cluster string, weight float64, nonProd bool, cfg Config) *minter {
	return &minter{
		name:    name,
		env:     env,
		cluster: cluster,
		weight:  weight,
		nonProd: nonProd,
		cfg:     cfg,
	}
}

// Workload implements ledger.Minter — the dispatch key stamped onto every request.
func (m *minter) Workload() string { return m.name }

// rpsAt interpolates the instantaneous request rate off_peak→peak by the shape factor
// (diurnal plateau × weekly × env weight), clamped so off-peak never under/peak never
// overshoots the configured envelope. The factor can exceed 1.0 (env weight); clamp the
// interpolation parameter to [0,1].
func (m *minter) rpsAt(now time.Time, eng *shape.Engine) float64 {
	f := eng.Factor(now, m.weight, m.nonProd)
	if f < 0 {
		f = 0
	}
	if f > 1 {
		f = 1
	}
	off := m.cfg.Traffic.OffPeakRPS
	peak := m.cfg.Traffic.PeakRPS
	return off + (peak-off)*f
}

// expectedVolume is the deterministic mean number of requests to mint on a tick of length
// tickSec. It is exposed (unexported but test-visible in-package) so the cadence-invariance
// test asserts on the math, not on stochastic Mint output.
//
// FULL VOLUME = rps(now) × tickSec — every request emits exactly one trace (one traceparent),
// so the trace volume tracks the real request rate and matches the metric lane's own
// rps×interval math. There is NO narrative-sample clamp: production realism means no sampling
// in the generator (filter downstream in Adaptive Traces if ever needed). Cadence invariance
// (ARCHITECTURE I10) is exact because the expectation is linear in tickSec — summing N short
// ticks equals one long tick of the same wall-time.
func (m *minter) expectedVolume(now time.Time, tickSec float64, eng *shape.Engine) float64 {
	return m.rpsAt(now, eng) * tickSec
}

// Mint fabricates this tick's correlated requests. Volume is StochasticRound(expected)
// so the long-run mean tracks expectedVolume exactly while integer counts stay honest.
func (m *minter) Mint(now time.Time, tickSec float64, eng *shape.Engine) []*ledger.Request {
	n := ledger.StochasticRound(m.expectedVolume(now, tickSec, eng), eng.Float64())
	if n <= 0 {
		return nil
	}
	out := make([]*ledger.Request, 0, n)
	for range n {
		out = append(out, m.mintOne(now, eng))
	}
	return out
}

// mintOne fabricates a single fully-correlated request: a fresh correlation key-set, the
// binding identity, a route+outcome draw, a lognormal-ish duration around the endpoint
// p95 (incident-scaled), browser-origin flag, and one sub-call per call spec summing to
// less than the request duration.
func (m *minter) mintOne(now time.Time, eng *shape.Engine) *ledger.Request {
	ep := m.pickEndpoint(eng)

	r := &ledger.Request{
		Correlation: ledger.NewCorrelation(),
		Workload:    m.name,
		Env:         m.env,
		Cluster:     m.cluster,
		Route:       ep.Route,
		Start:       now,
	}

	// Outcome: base from the endpoint error_rate, raised by a scoped "error_burst"
	// incident; "latency_spike" (scoped to this workload) multiplies the duration. Scope
	// is the workload name so incidents can target this instance (extract / shape.Eval).
	errRate := ep.ErrorRate
	if burst, inten := eng.Eval(now, "error_burst", m.name); burst {
		errRate = errRate + (1-errRate)*0.5*inten // push toward more 5xx during a burst
	}
	r.Outcome, r.StatusCode, r.ErrorKind = drawOutcome(errRate, eng)

	// Duration: lognormal-ish around the p95 target. p95 ≈ median × e^(1.645σ); pick a
	// modest σ so the median sits well below p95 and the tail reaches it.
	dur := drawDuration(ep.P95ms, eng)
	dur *= eng.FailFactor(now, "latency_spike", m.name, 4.0) // up to 4× under a full-intensity spike
	r.Duration = time.Duration(dur * float64(time.Millisecond))

	// Browser origin: a fraction of requests originate in a RUM session (only meaningful
	// when RUM is configured — otherwise the browser span/beacons are never emitted).
	r.BrowserOrigin = m.cfg.RUM && eng.Float64() < 0.6

	// Downstream hops: one CLIENT span per call spec, plausible sub-durations that sum to
	// strictly less than the request duration (the backend does work between/after hops).
	r.Calls = m.drawCalls(r.Duration, r.Outcome, eng)

	return r
}

// pickEndpoint draws an endpoint uniformly across the catalogue.
func (m *minter) pickEndpoint(eng *shape.Engine) Endpoint {
	if len(m.cfg.Endpoints) == 1 {
		return m.cfg.Endpoints[0]
	}
	return m.cfg.Endpoints[eng.IntN(len(m.cfg.Endpoints))]
}

// drawCalls builds one Call per spec with sub-durations that together occupy < ~70% of
// the request duration. On a server-error outcome the LAST hop is marked failed (the
// realistic "downstream blew up" story). Per-hop span IDs are minted here (I9).
func (m *minter) drawCalls(reqDur time.Duration, outcome ledger.Outcome, eng *shape.Engine) []ledger.Call {
	if len(m.calls) == 0 || reqDur <= 0 {
		return nil
	}
	budget := time.Duration(float64(reqDur) * 0.7)
	per := budget / time.Duration(len(m.calls))
	out := make([]ledger.Call, 0, len(m.calls))
	for i, cs := range m.calls {
		frac := 0.3 + eng.Float64()*0.7
		d := time.Duration(float64(per) * frac)
		if d < time.Millisecond {
			d = time.Millisecond
		}
		failed := outcome == ledger.OutcomeServerError && i == len(m.calls)-1
		c := ledger.Call{
			Kind:           cs.Kind,
			Target:         cs.Target,
			Engine:         cs.Engine,
			AI:             cs.AI,
			SpanID:         ledger.NewSpanID(),
			ParentHopIndex: cs.ParentHop,
			Duration:       d,
			Failed:         failed,
		}
		// A "service" hop — and an "llm_gateway" hop (the Path-B connected gateway SERVER
		// span) — gets a second span id for the callee/gateway's SERVER span.
		if cs.Kind == "service" || cs.Kind == fixture.KindLLMGateway {
			c.PeerSpanID = ledger.NewSpanID()
		}
		out = append(out, c)
	}
	return out
}

// drawOutcome maps an effective error rate to a concrete (outcome, status, errorKind).
// On error, split between client (4xx), throttle (429), and server (5xx).
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

// drawDuration returns a lognormal-ish latency in ms whose tail reaches roughly the p95
// target. median = p95 / e^(1.645σ) with σ=0.5 puts p95 at the configured value.
func drawDuration(p95ms float64, eng *shape.Engine) float64 {
	if p95ms <= 0 {
		p95ms = 120
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
