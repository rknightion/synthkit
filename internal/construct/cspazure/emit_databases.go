// SPDX-License-Identifier: AGPL-3.0-only

// emit_databases.go — Azure SQL, Elastic Pools, PG Flexible Servers (extract §1.5 databases)
//
// Traps reproduced exactly (extract §1.6):
//   - azure_microsoft_sql_servers_databases_storage_maximum_bytes — no _maximum before _bytes
//   - azure_microsoft_dbforpostgresql_flexibleservers_connections_connections_failed_total_count
//     — literal double "connections_"
//
// ALL metrics use st.Set — window-gauge invariant (extract §1.3).
package cspazure

import (
	"time"

	"github.com/rknightion/synthkit/internal/core"
)

// emitDatabases emits the databases sub-signal for one subscription.
func (c *construct) emitDatabases(_ time.Time, w *core.World, sub azureSub, bf float64) {
	rg := sub.resourceGroups[0] // rg-databases
	n := w.Shape.Noise(0.10)

	// ── Azure SQL Databases (1 server × 2 databases) ──────────────────────────
	// Use last 2 chars of subscription name to keep resourceName deterministic per sub.
	subSuffix := sub.subscriptionName[len(sub.subscriptionName)-2:]
	sqlServer := "sql-" + subSuffix + "-01"
	for _, dbName := range []string{"appdb", "analytics"} {
		// resourceID nests /databases/<name> under /servers/<server>; last segment == dbName.
		lbls := c.baseLabelsFor(sub, rg, "microsoft.sql/servers/"+sqlServer+"/databases", dbName)

		c.st.Set("azure_microsoft_sql_servers_databases_connection_successful_total_count",
			lbls, rnd(100*bf*n))
		c.st.Set("azure_microsoft_sql_servers_databases_deadlock_total_count",
			lbls, rnd(0.5*bf))
		c.st.Set("azure_microsoft_sql_servers_databases_sessions_count_average_count",
			lbls, rnd(50*bf))
		c.st.Set("azure_microsoft_sql_servers_databases_cpu_percent_average_percent",
			lbls, clamp(25*bf, 100))
		c.st.Set("azure_microsoft_sql_servers_databases_cpu_limit_average_count", lbls, 8)
		c.st.Set("azure_microsoft_sql_servers_databases_cpu_used_average_count",
			lbls, rnd(2*bf))
		// ⚠ TRAP §1.6 / extract §1.5: no _maximum before _bytes — exact literal name.
		c.st.Set("azure_microsoft_sql_servers_databases_storage_maximum_bytes",
			lbls, 32*1024*1024*1024)
		c.st.Set("azure_microsoft_sql_servers_databases_storage_percent_maximum_percent",
			lbls, clamp(10*bf, 100))
		c.st.Set("azure_microsoft_sql_servers_databases_dtu_used_average_count",
			lbls, rnd(30*bf))
		c.st.Set("azure_microsoft_sql_servers_databases_dtu_consumption_percent_average_percent",
			lbls, clamp(30*bf, 100))
		c.st.Set("azure_microsoft_sql_servers_databases_dtu_limit_average_count", lbls, 100)
	}

	// ── Azure SQL Elastic Pool ─────────────────────────────────────────────────
	// resourceID always ends /elasticpools/<pool>. The resourceName LABEL differs by path
	// (signals/cspazure.md [slug: cspazure], SK-16; live-confirmed on the managed scraper 2026-06-14):
	// azure_exporter → the <server> name; serverless → the derived "<server>/<pool>" form.
	poolName := "pool-01"
	poolLbls := c.baseLabels(sub, rg,
		"microsoft.sql/servers/"+sqlServer+"/elasticpools", poolName, sqlServer)

	c.st.Set("azure_microsoft_sql_servers_elasticpools_allocated_data_storage_average_bytes",
		poolLbls, 50*1024*1024*1024)
	c.st.Set("azure_microsoft_sql_servers_elasticpools_storage_used_average_bytes",
		poolLbls, rnd(5*1024*1024*1024*bf))
	c.st.Set("azure_microsoft_sql_servers_elasticpools_storage_limit_average_bytes",
		poolLbls, 100*1024*1024*1024)
	c.st.Set("azure_microsoft_sql_servers_elasticpools_cpu_percent_average_percent",
		poolLbls, clamp(20*bf, 100))
	c.st.Set("azure_microsoft_sql_servers_elasticpools_sql_instance_memory_percent_maximum_percent",
		poolLbls, clamp(40*bf, 100))
	c.st.Set("azure_microsoft_sql_servers_elasticpools_edtu_used_average_count",
		poolLbls, rnd(50*bf))
	c.st.Set("azure_microsoft_sql_servers_elasticpools_sessions_count_average_count",
		poolLbls, rnd(80*bf))
	c.st.Set("azure_microsoft_sql_servers_elasticpools_allocated_data_storage_percent_average_percent",
		poolLbls, clamp(50*bf, 100))
	c.st.Set("azure_microsoft_sql_servers_elasticpools_storage_percent_average_percent",
		poolLbls, clamp(10*bf, 100))

	// ── PostgreSQL Flexible Servers (extract §4.3 / §4.1) ─────────────────────
	// pgNames come from fx.DBs (dbo11y↔CSP join) or are self-contained.
	for _, pgName := range sub.pgNames {
		c.emitPGFlexibleServer(w, sub, rg, pgName, bf)
	}
}

// emitPGFlexibleServer emits the full §1.5 PG Flexible Server metric set.
// ALL metrics use st.Set — window-gauge invariant.
func (c *construct) emitPGFlexibleServer(w *core.World, sub azureSub, rg, pgName string, bf float64) {
	lbls := c.baseLabelsFor(sub, rg, "microsoft.dbforpostgresql/flexibleservers", pgName)
	n := w.Shape.Noise(0.12)

	// Seed/anchor metric.
	c.st.Set("azure_microsoft_dbforpostgresql_flexibleservers_active_connections_average_count",
		lbls, rnd(30*bf*n))
	c.st.Set("azure_microsoft_dbforpostgresql_flexibleservers_connections_succeeded_total_count",
		lbls, rnd(100*bf))
	// ⚠ TRAP §1.6: literal double "connections_" — must be reproduced byte-exact.
	c.st.Set("azure_microsoft_dbforpostgresql_flexibleservers_connections_connections_failed_total_count",
		lbls, rnd(2*bf))
	c.st.Set("azure_microsoft_dbforpostgresql_flexibleservers_cpu_percent_average_percent",
		lbls, clamp(20*bf, 100))
	c.st.Set("azure_microsoft_dbforpostgresql_flexibleservers_storage_used_maximum_bytes",
		lbls, rnd(10*1024*1024*1024*bf))
	c.st.Set("azure_microsoft_dbforpostgresql_flexibleservers_storage_percent_maximum_percent",
		lbls, clamp(30*bf, 100))
	c.st.Set("azure_microsoft_dbforpostgresql_flexibleservers_read_iops_maximum_count",
		lbls, rnd(500*bf))
	c.st.Set("azure_microsoft_dbforpostgresql_flexibleservers_write_iops_maximum_count",
		lbls, rnd(300*bf))
	c.st.Set("azure_microsoft_dbforpostgresql_flexibleservers_database_size_bytes_average_bytes",
		lbls, rnd(5*1024*1024*1024*bf))
	c.st.Set("azure_microsoft_dbforpostgresql_flexibleservers_storage_percent_average_percent",
		lbls, clamp(25*bf, 100))
	c.st.Set("azure_microsoft_dbforpostgresql_flexibleservers_memory_percent_average_percent",
		lbls, clamp(40*bf, 100))
	c.st.Set("azure_microsoft_dbforpostgresql_flexibleservers_read_iops_average_count",
		lbls, rnd(300*bf))
	c.st.Set("azure_microsoft_dbforpostgresql_flexibleservers_write_iops_average_count",
		lbls, rnd(150*bf))
	c.st.Set("azure_microsoft_dbforpostgresql_flexibleservers_network_bytes_ingress_total_bytes",
		lbls, rnd(10_000_000*bf))
	c.st.Set("azure_microsoft_dbforpostgresql_flexibleservers_network_bytes_egress_total_bytes",
		lbls, rnd(5_000_000*bf))
	c.st.Set("azure_microsoft_dbforpostgresql_flexibleservers_read_throughput_average_count",
		lbls, rnd(1000*bf))
	c.st.Set("azure_microsoft_dbforpostgresql_flexibleservers_write_throughput_average_count",
		lbls, rnd(500*bf))
}
