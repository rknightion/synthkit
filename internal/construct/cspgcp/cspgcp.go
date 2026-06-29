// SPDX-License-Identifier: AGPL-3.0-only

// Package cspgcp implements the "csp_gcp" construct.
//
// Kind:     "csp_gcp"
// Scope:    core.ScopeSubstrate — CSP families carry NO blueprint label (ARCHITECTURE §5/I21)
// Signals:  []core.SignalClass{core.Metrics, core.Logs}
// Interval: 60s
// Config:   projects (int, default 2), company (default "demo"),
//
//	sub_signals []string (default all: compute, databases, storage, networking,
//	                      loadbalancing, pubsub, cloudrun, bigtable, logs)
//
// FEATURE construct — declared under features: in the blueprint YAML.
//
// Identity: GCP project IDs are "<company>-NN" (2-digit zero-padded), regions cycle
// through a default list. Resources are DETERMINISTIC from fx.Seed per the extract
// identity shapes.
//
// DB fixture handling: if fx.DBs contains entries, Cloud SQL instances are taken from
// the fixture set (MySQL → Cloud SQL MySQL, odd-index Postgres → Cloud SQL Postgres,
// per the DB-hosting cohesion map in the extract §4.3). With no fixtures the emitter
// falls back to one synthetic MySQL instance per project.
//
// Signal contract: signals/cspgcp.md [slug: cspgcp]
//
// Critical invariants (wrong = app panels/variable-pickers dark):
//   - EVERY series carries job="integrations/gcp" + project_id + unit (always present, even "1").
//   - GAUGE→state.Set; CUMULATIVE/DELTA→state.Add; DISTRIBUTION→state.Observe with BASE name.
//   - Bigtable uses exported_instance (NOT instance). (§K.3)
//   - Cloud Run CONTAINERS: emit BOTH state="active" and state="idle". (§K.4)
//   - AlloyDB INSTANCES: emit BOTH status="up" and status="down". (§K.5)
//   - Cloud SQL INSTANCE_STATE: emit RUNNABLE + RUNNING + SUSPENDED + PENDING_CREATE + MAINTENANCE + FAILED + UNKNOWN_STATE. (J1)
//   - Cloud SQL REPLICATION_STATE: emit HEALTHY + UNHEALTHY. (J2)
//   - Pub/Sub unacked_bytes_by_region: MUST be emitted. (§J.4)
//   - database_id = "<project_id>:<instance_name>" (Cloud SQL join key).
package cspgcp

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/genai"
	"github.com/rknightion/synthkit/internal/sink/loki"
	"github.com/rknightion/synthkit/internal/sink/promrw"
	"github.com/rknightion/synthkit/internal/state"
)

const (
	gcpJob = "integrations/gcp"
)

// expBuckets returns n exponential histogram bounds: [scale*growth^0, ..., scale*growth^(n-1)].
// Real Stackdriver DISTRIBUTION metrics use per-metric exponentialBuckets params
// (scale, growthFactor, numFiniteBuckets); synthkit reproduces the shape so le values
// match what the GCP integration dashboard expects. Unit is per-metric (e.g. Cloud Run
// latency = ms, LB latency = ms). Rendered LEBare (native Prometheus scrape path).
func expBuckets(scale, growth float64, n int) []float64 {
	bs := make([]float64, n)
	v := scale
	for i := range n {
		bs[i] = v
		v *= growth
	}
	return bs
}

// ExpBucketsForTest exposes expBuckets for white-box testing of bucket shapes.
func ExpBucketsForTest(scale, growth float64, n int) []float64 { return expBuckets(scale, growth, n) }

// gcpRunLatencyBuckets is the exponential bound set for Cloud Run latency DISTRIBUTIONs
// (run.googleapis.com). Unit: ms. Params captured from live Stackdriver export.
var gcpRunLatencyBuckets = expBuckets(10, 1.1, 135)

// gcpLBLatencyBuckets is the exponential bound set for HTTPS LB + Pub/Sub + Bigtable
// latency DISTRIBUTIONs. Unit: ms. Params captured from live Stackdriver export.
var gcpLBLatencyBuckets = expBuckets(1, 1.4, 66)

// gcpDistBuckets is a representative exponential bound set for non-latency
// DISTRIBUTIONs (e.g. Cloud Run cpu/memory/concurrency).
var gcpDistBuckets = expBuckets(1, 1.4, 20)

// defaultGCPRegions matches the DefaultProfile from the extract estate §4.2.
var defaultGCPRegions = []string{"europe-west1", "us-central1", "asia-southeast1"}

// defaultSubSignals is the full sub-signal set.
// NOTE: "vertex" is NOT in this list — it must be requested explicitly via sub_signals.
var defaultSubSignals = []string{
	"compute", "databases", "storage", "networking",
	"loadbalancing", "pubsub", "cloudrun", "bigtable", "logs",
}

// Kind is the registry key.
const Kind = "csp_gcp"

// Config is the construct's YAML config struct.
type Config struct {
	// Projects is the number of synthetic GCP projects to emit (default 2).
	Projects int `yaml:"projects"`
	// Company is the company slug for project IDs: "<company>-NN" (default "demo").
	Company string `yaml:"company"`
	// SubSignals is the per-service-family emission switch. When empty, all families are
	// enabled. Set to a non-empty list to emit only those families and suppress the rest.
	// Valid values: compute, databases, storage, networking, loadbalancing, pubsub,
	// cloudrun, bigtable, logs.
	// OPT-IN ONLY (not in default set): vertex — Vertex AI Endpoint + Model Invocation metrics.
	// Blueprint must list it explicitly: sub_signals: [vertex]
	SubSignals []string `yaml:"sub_signals"`
}

// NewConfig returns a pointer to a default-zero Config for the YAML decoder.
func NewConfig() any { return &Config{} }

// gcpProject is the resolved identity of one synthetic GCP project.
type gcpProject struct {
	projectID string
	region    string
}

// Construct is the per-instance csp_gcp renderer. Not exported; callers use Build.
type Construct struct {
	projects   []gcpProject
	subSignals map[string]bool
	dbs        []*fixture.DB
	env        *fixture.Env // nil for substrate-aggregate path; set when vertex is env-scoped
	seed       string
	st         *state.State
}

// Compile-time interface check.
var _ core.Construct = (*Construct)(nil)

// Build validates cfg and fx, resolves projects deterministically, and returns a
// ready core.Construct instance.
func Build(cfg any, fx *fixture.Set) (core.Construct, error) {
	c, ok := cfg.(*Config)
	if !ok || c == nil {
		return nil, fmt.Errorf("cspgcp: Build called with %T, want *Config", cfg)
	}

	n := c.Projects
	if n <= 0 {
		n = 2
	}
	company := c.Company
	if company == "" {
		company = "demo"
	}

	subSignals := c.SubSignals
	if len(subSignals) == 0 {
		subSignals = defaultSubSignals
	}
	sigs := make(map[string]bool, len(subSignals))
	for _, s := range subSignals {
		sigs[s] = true
	}

	seed := ""
	if fx != nil {
		seed = fx.Seed
	}

	projects := make([]gcpProject, n)
	for i := range n {
		projects[i] = gcpProject{
			projectID: fmt.Sprintf("%s-%02d", company, i+1),
			region:    defaultGCPRegions[i%len(defaultGCPRegions)],
		}
	}

	var dbs []*fixture.DB
	var env *fixture.Env
	if fx != nil {
		dbs = fx.DBs
		env = fx.Env
	}

	return &Construct{
		projects:   projects,
		subSignals: sigs,
		dbs:        dbs,
		env:        env,
		seed:       seed,
		st:         state.NewState(),
	}, nil
}

func (c *Construct) Kind() string                { return Kind }
func (c *Construct) Signals() []core.SignalClass { return []core.SignalClass{core.Metrics, core.Logs} }
func (c *Construct) Interval() time.Duration     { return 60 * time.Second }

// Tick renders one 60-second observation window into w.Metrics and (if enabled) w.Logs.
func (c *Construct) Tick(ctx context.Context, now time.Time, w *core.World) error {
	batch := c.renderMetrics(now, w)
	if w.Metrics != nil {
		if err := w.Metrics.Write(ctx, batch); err != nil {
			return err
		}
	}
	if w.Logs != nil && c.subSignals["logs"] {
		streams := c.renderLogs(now)
		if err := w.Logs.Write(ctx, streams); err != nil {
			return err
		}
	}
	return nil
}

// renderMetrics builds the full per-tick batch across all projects. Separated so
// tests can call it without a full World.
func (c *Construct) renderMetrics(now time.Time, w *core.World) []promrw.Series {
	// B1 branch: env-scoped (fanned per-cell) uses the env-weighted Factor; aggregate keeps
	// BusinessFactor byte-for-byte (Factor(now,1,false) ≠ BusinessFactor on weekends).
	bf := w.Shape.BusinessFactor(now) * w.Shape.Noise(0.1)
	if c.env != nil {
		bf = w.Shape.Factor(now, c.env.Weight, c.env.NonProd) * w.Shape.Noise(0.1)
	}
	for _, proj := range c.projects {
		c.metricsForProject(now, w, proj, bf)
	}
	series := c.st.Collect(now)
	// Env-scoped: stamp env on EVERY series so per-cell instances don't push byte-identical
	// stackdriver_* series (Mimir duplicate-sample rejection). Single chokepoint because the
	// per-family emitters build labels inline (no shared baseLabels helper). Aggregate (c.env nil)
	// leaves series env-less (single instance, emitted once; I13).
	if c.env != nil {
		for i := range series {
			series[i].Labels["env"] = c.env.Name
		}
	}
	return series
}

// ── per-project metric groups ─────────────────────────────────────────────────

func (c *Construct) metricsForProject(now time.Time, w *core.World, proj gcpProject, bf float64) {
	if c.subSignals["compute"] {
		c.emitCompute(now, proj, bf)
	}
	if c.subSignals["databases"] {
		c.emitCloudSQL(proj, bf)
		c.emitAlloyDB(proj, bf)
	}
	if c.subSignals["storage"] {
		c.emitStorage(proj, bf)
	}
	if c.subSignals["networking"] {
		c.emitNetworking(proj, bf)
	}
	if c.subSignals["loadbalancing"] {
		c.emitLoadBalancing(w, proj, bf)
	}
	if c.subSignals["pubsub"] {
		c.emitPubSub(w, proj, bf)
	}
	if c.subSignals["cloudrun"] {
		c.emitCloudRun(w, proj, bf)
	}
	if c.subSignals["bigtable"] {
		c.emitBigtable(w, proj, bf)
	}
	if c.subSignals["vertex"] {
		c.emitVertex(now, w, proj, bf)
	}
}

// ── label helpers ────────────────────────────────────────────────────────────

// gcpLabels merges gcpBaseLabels(projectID, unit) with per-resource extra labels.
// The `unit` label is ALWAYS present, even when "1" (§K.1).
func gcpLabels(projectID, unit string, extra map[string]string) map[string]string {
	m := map[string]string{
		"job":        gcpJob,
		"project_id": projectID,
		"unit":       unit,
	}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

// mapMerge returns a new map with all keys from base plus overrides from extra.
func mapMerge(base, extra map[string]string) map[string]string {
	out := make(map[string]string, len(base)+len(extra))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

// ── Compute Engine ────────────────────────────────────────────────────────────

// vmInstanceID returns a deterministic numeric string for a GCE instance_id resource
// label, derived from the VM name via a simple hash. Real gce_instance resource labels
// are {project_id, instance_id, zone}; instance_name is a metric label.
func vmInstanceID(vmName string) string {
	h := uint64(14695981039346656037) // FNV-1a offset
	for _, b := range []byte(vmName) {
		h ^= uint64(b)
		h *= 1099511628211
	}
	// Clamp to a plausible GCE instance numeric ID range (10^18 ≈ max uint64/10).
	return fmt.Sprintf("%d", h%9000000000000000000+1000000000000000000)
}

func (c *Construct) emitCompute(now time.Time, proj gcpProject, bf float64) {
	vmNames := []string{"vm-app-01", "vm-app-02", "vm-app-03"}
	zones := []string{proj.region + "-a", proj.region + "-b", proj.region + "-c"}

	for i, vmName := range vmNames {
		zone := zones[i%len(zones)]
		instanceID := vmInstanceID(vmName)
		// GAUGE
		c.st.Set("stackdriver_gce_instance_compute_googleapis_com_instance_cpu_utilization",
			gcpLabels(proj.projectID, "1", map[string]string{
				"instance_name": vmName, "zone": zone, "instance_id": instanceID,
			}), math.Min(1.0, 0.25*bf))
		// CUMULATIVE → Add
		c.st.Add("stackdriver_gce_instance_compute_googleapis_com_instance_cpu_usage_time",
			gcpLabels(proj.projectID, "1", map[string]string{
				"instance_name": vmName, "zone": zone, "instance_id": instanceID,
			}), math.Round(60*0.25*bf))
		c.st.Add("stackdriver_gce_instance_compute_googleapis_com_instance_network_received_bytes_count",
			gcpLabels(proj.projectID, "By", map[string]string{
				"instance_name": vmName, "zone": zone, "instance_id": instanceID,
			}), math.Round(1_000_000*bf))
		c.st.Add("stackdriver_gce_instance_compute_googleapis_com_instance_network_sent_bytes_count",
			gcpLabels(proj.projectID, "By", map[string]string{
				"instance_name": vmName, "zone": zone, "instance_id": instanceID,
			}), math.Round(500_000*bf))
		c.st.Add("stackdriver_gce_instance_compute_googleapis_com_instance_disk_read_bytes_count",
			gcpLabels(proj.projectID, "By", map[string]string{
				"instance_name": vmName, "zone": zone, "instance_id": instanceID,
			}), math.Round(200_000*bf))
		c.st.Add("stackdriver_gce_instance_compute_googleapis_com_instance_disk_write_bytes_count",
			gcpLabels(proj.projectID, "By", map[string]string{
				"instance_name": vmName, "zone": zone, "instance_id": instanceID,
			}), math.Round(100_000*bf))
		c.st.Add("stackdriver_gce_instance_compute_googleapis_com_instance_disk_read_ops_count",
			gcpLabels(proj.projectID, "1", map[string]string{
				"instance_name": vmName, "zone": zone, "instance_id": instanceID,
			}), math.Round(50*bf))
		c.st.Add("stackdriver_gce_instance_compute_googleapis_com_instance_disk_write_ops_count",
			gcpLabels(proj.projectID, "1", map[string]string{
				"instance_name": vmName, "zone": zone, "instance_id": instanceID,
			}), math.Round(30*bf))
	}
}

// ── Cloud SQL ────────────────────────────────────────────────────────────────

// cloudSQLInstance is the resolved identity of one Cloud SQL instance.
type cloudSQLInstance struct {
	engine     string // "mysql" | "postgres"
	name       string
	databaseID string // "project_id:instance_name"
}

// resolveCloudSQLInstances returns the Cloud SQL instances for a given project.
// If fx.DBs is populated, MySQL DBs with Cloud==GCP on this project, and odd-index
// Postgres DBs with Cloud==GCP on this project are used. Otherwise a synthetic MySQL
// instance is returned so the anchor metric always exists.
func (c *Construct) resolveCloudSQLInstances(proj gcpProject) []cloudSQLInstance {
	if len(c.dbs) == 0 {
		// self-contained fallback
		synthName := fmt.Sprintf("mysql-%s-synth", proj.projectID)
		return []cloudSQLInstance{
			{engine: "mysql", name: synthName, databaseID: proj.projectID + ":" + synthName},
		}
	}

	var out []cloudSQLInstance
	pgIdx := 0
	for _, db := range c.dbs {
		if db.Cloud == nil {
			pgIdx++
			continue
		}
		if db.Cloud.Provider != "gcp" {
			pgIdx++
			continue
		}
		// ProviderAccount must match projectID for Cloud SQL assignment.
		// We use the DB name pattern to infer project. If DB has no explicit
		// provider account we fall back to emitting against the first project.
		switch db.Engine {
		case "mysql":
			// All MySQL → Cloud SQL MySQL (§4.3)
			out = append(out, cloudSQLInstance{
				engine:     "mysql",
				name:       db.Name,
				databaseID: proj.projectID + ":" + db.Name,
			})
		case "postgres":
			// Odd-index postgres → Cloud SQL PG (§4.3)
			if pgIdx%2 == 1 {
				out = append(out, cloudSQLInstance{
					engine:     "postgres",
					name:       db.Name,
					databaseID: proj.projectID + ":" + db.Name,
				})
			}
			pgIdx++
		}
	}

	if len(out) == 0 {
		// Fallback: no fixture matched this project
		synthName := fmt.Sprintf("mysql-%s-synth", proj.projectID)
		out = append(out, cloudSQLInstance{
			engine:     "mysql",
			name:       synthName,
			databaseID: proj.projectID + ":" + synthName,
		})
	}
	return out
}

func (c *Construct) emitCloudSQL(proj gcpProject, bf float64) {
	instances := c.resolveCloudSQLInstances(proj)

	for _, inst := range instances {
		lbls := gcpLabels(proj.projectID, "1", map[string]string{
			"instance":    inst.name,
			"database_id": inst.databaseID,
			"region":      proj.region,
		})

		// ── common metrics (all Cloud SQL instances) ──────────────────────────────
		// DATABASE_UP — GAUGE (anchor metric)
		c.st.Set("stackdriver_cloudsql_database_cloudsql_googleapis_com_database_up", lbls, 1)

		// CPU / Memory / Disk — GAUGE
		c.st.Set("stackdriver_cloudsql_database_cloudsql_googleapis_com_database_cpu_utilization",
			lbls, math.Min(1.0, 0.3*bf))
		c.st.Set("stackdriver_cloudsql_database_cloudsql_googleapis_com_database_memory_utilization",
			lbls, math.Min(1.0, 0.4*bf))
		c.st.Set("stackdriver_cloudsql_database_cloudsql_googleapis_com_database_disk_utilization",
			lbls, math.Min(1.0, 0.2*bf))
		c.st.Set("stackdriver_cloudsql_database_cloudsql_googleapis_com_database_available_for_failover",
			lbls, 1)
		c.st.Set("stackdriver_cloudsql_database_cloudsql_googleapis_com_database_cpu_reserved_cores",
			lbls, 4)
		c.st.Set("stackdriver_cloudsql_database_cloudsql_googleapis_com_database_memory_quota",
			lbls, 16*1024*1024*1024)
		c.st.Set("stackdriver_cloudsql_database_cloudsql_googleapis_com_database_disk_quota",
			lbls, 500*1024*1024*1024)

		// Network — CUMULATIVE
		netLbls := gcpLabels(proj.projectID, "By", map[string]string{
			"instance": inst.name, "database_id": inst.databaseID, "region": proj.region,
		})
		c.st.Add("stackdriver_cloudsql_database_cloudsql_googleapis_com_database_disk_read_ops_count",
			lbls, math.Round(100*bf))
		c.st.Add("stackdriver_cloudsql_database_cloudsql_googleapis_com_database_disk_write_ops_count",
			lbls, math.Round(50*bf))
		c.st.Set("stackdriver_cloudsql_database_cloudsql_googleapis_com_database_network_connections",
			lbls, math.Round(50*bf))
		c.st.Add("stackdriver_cloudsql_database_cloudsql_googleapis_com_database_network_received_bytes_count",
			netLbls, math.Round(500_000*bf))
		c.st.Add("stackdriver_cloudsql_database_cloudsql_googleapis_com_database_network_sent_bytes_count",
			netLbls, math.Round(200_000*bf))

		// INSTANCE_STATE enum — emit ALL values (load-bearing: incident panels) (J1)
		// Full live enum from SK-18 capture 2026-06-14.
		for _, st := range []string{"RUNNABLE", "RUNNING", "SUSPENDED", "PENDING_CREATE", "MAINTENANCE", "FAILED", "UNKNOWN_STATE"} {
			v := 0.0
			if st == "RUNNABLE" {
				v = 1.0
			}
			c.st.Set("stackdriver_cloudsql_database_cloudsql_googleapis_com_database_instance_state",
				gcpLabels(proj.projectID, "1", map[string]string{
					"instance": inst.name, "database_id": inst.databaseID,
					"region": proj.region, "state": st,
				}), v)
		}

		// REPLICATION_STATE enum — emit ALL values (J2)
		for _, st := range []string{"HEALTHY", "UNHEALTHY"} {
			v := 1.0
			if st == "UNHEALTHY" {
				v = 0.0
			}
			c.st.Set("stackdriver_cloudsql_database_cloudsql_googleapis_com_database_replication_state",
				gcpLabels(proj.projectID, "1", map[string]string{
					"instance": inst.name, "database_id": inst.databaseID,
					"region": proj.region, "state": st,
				}), v)
		}

		// ── MySQL-specific metrics ────────────────────────────────────────────────
		if inst.engine == "mysql" {
			c.st.Set("stackdriver_cloudsql_database_cloudsql_googleapis_com_database_mysql_innodb_buffer_pool_pages_total",
				lbls, 8192)
			c.st.Set("stackdriver_cloudsql_database_cloudsql_googleapis_com_database_mysql_innodb_buffer_pool_pages_free",
				lbls, math.Round(8192*(1-0.3*bf)))
			c.st.Set("stackdriver_cloudsql_database_cloudsql_googleapis_com_database_mysql_innodb_buffer_pool_pages_dirty",
				lbls, math.Round(100*bf))
		}

		// ── PostgreSQL-specific metrics ───────────────────────────────────────────
		if inst.engine == "postgres" {
			c.st.Set("stackdriver_cloudsql_database_cloudsql_googleapis_com_database_postgresql_num_backends",
				lbls, math.Round(20*bf))
			c.st.Add("stackdriver_cloudsql_database_cloudsql_googleapis_com_database_postgresql_transaction_count",
				lbls, math.Round(500*bf))
		}
	}
}

// ── AlloyDB ──────────────────────────────────────────────────────────────────

func (c *Construct) emitAlloyDB(proj gcpProject, bf float64) {
	clusterID := fmt.Sprintf("alloydb-%s", proj.projectID)
	location := proj.region

	instances := []struct{ id, node string }{
		{clusterID + "-primary", clusterID + "-primary-node-0"},
		{clusterID + "-replica", clusterID + "-replica-node-0"},
	}

	for _, inst := range instances {
		instLbls := gcpLabels(proj.projectID, "1", map[string]string{
			"instance_id": inst.id,
			"cluster_id":  clusterID,
			"location":    location,
		})

		// INSTANCES enum — emit BOTH "up" and "down" (§K.5 — load-bearing)
		for _, status := range []string{"up", "down"} {
			v := 1.0
			if status == "down" {
				v = 0.0
			}
			c.st.Set("stackdriver_alloydb_googleapis_com_instance_alloydb_googleapis_com_instance_postgres_instances",
				gcpLabels(proj.projectID, "1", map[string]string{
					"instance_id": inst.id, "cluster_id": clusterID,
					"location": location, "status": status,
				}), v)
		}

		c.st.Set("stackdriver_alloydb_googleapis_com_instance_alloydb_googleapis_com_instance_cpu_average_utilization",
			instLbls, math.Min(1.0, 0.3*bf))
		c.st.Set("stackdriver_alloydb_googleapis_com_instance_alloydb_googleapis_com_instance_cpu_maximum_utilization",
			instLbls, math.Min(1.0, 0.5*bf))
		c.st.Add("stackdriver_alloydb_googleapis_com_instance_alloydb_googleapis_com_instance_postgresql_deadlock_count",
			instLbls, math.Round(0.1*bf))
		c.st.Add("stackdriver_alloydb_googleapis_com_instance_alloydb_googleapis_com_instance_postgresql_deleted_tuples_count",
			instLbls, math.Round(100*bf))
		c.st.Add("stackdriver_alloydb_googleapis_com_instance_alloydb_googleapis_com_instance_postgresql_fetched_tuples_count",
			instLbls, math.Round(500*bf))
		c.st.Add("stackdriver_alloydb_googleapis_com_instance_alloydb_googleapis_com_instance_postgresql_inserted_tuples_count",
			instLbls, math.Round(200*bf))
		c.st.Add("stackdriver_alloydb_googleapis_com_instance_alloydb_googleapis_com_instance_postgresql_updated_tuples_count",
			instLbls, math.Round(150*bf))
		c.st.Add("stackdriver_alloydb_googleapis_com_instance_alloydb_googleapis_com_instance_postgresql_written_tuples_count",
			instLbls, math.Round(350*bf))
		c.st.Add("stackdriver_alloydb_googleapis_com_instance_alloydb_googleapis_com_instance_postgresql_returned_tuples_count",
			instLbls, math.Round(800*bf))
		c.st.Add("stackdriver_alloydb_googleapis_com_instance_alloydb_googleapis_com_instance_postgresql_new_connections_count",
			instLbls, math.Round(10*bf))
		c.st.Set("stackdriver_alloydb_googleapis_com_instance_alloydb_googleapis_com_instance_postgres_total_connections",
			instLbls, math.Round(30*bf))
		c.st.Add("stackdriver_alloydb_googleapis_com_instance_alloydb_googleapis_com_instance_postgres_transaction_count",
			instLbls, math.Round(300*bf))
		c.st.Set("stackdriver_alloydb_googleapis_com_instance_alloydb_googleapis_com_database_postgresql_vacuum_oldest_transaction_age",
			instLbls, math.Round(100000*bf))

		// BACKENDS_TOP_APPS — per application_name
		for _, appName := range []string{"demo-api", "demo-worker", "demo-analytics"} {
			c.st.Set("stackdriver_alloydb_googleapis_com_instance_alloydb_googleapis_com_instance_postgresql_backends_for_top_applications",
				gcpLabels(proj.projectID, "1", map[string]string{
					"instance_id": inst.id, "cluster_id": clusterID,
					"location": location, "application_name": appName,
				}), math.Round(5*bf))
		}

		// WAIT_TIME / WAIT_COUNT — per wait_event_name + wait_event_type
		for _, we := range []struct{ name, typ string }{
			{"Lock:relation", "Lock Wait"},
			{"IO:DataFileRead", "IO Wait"},
		} {
			weLbls := gcpLabels(proj.projectID, "ms", map[string]string{
				"instance_id": inst.id, "cluster_id": clusterID, "location": location,
				"wait_event_name": we.name, "wait_event_type": we.typ,
			})
			c.st.Add("stackdriver_alloydb_googleapis_com_instance_node_alloydb_googleapis_com_node_postgres_wait_time",
				weLbls, math.Round(50*bf))
			c.st.Add("stackdriver_alloydb_googleapis_com_instance_node_alloydb_googleapis_com_node_postgres_wait_count",
				weLbls, math.Round(10*bf))
		}

		// BACKENDS_BY_STATE / UPTIME
		nodeLbls := gcpLabels(proj.projectID, "1", map[string]string{
			"instance_id": inst.id, "cluster_id": clusterID, "location": location,
		})
		c.st.Set("stackdriver_alloydb_googleapis_com_instance_node_alloydb_googleapis_com_node_postgres_backends_by_state",
			nodeLbls, math.Round(15*bf))
		// node_postgres_uptime: CUMULATIVE counter (C in §K) — state.Add is correct.
		// Delta = the construct's own tick interval in seconds (60), so uptime tracks
		// wall-clock 1:1 (SK-29 resolved 2026-06-14). The predecessor froze this at 86400/tick,
		// which ran ≈1440× fast; the construct already knows its Interval() so no
		// tick-duration threading is needed. Pinned by TestNodePostgresUptimeDeltaPinned.
		c.st.Add("stackdriver_alloydb_googleapis_com_instance_node_alloydb_googleapis_com_node_postgres_uptime",
			nodeLbls, c.Interval().Seconds())
	}

	// ── AlloyDB Database metrics ──────────────────────────────────────────────
	for _, dbName := range []string{"appdb", "analytics"} {
		for _, dbState := range []string{"live", "dead"} {
			v := 50000.0 * bf
			if dbState == "dead" {
				v = 200 * bf
			}
			c.st.Set("stackdriver_alloydb_googleapis_com_database_alloydb_googleapis_com_database_postgresql_tuples",
				gcpLabels(proj.projectID, "1", map[string]string{
					"instance_id": instances[0].id, "cluster_id": clusterID,
					"location": location, "database": dbName, "state": dbState,
				}), v)
		}

		dbMetaLbls := gcpLabels(proj.projectID, "1", map[string]string{
			"instance_id": instances[0].id, "cluster_id": clusterID,
			"location": location, "database": dbName,
		})
		c.st.Add("stackdriver_alloydb_googleapis_com_database_alloydb_googleapis_com_database_postgresql_blks_read_for_top_databases",
			dbMetaLbls, math.Round(1000*bf))
		c.st.Add("stackdriver_alloydb_googleapis_com_database_alloydb_googleapis_com_database_postgresql_blks_hit_for_top_databases",
			dbMetaLbls, math.Round(50000*bf))
		c.st.Add("stackdriver_alloydb_googleapis_com_database_alloydb_googleapis_com_database_postgresql_temp_bytes_written_for_top_databases",
			dbMetaLbls, math.Round(10000*bf))
		c.st.Add("stackdriver_alloydb_googleapis_com_database_alloydb_googleapis_com_database_postgresql_temp_files_written_for_top_databases",
			dbMetaLbls, math.Round(5*bf))
		c.st.Add("stackdriver_alloydb_googleapis_com_database_alloydb_googleapis_com_database_postgresql_rolledback_transactions_for_top_databases",
			dbMetaLbls, math.Round(2*bf))
		c.st.Add("stackdriver_alloydb_googleapis_com_database_alloydb_googleapis_com_database_postgresql_statements_executed_count",
			dbMetaLbls, math.Round(1000*bf))
	}

	// ── AlloyDB Cluster metrics ───────────────────────────────────────────────
	c.st.Set("stackdriver_alloydb_googleapis_com_cluster_alloydb_googleapis_com_cluster_storage_usage",
		gcpLabels(proj.projectID, "By", map[string]string{
			"cluster_id": clusterID, "location": location,
		}), 50*1024*1024*1024)
}

// ── Cloud Storage ─────────────────────────────────────────────────────────────

func (c *Construct) emitStorage(proj gcpProject, bf float64) {
	buckets := []string{
		fmt.Sprintf("%s-assets", proj.projectID),
		fmt.Sprintf("%s-backups", proj.projectID),
		fmt.Sprintf("%s-data", proj.projectID),
	}
	for _, bucket := range buckets {
		// location is a real gcs_bucket resource label (§SK-18).
		lbls := gcpLabels(proj.projectID, "1", map[string]string{
			"bucket_name": bucket, "location": proj.region,
		})
		byLbls := gcpLabels(proj.projectID, "By", map[string]string{
			"bucket_name": bucket, "location": proj.region,
		})

		// GAUGE (anchor)
		c.st.Set("stackdriver_gcs_bucket_storage_googleapis_com_storage_object_count",
			lbls, math.Round(10000*bf))
		c.st.Set("stackdriver_gcs_bucket_storage_googleapis_com_storage_total_bytes",
			byLbls, math.Round(100*1024*1024*1024*bf))
		// CUMULATIVE
		c.st.Add("stackdriver_gcs_bucket_storage_googleapis_com_network_received_bytes_count",
			byLbls, math.Round(5_000_000*bf))
		c.st.Add("stackdriver_gcs_bucket_storage_googleapis_com_network_sent_bytes_count",
			byLbls, math.Round(10_000_000*bf))
		// api_request_count carries method + response_code metric labels (§SK-18).
		c.st.Add("stackdriver_gcs_bucket_storage_googleapis_com_api_request_count",
			gcpLabels(proj.projectID, "1", map[string]string{
				"bucket_name": bucket, "location": proj.region,
				"method": "ReadObject", "response_code": "OK",
			}), math.Round(500*bf))
	}
}

// ── VPC / Networking ─────────────────────────────────────────────────────────

func (c *Construct) emitNetworking(proj gcpProject, bf float64) {
	// google_service_gce_client
	svcLbls := gcpLabels(proj.projectID, "By", map[string]string{
		"local_resource_type": "vm",
	})
	c.st.Add("stackdriver_google_service_gce_client_networking_googleapis_com_google_service_response_bytes_count",
		svcLbls, math.Round(10_000_000*bf))
	c.st.Add("stackdriver_google_service_gce_client_networking_googleapis_com_google_service_request_bytes_count",
		svcLbls, math.Round(5_000_000*bf))

	// networking.googleapis.com/location
	locLbls := gcpLabels(proj.projectID, "By", map[string]string{})
	c.st.Add("stackdriver_networking_googleapis_com_location_networking_googleapis_com_fixed_standard_tier_usage",
		locLbls, math.Round(1_000_000*bf))

	// vpn_tunnel
	vpnLbls := gcpLabels(proj.projectID, "By", map[string]string{
		"local_resource_type": "vpn_tunnel",
	})
	c.st.Add("stackdriver_vpn_tunnel_networking_googleapis_com_vpn_tunnel_egress_bytes_count",
		vpnLbls, math.Round(2_000_000*bf))
	c.st.Add("stackdriver_vpn_tunnel_networking_googleapis_com_vpn_tunnel_ingress_bytes_count",
		vpnLbls, math.Round(1_500_000*bf))
}

// ── HTTPS Load Balancing ──────────────────────────────────────────────────────

func (c *Construct) emitLoadBalancing(w *core.World, proj gcpProject, bf float64) {
	countries := []string{"US", "DE", "GB", "FR"}
	backends := []string{"backend-api", "backend-static"}

	for _, country := range countries {
		for _, backend := range backends {
			lbls := gcpLabels(proj.projectID, "1", map[string]string{
				"client_country":      country,
				"backend_target_name": backend,
			})
			byLbls := gcpLabels(proj.projectID, "By", map[string]string{
				"client_country":      country,
				"backend_target_name": backend,
			})
			msLbls := gcpLabels(proj.projectID, "ms", map[string]string{
				"client_country":      country,
				"backend_target_name": backend,
			})

			// CUMULATIVE (anchor)
			c.st.Add("stackdriver_https_lb_rule_loadbalancing_googleapis_com_https_request_count",
				lbls, math.Round(200*bf))
			c.st.Add("stackdriver_https_lb_rule_loadbalancing_googleapis_com_https_request_bytes_count",
				byLbls, math.Round(1_000_000*bf))
			c.st.Add("stackdriver_https_lb_rule_loadbalancing_googleapis_com_https_response_bytes_count",
				byLbls, math.Round(5_000_000*bf))
			c.st.Add("stackdriver_https_lb_rule_loadbalancing_googleapis_com_https_backend_request_bytes_count",
				byLbls, math.Round(800_000*bf))
			c.st.Add("stackdriver_https_lb_rule_loadbalancing_googleapis_com_https_backend_response_bytes_count",
				byLbls, math.Round(4_000_000*bf))

			// DISTRIBUTION — pass BASE name (Collect appends _bucket/_sum/_count)
			// gcpLBLatencyBuckets: exponential bounds, unit ms, captured from live Stackdriver.
			obsVal := 50.0 * bf * w.Shape.Noise(0.3)
			c.st.Observe("stackdriver_https_lb_rule_loadbalancing_googleapis_com_https_total_latencies",
				msLbls, gcpLBLatencyBuckets, state.LEBare, obsVal)
			c.st.Observe("stackdriver_https_lb_rule_loadbalancing_googleapis_com_https_frontend_tcp_rtt",
				msLbls, gcpLBLatencyBuckets, state.LEBare, obsVal*0.3)
			c.st.Observe("stackdriver_https_lb_rule_loadbalancing_googleapis_com_https_backend_latencies",
				msLbls, gcpLBLatencyBuckets, state.LEBare, obsVal*0.7)
		}
	}
}

// ── Google Cloud Pub/Sub ──────────────────────────────────────────────────────

func (c *Construct) emitPubSub(w *core.World, proj gcpProject, bf float64) {
	subscriptions := []string{
		fmt.Sprintf("%s-events-sub", proj.projectID),
		fmt.Sprintf("%s-audit-sub", proj.projectID),
	}

	for _, subID := range subscriptions {
		lbls := gcpLabels(proj.projectID, "1", map[string]string{
			"subscription_id": subID,
			"instance":        subID,
		})
		msLbls := gcpLabels(proj.projectID, "ms", map[string]string{
			"subscription_id": subID,
			"instance":        subID,
		})
		byLbls := gcpLabels(proj.projectID, "By", map[string]string{
			"subscription_id": subID,
			"instance":        subID,
		})

		// CUMULATIVE
		c.st.Add("stackdriver_pubsub_subscription_pubsub_googleapis_com_subscription_push_request_count",
			lbls, math.Round(100*bf))
		c.st.Add("stackdriver_pubsub_subscription_pubsub_googleapis_com_subscription_pull_ack_request_count",
			lbls, math.Round(50*bf))
		c.st.Add("stackdriver_pubsub_subscription_pubsub_googleapis_com_subscription_streaming_pull_response_count",
			lbls, math.Round(200*bf))
		c.st.Add("stackdriver_pubsub_subscription_pubsub_googleapis_com_subscription_expired_ack_deadlines_count",
			lbls, math.Round(1*bf))

		// GAUGE
		c.st.Set("stackdriver_pubsub_subscription_pubsub_googleapis_com_subscription_num_outstanding_messages",
			lbls, math.Round(50*bf))
		c.st.Set("stackdriver_pubsub_subscription_pubsub_googleapis_com_subscription_num_undelivered_messages",
			lbls, math.Round(20*bf))
		c.st.Set("stackdriver_pubsub_subscription_pubsub_googleapis_com_subscription_oldest_unacked_message_age",
			lbls, math.Round(30*bf))
		c.st.Set("stackdriver_pubsub_subscription_pubsub_googleapis_com_subscription_delivery_latency_health_score",
			lbls, 1.0)
		c.st.Set("stackdriver_pubsub_subscription_pubsub_googleapis_com_subscription_num_unacked_messages_by_region",
			lbls, math.Round(15*bf))
		// MUST be emitted — variables.ts anchor for project_id + subscription_id (§J.4)
		c.st.Set("stackdriver_pubsub_subscription_pubsub_googleapis_com_subscription_unacked_bytes_by_region",
			byLbls, math.Round(500_000*bf))

		// DISTRIBUTION histogram — pass BASE name (§K.2)
		// gcpLBLatencyBuckets: ms latency, exponential bounds.
		c.st.Observe("stackdriver_pubsub_subscription_pubsub_googleapis_com_subscription_push_request_latencies",
			msLbls, gcpLBLatencyBuckets, state.LEBare, 30.0*bf*w.Shape.Noise(0.3))
	}
}

// ── Cloud Run ────────────────────────────────────────────────────────────────

func (c *Construct) emitCloudRun(w *core.World, proj gcpProject, bf float64) {
	services := []struct{ name, rev string }{
		{"demo-api", "demo-api-00001"},
		{"demo-worker", "demo-worker-00002"},
	}
	location := proj.region

	for _, svc := range services {
		containers := []string{"api", "sidecar"}
		for ci, containerName := range containers {
			baseLbls := map[string]string{
				"container_name":     containerName,
				"service_name":       svc.name,
				"location":           location,
				"revision_name":      svc.rev,
				"configuration_name": svc.name, // real cloud_run_revision resource label (§SK-18)
			}

			// CONTAINERS gauge — emit BOTH state="active" and state="idle" (§K.4 — load-bearing)
			for _, runState := range []string{"active", "idle"} {
				v := bf * 2
				if runState == "idle" {
					v = bf * 0.5
				}
				if ci > 0 {
					v *= 0.3
				}
				c.st.Set("stackdriver_cloud_run_revision_run_googleapis_com_container_containers",
					gcpLabels(proj.projectID, "1", mapMerge(baseLbls, map[string]string{"state": runState})), v)
			}

			lbls := gcpLabels(proj.projectID, "1", baseLbls)
			msLbls := gcpLabels(proj.projectID, "ms", baseLbls)
			cpuLbls := gcpLabels(proj.projectID, "cpu", baseLbls)
			byLbls := gcpLabels(proj.projectID, "By", baseLbls)

			// CUMULATIVE
			c.st.Add("stackdriver_cloud_run_revision_run_googleapis_com_container_network_received_bytes_count",
				byLbls, math.Round(1_000_000*bf))
			c.st.Add("stackdriver_cloud_run_revision_run_googleapis_com_container_network_sent_bytes_count",
				byLbls, math.Round(2_000_000*bf))
			c.st.Add("stackdriver_cloud_run_revision_run_googleapis_com_container_billable_instance_time",
				lbls, math.Round(60*bf))
			c.st.Add("stackdriver_cloud_run_revision_run_googleapis_com_container_network_throttled_inbound_bytes_count",
				byLbls, math.Round(100*bf))
			c.st.Add("stackdriver_cloud_run_revision_run_googleapis_com_container_network_throttled_outbound_bytes_count",
				byLbls, math.Round(50*bf))
			c.st.Add("stackdriver_cloud_run_revision_run_googleapis_com_container_completed_probe_attempt_count",
				lbls, math.Round(30*bf))
			c.st.Add("stackdriver_cloud_run_revision_run_googleapis_com_container_completed_probe_count",
				lbls, math.Round(30*bf))

			// DISTRIBUTION — pass BASE name (§K.2)
			// cpu/memory/concurrency: gcpDistBuckets (not latency).
			// startup/probe latencies: gcpRunLatencyBuckets (ms, exponential).
			c.st.Observe("stackdriver_cloud_run_revision_run_googleapis_com_container_cpu_usage",
				cpuLbls, gcpDistBuckets, state.LEBare, 0.05*bf*w.Shape.Noise(0.2))
			c.st.Observe("stackdriver_cloud_run_revision_run_googleapis_com_container_memory_usage",
				byLbls, gcpDistBuckets, state.LEBare, 128.0*1024*1024*bf*w.Shape.Noise(0.15))
			c.st.Observe("stackdriver_cloud_run_revision_run_googleapis_com_container_max_request_concurrencies",
				lbls, gcpDistBuckets, state.LEBare, 5.0*bf*w.Shape.Noise(0.3))
			c.st.Observe("stackdriver_cloud_run_revision_run_googleapis_com_container_startup_latencies",
				msLbls, gcpRunLatencyBuckets, state.LEBare, 200.0*bf*w.Shape.Noise(0.4))

			// Probe latency histograms
			for _, probeType := range []string{"liveness", "readiness"} {
				probeLbls := gcpLabels(proj.projectID, "ms", mapMerge(baseLbls, map[string]string{
					"probe_type":   probeType,
					"probe_action": "http",
					"is_healthy":   "true",
				}))
				c.st.Observe("stackdriver_cloud_run_revision_run_googleapis_com_container_probe_attempt_latencies",
					probeLbls, gcpRunLatencyBuckets, state.LEBare, 5.0*bf)
				c.st.Observe("stackdriver_cloud_run_revision_run_googleapis_com_container_probe_latencies",
					probeLbls, gcpRunLatencyBuckets, state.LEBare, 4.0*bf)
			}
		}
	}
}

// ── Cloud Bigtable ────────────────────────────────────────────────────────────

// bigtableMethods is the full SERVER_REQUEST_COUNT method enum (extract §2.5 / W4.2).
var bigtableMethods = []string{
	"Bigtable.ReadRows",
	"Bigtable.MutateRow",
	"Bigtable.MutateRows",
	"Bigtable.CheckAndMutateRow",
	"Bigtable.ReadModifyWriteRow",
	"Bigtable.SampleRowKeys",
	"Bigtable.ExecuteQuery",
}

func (c *Construct) emitBigtable(w *core.World, proj gcpProject, bf float64) {
	// ⚠ Use exported_instance (NOT instance) — §K.3 / variable picker uses label_values(NODE_COUNT, exported_instance)
	instanceName := fmt.Sprintf("bigtable-%s-01", proj.projectID)
	clusterName := instanceName + "-cluster"
	tableName := "user-events"

	// bigtable_cluster metrics
	clLbls := gcpLabels(proj.projectID, "1", map[string]string{
		"exported_instance": instanceName, // §K.3 — NOT "instance"
		"cluster":           clusterName,
	})

	// NODE_COUNT — GAUGE (anchor metric)
	c.st.Set("stackdriver_bigtable_cluster_bigtable_googleapis_com_cluster_node_count",
		clLbls, math.Round(3*bf))
	c.st.Set("stackdriver_bigtable_cluster_bigtable_googleapis_com_cluster_cpu_load",
		clLbls, math.Min(1.0, 0.4*bf))
	c.st.Set("stackdriver_bigtable_cluster_bigtable_googleapis_com_cluster_cpu_load_hottest_node",
		clLbls, math.Min(1.0, 0.6*bf))
	c.st.Set("stackdriver_bigtable_cluster_bigtable_googleapis_com_cluster_storage_utilization",
		clLbls, math.Min(1.0, 0.3*bf))
	c.st.Set("stackdriver_bigtable_cluster_bigtable_googleapis_com_disk_bytes_used",
		gcpLabels(proj.projectID, "By", map[string]string{
			"exported_instance": instanceName, "cluster": clusterName,
		}), math.Round(50*1024*1024*1024*bf))
	c.st.Set("stackdriver_bigtable_cluster_bigtable_googleapis_com_disk_storage_capacity",
		gcpLabels(proj.projectID, "By", map[string]string{
			"exported_instance": instanceName, "cluster": clusterName,
		}), 200*1024*1024*1024)

	// CPU load by app_profile/method/table
	c.st.Set("stackdriver_bigtable_cluster_bigtable_googleapis_com_cluster_cpu_load_by_app_profile_by_method_by_table",
		gcpLabels(proj.projectID, "1", map[string]string{
			"exported_instance": instanceName, "cluster": clusterName,
			"app_profile": "default", "method": "Bigtable.ReadRows", "table": tableName,
		}), math.Min(1.0, 0.3*bf))

	// bigtable_table metrics
	tblLbls := gcpLabels(proj.projectID, "1", map[string]string{
		"exported_instance": instanceName,
		"cluster":           clusterName,
		"table":             tableName,
	})
	byTblLbls := gcpLabels(proj.projectID, "By", map[string]string{
		"exported_instance": instanceName,
		"cluster":           clusterName,
		"table":             tableName,
	})

	c.st.Set("stackdriver_bigtable_table_bigtable_googleapis_com_table_bytes_used",
		byTblLbls, math.Round(10*1024*1024*1024*bf))
	c.st.Set("stackdriver_bigtable_table_bigtable_googleapis_com_server_data_boost_spu_usage",
		tblLbls, math.Round(100*bf))
	c.st.Add("stackdriver_bigtable_table_bigtable_googleapis_com_server_returned_rows_count",
		tblLbls, math.Round(10000*bf))
	c.st.Add("stackdriver_bigtable_table_bigtable_googleapis_com_server_modified_rows_count",
		tblLbls, math.Round(500*bf))
	c.st.Add("stackdriver_bigtable_table_bigtable_googleapis_com_server_sent_bytes_count",
		byTblLbls, math.Round(1_000_000*bf))
	c.st.Add("stackdriver_bigtable_table_bigtable_googleapis_com_server_received_bytes_count",
		byTblLbls, math.Round(500_000*bf))
	c.st.Add("stackdriver_bigtable_table_bigtable_googleapis_com_server_error_count",
		tblLbls, math.Round(0.5*bf))
	c.st.Add("stackdriver_bigtable_table_bigtable_googleapis_com_server_multi_cluster_failovers_count",
		tblLbls, math.Round(0.1*bf))

	// SERVER_REQUEST_COUNT — spread method label over ALL enum values (§J.9 / load-bearing)
	// Live label set (SK-18 capture 2026-06-14): exported_instance, cluster, table,
	// app_profile, zone, method, project_id, unit.
	for _, method := range bigtableMethods {
		c.st.Add("stackdriver_bigtable_table_bigtable_googleapis_com_server_request_count",
			gcpLabels(proj.projectID, "1", map[string]string{
				"exported_instance": instanceName,
				"cluster":           clusterName,
				"table":             tableName,
				"app_profile":       "default",
				"zone":              proj.region + "-a",
				"method":            method,
			}), math.Round(100*bf))
	}

	// DISTRIBUTION histograms — pass BASE name (§K.2)
	// gcpLBLatencyBuckets: ms latency, exponential bounds captured from live Stackdriver.
	msLbls := gcpLabels(proj.projectID, "ms", map[string]string{
		"exported_instance": instanceName, "cluster": clusterName, "table": tableName,
	})
	c.st.Observe("stackdriver_bigtable_table_bigtable_googleapis_com_server_latencies",
		msLbls, gcpLBLatencyBuckets, state.LEBare, 5.0*bf*w.Shape.Noise(0.3))
	c.st.Observe("stackdriver_bigtable_table_bigtable_googleapis_com_client_operation_latencies",
		msLbls, gcpLBLatencyBuckets, state.LEBare, 6.0*bf*w.Shape.Noise(0.3))
	c.st.Observe("stackdriver_bigtable_table_bigtable_googleapis_com_client_attempt_latencies",
		msLbls, gcpLBLatencyBuckets, state.LEBare, 4.5*bf*w.Shape.Noise(0.3))
}

// ── Vertex AI ────────────────────────────────────────────────────────────────
//
// Metrics sourced from GCP Cloud Monitoring Vertex AI documentation
// (cloud.google.com/monitoring/api/metrics_gcp; ctx7 /websites/cloud_google_monitoring,
// cross-checked with /googlecloudplatform/monitoring-dashboard-samples 2026-06-15).
//
// Two resource types:
//   - aiplatform.googleapis.com/Endpoint → Stackdriver resource "aiplatform_googleapis_com_endpoint"
//     carries prediction/online/* (per deployed model endpoint).
//   - aiplatform.googleapis.com/Location → Stackdriver resource "aiplatform_googleapis_com_location"
//     carries prediction/model_invocation/* (managed Model Garden / Foundation Model APIs).
//
// Env-awareness: when c.env != nil, stamp env=<name> on all vertex series and scale
// via Shape.Factor(now, weight, nonProd). When c.env == nil, omit env (I13) and use
// the aggregate bf passed from renderMetrics (same as all other cspgcp sub-signals).
//
// Vertex AI Endpoint: metric labels are model_id + version_id (per deployed model).
// Vertex AI Location: metric labels are model_id + model_version_id (model invocations).
//
// All metric names flagged v: assumed — no live Alloy prometheus.exporter.gcp capture yet.
// Confirmed GCP metric paths; Prometheus mangling convention extrapolated from live SK-18
// captures of other aiplatform families. Resolve with a live Alloy capture.

// vertexEndpointID returns a deterministic synthetic Vertex AI endpoint ID.
func vertexEndpointID(projectID string) string {
	h := uint64(14695981039346656037)
	for _, b := range []byte(projectID) {
		h ^= uint64(b)
		h *= 1099511628211
	}
	return fmt.Sprintf("%d", h%9000000000000000000+1000000000000000000)
}

// vertexModels is the set of synthetic model IDs for Vertex AI endpoint + invocation metrics.
// IDs are current-generation Vertex AI model IDs (gemini-2.5 family + Claude-on-Vertex +
// text embedding); gemini-1-5-* retired. version is "default" — a non-empty placeholder
// (never "" which would violate the omit-empty hard rule; swap to a real version string
// once a live Alloy capture confirms the exact GCP version format).
var vertexModels = []struct{ id, version string }{
	{"gemini-2.5-flash", "default"},
	{"gemini-2.5-flash-lite", "default"},
	{"gemini-2.5-pro", "default"},
	{"claude-sonnet-4-5@20250929", "default"},
	{"claude-haiku-4-5@20251001", "default"},
	{"text-embedding-005", "default"},
}

func (c *Construct) emitVertex(now time.Time, w *core.World, proj gcpProject, bf float64) {
	// Determine the effective scaling factor and whether to stamp env.
	// B1: branch on whether Env is set — do NOT blanket-swap BusinessFactor.
	var vf float64
	var envExtra map[string]string
	if c.env != nil {
		vf = w.Shape.Factor(now, c.env.Weight, c.env.NonProd) * w.Shape.Noise(0.1)
		envExtra = map[string]string{"env": c.env.Name}
	} else {
		// Aggregate path: use the bf already computed by renderMetrics (BusinessFactor*Noise).
		vf = bf
		envExtra = map[string]string{} // no env label (I13)
	}

	endpointID := vertexEndpointID(proj.projectID)
	location := proj.region

	// ── Endpoint-scoped metrics (prediction/online/*) ────────────────────────
	// Resource type: aiplatform_googleapis_com_endpoint
	// Labels: project_id, location, endpoint_id, model_id, version_id
	for _, m := range vertexModels {
		endpointBase := map[string]string{
			"location":    location,
			"endpoint_id": endpointID,
			"model_id":    m.id,
			"version_id":  m.version,
		}

		// Per-model volume differentiation: intrinsic weight from genai catalogue +
		// per-model Wander. vf already includes Noise(0.1) — do NOT add a second Noise call.
		mw := genai.VolumeWeight(m.id)

		// CUMULATIVE (DELTA in GCP, but exported as cumulative counter via stackdriver_exporter)
		// prediction/online/prediction_count — request count per model (v: assumed)
		c.st.Add(
			"stackdriver_aiplatform_googleapis_com_endpoint_aiplatform_googleapis_com_prediction_online_prediction_count",
			gcpLabels(proj.projectID, "1", mapMerge(mapMerge(endpointBase, map[string]string{
				"response_code":       "OK",
				"response_code_class": "2xx",
			}), envExtra)),
			math.Round(200*vf*mw*w.Shape.Wander(m.id, now, 0.15)))

		// prediction/online/error_count — errors per model (v: assumed)
		c.st.Add(
			"stackdriver_aiplatform_googleapis_com_endpoint_aiplatform_googleapis_com_prediction_online_error_count",
			gcpLabels(proj.projectID, "1", mapMerge(mapMerge(endpointBase, map[string]string{
				"response_code":       "500",
				"response_code_class": "5xx",
				"error_type":          "INTERNAL",
			}), envExtra)),
			math.Round(1*vf*mw*w.Shape.Wander(m.id, now, 0.15)))

		// prediction/online/response/latencies — DISTRIBUTION (ms) (v: assumed)
		c.st.Observe(
			"stackdriver_aiplatform_googleapis_com_endpoint_aiplatform_googleapis_com_prediction_online_response_latencies",
			gcpLabels(proj.projectID, "ms", mapMerge(endpointBase, envExtra)),
			gcpLBLatencyBuckets, state.LEBare, 80.0*vf*w.Shape.Noise(0.3))
	}

	// ── Location-scoped metrics (prediction/model_invocation/*) ─────────────
	// Resource type: aiplatform_googleapis_com_location
	// Labels: project_id, location, model_id, model_version_id
	for _, m := range vertexModels {
		invocBase := map[string]string{
			"location":         location,
			"model_id":         m.id,
			"model_version_id": m.version,
		}

		// Per-model volume differentiation (same weight+wander as endpoint loop above).
		// vf already includes Noise(0.1) — do NOT add a second Noise for the scalar counters.
		mw := genai.VolumeWeight(m.id)
		mwv := mw * w.Shape.Wander(m.id+"_loc", now, 0.15)

		// CUMULATIVE: model invocation count (v: assumed)
		c.st.Add(
			"stackdriver_aiplatform_googleapis_com_location_aiplatform_googleapis_com_prediction_model_invocation_invocations",
			gcpLabels(proj.projectID, "1", mapMerge(invocBase, envExtra)),
			math.Round(150*vf*mwv))

		// CUMULATIVE: input token count (v: assumed)
		c.st.Add(
			"stackdriver_aiplatform_googleapis_com_location_aiplatform_googleapis_com_prediction_model_invocation_input_token_count",
			gcpLabels(proj.projectID, "1", mapMerge(invocBase, envExtra)),
			math.Round(50000*vf*mwv))

		// CUMULATIVE: output token count (v: assumed)
		c.st.Add(
			"stackdriver_aiplatform_googleapis_com_location_aiplatform_googleapis_com_prediction_model_invocation_output_token_count",
			gcpLabels(proj.projectID, "1", mapMerge(invocBase, envExtra)),
			math.Round(15000*vf*mwv))

		// CUMULATIVE: failure count (v: assumed)
		c.st.Add(
			"stackdriver_aiplatform_googleapis_com_location_aiplatform_googleapis_com_prediction_model_invocation_failures",
			gcpLabels(proj.projectID, "1", mapMerge(mapMerge(invocBase, map[string]string{
				"error_type": "INTERNAL",
			}), envExtra)),
			math.Round(1*vf*mwv))

		// DISTRIBUTION: model invocation latencies (ms) (v: assumed)
		// Latency Observe already gets its own Noise(0.4) for the observation amplitude —
		// that second Noise is intentional (it's on the latency value, not the counter).
		c.st.Observe(
			"stackdriver_aiplatform_googleapis_com_location_aiplatform_googleapis_com_prediction_model_invocation_latencies",
			gcpLabels(proj.projectID, "ms", mapMerge(invocBase, envExtra)),
			gcpLBLatencyBuckets, state.LEBare, 500.0*vf*mw*w.Shape.Noise(0.4))
	}
}

// ── Logs sub-signal ───────────────────────────────────────────────────────────

// renderLogs returns Cloud Logging log streams for all projects.
// Stream label: {job="integrations/gcp"} — one stream per project per extract §2, logs.
func (c *Construct) renderLogs(now time.Time) []loki.Stream {
	var out []loki.Stream
	for _, proj := range c.projects {
		out = append(out, c.logsForProject(now, proj)...)
	}
	return out
}

func (c *Construct) logsForProject(now time.Time, proj gcpProject) []loki.Stream {
	// Stream labels: low-cardinality only. project_id is high-cardinality and goes
	// in line body / structured metadata per I14.
	streamLabels := map[string]string{"job": gcpJob}

	type gcpLogEntry struct {
		LogName     string                 `json:"logName"`
		Resource    map[string]interface{} `json:"resource"`
		Timestamp   string                 `json:"timestamp"`
		Severity    string                 `json:"severity"`
		TextPayload string                 `json:"textPayload,omitempty"`
	}

	entries := []gcpLogEntry{
		{
			LogName: fmt.Sprintf("projects/%s/logs/cloudaudit.googleapis.com%%2Factivity", proj.projectID),
			Resource: map[string]interface{}{
				"type":   "gce_instance",
				"labels": map[string]string{"project_id": proj.projectID, "zone": proj.region + "-a"},
			},
			Timestamp:   now.UTC().Format(time.RFC3339),
			Severity:    "INFO",
			TextPayload: "Instance started",
		},
		{
			LogName: fmt.Sprintf("projects/%s/logs/cloudsql.googleapis.com%%2Fpostgres.log", proj.projectID),
			Resource: map[string]interface{}{
				"type":   "cloudsql_database",
				"labels": map[string]string{"project_id": proj.projectID, "region": proj.region},
			},
			Timestamp:   now.Add(-30 * time.Second).UTC().Format(time.RFC3339),
			Severity:    "WARNING",
			TextPayload: fmt.Sprintf("slow query detected in project %s", proj.projectID),
		},
		{
			LogName: fmt.Sprintf("projects/%s/logs/run.googleapis.com%%2Frequests", proj.projectID),
			Resource: map[string]interface{}{
				"type":   "cloud_run_revision",
				"labels": map[string]string{"project_id": proj.projectID, "location": proj.region},
			},
			Timestamp:   now.Add(-10 * time.Second).UTC().Format(time.RFC3339),
			Severity:    "INFO",
			TextPayload: "request served",
		},
	}

	var lines []loki.Line
	for _, entry := range entries {
		body, _ := json.Marshal(entry)
		lines = append(lines, loki.Line{
			T:    now,
			Body: string(body),
			// project_id goes in structured metadata (high-cardinality, not a stream label — I14)
			Meta: map[string]string{"project_id": proj.projectID},
		})
	}

	return []loki.Stream{{Labels: streamLabels, Lines: lines}}
}
