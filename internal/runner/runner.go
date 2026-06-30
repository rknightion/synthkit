// SPDX-License-Identifier: AGPL-3.0-only

// Package runner is the composition root: it instantiates each blueprint's
// bill-of-materials through the explicit registry, builds the per-blueprint ledger and
// scoped sink writers, and drives the two-cadence scheduler (ARCHITECTURE §1/§4).
// This replaces the source generator's global self-registration + per-tick scenario
// gating + central policy matrix — there is no "if blueprint == X" anywhere below this
// line or anywhere above it.
package runner

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"log"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rknightion/synthkit/internal/blueprint"
	"github.com/rknightion/synthkit/internal/construct/fleetmgmt"
	"github.com/rknightion/synthkit/internal/control"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/failuremode"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/fleet"
	"github.com/rknightion/synthkit/internal/fleethook"
	"github.com/rknightion/synthkit/internal/ledger"
	"github.com/rknightion/synthkit/internal/scale"
	"github.com/rknightion/synthkit/internal/shape"
)

// Sinks is the shared raw sink set (one per signal class) every blueprint writes
// through. RUM is optional (nil = workloads declaring rum get a load-time warning and
// run without it). Profiles is optional (nil = PyroscopeProfiles lane is inert).
type Sinks struct {
	Metrics  core.MetricWriter
	Logs     core.LogWriter
	Traces   core.TraceWriter
	RUM      core.RUMSink
	Profiles core.PyroscopeWriter
	// OTLPMetrics is the optional native-OTLP metrics lane (nil = inert). Reuses the OTLP gateway.
	OTLPMetrics core.OTLPMetricWriter
	// Sigil is the optional sigil AI-Observability generation-ingest lane (nil = inert; the
	// aiagent workload's generation lane no-ops while traces/metrics still flow).
	Sigil core.SigilWriter
}

// Options tunes the scheduler.
type Options struct {
	MasterTick        time.Duration // ledger mint + ProjectBatch cadence (default 5s)
	MinMetricInterval time.Duration // DPM floor for metric lanes (default 60s)
	TickTimeout       time.Duration // optional per-blueprint per-tick backstop (0 = disabled; per-sink HTTP timeouts already bound individual pushes)

	// Fleet is the Fleet Management registration config. The zero value (empty FMURL) DISABLES
	// registration: fleet_management collectors then emit metrics only. When FMURL is set, Run
	// launches one fleet.Controller per blueprint that declares a fleet_management construct,
	// tied to Run's ctx/waitgroup for clean shutdown (unregister-on-cancel). Set by main.go.
	Fleet fleet.Config

	// Delivery-queue tunables (internal/sink/queue): sink delivery is decoupled from the tick
	// via a bounded in-memory queue drained by batched background senders (I41). Defaults are
	// applied in defaults() when zero.
	SendShards        int           // parallel ordered senders per sink (SEND_SHARDS, default 8)
	SendBatchMax      int           // items per send (SEND_BATCH_MAX, default 5000)
	SendDeadline      time.Duration // partial-batch flush deadline (SEND_BATCH_DEADLINE, default 5s)
	SendCapacity      int           // total buffered items before backpressure (SEND_QUEUE_CAPACITY, default 200000)
	SendDrainDeadline time.Duration // RunOnce flush / shutdown drain bound (SEND_DRAIN_DEADLINE, default 30s)
}

// TickFunc is the runner's stdlib-only self-observability seam (the scheduler counterpart of
// pushhook.Observer). It wraps one instance Tick/ProjectBatch call: an implementation may start a
// trace span + record duration/outcome, but MUST run fn(ctx) exactly once and return its error
// UNCHANGED. blueprint/kind/name identify the instance. nil ⇒ the runner calls fn directly, so the
// composition root's scheduler never links the OTel SDK. See selfobs.ObserveTick.
type TickFunc func(ctx context.Context, blueprint, kind, name string, fn func(context.Context) error) error

// CycleFunc is the runner's stdlib-only per-blueprint cycle-observability seam (the master-cycle
// counterpart of TickFunc). Run calls it once per completed per-blueprint master cycle with the
// blueprint name, the wall-clock duration of that cycle's tick work, and the number of master-ticker
// fires that coalesced (were dropped) since the previous cycle. Set once at startup via
// SetCycleObserver before Run; nil ⇒ no-op, so the scheduler never links the OTel SDK. See
// selfobs.ObserveCycle.
type CycleFunc func(ctx context.Context, blueprint string, dur time.Duration, dropped int)

func (o *Options) defaults() {
	if o.MasterTick <= 0 {
		o.MasterTick = 5 * time.Second
	}
	if o.MinMetricInterval <= 0 {
		o.MinMetricInterval = 60 * time.Second
	}
	if o.SendDrainDeadline <= 0 {
		o.SendDrainDeadline = 30 * time.Second
	}
	// The other Send* fields (Shards/BatchMax/Deadline/Capacity) are normalised by
	// queue.Options.withDefaults inside buildQueues; 0 there means "use the queue default".
	// TickTimeout is intentionally NOT defaulted: 0 = disabled. See the Options field comment.
}

type boundConstruct struct {
	name      string
	kind      string
	construct core.Construct
	world     *core.World
	interval  time.Duration
	nextDue   time.Time
	inv       *constructInv
}

type boundWorkload struct {
	workload core.Workload
	kind     string
	world    *core.World
	interval time.Duration
	nextDue  time.Time
	inv      *constructInv
}

// bpRuntime is one blueprint's running state: its own shape engine (its incidents +
// timezone), its own ledger, its own series budget.
type bpRuntime struct {
	name       string
	label      string
	source     string
	meta       blueprint.Metadata          // blueprint-level human-facing annotation (UI only)
	envMeta    []blueprint.ResolvedEnvMeta // per-env metadata for the UI (decl order)
	eng        *shape.Engine
	ledger     *ledger.Ledger
	budget     *seriesBudget
	constructs []*boundConstruct
	workloads  []*boundWorkload

	// scenarios + targets are the blueprint's resolved incident-model inventory: the Live closure
	// expands active scenarios + axis-wildcard scopes against them. scale carries the live scaling
	// overrides (control plane) every construct/workload World reads via w.Scaling.
	scenarios []blueprint.ResolvedScenario
	targets   []blueprint.Target
	scale     *scale.Source

	// fleetRoster is the STANDALONE fake-collector identity set for this blueprint's
	// collectors_per_os fleet_management instances (empty if none). Byte-identical to the
	// collector_id/instance labels the construct emits. Derived once at AddBlueprint via
	// fleetmgmt.Roster (mirrors fleetmgmt.Build's seed/cluster extraction).
	fleetRoster []fleet.Collector

	// fleetMirror are dynamic per-cluster k8s-monitoring roster contributors: each recomputes
	// its collectors from the cluster's CURRENT live node set via the SAME *scale.Source fed into
	// World.Scaling, so emitted collector_id labels and FM registrations stay byte-identical and
	// churn together as nodes scale. Empty unless a cluster sets k8s_monitoring.fleet_management.
	fleetMirror []fleet.RosterProvider

	// incidents are this blueprint's DECLARED schedule strings (res.Incidents), retained for
	// GET /control/incidents. rtEng is a dedicated shape engine holding only the operator-created
	// RUNTIME incidents; rebuilt by ApplyControl, consulted by the Live closure + ControlIncidents.
	// atomic.Pointer because the per-blueprint tick goroutine reads it while ApplyControl swaps it.
	incidents []string
	rtEng     atomic.Pointer[shape.Engine]
}

// fleetProvider composes the blueprint's standalone roster + dynamic mirror contributors into one
// provider. Returns nil when the blueprint declares no fleet collectors (so Run starts no controller).
func (bp *bpRuntime) fleetProvider() fleet.RosterProvider {
	if len(bp.fleetRoster) == 0 && len(bp.fleetMirror) == 0 {
		return nil
	}
	roster, mirror := bp.fleetRoster, bp.fleetMirror
	return func() []fleet.Collector {
		out := append([]fleet.Collector(nil), roster...)
		for _, m := range mirror {
			out = append(out, m()...)
		}
		return out
	}
}

// axisScopes lists the target names of an axis (for the Live closure's "<axis>:*" expansion).
func (bp *bpRuntime) axisScopes(axis failuremode.Axis) []string {
	var out []string
	for _, t := range bp.targets {
		if t.Axis == axis {
			out = append(out, t.Name)
		}
	}
	return out
}

// Runner drives N blueprints against one sink set.
type Runner struct {
	sinks Sinks
	reg   *core.Registry
	opts  Options
	bps   []*bpRuntime

	// ctl is the latest applied control snapshot (atomic — read by the per-engine
	// Live hooks on the hot path and by the enabled() gate).
	ctl atomic.Pointer[control.State]

	// tickObs wraps every instance Tick/ProjectBatch for self-observability (nil = no-op direct
	// call). Set once at startup via SetTickObserver before Run/RunOnce; never mutated concurrently.
	tickObs TickFunc

	// cycleObs observes each completed per-blueprint master cycle (nil = no-op). Set once at startup
	// via SetCycleObserver before Run; read concurrently by the per-blueprint goroutines, never
	// mutated after Run starts (same set-once contract as tickObs).
	cycleObs CycleFunc

	// queues are the per-signal delivery queues that decouple sink delivery from the tick (I41).
	// Built once in New (before AddBlueprint), wired into each instance World by buildWorld, and
	// started/drained by Run / RunOnce.
	queues queueSet

	// shapeWarnings accumulates per-blueprint shape-engine warnings (incident-schedule entries the
	// engine skipped during AddBlueprint) so the composition root can surface them as diagnostics.
	shapeWarnings []ShapeWarning
}

// ShapeWarning is one shape-engine incident-schedule warning, tagged with its blueprint.
type ShapeWarning struct {
	Blueprint string
	Message   string
}

// ShapeWarnings returns the incident-schedule warnings collected across all added blueprints.
func (r *Runner) ShapeWarnings() []ShapeWarning { return r.shapeWarnings }

// New builds a runner. The registry is the explicit catalog assembled by the wiring
// pass (catalog.go).
func New(sinks Sinks, reg *core.Registry, opts Options) *Runner {
	opts.defaults()
	r := &Runner{sinks: sinks, reg: reg, opts: opts}
	r.buildQueues()
	return r
}

// AddBlueprint instantiates a resolved blueprint's BoM. Loud on unknown kinds,
// duplicate names, or Build errors.
func (r *Runner) AddBlueprint(res *blueprint.Resolved) error {
	for _, bp := range r.bps {
		if bp.name == res.Name {
			return fmt.Errorf("runner: blueprint %q already added", res.Name)
		}
	}
	var eng *shape.Engine
	if len(res.Regions) > 0 {
		regions := make([]shape.Region, len(res.Regions))
		for i, r := range res.Regions {
			regions[i] = shape.Region{Name: r.Name, Timezone: r.Timezone, Weight: r.Weight}
		}
		eng = shape.NewWithRegions(regions, res.Incidents)
	} else {
		eng = shape.New(res.Timezone, res.Incidents)
	}
	for _, w := range eng.Warnings() {
		r.shapeWarnings = append(r.shapeWarnings, ShapeWarning{Blueprint: res.Name, Message: w})
	}
	led := ledger.New(eng, 0, 0)
	led.SetTickSeconds(r.opts.MasterTick.Seconds())
	bp := &bpRuntime{
		name:      res.Name,
		label:     res.Label,
		source:    res.Source,
		meta:      res.Metadata,
		envMeta:   res.Environments,
		eng:       eng,
		ledger:    led,
		budget:    newSeriesBudget(res.SeriesBudget),
		scenarios: res.Scenarios,
		targets:   res.Targets,
		scale:     scale.New(),
		incidents: res.Incidents,
	}

	// Control-plane Live seam (assigned AFTER bp exists — the closure captures bp). It unions, for
	// one mode: (a) the ad-hoc failure knob for that mode, and (b) every effect naming that mode in
	// an ACTIVE scenario defined in THIS blueprint. Either source's scope may be a bare target name,
	// "" (un-scoped — matches the mode's sole axis via scopeMatch), or "<axis>:*" (expanded here
	// against bp.targets). The scheduled incident windows are layered separately by the engine.
	bp.eng.Live = func(mode string) []shape.LiveFailure {
		st := r.ctl.Load()
		if st == nil {
			return nil
		}
		var out []shape.LiveFailure
		expand := func(scope string, inten float64) {
			if strings.HasSuffix(scope, ":*") {
				axis := failuremode.Axis(strings.TrimSuffix(scope, ":*"))
				for _, name := range bp.axisScopes(axis) {
					out = append(out, shape.LiveFailure{Enabled: true, Intensity: inten, Scope: name})
				}
				return
			}
			out = append(out, shape.LiveFailure{Enabled: true, Intensity: inten, Scope: scope})
		}
		// 1. ad-hoc failure for this mode.
		if f, ok := st.Failures[mode]; ok && f.Enabled {
			expand(f.Scope, f.Intensity)
		}
		// 2. active scenarios defined in THIS blueprint.
		for _, sc := range bp.scenarios {
			if !slices.Contains(st.ActiveScenarios, bp.name+"/"+sc.Name) {
				continue
			}
			for _, e := range sc.Effects {
				if e.Mode != mode {
					continue
				}
				inten := e.Intensity
				if inten <= 0 {
					inten = 1.0
				}
				expand(e.Target, inten) // "" → un-scoped match (single-axis mode)
			}
		}
		// 3. runtime incidents (operator-created, this blueprint). Their windows live in rtEng;
		// for each DISTINCT target among this blueprint's runtime incidents of this mode, ask rtEng
		// whether the window is active now. rtEng.Eval scope-matches internally, so calling it with
		// the incident's own target returns that window's (active,intensity); we then emit a
		// LiveFailure on that scope for the outer Eval to union.
		if rt := bp.rtEng.Load(); rt != nil {
			// time.Now() is used here rather than the tick's now because the Live func(mode string)
			// seam (frozen in the shape package) has no time parameter. In practice this agrees with
			// tick-now to within milliseconds — this is an intentional limitation of the no-shape-edit
			// design.
			now := time.Now()
			seen := map[string]bool{}
			for _, ri := range st.RuntimeIncidents {
				if ri.Blueprint != bp.name || ri.Mode != mode || seen[ri.Target] {
					continue
				}
				seen[ri.Target] = true
				if active, inten := rt.Eval(now, mode, ri.Target); active {
					out = append(out, shape.LiveFailure{Enabled: true, Intensity: inten, Scope: ri.Target})
				}
			}
		}
		return out
	}

	for _, ci := range res.Constructs {
		reg, ok := r.reg.Construct(ci.Kind)
		if !ok {
			return fmt.Errorf("runner: blueprint %q construct %q: kind %q not registered", res.Name, ci.Name, ci.Kind)
		}
		c, err := reg.Build(ci.Config, ci.Fixtures)
		if err != nil {
			return fmt.Errorf("runner: blueprint %q construct %q (%s): %w", res.Name, ci.Name, ci.Kind, err)
		}
		// Fleet Management: derive the FM-registration roster from the same seed/cluster/perOS the
		// fleetmgmt construct uses (fleetmgmt.Build), so the registered identities are byte-identical
		// to the emitted collector_id/instance labels. The roster derivation is pure; the controller
		// that consumes it is launched lazily in Run only when Options.Fleet.FMURL is set.
		if ci.Kind == fleetmgmt.Kind {
			fcfg, ok := ci.Config.(*fleetmgmt.Config)
			if !ok {
				return fmt.Errorf("runner: blueprint %q construct %q: fleet_management config %T, want *fleetmgmt.Config", res.Name, ci.Name, ci.Config)
			}
			seed, cluster := "", ""
			var cl *fixture.Cluster
			if ci.Fixtures != nil {
				seed = ci.Fixtures.Seed
				cl = ci.Fixtures.Cluster
				if cl != nil {
					cluster = cl.Name
				}
			}
			if cl != nil && cl.K8sMonitoring.Enabled && cl.K8sMonitoring.FleetManagement {
				// k8s-mirror instance: recompute the roster from the cluster's live node set each
				// call, via the SAME *scale.Source the construct reads (bp.scale → World.Scaling),
				// so registration and metrics agree byte-for-byte and churn together.
				clCaptured, src := cl, bp.scale
				bp.fleetMirror = append(bp.fleetMirror, func() []fleet.Collector {
					nodes := fixture.LiveNodes(clCaptured, src.Count)
					return fleetmgmt.K8sRoster(clCaptured.Seed, "", clCaptured.Name, "", nodes, clCaptured.K8sMonitoring)
				})
			} else {
				// Standalone (non-k8s) machine roster — fixed at AddBlueprint.
				bp.fleetRoster = append(bp.fleetRoster, fleetmgmt.Roster(seed, cluster, fcfg.CollectorsPerOS)...)
			}
		}

		label := ""
		if reg.Scope == core.ScopeBlueprint {
			label = res.Label
		}
		world, inv := r.buildWorld(bp, ci.Kind, ci.Name, c.Signals(), label, nil)
		bp.constructs = append(bp.constructs, &boundConstruct{
			name:      ci.Name,
			kind:      ci.Kind,
			construct: c,
			world:     world,
			inv:       inv,
			interval:  r.clampInterval(res.Name, ci.Name, c.Interval()),
		})
	}

	for _, wi := range res.Workloads {
		wreg, ok := r.reg.Workload(wi.Kind)
		if !ok {
			return fmt.Errorf("runner: blueprint %q workload %q: kind %q not registered", res.Name, wi.Name, wi.Kind)
		}
		var rum core.RUMSink
		if wi.RUM {
			if r.sinks.RUM != nil {
				// Hand the workload the decoupled delivery queue (built in buildQueues when a RUM
				// sink is present), not the raw Faro sink: ProjectBatch enqueues and a background
				// sender ships, so a transient Faro-collector POST failure becomes an async push
				// error/retry instead of failing the construct tick (mirrors the other four sinks).
				rum = r.queues.RUM
			} else {
				log.Printf("runner: blueprint %q workload %q declares rum but no Faro credentials are configured — RUM disabled", res.Name, wi.Name)
			}
		}
		w, err := wreg.Build(wi.Config, core.Binding{
			Name:      wi.Name,
			Seed:      res.Name + ":" + wi.Name,
			Replicas:  wi.Replicas,
			Env:       wi.Env,
			Cluster:   wi.Cluster,
			Calls:     wi.Calls,
			Databases: wi.Databases,
			RUM:       rum,
		})
		if err != nil {
			return fmt.Errorf("runner: blueprint %q workload %q (%s): %w", res.Name, wi.Name, wi.Kind, err)
		}
		led.AddMinter(w.Minter())
		wlabel := res.Label // workload signals are blueprint-scoped by default…
		if wreg.Scope == core.ScopeSubstrate {
			wlabel = "" // …except substrate-scoped workloads (ai_agent/sigil): no blueprint label
		}
		wworld, winv := r.buildWorld(bp, wi.Kind, wi.Name, w.Signals(), wlabel, led)
		bp.workloads = append(bp.workloads, &boundWorkload{
			workload: w,
			kind:     wi.Kind,
			world:    wworld,
			inv:      winv,
			interval: r.clampInterval(res.Name, wi.Name, w.Interval()),
		})
	}

	r.bps = append(r.bps, bp)
	return nil
}

// buildWorld wires the writers an instance declared — nothing more (the framework
// only asks for what Signals() promises). label "" = substrate (no stamping).
// kind/name identify the instance for the internal emission inventory (never stamped on the wire).
func (r *Runner) buildWorld(bp *bpRuntime, kind, name string, signals []core.SignalClass, label string, led *ledger.Ledger) (*core.World, *constructInv) {
	inv := newConstructInv(bp.name, kind, name)
	w := &core.World{Shape: bp.eng, Ledger: led, Scaling: bp.scale}
	// Wire the stamped writers at the delivery QUEUES (decorators of the raw sinks), not the
	// raw sinks — so Write enqueues and background senders ship (I41). Gated on the raw sink's
	// presence (the queue is non-nil iff its raw sink is).
	if slices.Contains(signals, core.Metrics) && r.sinks.Metrics != nil {
		w.Metrics = &stampedMetrics{sink: r.queues.Metrics, label: label, bp: bp.name, budget: bp.budget, inv: inv}
	}
	if slices.Contains(signals, core.Logs) && r.sinks.Logs != nil {
		w.Logs = &stampedLogs{sink: r.queues.Logs, label: label, inv: inv}
	}
	if slices.Contains(signals, core.Traces) && r.sinks.Traces != nil {
		w.Traces = &stampedTraces{sink: r.queues.Traces, label: label, inv: inv}
	}
	if slices.Contains(signals, core.PyroscopeProfiles) && r.sinks.Profiles != nil {
		w.Pyroscope = &stampedProfiles{sink: r.queues.Profiles, label: label}
	}
	if slices.Contains(signals, core.OTLPMetrics) && r.sinks.OTLPMetrics != nil {
		w.OTLPMetrics = &stampedOTLPMetrics{sink: r.queues.OTLPMetrics, label: label}
	}
	if slices.Contains(signals, core.Sigil) && r.sinks.Sigil != nil {
		// Substrate-scoped: no blueprint stamping (the ingest proto has no label field). The queue
		// itself satisfies core.SigilWriter; ship batches straight through.
		w.Sigil = r.queues.Sigil
	}
	return w, inv
}

func (r *Runner) clampInterval(bp, name string, iv time.Duration) time.Duration {
	if iv < r.opts.MinMetricInterval {
		log.Printf("runner: blueprint %q instance %q interval %v below the %v DPM floor — clamped", bp, name, iv, r.opts.MinMetricInterval)
		return r.opts.MinMetricInterval
	}
	return iv
}

// phaseOffset returns a deterministic per-instance start offset in [0, interval) derived from the
// instance name. Without it Run seeds every instance nextDue=now, so instances sharing the DPM-floor
// interval re-synchronise onto the SAME master tick every interval and run a whole window's heavy work
// in a single cycle — overrunning MasterTick and coalescing (dropping) the next fires. Spreading the
// first due time across the interval keeps per-tick work flat and mirrors real fleets, which never
// scrape in lockstep. Name-hashed (not rand) so cadence is reproducible and testable.
func phaseOffset(name string, interval time.Duration) time.Duration {
	if interval <= 0 {
		return 0
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(name))
	return time.Duration(h.Sum64() % uint64(interval))
}

// seedPhases sets each instance's first nextDue to now + its phase offset. Called once at the top of
// Run so the live loop starts already de-synchronised. RunOnce does not consult nextDue (it ticks
// every instance unconditionally), so the -once -dump inventory is unaffected.
func (r *Runner) seedPhases(now time.Time) {
	for _, bp := range r.bps {
		for _, bc := range bp.constructs {
			bc.nextDue = now.Add(phaseOffset(bc.name, bc.interval))
		}
		for _, bw := range bp.workloads {
			bw.nextDue = now.Add(phaseOffset(bw.workload.Name(), bw.interval))
		}
	}
}

// MasterTick runs one fast tick for every blueprint: mint the batch, hand each
// workload EXACTLY its own requests (exactly-once trace/log projection — I10).
// Used by RunOnce and external callers; Run uses the per-blueprint masterTickOne on its own goroutines.
func (r *Runner) MasterTick(ctx context.Context, now time.Time) error {
	// Ensure the delivery-queue senders are running: MasterTick enqueues (workload projection),
	// and a standalone caller (outside Run/RunOnce) would otherwise block on a full buffer with
	// nothing draining it. Idempotent — the live loop and RunOnce have already started them.
	r.startQueues()
	var errs []error
	for _, bp := range r.bps {
		if !r.enabled(bp.name) {
			continue
		}
		if err := r.masterTickOne(ctx, bp, now); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// masterTickOne mints ONE blueprint's batch and hands each of its workloads EXACTLY its own requests
// (exactly-once trace/log projection — I10). It is the single-blueprint slice of MasterTick: called
// serially by MasterTick (RunOnce) and concurrently — one goroutine per blueprint — by Run. The
// caller gates on enabled() before calling.
func (r *Runner) masterTickOne(ctx context.Context, bp *bpRuntime, now time.Time) error {
	batch := bp.ledger.Mint(now)
	if len(batch) == 0 {
		return nil
	}
	byWorkload := map[string][]*ledger.Request{}
	for _, req := range batch {
		byWorkload[req.Workload] = append(byWorkload[req.Workload], req)
	}
	var errs []error
	for _, bw := range bp.workloads {
		own := byWorkload[bw.workload.Name()]
		if len(own) == 0 {
			continue
		}
		err := r.observeTick(ctx, bp.name, bw.kind, bw.workload.Name(), func(ctx context.Context) error {
			return bw.workload.ProjectBatch(ctx, now, bw.world, own)
		})
		if err != nil {
			errs = append(errs, fmt.Errorf("blueprint %q workload %q ProjectBatch: %w", bp.name, bw.workload.Name(), err))
		}
	}
	return errors.Join(errs...)
}

// RunOnce drives one complete cycle at now (the -once verification mode): budget
// reset, one master tick, one metric Tick for every instance.
func (r *Runner) RunOnce(ctx context.Context, now time.Time) error {
	var errs []error
	// MasterTick (called below) starts the delivery-queue senders; Flush — not Drain — runs at
	// the end so the queue stays reusable across repeated RunOnce calls.
	for _, bp := range r.bps {
		bp.budget.reset()
	}
	if err := r.MasterTick(ctx, now); err != nil {
		errs = append(errs, err)
	}
	for _, bp := range r.bps {
		if !r.enabled(bp.name) {
			continue
		}
		for _, bc := range bp.constructs {
			if !r.constructEnabled(bp.name, bc.kind, bc.name) {
				continue
			}
			err := r.observeTick(ctx, bp.name, bc.kind, bc.name, func(ctx context.Context) error {
				return bc.construct.Tick(ctx, now, bc.world)
			})
			if err != nil {
				errs = append(errs, fmt.Errorf("blueprint %q construct %q Tick: %w", bp.name, bc.name, err))
			}
		}
		for _, bw := range bp.workloads {
			bw.world.EmitSpanMetrics = r.spanMetricsEnabled(bp.name)
			err := r.observeTick(ctx, bp.name, bw.kind, bw.workload.Name(), func(ctx context.Context) error {
				return bw.workload.Tick(ctx, now, bw.world)
			})
			if err != nil {
				errs = append(errs, fmt.Errorf("blueprint %q workload %q Tick: %w", bp.name, bw.workload.Name(), err))
			}
		}
	}
	// Synchronously flush all enqueued series before returning so -once delivery is complete
	// and deterministic. Flush keeps the queues reusable and surfaces sink push errors (which
	// no longer propagate through the now-async stamped Write).
	flushCtx, cancel := context.WithTimeout(context.Background(), r.opts.SendDrainDeadline)
	defer cancel()
	if err := r.Flush(flushCtx); err != nil {
		errs = append(errs, fmt.Errorf("delivery flush: %w", err))
	}
	return errors.Join(errs...)
}

// Run drives the live loop until ctx is done. Each blueprint runs on its OWN goroutine with its own
// master + budget-reset tickers, so a slow or hung sink push on one blueprint can never delay
// another's cadence (true per-blueprint isolation — blueprints share nothing but the
// concurrency-safe sinks). Per-tick work is bounded by Options.TickTimeout; coalesced (dropped)
// ticks and per-cycle wall-clock are surfaced via the CycleFunc seam. RunOnce stays serial.
func (r *Runner) Run(ctx context.Context) error {
	r.seedPhases(time.Now())

	var wg sync.WaitGroup
	// Start the delivery queues FIRST so their senders are draining before any blueprint tick
	// enqueues. Each q.Run blocks until ctx is done, then drains within SendDrainDeadline, so
	// wg.Wait below also waits for the final flush on shutdown.
	for _, q := range r.eachQueue() {
		wg.Add(1)
		go func(q drainable) { defer wg.Done(); q.Run(ctx, r.opts.SendDrainDeadline) }(q)
	}
	for _, bp := range r.bps {
		wg.Add(1)
		go r.blueprintLoop(ctx, bp, &wg)
	}
	r.startFleetControllers(ctx, &wg)
	wg.Wait()
	return ctx.Err()
}

// startFleetControllers launches one fleet.Controller per blueprint that declared a non-empty
// fleet_management roster, registering its fake collectors with the FM API and heartbeating them
// until ctx is cancelled (each Start then unregisters and returns). Each controller runs on the
// same waitgroup as the blueprint loops so Run blocks until they have all cleanly shut down.
//
// When Options.Fleet.FMURL is empty, registration is disabled: the collectors still emit metrics
// (the construct ticks regardless), so this logs once and starts nothing — metrics-only mode.
func (r *Runner) startFleetControllers(ctx context.Context, wg *sync.WaitGroup) {
	if r.opts.Fleet.FMURL == "" {
		// Log the metrics-only fallback at most ONCE (not per blueprint) — the message is a
		// global "you declared fleet but configured no FM endpoint" hint, so the first
		// roster-bearing blueprint triggers it and we return without starting any controller.
		for _, bp := range r.bps {
			if bp.fleetProvider() != nil {
				log.Printf("runner: fleet_management declared but GC_FM_URL unset — collectors emit metrics only (no FM registration)")
				return
			}
		}
		return
	}
	for _, bp := range r.bps {
		provider := bp.fleetProvider()
		if provider == nil {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			fleet.NewController(r.opts.Fleet).StartDynamic(ctx, provider)
		}()
	}
}

// blueprintLoop is the goroutine body for one blueprint. It owns bp exclusively for the lifetime of
// Run (no other goroutine touches bp's runtime state), so its construct/workload nextDue mutations
// and per-blueprint budget reset need no locking. It stops when ctx is cancelled.
func (r *Runner) blueprintLoop(ctx context.Context, bp *bpRuntime, wg *sync.WaitGroup) {
	defer wg.Done()

	master := time.NewTicker(r.opts.MasterTick)
	defer master.Stop()
	budgetReset := time.NewTicker(r.opts.MinMetricInterval)
	defer budgetReset.Stop()

	lastTick := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case <-budgetReset.C:
			bp.budget.reset()
		case t := <-master.C:
			if !r.enabled(bp.name) {
				lastTick = t
				continue
			}
			// Dropped-tick detection: a time.Ticker silently coalesces fires when a cycle overruns
			// the period. The gap since the previous handled tick reveals how many were dropped — the
			// failure mode that silently undercounts synthetic volume (each missed tick = a missed Mint).
			dropped := 0
			if r.opts.MasterTick > 0 {
				if d := int(t.Sub(lastTick)/r.opts.MasterTick) - 1; d > 0 {
					dropped = d
				}
			}
			lastTick = t

			start := time.Now()
			// Each sink push is already bounded by its own http.Client timeout (15s). TickTimeout, when
			// set (>0), adds an OPTIONAL coarse whole-blueprint-tick backstop. It is off by default
			// because a blueprint with many constructs can legitimately exceed any fixed budget under
			// remote slowness, and cancelling mid-burst would DROP valid data — an overrun is surfaced
			// via the dropped-tick metric, not by cancelling the work.
			tickCtx := ctx
			cancel := func() {}
			if r.opts.TickTimeout > 0 {
				var c context.CancelFunc
				tickCtx, c = context.WithTimeout(ctx, r.opts.TickTimeout)
				cancel = c
			}
			// Per-instance ProjectBatch errors are surfaced through the tick observer (structured
			// tick_error log → self-obs + the in-process health store read by /control/health); the
			// live loop does not re-log the aggregate to keep on-box output minimal.
			_ = r.masterTickOne(tickCtx, bp, t)
			r.tickBlueprintInstances(tickCtx, bp, t)
			cancel()

			// Report on the parent ctx (not tickCtx) so the observer is not cancelled by a tick deadline.
			r.callCycleObs(ctx, bp.name, time.Since(start), dropped)
		}
	}
}

// tickBlueprintInstances runs the due construct + workload metric Ticks for one blueprint (the
// per-instance Interval gate, ≥ the DPM floor). It mutates each instance's nextDue — safe because
// blueprintLoop owns bp on a single goroutine.
func (r *Runner) tickBlueprintInstances(ctx context.Context, bp *bpRuntime, t time.Time) {
	for _, bc := range bp.constructs {
		if !r.constructEnabled(bp.name, bc.kind, bc.name) {
			continue
		}
		if !t.Before(bc.nextDue) {
			bc.nextDue = t.Add(bc.interval)
			// Error surfaced via the tick observer (structured tick_error → self-obs + health store);
			// not re-logged here to keep on-box output minimal.
			_ = r.observeTick(ctx, bp.name, bc.kind, bc.name, func(ctx context.Context) error {
				return bc.construct.Tick(ctx, t, bc.world)
			})
		}
	}
	for _, bw := range bp.workloads {
		if !t.Before(bw.nextDue) {
			bw.nextDue = t.Add(bw.interval)
			bw.world.EmitSpanMetrics = r.spanMetricsEnabled(bp.name)
			// Error surfaced via the tick observer (structured tick_error → self-obs + health store);
			// not re-logged here to keep on-box output minimal.
			_ = r.observeTick(ctx, bp.name, bw.kind, bw.workload.Name(), func(ctx context.Context) error {
				return bw.workload.Tick(ctx, t, bw.world)
			})
		}
	}
}

// Blueprints lists the added blueprint names (diagnostics).
func (r *Runner) Blueprints() []string {
	out := make([]string, 0, len(r.bps))
	for _, bp := range r.bps {
		out = append(out, bp.name)
	}
	return out
}

// ControlSchema builds the blueprint-derived control schema (it makes *Runner satisfy
// control.SchemaSource). It unions the registry's failure-mode vocabulary, every blueprint's
// addressable-target inventory (with current effective scaling counts), and every defined scenario
// with its current activation state. v1 scales ONLY workloads, keyed by bare target name — no
// scaleKey indirection. Read-only: it never mutates runner state.
func (r *Runner) ControlSchema() control.Schema {
	st := r.ctl.Load()
	sc := control.Schema{
		VolumeMultiplier: control.Descriptor{Key: "volume_multiplier", Type: "float", Default: 1.0, Min: 0, Max: 100,
			Help: "master load knob — scales ALL synthetic volume (metrics + correlated traces/logs) coherently"},
	}
	seenMode := map[string]bool{}
	for _, m := range r.reg.AllFailureModes() {
		k := m.Name + "|" + string(m.Axis)
		if seenMode[k] {
			continue
		}
		seenMode[k] = true
		sc.Modes = append(sc.Modes, control.ModeInfo{Name: m.Name, Axis: string(m.Axis), Help: m.Help})
	}
	kindSeen := map[string]bool{}
	for _, bp := range r.bps {
		sc.Blueprints = append(sc.Blueprints, bp.name)
		bmi := control.BlueprintMetaInfo{Name: bp.name, MetaFields: toMetaFields(bp.meta)}
		for _, em := range bp.envMeta {
			bmi.Environments = append(bmi.Environments, control.EnvMetaInfo{Name: em.Name, MetaFields: toMetaFields(em.Metadata)})
		}
		sc.BlueprintMeta = append(sc.BlueprintMeta, bmi)
		for _, t := range bp.targets {
			ti := control.TargetInfo{Blueprint: bp.name, Name: t.Name, Axis: string(t.Axis)}
			if t.Scalable != nil {
				cur := t.Scalable.Default
				if st != nil {
					if v, ok := st.Scaling[bp.name+"/"+t.Name]; ok { // scaling keyed by qualified "blueprint/name"
						cur = v
					}
				}
				ti.Scalable = &control.ScalableInfo{Dimension: t.Scalable.Dimension,
					Min: t.Scalable.Min, Max: t.Scalable.Max, Default: t.Scalable.Default, Current: cur}
			}
			sc.Targets = append(sc.Targets, ti)
		}
		for _, s := range bp.scenarios {
			id := bp.name + "/" + s.Name
			info := control.ScenarioInfo{Blueprint: bp.name, Name: s.Name, Title: s.Title, Summary: s.Summary}
			if st != nil {
				info.Active = slices.Contains(st.ActiveScenarios, id)
			}
			for _, e := range s.Effects {
				info.Effects = append(info.Effects, control.EffectInfo{Mode: e.Mode, Target: e.Target, Intensity: e.Intensity})
			}
			sc.Scenarios = append(sc.Scenarios, info)
		}
		for _, bc := range bp.constructs {
			sc.Constructs = append(sc.Constructs, control.ConstructInfo{
				Blueprint: bp.name, Kind: bc.kind, Name: bc.name,
				Enabled: r.constructEnabled(bp.name, bc.kind, bc.name),
			})
			if !kindSeen[bc.kind] {
				kindSeen[bc.kind] = true
				sc.Kinds = append(sc.Kinds, bc.kind)
			}
		}
	}
	return sc
}

// toMetaFields maps blueprint-package metadata to the control-schema wire shape (UI annotation).
func toMetaFields(m blueprint.Metadata) control.MetaFields {
	return control.MetaFields{
		Description: m.Description,
		Tags:        m.Tags,
		Owner:       m.Owner,
		Links:       m.Links,
		Category:    m.Category,
	}
}

// incidentActiveNow reports whether a single incident's own schedule window is active at now,
// isolated from any live failure/scenario overlay and from sibling windows. It builds a throwaway
// Live-less shape engine holding ONLY this incident's spec (mirrors ValidateRuntimeIncident).
func incidentActiveNow(loc string, spec, mode, scope string, now time.Time) bool {
	return shape.New(loc, []string{spec}).Active(now, mode, scope)
}

// ControlIncidents implements control.IncidentSource: declared incidents (from each blueprint's
// retained schedule strings) plus operator-created runtime incidents (from control state), each
// with an authoritative active_now computed via the relevant shape engine. Read-only.
func (r *Runner) ControlIncidents() []control.IncidentInfo {
	now := time.Now()
	var out []control.IncidentInfo
	for _, bp := range r.bps {
		for _, spec := range bp.incidents {
			mode, scope, inten := splitSpec(spec)
			out = append(out, control.IncidentInfo{
				Source: "declared", Blueprint: bp.name, Mode: mode, Target: scope,
				ScheduleSpec: spec, Intensity: inten,
				ActiveNow: incidentActiveNow(bp.eng.Loc().String(), spec, mode, scope, now),
			})
		}
	}
	if st := r.ctl.Load(); st != nil {
		for _, bp := range r.bps {
			for _, ri := range st.RuntimeIncidents {
				if ri.Blueprint != bp.name {
					continue
				}
				spec := control.ScheduleSpec(ri.Mode, ri.Target, ri.At, ri.For, ri.Intensity)
				out = append(out, control.IncidentInfo{
					Source: "runtime", ID: ri.ID, Blueprint: ri.Blueprint, Mode: ri.Mode, Target: ri.Target,
					At: ri.At, For: ri.For, Intensity: ri.Intensity,
					ScheduleSpec: spec,
					ActiveNow:    incidentActiveNow(bp.eng.Loc().String(), spec, ri.Mode, ri.Target, now),
				})
			}
		}
	}
	return out
}

// ValidateRuntimeIncident implements control.IncidentSource: the blueprint must exist; the mode
// must be declared on the (resolved) target's axis; the target must be "" | a known target name |
// a "<axis>:*" wildcard; and at/for must parse under shape's grammar (checked by building a throwaway
// engine and inspecting its warnings — shape stays the sole grammar authority).
func (r *Runner) ValidateRuntimeIncident(ri control.RuntimeIncident) error {
	var bp *bpRuntime
	for _, b := range r.bps {
		if b.name == ri.Blueprint {
			bp = b
			break
		}
	}
	if bp == nil {
		return fmt.Errorf("unknown blueprint %q", ri.Blueprint)
	}
	if ri.Intensity < 0 || ri.Intensity > 1 {
		return fmt.Errorf("intensity %v out of [0,1]", ri.Intensity)
	}
	vocab := r.reg.AllFailureModes()
	// resolve the target axis: "" (sole-axis mode), "<axis>:*", or a named target.
	switch {
	case ri.Target == "":
		axes := map[failuremode.Axis]bool{}
		for _, m := range vocab {
			if m.Name == ri.Mode {
				axes[m.Axis] = true
			}
		}
		if len(axes) == 0 {
			return fmt.Errorf("unknown mode %q", ri.Mode)
		}
		if len(axes) > 1 {
			return fmt.Errorf("mode %q is multi-axis; name a target or <axis>:*", ri.Mode)
		}
	case strings.HasSuffix(ri.Target, ":*"):
		axis := failuremode.Axis(strings.TrimSuffix(ri.Target, ":*"))
		if !modeOnAxisVocab(vocab, ri.Mode, axis) {
			return fmt.Errorf("mode %q not declared on axis %q", ri.Mode, axis)
		}
	default:
		var axis failuremode.Axis
		found := false
		for _, t := range bp.targets {
			if t.Name == ri.Target {
				axis, found = t.Axis, true
				break
			}
		}
		if !found {
			return fmt.Errorf("unknown target %q in blueprint %q", ri.Target, ri.Blueprint)
		}
		if !modeOnAxisVocab(vocab, ri.Mode, axis) {
			return fmt.Errorf("mode %q not declared on target %q's axis %q", ri.Mode, ri.Target, axis)
		}
	}
	// at/for required, then delegate grammar validation to shape: build a throwaway engine from the
	// formatted spec and reject if shape emitted any skip-warning (shape stays the sole grammar authority).
	if strings.TrimSpace(ri.At) == "" || strings.TrimSpace(ri.For) == "" {
		return fmt.Errorf("at and for are required")
	}
	spec := control.ScheduleSpec(ri.Mode, ri.Target, ri.At, ri.For, ri.Intensity)
	if w := shape.New(bp.eng.Loc().String(), []string{spec}).Warnings(); len(w) > 0 {
		return fmt.Errorf("invalid schedule: %s", w[0])
	}
	return nil
}

// splitSpec extracts (mode, scope, intensity) from a shape schedule string
// "mode@time/dur[#intensity][@scope]" using only the stable outer envelope markers — it does NOT
// parse the time grammar (shape owns that). Used to render declared incidents + compute active_now.
func splitSpec(spec string) (mode, scope string, intensity float64) {
	intensity = 1.0
	modeRaw, rest, found := strings.Cut(spec, "@")
	mode = strings.TrimSpace(modeRaw)
	if !found {
		return mode, "", intensity
	}
	slash := strings.LastIndex(rest, "/")
	if slash < 0 {
		return mode, "", intensity
	}
	durPart := rest[slash+1:]
	if i := strings.LastIndex(durPart, "@"); i >= 0 {
		scope = strings.TrimSpace(durPart[i+1:])
		durPart = durPart[:i]
	}
	if i := strings.Index(durPart, "#"); i >= 0 {
		if f, err := strconv.ParseFloat(strings.TrimSpace(durPart[i+1:]), 64); err == nil {
			intensity = f
		}
	}
	return mode, scope, intensity
}

// modeOnAxisVocab reports whether the registry vocab declares mode on axis.
func modeOnAxisVocab(vocab []failuremode.Mode, mode string, axis failuremode.Axis) bool {
	for _, m := range vocab {
		if m.Name == mode && m.Axis == axis {
			return true
		}
	}
	return false
}

// ApplyControl applies a control-plane snapshot live: the master volume knob lands on
// every blueprint's shape engine (one knob moves ALL synthetic load coherently); the
// failure map is read by the engines' Live hooks; disabled blueprints stop ticking.
// Call once at startup with the persisted snapshot, then from the control handler.
func (r *Runner) ApplyControl(s control.State) {
	r.ctl.Store(&s)
	for _, bp := range r.bps {
		bp.eng.SetVolumeMultiplier(s.VolumeMultiplier)
	}
	for _, bp := range r.bps {
		bp.scale.Set(scalingFor(bp.name, s.Scaling))
	}
	for _, bp := range r.bps {
		specs := runtimeSpecsFor(bp.name, s.RuntimeIncidents)
		bp.rtEng.Store(shape.New(bp.eng.Loc().String(), specs))
	}
}

// runtimeSpecsFor selects the runtime incidents owned by blueprint bp and formats each as a
// shape schedule string (the same grammar as declared incidents), preserving slice order.
func runtimeSpecsFor(bp string, all []control.RuntimeIncident) []string {
	var out []string
	for _, ri := range all {
		if ri.Blueprint != bp {
			continue
		}
		out = append(out, control.ScheduleSpec(ri.Mode, ri.Target, ri.At, ri.For, ri.Intensity))
	}
	return out
}

// scalingFor extracts one blueprint's overrides from the global qualified-key scaling map: entries
// keyed "<bp>/<target>" are selected and stripped to the bare target name, so the per-blueprint
// scale.Source (read by blueprint-unaware constructs via World.Scaling) sees only its own targets.
func scalingFor(bp string, all map[string]int) map[string]int {
	out := map[string]int{}
	pfx := bp + "/"
	for k, v := range all {
		if name, ok := strings.CutPrefix(k, pfx); ok {
			out[name] = v
		}
	}
	return out
}

// SetTickObserver installs the self-observability tick seam. Call once at startup (from package
// main, after selfobs.Start) before Run/RunOnce. nil leaves the runner uninstrumented.
func (r *Runner) SetTickObserver(t TickFunc) { r.tickObs = t }

// observeTick runs one instance Tick/ProjectBatch through the tick seam when set, else calls fn
// directly. It returns fn's error unchanged either way, so error aggregation is identical whether
// self-obs is on or off.
func (r *Runner) observeTick(ctx context.Context, blueprint, kind, name string, fn func(context.Context) error) error {
	if r.tickObs == nil {
		return fn(ctx)
	}
	return r.tickObs(ctx, blueprint, kind, name, fn)
}

// SetCycleObserver installs the per-blueprint cycle-observability seam. Call once at startup (from
// package main, after selfobs.Start) before Run. nil leaves cycle metrics uninstrumented.
func (r *Runner) SetCycleObserver(fn CycleFunc) { r.cycleObs = fn }

// SetFleetObserver installs the FM lifecycle observability seam on the fleet config. Fleet
// controllers are built lazily at Run time (startFleetControllers reads r.opts.Fleet), so this must
// be called once at startup before Run. nil leaves FM controllers uninstrumented.
func (r *Runner) SetFleetObserver(o fleethook.Observer) { r.opts.Fleet.Observe = o }

// callCycleObs fires the cycle seam when set (no-op otherwise). Safe to call from the per-blueprint
// goroutines: cycleObs is set once before Run and never mutated after.
func (r *Runner) callCycleObs(ctx context.Context, blueprint string, dur time.Duration, dropped int) {
	if r.cycleObs == nil {
		return
	}
	r.cycleObs(ctx, blueprint, dur, dropped)
}

// LedgerSize is a self-obs gauge source: the total request-ledger size summed across all blueprints.
func (r *Runner) LedgerSize() int64 {
	var n int64
	for _, bp := range r.bps {
		n += int64(bp.ledger.Len())
	}
	return n
}

// VolumeMultiplier is a self-obs gauge source: the current control-plane master volume knob.
func (r *Runner) VolumeMultiplier() float64 {
	st := r.ctl.Load()
	if st == nil {
		return 1.0
	}
	return st.VolumeMultiplier
}

// BlueprintCount is a self-obs gauge source: the number of loaded blueprints.
func (r *Runner) BlueprintCount() int { return len(r.bps) }

// spanMetricsEnabled reports whether the named blueprint should emit synthkit's OWN backend
// spanmetrics + service-graph this tick. Opt-IN (default OFF, incl. a nil snapshot): defer to
// Grafana Cloud metrics-generator / beyla unless the blueprint is explicitly listed.
func (r *Runner) spanMetricsEnabled(blueprint string) bool {
	st := r.ctl.Load()
	if st == nil {
		return false // default OFF — defer to metrics-generator/beyla
	}
	return st.SpanMetricsEnabled(blueprint)
}

// enabled reports whether a blueprint is currently enabled (control plane).
func (r *Runner) enabled(name string) bool {
	st := r.ctl.Load()
	if st == nil {
		return true
	}
	return !slices.Contains(st.DisabledBlueprints, name)
}

// constructEnabled reports whether a construct instance is enabled: false if its kind is
// globally disabled OR its "blueprint/kind:name" key is in the per-instance disable list.
// Mirrors enabled() (reads the atomic control snapshot; nil => enabled).
func (r *Runner) constructEnabled(blueprint, kind, name string) bool {
	st := r.ctl.Load()
	if st == nil {
		return true
	}
	if slices.Contains(st.DisabledKinds, kind) {
		return false
	}
	return !slices.Contains(st.DisabledConstructs, control.ConstructKey(blueprint, kind, name))
}

// BlueprintSource returns a blueprint's raw YAML source (control.BlueprintSourcer).
func (r *Runner) BlueprintSource(name string) (string, bool) {
	for _, bp := range r.bps {
		if bp.name == name {
			return bp.source, true
		}
	}
	return "", false
}

// Recent exposes a blueprint's recent ledger requests (the Infinity JSON host's
// jsondata.Source surface).
func (r *Runner) Recent(blueprint string, now time.Time, window time.Duration) []*ledger.Request {
	for _, bp := range r.bps {
		if bp.name == blueprint {
			return bp.ledger.Recent(now, window)
		}
	}
	return nil
}

// WindowStats exposes a blueprint's cap-independent mint aggregates (jsondata.Source surface) —
// exact counts/distinct-workloads over the window even when the request ring is cap-trimmed.
func (r *Runner) WindowStats(blueprint string, now time.Time, window time.Duration) ledger.WindowStats {
	for _, bp := range r.bps {
		if bp.name == blueprint {
			return bp.ledger.WindowStats(now, window)
		}
	}
	return ledger.WindowStats{}
}
