// SPDX-License-Identifier: AGPL-3.0-only

// Package webservice implements the v1 "web_service" workload (ARCHITECTURE §2 Workload
// seam, kind="web_service"). It is the correlation-critical request-correlation lane: a
// browser→backend→DB request tree threaded by one ledger-minted correlation key-set
// across span-metrics, traces, logs, and (optionally) Faro/RUM beacons.
//
// Kind: "web_service"
// Signals: Metrics + Traces + Logs (+ RUM when the binding carries a Faro sink and cfg.RUM is set).
// Interval: 60s (metric lane; the DPM floor — I10).
//
// Two cadences (ARCHITECTURE §4 / I10):
//   - Minter() mints the correlated NARRATIVE SAMPLE per master tick — a small,
//     cadence-invariant volume that drives the trace/log/RUM story (NOT full RPS).
//   - Tick() is the metric lane: span-metrics + service-graph volume comes from the
//     traffic config (rps × shape × interval), mirroring the predecessor's app_apm.go which
//     separates aggregate metric VOLUME from the ledger SAMPLE (see metrics.go).
//   - ProjectBatch() is handed exactly its own minted batch and emits traces/logs/RUM
//     ONCE by construction.
//
// DE-AI'd span tree (v1): optional browser CLIENT root (browser-origin requests) →
// backend SERVER span (service.name = instance name) → one CLIENT span per
// ledger.Request.Call (db/cache hop with db.* semconv). There are NO
// invoke_workflow/invoke_agent/execute_tool/retrieval/chat spans, and NO AI/LLM
// semantic-convention attributes anywhere — the AI/LLM tree stays in the predecessor.
//
// Signal contract: signals/apm.md (+ signals/traces.md, signals/logs.md)
// Predecessor reference (READ-ONLY): generator/internal/emit/{app_apm,app_logs,rum,tracetree,tracetiming}.go
package webservice

import (
	"context"
	"fmt"
	"time"

	"github.com/rknightion/synthkit/internal/beyla"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/ledger"
	"github.com/rknightion/synthkit/internal/pyroscope"
	"github.com/rknightion/synthkit/internal/state"
)

const (
	kind     = "web_service"
	interval = 60 * time.Second

	// serviceVersion is the semver stamped on service.version / service_version across
	// every signal (the predecessor pins a single ServiceVersion constant).
	serviceVersion = "1.0.0"

	// backendSDKLanguage / browserSDKLanguage are the telemetry_sdk_language values for
	// the two resources in the tree.
	backendSDKLanguage = "go"
	browserSDKLanguage = "webjs"

	// source is the Tempo-derived APM series source label (always "tempo").
	source = "tempo"

	// frontendNamespace is the k8s namespace for the browser/RUM service resource.
	frontendNamespace = "ui"

	// faroDistroName is the telemetry.distro.name on the browser resource.
	faroDistroName = "faro-web-sdk"
	// faroSDKName / faroSDKVersion identify the Faro Web SDK in beacon meta.
	faroSDKName    = "faro-web"
	faroSDKVersion = "2.7.0"
)

// Config is the YAML config struct for the web_service workload. Unknown fields are
// rejected by strict yaml.v3 decoding at blueprint load (control-plane JSON round-trips
// zero/false — no omitempty on knobs, I24).
type Config struct {
	// Tracing enables the OTLP trace lane. Default true (see NewConfig).
	Tracing bool `yaml:"tracing"`
	// RUM enables the Faro/RUM lane (also requires the binding to carry a Faro sink).
	RUM bool `yaml:"rum"`
	// Traffic shapes the metric-lane request VOLUME (not the ledger sample).
	Traffic Traffic `yaml:"traffic"`
	// Endpoints is the route catalogue; requests are drawn uniformly across it.
	Endpoints []Endpoint `yaml:"endpoints"`
	// Observability is the additive emission-switch for the Beyla observation lane. Absent
	// (nil) ⇒ the workload keeps its existing source="tempo" behavior unchanged. When
	// .Beyla is set, the Beyla lane renders this workload's ledger traffic in Beyla's
	// surface (mode + context discriminated). Strict yaml decoding rejects unknown keys.
	Observability *struct {
		Beyla *BeylaObs `yaml:"beyla"`
	} `yaml:"observability"`
	// Pyroscope enables the SDK-push continuous-profiling lane. Absent (nil) or Enabled=false ⇒
	// no profiling emission. Mode="scraped" ⇒ Alloy scrapes the service; we do not push profiles
	// ourselves (Signals() omits PyroscopeProfiles so the runner never wires the sink).
	Pyroscope *pyroscope.ProfilingCfg `yaml:"pyroscope"`
	// OTel enables the native OTLP application-metrics lane (http.server.* via /v1/metrics).
	// Absent (nil) or Metrics=false ⇒ no OTLP-metrics emission (the workload keeps promrw span-metrics).
	OTel *OTelObs `yaml:"otel"`
	// Context / UseCase / Team are the §5 resource-attr canon (optional; AI blueprints only).
	// Empty ⇒ OMITTED (I13). Context ∈ {Platform, ContentGen, DataGen}; Team is set per blueprint (blueprint-only identity).
	// ⚠ This is the TOP-LEVEL context (the §5 canon) — distinct from BeylaObs.Context (ebpf_only|coexist_sdk).
	Context string `yaml:"context"`
	UseCase string `yaml:"use_case"`
	Team    string `yaml:"team"`
	// Version overrides the default service.version (released image-tag intent, §5). Empty ⇒ serviceVersion default.
	Version string `yaml:"version"`
}

// OTelObs is the per-workload native-OTLP metrics switch (otel:). Metrics gates emission;
// Mode selects the resource-attribute shape: "naked" (SDK-default attrs, app→gateway direct)
// or "k8s_monitoring" (k8sattributes/resourcedetection-enriched, app→in-cluster Alloy→gateway).
type OTelObs struct {
	// Metrics enables native OTLP application-metrics emission (http.server.* via /v1/metrics);
	// false (default) ⇒ no OTLP-metrics emission.
	Metrics bool   `yaml:"metrics"`
	Mode    string `yaml:"mode"` // "naked" (default) | "k8s_monitoring"
}

const (
	otelModeNaked         = "naked"
	otelModeK8sMonitoring = "k8s_monitoring"

	// otelHTTPScopeName/Version is the instrumentation scope stamped on the OTLP metrics
	// (the real otelhttp instrumentation library → otel_scope_name/version at the gateway).
	otelHTTPScopeName    = "go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	otelHTTPScopeVersion = "0.58.0"
	otelSDKVersion       = "1.34.0"

	otelDurationMetricName  = "http.server.request.duration"
	otelActiveReqMetricName = "http.server.active_requests"
)

func (c *Config) otelMetricsEnabled() bool { return c.OTel != nil && c.OTel.Metrics }

func (c *Config) otelMode() string {
	if c.OTel == nil || c.OTel.Mode == "" {
		return otelModeNaked
	}
	return c.OTel.Mode
}

// BeylaObs is the per-workload Beyla observation switch (observability.beyla). It selects
// the deployment substrate (mode) and the per-service instrumentation context — which
// drives the per-signal emission matrix (beyla.Emission). Empty Features ⇒ the
// chart-default feature list for the mode (beyla.DefaultFeatures).
type BeylaObs struct {
	Mode     string   `yaml:"mode"`     // "kubernetes" | "standalone" (default kubernetes)
	Context  string   `yaml:"context"`  // "ebpf_only" | "coexist_sdk" (default ebpf_only)
	Features []string `yaml:"features"` // empty ⇒ beyla.DefaultFeatures(mode)
}

// beylaObserved reports whether the Beyla observation lane is active for this workload.
func (c *Config) beylaObserved() bool {
	return c.Observability != nil && c.Observability.Beyla != nil
}

// beylaMode returns the resolved Beyla deployment mode (default kubernetes).
func (c *Config) beylaMode() beyla.Mode {
	if !c.beylaObserved() || c.Observability.Beyla.Mode == "" {
		return beyla.ModeKubernetes
	}
	return beyla.Mode(c.Observability.Beyla.Mode)
}

// beylaContext returns the resolved Beyla instrumentation context (default ebpf_only).
func (c *Config) beylaContext() beyla.Context {
	if !c.beylaObserved() || c.Observability.Beyla.Context == "" {
		return beyla.ContextEBPFOnly
	}
	return beyla.Context(c.Observability.Beyla.Context)
}

// beylaFeatures returns the resolved Beyla feature list (empty ⇒ mode defaults).
func (c *Config) beylaFeatures() []string {
	if !c.beylaObserved() || len(c.Observability.Beyla.Features) == 0 {
		return beyla.DefaultFeatures(c.beylaMode())
	}
	return c.Observability.Beyla.Features
}

// normalizeBeyla validates + defaults the observability.beyla emission switch in place:
// when present, an empty Mode defaults to "kubernetes", an empty Context to "ebpf_only",
// and empty Features to beyla.DefaultFeatures(mode). Invalid mode/context or unknown
// feature names are rejected (the blueprint loader surfaces the error at load). A nil
// Observability/Beyla is a no-op (the workload keeps its existing tempo behavior).
func normalizeBeyla(cfg *Config) error {
	if cfg.Observability == nil || cfg.Observability.Beyla == nil {
		return nil
	}
	b := cfg.Observability.Beyla
	if b.Mode == "" {
		b.Mode = string(beyla.ModeKubernetes)
	}
	switch beyla.Mode(b.Mode) {
	case beyla.ModeKubernetes, beyla.ModeStandalone:
	default:
		return fmt.Errorf("web_service: observability.beyla.mode %q invalid (want kubernetes|standalone)", b.Mode)
	}
	if b.Context == "" {
		b.Context = string(beyla.ContextEBPFOnly)
	}
	switch beyla.Context(b.Context) {
	case beyla.ContextEBPFOnly, beyla.ContextCoexistSDK:
	default:
		return fmt.Errorf("web_service: observability.beyla.context %q invalid (want ebpf_only|coexist_sdk)", b.Context)
	}
	known := map[string]bool{
		beyla.FeatureApplication:  true,
		beyla.FeatureNetwork:      true,
		beyla.FeatureServiceGraph: true,
		beyla.FeatureAppSpan:      true,
		beyla.FeatureAppHost:      true,
		beyla.FeatureStats:        true,
	}
	for _, f := range b.Features {
		if !known[f] {
			return fmt.Errorf("web_service: observability.beyla.features contains unknown feature %q", f)
		}
	}
	if len(b.Features) == 0 {
		b.Features = beyla.DefaultFeatures(beyla.Mode(b.Mode))
	}
	return nil
}

// Traffic is the shape-driven request-rate envelope for the metric lane.
type Traffic struct {
	Shape      string  `yaml:"shape"`        // shape profile name (informational; engine carries the curve)
	OffPeakRPS float64 `yaml:"off_peak_rps"` // trough request rate (default 5)
	PeakRPS    float64 `yaml:"peak_rps"`     // plateau request rate (default 50)
}

// Endpoint is one route the workload serves.
type Endpoint struct {
	Route     string  `yaml:"route"`      // e.g. "GET /v1/items"
	ErrorRate float64 `yaml:"error_rate"` // [0,1] base error fraction
	P95ms     float64 `yaml:"p95_ms"`     // p95 latency target in ms (drives the lognormal draw)
}

// NewConfig returns a *Config with the documented defaults. The blueprint loader decodes
// the YAML node into this pointer; absent scalars keep these defaults (bool false is the
// exception — Tracing defaults true here and the loader must preserve an explicit false).
func NewConfig() any {
	return &Config{
		Tracing: true,
		Traffic: Traffic{OffPeakRPS: 5, PeakRPS: 50},
		Endpoints: []Endpoint{
			{Route: "GET /", ErrorRate: 0.01, P95ms: 120},
		},
	}
}

// Registration returns the core.WorkloadReg for the "web_service" kind. Call this from
// the composition root's catalog wiring file; no init() self-registration.
func Registration() core.WorkloadReg {
	return core.WorkloadReg{
		Kind:         kind,
		Doc:          "web_service — browser→backend→DB request-correlation workload (APM span-metrics, traces, app logs, optional Faro/RUM)",
		NewConfig:    NewConfig,
		Build:        build,
		FailureModes: FailureModes,
	}
}

// Workload is one web_service instance bound to a cluster.
type Workload struct {
	cfg Config
	b   core.Binding

	// resolved identity (frozen at Build from the binding).
	name      string
	namespace string
	cluster   string
	env       string
	weight    float64
	nonProd   bool
	context   string // §5 canon (optional)
	useCase   string // §5 canon (optional)
	team      string // §5 canon (optional)
	version   string // service.version (declared override or serviceVersion default)
	podName   string
	nodeName  string // k8s node hostname for pod 0 (empty when no cluster placement or no nodes)
	hostArch  string // host.arch (GOARCH form) of pod 0's node, from its instance type ("" when no placement)
	feoAppID  string // gf.feo11y.app.id — deterministic 8-hex from seed (signals/traces.md [slug: traces-resource-attrs])

	m             *minter
	st            *state.State
	stOTLP        *state.State
	otlpColdStart time.Time
}

// build constructs a Workload from the cfg pointer NewConfig returned and the resolved
// binding. Defensive: nil/empty endpoints fall back to the default route so the metric
// lane always has at least one series.
func build(cfgAny any, b core.Binding) (core.Workload, error) {
	cfg, _ := cfgAny.(*Config)
	if cfg == nil {
		cfg = NewConfig().(*Config)
	}
	if len(cfg.Endpoints) == 0 {
		cfg.Endpoints = []Endpoint{{Route: "GET /", ErrorRate: 0.01, P95ms: 120}}
	}
	if cfg.Traffic.PeakRPS <= 0 {
		cfg.Traffic.PeakRPS = 50
	}
	if cfg.Traffic.OffPeakRPS < 0 {
		cfg.Traffic.OffPeakRPS = 0
	}
	if err := normalizeBeyla(cfg); err != nil {
		return nil, err
	}
	if cfg.OTel != nil {
		if cfg.OTel.Mode == "" {
			cfg.OTel.Mode = otelModeNaked
		}
		switch cfg.OTel.Mode {
		case otelModeNaked, otelModeK8sMonitoring:
		default:
			return nil, fmt.Errorf("web_service: otel.mode %q invalid (want naked|k8s_monitoring)", cfg.OTel.Mode)
		}
	}

	w := &Workload{
		cfg:    *cfg,
		b:      b,
		name:   b.Name,
		st:     state.NewState(),
		stOTLP: state.NewState(),
	}
	w.resolveIdentity()
	w.context = cfg.Context
	w.useCase = cfg.UseCase
	w.team = cfg.Team
	w.version = serviceVersion
	if cfg.Version != "" {
		w.version = cfg.Version
	}
	w.m = newMinter(w.name, w.env, w.cluster, w.weight, w.nonProd, *cfg)
	w.m.calls = callSpecsFrom(b)
	return w, nil
}

// resolveIdentity pins the service/cluster/env/pod identity from the binding once.
// Absent dimensions are OMITTED downstream — never sentinelled (I13).
func (w *Workload) resolveIdentity() {
	if w.b.Env != nil {
		w.env = w.b.Env.Name
		w.weight = w.b.Env.Weight
		w.nonProd = w.b.Env.NonProd
	}
	if w.weight == 0 {
		w.weight = 1.0
	}
	// Default namespace = instance name; pod name = "{name}-0". Override from the cluster
	// placement (the k8s substrate's pod identity) when this workload is placed there, so
	// target_info's k8s_pod_name joins the kube_pod_info series (I12 / extract §2.5).
	w.namespace = w.name
	w.podName = w.name + "-0"
	// Faro/FEO app identity: deterministic 8-hex from the workload seed (signals/traces.md [slug: traces-resource-attrs]).
	w.feoAppID = fixture.HexID(w.b.Seed, 8, "feo", "app_id")
	if w.b.Cluster != nil {
		w.cluster = w.b.Cluster.Name
		if own := w.ownPlacement(); own != nil {
			if own.Namespace != "" {
				w.namespace = own.Namespace
			}
			if len(own.PodNames) > 0 {
				w.podName = own.PodNames[0]
			}
			// Resolve node hostname + host.arch from pod 0's node index (NodeIdx[0] → Cluster.Nodes[i]).
			// host.arch is the node instance type's GOARCH form (fixture.LookupInstanceSpec().KubeArch()),
			// matching the node's own kubernetes.io/arch label. Guard all derefs: absent NodeIdx or
			// out-of-bounds index leaves nodeName="" / hostArch="" (omitted / amd64-default, I13).
			if len(own.NodeIdx) > 0 && own.NodeIdx[0] < len(w.b.Cluster.Nodes) {
				n := w.b.Cluster.Nodes[own.NodeIdx[0]]
				w.nodeName = n.Hostname
				w.hostArch = fixture.LookupInstanceSpec(n.InstanceType).KubeArch()
			}
		}
	}
}

// ownPlacement finds this workload's placement entry in the cluster by name.
func (w *Workload) ownPlacement() *fixture.Workload {
	if w.b.Cluster == nil {
		return nil
	}
	for i := range w.b.Cluster.Workloads {
		if w.b.Cluster.Workloads[i].Name == w.name {
			return &w.b.Cluster.Workloads[i]
		}
	}
	return nil
}

// job is the Prometheus/Loki job label: "{namespace}/{service}".
func (w *Workload) job() string { return w.namespace + "/" + w.name }

// Kind implements core.Workload.
func (w *Workload) Kind() string { return kind }

// Name implements core.Workload.
func (w *Workload) Name() string { return w.b.Name }

// Signals declares the classes this instance emits. RUM is included only when the
// binding carries a Faro sink AND cfg.RUM is set. PyroscopeProfiles is included when
// pyroscope is enabled in sdk (self-push) mode — scraped mode is omitted because the
// runner must not wire a push sink when Alloy owns collection.
func (w *Workload) Signals() []core.SignalClass {
	sigs := []core.SignalClass{core.Metrics}
	if w.cfg.Tracing {
		sigs = append(sigs, core.Traces)
	}
	sigs = append(sigs, core.Logs)
	if w.rumEnabled() {
		sigs = append(sigs, core.RUM)
	}
	if w.cfg.Pyroscope != nil && w.cfg.Pyroscope.Enabled && w.cfg.Pyroscope.ModeOrDefault() != "scraped" {
		sigs = append(sigs, core.PyroscopeProfiles)
	}
	if w.cfg.otelMetricsEnabled() {
		sigs = append(sigs, core.OTLPMetrics)
	}
	return sigs
}

// rumEnabled reports whether the RUM lane is active.
func (w *Workload) rumEnabled() bool { return w.cfg.RUM && w.b.RUM != nil }

// Interval implements core.Workload (metric lane cadence).
func (w *Workload) Interval() time.Duration { return interval }

// Minter implements core.Workload.
func (w *Workload) Minter() ledger.Minter { return w.m }

// ProjectBatch is the trace/log/RUM lane: it is handed EXACTLY this instance's freshly
// minted batch and emits each signal class once. Span timing starts from
// r.RenderStart() (I11). Empty batch → no-op.
func (w *Workload) ProjectBatch(ctx context.Context, now time.Time, world *core.World, batch []*ledger.Request) error {
	if len(batch) == 0 {
		return nil
	}
	if w.cfg.Tracing && world.Traces != nil {
		// Beyla owns the trace in ebpf_only (boundary-only span tree, distro-marked). In
		// coexist_sdk the SDK owns the trace, which synthkit's existing trace lane already
		// models — so keep projectTraces there. Non-beyla services keep projectTraces too.
		if w.cfg.beylaObserved() && w.cfg.beylaContext() == beyla.ContextEBPFOnly {
			if err := w.projectBeylaBatch(ctx, world, batch); err != nil {
				return err
			}
		} else if err := w.projectTraces(ctx, world, batch); err != nil {
			return err
		}
	}
	if world.Logs != nil {
		if err := w.projectLogs(ctx, world, batch); err != nil {
			return err
		}
		if err := w.projectAILogs(ctx, world, batch); err != nil {
			return err
		}
	}
	if w.rumEnabled() {
		if err := w.projectRUM(ctx, batch); err != nil {
			return err
		}
	}
	return nil
}
