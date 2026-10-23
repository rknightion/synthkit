// SPDX-License-Identifier: AGPL-3.0-only

package pipelinerun

import (
	"hash/fnv"
	"strconv"
	"time"
)

// EvalFunc resolves a failure mode for a scope at now; shape.Engine.Eval satisfies it.
type EvalFunc func(now time.Time, mode, scope string) (bool, float64)

// maxInFlight bounds the live runs kept per pipeline (a memory bound for very long stalls, not a
// modelled behaviour): further triggers are dropped and counted in PipelineWindow.Dropped.
const maxInFlight = 4096

// Duration jitter: each stage of each run takes Duration * [jitterLo, jitterLo+jitterSpan).
const (
	jitterLo   = 0.85
	jitterSpan = 0.30
)

// StageWindow is what one stage did inside a window (From, To], plus its state at To.
type StageWindow struct {
	Pipeline, Name, Service, Resource string
	// Started, Succeeded and Failed count stage executions inside the window.
	Started, Succeeded, Failed int
	// Running and Pending are the stage's executing and waiting items at To.
	Running, Pending int
	// OldestPendingAgeSec is To minus the arrival of the oldest waiting item; 0 when none waits.
	OldestPendingAgeSec float64
	// BusySec is the execution item-seconds that fell inside the window.
	BusySec float64
	// CompletedDurSec sums the run time of executions that completed inside the window.
	CompletedDurSec float64
}

// PipelineWindow is what one pipeline did inside a window, plus its state at To.
type PipelineWindow struct {
	Name, Environment string
	// Triggered counts runs triggered inside the window; Dropped counts triggers refused by the
	// in-flight bound.
	Triggered, Dropped int
	// Succeeded and Failed count runs reaching a terminal outcome inside the window; the DurSec
	// fields sum their trigger-to-terminal durations.
	Succeeded, Failed             int
	SucceededDurSec, FailedDurSec float64
	// Duration extrema are trigger-to-terminal seconds for the matching outcome in this window.
	// They are zero when its Succeeded or Failed count is zero; the count establishes presence.
	SucceededDurMinSec, SucceededDurMaxSec float64
	FailedDurMinSec, FailedDurMaxSec       float64
	// InFlight counts runs not yet terminal at To.
	InFlight int
	Stages   []StageWindow
}

// Window is one Advance result.
type Window struct {
	From, To  time.Time
	Pipelines []PipelineWindow
}

// StagesFor returns every stage, across pipelines, executed by service on resource. It is the seam
// a consuming construct reads its own resource's facts through.
func (w Window) StagesFor(service, resource string) []StageWindow {
	var out []StageWindow
	for _, p := range w.Pipelines {
		for _, s := range p.Stages {
			if s.Service == service && s.Resource == resource {
				out = append(out, s)
			}
		}
	}
	return out
}

// ForEnvironment returns the pipelines orchestrated by one MWAA environment.
func (w Window) ForEnvironment(env string) []PipelineWindow {
	var out []PipelineWindow
	for _, p := range w.Pipelines {
		if p.Environment != "" && p.Environment == env {
			out = append(out, p)
		}
	}
	return out
}

type item struct {
	run     int64
	trig    time.Time
	arrive  time.Time
	start   time.Time
	due     time.Time
	started bool
}

type pstate struct {
	c        compiled
	phase    time.Duration
	nextRun  int64
	stages   [][]*item
	inflight int
}

// Tracker simulates the declared pipelines for one construct instance. Two trackers built from
// the same seed and declaration and advanced at the same instants hold identical state.
type Tracker struct {
	seed    string
	window  time.Duration
	horizon time.Duration
	ps      []*pstate
	last    time.Time
	started bool
}

// NewTracker validates ps and returns a tracker whose first Advance covers one window.
func NewTracker(seed string, ps []Pipeline, window time.Duration) (*Tracker, error) {
	cs, err := compileAll(ps)
	if err != nil {
		return nil, err
	}
	if window <= 0 {
		window = time.Minute
	}
	t := &Tracker{seed: seed, window: window}
	for _, c := range cs {
		st := &pstate{c: c, stages: make([][]*item, len(c.durs))}
		st.phase = c.offset
		if !c.hasOff {
			mins := int64(c.every / time.Minute)
			st.phase = time.Duration(int64(hash64(seed, c.decl.Name, "phase")%uint64(mins))) * time.Minute
		}
		var total time.Duration
		for _, d := range c.durs {
			total += d
		}
		if h := c.every + time.Duration(float64(total)*(jitterLo+jitterSpan)); h > t.horizon {
			t.horizon = h
		}
		t.ps = append(t.ps, st)
	}
	return t, nil
}

// Empty reports whether the tracker has no pipelines.
func (t *Tracker) Empty() bool { return t == nil || len(t.ps) == 0 }

type stageFlags struct {
	stalled bool
	failInt float64
}

// Advance processes (last, now] (the first call: one window ending at now, after a replay of the
// history before it with no incidents) and returns what happened. Incidents are evaluated once at
// now for every stage scope; eval may be nil.
func (t *Tracker) Advance(now time.Time, eval EvalFunc) Window {
	if !t.started {
		hs := now.Add(-t.window - t.horizon)
		for _, p := range t.ps {
			p.nextRun = firstRunAfter(hs, p.c.every, p.phase)
		}
		t.process(hs, now.Add(-t.window), nil, nil)
		t.last = now.Add(-t.window)
		t.started = true
	}
	from := t.last
	if now.Before(from) {
		from = now
	}
	flags := make([][]stageFlags, len(t.ps))
	for pi, p := range t.ps {
		flags[pi] = make([]stageFlags, len(p.c.durs))
		if eval == nil {
			continue
		}
		for si, s := range p.c.decl.Stages {
			scope := StageScope(p.c.decl.Name, s.Name)
			if on, _ := eval(now, ModeStageStall, scope); on {
				flags[pi][si].stalled = true
			}
			if on, inten := eval(now, ModeRunFailure, scope); on {
				flags[pi][si].failInt = inten
			}
		}
	}
	w := Window{From: from, To: now, Pipelines: make([]PipelineWindow, len(t.ps))}
	t.process(from, now, flags, &w)
	t.last = now
	return w
}

func firstRunAfter(after time.Time, every, phase time.Duration) int64 {
	n := after.UnixNano() - int64(phase)
	k := n / int64(every)
	if n < 0 && n%int64(every) != 0 {
		k--
	}
	return k + 1
}

func (p *pstate) trigTime(k int64) time.Time {
	return time.Unix(0, k*int64(p.c.every)+int64(p.phase)).UTC()
}

func (t *Tracker) stageDur(p *pstate, k int64, si int) time.Duration {
	u := unit(hash64(t.seed, p.c.decl.Name, strconv.FormatInt(k, 10), strconv.Itoa(si), "dur"))
	d := time.Duration(float64(p.c.durs[si]) * (jitterLo + jitterSpan*u))
	d = d.Round(time.Second)
	if d < time.Second {
		d = time.Second
	}
	return d
}

func (t *Tracker) fails(p *pstate, k int64, si int, inten float64) bool {
	if inten <= 0 {
		return false
	}
	return unit(hash64(t.seed, p.c.decl.Name, strconv.FormatInt(k, 10), strconv.Itoa(si), "fail")) < inten
}

// process runs the event loop over (from, to]. flags nil means no incidents; w nil means do not
// record (history replay).
func (t *Tracker) process(from, to time.Time, flags [][]stageFlags, w *Window) {
	flag := func(pi, si int) stageFlags {
		if flags == nil {
			return stageFlags{}
		}
		return flags[pi][si]
	}
	var pw []PipelineWindow
	if w != nil {
		pw = w.Pipelines
		for pi, p := range t.ps {
			pw[pi].Name, pw[pi].Environment = p.c.decl.Name, p.c.decl.Environment
			pw[pi].Stages = make([]StageWindow, len(p.c.durs))
			for si, s := range p.c.decl.Stages {
				pw[pi].Stages[si] = StageWindow{Pipeline: p.c.decl.Name, Name: s.Name, Service: s.Service, Resource: s.Resource}
			}
		}
	}
	start := func(pi, si int, it *item, at time.Time) {
		p := t.ps[pi]
		it.started, it.start, it.due = true, at, at.Add(t.stageDur(p, it.run, si))
		if pw != nil {
			pw[pi].Stages[si].Started++
		}
	}
	busy := func(pi, si int, it *item, end time.Time) {
		if pw == nil {
			return
		}
		b := it.start
		if b.Before(from) {
			b = from
		}
		if end.After(b) {
			pw[pi].Stages[si].BusySec += end.Sub(b).Seconds()
		}
	}

	// A stage that is not stalled starts everything left waiting from earlier windows, FIFO.
	for pi, p := range t.ps {
		for si := range p.stages {
			if flag(pi, si).stalled {
				continue
			}
			for _, it := range p.stages[si] {
				if !it.started {
					at := it.arrive
					if at.Before(from) {
						at = from
					}
					start(pi, si, it, at)
				}
			}
		}
	}

	for {
		// Earliest completion (ties: pipeline, stage, then FIFO order), then earliest trigger.
		bestPI, bestSI, bestIdx := -1, -1, -1
		var bestT time.Time
		for pi, p := range t.ps {
			for si, items := range p.stages {
				for idx, it := range items {
					if !it.started || it.due.After(to) {
						continue
					}
					if bestPI < 0 || it.due.Before(bestT) {
						bestPI, bestSI, bestIdx, bestT = pi, si, idx, it.due
					}
				}
			}
		}
		trigPI := -1
		var trigT time.Time
		for pi, p := range t.ps {
			tk := p.trigTime(p.nextRun)
			if tk.After(to) {
				continue
			}
			if trigPI < 0 || tk.Before(trigT) {
				trigPI, trigT = pi, tk
			}
		}
		if bestPI < 0 && trigPI < 0 {
			break
		}
		if bestPI >= 0 && (trigPI < 0 || !trigT.Before(bestT)) {
			t.complete(bestPI, bestSI, bestIdx, flag, start, busy, pw)
			continue
		}
		p := t.ps[trigPI]
		k := p.nextRun
		p.nextRun++
		if p.inflight >= maxInFlight {
			if pw != nil {
				pw[trigPI].Dropped++
			}
			continue
		}
		p.inflight++
		it := &item{run: k, trig: trigT, arrive: trigT}
		p.stages[0] = append(p.stages[0], it)
		if pw != nil {
			pw[trigPI].Triggered++
		}
		if !flag(trigPI, 0).stalled {
			start(trigPI, 0, it, trigT)
		}
	}

	if pw == nil {
		return
	}
	for pi, p := range t.ps {
		pw[pi].InFlight = p.inflight
		for si, items := range p.stages {
			sw := &pw[pi].Stages[si]
			for _, it := range items {
				if it.started {
					sw.Running++
					busy(pi, si, it, to)
					continue
				}
				sw.Pending++
				if age := to.Sub(it.arrive).Seconds(); age > sw.OldestPendingAgeSec {
					sw.OldestPendingAgeSec = age
				}
			}
		}
	}
}

func (t *Tracker) complete(pi, si, idx int, flag func(int, int) stageFlags,
	start func(int, int, *item, time.Time), busy func(int, int, *item, time.Time), pw []PipelineWindow) {
	p := t.ps[pi]
	it := p.stages[si][idx]
	p.stages[si] = append(p.stages[si][:idx], p.stages[si][idx+1:]...)
	at := it.due
	busy(pi, si, it, at)
	if t.fails(p, it.run, si, flag(pi, si).failInt) {
		p.inflight--
		if pw != nil {
			pw[pi].Stages[si].Failed++
			pw[pi].Failed++
			dur := at.Sub(it.trig).Seconds()
			pw[pi].FailedDurSec += dur
			if pw[pi].Failed == 1 || dur < pw[pi].FailedDurMinSec {
				pw[pi].FailedDurMinSec = dur
			}
			if pw[pi].Failed == 1 || dur > pw[pi].FailedDurMaxSec {
				pw[pi].FailedDurMaxSec = dur
			}
		}
		return
	}
	if pw != nil {
		pw[pi].Stages[si].Succeeded++
		pw[pi].Stages[si].CompletedDurSec += at.Sub(it.start).Seconds()
	}
	if si == len(p.stages)-1 {
		p.inflight--
		if pw != nil {
			pw[pi].Succeeded++
			dur := at.Sub(it.trig).Seconds()
			pw[pi].SucceededDurSec += dur
			if pw[pi].Succeeded == 1 || dur < pw[pi].SucceededDurMinSec {
				pw[pi].SucceededDurMinSec = dur
			}
			if pw[pi].Succeeded == 1 || dur > pw[pi].SucceededDurMaxSec {
				pw[pi].SucceededDurMaxSec = dur
			}
		}
		return
	}
	next := &item{run: it.run, trig: it.trig, arrive: at}
	p.stages[si+1] = append(p.stages[si+1], next)
	if !flag(pi, si+1).stalled {
		start(pi, si+1, next, at)
	}
}

func hash64(parts ...string) uint64 {
	h := fnv.New64a()
	for _, s := range parts {
		_, _ = h.Write([]byte(s))
		_, _ = h.Write([]byte{0})
	}
	return h.Sum64()
}

// unit maps a hash to [0, 1).
func unit(h uint64) float64 { return float64(h>>11) / float64(uint64(1)<<53) }
