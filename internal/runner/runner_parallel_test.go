// SPDX-License-Identifier: AGPL-3.0-only

package runner

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
)

// sleepConstruct blocks in Tick for delay (respecting ctx) — models a slow sink push.
type sleepConstruct struct {
	delay   time.Duration
	started atomic.Int64
}

func (s *sleepConstruct) Kind() string                { return "sleep" }
func (s *sleepConstruct) Signals() []core.SignalClass { return nil }
func (s *sleepConstruct) Interval() time.Duration     { return time.Second }
func (s *sleepConstruct) Tick(ctx context.Context, _ time.Time, _ *core.World) error {
	s.started.Add(1)
	select {
	case <-time.After(s.delay):
	case <-ctx.Done():
	}
	return nil
}

// hungConstruct blocks in Tick until its ctx is cancelled — models a wedged sink push.
type hungConstruct struct{ started atomic.Int64 }

func (h *hungConstruct) Kind() string                { return "hung" }
func (h *hungConstruct) Signals() []core.SignalClass { return nil }
func (h *hungConstruct) Interval() time.Duration     { return time.Second }
func (h *hungConstruct) Tick(ctx context.Context, _ time.Time, _ *core.World) error {
	h.started.Add(1)
	<-ctx.Done()
	return ctx.Err()
}

// injectConstruct appends a construct to a named blueprint's runtime with an explicit interval
// (bypassing the DPM clamp), so timing tests can tick it on the master cadence.
func injectConstruct(t *testing.T, r *Runner, bpName, instName string, c core.Construct, interval time.Duration) {
	t.Helper()
	for _, bp := range r.bps {
		if bp.name == bpName {
			world, _ := r.buildWorld(bp, c.Kind(), instName, c.Signals(), "", nil)
			bp.constructs = append(bp.constructs, &boundConstruct{
				name:      instName,
				construct: c,
				world:     world,
				interval:  interval,
			})
			return
		}
	}
	t.Fatalf("blueprint %q not found for injection", bpName)
}

func timingRunner(t *testing.T, masterTick, tickTimeout time.Duration) *Runner {
	t.Helper()
	subs, scs, wls := &[]*fakeConstruct{}, &[]*fakeConstruct{}, &[]*fakeWorkload{}
	return New(
		Sinks{Metrics: &coretest.MetricCapture{}},
		testRegistry(subs, scs, wls),
		Options{MasterTick: masterTick, MinMetricInterval: 60 * time.Second, TickTimeout: tickTimeout},
	)
}

// TestRunOnceDoesNotCallCycleObserver: the cycle seam is fired only by the parallel Run loop, never
// by the serial RunOnce verification path.
func TestRunOnceDoesNotCallCycleObserver(t *testing.T) {
	r, _, _, _, _, _, _ := newTestRunner(t)
	if err := r.AddBlueprint(buildTestResolved("alpha")); err != nil {
		t.Fatal(err)
	}
	var called atomic.Int64
	r.SetCycleObserver(func(context.Context, string, time.Duration, int) { called.Add(1) })
	if err := r.RunOnce(context.Background(), time.Now()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if got := called.Load(); got != 0 {
		t.Errorf("CycleObserver called %d times by RunOnce; want 0 (RunOnce is the serial path)", got)
	}
}

// TestRunInvokesCycleObserverPerBlueprint: each blueprint's goroutine fires the cycle seam with its
// own name and non-negative dur/dropped. nil observer is exercised by every non-observer test.
func TestRunInvokesCycleObserverPerBlueprint(t *testing.T) {
	if testing.Short() {
		t.Skip("timing test")
	}
	r := timingRunner(t, 25*time.Millisecond, 5*time.Second)
	if err := r.AddBlueprint(buildTestResolved("alpha")); err != nil {
		t.Fatal(err)
	}
	if err := r.AddBlueprint(buildTestResolved("beta")); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	seen := map[string]int{}
	r.SetCycleObserver(func(_ context.Context, bp string, dur time.Duration, dropped int) {
		if dur < 0 || dropped < 0 {
			t.Errorf("cycle %q: negative dur=%v dropped=%d", bp, dur, dropped)
		}
		mu.Lock()
		seen[bp]++
		mu.Unlock()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	_ = r.Run(ctx)

	mu.Lock()
	defer mu.Unlock()
	if seen["alpha"] < 1 || seen["beta"] < 1 {
		t.Fatalf("cycle observer not fired for both blueprints: %v", seen)
	}
}

// TestRunParallelIsolation: a slow construct on "alpha" must NOT delay "beta" — the core promise of
// goroutine-per-blueprint. While alpha is blocked for ~8 tick periods, beta keeps ticking freely;
// and alpha's coalesced ticks are surfaced as dropped≥1.
func TestRunParallelIsolation(t *testing.T) {
	if testing.Short() {
		t.Skip("timing test")
	}
	const master = 25 * time.Millisecond
	r := timingRunner(t, master, 5*time.Second)
	if err := r.AddBlueprint(buildTestResolved("alpha")); err != nil {
		t.Fatal(err)
	}
	if err := r.AddBlueprint(buildTestResolved("beta")); err != nil {
		t.Fatal(err)
	}
	injectConstruct(t, r, "alpha", "slow", &sleepConstruct{delay: 200 * time.Millisecond}, master)

	var mu sync.Mutex
	seen := map[string]int{}
	maxDropped := map[string]int{}
	r.SetCycleObserver(func(_ context.Context, bp string, _ time.Duration, dropped int) {
		mu.Lock()
		seen[bp]++
		if dropped > maxDropped[bp] {
			maxDropped[bp] = dropped
		}
		mu.Unlock()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	_ = r.Run(ctx)

	mu.Lock()
	defer mu.Unlock()
	if seen["beta"] < 4 {
		t.Errorf("beta ticked only %d times — alpha's slow construct stalled it (isolation broken)", seen["beta"])
	}
	if seen["beta"] <= seen["alpha"] {
		t.Errorf("expected beta (free) to tick more than alpha (blocked): beta=%d alpha=%d", seen["beta"], seen["alpha"])
	}
	if maxDropped["alpha"] < 1 {
		t.Errorf("alpha's coalesced ticks were not surfaced as dropped≥1 (got max %d)", maxDropped["alpha"])
	}
}

// TestTickTimeoutBoundsHungConstruct: a construct that blocks forever must be released by the
// per-tick context deadline so the blueprint loop keeps cycling (started≥2 proves it was re-entered,
// i.e. the deadline fired and the loop continued rather than wedging on the first tick).
func TestTickTimeoutBoundsHungConstruct(t *testing.T) {
	if testing.Short() {
		t.Skip("timing test")
	}
	r := timingRunner(t, 25*time.Millisecond, 40*time.Millisecond)
	if err := r.AddBlueprint(buildTestResolved("alpha")); err != nil {
		t.Fatal(err)
	}
	hung := &hungConstruct{}
	injectConstruct(t, r, "alpha", "hung", hung, 25*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_ = r.Run(ctx)

	if got := hung.started.Load(); got < 2 {
		t.Fatalf("hung construct started %d times — the per-tick deadline did not release it (loop wedged)", got)
	}
}
