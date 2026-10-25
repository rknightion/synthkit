// SPDX-License-Identifier: AGPL-3.0-only

package dbo11ymysql

import (
	"fmt"
	"time"

	"github.com/rknightion/synthkit/internal/causal"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/shape"
)

// BindResource records the resolver-selected database resource; no telemetry identity changes.
func (c *Config) BindResource(name string) { c.resource = name }

// UseResources injects the immutable declarations. Each Build compiles an independent model.
func (c *Config) UseResources(s causal.Set) { c.set = s }

var (
	_ causal.Consumer        = (*Config)(nil)
	_ causal.BindingDeclarer = (*Config)(nil)
	_ causal.Advancer        = (*construct)(nil)
)

// ResourceBindings uses the dbo connection_info identity, not a CloudWatch dimension.
func (c *Config) ResourceBindings(ctx causal.BindContext) []causal.Binding {
	if c.resource == "" {
		return nil
	}
	return []causal.Binding{{Resource: c.resource, Identity: map[string]string{"db_instance_identifier": ctx.Instance}}}
}

func resolveBinding(cfg *Config, fx *fixture.Set) (causal.Resource, error) {
	if cfg.set.Empty() {
		return causal.Resource{}, fmt.Errorf("dbo11y_mysql: database %q resource %q: the blueprint declares no environments[].resources", fx.DB.Name, cfg.resource)
	}
	env := ""
	if fx.Env != nil {
		env = fx.Env.Name
	}
	r, _, ok := cfg.set.Resolve(cfg.resource, env)
	if !ok {
		return causal.Resource{}, fmt.Errorf("dbo11y_mysql: database %q resource %q is not a declared resource", fx.DB.Name, cfg.resource)
	}
	if r.Kind != causal.KindDependency && r.Kind != causal.KindPool {
		return causal.Resource{}, fmt.Errorf("dbo11y_mysql: database %q resource %q is a %s resource; want kind dependency or pool", fx.DB.Name, cfg.resource, r.Kind)
	}
	return r, nil
}

// AdvanceCausal receives every master tick, including ticks without emission.
func (c *construct) AdvanceCausal(now time.Time, clk causal.Clock) {
	if c.model != nil {
		c.model.Advance(now, clk)
	}
}

type staticClock struct{}

func (staticClock) Eval(time.Time, string, string) (bool, float64) { return false, 0 }
func (staticClock) Factor(time.Time, float64, bool) float64        { return 1 }
func (staticClock) VolumeMultiplier() float64                      { return 1 }

type boundReading struct {
	latency float64
	inUse   float64
	pool    bool
}

// boundWindow takes the half-open trailing minute. Latency is the arithmetic mean
// of exact-grid factors, not a final-state reading or an acquisition-wait estimate.
func (c *construct) boundWindow(now time.Time, eng *shape.Engine) boundReading {
	out := boundReading{latency: 1}
	if c.model == nil {
		return out
	}
	if eng != nil {
		c.model.Advance(now, eng)
	} else {
		c.model.Advance(now, staticClock{})
	}
	from := now.Add(-c.Interval())
	win, ok := c.model.Window(c.resource, from, now)
	if !ok || win.Steps == 0 {
		return out
	}
	var sum float64
	var n int
	for at := from.Truncate(causal.Step).Add(causal.Step); !at.After(now); at = at.Add(causal.Step) {
		st, ok := c.model.State(c.resource, at)
		if !ok || !st.At.Equal(at) {
			continue
		}
		sum += st.LatencyFactor
		n++
	}
	if n > 0 {
		out.latency = sum / float64(n)
	}
	if c.pool {
		out.inUse, out.pool = win.MeanInUse, true
	}
	return out
}
