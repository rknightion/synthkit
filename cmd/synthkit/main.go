// SPDX-License-Identifier: AGPL-3.0-only

// Command synthkit loads the blueprints explicitly selected from the configured sources,
// validates the set, and drives the two-cadence generator loop against Grafana Cloud sinks.
//
// Verification modes (ARCHITECTURE I32):
//
//	DRY_RUN=true synthkit -once -dump   # one full cycle; print the series/label inventory
//	synthkit -once                       # one cycle (live if DRY_RUN=false)
//	synthkit                             # the loop
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/rknightion/synthkit/internal/blueprint"
	"github.com/rknightion/synthkit/internal/blueprintschema"
	"github.com/rknightion/synthkit/internal/bpsource"
	"github.com/rknightion/synthkit/internal/config"
	"github.com/rknightion/synthkit/internal/control"
	"github.com/rknightion/synthkit/internal/fleet"
	"github.com/rknightion/synthkit/internal/fleethook"
	"github.com/rknightion/synthkit/internal/fleetstatus"
	"github.com/rknightion/synthkit/internal/healthstatus"
	"github.com/rknightion/synthkit/internal/inventory"
	"github.com/rknightion/synthkit/internal/jsondata"
	"github.com/rknightion/synthkit/internal/optionallane"
	"github.com/rknightion/synthkit/internal/preflight"
	"github.com/rknightion/synthkit/internal/profiling"
	"github.com/rknightion/synthkit/internal/pushhook"
	"github.com/rknightion/synthkit/internal/pushstatus"
	"github.com/rknightion/synthkit/internal/runner"
	"github.com/rknightion/synthkit/internal/selfobs"
	"github.com/rknightion/synthkit/internal/sink/faro"
	"github.com/rknightion/synthkit/internal/sink/loki"
	"github.com/rknightion/synthkit/internal/sink/otlp"
	"github.com/rknightion/synthkit/internal/sink/promrw"
	pyroscope "github.com/rknightion/synthkit/internal/sink/pyroscope"
	"github.com/rknightion/synthkit/internal/sink/queue"
	sigilsink "github.com/rknightion/synthkit/internal/sink/sigil"
)

// version is stamped onto self-profiling + self-observability data as service.version. Revision is
// the complete source commit. Published builds override both with -ldflags; local builds retain
// explicit development values rather than pretending to be a released artifact.
var (
	version  = "dev"
	revision = "unknown"
)

// queueObserverFan delivers one immutable queue event value to each consumer in order. The queue
// assigns shard/sequence before calling this fanout, so status and self-observability fold the
// same completion rather than independently ordering concurrent sender callbacks.
type queueObserverFan []queue.Observer

func (f queueObserverFan) EnqueueBlocked(sink string, d time.Duration) {
	for _, obs := range f {
		if obs != nil {
			obs.EnqueueBlocked(sink, d)
		}
	}
}

func (f queueObserverFan) FlushObserved(ev queue.FlushEvent) {
	for _, obs := range f {
		if obs != nil {
			obs.FlushObserved(ev)
		}
	}
}

func main() {
	once := flag.Bool("once", false, "run one full cycle and exit")
	dump := flag.Bool("dump", false, "with -once: print the full series/label inventory (diff vs signals/)")
	inventoryJSON := flag.Bool("inventory-json", false, "with -once in dry-run mode: write the canonical telemetry inventory as JSON")
	preflightCheck := flag.Bool("preflight", false, "validate and probe mandatory live Grafana endpoints, then exit")
	healthcheck := flag.Bool("healthcheck", false, "exit successfully only when the local control plane is delivery-ready")
	showVersion := flag.Bool("version", false, "print the release version and source revision as JSON, then exit")
	envPath := flag.String("env", ".env", "path to .env file (optional)")
	flag.Parse()
	if *showVersion {
		if err := json.NewEncoder(os.Stdout).Encode(struct {
			Version  string `json:"version"`
			Revision string `json:"revision"`
		}{Version: version, Revision: revision}); err != nil {
			log.Fatalf("synthkit version: %v", err)
		}
		return
	}
	if *healthcheck {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		endpoint, err := readinessEndpoint(os.Getenv("JSON_HTTP_ADDR"))
		if err != nil {
			log.Fatalf("synthkit healthcheck: %v", err)
		}
		if err := checkReadiness(ctx, &http.Client{Timeout: 4 * time.Second}, endpoint); err != nil {
			log.Fatalf("synthkit healthcheck: %v", err)
		}
		return
	}

	if *preflightCheck {
		if err := runPreflight(context.Background(), *envPath, os.Stdout, preflight.Options{}); err != nil {
			log.Fatalf("synthkit: %v", err)
		}
		return
	}
	if *inventoryJSON && (!*once || *dump) {
		log.Fatal("synthkit: -inventory-json requires -once and cannot be combined with -dump")
	}
	if err := runMode(*once, *dump, *inventoryJSON, *envPath); err != nil {
		log.Fatalf("synthkit: %v", err)
	}
}

func readinessEndpoint(bindAddr string) (string, error) {
	if bindAddr == "" {
		bindAddr = "127.0.0.1:8088"
	}
	host, port, err := net.SplitHostPort(bindAddr)
	if err != nil {
		return "", fmt.Errorf("invalid JSON_HTTP_ADDR %q: %w", bindAddr, err)
	}
	ip := net.ParseIP(host)
	if host == "" || (ip != nil && ip.IsUnspecified()) {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port) + "/control/readiness", nil
}

func checkReadiness(ctx context.Context, client *http.Client, endpoint string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("readiness returned HTTP %d", resp.StatusCode)
	}
	return nil
}

func runPreflight(ctx context.Context, envPath string, out io.Writer, opts preflight.Options) error {
	cfg, err := config.Load(envPath)
	if err != nil {
		return err
	}
	staticResults, err := preflight.Static(cfg)
	if err != nil {
		return err
	}
	if err := writePreflightResults(out, staticResults); err != nil {
		return err
	}
	optional, err := preflight.Optional(cfg)
	if err != nil {
		return err
	}
	if err := writeOptionalPreflightResults(out, optional); err != nil {
		return err
	}
	results, err := preflight.Check(ctx, cfg, opts)
	if err != nil {
		return err
	}
	if err := writePreflightResults(out, results); err != nil {
		return err
	}
	for _, result := range results {
		if result.State != preflight.StateReady {
			return fmt.Errorf("preflight: one or more mandatory lanes are not ready")
		}
	}
	return nil
}

func writeOptionalPreflightResults(out io.Writer, results []optionallane.Disposition) error {
	for _, result := range results {
		if len(result.MissingFields) == 0 {
			if _, err := fmt.Fprintf(out, "optional %s: %s (%s)\n", result.Lane, result.State, result.Reason); err != nil {
				return fmt.Errorf("preflight: write optional report: %w", err)
			}
			continue
		}
		fields := make([]string, 0, len(result.MissingFields))
		for _, field := range result.MissingFields {
			fields = append(fields, string(field))
		}
		if _, err := fmt.Fprintf(out, "optional %s: %s (%s; missing=%s)\n", result.Lane, result.State, result.Reason, strings.Join(fields, ",")); err != nil {
			return fmt.Errorf("preflight: write optional report: %w", err)
		}
	}
	return nil
}

func writePreflightResults(out io.Writer, results []preflight.Result) error {
	for _, result := range results {
		if result.Reason == "" {
			if _, err := fmt.Fprintf(out, "%s: %s\n", result.Lane, result.State); err != nil {
				return fmt.Errorf("preflight: write report: %w", err)
			}
			continue
		}
		if _, err := fmt.Fprintf(out, "%s: %s (%s)\n", result.Lane, result.State, result.Reason); err != nil {
			return fmt.Errorf("preflight: write report: %w", err)
		}
	}
	return nil
}

func run(once, dump bool, envPath string) error {
	return runMode(once, dump, false, envPath)
}

func runMode(once, dump, inventoryJSON bool, envPath string) error {
	cfg, err := config.Load(envPath)
	if err != nil {
		return err
	}
	if err := cfg.ValidateLive(); err != nil {
		return err
	}
	if inventoryJSON && !cfg.DryRun {
		return fmt.Errorf("-inventory-json requires DRY_RUN=true")
	}
	if err := validateControlExposure(controlExposure{
		HTTPAddr: cfg.HTTPAddr,
		HostBind: cfg.HostBind,
		Token:    cfg.ControlToken,
		Ack:      cfg.ControlExposure,
	}, inContainer(), !once); err != nil {
		return fmt.Errorf("control exposure: %w", err)
	}

	// Self-profiling (Pyroscope → a SEPARATE self-obs stack). Process profiles are just another
	// self-obs signal, so they share SELFOBS_ENABLED (no separate master switch) independently of
	// synthetic DRY_RUN. A no-op when its
	// own GC_PYROSCOPE_* creds are absent. Stop() on shutdown flushes the final profile.
	prof, perr := profiling.Start(profiling.Options{
		Enabled:       cfg.SelfObsEnabled,
		URL:           cfg.PyroscopeURL,
		User:          cfg.PyroscopeUser,
		Password:      cfg.PyroscopePassword,
		Tags:          profiling.ParseTags(cfg.PyroscopeTags),
		MutexFraction: cfg.PyroscopeMutexFraction,
		BlockRate:     cfg.PyroscopeBlockRate,
		Version:       version,
	})
	if perr != nil {
		log.Printf("profiling: failed to start: %v", perr)
	}
	// Install shutdown before any later startup step can fail. In particular, an invalid blueprint
	// selection returns below after profiling has started; it must still flush and stop the process
	// profiler through the same bounded path as normal shutdown.
	var (
		so   *selfobs.SelfObs
		serr error
	)
	defer func() {
		done := make(chan struct{})
		go func() {
			defer close(done)
			if so != nil {
				so.Shutdown(context.Background())
			}
			if prof != nil {
				if err := profiling.StopWithTimeout(prof, 8*time.Second); err != nil {
					log.Printf("profiling: %v", err)
				}
			}
		}()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			log.Printf("synthkit: shutdown deadline (10s) exceeded — forcing exit")
		}
	}()
	capFn := func() int { return cfg.SeriesCap }
	prom := promrw.New(cfg.PromRWURL, cfg.PromUser, cfg.Token, cfg.DryRun, capFn)
	lokiSink := loki.New(cfg.LokiURL, cfg.LokiUser, cfg.Token, cfg.DryRun)
	otlpSink := otlp.New(cfg.OTLPEndpoint, cfg.OTLPUser, cfg.Token, cfg.DryRun)
	otlpMetricsSink := otlp.NewMetrics(cfg.OTLPEndpoint, cfg.OTLPUser, cfg.Token, cfg.DryRun)
	otlpLogsSink := otlp.NewLogs(cfg.OTLPEndpoint, cfg.OTLPUser, cfg.Token, cfg.DryRun)
	sinks := runner.Sinks{Metrics: prom, Logs: lokiSink, Traces: otlpSink, OTLPMetrics: otlpMetricsSink, OTLPLogs: otlpLogsSink}
	var faroSink *faro.Sink
	if cfg.RUMEnabled() {
		faroSink = faro.New(cfg.FaroCollector, cfg.FaroAppKey, cfg.DryRun)
		sinks.RUM = faroSink
	}
	var profSink *pyroscope.Sink
	if cfg.SynthProfilesEnabled() || cfg.DryRun {
		profSink = pyroscope.New(cfg.ProfilesURL, cfg.ProfilesUser, cfg.Token, cfg.DryRun)
		sinks.Profiles = profSink
	}
	var sigilSink *sigilsink.Sink
	if cfg.SigilEnabled() || cfg.DryRun {
		s, serr := sigilsink.New(cfg.SigilEndpoint, cfg.SigilTenantID, cfg.SigilToken, cfg.DryRun)
		if serr != nil {
			return fmt.Errorf("sigil sink: %w", serr)
		}
		sigilSink = s
		sinks.Sigil = s
	}
	if inventoryJSON {
		prom.Capture = true
		lokiSink.Capture = true
		otlpSink.Capture = true
		otlpMetricsSink.Capture = true
		otlpLogsSink.Capture = true
		if profSink != nil {
			profSink.Capture = true
		}
	}

	reg := runner.Catalog()

	// Control plane store is built HERE (before blueprint resolution) so the source-config
	// adapter can read/write git source state during Resolve. ApplyControl runs after AddBlueprint
	// (below) — but the store itself is safe to construct first; Snapshot() is idempotent.
	store := control.NewStore(cfg.SnapshotPath)
	if err := store.ProbeWrite(); err != nil {
		log.Printf("control: startup state-volume probe failed: %v", err)
	}

	// diag collects load-time problems (skipped blueprints, dropped config entries) so they surface
	// in the control UI's diagnostics panel instead of only in stderr.
	diag := control.NewDiagnostics()

	// Build the git client and blueprint source manager.
	// tokenLookup: source's TokenEnvVar names the env var; empty name falls back to GIT_TOKEN.
	tokenLookup := func(name string) string {
		if name == "" {
			return cfg.GitTokenDefault
		}
		return os.Getenv(name)
	}
	gitClient := bpsource.NewNanogitClient(tokenLookup)
	sc := bpsource.NewStoreSourceConfig(store)
	mgr := bpsource.NewManager(bpsource.Options{
		BakedDir:       cfg.BlueprintsDir,
		BlueprintNames: cfg.BlueprintNames,
		DataDir:        cfg.BlueprintDataDir,
		Registry:       reg,
		Git:            gitClient,
		Config:         sc,
		Now:            func() int64 { return time.Now().UnixMilli() },
	})

	// Resolve loads built-ins + custom uploads + on-disk git blobs, fetching any configured
	// git sources (degrade-on-error). Replaces the old filepath.Glob + blueprint.Load loop.
	loaded, _, resolveDiags := mgr.Resolve(context.Background())
	if err := mgr.SelectionError(); err != nil {
		return err
	}
	skippedBlueprints := 0
	for _, d := range resolveDiags {
		diag.Add(d.Severity, d.Source, d.Stage, d.Detail)
		if d.Severity == "error" {
			skippedBlueprints++
		}
	}

	if len(loaded) == 0 && mgr.SelectionRequested() {
		return fmt.Errorf("no blueprints loaded successfully from %s (see diagnostics/logs)", cfg.BlueprintsDir)
	}
	selectedNames := make([]string, 0, len(loaded))
	for _, l := range loaded {
		selectedNames = append(selectedNames, l.Resolved.Name)
	}
	sort.Strings(selectedNames)
	log.Printf("selected blueprints: %d %v", len(selectedNames), selectedNames)
	if !mgr.SelectionRequested() {
		log.Printf("WARNING: no blueprints selected — synthkit is running in setup mode and emits no synthetic telemetry; set BLUEPRINT_NAMES=otlp-native (or another exact name) and restart; use BLUEPRINT_NAMES=* only to load the complete catalog")
	}
	// Log warnings for all loaded blueprints before validation.
	for _, l := range loaded {
		res := l.Resolved
		for _, w := range res.Warnings {
			diag.Add("warning", res.Name, "resolve", w)
		}
		log.Printf("loaded blueprint %q (provenance=%s): %d constructs, %d workloads", res.Name, l.Provenance, len(res.Constructs), len(res.Workloads))
	}
	// foldValidated validates built-ins as a set (fatal), then incrementally folds each
	// custom/git blueprint into the accepted set, skipping (with a diagnostic) any that collide.
	accepted, builtinErr, skipped := foldValidated(loaded)
	if builtinErr != nil {
		return builtinErr
	}
	for _, d := range skipped {
		log.Printf("ERROR skipping blueprint %q: %v", d.Source, d.Detail)
		diag.Add(d.Severity, d.Source, d.Stage, d.Detail)
		skippedBlueprints++
	}
	// The SM handoff is resolved before runner construction. It always writes the exact current
	// snapshot for a selected SM lane and removes SM constructs unless registration matches it.
	smState, err := prepareSMHandoff(accepted, cfg, version, time.Now())
	if err != nil {
		return err
	}
	r := runner.New(sinks, reg, runner.Options{
		MasterTick:  cfg.MasterTick,
		TickTimeout: cfg.TickTimeout,
		Fleet: fleet.Config{
			FMURL: cfg.FMURL, StackID: cfg.FMStackID, Token: cfg.FMToken, DryRun: cfg.DryRun,
		},
		SendShards: cfg.SendShards, SendBatchMax: cfg.SendBatchMax, SendDeadline: cfg.SendDeadline,
		SendCapacity: cfg.SendCapacity, SendDrainDeadline: cfg.SendDrainDeadline,
	})
	loadedBlueprints := 0
	runtimeResolved := make([]*blueprint.Resolved, 0, len(accepted))
	for _, res := range accepted {
		if err := r.AddBlueprint(res); err != nil {
			log.Printf("ERROR skipping blueprint %q: %v", res.Name, err)
			diag.Add("error", res.Name, "load", err.Error())
			skippedBlueprints++
			continue
		}
		loadedBlueprints++
		runtimeResolved = append(runtimeResolved, res)
	}
	// Degrade has a floor: if every blueprint was skipped (load OR add), there is nothing to run —
	// fail rather than boot a silent do-nothing instance.
	if r.BlueprintCount() == 0 && mgr.SelectionRequested() {
		return fmt.Errorf("no blueprints could be loaded/added from %s (see diagnostics/logs)", cfg.BlueprintsDir)
	}
	// Shape-engine incident-schedule warnings collected while wiring blueprints (bad/over-long
	// incident entries the engine skipped).
	for _, sw := range r.ShapeWarnings() {
		diag.Add("warning", sw.Blueprint, "incident", sw.Message)
	}

	// Apply the persisted control snapshot before the first tick.
	r.ApplyControl(store.Snapshot())
	// Self-observability (OTLP → a SEPARATE self-obs stack). The generator's OWN operability
	// telemetry, on a separate stack + credential triplet from all synthetic data — a no-op when
	// disabled/under-configured. Started AFTER the blueprints + control snapshot so its gauges see a
	// populated runner; the sink Observe fields + the runner tick seam are wired BEFORE any tick runs
	// (no data race). Gauges are plain callbacks, so selfobs never imports the runner/control package.
	// Self-observability describes synthkit itself and is independent of the synthetic DRY_RUN
	// switch. SELFOBS_ENABLED plus the complete private credential triplet is the only activation.
	so, serr = selfobs.Start(selfobs.Options{
		Enabled:        cfg.SelfObsEnabled,
		Endpoint:       cfg.SelfOTLPEndpoint,
		User:           cfg.SelfOTLPUser,
		Password:       cfg.SelfOTLPPassword,
		Tags:           selfobs.ParseTags(cfg.SelfObsTags),
		Version:        version,
		MetricInterval: cfg.SelfObsMetricInterval,
		DryRun:         cfg.DryRun, // stamped as the synthetic run mode; self-obs still exports
	}, selfobs.Gauges{
		LedgerSize:       r.LedgerSize,
		VolumeMultiplier: r.VolumeMultiplier,
		BlueprintCount:   r.BlueprintCount,
		Cardinality: func() []selfobs.CardinalityPoint {
			rep := r.Inventory()
			var out []selfobs.CardinalityPoint
			for _, bp := range rep.Blueprints {
				for _, c := range bp.Constructs {
					out = append(out, selfobs.CardinalityPoint{Blueprint: bp.Blueprint, Kind: c.Kind, Name: c.Name, Distinct: c.DistinctSeries})
				}
			}
			return out
		},
		QueueDepth: r.QueueDepths,
	})
	if serr != nil {
		log.Printf("selfobs: failed to start: %v", serr)
	}
	// Fan every push event out to BOTH the selfobs observer (nil when disabled) and the
	// pushstatus store (always non-nil → sinks are always instrumented for the control-plane
	// status view). pushstatus and selfobs both consume each event without coupling.
	ps := pushstatus.NewStore()
	configureReadinessLanes := func() {
		lanes := r.DeliveryReadinessLanes()
		configs := make([]pushstatus.LaneConfig, 0, len(lanes))
		for _, lane := range lanes {
			lc := pushstatus.LaneConfig{Name: lane.Name, FreshAfter: lane.EmissionInterval + cfg.SendDeadline}
			if cfg.DryRun {
				lc.Disabled = true
				lc.DisabledReason = "dry_run"
			}
			configs = append(configs, lc)
		}
		ps.ConfigureLanes(configs)
	}
	configureReadinessLanes()
	fan := func(a, b pushhook.Observer) pushhook.Observer {
		return func(ctx context.Context, ev pushhook.Event) {
			if a != nil {
				a(ctx, ev)
			}
			if b != nil {
				b(ctx, ev)
			}
		}
	}
	obs := fan(so.PushObserver(), ps.Observer())
	prom.Observe, lokiSink.Observe, otlpSink.Observe, otlpMetricsSink.Observe = obs, obs, obs, obs
	if faroSink != nil {
		faroSink.Observe = obs
	}
	if profSink != nil {
		profSink.Observe = obs
	}
	if sigilSink != nil {
		sigilSink.Observe = obs
	}
	// Same fan-out for the FM lifecycle: selfobs exports FM health to the staff stack, fleetstatus
	// feeds the control-plane TELEMETRY panel. Both consume each fleethook event without coupling.
	fs := fleetstatus.NewStore()
	fleetFan := func(a, b fleethook.Observer) fleethook.Observer {
		return func(ctx context.Context, ev fleethook.Event) {
			if a != nil {
				a(ctx, ev)
			}
			if b != nil {
				b(ctx, ev)
			}
		}
	}
	r.SetFleetObserver(fleetFan(so.FleetObserver(), fs.Observer()))
	hs := healthstatus.NewStore()
	// Tick fan-out: selfobs.ObserveTick wraps fn (span + metric; transparent pass-through when
	// disabled) and runs it exactly once; we capture fn's error and record duration+outcome to the
	// in-process health store. healthstatus never re-runs fn.
	tickFan := func(ctx context.Context, bp, kind, name string, fn func(context.Context) error) error {
		start := time.Now()
		var ferr error
		err := so.ObserveTick(ctx, bp, kind, name, func(c context.Context) error { ferr = fn(c); return ferr })
		hs.RecordOutcome(bp, kind, name, time.Since(start), ferr)
		return err
	}
	r.SetTickObserver(tickFan)
	cycleFan := func(ctx context.Context, bp string, dur time.Duration, dropped int) {
		so.ObserveCycle(ctx, bp, dur, dropped)
		hs.ObserveCycle(ctx, bp, dur, dropped)
	}
	r.SetCycleObserver(cycleFan)                 // per-blueprint cycle-duration + dropped-tick metrics; no-op when disabled
	r.SetQueueObserver(queueObserverFan{so, ps}) // identical loss/backpressure events feed selfobs and authenticated status
	if so.Enabled() {
		// Tee the std log to OTLP so the operational log stream ships to the self-obs stack. Only
		// when enabled — otherwise local runs are byte-for-byte unchanged (stderr only).
		log.SetOutput(so.LogWriter())
	}

	mode := "LIVE"
	if cfg.DryRun {
		mode = "DRY_RUN"
	}
	log.Printf("synthkit up: %d blueprints %v, master tick %v, mode %s", r.BlueprintCount(), r.Blueprints(), cfg.MasterTick, mode)

	if once {
		if err := r.RunOnce(context.Background(), time.Now()); err != nil {
			return err
		}
		if dump {
			printInventory(prom, lokiSink, otlpSink, otlpLogsSink, profSink, sigilSink)
		}
		if inventoryJSON {
			schema := withSynthProvenance(inventory.FromSinks(prom, lokiSink, otlpSink, otlpMetricsSink, otlpLogsSink, profSink, sigilSink))
			if err := schema.WriteJSON(os.Stdout); err != nil {
				return fmt.Errorf("inventory JSON: %w", err)
			}
		}
		return nil
	}

	// One handler serves the control plane AND the Infinity JSON host over the HTTP listener
	// (I26: GETs are side-effect-free; CORS echoes request headers). Mutating POSTs require
	// CONTROL_TOKEN (H2). Operators front the plain-HTTP bind with `tailscale serve` for a
	// browser-trusted endpoint; Grafana Cloud reaches it privately via the user-configured
	// PDC Tailscale connection.
	if cfg.ControlToken == "" {
		log.Printf("WARNING: CONTROL_TOKEN unset — control-plane reads and mutations are unauthenticated on the verified loopback-only exposure")
	}
	mux := http.NewServeMux()
	cv := toControlConfigView(cfg.Redacted())
	adapter := &blueprintAdminAdapter{mgr: mgr, sc: sc}
	readiness := func() control.ReadinessReport {
		persist := store.PersistHealth()
		return control.EvaluateReadiness(control.ReadinessInput{
			ProcessRunning: true,
			HTTPServing:    true,
			SetupRequired:  !mgr.SelectionRequested(),
			Blueprints: control.BlueprintReadiness{
				Loaded: loadedBlueprints, Skipped: skippedBlueprints, Active: r.ActiveBlueprintCount(),
			},
			PersistedState: control.PersistedStateReadiness{
				Writable: persist.LastOKMs > 0 && persist.LastError == "", Error: persist.LastError,
			},
			Lanes:                ps.SnapshotLanes(),
			LiveDeliveryExpected: !cfg.DryRun,
		})
	}
	mux.Handle("/control/", control.NewHandler(store, r.ApplyControl, cfg.ControlToken, r).
		SetStatus(control.StatusSources{
			Sinks:       ps.Snapshot,
			Queues:      func() []pushstatus.QueueStat { return ps.SnapshotQueues(r.QueueDepths()) },
			ByBlueprint: ps.SnapshotByBlueprint, Fleet: fs.Snapshot, Readiness: readiness,
			Optional: func() []optionallane.Disposition {
				facts := collectOptionalRuntimeFacts(runtimeResolved, sc.Sources(), cfg, smState)
				return optionalRuntimeDispositions(cfg, facts, ps.SnapshotLanes(), fs.Snapshot(), r.FleetRosterCount(), so.Enabled(), prof != nil && perr == nil)
			},
			DryRun: cfg.DryRun,
		}).
		SetBlueprintSchema(blueprintschema.JSON(runner.Catalog())).
		SetConfig(cv).
		SetInventory(r).
		SetHealth(func() any { return healthReport(hs.Snapshot()) }).
		SetDiagnostics(diag).
		SetBlueprintAdmin(adapter).
		SetChangeObserver(func(s control.State) {
			configureReadinessLanes()
			so.EmitEvent("config_change", configChangeAttrs(s), configChangeBody(s))
		}))
	mux.Handle("/", jsonHost(r, cfg.ControlToken))
	newSrv := func(addr string) *http.Server {
		return &http.Server{
			Addr:              addr,
			Handler:           mux,
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       15 * time.Second,
			WriteTimeout:      15 * time.Second,
			IdleTimeout:       60 * time.Second,
		}
	}
	srv := newSrv(cfg.HTTPAddr)
	go func() {
		log.Printf("control + Infinity JSON host (HTTP) on %s (/control/ui for the operator UI)", cfg.HTTPAddr)
		if herr := srv.ListenAndServe(); herr != nil && herr != http.ErrServerClosed {
			log.Printf("http host: %v", herr)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Background git poll: refreshes latestSHAs so /control/blueprints/pending can show
	// "update available" without inline git I/O on the request path. Off by default (0).
	// Deferred follow-up: POST /control/restart for live apply without container bounce.
	if cfg.GitPollInterval > 0 {
		pollInterval := time.Duration(cfg.GitPollInterval) * time.Second
		go func() {
			ticker := time.NewTicker(pollInterval)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					mgr.PollSources(ctx)
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	err = r.Run(ctx)
	// Graceful drain of in-flight HTTP requests, bounded so a wedged connection cannot hang exit.
	shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	_ = srv.Shutdown(shutCtx)
	cancel()
	if err == context.Canceled {
		return nil
	}
	return err
}

func jsonHost(src jsondata.Source, token string) http.Handler {
	data := jsondata.NewServer(src)
	mux := http.NewServeMux()
	mux.Handle("/healthz", data)
	protected := control.RequireToken(token, data)
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			data.ServeHTTP(w, r)
			return
		}
		protected.ServeHTTP(w, r)
	}))
	return mux
}

// isLoopback reports whether addr's host is a loopback address. addr is host:port. An empty host
// (":8088") or 0.0.0.0 binds ALL interfaces — NOT loopback — so it returns false and the caller
// validates exposure against the effective host-side publish address.
// inContainer reports whether the process is running inside a container, where
// loopback detection on the in-container bind is meaningless (the bind is
// necessarily 0.0.0.0; real exposure is the host port mapping). Docker writes
// /.dockerenv; we also honor an explicit hint env var.
func inContainer() bool {
	if os.Getenv("SYNTHKIT_IN_CONTAINER") != "" {
		return true
	}
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}
	return false
}

func isLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// toControlConfigView maps config.RedactedConfig to control.ConfigView field-for-field.
// config never imports control; control never imports config; the composition root does the mapping.
func toControlConfigView(rc config.RedactedConfig) control.ConfigView {
	groups := make([]control.ConfigGroup, 0, len(rc.Groups))
	for _, g := range rc.Groups {
		fields := make([]control.ConfigField, 0, len(g.Fields))
		for _, f := range g.Fields {
			fields = append(fields, control.ConfigField{
				Key:        f.Key,
				Value:      f.Value,
				Secret:     f.Secret,
				Configured: f.Configured,
			})
		}
		groups = append(groups, control.ConfigGroup{Title: g.Title, Fields: fields})
	}
	return control.ConfigView{Groups: groups}
}

// processMetrics is the live Go process snapshot embedded in the health payload.
type processMetrics struct {
	Goroutines int    `json:"goroutines"`
	HeapBytes  uint64 `json:"heap_bytes"`
	GCCount    uint32 `json:"gc_count"`
}

// healthPayload is the full /control/health body: the healthstatus.Report plus live process metrics.
type healthPayload struct {
	healthstatus.Report
	Process processMetrics `json:"process"`
}

// healthReport merges a healthstatus.Report with live Go runtime process metrics.
func healthReport(rep healthstatus.Report) healthPayload {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return healthPayload{
		Report: rep,
		Process: processMetrics{
			Goroutines: runtime.NumGoroutine(),
			HeapBytes:  ms.HeapAlloc,
			GCCount:    ms.NumGC,
		},
	}
}

// printInventory prints the FULL distinct series-name + label-key inventory (I32 —
// never just batch[0]) for offline diff against signals/. Explicit stdout writes;
// never redirect generator output into an artifact (I28).
func printInventory(prom *promrw.Sink, lokiSink *loki.Sink, otlpSink *otlp.Sink, otlpLogsSink *otlp.LogsSink, profSink *pyroscope.Sink, sigilSink *sigilsink.Sink) {
	fmt.Println("== metrics: series name → label keys ==")
	inv := prom.Inventory()
	names := make([]string, 0, len(inv))
	for n := range inv {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		fmt.Printf("%s  {%v}\n", n, inv[n])
	}
	fmt.Printf("== metrics: %d distinct series names ==\n\n", len(names))

	streamInv, metaInv := lokiSink.Inventory()
	fmt.Println("== logs: source → stream labels / structured metadata ==")
	sources := make([]string, 0, len(streamInv))
	for s := range streamInv {
		sources = append(sources, s)
	}
	sort.Strings(sources)
	for _, s := range sources {
		fmt.Printf("%s  stream=%v meta=%v\n", s, streamInv[s], metaInv[s])
	}
	fmt.Println()

	if otlpLogsSink != nil {
		logResAttrs, logRecAttrs := otlpLogsSink.Inventory()
		fmt.Println("== otlp logs (transport otlp_logs): service → resource attrs / record attrs ==")
		logSvcs := make([]string, 0, len(logResAttrs))
		for s := range logResAttrs {
			logSvcs = append(logSvcs, s)
		}
		sort.Strings(logSvcs)
		for _, s := range logSvcs {
			fmt.Printf("%s\n  resource=%v\n  record=%v\n", s, logResAttrs[s], logRecAttrs[s])
		}
		fmt.Println()
	}

	resAttrs, spanNames, spanAttrs := otlpSink.Inventory()
	fmt.Println("== traces: service → resource attrs / span names / span attrs ==")
	services := make([]string, 0, len(resAttrs))
	for s := range resAttrs {
		services = append(services, s)
	}
	sort.Strings(services)
	for _, s := range services {
		fmt.Printf("%s\n  resource=%v\n  spans=%v\n  attrs=%v\n", s, resAttrs[s], spanNames[s], spanAttrs[s])
	}

	if sigilSink != nil {
		si := sigilSink.Inventory()
		fmt.Println()
		fmt.Println("== sigil: ingest kind → operation names ==")
		if si.Generations > 0 {
			fmt.Printf("generations  ops=%v\n", si.OperationNames)
		}
		if si.WorkflowSteps > 0 {
			fmt.Printf("workflow_steps  ops=[]\n")
		}
		if si.Scores > 0 {
			fmt.Printf("scores  ops=[]\n")
		}
		fmt.Printf("== sigil: generations=%d workflow_steps=%d scores=%d ==\n", si.Generations, si.WorkflowSteps, si.Scores)
	}

	if profSink == nil {
		return
	}
	fmt.Println()
	profInv := profSink.Inventory()
	fmt.Println("=== PYROSCOPE === profile type → label keys ==")
	profileTypes := make([]string, 0, len(profInv))
	for pt := range profInv {
		profileTypes = append(profileTypes, pt)
	}
	sort.Strings(profileTypes)
	for _, pt := range profileTypes {
		fmt.Printf("%s  {%v}\n", pt, profInv[pt])
	}
	fmt.Printf("=== PYROSCOPE: %d distinct profile types ===\n", len(profileTypes))
}

// configChangeAttrs distills a control.State mutation into bounded, numeric structured attributes
// for the self-obs config_change event (operator audit trail) — counts only, never high-card lists,
// and never an empty value (an absent dimension is omitted, not "").
func configChangeAttrs(s control.State) map[string]string {
	return map[string]string{
		"volume_multiplier":   fmt.Sprintf("%.2f", s.VolumeMultiplier),
		"failures_active":     fmt.Sprint(countEnabledFailures(s.Failures)),
		"disabled_blueprints": fmt.Sprint(len(s.DisabledBlueprints)),
		"disabled_constructs": fmt.Sprint(len(s.DisabledConstructs)),
		"disabled_kinds":      fmt.Sprint(len(s.DisabledKinds)),
		"active_scenarios":    fmt.Sprint(len(s.ActiveScenarios)),
		"span_metrics_bps":    fmt.Sprint(len(s.SpanMetricsBlueprints)),
	}
}

// configChangeBody renders the one-line human summary of a control mutation for the log body. The
// active failure-mode names go here (the body) rather than a high-card attribute.
func configChangeBody(s control.State) string {
	active := make([]string, 0, len(s.Failures))
	for mode, f := range s.Failures {
		if f.Enabled {
			active = append(active, mode)
		}
	}
	sort.Strings(active)
	failures := "none"
	if len(active) > 0 {
		failures = strings.Join(active, ",")
	}
	return fmt.Sprintf("selfobs: config_change volume=%.2f disabled_bp=%d disabled_constructs=%d disabled_kinds=%d scenarios=%d failures=[%s]",
		s.VolumeMultiplier, len(s.DisabledBlueprints), len(s.DisabledConstructs), len(s.DisabledKinds), len(s.ActiveScenarios), failures)
}

// foldValidated validates built-ins as a set (fatal on collision), then folds each
// custom/git blueprint into the accepted set one at a time, skipping (with a diagnostic)
// any whose substrate identity collides with the already-accepted set. This implements the
// degrade-and-continue rule: a colliding upload or git blueprint is dropped with an error
// diagnostic, but built-ins (and previously accepted blueprints) keep running.
func foldValidated(loaded []bpsource.Loaded) (accepted []*blueprint.Resolved, builtinErr error, skipped []bpsource.Diag) {
	var builtins []*blueprint.Resolved
	var extra []bpsource.Loaded
	for _, l := range loaded {
		if l.Provenance == bpsource.ProvBuiltin {
			builtins = append(builtins, l.Resolved)
		} else {
			extra = append(extra, l)
		}
	}
	if err := blueprint.ValidateSet(builtins); err != nil {
		return nil, err, nil // built-in collision is a shipped-image bug — fatal
	}
	accepted = append(accepted, builtins...)
	for _, l := range extra {
		candidate := make([]*blueprint.Resolved, len(accepted)+1)
		copy(candidate, accepted)
		candidate[len(accepted)] = l.Resolved
		if err := blueprint.ValidateSet(candidate); err != nil {
			skipped = append(skipped, bpsource.Diag{
				Severity: "error",
				Source:   l.Resolved.Name,
				Stage:    "validate",
				Detail:   err.Error(),
			})
			continue
		}
		accepted = append(accepted, l.Resolved)
	}
	return accepted, nil, skipped
}

// countEnabledFailures counts the live failure modes currently toggled on.
func countEnabledFailures(f map[string]control.FailureSetting) int {
	n := 0
	for _, v := range f {
		if v.Enabled {
			n++
		}
	}
	return n
}
