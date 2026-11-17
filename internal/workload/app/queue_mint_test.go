// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/shape"
)

// A from_resource consumer mints through a deterministic carry, so a tick that mints nothing
// draws nothing from the engine; a declared-traffic app still takes its StochasticRound draw on
// every tick, including an empty one.
func TestMintEngineDrawsFromResourceVersusDeclared(t *testing.T) {
	nextDraw := func(eng *shape.Engine, draws int) float64 {
		for range draws {
			eng.Float64()
		}
		return eng.Float64()
	}

	// From_resource: the second Mint at the same instant is a repeated timestamp, mints nothing
	// and consumes neither carry nor draws.
	run := func(extra bool) (float64, int) {
		v := causalApps(t, causalResources, intakeApp, validatorApp)[1]
		eng := shape.New("UTC", nil)
		v.AdvanceCausal(causalNow, eng)
		first := v.m.Mint(causalNow, 60, eng)
		if len(first) == 0 {
			t.Fatal("no consumer requests")
		}
		if extra {
			if again := v.m.Mint(causalNow, 60, eng); len(again) != 0 {
				t.Fatalf("a repeated timestamp minted %d requests", len(again))
			}
		}
		return eng.Float64(), len(first)
	}
	a, n := run(false)
	b, m := run(true)
	if a != b || n != m {
		t.Errorf("an empty from_resource Mint changed the engine stream or the count: next draw %v vs %v, count %d vs %d", a, b, n, m)
	}

	// Declared traffic: an empty tick (zero rate) still consumes exactly one draw.
	w := buildApp(t, agentFlowCfg(false))
	w.m.traffic = Traffic{}
	eng := shape.New("UTC", nil)
	if got := w.m.Mint(causalNow, 60, eng); len(got) != 0 {
		t.Fatalf("zero-rate declared app minted %d requests", len(got))
	}
	if got, want := eng.Float64(), nextDraw(shape.New("UTC", nil), 1); got != want {
		t.Errorf("declared-traffic empty Mint left the stream at %v, want exactly one draw consumed (%v)", got, want)
	}
}

// Over many ticks at a fractional served rate the consumer mints floor-accumulated whole
// messages: every tick's count is the carry's whole events, and the total never drifts from the
// queue's served volume by a full event.
func TestFromResourceMintTracksServedVolume(t *testing.T) {
	v := causalApps(t, causalResources, intakeApp, validatorApp)[1]
	eng := shape.New("UTC", nil)
	const tick = 7 * time.Second // not a multiple of the model step, so windows straddle grid points
	var minted, served float64
	now := causalNow
	for range 600 {
		now = now.Add(tick)
		v.AdvanceCausal(now, eng)
		win, _ := v.m.causal.model.Window(v.m.causal.fromQueue, now.Add(-tick), now)
		served += win.Served
		minted += float64(len(v.m.Mint(now, tick.Seconds(), eng)))
	}
	if d := served - minted; d < -1e-6 || d >= 1 {
		t.Errorf("served %.6f messages, minted %.0f: the carry should hold back less than one event", served, minted)
	}
}
