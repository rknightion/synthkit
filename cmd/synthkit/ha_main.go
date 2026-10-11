// SPDX-License-Identifier: AGPL-3.0-only
package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/rknightion/synthkit/internal/blueprint"
	"github.com/rknightion/synthkit/internal/bpsource"
	"github.com/rknightion/synthkit/internal/config"
	"github.com/rknightion/synthkit/internal/control"
	"github.com/rknightion/synthkit/internal/fleet"
	"github.com/rknightion/synthkit/internal/fleethook"
	"github.com/rknightion/synthkit/internal/fleetstatus"
	"github.com/rknightion/synthkit/internal/ha"
	lease "github.com/rknightion/synthkit/internal/ha/kubernetes"
	"github.com/rknightion/synthkit/internal/healthstatus"
	"github.com/rknightion/synthkit/internal/preflight"
	"github.com/rknightion/synthkit/internal/profiling"
	"github.com/rknightion/synthkit/internal/pushhook"
	"github.com/rknightion/synthkit/internal/pushstatus"
	"github.com/rknightion/synthkit/internal/runner"
	"github.com/rknightion/synthkit/internal/selfobs"
	"github.com/rknightion/synthkit/internal/sink/faro"
	"github.com/rknightion/synthkit/internal/sink/httpretry"
	"github.com/rknightion/synthkit/internal/sink/loki"
	"github.com/rknightion/synthkit/internal/sink/otlp"
	"github.com/rknightion/synthkit/internal/sink/promrw"
	"github.com/rknightion/synthkit/internal/sink/pyroscope"
	"github.com/rknightion/synthkit/internal/sink/queue"
	"github.com/rknightion/synthkit/internal/sink/sigil"
)

// openConfiguredState never writes during construction. Production Kubernetes state
// uses only in-cluster credentials and explicit precreated object names.
func openConfiguredState(ctx context.Context, cfg *config.Config, gate ha.LeaderGate) (*control.Store, control.StateBackend, error) {
	if cfg.StateBackend != "kubernetes" {
		if cfg.HAMode == "lease" {
			store, err := control.NewHAStore(cfg.SnapshotPath, gate)
			return store, nil, err
		}
		return control.NewStore(cfg.SnapshotPath), nil, nil
	}
	objects := map[control.Key]string{control.Control: cfg.StateControlConfigMap, control.BootManifest: cfg.StateBootConfigMap}
	for id, name := range cfg.StateGitSourceConfigMaps {
		key, err := control.GitSourceKey(id)
		if err != nil {
			return nil, nil, err
		}
		objects[key] = name
	}
	backend, err := control.NewKubernetesBackend(control.KubernetesBackendOptions{Gate: gate, Namespace: cfg.HANamespace, Objects: objects, RequestTimeout: cfg.HAKubeRequestTimeout, MaxDocumentBytes: control.MaxStateDocumentBytes})
	if err != nil {
		return nil, nil, err
	}
	// Verify every named slot without discovering or manufacturing resources.
	for key := range objects {
		if _, err := backend.Load(ctx, key); err != nil {
			return nil, nil, err
		}
	}
	store, err := control.NewBackendStore(ctx, backend, gate, cfg.StateCASMaxAttempts)
	return store, backend, err
}

// haCrash is the sole loss/watchdog path: no drain, unregister, release or log.
func haCrash(gate *ha.Gate, exit func(int)) func() {
	return func() { gate.Revoke(); exit(1); panic("HA crash returned") }
}

type leaseElection interface {
	Run(context.Context)
	Seal(context.Context) error
	Release(context.Context) error
}
type haDependencies struct {
	Election  func(context.Context, lease.Options) (leaseElection, error)
	Preflight func(context.Context, *config.Config) error
	Exit      func(int) // production os.Exit; terminal and non-returning
	// StateBackend is a test-only process-edge factory. Production uses in-cluster auth.
	StateBackend     func(ha.LeaderGate) control.StateBackend
	BeforeProduction func(*haView) // test-only first-tick observation on the real composed runner
	// BeforeHandoff is a test-only scheduling hook, before terminal admission.
	BeforeHandoff func(string)
	// WrapHTTPListener is a test-only process-edge scheduling hook.
	WrapHTTPListener func(net.Listener) net.Listener
}

func productionHADependencies() haDependencies {
	return haDependencies{
		Election: func(ctx context.Context, o lease.Options) (leaseElection, error) { return lease.New(ctx, o) },
		Preflight: func(ctx context.Context, cfg *config.Config) error {
			if cfg.DryRun {
				return nil
			}
			results, err := preflight.Check(ctx, cfg, preflight.Options{})
			if err != nil {
				return err
			}
			for _, r := range results {
				if r.State != preflight.StateReady {
					return fmt.Errorf("mandatory credential preflight failed: %s", r.Lane)
				}
			}
			return nil
		}, Exit: os.Exit,
	}
}

// haLifecycle owns publication/launch versus termination. Election renewal has
// its own lifetime; neither producer cancellation nor drain cancels the elector.
type haLifecycle struct {
	mu                      sync.Mutex
	gate                    *ha.Gate
	terminating, activating bool
	acquired                bool // distinguishes never-led standby from a revoked former leader
	termination             time.Time
	term                    chan struct{}
	producerDone            chan struct{}
	producerCtx             context.Context
	producerCancel          context.CancelFunc
	current                 atomic.Pointer[haView]
	operational             haOperational
	producerError           error
}

func (l *haLifecycle) terminate() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.terminating {
		return
	}
	l.terminating = true
	l.termination = time.Now()
	l.gate.Quiesce()
	l.producerCancel()
	close(l.term)
}

// readinessFacts is installed only on views whose config/build/preflight completed.
// Read the lifecycle flags together so a revoked former leader cannot look like standby.
func (l *haLifecycle) readinessFacts() control.HAReadiness {
	l.mu.Lock()
	defer l.mu.Unlock()
	role := l.gate.Role()
	return control.HAReadiness{
		ConfigLoaded: true, RunnerBuilt: true, PreflightPassed: true,
		Standby:       !l.acquired && role == ha.RoleStandby,
		Transitioning: l.activating || l.terminating || (l.acquired && role != ha.RoleLeader),
	}
}

type haQueueObserver struct {
	view   *haView
	status *pushstatus.Store
}

func (o haQueueObserver) EnqueueBlocked(sink string, d time.Duration) {
	o.status.EnqueueBlocked(sink, d)
	o.view.so.Load().EnqueueBlocked(sink, d)
}
func (o haQueueObserver) FlushObserved(event queue.FlushEvent) {
	o.status.FlushObserved(event)
	o.view.so.Load().FlushObserved(event)
}

type haView struct {
	runner             *runner.Runner
	store              *control.Store
	manager            *bpsource.Manager
	loaded             []bpsource.Loaded
	manifest           bpsource.Manifest
	diagnostics        []bpsource.Diag
	handler            http.Handler
	readinessFacts     func() control.HAReadiness // installed before view publication, after preflight
	so                 atomic.Pointer[selfobs.SelfObs]
	observabilityReady chan struct{}
	prom               *promrw.Sink
	loki               *loki.Sink
	traces             *otlp.Sink
	metrics            *otlp.MetricsSink
	logs               *otlp.LogsSink
	profiles           *pyroscope.Sink
	sigil              *sigil.Sink
}

// newHAView is read-only even during acquisition; writes are committed separately
// using Activate's preparation context. A fresh view rebuilds topology, not merely knobs.
func newHAView(ctx context.Context, cfg *config.Config, gate ha.LeaderGate, bound ha.Bounded, injected ...control.StateBackend) (*haView, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var store *control.Store
	var backend control.StateBackend
	var err error
	if len(injected) > 0 && injected[0] != nil {
		backend = injected[0]
		store, err = control.NewBackendStore(ctx, backend, gate, cfg.StateCASMaxAttempts)
	} else {
		store, backend, err = openConfiguredState(ctx, cfg, gate)
	}
	if err != nil {
		return nil, err
	}
	policy, err := bpsource.NewSourcePolicy(cfg.GitSourceHostAllowlist)
	if err != nil {
		return nil, err
	}
	tokenLookup := func(name string) string {
		if name == "" || name == "GIT_TOKEN" {
			return cfg.GitTokenDefault
		}
		return os.Getenv(name)
	}
	sc := bpsource.NewStoreSourceConfig(store)
	reg := runner.Catalog()
	mgr := bpsource.NewManager(bpsource.Options{Backend: backend, CASAttempts: cfg.StateCASMaxAttempts, MaxDocumentBytes: cfg.StateGitSourceMaxBytes, BackendError: store.ObserveBackendError, Gate: gate, ReadOnly: true, BakedDir: cfg.BlueprintsDir, BlueprintNames: cfg.BlueprintNames, DataDir: cfg.BlueprintDataDir, Registry: reg, RuntimeLimits: blueprint.RuntimeLimits{MasterTick: cfg.MasterTick, MaxDPMPerSeries: cfg.MaxDPMPerSeries}, Git: bpsource.NewNanogitClientWithPolicy(tokenLookup, policy), SourcePolicy: policy, Config: sc})
	if err := mgr.LoadBackend(ctx); err != nil {
		return nil, err
	}
	loaded, manifest, diags := mgr.Resolve(ctx)
	if err := mgr.SelectionError(); err != nil {
		return nil, err
	}
	for _, d := range diags {
		if d.Severity == "error" {
			return nil, fmt.Errorf("HA blueprint load: %s", d.Detail)
		}
	}
	accepted, err, skipped := foldValidated(loaded)
	if err != nil {
		return nil, err
	}
	if len(skipped) > 0 {
		return nil, fmt.Errorf("HA selected blueprint set failed validation")
	}
	if _, err := prepareSMHandoff(accepted, cfg, version, time.Now()); err != nil {
		return nil, err
	}
	v := &haView{store: store, manager: mgr, loaded: loaded, manifest: manifest, diagnostics: diags, observabilityReady: make(chan struct{})}
	delivery := httpretry.Delivery{Gate: gate, HTTPTimeout: cfg.HAHTTPTimeout, RetryMaxElapsed: cfg.HARetryMaxElapsed}
	v.prom = promrw.New(cfg.PromRWURL, cfg.PromUser, cfg.Token, cfg.DryRun, func() int { return cfg.SeriesCap })
	v.prom.SetDelivery(delivery)
	v.loki = loki.New(cfg.LokiURL, cfg.LokiUser, cfg.Token, cfg.DryRun)
	v.loki.SetDelivery(delivery)
	v.traces = otlp.New(cfg.OTLPEndpoint, cfg.OTLPUser, cfg.Token, cfg.DryRun)
	v.traces.SetDelivery(delivery)
	v.metrics = otlp.NewMetrics(cfg.OTLPEndpoint, cfg.OTLPUser, cfg.Token, cfg.DryRun)
	v.metrics.SetDelivery(delivery)
	v.logs = otlp.NewLogs(cfg.OTLPEndpoint, cfg.OTLPUser, cfg.Token, cfg.DryRun)
	v.logs.SetDelivery(delivery)
	sinks := runner.Sinks{Metrics: v.prom, Logs: v.loki, Traces: v.traces, OTLPMetrics: v.metrics, OTLPLogs: v.logs}
	if cfg.RUMEnabled() {
		s := faro.New(cfg.FaroCollector, cfg.FaroAppKey, cfg.DryRun)
		s.SetDelivery(delivery)
		sinks.RUM = s
	}
	if cfg.SynthProfilesEnabled() || cfg.DryRun {
		v.profiles = pyroscope.New(cfg.ProfilesURL, cfg.ProfilesUser, cfg.Token, cfg.DryRun)
		v.profiles.SetDelivery(delivery)
		sinks.Profiles = v.profiles
	}
	if cfg.SigilEnabled() || cfg.DryRun {
		s, err := sigil.New(cfg.SigilEndpoint, cfg.SigilTenantID, cfg.SigilToken, cfg.DryRun)
		if err != nil {
			return nil, err
		}
		v.sigil = s
		s.SetDelivery(delivery)
		sinks.Sigil = s
	}
	v.runner = runner.New(sinks, reg, runner.Options{Gate: gate, Delivery: bound, MasterTick: cfg.MasterTick, MaxDPMPerSeries: cfg.MaxDPMPerSeries, TickTimeout: cfg.TickTimeout, Fleet: fleet.Config{FMURL: cfg.FMURL, StackID: cfg.FMStackID, Token: cfg.FMToken, DryRun: cfg.DryRun, Delivery: delivery, Bounded: bound}, SendShards: cfg.SendShards, SendBatchMax: cfg.SendBatchMax, SendDeadline: cfg.SendDeadline, SendCapacity: cfg.SendCapacity, SendDrainDeadline: cfg.SendDrainDeadline})
	for _, bp := range accepted {
		if err := v.runner.AddBlueprint(bp); err != nil {
			return nil, err
		}
	}
	if v.runner.BlueprintCount() == 0 && mgr.SelectionRequested() {
		return nil, errors.New("no selected HA blueprints loaded")
	}
	v.runner.ApplyControl(store.Snapshot())
	ps := pushstatus.NewStore()
	fs := fleetstatus.NewStore()
	hs := healthstatus.NewStore()
	configure := func() {
		var lanes []pushstatus.LaneConfig
		for _, l := range v.runner.DeliveryReadinessLanes() {
			lc := pushstatus.LaneConfig{Name: l.Name, FreshAfter: l.EmissionInterval + cfg.SendDeadline}
			if cfg.DryRun {
				lc.Disabled = true
				lc.DisabledReason = "dry_run"
			}
			lanes = append(lanes, lc)
		}
		ps.ConfigureLanes(lanes)
	}
	configure()
	obs := func(ctx context.Context, ev pushhook.Event) {
		ps.Observer()(ctx, ev)
		if so := v.so.Load(); so != nil {
			if observer := so.PushObserver(); observer != nil {
				observer(ctx, ev)
			}
		}
	}
	v.prom.Observe = obs
	v.loki.Observe = obs
	v.traces.Observe = obs
	v.metrics.Observe = obs
	v.logs.Observe = obs
	if s, ok := sinks.RUM.(*faro.Sink); ok {
		s.Observe = obs
	}
	if v.profiles != nil {
		v.profiles.Observe = obs
	}
	if v.sigil != nil {
		v.sigil.Observe = obs
	}
	v.runner.SetFleetObserver(func(ctx context.Context, event fleethook.Event) {
		fs.Observer()(ctx, event)
		if so := v.so.Load(); so != nil {
			if observe := so.FleetObserver(); observe != nil {
				observe(ctx, event)
			}
		}
	})
	v.runner.SetFleetReceiptObserver(fs.ReceiptObserver())
	v.runner.SetQueueObserver(haQueueObserver{view: v, status: ps})
	v.runner.SetTickObserver(func(ctx context.Context, bp, kind, name string, fn func(context.Context) error) error {
		start := time.Now()
		var err error
		if so := v.so.Load(); so != nil {
			err = so.ObserveTick(ctx, bp, kind, name, fn)
		} else {
			err = fn(ctx)
		}
		hs.RecordOutcome(bp, kind, name, time.Since(start), err)
		return err
	})
	v.runner.SetCycleObserver(func(ctx context.Context, bp string, d time.Duration, dropped int) {
		hs.ObserveCycle(ctx, bp, d, dropped)
		if so := v.so.Load(); so != nil {
			so.ObserveCycle(ctx, bp, d, dropped)
		}
	})
	readiness := func() control.ReadinessReport {
		facts := control.HAReadiness{ConfigLoaded: true, RunnerBuilt: true}
		if v.readinessFacts != nil {
			facts = v.readinessFacts()
		}
		persist := store.PersistHealth()
		var required []string
		for _, lane := range v.runner.DeliveryReadinessLanes() {
			required = append(required, lane.Name)
		}
		return control.EvaluateReadiness(control.ReadinessInput{
			HA: &facts, ProcessRunning: true, HTTPServing: true,
			SetupRequired: !mgr.SelectionRequested(),
			Blueprints:    control.BlueprintReadiness{Loaded: len(loaded), Active: v.runner.ActiveBlueprintCount()},
			PersistedState: control.PersistedStateReadiness{
				Writable: persist.LastOKMs > 0 && persist.LastError == "", Error: persist.LastError,
			},
			Lanes: ps.SnapshotLanes(), RequiredLanes: required, LiveDeliveryExpected: !cfg.DryRun,
		})
	}
	mutationBound := ha.Bounded{Gate: gate, Timeout: 2 * time.Second, Margin: cfg.HAFenceMargin, Crash: bound.Crash}
	handler := control.NewHandler(store, v.runner.ApplyControl, cfg.ControlToken, v.runner).SetHA(gate, mutationBound).
		SetBasePath(cfg.ControlBasePath).
		SetManagedFeatures(cfg.ControlManagedFeatures).
		SetStatus(control.StatusSources{Sinks: ps.Snapshot, Queues: func() []pushstatus.QueueStat { return ps.SnapshotQueues(v.runner.QueueDepths()) }, ByBlueprint: ps.SnapshotByBlueprint, Fleet: fs.Snapshot, DryRun: cfg.DryRun, Readiness: readiness}).
		SetBlueprintAdmin(&haBlueprintAdmin{blueprintAdminAdapter: &blueprintAdminAdapter{mgr: mgr, sc: sc}}).SetInventory(v.runner).SetConfig(toControlConfigView(cfg.RedactedHA())).SetHealth(func() any { return healthReport(hs.Snapshot()) }).
		SetChangeObserver(func(s control.State) {
			configure()
			<-v.observabilityReady // still accounted by the admitted mutation
			if so := v.so.Load(); so != nil {
				so.EmitEvent("config_change", configChangeAttrs(s), configChangeBody(s))
			}
		})
	mux := http.NewServeMux()
	mountControlHandler(mux, cfg.ControlBasePath, handler)
	mux.Handle("/", jsonHost(v.runner, cfg.ControlToken))
	v.handler = mux
	return v, nil
}

func haRoleTags(tags map[string]string, role ha.Role) map[string]string {
	result := make(map[string]string, len(tags)+1)
	for k, v := range tags {
		result[k] = v
	}
	result["ha.role"] = string(role)
	return result
}

type haOperational struct {
	so   *selfobs.SelfObs
	prof interface{ Stop() error }
}

func startHAOperational(cfg *config.Config, v *haView, role ha.Role) (haOperational, error) {
	so, err := selfobs.Start(selfobs.Options{Enabled: cfg.SelfObsEnabled, Endpoint: cfg.SelfOTLPEndpoint, User: cfg.SelfOTLPUser, Password: cfg.SelfOTLPPassword, Tags: haRoleTags(selfobs.ParseTags(cfg.SelfObsTags), role), Version: version, MetricInterval: cfg.SelfObsMetricInterval, DryRun: cfg.DryRun}, selfobs.Gauges{LedgerSize: v.runner.LedgerSize, VolumeMultiplier: v.runner.VolumeMultiplier, BlueprintCount: v.runner.BlueprintCount, QueueDepth: v.runner.QueueDepths, Cardinality: func() []selfobs.CardinalityPoint {
		var result []selfobs.CardinalityPoint
		for _, bp := range v.runner.Inventory().Blueprints {
			for _, c := range bp.Constructs {
				result = append(result, selfobs.CardinalityPoint{Blueprint: bp.Blueprint, Kind: c.Kind, Name: c.Name, Distinct: c.DistinctSeries})
			}
		}
		return result
	}})
	if err != nil {
		return haOperational{}, err
	}
	v.so.Store(so)
	if v.observabilityReady != nil {
		close(v.observabilityReady)
	}
	prof, err := profiling.Start(profiling.Options{Enabled: cfg.SelfObsEnabled, URL: cfg.PyroscopeURL, User: cfg.PyroscopeUser, Password: cfg.PyroscopePassword, Tags: haRoleTags(profiling.ParseTags(cfg.PyroscopeTags), role), Version: version, MutexFraction: cfg.PyroscopeMutexFraction, BlockRate: cfg.PyroscopeBlockRate})
	ops := haOperational{so: so}
	if prof != nil {
		ops.prof = prof
	}
	return ops, err
}
func (o haOperational) stop(ctx context.Context) error {
	if o.so != nil {
		o.so.Shutdown(ctx)
	}
	if o.prof != nil {
		return o.prof.Stop()
	}
	return ctx.Err()
}

func runLeaseMode(cfg *config.Config, once, dump, inventoryJSON bool) error {
	if dump || inventoryJSON {
		return errors.New("HA inventory verification requires HA_MODE=off")
	}
	signals := make(chan os.Signal, 2)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	term := make(chan struct{})
	go func() {
		select {
		case <-signals:
			close(term)
		case <-term:
		}
	}()
	return runHALifecycle(cfg, once, term, productionHADependencies())
}

func runHALifecycle(cfg *config.Config, once bool, terminate <-chan struct{}, deps haDependencies) error {
	gate := ha.NewGate()
	// Loss must also cancel an overlapping planned shutdown. The production
	// exit is immediate, but another goroutine may already be joining workers;
	// it must never proceed to seal/release after the terminal callback.
	fatalCtx, fatalCancel := context.WithCancel(context.Background())
	defer fatalCancel()
	terminal := &haTerminal{gate: gate, cancel: fatalCancel, exit: deps.Exit}
	crash := terminal.crash
	bound := ha.Bounded{Gate: gate, Timeout: cfg.HAFlushTimeout, Margin: cfg.HAFenceMargin, Crash: crash}
	prodCtx, prodCancel := context.WithCancel(context.Background())
	l := &haLifecycle{gate: gate, term: make(chan struct{}), producerCtx: prodCtx, producerCancel: prodCancel, producerDone: make(chan struct{})}
	startupDone := make(chan struct{})
	defer close(startupDone)
	go func() {
		select {
		case <-terminate:
			l.terminate()
		case <-startupDone:
		}
	}()
	var injectedBackend control.StateBackend
	if deps.StateBackend != nil {
		injectedBackend = deps.StateBackend(gate)
	}
	standby, err := newHAView(prodCtx, cfg, gate, bound, injectedBackend)
	if err != nil {
		prodCancel()
		return err
	}
	if err := deps.Preflight(prodCtx, cfg); err != nil {
		prodCancel()
		return err
	}
	standby.readinessFacts = l.readinessFacts
	l.current.Store(standby)
	standbyOps, err := startHAOperational(cfg, standby, ha.RoleStandby)
	if err != nil {
		prodCancel()
		return err
	}
	mutationBound := ha.Bounded{Gate: gate, Timeout: 2 * time.Second, Margin: cfg.HAFenceMargin, Crash: crash}
	// Mutations acquire admission BEFORE choosing the current view. An HTTP request
	// captured during standby can never apply through a stale pre-acquisition runner.
	router := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
		}
		if headers := r.Header.Get("Access-Control-Request-Headers"); headers != "" {
			w.Header().Set("Access-Control-Allow-Headers", headers)
		}
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
			l.current.Load().handler.ServeHTTP(w, r)
			return
		}
		control.RequireToken(cfg.ControlToken, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Admit before selecting the view. The control handler owns its
			// operation watchdog/read deadline and emits exactly one response.
			err := gate.Do(r.Context(), ha.Mutation, func(ctx context.Context) error { l.current.Load().handler.ServeHTTP(w, r.WithContext(ctx)); return nil })
			if err != nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte(`{"code":"not_leader"}`))
			}
		})).ServeHTTP(w, r)
	})
	listener, err := net.Listen("tcp", cfg.HTTPAddr)
	if err != nil {
		prodCancel()
		return err
	}
	if deps.WrapHTTPListener != nil {
		listener = deps.WrapHTTPListener(listener)
	}
	httpDrain := &haHTTPDrain{}
	srv := &http.Server{Handler: router, ConnState: httpDrain.connState, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second}
	serverErr := make(chan error, 1)
	go func() { serverErr <- srv.Serve(listener) }()
	election, err := deps.Election(context.Background(), lease.Options{Namespace: cfg.HANamespace, Name: cfg.HALeaseName, PodUID: cfg.PodUID, LeaseDuration: cfg.HALeaseDuration, RenewDeadline: cfg.HARenewDeadline, RetryPeriod: cfg.HARetryPeriod, RequestTimeout: cfg.HAKubeRequestTimeout, Stopped: crash, Started: func(leadCtx context.Context) {
		context.AfterFunc(leadCtx, prodCancel)
		l.mu.Lock()
		l.activating = true
		l.acquired = true
		l.mu.Unlock()
		var leader *haView
		err := gate.Activate(leadCtx, func(prepCtx context.Context) error {
			var err error
			leader, err = newHAView(prepCtx, cfg, gate, bound, injectedBackend)
			if err != nil {
				return err
			}
			if err := deps.Preflight(prepCtx, cfg); err != nil {
				return err
			}
			leader.readinessFacts = l.readinessFacts
			commit := ha.Bounded{Gate: gate, Timeout: 2 * time.Second, Margin: cfg.HAFenceMargin, Crash: crash}
			if err := commit.Run(prepCtx, ha.Mutation, func(ctx context.Context) error {
				if err := leader.store.ProbeWriteContext(ctx); err != nil {
					return err
				}
				if err := leader.manager.CommitResolved(ctx, leader.manifest, leader.loaded, leader.diagnostics); err != nil {
					return err
				}
				leader.runner.ApplyControl(leader.store.Snapshot())
				return ctx.Err()
			}); err != nil {
				return err
			}
			l.current.Store(leader)
			return nil
		})
		if err != nil {
			crash()
			return
		}
		// Old providers keep their immutable standby resource, including buffered events.
		rotate := ha.Bounded{Gate: ha.AlwaysLeader{}, Timeout: 2 * time.Second, Margin: cfg.HAFenceMargin, Crash: crash}
		if err := rotate.Run(leadCtx, ha.Delivery, standbyOps.stop); err != nil {
			crash()
			return
		}
		leaderOps, err := startHAOperational(cfg, leader, ha.RoleLeader)
		if err != nil {
			crash()
			return
		}
		l.mu.Lock()
		l.operational = leaderOps
		l.activating = false
		if l.terminating || prodCtx.Err() != nil || gate.Role() != ha.RoleLeader {
			close(l.producerDone)
			l.mu.Unlock()
			return
		}
		if deps.BeforeProduction != nil {
			deps.BeforeProduction(leader)
		}
		go func() {
			defer close(l.producerDone)
			if once {
				err := leader.runner.RunOnce(prodCtx, time.Now())
				l.mu.Lock()
				l.producerError = err
				l.mu.Unlock()
				l.terminate()
				return
			}
			var wg sync.WaitGroup
			if cfg.GitPollInterval > 0 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					ticker := time.NewTicker(time.Duration(cfg.GitPollInterval) * time.Second)
					defer ticker.Stop()
					for {
						select {
						case <-prodCtx.Done():
							return
						case <-ticker.C:
							_ = mutationBound.Run(prodCtx, ha.Mutation, func(ctx context.Context) error { leader.manager.PollSources(ctx); return ctx.Err() })
						}
					}
				}()
			}
			_ = leader.runner.RunProducers(prodCtx, context.Background())
			wg.Wait()
		}()
		l.mu.Unlock()
	}})
	if err != nil {
		prodCancel()
		_ = srv.Close()
		return err
	}
	go election.Run(context.Background())
	select {
	case <-terminate:
		l.terminate()
	case <-l.term:
	case <-serverErr:
		crash()
	}
	l.mu.Lock()
	deadline := l.termination.Add(cfg.SendDrainDeadline)
	l.mu.Unlock()
	drainCtx, cancel := context.WithDeadline(fatalCtx, deadline)
	defer cancel()
	completed := make(chan struct{})
	go func() {
		select {
		case <-drainCtx.Done():
			crash()
		case <-completed:
		}
	}()
	// A standby never had producers, and must neither unregister nor release.
	if deps.BeforeHandoff != nil {
		deps.BeforeHandoff("role-check")
	}
	if gate.Role() != ha.RoleLeader {
		l.mu.Lock()
		acquired := l.acquired
		l.mu.Unlock()
		if acquired {
			crash()
		}
		gate.Revoke()
		if deps.BeforeHandoff != nil {
			deps.BeforeHandoff("standby-exit")
		}
		terminal.finish(0)
	}
	select {
	case <-l.producerDone:
	case <-drainCtx.Done():
		crash()
	}
	current := l.current.Load()
	var wg sync.WaitGroup
	var mu sync.Mutex
	var cleanupErrs []error
	cleanup := func(fn func(context.Context) error) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := fn(drainCtx); err != nil {
				mu.Lock()
				cleanupErrs = append(cleanupErrs, err)
				mu.Unlock()
			}
		}()
	}
	cleanup(current.runner.JoinQueues)
	cleanup(current.runner.CleanupFleet)
	cleanup(gate.Wait)
	cleanup(func(ctx context.Context) error { return httpDrain.shutdown(ctx, srv) })
	wg.Wait()
	if len(cleanupErrs) > 0 || drainCtx.Err() != nil {
		crash()
	}
	l.mu.Lock()
	ops := l.operational
	l.mu.Unlock()
	if err := ops.stop(drainCtx); err != nil || drainCtx.Err() != nil {
		crash()
	}
	gate.Revoke()
	close(completed)
	if deps.BeforeHandoff != nil {
		deps.BeforeHandoff("seal")
	}
	sealCtx, sealCancel := context.WithTimeout(fatalCtx, cfg.HAKubeRequestTimeout)
	err = terminal.admit(sealCtx, election.Seal)
	sealCancel()
	if err != nil {
		crash()
	}
	if deps.BeforeHandoff != nil {
		deps.BeforeHandoff("release")
	}
	releaseCtx, releaseCancel := context.WithTimeout(fatalCtx, cfg.HAReleaseTimeout)
	_ = terminal.admit(releaseCtx, election.Release)
	releaseCancel()
	// A best-effort release failure can exit normally, but fatal cancellation
	// is not such a failure. finish also arbitrates a loss after this check.
	if fatalCtx.Err() != nil {
		crash()
	}
	// No defer, unregister, HTTP shutdown, exporter flush or state write follows release.
	l.mu.Lock()
	producerError := l.producerError
	l.mu.Unlock()
	code := 0
	if producerError != nil {
		code = 1
	}
	if deps.BeforeHandoff != nil {
		deps.BeforeHandoff("planned-exit")
	}
	terminal.finish(code)
	panic("HA terminal finish returned")
}
