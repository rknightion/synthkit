// SPDX-License-Identifier: AGPL-3.0-only

package cw

// AWS reference pages used for the entries below (retrieved through Context7 on 2026-09-05):
//   - AWS/MWAA: Container, queue, and database metrics for Amazon MWAA,
//     https://docs.aws.amazon.com/mwaa/latest/userguide/accessing-metrics-cw-container-queue-db.html
//   - AmazonMWAA: Apache Airflow environment metrics in CloudWatch,
//     https://docs.aws.amazon.com/mwaa/latest/userguide/access-metrics-cw.html
//   - AmazonMWAA dashboard cross-check,
//     https://docs.aws.amazon.com/mwaa/latest/userguide/monitoring-dashboard.html
//   - Glue: Monitoring AWS Glue using Amazon CloudWatch metrics,
//     https://docs.aws.amazon.com/glue/latest/dg/monitoring-awsglue-with-cloudwatch-metrics.html
//   - Metric-stream unit translation,
//     https://docs.aws.amazon.com/AmazonCloudWatch/latest/monitoring/CloudWatch-metric-streams-formats-opentelemetry-translation-100.html
//
// The source catalogue starts with 48 MWAA bases (17 AWS/MWAA and 31 AmazonMWAA) and 20 Glue
// bases in signals/cw.md [slug: cw-metric-stream-otlp]. The pages verify 11 AWS/MWAA bases,
// 28 AmazonMWAA bases, and all 20 Glue bases. Remaining MWAA skips are deliberately listed here:
//   - AWS/MWAA (6): active_connection_count, network_receive_throughput,
//     network_transmit_throughput, read_iops, read_latency, read_throughput.
//   - AmazonMWAA (3): celery_worker_heartbeat, file_path_queue_size, triggerer_heartbeat.
//     CeleryWorkerHeartbeat appears only in the dashboard summary without its unit/dimensions;
//     FilePathQueueSize is absent from the current metric table; and the current pages disagree
//     between TriggerHeartbeat and TriggererHeartbeat.
//
// Glue's Percentage wording is the documented Percent unit and is translated to `%` by the
// AWS Metric Streams table; all other units use that table verbatim.
//
// streamTableDataPipelines owns the AWS/MWAA, AmazonMWAA, and Glue namespaces. Entries remain
// absent until their exact AWS name, namespace, unit, and dimensions are cited.
func streamTableDataPipelines() streamTable {
	return streamTable{
		entries: map[string]StreamEntry{
			"aws_mwaa_approximate_age_of_oldest_task": {Namespace: "AWS/MWAA", MetricName: "ApproximateAgeOfOldestTask", Unit: "s"},
			"aws_mwaa_cpuutilization":                 {Namespace: "AWS/MWAA", MetricName: "CPUUtilization", Unit: "%"},
			"aws_mwaa_database_connections":           {Namespace: "AWS/MWAA", MetricName: "DatabaseConnections", Unit: "{Count}"},
			"aws_mwaa_disk_queue_depth":               {Namespace: "AWS/MWAA", MetricName: "DiskQueueDepth", Unit: "{Count}"},
			"aws_mwaa_freeable_memory":                {Namespace: "AWS/MWAA", MetricName: "FreeableMemory", Unit: "By"},
			"aws_mwaa_memory_utilization":             {Namespace: "AWS/MWAA", MetricName: "MemoryUtilization", Unit: "%"},
			"aws_mwaa_queued_tasks":                   {Namespace: "AWS/MWAA", MetricName: "QueuedTasks", Unit: "{Count}"},
			"aws_mwaa_running_tasks":                  {Namespace: "AWS/MWAA", MetricName: "RunningTasks", Unit: "{Count}"},
			"aws_mwaa_write_iops":                     {Namespace: "AWS/MWAA", MetricName: "WriteIOPS", Unit: "{Count}/s"},
			"aws_mwaa_write_latency":                  {Namespace: "AWS/MWAA", MetricName: "WriteLatency", Unit: "s"},
			"aws_mwaa_write_throughput":               {Namespace: "AWS/MWAA", MetricName: "WriteThroughput", Unit: "By/s"},

			"aws_amazonmwaa_critical_section_busy":                     {Namespace: "AmazonMWAA", MetricName: "CriticalSectionBusy", Unit: "{Count}"},
			"aws_amazonmwaa_critical_section_duration":                 {Namespace: "AmazonMWAA", MetricName: "CriticalSectionDuration", Unit: "ms"},
			"aws_amazonmwaa_critical_section_query_duration":           {Namespace: "AmazonMWAA", MetricName: "CriticalSectionQueryDuration", Unit: "ms"},
			"aws_amazonmwaa_dag_bag_size":                              {Namespace: "AmazonMWAA", MetricName: "DagBagSize", Unit: "{Count}"},
			"aws_amazonmwaa_dagfile_processing_last_duration":          {Namespace: "AmazonMWAA", MetricName: "DAGFileProcessingLastDuration", Unit: "s"},
			"aws_amazonmwaa_dagfile_processing_last_num_of_db_queries": {Namespace: "AmazonMWAA", MetricName: "DAGFileProcessingLastNumOfDbQueries", Unit: "{Count}"},
			"aws_amazonmwaa_dagfile_processing_last_run_seconds_ago":   {Namespace: "AmazonMWAA", MetricName: "DAGFileProcessingLastRunSecondsAgo", Unit: "s"},
			"aws_amazonmwaa_file_path_queue_update_count":              {Namespace: "AmazonMWAA", MetricName: "FilePathQueueUpdateCount", Unit: "{Count}"},
			"aws_amazonmwaa_import_errors":                             {Namespace: "AmazonMWAA", MetricName: "ImportErrors", Unit: "{Count}"},
			"aws_amazonmwaa_job_end":                                   {Namespace: "AmazonMWAA", MetricName: "JobEnd", Unit: "{Count}"},
			"aws_amazonmwaa_open_slots":                                {Namespace: "AmazonMWAA", MetricName: "OpenSlots", Unit: "{Count}"},
			"aws_amazonmwaa_orphaned":                                  {Namespace: "AmazonMWAA", MetricName: "Orphaned", Unit: "{Count}"},
			"aws_amazonmwaa_orphaned_tasks_adopted":                    {Namespace: "AmazonMWAA", MetricName: "OrphanedTasksAdopted", Unit: "{Count}"},
			"aws_amazonmwaa_orphaned_tasks_cleared":                    {Namespace: "AmazonMWAA", MetricName: "OrphanedTasksCleared", Unit: "{Count}"},
			"aws_amazonmwaa_pool_deferred_slots":                       {Namespace: "AmazonMWAA", MetricName: "PoolDeferredSlots", Unit: "{Count}"},
			"aws_amazonmwaa_pool_open_slots":                           {Namespace: "AmazonMWAA", MetricName: "PoolOpenSlots", Unit: "{Count}"},
			"aws_amazonmwaa_pool_queued_slots":                         {Namespace: "AmazonMWAA", MetricName: "PoolQueuedSlots", Unit: "{Count}"},
			"aws_amazonmwaa_pool_running_slots":                        {Namespace: "AmazonMWAA", MetricName: "PoolRunningSlots", Unit: "{Count}"},
			"aws_amazonmwaa_pool_scheduled_slots":                      {Namespace: "AmazonMWAA", MetricName: "PoolScheduledSlots", Unit: "{Count}"},
			"aws_amazonmwaa_processes":                                 {Namespace: "AmazonMWAA", MetricName: "Processes", Unit: "{Count}"},
			"aws_amazonmwaa_queued_tasks":                              {Namespace: "AmazonMWAA", MetricName: "QueuedTasks", Unit: "{Count}"},
			"aws_amazonmwaa_running_tasks":                             {Namespace: "AmazonMWAA", MetricName: "RunningTasks", Unit: "{Count}"},
			"aws_amazonmwaa_scheduler_heartbeat":                       {Namespace: "AmazonMWAA", MetricName: "SchedulerHeartbeat", Unit: "{Count}"},
			"aws_amazonmwaa_scheduler_loop_duration":                   {Namespace: "AmazonMWAA", MetricName: "SchedulerLoopDuration", Unit: "ms"},
			"aws_amazonmwaa_tasks_executable":                          {Namespace: "AmazonMWAA", MetricName: "TasksExecutable", Unit: "{Count}"},
			"aws_amazonmwaa_tasks_starving":                            {Namespace: "AmazonMWAA", MetricName: "TasksStarving", Unit: "{Count}"},
			"aws_amazonmwaa_total_parse_time":                          {Namespace: "AmazonMWAA", MetricName: "TotalParseTime", Unit: "s"},
			"aws_amazonmwaa_triggers_running":                          {Namespace: "AmazonMWAA", MetricName: "TriggersRunning", Unit: "{Count}"},

			"aws_glue_all_jvm_heap_usage":                           {Namespace: "Glue", MetricName: "glue.ALL.jvm.heap.usage", Unit: "%"},
			"aws_glue_all_jvm_heap_used":                            {Namespace: "Glue", MetricName: "glue.ALL.jvm.heap.used", Unit: "By"},
			"aws_glue_all_s3_filesystem_read_bytes":                 {Namespace: "Glue", MetricName: "glue.ALL.s3.filesystem.read_bytes", Unit: "By"},
			"aws_glue_all_s3_filesystem_write_bytes":                {Namespace: "Glue", MetricName: "glue.ALL.s3.filesystem.write_bytes", Unit: "By"},
			"aws_glue_all_system_cpu_system_load":                   {Namespace: "Glue", MetricName: "glue.ALL.system.cpuSystemLoad", Unit: "%"},
			"aws_glue_driver_aggregate_bytes_read":                  {Namespace: "Glue", MetricName: "glue.driver.aggregate.bytesRead", Unit: "By"},
			"aws_glue_driver_aggregate_elapsed_time":                {Namespace: "Glue", MetricName: "glue.driver.aggregate.elapsedTime", Unit: "ms"},
			"aws_glue_driver_aggregate_num_completed_stages":        {Namespace: "Glue", MetricName: "glue.driver.aggregate.numCompletedStages", Unit: "{Count}"},
			"aws_glue_driver_aggregate_num_completed_tasks":         {Namespace: "Glue", MetricName: "glue.driver.aggregate.numCompletedTasks", Unit: "{Count}"},
			"aws_glue_driver_aggregate_num_failed_tasks":            {Namespace: "Glue", MetricName: "glue.driver.aggregate.numFailedTasks", Unit: "{Count}"},
			"aws_glue_driver_aggregate_num_killed_tasks":            {Namespace: "Glue", MetricName: "glue.driver.aggregate.numKilledTasks", Unit: "{Count}"},
			"aws_glue_driver_aggregate_records_read":                {Namespace: "Glue", MetricName: "glue.driver.aggregate.recordsRead", Unit: "{Count}"},
			"aws_glue_driver_aggregate_shuffle_bytes_written":       {Namespace: "Glue", MetricName: "glue.driver.aggregate.shuffleBytesWritten", Unit: "By"},
			"aws_glue_driver_aggregate_shuffle_local_bytes_read":    {Namespace: "Glue", MetricName: "glue.driver.aggregate.shuffleLocalBytesRead", Unit: "By"},
			"aws_glue_driver_block_manager_disk_disk_space_used_mb": {Namespace: "Glue", MetricName: "glue.driver.BlockManager.disk.diskSpaceUsed_MB", Unit: "MBy"},
			"aws_glue_driver_jvm_heap_usage":                        {Namespace: "Glue", MetricName: "glue.driver.jvm.heap.usage", Unit: "%"},
			"aws_glue_driver_jvm_heap_used":                         {Namespace: "Glue", MetricName: "glue.driver.jvm.heap.used", Unit: "By"},
			"aws_glue_driver_s3_filesystem_read_bytes":              {Namespace: "Glue", MetricName: "glue.driver.s3.filesystem.read_bytes", Unit: "By"},
			"aws_glue_driver_s3_filesystem_write_bytes":             {Namespace: "Glue", MetricName: "glue.driver.s3.filesystem.write_bytes", Unit: "By"},
			"aws_glue_driver_system_cpu_system_load":                {Namespace: "Glue", MetricName: "glue.driver.system.cpuSystemLoad", Unit: "%"},
		},
		dimensions: map[string]map[string]string{
			"aws_mwaa_approximate_age_of_oldest_task": {
				"dimension_Environment": "Environment",
				"dimension_Queue":       "Queue",
			},
			"aws_mwaa_cpuutilization": {
				"dimension_Environment": "Environment",
				"dimension_Cluster":     "Cluster",
			},
			"aws_mwaa_database_connections": {
				"dimension_Environment":  "Environment",
				"dimension_DatabaseRole": "Database",
			},
			"aws_mwaa_disk_queue_depth": {
				"dimension_Environment":  "Environment",
				"dimension_DatabaseRole": "Database",
			},
			"aws_mwaa_freeable_memory": {
				"dimension_Environment":  "Environment",
				"dimension_DatabaseRole": "Database",
			},
			"aws_mwaa_memory_utilization": {
				"dimension_Environment": "Environment",
				"dimension_Cluster":     "Cluster",
			},
			"aws_mwaa_queued_tasks": {
				"dimension_Environment": "Environment",
				"dimension_Queue":       "Queue",
			},
			"aws_mwaa_running_tasks": {
				"dimension_Environment": "Environment",
				"dimension_Queue":       "Queue",
			},
			"aws_mwaa_write_iops": {
				"dimension_Environment":  "Environment",
				"dimension_DatabaseRole": "Database",
			},
			"aws_mwaa_write_latency": {
				"dimension_Environment":  "Environment",
				"dimension_DatabaseRole": "Database",
			},
			"aws_mwaa_write_throughput": {
				"dimension_Environment":  "Environment",
				"dimension_DatabaseRole": "Database",
			},

			"aws_amazonmwaa_critical_section_busy": {
				"dimension_Environment": "Environment",
				"dimension_Function":    "Function",
			},
			"aws_amazonmwaa_critical_section_duration": {
				"dimension_Environment": "Environment",
				"dimension_Function":    "Function",
			},
			"aws_amazonmwaa_critical_section_query_duration": {
				"dimension_Environment": "Environment",
				"dimension_Function":    "Function",
			},
			"aws_amazonmwaa_dag_bag_size": {
				"dimension_Environment": "Environment",
				"dimension_Function":    "Function",
			},
			"aws_amazonmwaa_dagfile_processing_last_duration": {
				"dimension_Environment":  "Environment",
				"dimension_DAG_Filename": "DAG Filename",
			},
			"aws_amazonmwaa_dagfile_processing_last_num_of_db_queries": {
				"dimension_Environment":  "Environment",
				"dimension_DAG_Filename": "DAG Filename",
			},
			"aws_amazonmwaa_dagfile_processing_last_run_seconds_ago": {
				"dimension_Environment":  "Environment",
				"dimension_DAG_Filename": "DAG Filename",
			},
			"aws_amazonmwaa_file_path_queue_update_count": {
				"dimension_Environment": "Environment",
				"dimension_Function":    "Function",
			},
			"aws_amazonmwaa_import_errors": {
				"dimension_Environment": "Environment",
				"dimension_Function":    "Function",
			},
			"aws_amazonmwaa_job_end": {
				"dimension_Environment": "Environment",
				"dimension_Job":         "Job",
			},
			"aws_amazonmwaa_open_slots": {
				"dimension_Environment": "Environment",
				"dimension_Function":    "Function",
			},
			"aws_amazonmwaa_orphaned": {
				"dimension_Environment": "Environment",
				"dimension_Function":    "Function",
			},
			"aws_amazonmwaa_orphaned_tasks_adopted": {
				"dimension_Environment": "Environment",
				"dimension_Function":    "Function",
			},
			"aws_amazonmwaa_orphaned_tasks_cleared": {
				"dimension_Environment": "Environment",
				"dimension_Function":    "Function",
			},
			"aws_amazonmwaa_pool_deferred_slots": {
				"dimension_Environment": "Environment",
				"dimension_Pool":        "Pool",
			},
			"aws_amazonmwaa_pool_open_slots": {
				"dimension_Environment": "Environment",
				"dimension_Pool":        "Pool",
			},
			"aws_amazonmwaa_pool_queued_slots": {
				"dimension_Environment": "Environment",
				"dimension_Pool":        "Pool",
			},
			"aws_amazonmwaa_pool_running_slots": {
				"dimension_Environment": "Environment",
				"dimension_Pool":        "Pool",
			},
			"aws_amazonmwaa_pool_scheduled_slots": {
				"dimension_Environment": "Environment",
				"dimension_Pool":        "Pool",
			},
			"aws_amazonmwaa_processes": {
				"dimension_Environment": "Environment",
				"dimension_Function":    "Function",
			},
			"aws_amazonmwaa_queued_tasks": {
				"dimension_Environment": "Environment",
				"dimension_Function":    "Function",
			},
			"aws_amazonmwaa_running_tasks": {
				"dimension_Environment": "Environment",
				"dimension_Function":    "Function",
			},
			"aws_amazonmwaa_scheduler_heartbeat": {
				"dimension_Environment": "Environment",
				"dimension_Function":    "Function",
			},
			"aws_amazonmwaa_scheduler_loop_duration": {
				"dimension_Environment": "Environment",
				"dimension_Function":    "Function",
			},
			"aws_amazonmwaa_tasks_executable": {
				"dimension_Environment": "Environment",
				"dimension_Function":    "Function",
			},
			"aws_amazonmwaa_tasks_starving": {
				"dimension_Environment": "Environment",
				"dimension_Function":    "Function",
			},
			"aws_amazonmwaa_total_parse_time": {
				"dimension_Environment": "Environment",
				"dimension_Function":    "Function",
			},
			"aws_amazonmwaa_triggers_running": {
				"dimension_Environment": "Environment",
				"dimension_Function":    "Function",
				"dimension_HostName":    "HostName",
			},

			"aws_glue_all_jvm_heap_usage": {
				"dimension_JobName":  "JobName",
				"dimension_JobRunId": "JobRunId",
				"dimension_Type":     "Type",
			},
			"aws_glue_all_jvm_heap_used": {
				"dimension_JobName":  "JobName",
				"dimension_JobRunId": "JobRunId",
				"dimension_Type":     "Type",
			},
			"aws_glue_all_s3_filesystem_read_bytes": {
				"dimension_JobName":  "JobName",
				"dimension_JobRunId": "JobRunId",
				"dimension_Type":     "Type",
			},
			"aws_glue_all_s3_filesystem_write_bytes": {
				"dimension_JobName":  "JobName",
				"dimension_JobRunId": "JobRunId",
				"dimension_Type":     "Type",
			},
			"aws_glue_all_system_cpu_system_load": {
				"dimension_JobName":  "JobName",
				"dimension_JobRunId": "JobRunId",
				"dimension_Type":     "Type",
			},
			"aws_glue_driver_aggregate_bytes_read": {
				"dimension_JobName":  "JobName",
				"dimension_JobRunId": "JobRunId",
				"dimension_Type":     "Type",
			},
			"aws_glue_driver_aggregate_elapsed_time": {
				"dimension_JobName":  "JobName",
				"dimension_JobRunId": "JobRunId",
				"dimension_Type":     "Type",
			},
			"aws_glue_driver_aggregate_num_completed_stages": {
				"dimension_JobName":  "JobName",
				"dimension_JobRunId": "JobRunId",
				"dimension_Type":     "Type",
			},
			"aws_glue_driver_aggregate_num_completed_tasks": {
				"dimension_JobName":  "JobName",
				"dimension_JobRunId": "JobRunId",
				"dimension_Type":     "Type",
			},
			"aws_glue_driver_aggregate_num_failed_tasks": {
				"dimension_JobName":  "JobName",
				"dimension_JobRunId": "JobRunId",
				"dimension_Type":     "Type",
			},
			"aws_glue_driver_aggregate_num_killed_tasks": {
				"dimension_JobName":  "JobName",
				"dimension_JobRunId": "JobRunId",
				"dimension_Type":     "Type",
			},
			"aws_glue_driver_aggregate_records_read": {
				"dimension_JobName":  "JobName",
				"dimension_JobRunId": "JobRunId",
				"dimension_Type":     "Type",
			},
			"aws_glue_driver_aggregate_shuffle_bytes_written": {
				"dimension_JobName":  "JobName",
				"dimension_JobRunId": "JobRunId",
				"dimension_Type":     "Type",
			},
			"aws_glue_driver_aggregate_shuffle_local_bytes_read": {
				"dimension_JobName":  "JobName",
				"dimension_JobRunId": "JobRunId",
				"dimension_Type":     "Type",
			},
			"aws_glue_driver_block_manager_disk_disk_space_used_mb": {
				"dimension_JobName":  "JobName",
				"dimension_JobRunId": "JobRunId",
				"dimension_Type":     "Type",
			},
			"aws_glue_driver_jvm_heap_usage": {
				"dimension_JobName":  "JobName",
				"dimension_JobRunId": "JobRunId",
				"dimension_Type":     "Type",
			},
			"aws_glue_driver_jvm_heap_used": {
				"dimension_JobName":  "JobName",
				"dimension_JobRunId": "JobRunId",
				"dimension_Type":     "Type",
			},
			"aws_glue_driver_s3_filesystem_read_bytes": {
				"dimension_JobName":  "JobName",
				"dimension_JobRunId": "JobRunId",
				"dimension_Type":     "Type",
			},
			"aws_glue_driver_s3_filesystem_write_bytes": {
				"dimension_JobName":  "JobName",
				"dimension_JobRunId": "JobRunId",
				"dimension_Type":     "Type",
			},
			"aws_glue_driver_system_cpu_system_load": {
				"dimension_JobName":  "JobName",
				"dimension_JobRunId": "JobRunId",
				"dimension_Type":     "Type",
			},
		},
	}
}
