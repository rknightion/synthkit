// SPDX-License-Identifier: AGPL-3.0-only

// Package snowflake implements the "snowflake" construct.
//
// Kind:     "snowflake"
// Scope:    core.ScopeSubstrate — identity is account/warehouse/database config-borne; no blueprint label (I21)
// Group:    core.GroupIntegration
// Signals:  []core.SignalClass{core.Metrics}
// Interval: 60s
//
// Emits 27 GAUGEs sourced from ACCOUNT_USAGE views via
// prometheus.exporter.snowflake (grafana/snowflake-prometheus-exporter, :9975).
// All metrics are rolling 24-h averages/sums — NEVER rate(). See signals/snowflake.md [slug: snowflake].
//
// Label groups (per signals contract):
//
//	account-level storage  : snowflake_storage_bytes, snowflake_stage_bytes, snowflake_failsafe_bytes
//	database storage       : snowflake_database_bytes, snowflake_database_failsafe_bytes → (name,id) per Database
//	credit usage           : snowflake_used_compute_credits, snowflake_used_cloud_services_credits → (service_type,service)
//	warehouse credits      : snowflake_warehouse_used_compute_credits, snowflake_warehouse_used_cloud_service_credits → (name,id) per Warehouse
//	login rates            : snowflake_login_rate, snowflake_successful_login_rate, snowflake_failed_login_rate → (client_type,client_version)
//	warehouse query load   : snowflake_warehouse_executed_queries, snowflake_warehouse_overloaded_queue_size,
//	                         snowflake_warehouse_provisioning_queue_size, snowflake_warehouse_blocked_queries → (name,id) per Warehouse
//	auto-clustering        : snowflake_auto_clustering_credits, snowflake_auto_clustering_bytes, snowflake_auto_clustering_rows → 6-label group
//	table storage          : snowflake_table_active_bytes, snowflake_table_time_travel_bytes, snowflake_table_failsafe_bytes,
//	                         snowflake_table_clone_bytes → 6-label group; snowflake_table_deleted_tables → no labels
//	db replication         : snowflake_db_replication_used_credits, snowflake_db_replication_transferred_bytes → (database_name,database_id)
//	exporter health        : snowflake_up → no labels
//
// Base labels on every series: job="integrations/snowflake", instance="<account>:9975".
//
// Signal contract: signals/snowflake.md [slug: snowflake]
package snowflake

import (
	"context"
	"fmt"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/state"
)

const (
	// Kind is the registry key.
	Kind = "snowflake"

	sfJob = "integrations/snowflake"
)

// Config is the construct's YAML config struct.
type Config struct {
	// Account is the Snowflake account identifier (default "demo-acct").
	Account string `yaml:"account"`
	// Warehouses is the list of virtual warehouse names to emit per-warehouse metrics for.
	// Default: ["wh_compute","wh_etl"].
	Warehouses []string `yaml:"warehouses"`
	// Databases is the list of database names to emit per-database metrics for.
	// Default: ["analytics","raw"].
	Databases []string `yaml:"databases"`
}

// defaultWarehouses are the generic default warehouse names.
var defaultWarehouses = []string{"wh_compute", "wh_etl"}

// defaultDatabases are the generic default database names.
var defaultDatabases = []string{"analytics", "raw"}

// syntheticTableEntries is the fixed set of table-level entries for the 6-label group.
// Each entry represents one (table_name,table_id,schema_name,schema_id,database_name,database_id) combination.
type tableEntry struct {
	tableName    string
	tableID      string
	schemaName   string
	schemaID     string
	databaseName string
	databaseID   string
}

// syntheticClientTypes is the fixed set of (client_type,client_version) pairs for login rate metrics.
type clientEntry struct {
	clientType    string
	clientVersion string
}

// syntheticServiceEntries is the fixed set of (service_type,service) pairs for credit metrics.
type serviceEntry struct {
	serviceType string
	service     string
}

// Construct is the per-instance snowflake renderer. Not exported; callers use Build.
type Construct struct {
	account    string
	instance   string // "<account>:9975"
	warehouses []warehouseEntry
	databases  []databaseEntry
	tables     []tableEntry
	clients    []clientEntry
	services   []serviceEntry
	st         *state.State
}

type warehouseEntry struct {
	name string
	id   string
}

type databaseEntry struct {
	name string
	id   string
}

// Compile-time interface check.
var _ core.Construct = (*Construct)(nil)

// Build validates cfg and fx, applies defaults, and returns a ready core.Construct.
func Build(cfg any, fx *fixture.Set) (core.Construct, error) {
	c, ok := cfg.(*Config)
	if !ok || c == nil {
		return nil, fmt.Errorf("snowflake: Build called with %T, want *Config", cfg)
	}

	account := c.Account
	if account == "" {
		account = "demo-acct"
	}

	warehouses := c.Warehouses
	if len(warehouses) == 0 {
		warehouses = defaultWarehouses
	}

	databases := c.Databases
	if len(databases) == 0 {
		databases = defaultDatabases
	}

	// Build warehouse entries with deterministic numeric IDs.
	whs := make([]warehouseEntry, len(warehouses))
	for i, name := range warehouses {
		whs[i] = warehouseEntry{
			name: name,
			id:   fmt.Sprintf("%d", deterministicID(name)),
		}
	}

	// Build database entries with deterministic numeric IDs.
	dbs := make([]databaseEntry, len(databases))
	for i, name := range databases {
		dbs[i] = databaseEntry{
			name: name,
			id:   fmt.Sprintf("%d", deterministicID(name)),
		}
	}

	// Fixed table entries for the 6-label group (auto-clustering + table storage).
	tables := []tableEntry{
		{
			tableName: "events", tableID: "101",
			schemaName: "public", schemaID: "201",
			databaseName: databases[0], databaseID: fmt.Sprintf("%d", deterministicID(databases[0])),
		},
		{
			tableName: "sessions", tableID: "102",
			schemaName: "public", schemaID: "201",
			databaseName: databases[0], databaseID: fmt.Sprintf("%d", deterministicID(databases[0])),
		},
	}

	// Fixed client type/version pairs for login rate metrics.
	clients := []clientEntry{
		{clientType: "PYTHON_DRIVER", clientVersion: "3.0.4"},
		{clientType: "JDBC", clientVersion: "3.13.35"},
	}

	// Fixed service entries for credit usage metrics.
	services := []serviceEntry{
		{serviceType: "WAREHOUSE", service: "COMPUTE_WH"},
		{serviceType: "SERVERLESS", service: "SEARCH_OPTIMIZATION"},
	}

	return &Construct{
		account:    account,
		instance:   fmt.Sprintf("%s:9975", account),
		warehouses: whs,
		databases:  dbs,
		tables:     tables,
		clients:    clients,
		services:   services,
		st:         state.NewState(),
	}, nil
}

func (c *Construct) Kind() string                { return Kind }
func (c *Construct) Signals() []core.SignalClass { return []core.SignalClass{core.Metrics} }
func (c *Construct) Interval() time.Duration     { return 60 * time.Second }

// Tick renders one 60-second observation window into w.Metrics.
func (c *Construct) Tick(ctx context.Context, now time.Time, w *core.World) error {
	c.renderMetrics(now, w)
	if w.Metrics != nil {
		return w.Metrics.Write(ctx, c.st.Collect(now))
	}
	return nil
}

// sfLabels builds the base label map for every snowflake series.
// Every series carries job="integrations/snowflake" + instance="<account>:9975".
// Extra per-group labels are merged in; absent labels are OMITTED (I13 — never "" or "NA").
func (c *Construct) sfLabels(extra map[string]string) map[string]string {
	m := make(map[string]string, 2+len(extra))
	m["job"] = sfJob
	m["instance"] = c.instance
	for k, v := range extra {
		if v != "" {
			m[k] = v
		}
	}
	return m
}

// seriesVar returns a stable-but-living per-series multiplier ≈ 1: a deterministic
// baseline offset (Spread) so peer series that share a formula get distinct, stable
// values, times a slow per-series drift (Wander) so the value is not frozen.
// amp sets the magnitude; volume/bytes/credits use ≈0.18, counts/rates ≈0.30.
// Returns 1.0 when no shape engine is wired.
func seriesVar(w *core.World, now time.Time, key string, amp float64) float64 {
	if w == nil || w.Shape == nil {
		return 1.0
	}
	return w.Shape.Spread(key, amp) * w.Shape.Wander(key, now, amp*0.4)
}

// ampVol / ampRate set per-series Spread+Wander magnitude per metric class.
const (
	ampVol  = 0.18 // bytes / credits / counts: ±18% baseline spread
	ampRate = 0.30 // login rates / query load: ±30% baseline spread
)

// renderMetrics builds all 27 snowflake gauges for one tick.
func (c *Construct) renderMetrics(now time.Time, w *core.World) {
	// Use BusinessFactor lightly — snowflake metrics are slow accounting, keep stable-ish.
	bf := 0.9 + w.Shape.BusinessFactor(now)*0.2 // range ≈ [0.7, 1.1]

	// ── Account-level storage (no labels — singletons, no peers) ─────────────
	// STORAGE_USAGE: total storage, stage bytes, failsafe bytes.
	// Account-level: single series each — seriesVar not needed (no peer to differ from).
	base := c.sfLabels(nil)
	c.st.Set("snowflake_storage_bytes", base, 500*1024*1024*1024*bf)  // ~500 GiB
	c.st.Set("snowflake_stage_bytes", base, 50*1024*1024*1024*bf)     // ~50 GiB
	c.st.Set("snowflake_failsafe_bytes", base, 100*1024*1024*1024*bf) // ~100 GiB

	// ── Database storage (name,id per Database) ───────────────────────────────
	// DATABASE_STORAGE_USAGE_HISTORY
	for _, db := range c.databases {
		lbls := c.sfLabels(map[string]string{"name": db.name, "id": db.id})
		sv := seriesVar(w, now, "db_bytes|"+db.name, ampVol)
		c.st.Set("snowflake_database_bytes", lbls, 200*1024*1024*1024*bf*sv)
		c.st.Set("snowflake_database_failsafe_bytes", lbls, 40*1024*1024*1024*bf*sv)
	}

	// ── Credit usage (service_type,service) ───────────────────────────────────
	// METERING_HISTORY
	for _, svc := range c.services {
		lbls := c.sfLabels(map[string]string{
			"service_type": svc.serviceType,
			"service":      svc.service,
		})
		sv := seriesVar(w, now, "svc_credits|"+svc.serviceType+"|"+svc.service, ampVol)
		c.st.Set("snowflake_used_compute_credits", lbls, 10.5*bf*sv)
		c.st.Set("snowflake_used_cloud_services_credits", lbls, 1.2*bf*sv)
	}

	// ── Warehouse credits (name,id per Warehouse) ──────────────────────────────
	// WAREHOUSE_METERING_HISTORY
	for _, wh := range c.warehouses {
		lbls := c.sfLabels(map[string]string{"name": wh.name, "id": wh.id})
		sv := seriesVar(w, now, "wh_credits|"+wh.name, ampVol)
		c.st.Set("snowflake_warehouse_used_compute_credits", lbls, 8.0*bf*sv)
		c.st.Set("snowflake_warehouse_used_cloud_service_credits", lbls, 0.5*bf*sv)
	}

	// ── Login rates (client_type,client_version) ──────────────────────────────
	// LOGIN_HISTORY (per_hour rates)
	for _, cl := range c.clients {
		lbls := c.sfLabels(map[string]string{
			"client_type":    cl.clientType,
			"client_version": cl.clientVersion,
		})
		sv := seriesVar(w, now, "login|"+cl.clientType+"|"+cl.clientVersion, ampRate)
		c.st.Set("snowflake_login_rate", lbls, 120*bf*sv)
		c.st.Set("snowflake_successful_login_rate", lbls, 118*bf*sv)
		c.st.Set("snowflake_failed_login_rate", lbls, 2*bf*sv)
	}

	// ── Warehouse query load (name,id per Warehouse) ──────────────────────────
	// WAREHOUSE_LOAD_HISTORY
	for _, wh := range c.warehouses {
		lbls := c.sfLabels(map[string]string{"name": wh.name, "id": wh.id})
		sv := seriesVar(w, now, "wh_load|"+wh.name, ampRate)
		c.st.Set("snowflake_warehouse_executed_queries", lbls, 45*bf*sv)
		c.st.Set("snowflake_warehouse_overloaded_queue_size", lbls, 2*bf*sv)
		c.st.Set("snowflake_warehouse_provisioning_queue_size", lbls, 1*bf*sv)
		c.st.Set("snowflake_warehouse_blocked_queries", lbls, 0.5*bf*sv)
	}

	// ── Auto-clustering (6-label group) ──────────────────────────────────────
	// AUTOMATIC_CLUSTERING_HISTORY
	for _, tbl := range c.tables {
		lbls := c.sfLabels(map[string]string{
			"table_name":    tbl.tableName,
			"table_id":      tbl.tableID,
			"schema_name":   tbl.schemaName,
			"schema_id":     tbl.schemaID,
			"database_name": tbl.databaseName,
			"database_id":   tbl.databaseID,
		})
		sv := seriesVar(w, now, "auto_cluster|"+tbl.tableName, ampVol)
		c.st.Set("snowflake_auto_clustering_credits", lbls, 0.3*bf*sv)
		c.st.Set("snowflake_auto_clustering_bytes", lbls, 1*1024*1024*1024*bf*sv)
		c.st.Set("snowflake_auto_clustering_rows", lbls, 5_000_000*bf*sv)
	}

	// ── Table storage (6-label group first four; deleted_tables no labels) ────
	// TABLE_STORAGE_METRICS
	for _, tbl := range c.tables {
		lbls := c.sfLabels(map[string]string{
			"table_name":    tbl.tableName,
			"table_id":      tbl.tableID,
			"schema_name":   tbl.schemaName,
			"schema_id":     tbl.schemaID,
			"database_name": tbl.databaseName,
			"database_id":   tbl.databaseID,
		})
		sv := seriesVar(w, now, "tbl_storage|"+tbl.tableName, ampVol)
		c.st.Set("snowflake_table_active_bytes", lbls, 20*1024*1024*1024*bf*sv)
		c.st.Set("snowflake_table_time_travel_bytes", lbls, 5*1024*1024*1024*bf*sv)
		c.st.Set("snowflake_table_failsafe_bytes", lbls, 10*1024*1024*1024*bf*sv)
		c.st.Set("snowflake_table_clone_bytes", lbls, 2*1024*1024*1024*bf*sv)
	}
	// deleted_tables: singleton, no group labels (I13 — omit absent labels) — stays constant.
	c.st.Set("snowflake_table_deleted_tables", c.sfLabels(nil), 0)

	// ── Database replication (database_name,database_id) ──────────────────────
	// REPLICATION_USAGE_HISTORY
	for _, db := range c.databases {
		lbls := c.sfLabels(map[string]string{
			"database_name": db.name,
			"database_id":   db.id,
		})
		sv := seriesVar(w, now, "db_repl|"+db.name, ampVol)
		c.st.Set("snowflake_db_replication_used_credits", lbls, 0.1*bf*sv)
		c.st.Set("snowflake_db_replication_transferred_bytes", lbls, 500*1024*1024*bf*sv)
	}

	// ── Exporter health (singleton, no labels) ─────────────────────────────────
	// snowflake_up is always 1 — exporter health flag, not a measurement.
	c.st.Set("snowflake_up", c.sfLabels(nil), 1)
}

// deterministicID returns a stable short numeric ID for a name using FNV-1a.
// Used to generate consistent but plausible id label values.
func deterministicID(name string) uint32 {
	h := uint32(2166136261) // FNV-1a 32-bit offset basis
	for _, b := range []byte(name) {
		h ^= uint32(b)
		h *= 16777619
	}
	// Clamp to 5-digit range: [10000, 99999].
	return h%90000 + 10000
}
