// SPDX-License-Identifier: AGPL-3.0-only
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
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
	"github.com/rknightion/synthkit/internal/optionallane"
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
	if cfg.StateFleetConfigMap != "" {
		objects[control.FleetRegistered] = cfg.StateFleetConfigMap
	}
	if cfg.StateSMConfigMap != "" {
		objects[control.SMState] = cfg.StateSMConfigMap
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

// fleetStateAdapter keeps fleet independent of control while reusing its gated CAS backend.
// The document is a cleanup ledger, including registration intents with unknown HTTP outcomes.
type fleetStateAdapter struct {
	backend     control.StateBackend
	attempts    int
	destination string
}
type fleetStateDocument struct {
	Version     int      `json:"version"`
	Destination string   `json:"destination,omitempty"`
	IDs         []string `json:"ids"`
}

// Match the client's trailing-slash handling; token rotation never changes ownership.
// Encode the pair unambiguously and persist only its non-secret destination digest.
func fleetDestination(fmURL, stackID string) string {
	data, _ := json.Marshal([2]string{strings.TrimRight(fmURL, "/"), stackID})
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum)
}

func decodeFleetState(data []byte, destination string) ([]string, error) {
	if len(data) == 0 {
		return nil, nil
	}
	if len(data) > control.MaxStateDocumentBytes {
		return nil, errors.New("fleet state exceeds byte cap")
	}
	var doc fleetStateDocument
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&doc); err != nil {
		return nil, errors.New("invalid fleet state")
	}
	if err := decoder.Decode(new(any)); err != io.EOF || (doc.Version != 1 && doc.Version != 2) {
		return nil, errors.New("invalid fleet state version or trailing data")
	}
	if doc.Version == 1 {
		// No safe automatic ownership assignment exists for old nonempty intent.
		if len(doc.IDs) != 0 || doc.Destination != "" {
			return nil, fleet.ErrLedgerUnbound
		}
	} else if destination == "" || doc.Destination != destination {
		return nil, fleet.ErrLedgerDestinationMismatch
	}
	seen := map[string]bool{}
	for _, id := range doc.IDs {
		if id == "" || len(id) > 256 || strings.IndexFunc(id, func(r rune) bool { return r <= 32 || r == 127 }) >= 0 || seen[id] {
			return nil, errors.New("invalid fleet state IDs")
		}
		seen[id] = true
	}
	return doc.IDs, nil
}
func (s *fleetStateAdapter) Load(ctx context.Context) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	snap, err := s.backend.Load(ctx, control.FleetRegistered)
	if err != nil {
		return nil, err
	}
	return decodeFleetState(snap.Data, s.destination)
}

// Inventory is Load plus whether a destination-bound version-2 document exists. An absent
// document (a missing file ledger or a never-written ConfigMap) and an empty legacy document
// are not bound, so owned teardown cannot certify their empty ID set.
func (s *fleetStateAdapter) Inventory(ctx context.Context) ([]string, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	snap, err := s.backend.Load(ctx, control.FleetRegistered)
	if err != nil {
		return nil, false, err
	}
	ids, err := decodeFleetState(snap.Data, s.destination)
	if err != nil || len(snap.Data) == 0 {
		return ids, false, err
	}
	var doc struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(snap.Data, &doc); err != nil {
		return nil, false, err
	}
	return ids, doc.Version == 2, nil
}
func (s *fleetStateAdapter) Set(ctx context.Context, id string, present bool) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	for attempt := 0; attempt < s.attempts; attempt++ {
		snap, err := s.backend.Load(ctx, control.FleetRegistered)
		if err != nil {
			return err
		}
		ids, err := decodeFleetState(snap.Data, s.destination)
		if err != nil {
			return err
		}
		set := map[string]bool{}
		for _, old := range ids {
			set[old] = true
		}
		if set[id] == present {
			return ctx.Err()
		}
		if present {
			set[id] = true
		} else {
			delete(set, id)
		}
		ids = make([]string, 0, len(set))
		for old := range set {
			ids = append(ids, old)
		}
		sort.Strings(ids)
		data, err := json.Marshal(fleetStateDocument{Version: 2, Destination: s.destination, IDs: ids})
		if err != nil {
			return err
		}
		if _, err := decodeFleetState(data, s.destination); err != nil {
			return err
		}
		_, err = s.backend.CompareAndSwap(ctx, control.FleetRegistered, snap.Revision, data)
		if errors.Is(err, control.ErrConflict) {
			continue
		}
		return err
	}
	return control.ErrConflict
}
func configuredFleetStore(cfg *config.Config, backend control.StateBackend, gate ha.LeaderGate) (fleet.RegisteredStore, error) {
	if cfg.FMURL == "" || cfg.DryRun {
		return nil, nil
	}
	if cfg.StateBackend == "kubernetes" {
		if cfg.StateFleetConfigMap == "" || backend == nil {
			return nil, errors.New("Fleet registration requires STATE_FLEET_CONFIGMAP with Kubernetes state")
		}
	} else {
		var err error
		backend, err = control.NewFileBackend(control.FileBackendOptions{Gate: gate, Paths: map[control.Key]string{control.FleetRegistered: filepath.Clean(cfg.SnapshotPath) + ".fleet.json"}})
		if err != nil {
			return nil, err
		}
	}
	attempts := cfg.StateCASMaxAttempts
	if attempts < 1 {
		attempts = 5
	}
	if attempts > 5 {
		return nil, errors.New("invalid fleet CAS budget")
	}
	return &fleetStateAdapter{backend: backend, attempts: attempts, destination: fleetDestination(cfg.FMURL, cfg.FMStackID)}, nil
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
	// FleetTeardown, when set with FleetUnregister, replaces the selected-roster teardown with the
	// owned-ledger teardown, run by the elected leader with its gated Fleet configuration.
	FleetTeardown func(context.Context, fleet.Config) error
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
	teardownCompleted       bool // true only after explicit unregister actually succeeds
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
	smState            smHandoff
	fleet              fleet.Config
}

// newHAView is read-only in standby. Acquisition explicitly publishes SM through
// Activate's preparation context before building the production runner.
func newHAView(ctx context.Context, cfg *config.Config, gate ha.LeaderGate, bound ha.Bounded, publishSM bool, injected ...control.StateBackend) (*haView, error) {
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
	if err := mgr.PreviewDeclaredFile(ctx, cfg.GitSourcesFile); err != nil {
		return nil, err
	}
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
	if !cfg.FleetUnregister {
		if err := applyStartupDisabledKinds(reg, accepted, cfg.StartupDisabledKinds); err != nil {
			return nil, err
		}
	}
	smStore, err := configuredSMStore(cfg, backend)
	if err != nil {
		return nil, err
	}
	var smState smHandoff
	smBound := ha.Bounded{Gate: gate, Timeout: 2 * time.Second, Margin: cfg.HAFenceMargin, Crash: bound.Crash}
	var smLiveBound func(func(context.Context) error) error
	if publishSM && cfg.StateBackend == "kubernetes" {
		smLiveBound = func(fn func(context.Context) error) error { return smBound.Run(context.Background(), ha.Mutation, fn) }
	}
	smAdmit := newSMAdmission(cfg, version, smStore, publishSM, !cfg.FleetUnregister && smStartupDisabled(cfg), smLiveBound)
	smPrepare := func(ctx context.Context) error {
		var err error
		smState, err = smAdmit.prepareBoot(ctx, accepted, store.Snapshot().LiveBlueprints, reg, blueprint.RuntimeLimits{MasterTick: cfg.MasterTick, MaxDPMPerSeries: cfg.MaxDPMPerSeries})
		return err
	}
	if publishSM && cfg.StateBackend == "kubernetes" {
		if err := smBound.Run(ctx, ha.Mutation, smPrepare); err != nil {
			return nil, err
		}
	} else if err := smPrepare(ctx); err != nil {
		return nil, err
	}
	v := &haView{store: store, manager: mgr, loaded: loaded, manifest: manifest, diagnostics: diags, observabilityReady: make(chan struct{}), smState: smState}
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
	fleetStore, err := configuredFleetStore(cfg, backend, gate)
	if err != nil {
		return nil, err
	}
	v.fleet = fleet.Config{Store: fleetStore, FMURL: cfg.FMURL, StackID: cfg.FMStackID, Token: cfg.FMToken, DryRun: cfg.DryRun, Delivery: delivery, Bounded: bound}
	v.runner = runner.New(sinks, reg, runner.Options{Gate: gate, Delivery: bound, MasterTick: cfg.MasterTick, MaxDPMPerSeries: cfg.MaxDPMPerSeries, TickTimeout: cfg.TickTimeout, Fleet: v.fleet, SendShards: cfg.SendShards, SendBatchMax: cfg.SendBatchMax, SendDeadline: cfg.SendDeadline, SendCapacity: cfg.SendCapacity, SendDrainDeadline: cfg.SendDrainDeadline, LiveAdmit: smAdmit.Admit})
	for _, bp := range accepted {
		if err := v.runner.AddBlueprint(bp); err != nil {
			return nil, err
		}
	}
	if v.runner.BlueprintCount() == 0 && mgr.SelectionRequested() {
		return nil, errors.New("no selected HA blueprints loaded")
	}
	v.runner.ApplyControl(store.Snapshot())
	// The restore ran inside ApplyControl's first call. Only the view built for leadership reports
	// it: a standby restores the same persisted set into its own read-only runner, and its skips
	// would describe a process that is not serving the fleet. During gate.Activate's prepare the
	// gate is not yet active, so in production publishSM decides; the role check covers gates that
	// always report leader.
	diag := control.NewDiagnostics()
	if publishSM || gate.Role() == ha.RoleLeader {
		for _, sw := range v.runner.ShapeWarnings() {
			diag.Add("warning", sw.Blueprint, "incident", sw.Message)
		}
		for _, skip := range v.runner.LiveRestoreSkips() {
			diag.Add("error", skip.Blueprint, "live", skip.Reason)
		}
	}
	ps := pushstatus.NewStore()
	fs := fleetstatus.NewStore()
	hs := healthstatus.NewStore()
	configure := func() {
		ps.ConfigureLanes(readinessLaneConfigs(v.runner.DeliveryReadinessLanes(), cfg.SendDeadline, cfg.DryRun))
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
		SetStartup(func() bool {
			// Installed before publication, only after build and preflight. Boot
			// completion is independent of election, delivery and transition state.
			if v.readinessFacts == nil {
				return false
			}
			facts := v.readinessFacts()
			return facts.ConfigLoaded && facts.RunnerBuilt && facts.PreflightPassed
		}).
		SetBasePath(cfg.ControlBasePath).
		SetManagedFeatures(cfg.ControlManagedFeatures).
		SetStatus(control.StatusSources{Sinks: ps.Snapshot, Queues: func() []pushstatus.QueueStat { return ps.SnapshotQueues(v.runner.QueueDepths()) }, ByBlueprint: ps.SnapshotByBlueprint, Fleet: fs.Snapshot, DryRun: cfg.DryRun, Readiness: readiness, Optional: func() []optionallane.Disposition {
			// The running set, not the boot list: restored and live-applied blueprints count too. A
			// standby reports its own read-only restore here, which can briefly differ from the
			// leader's set for a live blueprint whose SM needs the leader's handoff.
			facts := collectOptionalRuntimeFacts(v.runner.RunningResolved(), mgr.Sources(), cfg, v.smState)
			if gate.Role() != ha.RoleLeader {
				facts.smRegistered = false
			}
			return optionalRuntimeDispositions(cfg, facts, ps.SnapshotLanes(), fs.Snapshot(), v.runner.FleetRosterCount(), cfg.SelfObsEnabled, cfg.SelfObsEnabled && cfg.PyroscopeURL != "")
		}}).
		SetBlueprintAdmin(&haBlueprintAdmin{blueprintAdminAdapter: &blueprintAdminAdapter{mgr: mgr, sc: sc}}).SetInventory(v.runner).SetDiagnostics(diag).SetConfig(toControlConfigView(cfg.RedactedHA())).SetHealth(func() any { return healthReport(hs.Snapshot()) }).
		SetChangeObserver(func(s control.State) {
			configure()
			<-v.observabilityReady // still accounted by the admitted mutation
			if so := v.so.Load(); so != nil {
				so.EmitEvent("config_change", configChangeAttrs(s), configChangeBody(s))
			}
		})
	applyReadinessBlueprintFlags(handler, cfg, v.runner)
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
	so         *selfobs.SelfObs
	prof       interface{ Stop() error }
	restoreLog func()
}

// startHAOperational stamps each role provider with the process's election
// identity digest; leadership is the (session ID, ha.role=leader) resource.
func startHAOperational(cfg *config.Config, v *haView, role ha.Role, leaseIdentity string) (haOperational, error) {
	so, err := selfobs.Start(selfobs.Options{Enabled: cfg.SelfObsEnabled, Endpoint: cfg.SelfOTLPEndpoint, User: cfg.SelfOTLPUser, Password: cfg.SelfOTLPPassword, Tags: haRoleTags(selfobs.ParseTags(cfg.SelfObsTags), role), Version: version, MetricInterval: cfg.SelfObsMetricInterval, DryRun: cfg.DryRun, LeaseIdentity: leaseIdentity}, selfobs.Gauges{LedgerSize: v.runner.LedgerSize, VolumeMultiplier: v.runner.VolumeMultiplier, BlueprintCount: v.runner.BlueprintCount, QueueDepth: v.runner.QueueDepths, DeliveryManifest: deliveryManifestSource(v.runner, role == ha.RoleStandby, cfg.DryRun), Cardinality: func() []selfobs.CardinalityPoint {
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
	// Role providers are retired before the next one starts. Capture the original
	// writer once per provider, and detach before shutdown so SDK shutdown errors
	// cannot be sent back into the provider being closed. Copies share one restore.
	if err == nil && so.Enabled() {
		previous := log.Writer()
		writer := so.LogWriter()
		log.SetOutput(writer)
		var restored sync.Once
		ops.restoreLog = func() {
			restored.Do(func() {
				if log.Writer() == writer {
					log.SetOutput(previous)
				}
			})
		}
	}
	return ops, err
}
func (o haOperational) stop(ctx context.Context) error {
	if o.restoreLog != nil {
		o.restoreLog()
	}
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
	return runLeaseModeWith(cfg, once, productionHADependencies())
}

// runLeaseModeWith is runLeaseMode with explicit dependencies, sharing its signal handling.
func runLeaseModeWith(cfg *config.Config, once bool, deps haDependencies) error {
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
	return runHALifecycle(cfg, once, term, deps)
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
	standby, err := newHAView(prodCtx, cfg, gate, bound, false, injectedBackend)
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
	// One election identity per process, minted before any operational export so
	// standby and leader providers carry the digest of the value the Lease records.
	// Without a Pod UID (rejected by lease-mode config) attribution stays unknown.
	var identity string
	if cfg.PodUID != "" {
		if identity, err = lease.NewIdentity(cfg.PodUID); err != nil {
			prodCancel()
			return err
		}
	}
	standbyOps, err := startHAOperational(cfg, standby, ha.RoleStandby, identity)
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
	election, err := deps.Election(context.Background(), lease.Options{Namespace: cfg.HANamespace, Name: cfg.HALeaseName, PodUID: cfg.PodUID, Identity: identity, LeaseDuration: cfg.HALeaseDuration, RenewDeadline: cfg.HARenewDeadline, RetryPeriod: cfg.HARetryPeriod, RequestTimeout: cfg.HAKubeRequestTimeout, Stopped: crash, Started: func(leadCtx context.Context) {
		context.AfterFunc(leadCtx, prodCancel)
		l.mu.Lock()
		l.activating = true
		l.acquired = true
		l.mu.Unlock()
		var leader *haView
		err := gate.Activate(leadCtx, func(prepCtx context.Context) error {
			var err error
			leader, err = newHAView(prepCtx, cfg, gate, bound, !cfg.FleetUnregister, injectedBackend)
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
				if err := leader.manager.CommitDeclared(ctx); err != nil {
					return err
				}
				if err := leader.manager.CommitResolved(ctx, leader.manifest, leader.loaded, leader.diagnostics); err != nil {
					return err
				}
				if err := applyActivatedControl(ctx, cfg, leader); err != nil {
					return err
				}
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
		leaderOps, err := startHAOperational(cfg, leader, ha.RoleLeader, identity)
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
				var err error
				if cfg.FleetUnregister && deps.FleetTeardown != nil {
					err = deps.FleetTeardown(prodCtx, leader.fleet)
				} else if cfg.FleetUnregister {
					err = leader.runner.UnregisterFleet(prodCtx)
				} else {
					err = leader.runner.RunOnce(prodCtx, time.Now())
				}
				l.mu.Lock()
				l.producerError = err
				l.teardownCompleted = cfg.FleetUnregister && err == nil
				l.mu.Unlock()
				l.terminate()
				return
			}
			var wg sync.WaitGroup
			if cfg.GitSourcesFile != "" {
				wg.Add(1)
				go func() {
					defer wg.Done()
					leader.manager.WatchDeclared(prodCtx, cfg.GitSourcesFile, func(c context.Context, fn func(context.Context) error) error {
						return mutationBound.Run(c, ha.Mutation, fn)
					}, func(err error) { log.Printf("declarative git sources: %v", err) })
				}()
			}
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
		code := 0
		if cfg.FleetUnregister {
			code = 1 // interruption before leadership is not successful teardown
		}
		terminal.finish(code)
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
	// Planned restart preserves FM registration; only -fleet-unregister is teardown.
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
	teardownCompleted := l.teardownCompleted
	l.mu.Unlock()
	code := 0
	if producerError != nil || (cfg.FleetUnregister && !teardownCompleted) {
		code = 1
	}
	if deps.BeforeHandoff != nil {
		deps.BeforeHandoff("planned-exit")
	}
	terminal.finish(code)
	panic("HA terminal finish returned")
}
