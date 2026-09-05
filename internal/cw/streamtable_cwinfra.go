// SPDX-License-Identifier: AGPL-3.0-only

package cw

// AWS reference pages used for the entries below (retrieved through Context7 on 2026-09-05):
//   - Application Load Balancer metric names, units, and dimension forms:
//     https://docs.aws.amazon.com/elasticloadbalancing/latest/application/load-balancer-cloudwatch-metrics.html
//   - Network Load Balancer metric names, units, and dimension forms:
//     https://docs.aws.amazon.com/elasticloadbalancing/latest/network/load-balancer-cloudwatch-metrics.html
//   - EBS metric names, units, and dimension forms:
//     https://docs.aws.amazon.com/ebs/latest/userguide/using_cloudwatch_ebs.html
//   - EKS control-plane metric names and units:
//     https://docs.aws.amazon.com/eks/latest/userguide/cloudwatch.html
//   - Amazon Data Firehose metric names and units:
//     https://docs.aws.amazon.com/firehose/latest/dev/monitoring-with-cloudwatch-metrics.html
//   - NAT Gateway metric names, units, and dimensions:
//     https://docs.aws.amazon.com/vpc/latest/userguide/metrics-dimensions-nat-gateway.html
//   - PrivateLink metric names and dimensions:
//     https://docs.aws.amazon.com/vpc/latest/privatelink/privatelink-cloudwatch-metrics.html
//   - S3 storage metric names, units, and dimensions:
//     https://docs.aws.amazon.com/AmazonS3/latest/userguide/metrics-dimensions.html
//   - CloudWatch's exact resource-tag metric dimension combinations:
//     https://docs.aws.amazon.com/AmazonCloudWatch/latest/monitoring/SupportedMetricsForResourceTagsForTelemetry.html
//   - CloudWatch's exact OTel-enrichment metric names and dimensions:
//     https://docs.aws.amazon.com/AmazonCloudWatch/latest/monitoring/CloudWatch-OTelEnrichment-SupportedMetrics.html
//   - CloudWatch Metric Streams 1.0.0 unit translation to UCUM:
//     https://docs.aws.amazon.com/AmazonCloudWatch/latest/monitoring/CloudWatch-metric-streams-formats-opentelemetry-translation-100.html
//
// MetricName values are copied verbatim from the AWS references; they are not reconstructed from
// the mangled Prometheus base. Unit values are the corresponding case-sensitive UCUM values from
// the Metric Streams translation table. AWS's descriptions use Ops/s for VolumeAvgIOPS and KiB/s
// for VolumeAvgThroughput, so their count/second and kilobyte/second meanings are represented as
// {Count}/s and kBy/s respectively. PrivateLink's reference page establishes count and byte
// semantics in its metric descriptions even though it does not print a separate Units field.
// Firehose's limit metrics describe a per-second byte or record/request limit, but AWS assigns
// the record/request limits the CloudWatch Count unit; only the byte limit uses By/s.
// DeliveryToHttpEndpoint.Success is withheld: AWS defines it as records delivered per attempt,
// while the current construct models a 0-1 success ratio.
func streamTableCWInfra() streamTable {
	return streamTable{
		entries: map[string]StreamEntry{
			// AWS/ApplicationELB.
			"aws_applicationelb_active_connection_count": {
				Namespace: "AWS/ApplicationELB", MetricName: "ActiveConnectionCount", Unit: "{Count}",
			},
			"aws_applicationelb_client_tlsnegotiation_error_count": {
				Namespace: "AWS/ApplicationELB", MetricName: "ClientTLSNegotiationErrorCount", Unit: "{Count}",
			},
			"aws_applicationelb_consumed_lcus": {
				Namespace: "AWS/ApplicationELB", MetricName: "ConsumedLCUs", Unit: "{Count}",
			},
			"aws_applicationelb_desync_mitigation_mode_non_compliant_request_count": {
				Namespace: "AWS/ApplicationELB", MetricName: "DesyncMitigationMode_NonCompliant_Request_Count", Unit: "{Count}",
			},
			"aws_applicationelb_healthy_host_count": {
				Namespace: "AWS/ApplicationELB", MetricName: "HealthyHostCount", Unit: "{Count}",
			},
			"aws_applicationelb_http_fixed_response_count": {
				Namespace: "AWS/ApplicationELB", MetricName: "HTTP_Fixed_Response_Count", Unit: "{Count}",
			},
			"aws_applicationelb_http_redirect_count": {
				Namespace: "AWS/ApplicationELB", MetricName: "HTTP_Redirect_Count", Unit: "{Count}",
			},
			"aws_applicationelb_httpcode_elb_3_xx_count": {
				Namespace: "AWS/ApplicationELB", MetricName: "HTTPCode_ELB_3XX_Count", Unit: "{Count}",
			},
			"aws_applicationelb_httpcode_elb_4_xx_count": {
				Namespace: "AWS/ApplicationELB", MetricName: "HTTPCode_ELB_4XX_Count", Unit: "{Count}",
			},
			"aws_applicationelb_httpcode_elb_5_xx_count": {
				Namespace: "AWS/ApplicationELB", MetricName: "HTTPCode_ELB_5XX_Count", Unit: "{Count}",
			},
			"aws_applicationelb_httpcode_target_2_xx_count": {
				Namespace: "AWS/ApplicationELB", MetricName: "HTTPCode_Target_2XX_Count", Unit: "{Count}",
			},
			"aws_applicationelb_httpcode_target_4_xx_count": {
				Namespace: "AWS/ApplicationELB", MetricName: "HTTPCode_Target_4XX_Count", Unit: "{Count}",
			},
			"aws_applicationelb_httpcode_target_5_xx_count": {
				Namespace: "AWS/ApplicationELB", MetricName: "HTTPCode_Target_5XX_Count", Unit: "{Count}",
			},
			"aws_applicationelb_new_connection_count": {
				Namespace: "AWS/ApplicationELB", MetricName: "NewConnectionCount", Unit: "{Count}",
			},
			"aws_applicationelb_peak_lcus": {
				Namespace: "AWS/ApplicationELB", MetricName: "PeakLCUs", Unit: "{Count}",
			},
			"aws_applicationelb_processed_bytes": {
				Namespace: "AWS/ApplicationELB", MetricName: "ProcessedBytes", Unit: "By",
			},
			"aws_applicationelb_request_count": {
				Namespace: "AWS/ApplicationELB", MetricName: "RequestCount", Unit: "{Count}",
			},
			"aws_applicationelb_request_count_per_target": {
				Namespace: "AWS/ApplicationELB", MetricName: "RequestCountPerTarget", Unit: "{Count}",
			},
			"aws_applicationelb_rule_evaluations": {
				Namespace: "AWS/ApplicationELB", MetricName: "RuleEvaluations", Unit: "{Count}",
			},
			"aws_applicationelb_target_connection_error_count": {
				Namespace: "AWS/ApplicationELB", MetricName: "TargetConnectionErrorCount", Unit: "{Count}",
			},
			"aws_applicationelb_target_response_time": {
				Namespace: "AWS/ApplicationELB", MetricName: "TargetResponseTime", Unit: "s",
			},
			"aws_applicationelb_un_healthy_host_count": {
				Namespace: "AWS/ApplicationELB", MetricName: "UnHealthyHostCount", Unit: "{Count}",
			},

			// AWS/NetworkELB.
			"aws_networkelb_active_flow_count": {
				Namespace: "AWS/NetworkELB", MetricName: "ActiveFlowCount", Unit: "{Count}",
			},
			"aws_networkelb_healthy_host_count": {
				Namespace: "AWS/NetworkELB", MetricName: "HealthyHostCount", Unit: "{Count}",
			},
			"aws_networkelb_new_flow_count": {
				Namespace: "AWS/NetworkELB", MetricName: "NewFlowCount", Unit: "{Count}",
			},
			"aws_networkelb_peak_bytes_per_second": {
				Namespace: "AWS/NetworkELB", MetricName: "PeakBytesPerSecond", Unit: "By/s",
			},
			"aws_networkelb_peak_packets_per_second": {
				Namespace: "AWS/NetworkELB", MetricName: "PeakPacketsPerSecond", Unit: "{Count}/s",
			},
			"aws_networkelb_port_allocation_error_count": {
				Namespace: "AWS/NetworkELB", MetricName: "PortAllocationErrorCount", Unit: "{Count}",
			},
			"aws_networkelb_processed_bytes": {
				Namespace: "AWS/NetworkELB", MetricName: "ProcessedBytes", Unit: "By",
			},
			"aws_networkelb_tcp_client_reset_count": {
				Namespace: "AWS/NetworkELB", MetricName: "TCP_Client_Reset_Count", Unit: "{Count}",
			},
			"aws_networkelb_tcp_elb_reset_count": {
				Namespace: "AWS/NetworkELB", MetricName: "TCP_ELB_Reset_Count", Unit: "{Count}",
			},
			"aws_networkelb_tcp_target_reset_count": {
				Namespace: "AWS/NetworkELB", MetricName: "TCP_Target_Reset_Count", Unit: "{Count}",
			},
			"aws_networkelb_un_healthy_host_count": {
				Namespace: "AWS/NetworkELB", MetricName: "UnHealthyHostCount", Unit: "{Count}",
			},

			// AWS/EBS.
			"aws_ebs_burst_balance": {
				Namespace: "AWS/EBS", MetricName: "BurstBalance", Unit: "%",
			},
			"aws_ebs_volume_avg_iops": {
				Namespace: "AWS/EBS", MetricName: "VolumeAvgIOPS", Unit: "{Count}/s",
			},
			"aws_ebs_volume_avg_read_latency": {
				Namespace: "AWS/EBS", MetricName: "VolumeAvgReadLatency", Unit: "ms",
			},
			"aws_ebs_volume_avg_throughput": {
				Namespace: "AWS/EBS", MetricName: "VolumeAvgThroughput", Unit: "kBy/s",
			},
			"aws_ebs_volume_avg_write_latency": {
				Namespace: "AWS/EBS", MetricName: "VolumeAvgWriteLatency", Unit: "ms",
			},
			"aws_ebs_volume_idle_time": {
				Namespace: "AWS/EBS", MetricName: "VolumeIdleTime", Unit: "s",
			},
			"aws_ebs_volume_iopsexceeded_check": {
				Namespace: "AWS/EBS", MetricName: "VolumeIOPSExceededCheck", Unit: "1",
			},
			"aws_ebs_volume_queue_length": {
				Namespace: "AWS/EBS", MetricName: "VolumeQueueLength", Unit: "{Count}",
			},
			"aws_ebs_volume_read_bytes": {
				Namespace: "AWS/EBS", MetricName: "VolumeReadBytes", Unit: "By",
			},
			"aws_ebs_volume_read_ops": {
				Namespace: "AWS/EBS", MetricName: "VolumeReadOps", Unit: "{Count}",
			},
			"aws_ebs_volume_stalled_iocheck": {
				Namespace: "AWS/EBS", MetricName: "VolumeStalledIOCheck", Unit: "1",
			},
			"aws_ebs_volume_throughput_exceeded_check": {
				Namespace: "AWS/EBS", MetricName: "VolumeThroughputExceededCheck", Unit: "1",
			},
			"aws_ebs_volume_total_read_time": {
				Namespace: "AWS/EBS", MetricName: "VolumeTotalReadTime", Unit: "s",
			},
			"aws_ebs_volume_total_write_time": {
				Namespace: "AWS/EBS", MetricName: "VolumeTotalWriteTime", Unit: "s",
			},
			"aws_ebs_volume_write_bytes": {
				Namespace: "AWS/EBS", MetricName: "VolumeWriteBytes", Unit: "By",
			},
			"aws_ebs_volume_write_ops": {
				Namespace: "AWS/EBS", MetricName: "VolumeWriteOps", Unit: "{Count}",
			},

			// AWS/EKS.
			"aws_eks_apiserver_request_duration_seconds_get_p99": {
				Namespace: "AWS/EKS", MetricName: "apiserver_request_duration_seconds_GET_P99", Unit: "s",
			},
			"aws_eks_apiserver_request_total": {
				Namespace: "AWS/EKS", MetricName: "apiserver_request_total", Unit: "{Count}",
			},
			"aws_eks_apiserver_request_total_4_xx": {
				Namespace: "AWS/EKS", MetricName: "apiserver_request_total_4XX", Unit: "{Count}",
			},
			"aws_eks_apiserver_request_total_5_xx": {
				Namespace: "AWS/EKS", MetricName: "apiserver_request_total_5XX", Unit: "{Count}",
			},
			"aws_eks_etcd_mvcc_db_total_size_in_bytes": {
				Namespace: "AWS/EKS", MetricName: "etcd_mvcc_db_total_size_in_bytes", Unit: "By",
			},
			"aws_eks_scheduler_pending_pods": {
				Namespace: "AWS/EKS", MetricName: "scheduler_pending_pods", Unit: "{Count}",
			},

			// AWS/Firehose.
			"aws_firehose_bytes_per_second_limit": {
				Namespace: "AWS/Firehose", MetricName: "BytesPerSecondLimit", Unit: "By/s",
			},
			"aws_firehose_delivery_to_http_endpoint_bytes": {
				Namespace: "AWS/Firehose", MetricName: "DeliveryToHttpEndpoint.Bytes", Unit: "By",
			},
			"aws_firehose_delivery_to_http_endpoint_data_freshness": {
				Namespace: "AWS/Firehose", MetricName: "DeliveryToHttpEndpoint.DataFreshness", Unit: "s",
			},
			"aws_firehose_delivery_to_http_endpoint_processed_bytes": {
				Namespace: "AWS/Firehose", MetricName: "DeliveryToHttpEndpoint.ProcessedBytes", Unit: "By",
			},
			"aws_firehose_delivery_to_http_endpoint_processed_records": {
				Namespace: "AWS/Firehose", MetricName: "DeliveryToHttpEndpoint.ProcessedRecords", Unit: "{Count}",
			},
			"aws_firehose_delivery_to_http_endpoint_records": {
				Namespace: "AWS/Firehose", MetricName: "DeliveryToHttpEndpoint.Records", Unit: "{Count}",
			},
			"aws_firehose_describe_delivery_stream_latency": {
				Namespace: "AWS/Firehose", MetricName: "DescribeDeliveryStream.Latency", Unit: "ms",
			},
			"aws_firehose_describe_delivery_stream_requests": {
				Namespace: "AWS/Firehose", MetricName: "DescribeDeliveryStream.Requests", Unit: "{Count}",
			},
			"aws_firehose_incoming_bytes": {
				Namespace: "AWS/Firehose", MetricName: "IncomingBytes", Unit: "By",
			},
			"aws_firehose_incoming_put_requests": {
				Namespace: "AWS/Firehose", MetricName: "IncomingPutRequests", Unit: "{Count}",
			},
			"aws_firehose_incoming_records": {
				Namespace: "AWS/Firehose", MetricName: "IncomingRecords", Unit: "{Count}",
			},
			"aws_firehose_put_record_batch_bytes": {
				Namespace: "AWS/Firehose", MetricName: "PutRecordBatch.Bytes", Unit: "By",
			},
			"aws_firehose_put_record_batch_latency": {
				Namespace: "AWS/Firehose", MetricName: "PutRecordBatch.Latency", Unit: "ms",
			},
			"aws_firehose_put_record_batch_records": {
				Namespace: "AWS/Firehose", MetricName: "PutRecordBatch.Records", Unit: "{Count}",
			},
			"aws_firehose_put_record_batch_requests": {
				Namespace: "AWS/Firehose", MetricName: "PutRecordBatch.Requests", Unit: "{Count}",
			},
			"aws_firehose_put_record_bytes": {
				Namespace: "AWS/Firehose", MetricName: "PutRecord.Bytes", Unit: "By",
			},
			"aws_firehose_put_record_latency": {
				Namespace: "AWS/Firehose", MetricName: "PutRecord.Latency", Unit: "ms",
			},
			"aws_firehose_put_record_requests": {
				Namespace: "AWS/Firehose", MetricName: "PutRecord.Requests", Unit: "{Count}",
			},
			"aws_firehose_put_requests_per_second_limit": {
				Namespace: "AWS/Firehose", MetricName: "PutRequestsPerSecondLimit", Unit: "{Count}",
			},
			"aws_firehose_records_per_second_limit": {
				Namespace: "AWS/Firehose", MetricName: "RecordsPerSecondLimit", Unit: "{Count}",
			},
			"aws_firehose_throttled_records": {
				Namespace: "AWS/Firehose", MetricName: "ThrottledRecords", Unit: "{Count}",
			},

			// AWS/NATGateway.
			"aws_natgateway_active_connection_count": {
				Namespace: "AWS/NATGateway", MetricName: "ActiveConnectionCount", Unit: "{Count}",
			},
			"aws_natgateway_bytes_in_from_destination": {
				Namespace: "AWS/NATGateway", MetricName: "BytesInFromDestination", Unit: "By",
			},
			"aws_natgateway_bytes_in_from_source": {
				Namespace: "AWS/NATGateway", MetricName: "BytesInFromSource", Unit: "By",
			},
			"aws_natgateway_bytes_out_to_destination": {
				Namespace: "AWS/NATGateway", MetricName: "BytesOutToDestination", Unit: "By",
			},
			"aws_natgateway_bytes_out_to_source": {
				Namespace: "AWS/NATGateway", MetricName: "BytesOutToSource", Unit: "By",
			},
			"aws_natgateway_connection_attempt_count": {
				Namespace: "AWS/NATGateway", MetricName: "ConnectionAttemptCount", Unit: "{Count}",
			},
			"aws_natgateway_connection_established_count": {
				Namespace: "AWS/NATGateway", MetricName: "ConnectionEstablishedCount", Unit: "{Count}",
			},
			"aws_natgateway_error_port_allocation": {
				Namespace: "AWS/NATGateway", MetricName: "ErrorPortAllocation", Unit: "{Count}",
			},
			"aws_natgateway_packets_drop_count": {
				Namespace: "AWS/NATGateway", MetricName: "PacketsDropCount", Unit: "{Count}",
			},
			"aws_natgateway_packets_in_from_destination": {
				Namespace: "AWS/NATGateway", MetricName: "PacketsInFromDestination", Unit: "{Count}",
			},
			"aws_natgateway_packets_in_from_source": {
				Namespace: "AWS/NATGateway", MetricName: "PacketsInFromSource", Unit: "{Count}",
			},
			"aws_natgateway_packets_out_to_destination": {
				Namespace: "AWS/NATGateway", MetricName: "PacketsOutToDestination", Unit: "{Count}",
			},
			"aws_natgateway_packets_out_to_source": {
				Namespace: "AWS/NATGateway", MetricName: "PacketsOutToSource", Unit: "{Count}",
			},
			"aws_natgateway_peak_bytes_per_second": {
				Namespace: "AWS/NATGateway", MetricName: "PeakBytesPerSecond", Unit: "By/s",
			},
			"aws_natgateway_peak_packets_per_second": {
				Namespace: "AWS/NATGateway", MetricName: "PeakPacketsPerSecond", Unit: "{Count}",
			},

			// AWS/PrivateLinkEndpoints.
			"aws_privatelinkendpoints_active_connections": {
				Namespace: "AWS/PrivateLinkEndpoints", MetricName: "ActiveConnections", Unit: "{Count}",
			},
			"aws_privatelinkendpoints_bytes_processed": {
				Namespace: "AWS/PrivateLinkEndpoints", MetricName: "BytesProcessed", Unit: "By",
			},
			"aws_privatelinkendpoints_new_connections": {
				Namespace: "AWS/PrivateLinkEndpoints", MetricName: "NewConnections", Unit: "{Count}",
			},
			"aws_privatelinkendpoints_packets_dropped": {
				Namespace: "AWS/PrivateLinkEndpoints", MetricName: "PacketsDropped", Unit: "{Count}",
			},
			"aws_privatelinkendpoints_rst_packets_received": {
				Namespace: "AWS/PrivateLinkEndpoints", MetricName: "RstPacketsReceived", Unit: "{Count}",
			},

			// AWS/PrivateLinkServices.
			"aws_privatelinkservices_active_connections": {
				Namespace: "AWS/PrivateLinkServices", MetricName: "ActiveConnections", Unit: "{Count}",
			},
			"aws_privatelinkservices_bytes_processed": {
				Namespace: "AWS/PrivateLinkServices", MetricName: "BytesProcessed", Unit: "By",
			},
			"aws_privatelinkservices_endpoints_count": {
				Namespace: "AWS/PrivateLinkServices", MetricName: "EndpointsCount", Unit: "{Count}",
			},
			"aws_privatelinkservices_new_connections": {
				Namespace: "AWS/PrivateLinkServices", MetricName: "NewConnections", Unit: "{Count}",
			},
			"aws_privatelinkservices_rst_packets_sent": {
				Namespace: "AWS/PrivateLinkServices", MetricName: "RstPacketsSent", Unit: "{Count}",
			},

			// AWS/S3 storage metrics.
			"aws_s3_bucket_size_bytes": {
				Namespace: "AWS/S3", MetricName: "BucketSizeBytes", Unit: "By",
			},
			"aws_s3_number_of_objects": {
				Namespace: "AWS/S3", MetricName: "NumberOfObjects", Unit: "{Count}",
			},
		},
		dimensions: map[string]map[string]string{
			// AWS/ApplicationELB load-balancer-scoped families.
			"aws_applicationelb_active_connection_count": {
				"dimension_LoadBalancer": "LoadBalancer", "dimension_AvailabilityZone": "AvailabilityZone",
			},
			"aws_applicationelb_client_tlsnegotiation_error_count": {
				"dimension_LoadBalancer": "LoadBalancer", "dimension_AvailabilityZone": "AvailabilityZone",
			},
			"aws_applicationelb_desync_mitigation_mode_non_compliant_request_count": {
				"dimension_LoadBalancer": "LoadBalancer", "dimension_AvailabilityZone": "AvailabilityZone",
			},
			"aws_applicationelb_http_fixed_response_count": {
				"dimension_LoadBalancer": "LoadBalancer", "dimension_AvailabilityZone": "AvailabilityZone",
			},
			"aws_applicationelb_http_redirect_count": {
				"dimension_LoadBalancer": "LoadBalancer", "dimension_AvailabilityZone": "AvailabilityZone",
			},
			"aws_applicationelb_httpcode_elb_3_xx_count": {
				"dimension_LoadBalancer": "LoadBalancer", "dimension_AvailabilityZone": "AvailabilityZone",
			},
			"aws_applicationelb_httpcode_elb_4_xx_count": {
				"dimension_LoadBalancer": "LoadBalancer", "dimension_AvailabilityZone": "AvailabilityZone",
			},
			"aws_applicationelb_httpcode_elb_5_xx_count": {
				"dimension_LoadBalancer": "LoadBalancer", "dimension_AvailabilityZone": "AvailabilityZone",
			},
			"aws_applicationelb_new_connection_count": {
				"dimension_LoadBalancer": "LoadBalancer", "dimension_AvailabilityZone": "AvailabilityZone",
			},
			"aws_applicationelb_processed_bytes": {
				"dimension_LoadBalancer": "LoadBalancer", "dimension_AvailabilityZone": "AvailabilityZone",
			},
			// AWS/ApplicationELB target-group-scoped families.
			"aws_applicationelb_healthy_host_count": {
				"dimension_LoadBalancer": "LoadBalancer", "dimension_TargetGroup": "TargetGroup", "dimension_AvailabilityZone": "AvailabilityZone",
			},
			"aws_applicationelb_httpcode_target_2_xx_count": {
				"dimension_LoadBalancer": "LoadBalancer", "dimension_TargetGroup": "TargetGroup", "dimension_AvailabilityZone": "AvailabilityZone",
			},
			"aws_applicationelb_httpcode_target_4_xx_count": {
				"dimension_LoadBalancer": "LoadBalancer", "dimension_TargetGroup": "TargetGroup", "dimension_AvailabilityZone": "AvailabilityZone",
			},
			"aws_applicationelb_httpcode_target_5_xx_count": {
				"dimension_LoadBalancer": "LoadBalancer", "dimension_TargetGroup": "TargetGroup", "dimension_AvailabilityZone": "AvailabilityZone",
			},
			"aws_applicationelb_request_count": {
				"dimension_LoadBalancer": "LoadBalancer", "dimension_TargetGroup": "TargetGroup", "dimension_AvailabilityZone": "AvailabilityZone",
			},
			"aws_applicationelb_request_count_per_target": {
				"dimension_LoadBalancer": "LoadBalancer", "dimension_TargetGroup": "TargetGroup", "dimension_AvailabilityZone": "AvailabilityZone",
			},
			"aws_applicationelb_target_connection_error_count": {
				"dimension_LoadBalancer": "LoadBalancer", "dimension_TargetGroup": "TargetGroup", "dimension_AvailabilityZone": "AvailabilityZone",
			},
			"aws_applicationelb_target_response_time": {
				"dimension_LoadBalancer": "LoadBalancer", "dimension_TargetGroup": "TargetGroup", "dimension_AvailabilityZone": "AvailabilityZone",
			},
			"aws_applicationelb_un_healthy_host_count": {
				"dimension_LoadBalancer": "LoadBalancer", "dimension_TargetGroup": "TargetGroup", "dimension_AvailabilityZone": "AvailabilityZone",
			},
			// These three ALB metrics are published with LoadBalancer only.
			"aws_applicationelb_consumed_lcus":    {"dimension_LoadBalancer": "LoadBalancer"},
			"aws_applicationelb_peak_lcus":        {"dimension_LoadBalancer": "LoadBalancer"},
			"aws_applicationelb_rule_evaluations": {"dimension_LoadBalancer": "LoadBalancer"},

			// AWS/NetworkELB target-group-scoped families.
			"aws_networkelb_active_flow_count": {
				"dimension_AvailabilityZone": "AvailabilityZone", "dimension_LoadBalancer": "LoadBalancer", "dimension_TargetGroup": "TargetGroup",
			},
			"aws_networkelb_healthy_host_count": {
				"dimension_AvailabilityZone": "AvailabilityZone", "dimension_LoadBalancer": "LoadBalancer", "dimension_TargetGroup": "TargetGroup",
			},
			"aws_networkelb_new_flow_count": {
				"dimension_AvailabilityZone": "AvailabilityZone", "dimension_LoadBalancer": "LoadBalancer", "dimension_TargetGroup": "TargetGroup",
			},
			"aws_networkelb_un_healthy_host_count": {
				"dimension_AvailabilityZone": "AvailabilityZone", "dimension_LoadBalancer": "LoadBalancer", "dimension_TargetGroup": "TargetGroup",
			},
			// AWS/NetworkELB load-balancer-scoped families.
			"aws_networkelb_peak_bytes_per_second": {
				"dimension_AvailabilityZone": "AvailabilityZone", "dimension_LoadBalancer": "LoadBalancer",
			},
			"aws_networkelb_peak_packets_per_second": {
				"dimension_AvailabilityZone": "AvailabilityZone", "dimension_LoadBalancer": "LoadBalancer",
			},
			"aws_networkelb_port_allocation_error_count": {
				"dimension_AvailabilityZone": "AvailabilityZone", "dimension_LoadBalancer": "LoadBalancer",
			},
			"aws_networkelb_processed_bytes": {
				"dimension_AvailabilityZone": "AvailabilityZone", "dimension_LoadBalancer": "LoadBalancer",
			},
			"aws_networkelb_tcp_client_reset_count": {
				"dimension_AvailabilityZone": "AvailabilityZone", "dimension_LoadBalancer": "LoadBalancer",
			},
			"aws_networkelb_tcp_elb_reset_count": {
				"dimension_AvailabilityZone": "AvailabilityZone", "dimension_LoadBalancer": "LoadBalancer",
			},
			"aws_networkelb_tcp_target_reset_count": {
				"dimension_AvailabilityZone": "AvailabilityZone", "dimension_LoadBalancer": "LoadBalancer",
			},

			// AWS/EBS volume-only families.
			"aws_ebs_burst_balance": {
				"dimension_VolumeId": "VolumeId",
			},
			"aws_ebs_volume_idle_time": {
				"dimension_VolumeId": "VolumeId",
			},
			"aws_ebs_volume_queue_length": {
				"dimension_VolumeId": "VolumeId",
			},
			"aws_ebs_volume_read_bytes": {
				"dimension_VolumeId": "VolumeId",
			},
			"aws_ebs_volume_read_ops": {
				"dimension_VolumeId": "VolumeId",
			},
			"aws_ebs_volume_total_read_time": {
				"dimension_VolumeId": "VolumeId",
			},
			"aws_ebs_volume_total_write_time": {
				"dimension_VolumeId": "VolumeId",
			},
			"aws_ebs_volume_write_bytes": {
				"dimension_VolumeId": "VolumeId",
			},
			"aws_ebs_volume_write_ops": {
				"dimension_VolumeId": "VolumeId",
			},
			// Nitro EBS families with the co-labeled instance dimension.
			"aws_ebs_volume_avg_iops": {
				"dimension_InstanceId": "InstanceId", "dimension_VolumeId": "VolumeId",
			},
			"aws_ebs_volume_avg_read_latency": {
				"dimension_InstanceId": "InstanceId", "dimension_VolumeId": "VolumeId",
			},
			"aws_ebs_volume_avg_throughput": {
				"dimension_InstanceId": "InstanceId", "dimension_VolumeId": "VolumeId",
			},
			"aws_ebs_volume_avg_write_latency": {
				"dimension_InstanceId": "InstanceId", "dimension_VolumeId": "VolumeId",
			},
			"aws_ebs_volume_iopsexceeded_check": {
				"dimension_InstanceId": "InstanceId", "dimension_VolumeId": "VolumeId",
			},
			"aws_ebs_volume_stalled_iocheck": {
				"dimension_InstanceId": "InstanceId", "dimension_VolumeId": "VolumeId",
			},
			"aws_ebs_volume_throughput_exceeded_check": {
				"dimension_InstanceId": "InstanceId", "dimension_VolumeId": "VolumeId",
			},

			// AWS/EKS.
			"aws_eks_apiserver_request_duration_seconds_get_p99": {"dimension_ClusterName": "ClusterName"},
			"aws_eks_apiserver_request_total":                    {"dimension_ClusterName": "ClusterName"},
			"aws_eks_apiserver_request_total_4_xx":               {"dimension_ClusterName": "ClusterName"},
			"aws_eks_apiserver_request_total_5_xx":               {"dimension_ClusterName": "ClusterName"},
			"aws_eks_etcd_mvcc_db_total_size_in_bytes":           {"dimension_ClusterName": "ClusterName"},
			"aws_eks_scheduler_pending_pods":                     {"dimension_ClusterName": "ClusterName"},

			// AWS/Firehose.
			"aws_firehose_bytes_per_second_limit":                      {"dimension_DeliveryStreamName": "DeliveryStreamName"},
			"aws_firehose_delivery_to_http_endpoint_bytes":             {"dimension_DeliveryStreamName": "DeliveryStreamName"},
			"aws_firehose_delivery_to_http_endpoint_data_freshness":    {"dimension_DeliveryStreamName": "DeliveryStreamName"},
			"aws_firehose_delivery_to_http_endpoint_processed_bytes":   {"dimension_DeliveryStreamName": "DeliveryStreamName"},
			"aws_firehose_delivery_to_http_endpoint_processed_records": {"dimension_DeliveryStreamName": "DeliveryStreamName"},
			"aws_firehose_delivery_to_http_endpoint_records":           {"dimension_DeliveryStreamName": "DeliveryStreamName"},
			"aws_firehose_describe_delivery_stream_latency":            {"dimension_DeliveryStreamName": "DeliveryStreamName"},
			"aws_firehose_describe_delivery_stream_requests":           {"dimension_DeliveryStreamName": "DeliveryStreamName"},
			"aws_firehose_incoming_bytes":                              {"dimension_DeliveryStreamName": "DeliveryStreamName"},
			"aws_firehose_incoming_put_requests":                       {"dimension_DeliveryStreamName": "DeliveryStreamName"},
			"aws_firehose_incoming_records":                            {"dimension_DeliveryStreamName": "DeliveryStreamName"},
			"aws_firehose_put_record_batch_bytes":                      {"dimension_DeliveryStreamName": "DeliveryStreamName"},
			"aws_firehose_put_record_batch_latency":                    {"dimension_DeliveryStreamName": "DeliveryStreamName"},
			"aws_firehose_put_record_batch_records":                    {"dimension_DeliveryStreamName": "DeliveryStreamName"},
			"aws_firehose_put_record_batch_requests":                   {"dimension_DeliveryStreamName": "DeliveryStreamName"},
			"aws_firehose_put_record_bytes":                            {"dimension_DeliveryStreamName": "DeliveryStreamName"},
			"aws_firehose_put_record_latency":                          {"dimension_DeliveryStreamName": "DeliveryStreamName"},
			"aws_firehose_put_record_requests":                         {"dimension_DeliveryStreamName": "DeliveryStreamName"},
			"aws_firehose_put_requests_per_second_limit":               {"dimension_DeliveryStreamName": "DeliveryStreamName"},
			"aws_firehose_records_per_second_limit":                    {"dimension_DeliveryStreamName": "DeliveryStreamName"},
			"aws_firehose_throttled_records":                           {"dimension_DeliveryStreamName": "DeliveryStreamName"},

			// AWS/NATGateway.
			"aws_natgateway_active_connection_count":      {"dimension_NatGatewayId": "NatGatewayId"},
			"aws_natgateway_bytes_in_from_destination":    {"dimension_NatGatewayId": "NatGatewayId"},
			"aws_natgateway_bytes_in_from_source":         {"dimension_NatGatewayId": "NatGatewayId"},
			"aws_natgateway_bytes_out_to_destination":     {"dimension_NatGatewayId": "NatGatewayId"},
			"aws_natgateway_bytes_out_to_source":          {"dimension_NatGatewayId": "NatGatewayId"},
			"aws_natgateway_connection_attempt_count":     {"dimension_NatGatewayId": "NatGatewayId"},
			"aws_natgateway_connection_established_count": {"dimension_NatGatewayId": "NatGatewayId"},
			"aws_natgateway_error_port_allocation":        {"dimension_NatGatewayId": "NatGatewayId"},
			"aws_natgateway_packets_drop_count":           {"dimension_NatGatewayId": "NatGatewayId"},
			"aws_natgateway_packets_in_from_destination":  {"dimension_NatGatewayId": "NatGatewayId"},
			"aws_natgateway_packets_in_from_source":       {"dimension_NatGatewayId": "NatGatewayId"},
			"aws_natgateway_packets_out_to_destination":   {"dimension_NatGatewayId": "NatGatewayId"},
			"aws_natgateway_packets_out_to_source":        {"dimension_NatGatewayId": "NatGatewayId"},
			"aws_natgateway_peak_bytes_per_second":        {"dimension_NatGatewayId": "NatGatewayId"},
			"aws_natgateway_peak_packets_per_second":      {"dimension_NatGatewayId": "NatGatewayId"},

			// AWS/PrivateLinkEndpoints. The Subnet Id form is the current cwinfra form;
			// the AWS reference also documents a form without Subnet Id.
			"aws_privatelinkendpoints_active_connections": {
				"dimension_Endpoint_Type": "Endpoint Type", "dimension_Service_Name": "Service Name", "dimension_Subnet_Id": "Subnet Id", "dimension_VPC_Endpoint_Id": "VPC Endpoint Id", "dimension_VPC_Id": "VPC Id",
			},
			"aws_privatelinkendpoints_bytes_processed": {
				"dimension_Endpoint_Type": "Endpoint Type", "dimension_Service_Name": "Service Name", "dimension_Subnet_Id": "Subnet Id", "dimension_VPC_Endpoint_Id": "VPC Endpoint Id", "dimension_VPC_Id": "VPC Id",
			},
			"aws_privatelinkendpoints_new_connections": {
				"dimension_Endpoint_Type": "Endpoint Type", "dimension_Service_Name": "Service Name", "dimension_Subnet_Id": "Subnet Id", "dimension_VPC_Endpoint_Id": "VPC Endpoint Id", "dimension_VPC_Id": "VPC Id",
			},
			"aws_privatelinkendpoints_packets_dropped": {
				"dimension_Endpoint_Type": "Endpoint Type", "dimension_Service_Name": "Service Name", "dimension_Subnet_Id": "Subnet Id", "dimension_VPC_Endpoint_Id": "VPC Endpoint Id", "dimension_VPC_Id": "VPC Id",
			},
			"aws_privatelinkendpoints_rst_packets_received": {
				"dimension_Endpoint_Type": "Endpoint Type", "dimension_Service_Name": "Service Name", "dimension_Subnet_Id": "Subnet Id", "dimension_VPC_Endpoint_Id": "VPC Endpoint Id", "dimension_VPC_Id": "VPC Id",
			},

			// AWS/PrivateLinkServices. The current cwinfra service form carries all four
			// labels; each label spelling is copied from the AWS reference.
			"aws_privatelinkservices_active_connections": {
				"dimension_Az": "Az", "dimension_Load_Balancer_Arn": "Load Balancer Arn", "dimension_Service_Id": "Service Id", "dimension_VPC_Endpoint_Id": "VPC Endpoint Id",
			},
			"aws_privatelinkservices_bytes_processed": {
				"dimension_Az": "Az", "dimension_Load_Balancer_Arn": "Load Balancer Arn", "dimension_Service_Id": "Service Id", "dimension_VPC_Endpoint_Id": "VPC Endpoint Id",
			},
			"aws_privatelinkservices_endpoints_count": {
				"dimension_Service_Id": "Service Id",
			},
			"aws_privatelinkservices_new_connections": {
				"dimension_Az": "Az", "dimension_Load_Balancer_Arn": "Load Balancer Arn", "dimension_Service_Id": "Service Id", "dimension_VPC_Endpoint_Id": "VPC Endpoint Id",
			},
			"aws_privatelinkservices_rst_packets_sent": {
				"dimension_Az": "Az", "dimension_Load_Balancer_Arn": "Load Balancer Arn", "dimension_Service_Id": "Service Id", "dimension_VPC_Endpoint_Id": "VPC Endpoint Id",
			},

			// AWS/S3 storage metrics.
			"aws_s3_bucket_size_bytes": {
				"dimension_BucketName": "BucketName", "dimension_StorageType": "StorageType",
			},
			"aws_s3_number_of_objects": {
				"dimension_BucketName": "BucketName", "dimension_StorageType": "StorageType",
			},
		},
	}
}
