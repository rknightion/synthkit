// SPDX-License-Identifier: AGPL-3.0-only

// Package docdb emits AWS/DocDB CloudWatch metrics for one declared DocumentDB cluster.
//
// Kind:     "docdb"
// Scope:    core.ScopeBlueprint
// Signals:  []core.SignalClass{core.Metrics}
// Interval: 60s
// Config:   no YAML knobs; the DB fixture carries identity (see "Causal binding" for the
// resolver-filled databases[].resource)
//
// Build requires:
//   - fx.DB    non-nil (name = cluster identifier, engine = "docdb")
//   - fx.Cloud non-nil (account_id, region)
//
// Signal contract (signals/cw.md [slug: cw-docdb]):
//
//	aws_docdb_cpuutilization, aws_docdb_database_connections, aws_docdb_freeable_memory,
//	aws_docdb_read_latency, aws_docdb_write_latency, aws_docdb_read_iops, aws_docdb_write_iops,
//	aws_docdb_buffer_cache_hit_ratio, aws_docdb_opcounters_{insert,query,update,delete,getmore,command},
//	aws_docdb_documents_{inserted,returned,updated,deleted}, aws_docdb_swap_usage.
//	All five stat suffixes emitted per base.
//
// Dimensions: dimension_DBClusterIdentifier, dimension_Role ∈ {WRITER,READER}.
//
// Causal binding (opt-in, SKT-0184): a database entry that names a declared `dependency` or
// `pool` resource (databases[].resource) is driven by the shared causal model instead of the
// legacy database failure modes. ReadLatency and WriteLatency are scaled by the resource's
// latency factor (the dependency's, or for a pool the dependency it is held across), averaged
// over the model's grid steps inside the tick window; the WRITER row of DatabaseConnections
// follows the pool's mean in-use connections when bound to a pool; the READER row and a
// dependency-bound cluster keep the baseline, so the pool's connections are counted once. No
// other family reads the model and no name is added. dependency_unavailable and
// credential_expired have no sourced DocumentDB metric, so they move nothing here. A bound
// cluster ignores the legacy AxisDatabase modes (the loader rejects scheduling them against
// it), so one cause moves it. An unbound cluster runs the legacy path byte for byte.
package docdb

import (
	"context"
	"fmt"
	"time"

	"github.com/rknightion/synthkit/internal/causal"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/cw"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/shape"
	"github.com/rknightion/synthkit/internal/state"
)

// Config carries no YAML knobs - the DB fixture (engine=docdb) carries identity. The unexported
// fields are filled by the resolver (BindResource, UseResources), never decoded.
type Config struct {
	// resource is the causal resource this cluster is bound to ("" = unbound, the legacy path).
	resource string
	// set is the blueprint's declared causal resources (causal.Consumer), injected only when the
	// blueprint declares at least one.
	set causal.Set
}

// BindResource records the causal resource the database entry names (databases[].resource). The
// blueprint resolver duck-types it, so the blueprint package imports no construct.
func (c *Config) BindResource(name string) { c.resource = name }

// UseResources receives the declared causal resources (causal.Consumer).
func (c *Config) UseResources(s causal.Set) { c.set = s }

var _ causal.Consumer = (*Config)(nil)

// Construct is the per-instance DocDB renderer. Not exported; callers use Build.
type Construct struct {
	db    *fixture.DB
	cloud *fixture.Cloud
	env   *fixture.Env
	st    *state.State

	// model is the compiled causal model; nil when the cluster is unbound.
	model    *causal.Model
	resource string // resolved name of the bound resource
	pool     bool   // the bound resource is a pool, so connections follow its in-use count
}

// Compile-time interface checks.
var (
	_ core.Construct  = (*Construct)(nil)
	_ causal.Advancer = (*Construct)(nil)
)

// Build validates fixtures and returns a ready core.Construct instance.
func Build(cfgAny any, fx *fixture.Set) (core.Construct, error) {
	if fx == nil || fx.Cloud == nil {
		return nil, fmt.Errorf("docdb: Build requires a non-nil fixture.Cloud")
	}
	if fx.DB == nil {
		return nil, fmt.Errorf("docdb: Build requires a non-nil fixture.DB")
	}
	c := &Construct{db: fx.DB, cloud: fx.Cloud, env: fx.Env, st: state.NewState()}
	if cfg, ok := cfgAny.(*Config); ok && cfg != nil && cfg.resource != "" {
		r, err := resolveBinding(cfg, fx)
		if err != nil {
			return nil, err
		}
		m, err := causal.Compile(cfg.set)
		if err != nil {
			return nil, fmt.Errorf("docdb: causal model: %w", err)
		}
		c.model, c.resource, c.pool = m, r.Name, r.Kind == causal.KindPool
	}
	return c, nil
}

// resolveBinding checks one bound cluster against the injected causal set and returns the
// resource it resolved. The resource is resolved the db_instance way: "<name>-<lower(env)>"
// first, then the exact name; the model is addressed by the resolved resource's own name.
func resolveBinding(cfg *Config, fx *fixture.Set) (causal.Resource, error) {
	if cfg.set.Empty() {
		return causal.Resource{}, fmt.Errorf("docdb: database %q resource %q: the blueprint declares no environments[].resources", fx.DB.Name, cfg.resource)
	}
	env := ""
	if fx.Env != nil {
		env = fx.Env.Name
	}
	r, _, ok := cfg.set.Resolve(cfg.resource, env)
	if !ok {
		return causal.Resource{}, fmt.Errorf("docdb: database %q resource %q is not a declared resource", fx.DB.Name, cfg.resource)
	}
	if r.Kind != causal.KindDependency && r.Kind != causal.KindPool {
		return causal.Resource{}, fmt.Errorf("docdb: database %q resource %q is a %s resource; want kind dependency or pool", fx.DB.Name, cfg.resource, r.Kind)
	}
	return r, nil
}

// AdvanceCausal implements causal.Advancer: the composition root advances the model on every
// master tick so bound consumers sample live faults at the same instants.
func (c *Construct) AdvanceCausal(now time.Time, clk causal.Clock) {
	if c.model != nil {
		c.model.Advance(now, clk)
	}
}

// staticClock is the model's clock when no shape engine is present: no fault, full business.
type staticClock struct{}

func (staticClock) Eval(time.Time, string, string) (bool, float64) { return false, 0 }
func (staticClock) Factor(time.Time, float64, bool) float64        { return 1 }
func (staticClock) VolumeMultiplier() float64                      { return 1 }

// advance brings the model to now before a Tick reads it (idempotent after AdvanceCausal).
func (c *Construct) advance(now time.Time, eng *shape.Engine) {
	if eng != nil {
		c.model.Advance(now, eng)
		return
	}
	c.model.Advance(now, staticClock{})
}

// boundWindow is the bound resource's mean latency factor and mean in-use connections over the
// model's grid steps inside the tick window (now - Interval, now]. haveConns reports that a
// pool supplied the connection count. A window holding no step (a model that has not reached
// it) reads as the baseline: factor 1 and no connection override.
func (c *Construct) boundWindow(now time.Time) (lf, conns float64, haveConns bool) {
	lf = 1
	from := now.Add(-c.Interval())
	var sumLF, sumInUse float64
	var steps int
	for k := floorGrid(from) + 1; k <= floorGrid(now); k++ {
		st, ok := c.model.State(c.resource, time.Unix(0, k*int64(causal.Step)).UTC())
		if !ok || st.At.UnixNano() != k*int64(causal.Step) {
			continue
		}
		steps++
		sumLF += st.LatencyFactor
		sumInUse += st.InUse
	}
	if steps == 0 {
		return lf, 0, false
	}
	return sumLF / float64(steps), sumInUse / float64(steps), c.pool
}

func floorGrid(t time.Time) int64 {
	n, d := t.UnixNano(), int64(causal.Step)
	q := n / d
	if n%d != 0 && n < 0 {
		q--
	}
	return q
}

func (c *Construct) Kind() string { return "docdb" }
func (c *Construct) Signals() []core.SignalClass {
	if c.cloud.CloudWatchExportMode() == "otlp" {
		return []core.SignalClass{core.OTLPMetrics}
	}
	return []core.SignalClass{core.Metrics}
}
func (c *Construct) Interval() time.Duration { return 60 * time.Second }

// Tick renders one 60-second observation window into w.Metrics.
// All series use state.Set (per-period gauges, ARCHITECTURE I5 — NEVER state.Add).
func (c *Construct) Tick(ctx context.Context, now time.Time, w *core.World) error {
	bf := w.Shape.BusinessFactor(now)

	// Failure-mode amplifiers (AxisDatabase, scoped to db.Name — the SAME scope key any
	// future dbo11y construct would use for coherent incident propagation). A bound cluster
	// reads the shared causal model instead: the latency factor scales the latency families and
	// a pool's in-use count is the writer's connection count, so it is moved by one cause alone.
	connMult, latMult := 1.0, 1.0
	var poolConns float64
	var havePool bool
	if c.model != nil {
		c.advance(now, w.Shape)
		latMult, poolConns, havePool = c.boundWindow(now)
	} else {
		connMult = w.Shape.FailFactor(now, "connection_saturation", c.db.Name, 3.0)
		latMult = w.Shape.FailFactor(now, "slow_query_storm", c.db.Name, 4.0)
	}
	// connections is the baseline every role reports. A pool's in-use count is the connections
	// the service's pool holds, which are the cluster's writer connections; it is reported on the
	// WRITER row alone so summing the roles counts them once. The construct declares no role
	// weights, so the READER row keeps its baseline rather than inventing a split.
	baseConns := (20 + 80*bf) * connMult

	for _, role := range []string{"WRITER", "READER"} {
		lbls := c.baseLabels(map[string]string{
			"dimension_DBClusterIdentifier": c.db.Name,
			"dimension_Role":                role,
		})
		// Base names sourced VERBATIM from signals/cw.md [slug: cw-docdb].
		setGauge(c.st, "aws_docdb_cpuutilization", lbls, 18+12*bf)
		connections := baseConns
		if havePool && role == "WRITER" {
			connections = poolConns
		}
		setGauge(c.st, "aws_docdb_database_connections", lbls, connections)
		setGauge(c.st, "aws_docdb_freeable_memory", lbls, 8e9-2e9*bf)
		setGauge(c.st, "aws_docdb_buffer_cache_hit_ratio", lbls, 99-2*bf)
		setGauge(c.st, "aws_docdb_read_latency", lbls, (0.001+0.003*bf)*latMult)  // seconds
		setGauge(c.st, "aws_docdb_write_latency", lbls, (0.002+0.005*bf)*latMult) // seconds
		setGauge(c.st, "aws_docdb_read_iops", lbls, 100+900*bf)
		setGauge(c.st, "aws_docdb_write_iops", lbls, 50+450*bf)
		setGauge(c.st, "aws_docdb_swap_usage", lbls, 0)
		for _, op := range []string{"insert", "query", "update", "delete", "getmore", "command"} {
			setGauge(c.st, "aws_docdb_opcounters_"+op, lbls, 10+90*bf)
		}
		for _, d := range []string{"inserted", "returned", "updated", "deleted"} {
			setGauge(c.st, "aws_docdb_documents_"+d, lbls, 10+90*bf)
		}
	}
	// Info series — no stat suffix, gauge=1.
	c.st.Set("aws_docdb_info", c.baseLabels(map[string]string{
		"dimension_DBClusterIdentifier": c.db.Name,
	}), 1)
	batch := c.st.Collect(now)
	if c.cloud.CloudWatchExportMode() == "otlp" {
		_, err := cw.WriteMetricStreams(ctx, w.OTLPMetrics, c.cloud, batch)
		return err
	}
	return w.Metrics.Write(ctx, batch)
}

// baseLabels builds the full CloudWatch label set for one series.
// extra labels (dimension_*, tag_*) are merged in. Absent dimensions are omitted (I13).
func (c *Construct) baseLabels(extra map[string]string) map[string]string {
	m := map[string]string{
		"account_id": c.cloud.AccountID,
		"region":     c.cloud.Region,
		"namespace":  "AWS/DocDB",
		"job":        "cloud/aws/docdb",
		"name": fmt.Sprintf("arn:aws:rds:%s:%s:cluster:%s",
			c.cloud.Region, c.cloud.AccountID, c.db.Name),
	}
	for k, v := range extra {
		if v != "" { // I13: absent dimension OMITTED
			m[k] = v
		}
	}
	return m
}

// setGauge emits the full five-stat CW family for one DocDB per-period metric.
// All values use state.Set (per-period GAUGE — NEVER Add; ARCHITECTURE I5).
func setGauge(st *state.State, name string, lbls map[string]string, v float64) {
	cw.EmitStats(st, name, lbls, cw.StatSet{Sum: v, Average: v, Maximum: v, Minimum: v, SampleCount: 60})
}
