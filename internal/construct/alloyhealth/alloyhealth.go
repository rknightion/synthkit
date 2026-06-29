// SPDX-License-Identifier: AGPL-3.0-only

// Package alloyhealth implements the "alloy_health" construct.
//
// Kind:     "alloy_health"
// Scope:    ScopeSubstrate  — no blueprint label ever (I21/§5)
// Signals:  [Metrics]
// Interval: 60s
// Config:   empty struct (all wiring comes from fx.Cluster)
//
// This is a cluster ADDON — it must be wired via the blueprint addons list on the
// cluster that has k8s_monitoring.alloy=true. The construct receives the cluster's
// identity via fx.Cluster.
//
// Emits the Alloy pipeline meta-health metric set:
//   - otelcol_receiver_{accepted,refused}_spans_total  (Counter, per receiver × pod)
//   - otelcol_exporter_{sent,send_failed}_spans_total  (Counter, per exporter × pod)
//   - otelcol_exporter_queue_size                      (Gauge, per exporter × pod)
//   - otelcol_processor_batch_batch_send_size_count    (Counter, per pod)
//   - otelcol_processor_batch_metadata_cardinality     (Gauge, per pod)
//   - up{pipeline}                                     (Gauge 0/1, per pipeline × pod)
//   - alloy_build_info                                 (Gauge = 1, version from cluster)
//
// PLUS the content sentinel (I23):
//   - synthkit_content_dropped_total  (Counter, state.Add, grows every tick)
//   - synthkit_content_leak_test      (Gauge, ALWAYS exactly 0.0)
//
// Version sourcing: fx.Cluster.K8sMonitoring.AlloyVersion when non-empty (already
// "v"-prefixed by the fixture resolver — I22); else "v1.16.3".
//
// Reference: signals/fm.md [slug: fm-alloy-health]
package alloyhealth

import (
	"context"
	"fmt"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/sink/promrw"
	"github.com/rknightion/synthkit/internal/state"
)

// Kind is the registry key for this construct.
const Kind = "alloy_health"

// defaultAlloyVersion is used when fx.Cluster.K8sMonitoring.AlloyVersion is empty.
// Must be "v"-prefixed (I22 / T10: k8s-monitoring filters alloy_build_info{version=~"v.+"}).
const defaultAlloyVersion = "v1.16.3"

// Topology constants — two HA pod replicas, namespace "infra".
const (
	alloyJob       = "integrations/alloy"
	alloyNamespace = "infra"
	alloyPodA      = "10.1.4.10:12345"
	alloyPodB      = "10.1.4.11:12345"
)

// alloyPods is the ordered pair of HA pod instance strings.
var alloyPods = []string{alloyPodA, alloyPodB}

// alloyPipelines are the generic Alloy pipeline names used for per-pipeline up gauges
// and content-sentinel series. These are technology-neutral pipeline identifiers —
// AI/LLM-specific pipeline names are out of scope for synthkit v1 (CLAUDE.md).
var alloyPipelines = []string{
	"traces",
	"metrics",
	"logs",
	"spans",
	"events",
}

// otelReceivers are the otelcol receiver component names.
var otelReceivers = []string{"otlp", "prometheus"}

// otelExporters are the otelcol exporter component names.
var otelExporters = []string{"otlphttp", "prometheusremotewrite"}

// Config is the construct config struct (empty — all wiring comes from fx.Cluster).
type Config struct{}

// NewConfig returns an empty *Config for the YAML decoder.
func NewConfig() any { return &Config{} }

// Construct is the alloy_health instance for one cluster.
type Construct struct {
	cluster      string // "cluster" / "k8s_cluster_name" label value
	alloyVersion string // "v"-prefixed Alloy version from fixture or default
	st           *state.State
}

// Build validates fx.Cluster (required) and returns a ready Construct.
func Build(cfg any, fx *fixture.Set) (core.Construct, error) {
	if _, ok := cfg.(*Config); !ok {
		return nil, fmt.Errorf("alloyhealth: Build called with %T, want *Config", cfg)
	}
	if fx == nil || fx.Cluster == nil {
		return nil, fmt.Errorf("alloyhealth: fixture.Cluster is required (nil)")
	}

	ver := fx.Cluster.K8sMonitoring.AlloyVersion
	if ver == "" {
		ver = defaultAlloyVersion
	}

	return &Construct{
		cluster:      fx.Cluster.Name,
		alloyVersion: ver,
		st:           state.NewState(),
	}, nil
}

func (c *Construct) Kind() string                { return Kind }
func (c *Construct) Signals() []core.SignalClass { return []core.SignalClass{core.Metrics} }
func (c *Construct) Interval() time.Duration     { return 60 * time.Second }

// Tick renders one 60s Alloy meta-health snapshot.
func (c *Construct) Tick(ctx context.Context, now time.Time, w *core.World) error {
	batch := c.build(now, w.Shape.BusinessFactor(now))
	return w.Metrics.Write(ctx, batch)
}

// build accumulates one tick into c.st and returns the collected series. Separated from
// Tick so tests can assert the wire contract without a live sink.
//
// factor is the diurnal × weekly shape multiplier (BusinessFactor — substrate metric,
// no per-env weight).
func (c *Construct) build(now time.Time, factor float64) []promrw.Series {
	cluster := c.cluster

	// ── alloyBaseLabels returns the Alloy self-metric base labels for a given pod. ──
	baseFor := func(pod string) map[string]string {
		return map[string]string{
			"cluster":          cluster,
			"k8s_cluster_name": cluster,
			"namespace":        alloyNamespace,
			"job":              alloyJob,
			"instance":         pod,
		}
	}

	// ── 1. alloy_build_info ──────────────────────────────────────────────────────
	// Gauge = 1; carries extra version/revision/goversion labels. Emitted per pod.
	for _, pod := range alloyPods {
		infoLabels := baseFor(pod)
		infoLabels["version"] = c.alloyVersion
		infoLabels["revision"] = "1e2007e"
		infoLabels["goversion"] = "go1.26.3"
		c.st.Set("alloy_build_info", infoLabels, 1)
	}

	// Base span throughput this tick — scaled by diurnal factor.
	baseSpans := factor * 1500.0
	if baseSpans < 1 {
		baseSpans = 1
	}

	// ── 2. Per-pipeline up gauge ─────────────────────────────────────────────────
	for _, pod := range alloyPods {
		base := baseFor(pod)
		for _, pipeline := range alloyPipelines {
			upLabels := copyLabels(base)
			upLabels["pipeline"] = pipeline
			c.st.Set("up", upLabels, 1.0)
		}
	}

	// ── 3. otelcol_receiver_* (per receiver × pod) ──────────────────────────────
	for _, pod := range alloyPods {
		base := baseFor(pod)
		podSpans := baseSpans / 2.0 // HA split — half load per pod
		if podSpans < 1 {
			podSpans = 1
		}
		for _, recv := range otelReceivers {
			recvSpans := podSpans
			if recv == "prometheus" {
				recvSpans = 0 // prometheus receiver = metrics-only, no spans
			}
			recvLabels := copyLabels(base)
			recvLabels["receiver"] = recv
			accepted := recvSpans * 0.9995
			refused := recvSpans * 0.0005
			c.st.Add("otelcol_receiver_accepted_spans_total", recvLabels, accepted)
			// refused counter always emitted (even at ~0) so the series exists for alerting.
			c.st.Add("otelcol_receiver_refused_spans_total", recvLabels, refused)
		}
	}

	// ── 4. otelcol_exporter_* (per exporter × pod) ──────────────────────────────
	for _, pod := range alloyPods {
		base := baseFor(pod)
		podSpans := baseSpans / 2.0
		if podSpans < 1 {
			podSpans = 1
		}
		for _, exp := range otelExporters {
			expSpans := podSpans
			if exp == "prometheusremotewrite" {
				expSpans = 0 // metrics-only exporter: no spans
			}
			expLabels := copyLabels(base)
			expLabels["exporter"] = exp
			sent := expSpans * 0.9997
			failed := expSpans * 0.0003
			c.st.Add("otelcol_exporter_sent_spans_total", expLabels, sent)
			c.st.Add("otelcol_exporter_send_failed_spans_total", expLabels, failed)
			// queue_size: instantaneous gauge, small in healthy state.
			c.st.Set("otelcol_exporter_queue_size", expLabels, 3.0)
		}
	}

	// ── 5. otelcol_processor_batch_* (per pod) ──────────────────────────────────
	batchesPerTick := baseSpans / 200.0 // ~200 spans per batch
	if batchesPerTick < 1 {
		batchesPerTick = 1
	}
	for _, pod := range alloyPods {
		base := baseFor(pod)
		procLabels := copyLabels(base)
		procLabels["processor"] = "batch"
		c.st.Add("otelcol_processor_batch_batch_send_size_count", procLabels, batchesPerTick/2.0)
		c.st.Set("otelcol_processor_batch_metadata_cardinality", procLabels, 20.0)
	}

	// ── 6. synthkit_content_dropped_total — content sentinel counter (I23) ───────
	// Proves the content strip is active. Grows every tick (state.Add).
	// Labels: cluster, k8s_cluster_name, namespace, job, pipeline.
	// Each pipeline emits a fixed dropsPerTick (always ≥ 0.5 so the series is never
	// stale — "always a trickle"). No AI/LLM field_class in v1: use generic categories.
	type dropEntry struct {
		pipeline     string
		dropsPerTick float64
	}
	dropSeries := []dropEntry{
		{"traces", 8.0},
		{"metrics", 3.0},
		{"logs", 5.0},
		{"spans", 4.0},
		{"events", 2.0},
	}
	for _, d := range dropSeries {
		drops := d.dropsPerTick * factor
		if drops < 0.5 {
			drops = 0.5 // always a trickle — proves the processor is running
		}
		dropLabels := map[string]string{
			"cluster":          cluster,
			"k8s_cluster_name": cluster,
			"namespace":        alloyNamespace,
			"job":              alloyJob,
			"pipeline":         d.pipeline,
		}
		c.st.Add("synthkit_content_dropped_total", dropLabels, drops)
	}

	// ── 7. synthkit_content_leak_test — leak sentinel gauge (I23) ───────────────
	// ALWAYS exactly 0.0 — proves the content strip is working.
	// A non-zero value would indicate a PII/content leak (T11 in the extract).
	// Labels: cluster, k8s_cluster_name, namespace, job, pipeline.
	for _, pipeline := range alloyPipelines {
		leakLabels := map[string]string{
			"cluster":          cluster,
			"k8s_cluster_name": cluster,
			"namespace":        alloyNamespace,
			"job":              alloyJob,
			"pipeline":         pipeline,
		}
		c.st.Set("synthkit_content_leak_test", leakLabels, 0.0)
	}

	return c.st.Collect(now)
}

// copyLabels returns a shallow copy of a label map with extra capacity for extension.
func copyLabels(m map[string]string) map[string]string {
	out := make(map[string]string, len(m)+2)
	for k, v := range m {
		out[k] = v
	}
	return out
}
