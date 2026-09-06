// SPDX-License-Identifier: AGPL-3.0-only

package cspgcp

// native_otlp.go contains the explicit Cloud Monitoring metric catalogue and the
// hand-encoded projection used by googlecloudmonitoringreceiver. Native names are
// written as Cloud Monitoring types; they are never assembled from Prometheus names.

import (
	"context"
	"sort"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/sink/otlp"
	"github.com/rknightion/synthkit/internal/sink/promrw"
	"github.com/rknightion/synthkit/internal/state"
)

type nativeMetricMode uint8

const (
	nativeGaugeMode nativeMetricMode = iota
	nativeDeltaMode
	nativeDeltaHistogramMode
)

// nativeMetricSpec binds one source-confirmed Cloud Monitoring descriptor to the
// existing scrape family it models. Both names are explicit so a future naming
// change in either protocol cannot silently rewrite the other protocol.
type nativeMetricSpec struct {
	nativeName       string
	scrapeName       string
	unit             string
	description      string
	resourceType     string
	resourceKeys     []string
	metricKeys       []string
	mode             nativeMetricMode
	scale            float64
	gaugeFromCounter bool
}

func nativeGauge(nativeName, scrapeName, unit, description, resourceType string, resourceKeys []string) nativeMetricSpec {
	return nativeMetricSpec{
		nativeName: nativeName, scrapeName: scrapeName, unit: unit, description: description,
		resourceType: resourceType, resourceKeys: resourceKeys, mode: nativeGaugeMode,
	}
}

func nativeDelta(nativeName, scrapeName, unit, description, resourceType string, resourceKeys []string) nativeMetricSpec {
	return nativeMetricSpec{
		nativeName: nativeName, scrapeName: scrapeName, unit: unit, description: description,
		resourceType: resourceType, resourceKeys: resourceKeys, mode: nativeDeltaMode,
	}
}

func nativeDeltaHistogram(nativeName, scrapeName, unit, description, resourceType string, resourceKeys []string) nativeMetricSpec {
	return nativeMetricSpec{
		nativeName: nativeName, scrapeName: scrapeName, unit: unit, description: description,
		resourceType: resourceType, resourceKeys: resourceKeys, mode: nativeDeltaHistogramMode,
	}
}

// nativeMetricCatalog is sourced from the Cloud Monitoring metric list. The
// receiver drops GAUGE/CUMULATIVE distributions, so the two Cloud Run usage
// distributions are deliberately absent. Vertex AI is also absent because the
// repository's Vertex names remain v: assumed in signals/cspgcp.md.
var nativeMetricCatalog = applyNativeMetadata([]nativeMetricSpec{
	nativeGauge("compute.googleapis.com/instance/cpu/utilization", "stackdriver_gce_instance_compute_googleapis_com_instance_cpu_utilization", "10^2.%", "Fractional utilization of allocated CPU on this instance. Values are typically numbers between 0.0 and 1.0 (but some machine types allow bursting above 1.0). Charts display the values as a percentage between 0% and 100% (or more). This metric is reported by the hypervisor for the VM and can differ from `agent.googleapis.com/cpu/utilization`, which is reported from inside the VM. Sampled every 60 seconds. After sampling, data is not visible for up to 240 seconds.", "gce_instance", []string{"project_id", "instance_id", "zone"}),
	nativeDelta("compute.googleapis.com/instance/cpu/usage_time", "stackdriver_gce_instance_compute_googleapis_com_instance_cpu_usage_time", "s{CPU}", "Delta vCPU usage for all vCPUs, in vCPU-seconds. To compute the per-vCPU utilization fraction, divide this value by (end-start)*N, where end and start define this value's time interval and N is `compute.googleapis.com/instance/cpu/reserved_cores` at the end of the interval. This value is reported by the hypervisor for the VM and can differ from `agent.googleapis.com/cpu/usage_time`, which is reported from inside the VM. Sampled every 60 seconds. After sampling, data is not visible for up to 240 seconds.", "gce_instance", []string{"project_id", "instance_id", "zone"}),
	nativeDelta("compute.googleapis.com/instance/network/received_bytes_count", "stackdriver_gce_instance_compute_googleapis_com_instance_network_received_bytes_count", "By", "Count of bytes received from the network. Sampled every 60 seconds. After sampling, data is not visible for up to 240 seconds.", "gce_instance", []string{"project_id", "instance_id", "zone"}),
	nativeDelta("compute.googleapis.com/instance/network/sent_bytes_count", "stackdriver_gce_instance_compute_googleapis_com_instance_network_sent_bytes_count", "By", "Count of bytes sent over the network. Sampled every 60 seconds. After sampling, data is not visible for up to 240 seconds.", "gce_instance", []string{"project_id", "instance_id", "zone"}),
	nativeDelta("compute.googleapis.com/instance/disk/read_bytes_count", "stackdriver_gce_instance_compute_googleapis_com_instance_disk_read_bytes_count", "By", "Count of bytes read from disk. Sampled every 60 seconds. After sampling, data is not visible for up to 240 seconds.", "gce_instance", []string{"project_id", "instance_id", "zone"}),
	nativeDelta("compute.googleapis.com/instance/disk/write_bytes_count", "stackdriver_gce_instance_compute_googleapis_com_instance_disk_write_bytes_count", "By", "Count of bytes written to disk. Sampled every 60 seconds. After sampling, data is not visible for up to 240 seconds.", "gce_instance", []string{"project_id", "instance_id", "zone"}),
	nativeDelta("compute.googleapis.com/instance/disk/read_ops_count", "stackdriver_gce_instance_compute_googleapis_com_instance_disk_read_ops_count", "1", "Count of disk read IO operations. Sampled every 60 seconds. After sampling, data is not visible for up to 240 seconds.", "gce_instance", []string{"project_id", "instance_id", "zone"}),
	nativeDelta("compute.googleapis.com/instance/disk/write_ops_count", "stackdriver_gce_instance_compute_googleapis_com_instance_disk_write_ops_count", "1", "Count of disk write IO operations. Sampled every 60 seconds. After sampling, data is not visible for up to 240 seconds.", "gce_instance", []string{"project_id", "instance_id", "zone"}),
	nativeGauge("cloudsql.googleapis.com/database/up", "stackdriver_cloudsql_database_cloudsql_googleapis_com_database_up", "1", "Indicates if the server is up or not. Sampled every 60 seconds. After sampling, data is not visible for up to 165 seconds.", "cloudsql_database", []string{"project_id", "database_id", "region"}),
	nativeGauge("cloudsql.googleapis.com/database/cpu/utilization", "stackdriver_cloudsql_database_cloudsql_googleapis_com_database_cpu_utilization", "10^2.%", "Current CPU utilization represented as a percentage of the reserved CPU that is currently in use. Values are typically numbers between 0.0 and 1.0 (but might exceed 1.0). Charts display the values as a percentage between 0% and 100% (or more). Sampled every 60 seconds. After sampling, data is not visible for up to 165 seconds.", "cloudsql_database", []string{"project_id", "database_id", "region"}),
	nativeGauge("cloudsql.googleapis.com/database/memory/utilization", "stackdriver_cloudsql_database_cloudsql_googleapis_com_database_memory_utilization", "1", "The fraction of the memory quota that is currently in use. Sampled every 60 seconds. After sampling, data is not visible for up to 165 seconds.", "cloudsql_database", []string{"project_id", "database_id", "region"}),
	nativeGauge("cloudsql.googleapis.com/database/disk/utilization", "stackdriver_cloudsql_database_cloudsql_googleapis_com_database_disk_utilization", "1", "The fraction of the disk quota that is currently in use. Sampled every 60 seconds. After sampling, data is not visible for up to 165 seconds.", "cloudsql_database", []string{"project_id", "database_id", "region"}),
	nativeGauge("cloudsql.googleapis.com/database/available_for_failover", "stackdriver_cloudsql_database_cloudsql_googleapis_com_database_available_for_failover", "1", "This is > 0 if the failover operation is available on the instance. Sampled every 60 seconds. After sampling, data is not visible for up to 165 seconds.", "cloudsql_database", []string{"project_id", "database_id", "region"}),
	nativeGauge("cloudsql.googleapis.com/database/cpu/reserved_cores", "stackdriver_cloudsql_database_cloudsql_googleapis_com_database_cpu_reserved_cores", "1", "Number of cores reserved for the database. Sampled every 60 seconds. After sampling, data is not visible for up to 165 seconds.", "cloudsql_database", []string{"project_id", "database_id", "region"}),
	nativeGauge("cloudsql.googleapis.com/database/memory/quota", "stackdriver_cloudsql_database_cloudsql_googleapis_com_database_memory_quota", "By", "Maximum RAM size in bytes. Sampled every 60 seconds. After sampling, data is not visible for up to 165 seconds.", "cloudsql_database", []string{"project_id", "database_id", "region"}),
	nativeGauge("cloudsql.googleapis.com/database/disk/quota", "stackdriver_cloudsql_database_cloudsql_googleapis_com_database_disk_quota", "By", "Maximum data disk size in bytes. Sampled every 60 seconds. After sampling, data is not visible for up to 165 seconds.", "cloudsql_database", []string{"project_id", "database_id", "region"}),
	nativeDelta("cloudsql.googleapis.com/database/disk/read_ops_count", "stackdriver_cloudsql_database_cloudsql_googleapis_com_database_disk_read_ops_count", "1", "Delta count of data disk read IO operations. Sampled every 60 seconds. After sampling, data is not visible for up to 165 seconds.", "cloudsql_database", []string{"project_id", "database_id", "region"}),
	nativeDelta("cloudsql.googleapis.com/database/disk/write_ops_count", "stackdriver_cloudsql_database_cloudsql_googleapis_com_database_disk_write_ops_count", "1", "Delta count of data disk write IO operations. Sampled every 60 seconds. After sampling, data is not visible for up to 165 seconds.", "cloudsql_database", []string{"project_id", "database_id", "region"}),
	nativeGauge("cloudsql.googleapis.com/database/network/connections", "stackdriver_cloudsql_database_cloudsql_googleapis_com_database_network_connections", "1", "Number of connections to databases on the Cloud SQL instance. Only applicable to MySQL and SQL Server. Sampled every 60 seconds. After sampling, data is not visible for up to 165 seconds.", "cloudsql_database", []string{"project_id", "database_id", "region"}),
	nativeDelta("cloudsql.googleapis.com/database/network/received_bytes_count", "stackdriver_cloudsql_database_cloudsql_googleapis_com_database_network_received_bytes_count", "By", "Delta count of bytes received through the network. Sampled every 60 seconds. After sampling, data is not visible for up to 165 seconds.", "cloudsql_database", []string{"project_id", "database_id", "region"}),
	nativeDelta("cloudsql.googleapis.com/database/network/sent_bytes_count", "stackdriver_cloudsql_database_cloudsql_googleapis_com_database_network_sent_bytes_count", "By", "Delta count of bytes sent through the network. Sampled every 60 seconds. After sampling, data is not visible for up to 165 seconds.", "cloudsql_database", []string{"project_id", "database_id", "region"}),
	nativeGauge("cloudsql.googleapis.com/database/instance_state", "stackdriver_cloudsql_database_cloudsql_googleapis_com_database_instance_state", "", "The current serving state of the Cloud SQL instance. Because there are seven possible states, seven data points are returned. Each of them has a different field value representing each state. Only the one that matches the current state of the instance is TRUE. All the others are FALSE. The state can be one of the following: RUNNING: The instance is running, or is ready to run when accessed. SUSPENDED: The instance is not available, for example due to problems with billing. RUNNABLE: The instance has been stopped by owner. It is not currently running, but it's ready to be restarted. PENDING_CREATE: The instance is being created. MAINTENANCE: The instance is down for maintenance. FAILED: The instance creation failed. UNKNOWN_STATE: The state of the instance is unknown. Sampled every 60 seconds. After sampling, data is not visible for up to 165 seconds.", "cloudsql_database", []string{"project_id", "database_id", "region"}),
	nativeGauge("cloudsql.googleapis.com/database/replication/state", "stackdriver_cloudsql_database_cloudsql_googleapis_com_database_replication_state", "", "The current serving state of replication. Sampled every 60 seconds. After sampling, data is not visible for up to 165 seconds.", "cloudsql_database", []string{"project_id", "database_id", "region"}),
	nativeGauge("cloudsql.googleapis.com/database/mysql/innodb_buffer_pool_pages_total", "stackdriver_cloudsql_database_cloudsql_googleapis_com_database_mysql_innodb_buffer_pool_pages_total", "1", "Total number of pages in the InnoDB buffer pool. Sampled every 60 seconds. After sampling, data is not visible for up to 165 seconds.", "cloudsql_database", []string{"project_id", "database_id", "region"}),
	nativeGauge("cloudsql.googleapis.com/database/mysql/innodb_buffer_pool_pages_free", "stackdriver_cloudsql_database_cloudsql_googleapis_com_database_mysql_innodb_buffer_pool_pages_free", "1", "Number of unused pages in the InnoDB buffer pool. Sampled every 60 seconds. After sampling, data is not visible for up to 165 seconds.", "cloudsql_database", []string{"project_id", "database_id", "region"}),
	nativeGauge("cloudsql.googleapis.com/database/mysql/innodb_buffer_pool_pages_dirty", "stackdriver_cloudsql_database_cloudsql_googleapis_com_database_mysql_innodb_buffer_pool_pages_dirty", "1", "Number of unflushed pages in the InnoDB buffer pool. Sampled every 60 seconds. After sampling, data is not visible for up to 165 seconds.", "cloudsql_database", []string{"project_id", "database_id", "region"}),
	nativeGauge("cloudsql.googleapis.com/database/postgresql/num_backends", "stackdriver_cloudsql_database_cloudsql_googleapis_com_database_postgresql_num_backends", "1", "Number of connections to the Cloud SQL PostgreSQL instance. Sampled every 60 seconds. After sampling, data is not visible for up to 165 seconds.", "cloudsql_database", []string{"project_id", "database_id", "region"}),
	nativeDelta("cloudsql.googleapis.com/database/postgresql/transaction_count", "stackdriver_cloudsql_database_cloudsql_googleapis_com_database_postgresql_transaction_count", "1", "Delta count of number of transactions. Sampled every 60 seconds. After sampling, data is not visible for up to 165 seconds.", "cloudsql_database", []string{"project_id", "database_id", "region"}),
	nativeGauge("alloydb.googleapis.com/instance/postgres/instances", "stackdriver_alloydb_googleapis_com_instance_alloydb_googleapis_com_instance_postgres_instances", "1", "The number of nodes in the instance, along with their status, which can be either up or down. Sampled every 60 seconds. After sampling, data is not visible for up to 180 seconds.", "alloydb.googleapis.com/Instance", []string{"project_id", "cluster_id", "instance_id", "location"}),
	nativeGauge("alloydb.googleapis.com/instance/cpu/average_utilization", "stackdriver_alloydb_googleapis_com_instance_alloydb_googleapis_com_instance_cpu_average_utilization", "10^2.%", "Mean CPU utilization across all currently serving nodes of the instance from 0 to 100. Sampled every 60 seconds. After sampling, data is not visible for up to 240 seconds.", "alloydb.googleapis.com/Instance", []string{"project_id", "cluster_id", "instance_id", "location"}),
	nativeGauge("alloydb.googleapis.com/instance/cpu/maximum_utilization", "stackdriver_alloydb_googleapis_com_instance_alloydb_googleapis_com_instance_cpu_maximum_utilization", "10^2.%", "Maximum CPU utilization across all currently serving nodes of the instance from 0 to 100. Sampled every 60 seconds. After sampling, data is not visible for up to 240 seconds.", "alloydb.googleapis.com/Instance", []string{"project_id", "cluster_id", "instance_id", "location"}),
	nativeDelta("alloydb.googleapis.com/instance/postgresql/deadlock_count", "stackdriver_alloydb_googleapis_com_instance_alloydb_googleapis_com_instance_postgresql_deadlock_count", "1", "Number of deadlocks detected in the instance. Sampled every 60 seconds. After sampling, data is not visible for up to 180 seconds.", "alloydb.googleapis.com/Instance", []string{"project_id", "cluster_id", "instance_id", "location"}),
	nativeDelta("alloydb.googleapis.com/instance/postgresql/deleted_tuples_count", "stackdriver_alloydb_googleapis_com_instance_alloydb_googleapis_com_instance_postgresql_deleted_tuples_count", "1", "Number of rows deleted while processing the queries in the instance since the last sample. Sampled every 60 seconds. After sampling, data is not visible for up to 180 seconds.", "alloydb.googleapis.com/Instance", []string{"project_id", "cluster_id", "instance_id", "location"}),
	nativeDelta("alloydb.googleapis.com/instance/postgresql/fetched_tuples_count", "stackdriver_alloydb_googleapis_com_instance_alloydb_googleapis_com_instance_postgresql_fetched_tuples_count", "1", "Number of rows fetched while processing the queries in the instance since the last sample. Sampled every 60 seconds. After sampling, data is not visible for up to 180 seconds.", "alloydb.googleapis.com/Instance", []string{"project_id", "cluster_id", "instance_id", "location"}),
	nativeDelta("alloydb.googleapis.com/instance/postgresql/inserted_tuples_count", "stackdriver_alloydb_googleapis_com_instance_alloydb_googleapis_com_instance_postgresql_inserted_tuples_count", "1", "Number of rows inserted while processing the queries in the instance since the last sample. Sampled every 60 seconds. After sampling, data is not visible for up to 180 seconds.", "alloydb.googleapis.com/Instance", []string{"project_id", "cluster_id", "instance_id", "location"}),
	nativeDelta("alloydb.googleapis.com/instance/postgresql/updated_tuples_count", "stackdriver_alloydb_googleapis_com_instance_alloydb_googleapis_com_instance_postgresql_updated_tuples_count", "1", "Number of rows updated while processing the queries in the instance since the last sample. Sampled every 60 seconds. After sampling, data is not visible for up to 180 seconds.", "alloydb.googleapis.com/Instance", []string{"project_id", "cluster_id", "instance_id", "location"}),
	nativeDelta("alloydb.googleapis.com/instance/postgresql/written_tuples_count", "stackdriver_alloydb_googleapis_com_instance_alloydb_googleapis_com_instance_postgresql_written_tuples_count", "1", "Number of rows written while processing the queries in the instance since the last sample. Sampled every 60 seconds. After sampling, data is not visible for up to 180 seconds.", "alloydb.googleapis.com/Instance", []string{"project_id", "cluster_id", "instance_id", "location"}),
	nativeDelta("alloydb.googleapis.com/instance/postgresql/returned_tuples_count", "stackdriver_alloydb_googleapis_com_instance_alloydb_googleapis_com_instance_postgresql_returned_tuples_count", "1", "Number of rows scanned while processing the queries in the instance since the last sample. Sampled every 60 seconds. After sampling, data is not visible for up to 180 seconds.", "alloydb.googleapis.com/Instance", []string{"project_id", "cluster_id", "instance_id", "location"}),
	nativeDelta("alloydb.googleapis.com/instance/postgresql/new_connections_count", "stackdriver_alloydb_googleapis_com_instance_alloydb_googleapis_com_instance_postgresql_new_connections_count", "1", "The number of new connections added to the instance. Sampled every 60 seconds. After sampling, data is not visible for up to 180 seconds.", "alloydb.googleapis.com/Instance", []string{"project_id", "cluster_id", "instance_id", "location"}),
	nativeGauge("alloydb.googleapis.com/instance/postgres/total_connections", "stackdriver_alloydb_googleapis_com_instance_alloydb_googleapis_com_instance_postgres_total_connections", "1", "The number of active and idle connections to the AlloyDB instance across serving nodes of the instance. Sampled every 60 seconds. After sampling, data is not visible for up to 180 seconds.", "alloydb.googleapis.com/Instance", []string{"project_id", "cluster_id", "instance_id", "location"}),
	nativeDelta("alloydb.googleapis.com/instance/postgres/transaction_count", "stackdriver_alloydb_googleapis_com_instance_alloydb_googleapis_com_instance_postgres_transaction_count", "1", "The number of committed and rolled back transactions across all serving nodes of the instance. Sampled every 60 seconds. After sampling, data is not visible for up to 180 seconds.", "alloydb.googleapis.com/Instance", []string{"project_id", "cluster_id", "instance_id", "location"}),
	nativeGauge("alloydb.googleapis.com/database/postgresql/vacuum/oldest_transaction_age", "stackdriver_alloydb_googleapis_com_instance_alloydb_googleapis_com_database_postgresql_vacuum_oldest_transaction_age", "1", "Current age of the oldest uncommitted transaction. It's measured in the number of transactions that started since the oldest transaction. Sampled every 60 seconds. After sampling, data is not visible for up to 180 seconds.", "alloydb.googleapis.com/Instance", []string{"project_id", "cluster_id", "instance_id", "location"}),
	nativeGauge("alloydb.googleapis.com/instance/postgresql/backends_for_top_applications", "stackdriver_alloydb_googleapis_com_instance_alloydb_googleapis_com_instance_postgresql_backends_for_top_applications", "1", "The current number of connections to the AlloyDB instance, grouped by applications for top 500 applications. Sampled every 60 seconds. After sampling, data is not visible for up to 180 seconds.", "alloydb.googleapis.com/Instance", []string{"project_id", "cluster_id", "instance_id", "location"}),
	nativeDelta("alloydb.googleapis.com/node/postgres/wait_time", "stackdriver_alloydb_googleapis_com_instance_node_alloydb_googleapis_com_node_postgres_wait_time", "us", "Total elapsed wait time for each wait event in the node. Sampled every 60 seconds. After sampling, data is not visible for up to 180 seconds.", "alloydb.googleapis.com/InstanceNode", []string{"project_id", "cluster_id", "instance_id", "location"}),
	nativeDelta("alloydb.googleapis.com/node/postgres/wait_count", "stackdriver_alloydb_googleapis_com_instance_node_alloydb_googleapis_com_node_postgres_wait_count", "1", "Total number of times processes waited for each wait event in the node. Sampled every 60 seconds. After sampling, data is not visible for up to 180 seconds.", "alloydb.googleapis.com/InstanceNode", []string{"project_id", "cluster_id", "instance_id", "location"}),
	nativeGauge("alloydb.googleapis.com/node/postgres/backends_by_state", "stackdriver_alloydb_googleapis_com_instance_node_alloydb_googleapis_com_node_postgres_backends_by_state", "1", "The current number of connections to the node grouped by the state: idle, active, idle_in_transaction, idle_in_transaction_aborted, disabled, and fastpath_function_call. Sampled every 60 seconds. After sampling, data is not visible for up to 180 seconds.", "alloydb.googleapis.com/InstanceNode", []string{"project_id", "cluster_id", "instance_id", "location"}),
	nativeGauge("alloydb.googleapis.com/node/postgres/uptime", "stackdriver_alloydb_googleapis_com_instance_node_alloydb_googleapis_com_node_postgres_uptime", "1", "Rate of database availability in the node. Sampled every 60 seconds. After sampling, data is not visible for up to 180 seconds.", "alloydb.googleapis.com/InstanceNode", []string{"project_id", "cluster_id", "instance_id", "location"}),
	nativeGauge("alloydb.googleapis.com/database/postgresql/tuples", "stackdriver_alloydb_googleapis_com_database_alloydb_googleapis_com_database_postgresql_tuples", "1", "Number of tuples (rows) by state per database in the instance. This metric will only be exposed when the number of db’s is less than 50. Sampled every 60 seconds. After sampling, data is not visible for up to 180 seconds.", "alloydb.googleapis.com/Database", []string{"project_id", "cluster_id", "instance_id", "location", "database"}),
	nativeDelta("alloydb.googleapis.com/database/postgresql/blks_read_for_top_databases", "stackdriver_alloydb_googleapis_com_database_alloydb_googleapis_com_database_postgresql_blks_read_for_top_databases", "1", "Number of blocks read by Postgres that were not in the buffer cache per database for top 500 databases. Sampled every 60 seconds. After sampling, data is not visible for up to 180 seconds.", "alloydb.googleapis.com/Database", []string{"project_id", "cluster_id", "instance_id", "location", "database"}),
	nativeDelta("alloydb.googleapis.com/database/postgresql/blks_hit_for_top_databases", "stackdriver_alloydb_googleapis_com_database_alloydb_googleapis_com_database_postgresql_blks_hit_for_top_databases", "1", "Number of times Postgres found the requested block in the buffer cache per database for top 500 databases. Sampled every 60 seconds. After sampling, data is not visible for up to 180 seconds.", "alloydb.googleapis.com/Database", []string{"project_id", "cluster_id", "instance_id", "location", "database"}),
	nativeDelta("alloydb.googleapis.com/database/postgresql/temp_bytes_written_for_top_databases", "stackdriver_alloydb_googleapis_com_database_alloydb_googleapis_com_database_postgresql_temp_bytes_written_for_top_databases", "By", "The total amount of data(in bytes) written to temporary files by the queries per database for top 500 dbs. Sampled every 60 seconds. After sampling, data is not visible for up to 180 seconds.", "alloydb.googleapis.com/Database", []string{"project_id", "cluster_id", "instance_id", "location", "database"}),
	nativeDelta("alloydb.googleapis.com/database/postgresql/temp_files_written_for_top_databases", "stackdriver_alloydb_googleapis_com_database_alloydb_googleapis_com_database_postgresql_temp_files_written_for_top_databases", "1", "The number of temporary files used for writing data per database while performing internal algorithms like join, sort etc for top 500 dbs. Sampled every 60 seconds. After sampling, data is not visible for up to 180 seconds.", "alloydb.googleapis.com/Database", []string{"project_id", "cluster_id", "instance_id", "location", "database"}),
	nativeDelta("alloydb.googleapis.com/database/postgresql/rolledback_transactions_for_top_databases", "stackdriver_alloydb_googleapis_com_database_alloydb_googleapis_com_database_postgresql_rolledback_transactions_for_top_databases", "1", "Total number of transactions rolledback per database for top 500 databases. Sampled every 60 seconds. After sampling, data is not visible for up to 180 seconds.", "alloydb.googleapis.com/Database", []string{"project_id", "cluster_id", "instance_id", "location", "database"}),
	nativeDelta("alloydb.googleapis.com/database/postgresql/statements_executed_count", "stackdriver_alloydb_googleapis_com_database_alloydb_googleapis_com_database_postgresql_statements_executed_count", "1", "Total count of statements executed in the instance per database per operation_type. Only available for instances with Query insights enabled. Sampled every 60 seconds. After sampling, data is not visible for up to 180 seconds.", "alloydb.googleapis.com/Database", []string{"project_id", "cluster_id", "instance_id", "location", "database"}),
	nativeGauge("alloydb.googleapis.com/cluster/storage/usage", "stackdriver_alloydb_googleapis_com_cluster_alloydb_googleapis_com_cluster_storage_usage", "By", "The total AlloyDB storage in bytes across the entire cluster. Sampled every 60 seconds. After sampling, data is not visible for up to 180 seconds.", "alloydb.googleapis.com/Cluster", []string{"project_id", "cluster_id", "location"}),
	nativeGauge("storage.googleapis.com/storage/object_count", "stackdriver_gcs_bucket_storage_googleapis_com_storage_object_count", "1", "Total number of objects per bucket, grouped by storage class. Soft-deleted objects are not included in the total; use the updated v2 metric for a breakdown of total usage including soft-deleted objects. This value is measured once per day, and there might be a delay after measuring before the value becomes available in Cloud Monitoring. Once available, the value is repeated at each sampling interval throughout the day. Buckets with no objects in them are not tracked by this metric. For this metric, the sampling period is a reporting period, not a measurement period. Sampled every 300 seconds. After sampling, data is not visible for up to 690 seconds.", "gcs_bucket", []string{"project_id", "bucket_name", "location"}),
	nativeGauge("storage.googleapis.com/storage/total_bytes", "stackdriver_gcs_bucket_storage_googleapis_com_storage_total_bytes", "By", "Total size of all objects in the bucket, grouped by storage class. Soft-deleted objects are not included in the total; use the updated v2 metric for a breakdown of total usage including soft-deleted objects. This value is measured once per day, and there might be a delay after measuring before the value becomes available in Cloud Monitoring. Once available, the value is repeated at each sampling interval throughout the day. Buckets with no objects in them are not tracked by this metric. For this metric, the sampling period is a reporting period, not a measurement period. Sampled every 300 seconds. After sampling, data is not visible for up to 690 seconds.", "gcs_bucket", []string{"project_id", "bucket_name", "location"}),
	nativeDelta("storage.googleapis.com/network/received_bytes_count", "stackdriver_gcs_bucket_storage_googleapis_com_network_received_bytes_count", "By", "Delta count of bytes received over the network, grouped by the API method name and response code. Sampled every 60 seconds. After sampling, data is not visible for up to 210 seconds.", "gcs_bucket", []string{"project_id", "bucket_name", "location"}),
	nativeDelta("storage.googleapis.com/network/sent_bytes_count", "stackdriver_gcs_bucket_storage_googleapis_com_network_sent_bytes_count", "By", "Delta count of bytes sent over the network, grouped by the API method name and response code. Sampled every 60 seconds. After sampling, data is not visible for up to 210 seconds.", "gcs_bucket", []string{"project_id", "bucket_name", "location"}),
	nativeDelta("storage.googleapis.com/api/request_count", "stackdriver_gcs_bucket_storage_googleapis_com_api_request_count", "1", "Delta count of API calls, grouped by the API method name and response code. Sampled every 60 seconds. After sampling, data is not visible for up to 210 seconds.", "gcs_bucket", []string{"project_id", "bucket_name", "location"}),
	nativeDelta("networking.googleapis.com/google_service/request_bytes_count", "stackdriver_google_service_gce_client_networking_googleapis_com_google_service_request_bytes_count", "By", "The number of bytes sent in requests from the clients to the Google Service. Sampled every 60 seconds. After sampling, data is not visible for up to 150 seconds.", "google_service_gce_client", []string{"project_id"}),
	nativeDelta("networking.googleapis.com/google_service/response_bytes_count", "stackdriver_google_service_gce_client_networking_googleapis_com_google_service_response_bytes_count", "By", "The number of bytes sent in responses to the clients from the Google Service. Sampled every 60 seconds. After sampling, data is not visible for up to 150 seconds.", "google_service_gce_client", []string{"project_id"}),
	nativeGauge("networking.googleapis.com/fixed_standard_tier/usage", "stackdriver_networking_googleapis_com_location_networking_googleapis_com_fixed_standard_tier_usage", "By", "The current rate of egress bytes per second sent over Fixed Standard Tier. Sampled every 60 seconds. After sampling, data is not visible for up to 60 seconds.", "networking.googleapis.com/Location", []string{"project_id"}),
	nativeDelta("networking.googleapis.com/vpn_tunnel/egress_bytes_count", "stackdriver_vpn_tunnel_networking_googleapis_com_vpn_tunnel_egress_bytes_count", "By", "The number of bytes sent from GCP via the Cloud VPN tunnel. Sampled every 60 seconds. After sampling, data is not visible for up to 150 seconds.", "vpn_tunnel", []string{"project_id"}),
	nativeDelta("networking.googleapis.com/vpn_tunnel/ingress_bytes_count", "stackdriver_vpn_tunnel_networking_googleapis_com_vpn_tunnel_ingress_bytes_count", "By", "The number of bytes sent to GCP via the Cloud VPN tunnel. Sampled every 60 seconds. After sampling, data is not visible for up to 150 seconds.", "vpn_tunnel", []string{"project_id"}),
	nativeDelta("loadbalancing.googleapis.com/https/request_count", "stackdriver_https_lb_rule_loadbalancing_googleapis_com_https_request_count", "1", "The number of requests served by external HTTP(S) load balancer. Sampled every 60 seconds. After sampling, data is not visible for up to 210 seconds.", "https_lb_rule", []string{"project_id"}),
	nativeDelta("loadbalancing.googleapis.com/https/request_bytes_count", "stackdriver_https_lb_rule_loadbalancing_googleapis_com_https_request_bytes_count", "By", "The number of bytes sent as requests from clients to external HTTP(S) load balancer. Sampled every 60 seconds. After sampling, data is not visible for up to 210 seconds.", "https_lb_rule", []string{"project_id"}),
	nativeDelta("loadbalancing.googleapis.com/https/response_bytes_count", "stackdriver_https_lb_rule_loadbalancing_googleapis_com_https_response_bytes_count", "By", "The number of bytes sent as responses from external HTTP(S) load balancer to clients. Sampled every 60 seconds. After sampling, data is not visible for up to 210 seconds.", "https_lb_rule", []string{"project_id"}),
	nativeDelta("loadbalancing.googleapis.com/https/backend_request_bytes_count", "stackdriver_https_lb_rule_loadbalancing_googleapis_com_https_backend_request_bytes_count", "By", "The number of bytes sent as requests from external HTTP(S) load balancer to backends. For Service Extensions, this value represents the total number of bytes sent from the load balancer to the extension backend. Sampled every 60 seconds. After sampling, data is not visible for up to 210 seconds.", "https_lb_rule", []string{"project_id"}),
	nativeDelta("loadbalancing.googleapis.com/https/backend_response_bytes_count", "stackdriver_https_lb_rule_loadbalancing_googleapis_com_https_backend_response_bytes_count", "By", "The number of bytes sent as responses from backends (or cache) to external HTTP(S) load balancer. For Service Extensions, this value represents the total number of bytes received by the load balancer from the extension backend. Sampled every 60 seconds. After sampling, data is not visible for up to 210 seconds.", "https_lb_rule", []string{"project_id"}),
	nativeDeltaHistogram("loadbalancing.googleapis.com/https/total_latencies", "stackdriver_https_lb_rule_loadbalancing_googleapis_com_https_total_latencies", "ms", "A distribution of the latency calculated from when the request was received by the external HTTP(S) load balancer proxy until the proxy got ACK from client on last response byte. Sampled every 60 seconds. After sampling, data is not visible for up to 210 seconds.", "https_lb_rule", []string{"project_id"}),
	nativeDeltaHistogram("loadbalancing.googleapis.com/https/frontend_tcp_rtt", "stackdriver_https_lb_rule_loadbalancing_googleapis_com_https_frontend_tcp_rtt", "ms", "A distribution of the RTT measured for each connection between client and proxy. Sampled every 60 seconds. After sampling, data is not visible for up to 210 seconds.", "https_lb_rule", []string{"project_id"}),
	nativeDeltaHistogram("loadbalancing.googleapis.com/https/backend_latencies", "stackdriver_https_lb_rule_loadbalancing_googleapis_com_https_backend_latencies", "ms", "A distribution of the latency calculated from when the request was sent by the proxy to the backend until the proxy received from the backend the last byte of response. For Service Extensions, this value represents the sum of latencies of each ProcessingRequest/ProcessingResponse pair between the load balancer and the extension backend. Sampled every 60 seconds. After sampling, data is not visible for up to 210 seconds.", "https_lb_rule", []string{"project_id"}),
	nativeDelta("pubsub.googleapis.com/subscription/push_request_count", "stackdriver_pubsub_subscription_pubsub_googleapis_com_subscription_push_request_count", "1", "Cumulative count of push attempts, grouped by result. Unlike pulls, the push server implementation does not batch user messages. So each request only contains one user message. The push server retries on errors, so a given user message can appear multiple times. Sampled every 60 seconds. After sampling, data is not visible for up to 120 seconds.", "pubsub_subscription", []string{"project_id", "subscription_id"}),
	nativeDelta("pubsub.googleapis.com/subscription/pull_ack_request_count", "stackdriver_pubsub_subscription_pubsub_googleapis_com_subscription_pull_ack_request_count", "1", "Cumulative count of acknowledge requests, grouped by result. Sampled every 60 seconds. After sampling, data is not visible for up to 181 seconds.", "pubsub_subscription", []string{"project_id", "subscription_id"}),
	nativeDelta("pubsub.googleapis.com/subscription/streaming_pull_response_count", "stackdriver_pubsub_subscription_pubsub_googleapis_com_subscription_streaming_pull_response_count", "1", "Cumulative count of streaming pull responses, grouped by result. Sampled every 60 seconds. After sampling, data is not visible for up to 181 seconds.", "pubsub_subscription", []string{"project_id", "subscription_id"}),
	nativeDelta("pubsub.googleapis.com/subscription/expired_ack_deadlines_count", "stackdriver_pubsub_subscription_pubsub_googleapis_com_subscription_expired_ack_deadlines_count", "1", "Cumulative count of messages whose ack deadline expired while the message was outstanding to a subscriber client. Sampled every 60 seconds. After sampling, data is not visible for up to 240 seconds.", "pubsub_subscription", []string{"project_id", "subscription_id"}),
	nativeGauge("pubsub.googleapis.com/subscription/num_outstanding_messages", "stackdriver_pubsub_subscription_pubsub_googleapis_com_subscription_num_outstanding_messages", "1", "Number of messages delivered to a subscription's push endpoint, but not yet acknowledged. Sampled every 60 seconds. After sampling, data is not visible for up to 240 seconds.", "pubsub_subscription", []string{"project_id", "subscription_id"}),
	nativeGauge("pubsub.googleapis.com/subscription/num_undelivered_messages", "stackdriver_pubsub_subscription_pubsub_googleapis_com_subscription_num_undelivered_messages", "1", "Number of unacknowledged messages (a.k.a. backlog messages) in a subscription. Sampled every 60 seconds. After sampling, data is not visible for up to 120 seconds.", "pubsub_subscription", []string{"project_id", "subscription_id"}),
	nativeGauge("pubsub.googleapis.com/subscription/oldest_unacked_message_age", "stackdriver_pubsub_subscription_pubsub_googleapis_com_subscription_oldest_unacked_message_age", "s", "Age (in seconds) of the oldest unacknowledged message (a.k.a. backlog message) in a subscription. Sampled every 60 seconds. After sampling, data is not visible for up to 120 seconds.", "pubsub_subscription", []string{"project_id", "subscription_id"}),
	nativeGauge("pubsub.googleapis.com/subscription/delivery_latency_health_score", "stackdriver_pubsub_subscription_pubsub_googleapis_com_subscription_delivery_latency_health_score", "1", "A score that measures the health of a subscription over a 10 minute rolling window. Sampled every 60 seconds. After sampling, data is not visible for up to 360 seconds.", "pubsub_subscription", []string{"project_id", "subscription_id"}),
	nativeGauge("pubsub.googleapis.com/subscription/num_unacked_messages_by_region", "stackdriver_pubsub_subscription_pubsub_googleapis_com_subscription_num_unacked_messages_by_region", "1", "Number of unacknowledged messages in a subscription, broken down by Cloud region. Sampled every 60 seconds. After sampling, data is not visible for up to 120 seconds.", "pubsub_subscription", []string{"project_id", "subscription_id"}),
	nativeGauge("pubsub.googleapis.com/subscription/unacked_bytes_by_region", "stackdriver_pubsub_subscription_pubsub_googleapis_com_subscription_unacked_bytes_by_region", "By", "Total byte size of the unacknowledged messages in a subscription, broken down by Cloud region. Sampled every 60 seconds. After sampling, data is not visible for up to 120 seconds.", "pubsub_subscription", []string{"project_id", "subscription_id"}),
	nativeDeltaHistogram("pubsub.googleapis.com/subscription/push_request_latencies", "stackdriver_pubsub_subscription_pubsub_googleapis_com_subscription_push_request_latencies", "us", "Distribution of push request latencies (in microseconds), grouped by result. Sampled every 60 seconds. After sampling, data is not visible for up to 240 seconds.", "pubsub_subscription", []string{"project_id", "subscription_id"}),
	nativeGauge("run.googleapis.com/container/containers", "stackdriver_cloud_run_revision_run_googleapis_com_container_containers", "1", "Number of container instances that exist, broken down by state. Sampled every 60 seconds. After sampling, data is not visible for up to 120 seconds.", "cloud_run_revision", []string{"project_id", "service_name", "location", "revision_name", "configuration_name"}),
	nativeDelta("run.googleapis.com/container/network/received_bytes_count", "stackdriver_cloud_run_revision_run_googleapis_com_container_network_received_bytes_count", "By", "Incoming socket and HTTP request traffic, in bytes. Sampled every 60 seconds. After sampling, data is not visible for up to 120 seconds.", "cloud_run_revision", []string{"project_id", "service_name", "location", "revision_name", "configuration_name"}),
	nativeDelta("run.googleapis.com/container/network/sent_bytes_count", "stackdriver_cloud_run_revision_run_googleapis_com_container_network_sent_bytes_count", "By", "Outgoing socket and HTTP response traffic, in bytes. Sampled every 60 seconds. After sampling, data is not visible for up to 120 seconds.", "cloud_run_revision", []string{"project_id", "service_name", "location", "revision_name", "configuration_name"}),
	nativeDelta("run.googleapis.com/container/billable_instance_time", "stackdriver_cloud_run_revision_run_googleapis_com_container_billable_instance_time", "s", "Billable time aggregated across all container instances. For a given container instance, billable time occurs when the container instance is starting or at least one request is being processed. Billable time is rounded up to the nearest 100 milliseconds. Examples: If a revision with 2 container instances has been continuously serving traffic in the last minute, the value is 2s/s with the default \"rate\" aligner. If a single request lasting 30ms was received by a revision in the past minute, it is rounded up to 100ms and averaged to 1.7ms/s over the minute with the default \"rate\" aligner. Sampled every 60 seconds. After sampling, data is not visible for up to 120 seconds.", "cloud_run_revision", []string{"project_id", "service_name", "location", "revision_name", "configuration_name"}),
	nativeDelta("run.googleapis.com/container/network/throttled_inbound_bytes_count", "stackdriver_cloud_run_revision_run_googleapis_com_container_network_throttled_inbound_bytes_count", "By", "Inbound bytes dropped due to network throttling. Sampled every 60 seconds. After sampling, data is not visible for up to 120 seconds.", "cloud_run_revision", []string{"project_id", "service_name", "location", "revision_name", "configuration_name"}),
	nativeDelta("run.googleapis.com/container/network/throttled_outbound_bytes_count", "stackdriver_cloud_run_revision_run_googleapis_com_container_network_throttled_outbound_bytes_count", "By", "Outbound bytes dropped due to network throttling. Sampled every 60 seconds. After sampling, data is not visible for up to 120 seconds.", "cloud_run_revision", []string{"project_id", "service_name", "location", "revision_name", "configuration_name"}),
	nativeDelta("run.googleapis.com/container/completed_probe_attempt_count", "stackdriver_cloud_run_revision_run_googleapis_com_container_completed_probe_attempt_count", "1", "Number of completed health check probe attempts and their results. Sampled every 60 seconds. After sampling, data is not visible for up to 120 seconds.", "cloud_run_revision", []string{"project_id", "service_name", "location", "revision_name", "configuration_name"}),
	nativeDelta("run.googleapis.com/container/completed_probe_count", "stackdriver_cloud_run_revision_run_googleapis_com_container_completed_probe_count", "1", "Number of completed health check probes and their results. Sampled every 60 seconds. After sampling, data is not visible for up to 120 seconds.", "cloud_run_revision", []string{"project_id", "service_name", "location", "revision_name", "configuration_name"}),
	nativeDeltaHistogram("run.googleapis.com/container/max_request_concurrencies", "stackdriver_cloud_run_revision_run_googleapis_com_container_max_request_concurrencies", "1", "Distribution of the maximum number of concurrent requests being served by each container instance over a minute. Filter by 'state' = 'active' only get the concurrency of active container instances. Sampled every 60 seconds. After sampling, data is not visible for up to 120 seconds.", "cloud_run_revision", []string{"project_id", "service_name", "location", "revision_name", "configuration_name"}),
	nativeDeltaHistogram("run.googleapis.com/container/startup_latencies", "stackdriver_cloud_run_revision_run_googleapis_com_container_startup_latencies", "ms", "Distribution of time spent starting a new container instance in milliseconds. Sampled every 60 seconds. After sampling, data is not visible for up to 120 seconds.", "cloud_run_revision", []string{"project_id", "service_name", "location", "revision_name", "configuration_name"}),
	nativeDeltaHistogram("run.googleapis.com/container/probe_attempt_latencies", "stackdriver_cloud_run_revision_run_googleapis_com_container_probe_attempt_latencies", "ms", "Distribution of time spent running a single probe attempt before success or failure in milliseconds. Sampled every 60 seconds. After sampling, data is not visible for up to 120 seconds.", "cloud_run_revision", []string{"project_id", "service_name", "location", "revision_name", "configuration_name"}),
	nativeDeltaHistogram("run.googleapis.com/container/probe_latencies", "stackdriver_cloud_run_revision_run_googleapis_com_container_probe_latencies", "ms", "Distribution of time spent running a probe before success or failure in milliseconds. Sampled every 60 seconds. After sampling, data is not visible for up to 120 seconds.", "cloud_run_revision", []string{"project_id", "service_name", "location", "revision_name", "configuration_name"}),
	nativeGauge("bigtable.googleapis.com/cluster/node_count", "stackdriver_bigtable_cluster_bigtable_googleapis_com_cluster_node_count", "1", "Number of nodes in a cluster. Sampled every 60 seconds. After sampling, data is not visible for up to 240 seconds.", "bigtable_cluster", []string{"project_id", "exported_instance", "cluster", "zone"}),
	nativeGauge("bigtable.googleapis.com/cluster/cpu_load", "stackdriver_bigtable_cluster_bigtable_googleapis_com_cluster_cpu_load", "1", "CPU load of a cluster. Sampled every 60 seconds. After sampling, data is not visible for up to 240 seconds.", "bigtable_cluster", []string{"project_id", "exported_instance", "cluster", "zone"}),
	nativeGauge("bigtable.googleapis.com/cluster/cpu_load_hottest_node", "stackdriver_bigtable_cluster_bigtable_googleapis_com_cluster_cpu_load_hottest_node", "1", "CPU load of the busiest node in a cluster. Sampled every 60 seconds. After sampling, data is not visible for up to 180 seconds.", "bigtable_cluster", []string{"project_id", "exported_instance", "cluster", "zone"}),
	nativeGauge("bigtable.googleapis.com/cluster/storage_utilization", "stackdriver_bigtable_cluster_bigtable_googleapis_com_cluster_storage_utilization", "1", "Storage used as a fraction of total storage capacity. Sampled every 60 seconds. After sampling, data is not visible for up to 180 seconds.", "bigtable_cluster", []string{"project_id", "exported_instance", "cluster", "zone"}),
	nativeGauge("bigtable.googleapis.com/disk/bytes_used", "stackdriver_bigtable_cluster_bigtable_googleapis_com_disk_bytes_used", "By", "Amount of compressed data for tables stored in a cluster. Sampled every 60 seconds. After sampling, data is not visible for up to 180 seconds.", "bigtable_cluster", []string{"project_id", "exported_instance", "cluster", "zone"}),
	nativeGauge("bigtable.googleapis.com/disk/storage_capacity", "stackdriver_bigtable_cluster_bigtable_googleapis_com_disk_storage_capacity", "By", "Capacity of compressed data for tables that can be stored in a cluster. Sampled every 60 seconds. After sampling, data is not visible for up to 240 seconds.", "bigtable_cluster", []string{"project_id", "exported_instance", "cluster", "zone"}),
	nativeGauge("bigtable.googleapis.com/cluster/cpu_load_by_app_profile_by_method_by_table", "stackdriver_bigtable_cluster_bigtable_googleapis_com_cluster_cpu_load_by_app_profile_by_method_by_table", "1", "CPU load of a cluster. Split by app profile, method, and table. Contains the same underlying data as bigtable.googleapis.com/cluster/cpu_load. Sampled every 60 seconds. After sampling, data is not visible for up to 240 seconds.", "bigtable_cluster", []string{"project_id", "exported_instance", "cluster", "zone"}),
	nativeGauge("bigtable.googleapis.com/table/bytes_used", "stackdriver_bigtable_table_bigtable_googleapis_com_table_bytes_used", "By", "Amount of compressed data stored in a table. Sampled every 60 seconds. After sampling, data is not visible for up to 120 seconds.", "bigtable_table", []string{"project_id", "exported_instance", "cluster", "table", "zone"}),
	nativeGauge("bigtable.googleapis.com/server/data_boost/spu_usage", "stackdriver_bigtable_table_bigtable_googleapis_com_server_data_boost_spu_usage", "1", "The Serverless-Processing-Units usage (in SPU-seconds) for Data Boost requests. Sampled every 60 seconds. After sampling, data is not visible for up to 120 seconds.", "bigtable_table", []string{"project_id", "exported_instance", "cluster", "table", "zone"}),
	nativeDelta("bigtable.googleapis.com/server/returned_rows_count", "stackdriver_bigtable_table_bigtable_googleapis_com_server_returned_rows_count", "1", "Number of rows returned by server requests for a table. Sampled every 60 seconds. After sampling, data is not visible for up to 120 seconds.", "bigtable_table", []string{"project_id", "exported_instance", "cluster", "table", "zone"}),
	nativeDelta("bigtable.googleapis.com/server/modified_rows_count", "stackdriver_bigtable_table_bigtable_googleapis_com_server_modified_rows_count", "1", "Number of rows modified by server requests for a table. Sampled every 60 seconds. After sampling, data is not visible for up to 120 seconds.", "bigtable_table", []string{"project_id", "exported_instance", "cluster", "table", "zone"}),
	nativeDelta("bigtable.googleapis.com/server/sent_bytes_count", "stackdriver_bigtable_table_bigtable_googleapis_com_server_sent_bytes_count", "By", "Number of bytes of response data sent by servers for a table. Sampled every 60 seconds. After sampling, data is not visible for up to 120 seconds.", "bigtable_table", []string{"project_id", "exported_instance", "cluster", "table", "zone"}),
	nativeDelta("bigtable.googleapis.com/server/received_bytes_count", "stackdriver_bigtable_table_bigtable_googleapis_com_server_received_bytes_count", "By", "Number of bytes of request data received by servers for a table. Sampled every 60 seconds. After sampling, data is not visible for up to 120 seconds.", "bigtable_table", []string{"project_id", "exported_instance", "cluster", "table", "zone"}),
	nativeDelta("bigtable.googleapis.com/server/error_count", "stackdriver_bigtable_table_bigtable_googleapis_com_server_error_count", "1", "Number of server requests for a table that failed with an error. Sampled every 60 seconds. After sampling, data is not visible for up to 120 seconds.", "bigtable_table", []string{"project_id", "exported_instance", "cluster", "table", "zone"}),
	nativeDelta("bigtable.googleapis.com/server/multi_cluster_failovers_count", "stackdriver_bigtable_table_bigtable_googleapis_com_server_multi_cluster_failovers_count", "1", "Number of failovers during multi-cluster requests. Sampled every 60 seconds. After sampling, data is not visible for up to 120 seconds.", "bigtable_table", []string{"project_id", "exported_instance", "cluster", "table", "zone"}),
	nativeDelta("bigtable.googleapis.com/server/request_count", "stackdriver_bigtable_table_bigtable_googleapis_com_server_request_count", "1", "Number of server requests for a table. Sampled every 60 seconds. After sampling, data is not visible for up to 120 seconds.", "bigtable_table", []string{"project_id", "exported_instance", "cluster", "table", "zone"}),
	nativeDeltaHistogram("bigtable.googleapis.com/server/latencies", "stackdriver_bigtable_table_bigtable_googleapis_com_server_latencies", "ms", "Distribution of server request latencies for a table. The latency is measured between the time when Cloud Bigtable (behind the Google frontend) receives an RPC and the time when it sends back the last byte of the response. Sampled every 60 seconds. After sampling, data is not visible for up to 120 seconds.", "bigtable_table", []string{"project_id", "exported_instance", "cluster", "table", "zone"}),
	nativeDeltaHistogram("bigtable.googleapis.com/client/operation_latencies", "stackdriver_bigtable_table_bigtable_googleapis_com_client_operation_latencies", "ms", "Distribution of the total end-to-end latency across all RPC attempts associated with a Bigtable operation. Sampled every 60 seconds. After sampling, data is not visible for up to 120 seconds.", "bigtable_table", []string{"project_id", "exported_instance", "cluster", "table", "zone"}),
	nativeDeltaHistogram("bigtable.googleapis.com/client/attempt_latencies", "stackdriver_bigtable_table_bigtable_googleapis_com_client_attempt_latencies", "ms", "Client observed latency per RPC attempt. Sampled every 60 seconds. After sampling, data is not visible for up to 120 seconds.", "bigtable_table", []string{"project_id", "exported_instance", "cluster", "table", "zone"}),
})

type nativeMetricMetadataEntry struct {
	metricKeys       []string
	scale            float64
	gaugeFromCounter bool
}

// nativeMetricMetadata is the vendor descriptor contract. Metric labels are
// allowlisted per family; resource labels are kept separately in each spec.
// The two non-unity scales preserve the vendor units while the scrape lane
// continues to expose milliseconds.
var nativeMetricMetadata = map[string]nativeMetricMetadataEntry{
	"compute.googleapis.com/instance/cpu/utilization":                                      {metricKeys: []string{"instance_name"}, scale: 1, gaugeFromCounter: false},
	"compute.googleapis.com/instance/cpu/usage_time":                                       {metricKeys: []string{"instance_name"}, scale: 1, gaugeFromCounter: false},
	"compute.googleapis.com/instance/network/received_bytes_count":                         {metricKeys: []string{"instance_name", "loadbalanced"}, scale: 1, gaugeFromCounter: false},
	"compute.googleapis.com/instance/network/sent_bytes_count":                             {metricKeys: []string{"instance_name", "loadbalanced"}, scale: 1, gaugeFromCounter: false},
	"compute.googleapis.com/instance/disk/read_bytes_count":                                {metricKeys: []string{"instance_name", "device_name", "storage_type", "device_type"}, scale: 1, gaugeFromCounter: false},
	"compute.googleapis.com/instance/disk/write_bytes_count":                               {metricKeys: []string{"instance_name", "device_name", "storage_type", "device_type"}, scale: 1, gaugeFromCounter: false},
	"compute.googleapis.com/instance/disk/read_ops_count":                                  {metricKeys: []string{"instance_name", "device_name", "storage_type", "device_type"}, scale: 1, gaugeFromCounter: false},
	"compute.googleapis.com/instance/disk/write_ops_count":                                 {metricKeys: []string{"instance_name", "device_name", "storage_type", "device_type"}, scale: 1, gaugeFromCounter: false},
	"cloudsql.googleapis.com/database/up":                                                  {metricKeys: []string{}, scale: 1, gaugeFromCounter: false},
	"cloudsql.googleapis.com/database/cpu/utilization":                                     {metricKeys: []string{}, scale: 1, gaugeFromCounter: false},
	"cloudsql.googleapis.com/database/memory/utilization":                                  {metricKeys: []string{}, scale: 1, gaugeFromCounter: false},
	"cloudsql.googleapis.com/database/disk/utilization":                                    {metricKeys: []string{}, scale: 1, gaugeFromCounter: false},
	"cloudsql.googleapis.com/database/available_for_failover":                              {metricKeys: []string{}, scale: 1, gaugeFromCounter: false},
	"cloudsql.googleapis.com/database/cpu/reserved_cores":                                  {metricKeys: []string{}, scale: 1, gaugeFromCounter: false},
	"cloudsql.googleapis.com/database/memory/quota":                                        {metricKeys: []string{}, scale: 1, gaugeFromCounter: false},
	"cloudsql.googleapis.com/database/disk/quota":                                          {metricKeys: []string{}, scale: 1, gaugeFromCounter: false},
	"cloudsql.googleapis.com/database/disk/read_ops_count":                                 {metricKeys: []string{}, scale: 1, gaugeFromCounter: false},
	"cloudsql.googleapis.com/database/disk/write_ops_count":                                {metricKeys: []string{}, scale: 1, gaugeFromCounter: false},
	"cloudsql.googleapis.com/database/network/connections":                                 {metricKeys: []string{}, scale: 1, gaugeFromCounter: false},
	"cloudsql.googleapis.com/database/network/received_bytes_count":                        {metricKeys: []string{}, scale: 1, gaugeFromCounter: false},
	"cloudsql.googleapis.com/database/network/sent_bytes_count":                            {metricKeys: []string{"destination"}, scale: 1, gaugeFromCounter: false},
	"cloudsql.googleapis.com/database/instance_state":                                      {metricKeys: []string{"state"}, scale: 1, gaugeFromCounter: false},
	"cloudsql.googleapis.com/database/replication/state":                                   {metricKeys: []string{"state"}, scale: 1, gaugeFromCounter: false},
	"cloudsql.googleapis.com/database/mysql/innodb_buffer_pool_pages_total":                {metricKeys: []string{}, scale: 1, gaugeFromCounter: false},
	"cloudsql.googleapis.com/database/mysql/innodb_buffer_pool_pages_free":                 {metricKeys: []string{}, scale: 1, gaugeFromCounter: false},
	"cloudsql.googleapis.com/database/mysql/innodb_buffer_pool_pages_dirty":                {metricKeys: []string{}, scale: 1, gaugeFromCounter: false},
	"cloudsql.googleapis.com/database/postgresql/num_backends":                             {metricKeys: []string{"database"}, scale: 1, gaugeFromCounter: false},
	"cloudsql.googleapis.com/database/postgresql/transaction_count":                        {metricKeys: []string{"database", "transaction_type"}, scale: 1, gaugeFromCounter: false},
	"alloydb.googleapis.com/instance/postgres/instances":                                   {metricKeys: []string{"status"}, scale: 1, gaugeFromCounter: false},
	"alloydb.googleapis.com/instance/cpu/average_utilization":                              {metricKeys: []string{}, scale: 1, gaugeFromCounter: false},
	"alloydb.googleapis.com/instance/cpu/maximum_utilization":                              {metricKeys: []string{}, scale: 1, gaugeFromCounter: false},
	"alloydb.googleapis.com/instance/postgresql/deadlock_count":                            {metricKeys: []string{}, scale: 1, gaugeFromCounter: false},
	"alloydb.googleapis.com/instance/postgresql/deleted_tuples_count":                      {metricKeys: []string{}, scale: 1, gaugeFromCounter: false},
	"alloydb.googleapis.com/instance/postgresql/fetched_tuples_count":                      {metricKeys: []string{}, scale: 1, gaugeFromCounter: false},
	"alloydb.googleapis.com/instance/postgresql/inserted_tuples_count":                     {metricKeys: []string{}, scale: 1, gaugeFromCounter: false},
	"alloydb.googleapis.com/instance/postgresql/updated_tuples_count":                      {metricKeys: []string{}, scale: 1, gaugeFromCounter: false},
	"alloydb.googleapis.com/instance/postgresql/written_tuples_count":                      {metricKeys: []string{}, scale: 1, gaugeFromCounter: false},
	"alloydb.googleapis.com/instance/postgresql/returned_tuples_count":                     {metricKeys: []string{}, scale: 1, gaugeFromCounter: false},
	"alloydb.googleapis.com/instance/postgresql/new_connections_count":                     {metricKeys: []string{}, scale: 1, gaugeFromCounter: false},
	"alloydb.googleapis.com/instance/postgres/total_connections":                           {metricKeys: []string{}, scale: 1, gaugeFromCounter: false},
	"alloydb.googleapis.com/instance/postgres/transaction_count":                           {metricKeys: []string{}, scale: 1, gaugeFromCounter: false},
	"alloydb.googleapis.com/database/postgresql/vacuum/oldest_transaction_age":             {metricKeys: []string{"type"}, scale: 1, gaugeFromCounter: false},
	"alloydb.googleapis.com/instance/postgresql/backends_for_top_applications":             {metricKeys: []string{"application_name"}, scale: 1, gaugeFromCounter: false},
	"alloydb.googleapis.com/node/postgres/wait_time":                                       {metricKeys: []string{"wait_event_type", "wait_event_name"}, scale: 1000, gaugeFromCounter: false},
	"alloydb.googleapis.com/node/postgres/wait_count":                                      {metricKeys: []string{"wait_event_type", "wait_event_name"}, scale: 1, gaugeFromCounter: false},
	"alloydb.googleapis.com/node/postgres/backends_by_state":                               {metricKeys: []string{"state"}, scale: 1, gaugeFromCounter: false},
	"alloydb.googleapis.com/node/postgres/uptime":                                          {metricKeys: []string{}, scale: 1, gaugeFromCounter: true},
	"alloydb.googleapis.com/database/postgresql/tuples":                                    {metricKeys: []string{"state"}, scale: 1, gaugeFromCounter: false},
	"alloydb.googleapis.com/database/postgresql/blks_read_for_top_databases":               {metricKeys: []string{}, scale: 1, gaugeFromCounter: false},
	"alloydb.googleapis.com/database/postgresql/blks_hit_for_top_databases":                {metricKeys: []string{}, scale: 1, gaugeFromCounter: false},
	"alloydb.googleapis.com/database/postgresql/temp_bytes_written_for_top_databases":      {metricKeys: []string{}, scale: 1, gaugeFromCounter: false},
	"alloydb.googleapis.com/database/postgresql/temp_files_written_for_top_databases":      {metricKeys: []string{}, scale: 1, gaugeFromCounter: false},
	"alloydb.googleapis.com/database/postgresql/rolledback_transactions_for_top_databases": {metricKeys: []string{}, scale: 1, gaugeFromCounter: false},
	"alloydb.googleapis.com/database/postgresql/statements_executed_count":                 {metricKeys: []string{"operation_type"}, scale: 1, gaugeFromCounter: false},
	"alloydb.googleapis.com/cluster/storage/usage":                                         {metricKeys: []string{}, scale: 1, gaugeFromCounter: false},
	"storage.googleapis.com/storage/object_count":                                          {metricKeys: []string{"storage_class"}, scale: 1, gaugeFromCounter: false},
	"storage.googleapis.com/storage/total_bytes":                                           {metricKeys: []string{"storage_class"}, scale: 1, gaugeFromCounter: false},
	"storage.googleapis.com/network/received_bytes_count":                                  {metricKeys: []string{"response_code", "method"}, scale: 1, gaugeFromCounter: false},
	"storage.googleapis.com/network/sent_bytes_count":                                      {metricKeys: []string{"response_code", "method"}, scale: 1, gaugeFromCounter: false},
	"storage.googleapis.com/api/request_count":                                             {metricKeys: []string{"response_code", "method"}, scale: 1, gaugeFromCounter: false},
	"networking.googleapis.com/google_service/request_bytes_count":                         {metricKeys: []string{"protocol", "response_code_class", "service_name", "service_region", "local_network", "local_subnetwork", "local_network_interface"}, scale: 1, gaugeFromCounter: false},
	"networking.googleapis.com/google_service/response_bytes_count":                        {metricKeys: []string{"protocol", "response_code_class", "service_name", "service_region", "local_network", "local_subnetwork", "local_network_interface"}, scale: 1, gaugeFromCounter: false},
	"networking.googleapis.com/fixed_standard_tier/usage":                                  {metricKeys: []string{"bandwidth_policy_id", "traffic_source"}, scale: 1, gaugeFromCounter: true},
	"networking.googleapis.com/vpn_tunnel/egress_bytes_count":                              {metricKeys: []string{"local_project_number", "local_project_id", "local_region", "local_zone", "local_location_type", "local_resource_type", "local_network", "local_subnetwork", "protocol"}, scale: 1, gaugeFromCounter: false},
	"networking.googleapis.com/vpn_tunnel/ingress_bytes_count":                             {metricKeys: []string{"local_project_number", "local_project_id", "local_region", "local_zone", "local_location_type", "local_resource_type", "local_network", "local_subnetwork", "protocol"}, scale: 1, gaugeFromCounter: false},
	"loadbalancing.googleapis.com/https/request_count":                                     {metricKeys: []string{"protocol", "response_code", "load_balancing_scheme", "response_code_class", "proxy_continent", "cache_result", "client_country"}, scale: 1, gaugeFromCounter: false},
	"loadbalancing.googleapis.com/https/request_bytes_count":                               {metricKeys: []string{"protocol", "response_code", "load_balancing_scheme", "response_code_class", "proxy_continent", "cache_result", "client_country"}, scale: 1, gaugeFromCounter: false},
	"loadbalancing.googleapis.com/https/response_bytes_count":                              {metricKeys: []string{"protocol", "response_code", "load_balancing_scheme", "response_code_class", "proxy_continent", "cache_result", "client_country"}, scale: 1, gaugeFromCounter: false},
	"loadbalancing.googleapis.com/https/backend_request_bytes_count":                       {metricKeys: []string{"response_code", "load_balancing_scheme", "response_code_class", "proxy_continent", "cache_result"}, scale: 1, gaugeFromCounter: false},
	"loadbalancing.googleapis.com/https/backend_response_bytes_count":                      {metricKeys: []string{"response_code", "load_balancing_scheme", "response_code_class", "proxy_continent", "cache_result"}, scale: 1, gaugeFromCounter: false},
	"loadbalancing.googleapis.com/https/total_latencies":                                   {metricKeys: []string{"protocol", "response_code", "load_balancing_scheme", "response_code_class", "proxy_continent", "cache_result", "client_country"}, scale: 1, gaugeFromCounter: false},
	"loadbalancing.googleapis.com/https/frontend_tcp_rtt":                                  {metricKeys: []string{"load_balancing_scheme", "proxy_continent", "client_country"}, scale: 1, gaugeFromCounter: false},
	"loadbalancing.googleapis.com/https/backend_latencies":                                 {metricKeys: []string{"protocol", "response_code", "load_balancing_scheme", "response_code_class", "proxy_continent", "cache_result", "client_country"}, scale: 1, gaugeFromCounter: false},
	"pubsub.googleapis.com/subscription/push_request_count":                                {metricKeys: []string{"response_class", "response_code", "delivery_type"}, scale: 1, gaugeFromCounter: false},
	"pubsub.googleapis.com/subscription/pull_ack_request_count":                            {metricKeys: []string{"response_class", "response_code"}, scale: 1, gaugeFromCounter: false},
	"pubsub.googleapis.com/subscription/streaming_pull_response_count":                     {metricKeys: []string{"response_class", "response_code"}, scale: 1, gaugeFromCounter: false},
	"pubsub.googleapis.com/subscription/expired_ack_deadlines_count":                       {metricKeys: []string{"delivery_type"}, scale: 1, gaugeFromCounter: false},
	"pubsub.googleapis.com/subscription/num_outstanding_messages":                          {metricKeys: []string{}, scale: 1, gaugeFromCounter: false},
	"pubsub.googleapis.com/subscription/num_undelivered_messages":                          {metricKeys: []string{}, scale: 1, gaugeFromCounter: false},
	"pubsub.googleapis.com/subscription/oldest_unacked_message_age":                        {metricKeys: []string{}, scale: 1, gaugeFromCounter: false},
	"pubsub.googleapis.com/subscription/delivery_latency_health_score":                     {metricKeys: []string{"criteria"}, scale: 1, gaugeFromCounter: false},
	"pubsub.googleapis.com/subscription/num_unacked_messages_by_region":                    {metricKeys: []string{"region"}, scale: 1, gaugeFromCounter: false},
	"pubsub.googleapis.com/subscription/unacked_bytes_by_region":                           {metricKeys: []string{"region"}, scale: 1, gaugeFromCounter: false},
	"pubsub.googleapis.com/subscription/push_request_latencies":                            {metricKeys: []string{"response_code", "delivery_type"}, scale: 1000, gaugeFromCounter: false},
	"run.googleapis.com/container/containers":                                              {metricKeys: []string{"container_name", "state"}, scale: 1, gaugeFromCounter: false},
	"run.googleapis.com/container/network/received_bytes_count":                            {metricKeys: []string{"kind"}, scale: 1, gaugeFromCounter: false},
	"run.googleapis.com/container/network/sent_bytes_count":                                {metricKeys: []string{"kind"}, scale: 1, gaugeFromCounter: false},
	"run.googleapis.com/container/billable_instance_time":                                  {metricKeys: []string{}, scale: 1, gaugeFromCounter: false},
	"run.googleapis.com/container/network/throttled_inbound_bytes_count":                   {metricKeys: []string{"network", "transport", "type"}, scale: 1, gaugeFromCounter: false},
	"run.googleapis.com/container/network/throttled_outbound_bytes_count":                  {metricKeys: []string{"network", "transport", "type"}, scale: 1, gaugeFromCounter: false},
	"run.googleapis.com/container/completed_probe_attempt_count":                           {metricKeys: []string{"probe_action", "is_healthy", "container_name", "is_default", "probe_type"}, scale: 1, gaugeFromCounter: false},
	"run.googleapis.com/container/completed_probe_count":                                   {metricKeys: []string{"probe_action", "is_healthy", "container_name", "is_default", "probe_type"}, scale: 1, gaugeFromCounter: false},
	"run.googleapis.com/container/max_request_concurrencies":                               {metricKeys: []string{"state"}, scale: 1, gaugeFromCounter: false},
	"run.googleapis.com/container/startup_latencies":                                       {metricKeys: []string{}, scale: 1, gaugeFromCounter: false},
	"run.googleapis.com/container/probe_attempt_latencies":                                 {metricKeys: []string{"probe_action", "is_healthy", "container_name", "is_default", "probe_type"}, scale: 1, gaugeFromCounter: false},
	"run.googleapis.com/container/probe_latencies":                                         {metricKeys: []string{"probe_action", "is_healthy", "container_name", "is_default", "probe_type"}, scale: 1, gaugeFromCounter: false},
	"bigtable.googleapis.com/cluster/node_count":                                           {metricKeys: []string{"storage_type"}, scale: 1, gaugeFromCounter: false},
	"bigtable.googleapis.com/cluster/cpu_load":                                             {metricKeys: []string{}, scale: 1, gaugeFromCounter: false},
	"bigtable.googleapis.com/cluster/cpu_load_hottest_node":                                {metricKeys: []string{}, scale: 1, gaugeFromCounter: false},
	"bigtable.googleapis.com/cluster/storage_utilization":                                  {metricKeys: []string{"storage_type"}, scale: 1, gaugeFromCounter: false},
	"bigtable.googleapis.com/disk/bytes_used":                                              {metricKeys: []string{"storage_type"}, scale: 1, gaugeFromCounter: false},
	"bigtable.googleapis.com/disk/storage_capacity":                                        {metricKeys: []string{"storage_type"}, scale: 1, gaugeFromCounter: false},
	"bigtable.googleapis.com/cluster/cpu_load_by_app_profile_by_method_by_table":           {metricKeys: []string{"app_profile", "method", "table"}, scale: 1, gaugeFromCounter: false},
	"bigtable.googleapis.com/table/bytes_used":                                             {metricKeys: []string{"storage_type"}, scale: 1, gaugeFromCounter: false},
	"bigtable.googleapis.com/server/data_boost/spu_usage":                                  {metricKeys: []string{"app_profile", "method"}, scale: 1, gaugeFromCounter: false},
	"bigtable.googleapis.com/server/returned_rows_count":                                   {metricKeys: []string{"method", "app_profile"}, scale: 1, gaugeFromCounter: false},
	"bigtable.googleapis.com/server/modified_rows_count":                                   {metricKeys: []string{"method", "app_profile"}, scale: 1, gaugeFromCounter: false},
	"bigtable.googleapis.com/server/sent_bytes_count":                                      {metricKeys: []string{"method", "app_profile"}, scale: 1, gaugeFromCounter: false},
	"bigtable.googleapis.com/server/received_bytes_count":                                  {metricKeys: []string{"method", "app_profile"}, scale: 1, gaugeFromCounter: false},
	"bigtable.googleapis.com/server/error_count":                                           {metricKeys: []string{"method", "error_code", "app_profile"}, scale: 1, gaugeFromCounter: false},
	"bigtable.googleapis.com/server/multi_cluster_failovers_count":                         {metricKeys: []string{"method", "app_profile"}, scale: 1, gaugeFromCounter: false},
	"bigtable.googleapis.com/server/request_count":                                         {metricKeys: []string{"method", "app_profile"}, scale: 1, gaugeFromCounter: false},
	"bigtable.googleapis.com/server/latencies":                                             {metricKeys: []string{"method", "app_profile"}, scale: 1, gaugeFromCounter: false},
	"bigtable.googleapis.com/client/operation_latencies":                                   {metricKeys: []string{"method", "app_profile", "streaming", "status", "client_name"}, scale: 1, gaugeFromCounter: false},
	"bigtable.googleapis.com/client/attempt_latencies":                                     {metricKeys: []string{"method", "app_profile", "streaming", "status", "client_name"}, scale: 1, gaugeFromCounter: false},
}

func applyNativeMetadata(catalog []nativeMetricSpec) []nativeMetricSpec {
	for i := range catalog {
		metadata, ok := nativeMetricMetadata[catalog[i].nativeName]
		if !ok {
			panic("cspgcp: native metric has no vendor metadata: " + catalog[i].nativeName)
		}
		catalog[i].metricKeys = append([]string(nil), metadata.metricKeys...)
		catalog[i].scale = metadata.scale
		catalog[i].gaugeFromCounter = metadata.gaugeFromCounter
	}
	return catalog
}

var nativeMetricByScrape = func() map[string]nativeMetricSpec {
	out := make(map[string]nativeMetricSpec, len(nativeMetricCatalog))
	for _, spec := range nativeMetricCatalog {
		out[spec.scrapeName] = spec
	}
	return out
}()

type nativeOTLPState struct {
	previousTick  time.Time
	scalarPrev    map[string]float64
	histogramPrev map[string]nativeHistogramSnapshot
}

type nativeHistogramSnapshot struct {
	bounds       []float64
	bucketCounts []uint64
	sum          float64
	count        uint64
}

func newNativeOTLPState() *nativeOTLPState {
	return &nativeOTLPState{
		scalarPrev:    make(map[string]float64),
		histogramPrev: make(map[string]nativeHistogramSnapshot),
	}
}

func (s *nativeOTLPState) begin(now time.Time) time.Time {
	start := now.Add(-60 * time.Second)
	if !s.previousTick.IsZero() {
		start = s.previousTick
	}
	s.previousTick = now
	return start
}

type nativeResourceBuilder struct {
	attrs   map[string]any
	metrics map[string]*nativeMetricBuilder
}

type nativeMetricBuilder struct {
	spec       nativeMetricSpec
	numbers    []otlp.NumberPoint
	histograms []otlp.HistogramPoint
}

// writeNativeMetrics converts the already-rendered scrape state into receiver
// datapoints. The Prometheus state remains authoritative for synthetic values;
// native delta snapshots are tracked independently so the two lanes have no
// shared exporter state or draw-order side effects.
func (c *Construct) writeNativeMetrics(ctx context.Context, now time.Time, w *core.World) error {
	if c.otlpState == nil || w == nil || w.OTLPMetrics == nil {
		return nil
	}
	start := c.otlpState.begin(now)
	resources := make(map[string]*nativeResourceBuilder)

	for _, series := range c.st.Collect(now) {
		spec, ok := nativeMetricByScrape[series.Name]
		if !ok || spec.mode == nativeDeltaHistogramMode {
			continue
		}
		resourceKey, resourceAttrs, pointAttrs := c.nativeResourceAttrs(series.Labels, spec)
		resource := resources[resourceKey]
		if resource == nil {
			resource = &nativeResourceBuilder{attrs: resourceAttrs, metrics: make(map[string]*nativeMetricBuilder)}
			resources[resourceKey] = resource
		}
		metric := resource.metrics[spec.nativeName]
		if metric == nil {
			metric = &nativeMetricBuilder{spec: spec}
			resource.metrics[spec.nativeName] = metric
		}
		value := c.nativeScalarValue(spec, series)
		point := otlp.NumberPoint{Attrs: pointAttrs, Time: now, Value: value}
		if spec.mode == nativeDeltaMode {
			point.Start = start
		}
		metric.numbers = append(metric.numbers, point)
	}

	for _, hist := range c.st.CollectHistos() {
		spec, ok := nativeMetricByScrape[hist.Name]
		if !ok || spec.mode != nativeDeltaHistogramMode {
			continue
		}
		resourceKey, resourceAttrs, pointAttrs := c.nativeResourceAttrs(hist.Labels, spec)
		resource := resources[resourceKey]
		if resource == nil {
			resource = &nativeResourceBuilder{attrs: resourceAttrs, metrics: make(map[string]*nativeMetricBuilder)}
			resources[resourceKey] = resource
		}
		metric := resource.metrics[spec.nativeName]
		if metric == nil {
			metric = &nativeMetricBuilder{spec: spec}
			resource.metrics[spec.nativeName] = metric
		}
		point := c.nativeHistogramPoint(spec, hist, start, now)
		// Use the same attribute projection as scalar points. Keeping it here
		// makes the pointAttrs value explicit even though nativeHistogramPoint
		// recomputes it to preserve the snapshot key contract.
		point.Attrs = pointAttrs
		metric.histograms = append(metric.histograms, point)
	}

	if len(resources) == 0 {
		return nil
	}
	resourceKeys := make([]string, 0, len(resources))
	for key := range resources {
		resourceKeys = append(resourceKeys, key)
	}
	sort.Strings(resourceKeys)
	out := make([]otlp.MetricResource, 0, len(resourceKeys))
	for _, resourceKey := range resourceKeys {
		resource := resources[resourceKey]
		metricNames := make([]string, 0, len(resource.metrics))
		for name := range resource.metrics {
			metricNames = append(metricNames, name)
		}
		sort.Strings(metricNames)
		metrics := make([]otlp.Metric, 0, len(metricNames))
		for _, name := range metricNames {
			builder := resource.metrics[name]
			if len(builder.numbers) == 0 && len(builder.histograms) == 0 {
				continue
			}
			metric := otlp.Metric{
				Name:        builder.spec.nativeName,
				Description: builder.spec.description,
				Unit:        builder.spec.unit,
				Numbers:     builder.numbers,
				Histograms:  builder.histograms,
			}
			switch builder.spec.mode {
			case nativeDeltaHistogramMode:
				metric.Kind = otlp.MetricHistogram
				metric.Temporality = otlp.TemporalityDelta
			case nativeDeltaMode:
				metric.Kind = otlp.MetricSum
				metric.Temporality = otlp.TemporalityDelta
				metric.Monotonic = false
			default:
				metric.Kind = otlp.MetricGauge
			}
			metrics = append(metrics, metric)
		}
		if len(metrics) == 0 {
			continue
		}
		out = append(out, otlp.MetricResource{
			Attrs:              resource.attrs,
			Scope:              otlp.Scope{},
			PreserveEmptyScope: true,
			Metrics:            metrics,
		})
	}
	if len(out) == 0 {
		return nil
	}
	return w.OTLPMetrics.Write(ctx, out)
}

func (c *Construct) nativeScalarValue(spec nativeMetricSpec, series promrw.Series) float64 {
	key := spec.nativeName + "\x00" + state.LabelSig(series.Labels)
	needsDelta := spec.mode == nativeDeltaMode || spec.gaugeFromCounter
	if !needsDelta {
		return series.Value * spec.scale
	}
	previous, seen := c.otlpState.scalarPrev[key]
	c.otlpState.scalarPrev[key] = series.Value
	value := series.Value
	if !seen || series.Value < previous {
		return value * spec.scale
	}
	value -= previous
	return value * spec.scale
}

func (c *Construct) nativeHistogramPoint(spec nativeMetricSpec, hist state.HistoPoint, start, now time.Time) otlp.HistogramPoint {
	key := spec.nativeName + "\x00" + state.LabelSig(hist.Labels)
	previous, seen := c.otlpState.histogramPrev[key]
	current := nativeHistogramSnapshot{
		bounds:       append([]float64(nil), hist.Bounds...),
		bucketCounts: append([]uint64(nil), hist.BucketCounts...),
		sum:          hist.Sum,
		count:        hist.Count,
	}
	c.otlpState.histogramPrev[key] = current
	if seen {
		current.sum = nonNegativeDelta(current.sum, previous.sum)
		current.count = uint64Delta(current.count, previous.count)
		for i := range current.bucketCounts {
			var before uint64
			if i < len(previous.bucketCounts) {
				before = previous.bucketCounts[i]
			}
			current.bucketCounts[i] = uint64Delta(current.bucketCounts[i], before)
		}
	}
	if spec.scale != 1 {
		current.sum *= spec.scale
		for i := range current.bounds {
			current.bounds[i] *= spec.scale
		}
	}
	return otlp.HistogramPoint{
		Attrs:        nativeAnyAttrs(hist.Labels, spec),
		Start:        start,
		Time:         now,
		Sum:          current.sum,
		Count:        current.count,
		Bounds:       append([]float64(nil), current.bounds...),
		BucketCounts: append([]uint64(nil), current.bucketCounts...),
	}
}

func nonNegativeDelta(current, previous float64) float64 {
	if current < previous {
		return current
	}
	return current - previous
}

func uint64Delta(current, previous uint64) uint64 {
	if current < previous {
		return current
	}
	return current - previous
}

func (c *Construct) nativeResourceAttrs(labels map[string]string, spec nativeMetricSpec) (string, map[string]any, map[string]any) {
	resource := map[string]any{"gcp.resource_type": spec.resourceType}
	resourceString := map[string]string{"gcp.resource_type": spec.resourceType}
	for _, key := range spec.resourceKeys {
		if value, ok := labels[key]; ok {
			resource[key] = value
			resourceString[key] = value
		}
	}
	// The receiver keeps user labels on the resource. Preserve the declared
	// environment identity there, as the scrape projection does, so environment
	// instances cannot collapse into one resource. Aggregate declarations omit it.
	if c.env != nil && c.env.Name != "" {
		resource["env"] = c.env.Name
		resourceString["env"] = c.env.Name
	}
	point := nativeAnyAttrs(labels, spec)
	return state.LabelSig(resourceString), resource, point
}

func nativeAnyAttrs(labels map[string]string, spec nativeMetricSpec) map[string]any {
	allowed := make(map[string]bool, len(spec.metricKeys))
	for _, key := range spec.metricKeys {
		allowed[key] = true
	}
	point := make(map[string]any, len(spec.metricKeys))
	for key, value := range labels {
		if allowed[key] {
			point[key] = value
		}
	}
	return point
}
