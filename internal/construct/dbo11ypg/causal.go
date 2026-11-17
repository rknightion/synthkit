// SPDX-License-Identifier: AGPL-3.0-only

package dbo11ypg

import (
	"fmt"
	"github.com/rknightion/synthkit/internal/causal"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/shape"
	"time"
)

var (
	_ causal.Consumer        = (*Config)(nil)
	_ causal.BindingDeclarer = (*Config)(nil)
	_ causal.Advancer        = (*construct)(nil)
)

// ResourceBindings identifies the instance using its connection_info join key.
func (c *Config) ResourceBindings(ctx causal.BindContext) []causal.Binding {
	if c.resource == "" {
		return nil
	}
	return []causal.Binding{{Resource: c.resource, Identity: map[string]string{"db_instance_identifier": ctx.Instance}}}
}

// Bound instances ignore ALL legacy database-axis incidents, including direct injection.
func (c *construct) legacyFactor(now time.Time, w *core.World, mode string, peak float64) float64 {
	if c.model != nil {
		return 1
	}
	return w.Shape.FailFactor(now, mode, c.db.Name, peak)
}

func resolveBinding(cfg *Config, fx *fixture.Set) (causal.Resource, error) {
	if cfg.set.Empty() {
		return causal.Resource{}, fmt.Errorf("dbo11y_postgres: database %q resource %q: the blueprint declares no environments[].resources", fx.DB.Name, cfg.resource)
	}
	env := ""
	if fx.Env != nil {
		env = fx.Env.Name
	}
	r, _, ok := cfg.set.Resolve(cfg.resource, env)
	if !ok {
		return causal.Resource{}, fmt.Errorf("dbo11y_postgres: database %q resource %q is not a declared resource", fx.DB.Name, cfg.resource)
	}
	if r.Kind != causal.KindDependency && r.Kind != causal.KindPool {
		return causal.Resource{}, fmt.Errorf("dbo11y_postgres: database %q resource %q is a %s resource; want kind dependency or pool", fx.DB.Name, cfg.resource, r.Kind)
	}
	return r, nil
}

// staticClock is the causal clock of a tick with no shape engine: no faults, constant load.
type staticClock struct{}

func (staticClock) Eval(time.Time, string, string) (bool, float64) { return false, 0 }
func (staticClock) Factor(time.Time, float64, bool) float64        { return 1 }
func (staticClock) VolumeMultiplier() float64                      { return 1 }

// AdvanceCausal advances the construct's model to now (causal.Advancer); the runner calls it on
// every master tick so every consumer samples live faults at the same instants. An unbound
// instance holds no model and ignores it.
func (c *construct) AdvanceCausal(now time.Time, clk causal.Clock) {
	if c.model != nil {
		c.model.Advance(now, clk)
	}
}

// boundReading is what the causal model contributes to one tick.
type boundReading struct {
	latency float64 // mean latency factor over the window's grid steps; 1 when unbound
	inUse   float64 // mean in-use pool connections over the window
	pool    bool    // inUse replaces the baseline connection count
}

// boundWindow reads the bound resource over the trailing tick window (now-Interval, now]. An
// unbound instance, or a window the model holds no step for, reads as the unbound baseline.
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
