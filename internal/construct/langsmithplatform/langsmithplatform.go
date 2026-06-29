// SPDX-License-Identifier: AGPL-3.0-only

// Package langsmithplatform implements the "langsmith_platform" construct.
//
// Kind:     "langsmith_platform"
// Scope:    core.ScopeSubstrate — substrate identity (job=langsmith-<svc>); NO blueprint label.
// Group:    core.GroupIntegration
// Signals:  []core.SignalClass{core.Metrics}
// Interval: 60s
//
// Emits the standard exporter families for each configured LangSmith service via
// a /metrics scrape. LangSmith documents ZERO custom app-metric names — the services
// are Python, so the accurate representation is the standard exporter families with
// job=langsmith-<service>. Do NOT invent langsmith_* app series.
//
// Signal contract: signals/langsmith.md [slug: langsmith-platform]
//
// Services and their scrape ports (from signals/langsmith.md):
//
//	backend:1984, host-backend:1985, platform-backend:1986, playground:1988
//	clickhouse:9363, redis:9121, postgres:9187, nginx:9113
//
// Emission by service:
//
//	Python services (backend/host-backend/platform-backend/playground):
//	  process_* + python_* + http_* families
//	clickhouse: ClickHouse* family
//	redis:      redis_* family
//	postgres:   pg_* family
//	nginx:      nginx_* family
package langsmithplatform

import (
	"context"
	"fmt"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/sink/promrw"
	"github.com/rknightion/synthkit/internal/state"
)

// Kind is the registry key.
const Kind = "langsmith_platform"

// defaultServices is the full default service list.
var defaultServices = []string{
	"backend",
	"host-backend",
	"platform-backend",
	"playground",
	"clickhouse",
	"redis",
	"postgres",
	"nginx",
}

// servicePorts maps each service name to its scrape port.
var servicePorts = map[string]int{
	"backend":          1984,
	"host-backend":     1985,
	"platform-backend": 1986,
	"playground":       1988,
	"clickhouse":       9363,
	"redis":            9121,
	"postgres":         9187,
	"nginx":            9113,
}

// pythonServices is the set of Python-based services (process+python+http families).
var pythonServices = map[string]bool{
	"backend":          true,
	"host-backend":     true,
	"platform-backend": true,
	"playground":       true,
}

// httpBuckets is a plausible seconds-valued bucket set for HTTP request duration histograms
// (Prometheus http_request_duration_seconds histogram — standard middleware).
// Bounds: 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10 (standard prom-client defaults).
var httpBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

// volAmp / rateAmp set per-series Spread+Wander magnitudes for Python-service metrics.
// Volume/memory/request metrics use volAmp; rate-like counters (gc, cpu) use rateAmp.
const (
	volAmp  = 0.18 // ±~18% baseline spread for volume/memory/latency
	rateAmp = 0.30 // ±~30% spread for rate/count-like counters
)

// seriesVar returns a stable-but-living per-series multiplier ≈ 1: a deterministic baseline
// OFFSET (Spread) times a slow per-series DRIFT (Wander). amp sets the magnitude.
// Returns 1.0 when no shape engine is wired.
func (c *Construct) seriesVar(w *core.World, now time.Time, key string, amp float64) float64 {
	if w == nil || w.Shape == nil {
		return 1.0
	}
	return w.Shape.Spread(key, amp) * w.Shape.Wander(key, now, amp*0.4)
}

// Config is the construct's YAML config struct.
type Config struct {
	// Services is the list of LangSmith service names to emit metrics for.
	// When empty, all eight default services are emitted.
	// Valid values: backend, host-backend, platform-backend, playground,
	//               clickhouse, redis, postgres, nginx.
	Services []string `yaml:"services"`
}

// NewConfig returns a pointer to a zero Config for the YAML decoder.
func NewConfig() any { return &Config{} }

// Construct is the per-instance langsmith_platform renderer.
type Construct struct {
	services []string
	st       *state.State
	// Env-scoping (Spec 3): when the fixture carries an Env, the construct is fanned per-env —
	// envName is stamped as the `env` label and magnitudes scale by Shape.Factor(now, weight, nonProd).
	// Aggregate (nil Env) omits the env label entirely (I13) and uses Shape.BusinessFactor (n-1).
	envScoped bool
	envName   string
	weight    float64
	nonProd   bool
}

// Compile-time interface check.
var _ core.Construct = (*Construct)(nil)

// Build validates cfg and fx and returns a ready core.Construct instance.
func Build(cfg any, fx *fixture.Set) (core.Construct, error) {
	c, ok := cfg.(*Config)
	if !ok || c == nil {
		return nil, fmt.Errorf("langsmithplatform: Build called with %T, want *Config", cfg)
	}

	services := c.Services
	if len(services) == 0 {
		services = defaultServices
	}

	// Env-scoped fan-out (Spec 3): the fixture's Env drives per-env weight scaling and stamps
	// the env label. Aggregate (nil Env) omits the env label (I13) and uses weight 1.0.
	weight, nonProd, envScoped := 1.0, false, false
	var envName string
	if fx != nil && fx.Env != nil {
		envName = fx.Env.Name
		weight = fx.Env.Weight
		nonProd = fx.Env.NonProd
		envScoped = true
	}

	return &Construct{
		services:  services,
		st:        state.NewState(),
		envScoped: envScoped,
		envName:   envName,
		weight:    weight,
		nonProd:   nonProd,
	}, nil
}

func (c *Construct) Kind() string                { return Kind }
func (c *Construct) Signals() []core.SignalClass { return []core.SignalClass{core.Metrics} }
func (c *Construct) Interval() time.Duration     { return 60 * time.Second }

// Tick renders one 60-second observation window into w.Metrics.
func (c *Construct) Tick(ctx context.Context, now time.Time, w *core.World) error {
	batch := c.renderMetrics(now, w)
	if w.Metrics != nil {
		if err := w.Metrics.Write(ctx, batch); err != nil {
			return err
		}
	}
	return nil
}

// renderMetrics builds the full per-tick batch. Separated so tests can call without a full World.
func (c *Construct) renderMetrics(now time.Time, w *core.World) []promrw.Series {
	// Magnitude factor (B1): env-scoped uses the env-weighted Factor (per-env weekend collapse);
	// aggregate keeps BusinessFactor byte-for-byte (Factor(now,1,false) ≠ BusinessFactor on
	// weekends — 0.2 vs 0.3 — so a blanket swap would regress committed blueprints).
	var bf float64
	if c.envScoped {
		bf = w.Shape.Factor(now, c.weight, c.nonProd)
	} else {
		bf = w.Shape.BusinessFactor(now)
	}

	for _, svc := range c.services {
		c.emitService(now, svc, bf, w)
	}
	return c.st.Collect(now)
}

// baseLabels returns the required labels for every series: job, instance, and (when
// env-scoped) env. The env label is OMITTED when aggregate (I13 — absent dimension omitted).
func (c *Construct) baseLabels(svc string) map[string]string {
	port, ok := servicePorts[svc]
	if !ok {
		port = 0
	}
	instance := fmt.Sprintf("%s:%d", svc, port)
	m := map[string]string{
		"job":      "langsmith-" + svc,
		"instance": instance,
	}
	if c.envScoped {
		m["env"] = c.envName
	}
	return m
}

// emitService dispatches to the correct exporter family for the given service.
func (c *Construct) emitService(now time.Time, svc string, bf float64, w *core.World) {
	switch {
	case pythonServices[svc]:
		c.emitPython(now, svc, bf, w)
	case svc == "clickhouse":
		c.emitClickHouse(svc, bf)
	case svc == "redis":
		c.emitRedis(svc, bf)
	case svc == "postgres":
		c.emitPostgres(svc, bf)
	case svc == "nginx":
		c.emitNginx(svc, bf)
	}
}

// ── Python services: process_* + python_* + http_* ───────────────────────────

func (c *Construct) emitPython(now time.Time, svc string, bf float64, w *core.World) {
	lbls := c.baseLabels(svc)

	// vf is a per-series symmetric multiplier keyed on metric name + service name.
	// Each of the four Python services (backend/host-backend/platform-backend/playground)
	// gets a distinct stable-but-drifting baseline so peers don't emit byte-identical values.
	vf := func(metric string, amp float64) float64 {
		return c.seriesVar(w, now, metric+"|"+svc, amp)
	}

	// process_* family — gauges
	// process_max_fds is a system-limit constant (1024 is the OS fd limit — intentionally uniform).
	// process_start_time_seconds is a wall-clock stamp, not a magnitude — per-service offset
	// spreads services slightly (each process started at a different time in practice).
	c.st.Set("process_resident_memory_bytes", lbls, 150_000_000*bf*vf("proc_rss", volAmp))
	c.st.Set("process_virtual_memory_bytes", lbls, 800_000_000*bf*vf("proc_vss", volAmp))
	c.st.Set("process_open_fds", lbls, 30*bf*vf("proc_fds", volAmp))
	c.st.Set("process_max_fds", lbls, 1024)
	c.st.Set("process_start_time_seconds", lbls, float64(now.Unix()-3600)) // started ~1h ago; wall-clock stamp, not a magnitude

	// process_cpu_seconds_total — counter
	c.st.Add("process_cpu_seconds_total", lbls, 1.5*bf*vf("proc_cpu", rateAmp))

	// python_* family — counters + gauge
	// python_info is an info metric (value always 1 — intentionally constant).
	c.st.Add("python_gc_objects_collected_total", lbls, 200*bf*vf("py_gc_obj", rateAmp))
	c.st.Add("python_gc_collections_total", lbls, 5*bf*vf("py_gc_col", rateAmp))
	c.st.Set("python_info", lbls, 1)

	// http_* family — counter + histogram
	c.st.Add("http_requests_total", lbls, 50*bf*vf("http_req", volAmp))
	// http_request_duration_seconds — histogram (LEBare, seconds buckets)
	// Noise() is IID per-tick texture on top of the per-series spread.
	noiseF := 1.0
	if w.Shape != nil {
		noiseF = w.Shape.Noise(0.3)
	}
	c.st.Observe("http_request_duration_seconds", lbls, httpBuckets, state.LEBare, 0.12*bf*vf("http_dur", volAmp)*noiseF)
}

// ── ClickHouse :9363 ─────────────────────────────────────────────────────────

func (c *Construct) emitClickHouse(svc string, bf float64) {
	lbls := c.baseLabels(svc)

	// ClickHouseMetrics_* — gauges
	c.st.Set("ClickHouseMetrics_Query", lbls, 3*bf)
	c.st.Set("ClickHouseMetrics_TCPConnection", lbls, 8*bf)

	// ClickHouseProfileEvents_* — counters
	c.st.Add("ClickHouseProfileEvents_Query", lbls, 40*bf)
	c.st.Add("ClickHouseProfileEvents_SelectQuery", lbls, 35*bf)
	// SelectedRows/InsertedRows: rows processed by SELECT and INSERT statements — monotonic counters
	// scaled by diurnal factor like siblings (signals/langsmith.md [slug: langsmith-platform]).
	c.st.Add("ClickHouseProfileEvents_SelectedRows", lbls, 500_000*bf)
	c.st.Add("ClickHouseProfileEvents_InsertedRows", lbls, 50_000*bf)

	// ClickHouseAsyncMetrics_* — gauge
	c.st.Set("ClickHouseAsyncMetrics_Uptime", lbls, 86400*bf)
}

// ── redis_exporter :9121 ─────────────────────────────────────────────────────

func (c *Construct) emitRedis(svc string, bf float64) {
	lbls := c.baseLabels(svc)

	// gauges
	c.st.Set("redis_up", lbls, 1)
	c.st.Set("redis_connected_clients", lbls, 12*bf)
	c.st.Set("redis_memory_used_bytes", lbls, 50_000_000*bf)

	// counters
	c.st.Add("redis_commands_processed_total", lbls, 500*bf)
	c.st.Add("redis_keyspace_hits_total", lbls, 450*bf)
	c.st.Add("redis_keyspace_misses_total", lbls, 50*bf)
}

// ── postgres_exporter :9187 ──────────────────────────────────────────────────

// pgActivityStates are the connection-state values emitted by pg_stat_activity_count.
// Counts must sum near pg_stat_database_numbackends (≈8 at bf=1).
var pgActivityStates = []struct {
	state string
	count float64
}{
	{"active", 3},
	{"idle", 4},
	{"idle in transaction", 1},
}

func (c *Construct) emitPostgres(svc string, bf float64) {
	lbls := c.baseLabels(svc)

	// gauges
	c.st.Set("pg_up", lbls, 1)
	c.st.Set("pg_stat_database_numbackends", lbls, 8*bf)

	// pg_stat_activity_count — gauge per connection state (low-card label: state).
	// Distinct from pg_stat_database_numbackends (which is a total-backends count per datname).
	// State values mirror real postgres_exporter output (signals/langsmith.md [slug: langsmith-platform]).
	for _, s := range pgActivityStates {
		stateLbls := make(map[string]string, len(lbls)+1)
		for k, v := range lbls {
			stateLbls[k] = v
		}
		stateLbls["state"] = s.state
		c.st.Set("pg_stat_activity_count", stateLbls, s.count*bf)
	}

	// pg_replication_lag — gauge (seconds); near-zero under normal operation.
	// Shape: small positive value rarely reaching 2s (mirrors dbo11y.md [slug: dbo11ypg]).
	c.st.Set("pg_replication_lag", lbls, 0.05*bf)

	// counters
	c.st.Add("pg_stat_database_xact_commit", lbls, 300*bf)
	c.st.Add("pg_stat_database_xact_rollback", lbls, 2*bf)
	c.st.Add("pg_stat_database_blks_hit", lbls, 10000*bf)
}

// ── nginx-prometheus-exporter :9113 ─────────────────────────────────────────

func (c *Construct) emitNginx(svc string, bf float64) {
	lbls := c.baseLabels(svc)

	// counter
	c.st.Add("nginx_http_requests_total", lbls, 1000*bf)

	// gauges
	c.st.Set("nginx_connections_active", lbls, 25*bf)

	// counters
	c.st.Add("nginx_connections_accepted", lbls, 1050*bf)
	c.st.Add("nginx_connections_handled", lbls, 1050*bf)
}
