// SPDX-License-Identifier: AGPL-3.0-only

package cw

// AWS reference pages used for the entries below:
//   - AWS/RDS: Amazon RDS User Guide, "Amazon CloudWatch metrics for Amazon RDS",
//     https://docs.aws.amazon.com/AmazonRDS/latest/UserGuide/rds-metrics.html
//   - AWS/RDS: Amazon RDS User Guide, "Amazon CloudWatch dimensions for Amazon RDS",
//     https://docs.aws.amazon.com/AmazonRDS/latest/UserGuide/dimensions.html
//   - AWS/RDS: Amazon RDS User Guide, "Viewing DB instance metrics in the CloudWatch console and AWS CLI",
//     https://docs.aws.amazon.com/AmazonRDS/latest/UserGuide/metrics_dimensions.html
//   - AWS/DocDB: Amazon DocumentDB Developer Guide, "Monitoring Amazon DocumentDB with CloudWatch",
//     https://docs.aws.amazon.com/documentdb/latest/developerguide/cloud_watch.html
//   - AWS/Neptune: Amazon Neptune User Guide, "Neptune CloudWatch Metrics",
//     https://docs.aws.amazon.com/neptune/latest/userguide/cw-metrics.html
//   - AWS/Neptune: Amazon Neptune User Guide, "Neptune CloudWatch Dimensions",
//     https://docs.aws.amazon.com/neptune/latest/userguide/cw-dimensions.html
//   - AWS/CloudWatch: "CloudWatch metrics supported for resource tags for telemetry",
//     https://docs.aws.amazon.com/AmazonCloudWatch/latest/monitoring/SupportedMetricsForResourceTagsForTelemetry.html
//   - AWS/CloudWatch: "Translations with OpenTelemetry 1.0.0 format in CloudWatch",
//     https://docs.aws.amazon.com/AmazonCloudWatch/latest/monitoring/CloudWatch-metric-streams-formats-opentelemetry-translation-100.html
//
// Context7 checks on 2026-09-05 used /websites/aws_amazon_amazonrds for RDS,
// /websites/aws_amazon_amazoncloudwatch for DocDB's AWS namespace and dimensions, and
// /websites/aws_amazon_neptune_userguide for Neptune. Units below are the AWS Metric Streams
// OTLP table's UCUM translations, not the reference pages' display-unit labels. Entries remain
// absent until their exact AWS name, namespace, unit, and dimensions are verified.
func streamTableRDSFamily() streamTable {
	return streamTable{
		entries: map[string]StreamEntry{
			// AWS/RDS instance metrics.
			"aws_rds_burst_balance":                {Namespace: "AWS/RDS", MetricName: "BurstBalance", Unit: "%"},
			"aws_rds_cpuutilization":               {Namespace: "AWS/RDS", MetricName: "CPUUtilization", Unit: "%"},
			"aws_rds_database_connections":         {Namespace: "AWS/RDS", MetricName: "DatabaseConnections", Unit: "{Count}"},
			"aws_rds_disk_queue_depth":             {Namespace: "AWS/RDS", MetricName: "DiskQueueDepth", Unit: "{Count}"},
			"aws_rds_free_storage_space":           {Namespace: "AWS/RDS", MetricName: "FreeStorageSpace", Unit: "By"},
			"aws_rds_freeable_memory":              {Namespace: "AWS/RDS", MetricName: "FreeableMemory", Unit: "By"},
			"aws_rds_maximum_used_transaction_ids": {Namespace: "AWS/RDS", MetricName: "MaximumUsedTransactionIDs", Unit: "{Count}"},
			"aws_rds_network_receive_throughput":   {Namespace: "AWS/RDS", MetricName: "NetworkReceiveThroughput", Unit: "By/s"},
			"aws_rds_network_transmit_throughput":  {Namespace: "AWS/RDS", MetricName: "NetworkTransmitThroughput", Unit: "By/s"},
			"aws_rds_read_iops":                    {Namespace: "AWS/RDS", MetricName: "ReadIOPS", Unit: "{Count}/s"},
			"aws_rds_read_latency":                 {Namespace: "AWS/RDS", MetricName: "ReadLatency", Unit: "s"},
			"aws_rds_read_throughput":              {Namespace: "AWS/RDS", MetricName: "ReadThroughput", Unit: "By/s"},
			"aws_rds_replication_slot_disk_usage":  {Namespace: "AWS/RDS", MetricName: "ReplicationSlotDiskUsage", Unit: "By"},
			"aws_rds_swap_usage":                   {Namespace: "AWS/RDS", MetricName: "SwapUsage", Unit: "By"},
			"aws_rds_transaction_logs_disk_usage":  {Namespace: "AWS/RDS", MetricName: "TransactionLogsDiskUsage", Unit: "By"},
			"aws_rds_transaction_logs_generation":  {Namespace: "AWS/RDS", MetricName: "TransactionLogsGeneration", Unit: "By/s"},
			"aws_rds_write_iops":                   {Namespace: "AWS/RDS", MetricName: "WriteIOPS", Unit: "{Count}/s"},
			"aws_rds_write_latency":                {Namespace: "AWS/RDS", MetricName: "WriteLatency", Unit: "s"},
			"aws_rds_write_throughput":             {Namespace: "AWS/RDS", MetricName: "WriteThroughput", Unit: "By/s"},

			// AWS/DocDB metrics. ReadLatency and SwapUsage remain intentionally skipped: the
			// current AWS page names them but does not state a unit for either metric.
			"aws_docdb_buffer_cache_hit_ratio": {Namespace: "AWS/DocDB", MetricName: "BufferCacheHitRatio", Unit: "%"},
			"aws_docdb_cpuutilization":         {Namespace: "AWS/DocDB", MetricName: "CPUUtilization", Unit: "%"},
			"aws_docdb_database_connections":   {Namespace: "AWS/DocDB", MetricName: "DatabaseConnections", Unit: "{Count}"},
			"aws_docdb_documents_deleted":      {Namespace: "AWS/DocDB", MetricName: "DocumentsDeleted", Unit: "{Count}"},
			"aws_docdb_documents_inserted":     {Namespace: "AWS/DocDB", MetricName: "DocumentsInserted", Unit: "{Count}"},
			"aws_docdb_documents_returned":     {Namespace: "AWS/DocDB", MetricName: "DocumentsReturned", Unit: "{Count}"},
			"aws_docdb_documents_updated":      {Namespace: "AWS/DocDB", MetricName: "DocumentsUpdated", Unit: "{Count}"},
			"aws_docdb_freeable_memory":        {Namespace: "AWS/DocDB", MetricName: "FreeableMemory", Unit: "By"},
			"aws_docdb_opcounters_command":     {Namespace: "AWS/DocDB", MetricName: "OpcountersCommand", Unit: "{Count}"},
			"aws_docdb_opcounters_delete":      {Namespace: "AWS/DocDB", MetricName: "OpcountersDelete", Unit: "{Count}"},
			"aws_docdb_opcounters_getmore":     {Namespace: "AWS/DocDB", MetricName: "OpcountersGetmore", Unit: "{Count}"},
			"aws_docdb_opcounters_insert":      {Namespace: "AWS/DocDB", MetricName: "OpcountersInsert", Unit: "{Count}"},
			"aws_docdb_opcounters_query":       {Namespace: "AWS/DocDB", MetricName: "OpcountersQuery", Unit: "{Count}"},
			"aws_docdb_opcounters_update":      {Namespace: "AWS/DocDB", MetricName: "OpcountersUpdate", Unit: "{Count}"},
			"aws_docdb_read_iops":              {Namespace: "AWS/DocDB", MetricName: "ReadIOPS", Unit: "{Count}/s"},
			"aws_docdb_write_iops":             {Namespace: "AWS/DocDB", MetricName: "WriteIOPS", Unit: "{Count}/s"},
			"aws_docdb_write_latency":          {Namespace: "AWS/DocDB", MetricName: "WriteLatency", Unit: "ms"},

			// AWS/Neptune metrics.
			"aws_neptune_buffer_cache_hit_ratio":              {Namespace: "AWS/Neptune", MetricName: "BufferCacheHitRatio", Unit: "%"},
			"aws_neptune_cluster_replica_lag_maximum":         {Namespace: "AWS/Neptune", MetricName: "ClusterReplicaLagMaximum", Unit: "ms"},
			"aws_neptune_cpuutilization":                      {Namespace: "AWS/Neptune", MetricName: "CPUUtilization", Unit: "%"},
			"aws_neptune_gremlin_client_errors_per_sec":       {Namespace: "AWS/Neptune", MetricName: "GremlinClientErrorsPerSec", Unit: "{Count}/s"},
			"aws_neptune_gremlin_requests_per_sec":            {Namespace: "AWS/Neptune", MetricName: "GremlinRequestsPerSec", Unit: "{Count}/s"},
			"aws_neptune_gremlin_server_errors_per_sec":       {Namespace: "AWS/Neptune", MetricName: "GremlinServerErrorsPerSec", Unit: "{Count}/s"},
			"aws_neptune_main_request_queue_pending_requests": {Namespace: "AWS/Neptune", MetricName: "MainRequestQueuePendingRequests", Unit: "{Count}"},
			"aws_neptune_num_tx_committed":                    {Namespace: "AWS/Neptune", MetricName: "NumTxCommitted", Unit: "{Count}/s"},
			"aws_neptune_num_tx_opened":                       {Namespace: "AWS/Neptune", MetricName: "NumTxOpened", Unit: "{Count}/s"},
			"aws_neptune_num_tx_rolled_back":                  {Namespace: "AWS/Neptune", MetricName: "NumTxRolledBack", Unit: "{Count}/s"},
			"aws_neptune_total_client_errors_per_sec":         {Namespace: "AWS/Neptune", MetricName: "TotalClientErrorsPerSec", Unit: "{Count}/s"},
			"aws_neptune_total_requests_per_sec":              {Namespace: "AWS/Neptune", MetricName: "TotalRequestsPerSec", Unit: "{Count}/s"},
			"aws_neptune_total_server_errors_per_sec":         {Namespace: "AWS/Neptune", MetricName: "TotalServerErrorsPerSec", Unit: "{Count}/s"},
		},
		dimensions: map[string]map[string]string{
			// The RDS construct emits the DB instance form for these families.
			"aws_rds_burst_balance":                {"dimension_DBInstanceIdentifier": "DBInstanceIdentifier"},
			"aws_rds_cpuutilization":               {"dimension_DBInstanceIdentifier": "DBInstanceIdentifier"},
			"aws_rds_database_connections":         {"dimension_DBInstanceIdentifier": "DBInstanceIdentifier"},
			"aws_rds_disk_queue_depth":             {"dimension_DBInstanceIdentifier": "DBInstanceIdentifier"},
			"aws_rds_free_storage_space":           {"dimension_DBInstanceIdentifier": "DBInstanceIdentifier"},
			"aws_rds_freeable_memory":              {"dimension_DBInstanceIdentifier": "DBInstanceIdentifier"},
			"aws_rds_maximum_used_transaction_ids": {"dimension_DBInstanceIdentifier": "DBInstanceIdentifier"},
			"aws_rds_network_receive_throughput":   {"dimension_DBInstanceIdentifier": "DBInstanceIdentifier"},
			"aws_rds_network_transmit_throughput":  {"dimension_DBInstanceIdentifier": "DBInstanceIdentifier"},
			"aws_rds_read_iops":                    {"dimension_DBInstanceIdentifier": "DBInstanceIdentifier"},
			"aws_rds_read_latency":                 {"dimension_DBInstanceIdentifier": "DBInstanceIdentifier"},
			"aws_rds_read_throughput":              {"dimension_DBInstanceIdentifier": "DBInstanceIdentifier"},
			"aws_rds_replication_slot_disk_usage":  {"dimension_DBInstanceIdentifier": "DBInstanceIdentifier"},
			"aws_rds_swap_usage":                   {"dimension_DBInstanceIdentifier": "DBInstanceIdentifier"},
			"aws_rds_transaction_logs_disk_usage":  {"dimension_DBInstanceIdentifier": "DBInstanceIdentifier"},
			"aws_rds_transaction_logs_generation":  {"dimension_DBInstanceIdentifier": "DBInstanceIdentifier"},
			"aws_rds_write_iops":                   {"dimension_DBInstanceIdentifier": "DBInstanceIdentifier"},
			"aws_rds_write_latency":                {"dimension_DBInstanceIdentifier": "DBInstanceIdentifier"},
			"aws_rds_write_throughput":             {"dimension_DBInstanceIdentifier": "DBInstanceIdentifier"},

			// The DocDB construct emits the documented cluster + role form.
			"aws_docdb_buffer_cache_hit_ratio": {"dimension_DBClusterIdentifier": "DBClusterIdentifier", "dimension_Role": "Role"},
			"aws_docdb_cpuutilization":         {"dimension_DBClusterIdentifier": "DBClusterIdentifier", "dimension_Role": "Role"},
			"aws_docdb_database_connections":   {"dimension_DBClusterIdentifier": "DBClusterIdentifier", "dimension_Role": "Role"},
			"aws_docdb_documents_deleted":      {"dimension_DBClusterIdentifier": "DBClusterIdentifier", "dimension_Role": "Role"},
			"aws_docdb_documents_inserted":     {"dimension_DBClusterIdentifier": "DBClusterIdentifier", "dimension_Role": "Role"},
			"aws_docdb_documents_returned":     {"dimension_DBClusterIdentifier": "DBClusterIdentifier", "dimension_Role": "Role"},
			"aws_docdb_documents_updated":      {"dimension_DBClusterIdentifier": "DBClusterIdentifier", "dimension_Role": "Role"},
			"aws_docdb_freeable_memory":        {"dimension_DBClusterIdentifier": "DBClusterIdentifier", "dimension_Role": "Role"},
			"aws_docdb_opcounters_command":     {"dimension_DBClusterIdentifier": "DBClusterIdentifier", "dimension_Role": "Role"},
			"aws_docdb_opcounters_delete":      {"dimension_DBClusterIdentifier": "DBClusterIdentifier", "dimension_Role": "Role"},
			"aws_docdb_opcounters_getmore":     {"dimension_DBClusterIdentifier": "DBClusterIdentifier", "dimension_Role": "Role"},
			"aws_docdb_opcounters_insert":      {"dimension_DBClusterIdentifier": "DBClusterIdentifier", "dimension_Role": "Role"},
			"aws_docdb_opcounters_query":       {"dimension_DBClusterIdentifier": "DBClusterIdentifier", "dimension_Role": "Role"},
			"aws_docdb_opcounters_update":      {"dimension_DBClusterIdentifier": "DBClusterIdentifier", "dimension_Role": "Role"},
			"aws_docdb_read_iops":              {"dimension_DBClusterIdentifier": "DBClusterIdentifier", "dimension_Role": "Role"},
			"aws_docdb_write_iops":             {"dimension_DBClusterIdentifier": "DBClusterIdentifier", "dimension_Role": "Role"},
			"aws_docdb_write_latency":          {"dimension_DBClusterIdentifier": "DBClusterIdentifier", "dimension_Role": "Role"},

			// The Neptune construct emits the documented cluster + role form.
			"aws_neptune_buffer_cache_hit_ratio":              {"dimension_DBClusterIdentifier": "DBClusterIdentifier", "dimension_Role": "Role"},
			"aws_neptune_cluster_replica_lag_maximum":         {"dimension_DBClusterIdentifier": "DBClusterIdentifier", "dimension_Role": "Role"},
			"aws_neptune_cpuutilization":                      {"dimension_DBClusterIdentifier": "DBClusterIdentifier", "dimension_Role": "Role"},
			"aws_neptune_gremlin_client_errors_per_sec":       {"dimension_DBClusterIdentifier": "DBClusterIdentifier", "dimension_Role": "Role"},
			"aws_neptune_gremlin_requests_per_sec":            {"dimension_DBClusterIdentifier": "DBClusterIdentifier", "dimension_Role": "Role"},
			"aws_neptune_gremlin_server_errors_per_sec":       {"dimension_DBClusterIdentifier": "DBClusterIdentifier", "dimension_Role": "Role"},
			"aws_neptune_main_request_queue_pending_requests": {"dimension_DBClusterIdentifier": "DBClusterIdentifier", "dimension_Role": "Role"},
			"aws_neptune_num_tx_committed":                    {"dimension_DBClusterIdentifier": "DBClusterIdentifier", "dimension_Role": "Role"},
			"aws_neptune_num_tx_opened":                       {"dimension_DBClusterIdentifier": "DBClusterIdentifier", "dimension_Role": "Role"},
			"aws_neptune_num_tx_rolled_back":                  {"dimension_DBClusterIdentifier": "DBClusterIdentifier", "dimension_Role": "Role"},
			"aws_neptune_total_client_errors_per_sec":         {"dimension_DBClusterIdentifier": "DBClusterIdentifier", "dimension_Role": "Role"},
			"aws_neptune_total_requests_per_sec":              {"dimension_DBClusterIdentifier": "DBClusterIdentifier", "dimension_Role": "Role"},
			"aws_neptune_total_server_errors_per_sec":         {"dimension_DBClusterIdentifier": "DBClusterIdentifier", "dimension_Role": "Role"},
		},
	}
}
