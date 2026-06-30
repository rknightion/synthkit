// SPDX-License-Identifier: AGPL-3.0-only

// Package core defines synthkit's three frozen seams — Construct, Workload, and the
// registry the Blueprint layer validates against — plus the World handed to every
// tick. See ARCHITECTURE.md for the full contract and the invariants the seams encode.
//
// Coupling rules (enforced by tests, not convention):
//   - A construct imports core, fixture, shape, state, and sink TYPES — never another
//     construct, never a workload, never the blueprint package.
//   - A construct contains ZERO blueprint-name references (de-Rochification grep test).
//   - Shared identity (EC2↔node, DB↔cloud) arrives via fixture.Set, resolved ONCE by
//     the composition root — constructs never negotiate identity with each other.
package core

import (
	"context"
	"time"

	"github.com/rknightion/synthkit/internal/failuremode"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/ledger"
	"github.com/rknightion/synthkit/internal/scale"
	"github.com/rknightion/synthkit/internal/shape"
	"github.com/rknightion/synthkit/internal/sigil"
	"github.com/rknightion/synthkit/internal/sink/faro"
	"github.com/rknightion/synthkit/internal/sink/loki"
	"github.com/rknightion/synthkit/internal/sink/otlp"
	"github.com/rknightion/synthkit/internal/sink/promrw"
	pyrosink "github.com/rknightion/synthkit/internal/sink/pyroscope"
)

// SignalClass is a class of telemetry a construct/workload can emit. The framework
// only asks an instance for the classes it declares.
type SignalClass uint8

const (
	Metrics SignalClass = iota
	Traces
	Logs
	RUM
	PyroscopeProfiles
	OTLPMetrics
	Sigil
)

func (c SignalClass) String() string {
	switch c {
	case Metrics:
		return "metrics"
	case Traces:
		return "traces"
	case Logs:
		return "logs"
	case RUM:
		return "rum"
	case PyroscopeProfiles:
		return "pyroscope_profiles"
	case OTLPMetrics:
		return "otlp_metrics"
	case Sigil:
		return "sigil"
	default:
		return "unknown"
	}
}

// Scope decides whether the sink stamps the blueprint selector label (ARCHITECTURE
// I17/I21). The decision is made ONCE per construct kind, at registration — never
// inside a render path.
type Scope uint8

const (
	// ScopeBlueprint: every series/stream/span is stamped with the blueprint selector
	// label by the scoped writer (clone-before-stamp).
	ScopeBlueprint Scope = iota
	// ScopeSubstrate: NO blueprint label, ever. The series separate by blueprint-declared
	// identity (cluster, account_id, instance) — required for vendor-app conformance
	// families whose queries aggregate across clusters (k8s-monitoring, dbo11y, CSP).
	ScopeSubstrate
)

// MetricWriter writes fully-labelled, FINAL-named Prometheus series. Implementations
// are pre-scoped at wiring time: a ScopeBlueprint instance receives a writer that
// stamps the selector label (cloning each series' labels first — Collect() output
// aliases live state); ScopeSubstrate writers never stamp.
type MetricWriter interface {
	Write(ctx context.Context, batch []promrw.Series) error
}

// LogWriter writes Loki streams (low-card stream labels; high-card keys in structured
// metadata — the sink asserts).
type LogWriter interface {
	Write(ctx context.Context, streams []loki.Stream) error
}

// TraceWriter writes OTLP trace resource blocks (traces ONLY; native OTLP metrics use OTLPMetricWriter).
type TraceWriter interface {
	Write(ctx context.Context, resources []otlp.Resource) error
}

// OTLPMetricWriter writes native OTLP metric resource blocks to the OTLP gateway (/v1/metrics).
// DISTINCT from MetricWriter (promrw, final pre-mangled Prometheus names): this lane emits OTLP
// semantic names and resource attributes and lets the gateway own normalization (target_info,
// promotion, unit/_total suffixes, otel_scope_*). Pre-scoped like the other writers: a
// ScopeBlueprint instance receives a writer that stamps the "blueprint" resource attribute.
type OTLPMetricWriter interface {
	Write(ctx context.Context, resources []otlp.MetricResource) error
}

// PyroscopeWriter writes Pyroscope profile series (one pprof Profile per series per push). Pre-scoped
// like the other writers: ScopeBlueprint stamps the selector label (clone-before-stamp); ScopeSubstrate
// never does.
type PyroscopeWriter interface {
	Write(ctx context.Context, series []pyrosink.Series) error
}

// SigilWriter ships native sigil AI-Observability ingest batches (generations/workflow-steps/
// scores) to the sigil HTTP ingest. UNLIKE the other writers it is NOT blueprint-scoped: sigil
// data is substrate-like (the ingest proto has no blueprint-label field, and real sigil data
// carries none) — disambiguation is by agent_name/service.name/conversation_id (review M3).
type SigilWriter interface {
	Write(ctx context.Context, batches []sigil.Export) error
}

// World is the per-blueprint execution context handed to every Tick/ProjectBatch.
// Writers are nil for signal classes the instance did not declare — the runner only
// wires what Signals() promises. Ledger is non-nil for workloads only; constructs
// never read it (and never mint IDs — ARCHITECTURE I9).
type World struct {
	Shape     *shape.Engine
	Metrics   MetricWriter
	Logs      LogWriter
	Traces    TraceWriter
	Pyroscope PyroscopeWriter // nil unless PyroscopeProfiles declared
	// OTLPMetrics is the native-OTLP metrics lane (nil unless the instance declared OTLPMetrics).
	// Opt-in alternative to Metrics (promrw) for emitters modelling OTLP-SDK apps pushed via Alloy.
	OTLPMetrics OTLPMetricWriter
	Ledger      *ledger.Ledger
	// Scaling answers live replica/read-replica counts (control plane). Nil for instances that
	// declared no scalable dimension; readers fall back to their blueprint-declared default.
	Scaling *scale.Source
	// EmitSpanMetrics gates synthkit's OWN backend spanmetrics + service-graph emission.
	// false ⇒ defer to Grafana Cloud metrics-generator / beyla (which also emit the exemplars).
	EmitSpanMetrics bool
	// Sigil is the native sigil AI-Observability ingest lane (nil unless the instance declared Sigil).
	Sigil SigilWriter
}

// Construct is an infra/topology module instance: render the config + fixtures it was
// built with, know nothing about blueprints or other constructs.
type Construct interface {
	// Kind returns the registry key (e.g. "coredns", "rds"). Stable, snake_case.
	Kind() string
	// Signals declares which classes this instance emits.
	Signals() []SignalClass
	// Interval is the tick cadence. Metric lanes are ≥60s (the DPM floor, I10);
	// the runner clamps lower values up and logs.
	Interval() time.Duration
	// Tick renders one batch into w. Counters/histograms accumulate in the instance's
	// own state.State across ticks (push running totals, never deltas — I3).
	Tick(ctx context.Context, now time.Time, w *World) error
}

// Workload is a traffic/application module instance on the workload axis. It owns
// request projection: metric lanes Tick on Interval() and read
// w.Ledger.ActiveFor(name, …); trace/log/RUM lanes are handed the exact minted batch
// via ProjectBatch and emit ONCE by construction (two-cadence model, I10).
type Workload interface {
	// Kind returns the registry key (e.g. "web_service").
	Kind() string
	// Name returns the instance name from the blueprint (e.g. "acme-api").
	Name() string
	Signals() []SignalClass
	Interval() time.Duration
	// Minter contributes this instance's request volume to the blueprint's ledger.
	Minter() ledger.Minter
	// Tick is the metric lane (RED/span-metrics/service-graph from Active()).
	Tick(ctx context.Context, now time.Time, w *World) error
	// ProjectBatch is the trace/log/RUM lane: called by the master clock with exactly
	// the requests this instance's Minter just minted. Span timing starts from
	// r.RenderStart().
	ProjectBatch(ctx context.Context, now time.Time, w *World, batch []*ledger.Request) error
}

// RUMSink is the Faro beacon surface, passed to workloads that declare RUM via their
// Binding (it is not a World writer: exactly one workload kind uses it, and its
// credentials are optional — absent creds disable RUM at load with a warning).
type RUMSink interface {
	Write(ctx context.Context, payloads []faro.Payload) error
}

// Binding is a workload instance's resolved wiring: its identity, where it runs, and
// what it calls.
type Binding struct {
	Name     string // instance name from the blueprint ("acme-api")
	Seed     string
	Replicas int
	Env      *fixture.Env
	Cluster  *fixture.Cluster
	Calls    []fixture.CallTarget
	// Databases are the resolved RDS/Postgres fixtures declared in this workload's env. A workload
	// uses them to resolve a db-leaf node to its env's RDS instance (server.address / db.namespace
	// on the db-client span) WITHOUT importing the blueprint or minting identity — the resolver
	// owns the per-env db identity; the workload only reads it. Empty when the env declares none.
	Databases []*fixture.DB
	RUM       RUMSink // nil unless declared AND credentialed
}

// Group classifies HOW a construct is declared in a blueprint (orthogonal to Scope,
// which is about label-stamping). Topology kinds (resolver-emitted) and cluster add-ons
// leave it empty — they are declared implicitly via `cloud`/`cluster`/`databases`/… or a
// cluster's `addons:`. The two TOP-LEVEL sections carry an explicit group so the loader
// can reject a mis-bucketed declaration (a Grafana Cloud feature under `integrations:`, or
// an external source under `features:`).
type Group string

const (
	// GroupFeature: a Grafana Cloud product you enable in your stack (declared under
	// `features:` — e.g. synthetic_monitoring, fleet_management).
	GroupFeature Group = "feature"
	// GroupIntegration: an external system Grafana Cloud ingests/observes (declared under
	// `integrations:` — e.g. cloudflare, csp_azure, csp_gcp).
	GroupIntegration Group = "integration"
)

// ConstructReg registers one construct kind. Build receives the kind's own config
// struct (the same pointer type NewConfig returns, decoded from blueprint YAML) and
// the resolved fixtures.
type ConstructReg struct {
	Kind      string
	Doc       string // one line, used in validation errors and generated docs
	Scope     Scope
	Group     Group // declaration section (feature|integration); empty for topology/add-on kinds
	NewConfig func() any
	Build     func(cfg any, fx *fixture.Set) (Construct, error)
	// FailureModes declares the failure modes this construct kind responds to (each tagged with
	// its scope axis). The blueprint resolver validates scenario/incident references against the
	// union of these; the construct implements each via shape.Eval(now, Mode.Name, scope). nil =
	// responds to no modes.
	FailureModes []failuremode.Mode
}

// WorkloadReg registers one workload kind.
type WorkloadReg struct {
	Kind      string
	Doc       string
	NewConfig func() any
	Build     func(cfg any, b Binding) (Workload, error)
	// FailureModes — see ConstructReg.FailureModes (workload axis).
	FailureModes []failuremode.Mode
	// Scope decides blueprint-label stamping for this workload's lanes (default ScopeBlueprint).
	// ScopeSubstrate ⇒ no blueprint selector label (e.g. the ai_agent/sigil workload, whose
	// real-world data carries none and whose ingest proto has no field for it — disambiguation
	// is by service.name/agent_name/conversation_id, like substrate constructs).
	Scope Scope
}
