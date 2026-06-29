// SPDX-License-Identifier: AGPL-3.0-only

// Package dashboard is the synthkit dashboard builder library: the Manifest contract
// (what a blueprint's emitted signal surface looks like) plus estate-aware, query-correct
// helpers over the Grafana Foundation SDK GA v2 schema. It ships ZERO finished dashboards —
// per-blueprint templates compose these helpers. The synthetic-emit hot path never imports
// this package (archtest-guarded); only cmd/synthkit-dash + templates do.
package dashboard

// InstrumentKind classifies a metric's PromQL query form. Derived from the emitted
// name-set by internal/dashgen (never hand-declared).
type InstrumentKind uint8

const (
	Gauge InstrumentKind = iota
	Counter
	HistogramClassic // hand-mangled _bucket/_sum/_count (synthkit's promrw histograms)
	HistogramNative  // stack-generated trace-derived (spanmetrics, service-graph)
)

func (k InstrumentKind) String() string {
	switch k {
	case Counter:
		return "counter"
	case HistogramClassic:
		return "histogram_classic"
	case HistogramNative:
		return "histogram_native"
	default:
		return "gauge"
	}
}

// MetricScope mirrors core.Scope but is duplicated here so the builder library stays
// importable by templates without pulling internal/core into their dependency set.
type MetricScope uint8

const (
	ScopeBlueprint MetricScope = iota // carries the "blueprint" selector label
	ScopeSubstrate                    // no blueprint label; disambiguated by identity
)

// Manifest is the per-blueprint signal surface + resolved topology a template consumes.
// Built by internal/dashgen from blueprint.Resolved (topology) and the dry-run sink
// inventory (signals). It cannot drift from emission: signals come from the emit path.
type Manifest struct {
	Blueprint string // blueprint name
	Label     string // selector label value (== Blueprint unless overridden)

	Environments []EnvRef
	Clusters     []ClusterRef
	Accounts     []AccountRef
	Databases    []DBRef
	Caches       []CacheRef
	Workloads    []WorkloadRef
	Integrations []IntegrationRef

	Metrics    []MetricSignal // every emitted prom series base, classified
	LogSources []LogSource    // loki stream sources
	Spans      []SpanSource   // otlp services + span names
}

type EnvRef struct {
	Name     string
	Provider string // "aws" | "gcp" | "azure" | ""
	Account  string // cloud account_id
	Region   string
	VpcID    string
}

type ClusterRef struct {
	Name       string
	Type       string // "eks"
	Env        string
	Account    string
	K8sMonitor bool // k8s_monitoring.enabled
	OpenCost   bool
	Kepler     bool
}

type AccountRef struct {
	Provider string
	ID       string
	Region   string
}

type DBRef struct {
	Engine  string // "postgres" | "mysql"
	Version string
	Name    string // RDS DBInstanceIdentifier AND dbo11y identifier
	Env     string
	Account string
}

type CacheRef struct {
	Engine  string
	Version string
	Name    string
	Env     string
}

type WorkloadRef struct {
	Name    string
	Kind    string // "web_service"
	Cluster string // runs_on
	Env     string
	Calls   []CallRef // downstream deps (correlation wiring)
}

type CallRef struct {
	Kind string // "db" | "cache"
	Name string // target DB/cache name
}

// IntegrationRef is an off-the-shelf vendor integration this estate lights up. The
// deep-link target (vendor dashboard uid/slug) is per-stack and arrives via config,
// not from the BoM — left empty here and filled by the index builder from its config.
type IntegrationRef struct {
	Kind string // "k8s" | "aws" | "gcp" | "azure" | "cloudflare"
}

type MetricSignal struct {
	Name       string // FINAL series name (the _bucket/_sum/etc. family base is grouped: see internal/dashgen)
	Instrument InstrumentKind
	LabelKeys  []string
	Scope      MetricScope
	Dual       bool // family emits BOTH native and classic forms on the wire (native + _bucket)
}

type LogSource struct {
	Source       string   // the `source` stream label value
	StreamKeys   []string // stream label keys
	MetadataKeys []string // structured-metadata keys
}

type SpanSource struct {
	Service      string
	SpanNames    []string
	AttrKeys     []string // span attribute keys
	ResourceKeys []string // resource attribute keys (service.namespace, deployment.environment, …)
}

// Metric returns the MetricSignal with the given name.
func (m *Manifest) Metric(name string) (MetricSignal, bool) {
	for _, s := range m.Metrics {
		if s.Name == name {
			return s, true
		}
	}
	return MetricSignal{}, false
}
