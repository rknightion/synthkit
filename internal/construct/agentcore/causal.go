// SPDX-License-Identifier: AGPL-3.0-only

package agentcore

import (
	"fmt"
	"math"
	"time"

	"github.com/rknightion/synthkit/internal/causal"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/shape"
)

// boundRuntime is one runtime bound to a causal concurrency resource.
type boundRuntime struct {
	agent    string
	resource string // resolved declared name
	arn      string // the series' name label
}

// bind validates Config.Runtimes against the injected causal set and compiles the model. A
// deployment with no runtimes holds no model and runs exactly the unbound code path.
func (c *Construct) bind(fx *fixture.Set) error {
	cfg := c.cfg
	if len(cfg.Runtimes) == 0 {
		return nil
	}
	if cfg.set.Empty() {
		return fmt.Errorf("agentcore: runtimes bind causal resources, but the blueprint declares no environments[].resources")
	}
	if !cfg.wantRuntime() {
		return fmt.Errorf("agentcore: runtimes need the runtime sub-signal: a bound runtime supplies invocation-class series")
	}
	env := ""
	if fx.Env != nil {
		env = fx.Env.Name
	}
	agents := map[string]bool{}
	for _, a := range cfg.Agents {
		agents[a] = true
	}
	seenAgent := map[string]bool{}
	seenResource := map[string]string{}
	for _, rt := range cfg.Runtimes {
		if !agents[rt.Agent] {
			return fmt.Errorf("agentcore: runtime %q is not a declared agent (agents: %v)", rt.Agent, cfg.Agents)
		}
		if rt.Agent == "global" {
			// Its ARN is the deployment-wide series' name label: the two would be one series.
			return fmt.Errorf("agentcore: runtime %q cannot be bound: its series would collide with the deployment-wide runtime/global series", rt.Agent)
		}
		if seenAgent[rt.Agent] {
			return fmt.Errorf("agentcore: runtime %q is bound twice", rt.Agent)
		}
		seenAgent[rt.Agent] = true
		if rt.Resource == "" {
			return fmt.Errorf("agentcore: runtime %q needs a resource", rt.Agent)
		}
		r, _, ok := cfg.set.Resolve(rt.Resource, env)
		if !ok {
			return fmt.Errorf("agentcore: runtime %q resource %q is not a declared resource", rt.Agent, rt.Resource)
		}
		if r.Kind != causal.KindConcurrency {
			return fmt.Errorf("agentcore: runtime %q resource %q is a %s resource; want kind concurrency", rt.Agent, rt.Resource, r.Kind)
		}
		if other, dup := seenResource[r.Name]; dup {
			return fmt.Errorf("agentcore: runtimes %q and %q bind the same resource %q; a concurrency resource binds one runtime", other, rt.Agent, r.Name)
		}
		seenResource[r.Name] = rt.Agent
		c.bound = append(c.bound, boundRuntime{
			agent: rt.Agent, resource: r.Name,
			arn: fmt.Sprintf("arn:aws:bedrock-agentcore:%s:%s:runtime/%s", c.cloud.Region, c.cloud.AccountID, rt.Agent),
		})
	}
	m, err := causal.Compile(cfg.set)
	if err != nil {
		return fmt.Errorf("agentcore: causal model: %w", err)
	}
	c.model = m
	return nil
}

// staticClock advances a model with no shape engine: no faults, constant business factor.
type staticClock struct{}

func (staticClock) Eval(time.Time, string, string) (bool, float64) { return false, 0 }
func (staticClock) Factor(time.Time, float64, bool) float64        { return 1 }
func (staticClock) VolumeMultiplier() float64                      { return 1 }

var _ causal.Advancer = (*Construct)(nil)

// AdvanceCausal advances the construct's model to now (causal.Advancer); the runner calls it on
// every master tick so every consumer samples live faults at the same instants. A construct
// with no bound runtime holds no model and ignores it.
func (c *Construct) AdvanceCausal(now time.Time, clk causal.Clock) {
	if c.model != nil {
		c.model.Advance(now, clk)
	}
}

// advance brings the model to now before a Tick reads its window (idempotent after the runner's
// AdvanceCausal).
func (c *Construct) advance(now time.Time, eng *shape.Engine) {
	if eng != nil {
		c.model.Advance(now, eng)
		return
	}
	c.model.Advance(now, staticClock{})
}

// emitBound emits one bound runtime's invocation-class families for the window (from, to]:
// invocations are the admitted requests, throttles the rejected ones, system_errors the admitted
// requests that failed, total_errors their sum (the model has no client-error source) and latency
// the effective hold. A window with no admitted request has no latency observation.
func (c *Construct) emitBound(b *boundRuntime, from, to time.Time) {
	win, ok := c.model.Window(b.resource, from, to)
	if !ok {
		return
	}
	lbls := c.baseLabels(nil)
	lbls["name"] = b.arn
	invocations := win.Admitted
	throttles := win.Rejected
	systemErrors := math.Min(win.Failed, invocations)
	emitGauge(c.st, "aws_bedrock_agentcore_invocations", lbls, invocations)
	emitGauge(c.st, "aws_bedrock_agentcore_throttles", lbls, throttles)
	emitGauge(c.st, "aws_bedrock_agentcore_system_errors", lbls, systemErrors)
	emitGauge(c.st, "aws_bedrock_agentcore_total_errors", lbls, systemErrors+throttles)
	if invocations > 0 {
		emitGauge(c.st, "aws_bedrock_agentcore_latency", lbls, c.meanHold(b.resource, from, to))
		return
	}
	// The state store keeps a gauge until it is deleted: drop the last window's latency so an
	// empty window reports no observation instead of a stale one.
	for _, suffix := range []string{"_sum", "_average", "_maximum", "_minimum", "_sample_count"} {
		c.st.DeleteGauge("aws_bedrock_agentcore_latency"+suffix, lbls)
	}
}

// meanHold is the effective hold of the window's grid steps, weighted by each step's served
// rate: the mean per-request latency. A window with no served work reads the newest state.
func (c *Construct) meanHold(resource string, from, to time.Time) float64 {
	var weighted, weight float64
	for k := floorGrid(from) + 1; k <= floorGrid(to); k++ {
		st, ok := c.model.State(resource, time.Unix(0, k*int64(causal.Step)).UTC())
		if !ok || st.At.UnixNano() != k*int64(causal.Step) {
			continue
		}
		weighted += st.HoldMS * st.ServedRPS
		weight += st.ServedRPS
	}
	if weight > 0 {
		return weighted / weight
	}
	st, _ := c.model.State(resource, to)
	return st.HoldMS
}

func floorGrid(t time.Time) int64 {
	n, d := t.UnixNano(), int64(causal.Step)
	q := n / d
	if n%d != 0 && n < 0 {
		q--
	}
	return q
}
