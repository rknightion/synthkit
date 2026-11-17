// SPDX-License-Identifier: AGPL-3.0-only

package runner

import (
	"context"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/blueprint"
	"github.com/rknightion/synthkit/internal/causal"
	"github.com/rknightion/synthkit/internal/control"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/shape"
)

// advanceRecorder records every AdvanceCausal call and the clock it received.
type advanceRecorder struct {
	calls  []time.Time
	clocks []causal.Clock
}

func (a *advanceRecorder) AdvanceCausal(now time.Time, c causal.Clock) {
	a.calls = append(a.calls, now)
	a.clocks = append(a.clocks, c)
}

type causalConstruct struct {
	fakeConstruct
	advanceRecorder
}

type causalWorkload struct {
	fakeWorkload
	advanceRecorder
}

// TestAdvanceCausalEveryMasterTick pins the root-owned causal seam: every causal construct and
// workload is advanced on every master tick of its blueprint, with the blueprint's shape engine,
// whatever its own cadence, so all consumers sample live faults at the same instants.
func TestAdvanceCausalEveryMasterTick(t *testing.T) {
	var cons []*causalConstruct
	var wls []*causalWorkload
	reg := core.NewRegistry()
	reg.RegisterConstruct(core.ConstructReg{Kind: "causal_c", Doc: "t", Scope: core.ScopeBlueprint,
		NewConfig:      func() any { return &struct{}{} },
		MetricProducer: fixedMetricProducer(producerPromRW),
		Build: func(any, *fixture.Set) (core.Construct, error) {
			c := &causalConstruct{fakeConstruct: fakeConstruct{kind: "causal_c", signals: []core.SignalClass{core.Metrics}, interval: 10 * time.Minute}}
			cons = append(cons, c)
			return c, nil
		}})
	reg.RegisterWorkload(core.WorkloadReg{Kind: "causal_w", Doc: "t",
		NewConfig:      func() any { return &struct{}{} },
		MetricProducer: fixedMetricProducer(producerPromRW),
		Build: func(_ any, b core.Binding) (core.Workload, error) {
			w := &causalWorkload{fakeWorkload: fakeWorkload{name: b.Name}}
			wls = append(wls, w)
			return w, nil
		}})
	r := New(Sinks{Metrics: &coretest.MetricCapture{}, Logs: &coretest.LogCapture{}, Traces: &coretest.TraceCapture{}}, reg, Options{})
	cl := &fixture.Cluster{Name: "c", Env: &fixture.Env{Name: "prod", Weight: 1}}
	if err := r.AddBlueprint(&blueprint.Resolved{Name: "causal-bp", Label: "causal-bp",
		Constructs: []blueprint.ConstructInstance{{Kind: "causal_c", Name: "c1", Config: &struct{}{}, Fixtures: &fixture.Set{Seed: "s"}}},
		Workloads:  []blueprint.WorkloadInstance{{Kind: "causal_w", Name: "api", Config: &struct{}{}, Replicas: 1, Env: cl.Env, Cluster: cl}},
	}); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC)
	ticks := []time.Time{start, start.Add(5 * time.Second), start.Add(10 * time.Second)}
	for _, now := range ticks {
		if err := r.MasterTick(context.Background(), now); err != nil {
			t.Fatal(err)
		}
	}
	if len(cons) != 1 || len(wls) != 1 {
		t.Fatalf("built %d constructs, %d workloads", len(cons), len(wls))
	}
	for name, rec := range map[string]*advanceRecorder{"construct": &cons[0].advanceRecorder, "workload": &wls[0].advanceRecorder} {
		if len(rec.calls) != len(ticks) {
			t.Fatalf("%s advanced %d times over %d master ticks", name, len(rec.calls), len(ticks))
		}
		for i, now := range ticks {
			if !rec.calls[i].Equal(now) {
				t.Errorf("%s call %d at %v, want %v", name, i, rec.calls[i], now)
			}
			if eng, ok := rec.clocks[i].(*shape.Engine); !ok || eng == nil {
				t.Errorf("%s call %d clock %T is not the blueprint shape engine", name, i, rec.clocks[i])
			}
		}
	}
	if cons[0].clocks[0] != wls[0].clocks[0] {
		t.Error("construct and workload received different clocks")
	}
}

// TestAdvanceCausalDisabledConstruct pins that disabling a construct (one instance, or its whole
// kind) stops its metric ticks, not its causal model: it is still advanced on every master tick.
// A model that skipped ticks while disabled would, on re-enable, hold a stale history that the
// still-running consumers of the same blueprint do not share, and the independently compiled
// models would disagree until the next Horizon restart (ARCHITECTURE I42).
func TestAdvanceCausalDisabledConstruct(t *testing.T) {
	for name, state := range map[string]control.State{
		"instance": {VolumeMultiplier: 1, DisabledConstructs: []string{control.ConstructKey("causal-bp", "causal_c", "c1")}},
		"kind":     {VolumeMultiplier: 1, DisabledKinds: []string{"causal_c"}},
	} {
		t.Run(name, func(t *testing.T) {
			var cons []*causalConstruct
			reg := core.NewRegistry()
			reg.RegisterConstruct(core.ConstructReg{Kind: "causal_c", Doc: "t", Scope: core.ScopeBlueprint,
				NewConfig:      func() any { return &struct{}{} },
				MetricProducer: fixedMetricProducer(producerPromRW),
				Build: func(any, *fixture.Set) (core.Construct, error) {
					c := &causalConstruct{fakeConstruct: fakeConstruct{kind: "causal_c", signals: []core.SignalClass{core.Metrics}, interval: 10 * time.Minute}}
					cons = append(cons, c)
					return c, nil
				}})
			r := New(Sinks{Metrics: &coretest.MetricCapture{}, Logs: &coretest.LogCapture{}, Traces: &coretest.TraceCapture{}}, reg, Options{})
			if err := r.AddBlueprint(&blueprint.Resolved{Name: "causal-bp", Label: "causal-bp",
				Constructs: []blueprint.ConstructInstance{{Kind: "causal_c", Name: "c1", Config: &struct{}{}, Fixtures: &fixture.Set{Seed: "s"}}},
			}); err != nil {
				t.Fatal(err)
			}
			r.ApplyControl(state)
			if r.constructEnabled("causal-bp", "causal_c", "c1") {
				t.Fatal("construct is still enabled: the test exercises nothing")
			}
			start := time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC)
			ticks := []time.Time{start, start.Add(5 * time.Second), start.Add(10 * time.Second)}
			for _, now := range ticks {
				if err := r.MasterTick(context.Background(), now); err != nil {
					t.Fatal(err)
				}
			}
			if len(cons) != 1 {
				t.Fatalf("built %d constructs", len(cons))
			}
			if got := cons[0].calls; len(got) != len(ticks) {
				t.Fatalf("disabled construct advanced %d times over %d master ticks", len(got), len(ticks))
			}
		})
	}
}
