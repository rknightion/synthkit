// SPDX-License-Identifier: AGPL-3.0-only

package cw

// AWS reference pages used for the entries below (retrieved through Context7 on 2026-09-05):
//   - ElastiCache host-level names and units:
//     https://docs.aws.amazon.com/AmazonElastiCache/latest/dg/CacheMetrics.HostLevel.html
//   - ElastiCache Valkey/Redis OSS names and units:
//     https://docs.aws.amazon.com/AmazonElastiCache/latest/dg/CacheMetrics.Redis.html
//   - ElastiCache and EC2 exact dimension combinations:
//     https://docs.aws.amazon.com/AmazonCloudWatch/latest/monitoring/SupportedMetricsForResourceTagsForTelemetry.html
//   - OpenSearch Serverless names and dimensions (the page has no unit field):
//     https://docs.aws.amazon.com/opensearch-service/latest/developerguide/monitoring-cloudwatch.html
//   - EC2 names, units, and general dimensions:
//     https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/viewing_metrics_with_cloudwatch.html
//   - CloudWatch Metric Streams OTLP unit translation:
//     https://docs.aws.amazon.com/AmazonCloudWatch/latest/monitoring/CloudWatch-metric-streams-formats-opentelemetry-translation-100.html
//
// Context7 library IDs used once per namespace: ElastiCache via
// /websites/aws_amazon_amazoncloudwatch, OpenSearch Serverless via
// /websites/aws_amazon_opensearch-service_developerguide, and EC2 via
// /websites/aws_amazon_awsec2_userguide.
//
// An entry remains absent until its exact AWS name, namespace, unit, and dimensions are verified
// and its documented unit is present in the AWS OTLP translation table above. Unconfirmed bases:
//   - ElastiCache: aws_elasticache_memory_fragmentation_ratio (documented unit Number) and
//     aws_elasticache_save_in_progress (documented unit Boolean); both are in CacheMetrics.Redis
//     but neither unit appears in the OTLP translation table.
//   - AOSS: aws_aoss_2xx, aws_aoss_4xx, aws_aoss_5xx, aws_aoss_active_collection,
//     aws_aoss_deleted_documents, aws_aoss_indexing_ocu, aws_aoss_ingestion_request_errors,
//     aws_aoss_ingestion_request_latency, aws_aoss_ingestion_request_rate,
//     aws_aoss_ingestion_request_success, aws_aoss_search_ocu, aws_aoss_search_request_errors,
//     aws_aoss_search_request_latency, aws_aoss_search_request_rate,
//     aws_aoss_searchable_documents, and aws_aoss_storage_used_in_s3. The OpenSearch Serverless
//     page supplies names and dimensions but no documented unit for these rows.
//   - EC2: aws_ec2_cpucredit_balance, aws_ec2_cpucredit_usage,
//     aws_ec2_cpusurplus_credit_balance, and aws_ec2_cpusurplus_credits_charged. The EC2 page
//     documents Credits (vCPU-minutes), which is not a unit in the OTLP translation table.
func streamTableCacheSearchEC2() streamTable {
	return streamTable{
		entries: map[string]StreamEntry{
			// ElastiCache (AWS/ElastiCache).
			"aws_elasticache_blocked_connections":              {Namespace: "AWS/ElastiCache", MetricName: "BlockedConnections", Unit: "{Count}"},
			"aws_elasticache_bytes_used_for_cache":             {Namespace: "AWS/ElastiCache", MetricName: "BytesUsedForCache", Unit: "By"},
			"aws_elasticache_cache_hits":                       {Namespace: "AWS/ElastiCache", MetricName: "CacheHits", Unit: "{Count}"},
			"aws_elasticache_cache_misses":                     {Namespace: "AWS/ElastiCache", MetricName: "CacheMisses", Unit: "{Count}"},
			"aws_elasticache_cpuutilization":                   {Namespace: "AWS/ElastiCache", MetricName: "CPUUtilization", Unit: "%"},
			"aws_elasticache_curr_connections":                 {Namespace: "AWS/ElastiCache", MetricName: "CurrConnections", Unit: "{Count}"},
			"aws_elasticache_curr_items":                       {Namespace: "AWS/ElastiCache", MetricName: "CurrItems", Unit: "{Count}"},
			"aws_elasticache_database_memory_usage_percentage": {Namespace: "AWS/ElastiCache", MetricName: "DatabaseMemoryUsagePercentage", Unit: "%"},
			"aws_elasticache_engine_cpuutilization":            {Namespace: "AWS/ElastiCache", MetricName: "EngineCPUUtilization", Unit: "%"},
			"aws_elasticache_error_count":                      {Namespace: "AWS/ElastiCache", MetricName: "ErrorCount", Unit: "{Count}"},
			"aws_elasticache_evictions":                        {Namespace: "AWS/ElastiCache", MetricName: "Evictions", Unit: "{Count}"},
			"aws_elasticache_freeable_memory":                  {Namespace: "AWS/ElastiCache", MetricName: "FreeableMemory", Unit: "By"},
			"aws_elasticache_is_master":                        {Namespace: "AWS/ElastiCache", MetricName: "IsMaster", Unit: "{Count}"},
			"aws_elasticache_network_bytes_in":                 {Namespace: "AWS/ElastiCache", MetricName: "NetworkBytesIn", Unit: "By"},
			"aws_elasticache_network_bytes_out":                {Namespace: "AWS/ElastiCache", MetricName: "NetworkBytesOut", Unit: "By"},
			"aws_elasticache_new_connections":                  {Namespace: "AWS/ElastiCache", MetricName: "NewConnections", Unit: "{Count}"},
			"aws_elasticache_processed_commands":               {Namespace: "AWS/ElastiCache", MetricName: "ProcessedCommands", Unit: "{Count}"},
			"aws_elasticache_reclaimed":                        {Namespace: "AWS/ElastiCache", MetricName: "Reclaimed", Unit: "{Count}"},
			"aws_elasticache_replication_bytes":                {Namespace: "AWS/ElastiCache", MetricName: "ReplicationBytes", Unit: "By"},
			"aws_elasticache_replication_lag":                  {Namespace: "AWS/ElastiCache", MetricName: "ReplicationLag", Unit: "s"},
			"aws_elasticache_set_type_cmds":                    {Namespace: "AWS/ElastiCache", MetricName: "SetTypeCmds", Unit: "{Count}"},
			"aws_elasticache_swap_usage":                       {Namespace: "AWS/ElastiCache", MetricName: "SwapUsage", Unit: "By"},

			// AOSS (AWS/AOSS) is intentionally empty: the AWS reference page does not document units.

			// EC2 (AWS/EC2). Preserve the pre-existing CPUUtilization mapping exactly.
			"aws_ec2_cpuutilization":                        {Namespace: "AWS/EC2", MetricName: "CPUUtilization", Unit: "%"},
			"aws_ec2_ebsbyte_balance_percent":               {Namespace: "AWS/EC2", MetricName: "EBSByteBalance%", Unit: "%"},
			"aws_ec2_ebsiobalance_percent":                  {Namespace: "AWS/EC2", MetricName: "EBSIOBalance%", Unit: "%"},
			"aws_ec2_ebsread_bytes":                         {Namespace: "AWS/EC2", MetricName: "EBSReadBytes", Unit: "By"},
			"aws_ec2_ebsread_ops":                           {Namespace: "AWS/EC2", MetricName: "EBSReadOps", Unit: "{Count}"},
			"aws_ec2_ebswrite_bytes":                        {Namespace: "AWS/EC2", MetricName: "EBSWriteBytes", Unit: "By"},
			"aws_ec2_ebswrite_ops":                          {Namespace: "AWS/EC2", MetricName: "EBSWriteOps", Unit: "{Count}"},
			"aws_ec2_instance_ebsiopsexceeded_check":        {Namespace: "AWS/EC2", MetricName: "InstanceEBSIOPSExceededCheck", Unit: "1"},
			"aws_ec2_instance_ebsthroughput_exceeded_check": {Namespace: "AWS/EC2", MetricName: "InstanceEBSThroughputExceededCheck", Unit: "1"},
			"aws_ec2_metadata_no_token":                     {Namespace: "AWS/EC2", MetricName: "MetadataNoToken", Unit: "{Count}"},
			"aws_ec2_metadata_no_token_rejected":            {Namespace: "AWS/EC2", MetricName: "MetadataNoTokenRejected", Unit: "{Count}"},
			"aws_ec2_network_in":                            {Namespace: "AWS/EC2", MetricName: "NetworkIn", Unit: "By"},
			"aws_ec2_network_out":                           {Namespace: "AWS/EC2", MetricName: "NetworkOut", Unit: "By"},
			"aws_ec2_network_packets_in":                    {Namespace: "AWS/EC2", MetricName: "NetworkPacketsIn", Unit: "{Count}"},
			"aws_ec2_network_packets_out":                   {Namespace: "AWS/EC2", MetricName: "NetworkPacketsOut", Unit: "{Count}"},
			"aws_ec2_status_check_failed":                   {Namespace: "AWS/EC2", MetricName: "StatusCheckFailed", Unit: "{Count}"},
			"aws_ec2_status_check_failed_attached_ebs":      {Namespace: "AWS/EC2", MetricName: "StatusCheckFailed_AttachedEBS", Unit: "{Count}"},
			"aws_ec2_status_check_failed_instance":          {Namespace: "AWS/EC2", MetricName: "StatusCheckFailed_Instance", Unit: "{Count}"},
			"aws_ec2_status_check_failed_system":            {Namespace: "AWS/EC2", MetricName: "StatusCheckFailed_System", Unit: "{Count}"},
		},
		dimensions: map[string]map[string]string{
			// Per-node ElastiCache forms use the exact CacheClusterId + CacheNodeId pair. The
			// documented data-tiered form may additionally carry Tier when that label exists.
			"aws_elasticache_blocked_connections": {
				"dimension_CacheClusterId": "CacheClusterId",
				"dimension_CacheNodeId":    "CacheNodeId",
			},
			"aws_elasticache_bytes_used_for_cache": {
				"dimension_CacheClusterId": "CacheClusterId",
				"dimension_CacheNodeId":    "CacheNodeId",
				"dimension_Tier":           "Tier",
			},
			"aws_elasticache_cache_hits": {
				"dimension_CacheClusterId": "CacheClusterId",
				"dimension_CacheNodeId":    "CacheNodeId",
			},
			"aws_elasticache_cache_misses": {
				"dimension_CacheClusterId": "CacheClusterId",
				"dimension_CacheNodeId":    "CacheNodeId",
			},
			"aws_elasticache_cpuutilization": {
				"dimension_CacheClusterId": "CacheClusterId",
				"dimension_CacheNodeId":    "CacheNodeId",
			},
			"aws_elasticache_curr_connections": {
				"dimension_CacheClusterId": "CacheClusterId",
				"dimension_CacheNodeId":    "CacheNodeId",
			},
			"aws_elasticache_curr_items": {
				"dimension_CacheClusterId": "CacheClusterId",
				"dimension_CacheNodeId":    "CacheNodeId",
				"dimension_Tier":           "Tier",
			},
			"aws_elasticache_database_memory_usage_percentage": {
				"dimension_CacheClusterId": "CacheClusterId",
				"dimension_CacheNodeId":    "CacheNodeId",
			},
			"aws_elasticache_engine_cpuutilization": {
				"dimension_CacheClusterId": "CacheClusterId",
				"dimension_CacheNodeId":    "CacheNodeId",
			},
			"aws_elasticache_error_count": {
				"dimension_CacheClusterId": "CacheClusterId",
				"dimension_CacheNodeId":    "CacheNodeId",
			},
			"aws_elasticache_evictions": {
				"dimension_CacheClusterId": "CacheClusterId",
				"dimension_CacheNodeId":    "CacheNodeId",
			},
			"aws_elasticache_freeable_memory": {
				"dimension_CacheClusterId": "CacheClusterId",
				"dimension_CacheNodeId":    "CacheNodeId",
			},
			"aws_elasticache_is_master": {
				"dimension_CacheClusterId": "CacheClusterId",
				"dimension_CacheNodeId":    "CacheNodeId",
			},
			"aws_elasticache_network_bytes_in": {
				"dimension_CacheClusterId": "CacheClusterId",
				"dimension_CacheNodeId":    "CacheNodeId",
			},
			"aws_elasticache_network_bytes_out": {
				"dimension_CacheClusterId": "CacheClusterId",
				"dimension_CacheNodeId":    "CacheNodeId",
			},
			"aws_elasticache_new_connections": {
				"dimension_CacheClusterId": "CacheClusterId",
				"dimension_CacheNodeId":    "CacheNodeId",
			},
			"aws_elasticache_processed_commands": {
				"dimension_CacheClusterId": "CacheClusterId",
				"dimension_CacheNodeId":    "CacheNodeId",
			},
			"aws_elasticache_reclaimed": {
				"dimension_CacheClusterId": "CacheClusterId",
				"dimension_CacheNodeId":    "CacheNodeId",
			},
			"aws_elasticache_replication_bytes": {
				"dimension_CacheClusterId": "CacheClusterId",
				"dimension_CacheNodeId":    "CacheNodeId",
			},
			"aws_elasticache_replication_lag": {
				"dimension_CacheClusterId": "CacheClusterId",
				"dimension_CacheNodeId":    "CacheNodeId",
			},
			"aws_elasticache_set_type_cmds": {
				"dimension_CacheClusterId": "CacheClusterId",
				"dimension_CacheNodeId":    "CacheNodeId",
			},
			"aws_elasticache_swap_usage": {
				"dimension_CacheClusterId": "CacheClusterId",
				"dimension_CacheNodeId":    "CacheNodeId",
			},

			// EC2 forms follow the documented instance/ASG split already captured by the
			// construct. The three instance-only families retain only InstanceId.
			"aws_ec2_cpuutilization": {
				"dimension_AutoScalingGroupName": "AutoScalingGroupName",
				"dimension_InstanceId":           "InstanceId",
			},
			"aws_ec2_ebsbyte_balance_percent": {
				"dimension_AutoScalingGroupName": "AutoScalingGroupName",
				"dimension_InstanceId":           "InstanceId",
			},
			"aws_ec2_ebsiobalance_percent": {
				"dimension_AutoScalingGroupName": "AutoScalingGroupName",
				"dimension_InstanceId":           "InstanceId",
			},
			"aws_ec2_ebsread_bytes": {
				"dimension_AutoScalingGroupName": "AutoScalingGroupName",
				"dimension_InstanceId":           "InstanceId",
			},
			"aws_ec2_ebsread_ops": {
				"dimension_AutoScalingGroupName": "AutoScalingGroupName",
				"dimension_InstanceId":           "InstanceId",
			},
			"aws_ec2_ebswrite_bytes": {
				"dimension_AutoScalingGroupName": "AutoScalingGroupName",
				"dimension_InstanceId":           "InstanceId",
			},
			"aws_ec2_ebswrite_ops": {
				"dimension_AutoScalingGroupName": "AutoScalingGroupName",
				"dimension_InstanceId":           "InstanceId",
			},
			"aws_ec2_instance_ebsiopsexceeded_check": {
				"dimension_InstanceId": "InstanceId",
			},
			"aws_ec2_instance_ebsthroughput_exceeded_check": {
				"dimension_InstanceId": "InstanceId",
			},
			"aws_ec2_metadata_no_token": {
				"dimension_AutoScalingGroupName": "AutoScalingGroupName",
				"dimension_InstanceId":           "InstanceId",
			},
			"aws_ec2_metadata_no_token_rejected": {
				"dimension_InstanceId": "InstanceId",
			},
			"aws_ec2_network_in": {
				"dimension_AutoScalingGroupName": "AutoScalingGroupName",
				"dimension_InstanceId":           "InstanceId",
			},
			"aws_ec2_network_out": {
				"dimension_AutoScalingGroupName": "AutoScalingGroupName",
				"dimension_InstanceId":           "InstanceId",
			},
			"aws_ec2_network_packets_in": {
				"dimension_AutoScalingGroupName": "AutoScalingGroupName",
				"dimension_InstanceId":           "InstanceId",
			},
			"aws_ec2_network_packets_out": {
				"dimension_AutoScalingGroupName": "AutoScalingGroupName",
				"dimension_InstanceId":           "InstanceId",
			},
			"aws_ec2_status_check_failed": {
				"dimension_AutoScalingGroupName": "AutoScalingGroupName",
				"dimension_InstanceId":           "InstanceId",
			},
			"aws_ec2_status_check_failed_attached_ebs": {
				"dimension_AutoScalingGroupName": "AutoScalingGroupName",
				"dimension_InstanceId":           "InstanceId",
			},
			"aws_ec2_status_check_failed_instance": {
				"dimension_AutoScalingGroupName": "AutoScalingGroupName",
				"dimension_InstanceId":           "InstanceId",
			},
			"aws_ec2_status_check_failed_system": {
				"dimension_AutoScalingGroupName": "AutoScalingGroupName",
				"dimension_InstanceId":           "InstanceId",
			},
		},
	}
}
