// SPDX-License-Identifier: AGPL-3.0-only

package cspazure

// native_otlp.go is the explicit Azure Monitor receiver catalogue. The receiver's
// source keeps the Azure programmatic metric name and aggregation separate from the
// scrape form, then builds the OTLP name as:
//
//   lower("azure_" + strings.ReplaceAll(metricName, " ", "_") + "_" + aggregation)
//
// The native metric names below are copied from the Azure Monitor supported-metrics
// tables. They are deliberately not derived from the azure_microsoft_* Prometheus
// names: those names include the ARM provider path and the scrape aggregation/unit
// spelling, and cannot recover the receiver contract safely.

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/sink/otlp"
	"github.com/rknightion/synthkit/internal/sink/promrw"
)

const azureMonitorReceiverScope = "github.com/open-telemetry/opentelemetry-collector-contrib/receiver/azuremonitorreceiver"

type nativeMetricSpec struct {
	ResourceType string
	LegacyName   string
	MetricName   string
	Aggregation  string
	Unit         string
	Dimensions   []string
}

func nativeSpec(resourceType, legacyName, metricName, aggregation, unit string, dimensions ...string) nativeMetricSpec {
	return nativeMetricSpec{
		ResourceType: resourceType,
		LegacyName:   legacyName,
		MetricName:   metricName,
		Aggregation:  aggregation,
		Unit:         unit,
		Dimensions:   dimensions,
	}
}

// nativeMetricSpecs is ordered by the existing scrape families. VNet is absent:
// the current Azure Monitor supported-metrics page does not specify the existing
// Subnets/AvailableAddresses/... families, so the lane withholds them.
var nativeMetricSpecs = []nativeMetricSpec{
	// Microsoft.Compute/virtualMachines.
	nativeSpec("Microsoft.Compute/virtualMachines", "azure_microsoft_compute_virtualmachines_vmavailabilitymetric_average_count", "VmAvailabilityMetric", "Average", "Count"),
	nativeSpec("Microsoft.Compute/virtualMachines", "azure_microsoft_compute_virtualmachines_percentage_cpu_average_percent", "Percentage CPU", "Average", "Percent"),
	nativeSpec("Microsoft.Compute/virtualMachines", "azure_microsoft_compute_virtualmachines_available_memory_bytes_average_bytes", "Available Memory Bytes", "Average", "Bytes"),
	nativeSpec("Microsoft.Compute/virtualMachines", "azure_microsoft_compute_virtualmachines_cpu_credits_consumed_average_count", "CPU Credits Consumed", "Average", "Count"),
	nativeSpec("Microsoft.Compute/virtualMachines", "azure_microsoft_compute_virtualmachines_cpu_credits_remaining_average_count", "CPU Credits Remaining", "Average", "Count"),
	nativeSpec("Microsoft.Compute/virtualMachines", "azure_microsoft_compute_virtualmachines_disk_read_bytes_total_bytes", "Disk Read Bytes", "Total", "Bytes"),
	nativeSpec("Microsoft.Compute/virtualMachines", "azure_microsoft_compute_virtualmachines_disk_write_bytes_total_bytes", "Disk Write Bytes", "Total", "Bytes"),
	nativeSpec("Microsoft.Compute/virtualMachines", "azure_microsoft_compute_virtualmachines_disk_read_operations_sec_average_countpersecond", "Disk Read Operations/Sec", "Average", "CountPerSecond"),
	nativeSpec("Microsoft.Compute/virtualMachines", "azure_microsoft_compute_virtualmachines_disk_write_operations_sec_average_countpersecond", "Disk Write Operations/Sec", "Average", "CountPerSecond"),
	nativeSpec("Microsoft.Compute/virtualMachines", "azure_microsoft_compute_virtualmachines_inbound_flows_average_count", "Inbound Flows", "Average", "Count"),
	nativeSpec("Microsoft.Compute/virtualMachines", "azure_microsoft_compute_virtualmachines_outbound_flows_average_count", "Outbound Flows", "Average", "Count"),
	nativeSpec("Microsoft.Compute/virtualMachines", "azure_microsoft_compute_virtualmachines_network_in_total_total_bytes", "Network In Total", "Total", "Bytes"),
	nativeSpec("Microsoft.Compute/virtualMachines", "azure_microsoft_compute_virtualmachines_network_out_total_total_bytes", "Network Out Total", "Total", "Bytes"),

	// Microsoft.Sql/servers/databases.
	nativeSpec("Microsoft.Sql/servers/databases", "azure_microsoft_sql_servers_databases_connection_successful_total_count", "connection_successful", "Total", "Count", "SslProtocol", "ValidatedDriverNameAndVersion"),
	nativeSpec("Microsoft.Sql/servers/databases", "azure_microsoft_sql_servers_databases_deadlock_total_count", "deadlock", "Total", "Count"),
	nativeSpec("Microsoft.Sql/servers/databases", "azure_microsoft_sql_servers_databases_sessions_count_average_count", "sessions_count", "Average", "Count"),
	nativeSpec("Microsoft.Sql/servers/databases", "azure_microsoft_sql_servers_databases_cpu_percent_average_percent", "cpu_percent", "Average", "Percent"),
	nativeSpec("Microsoft.Sql/servers/databases", "azure_microsoft_sql_servers_databases_cpu_limit_average_count", "cpu_limit", "Average", "Count"),
	nativeSpec("Microsoft.Sql/servers/databases", "azure_microsoft_sql_servers_databases_cpu_used_average_count", "cpu_used", "Average", "Count"),
	nativeSpec("Microsoft.Sql/servers/databases", "azure_microsoft_sql_servers_databases_storage_maximum_bytes", "storage", "Maximum", "Bytes"),
	nativeSpec("Microsoft.Sql/servers/databases", "azure_microsoft_sql_servers_databases_storage_percent_maximum_percent", "storage_percent", "Maximum", "Percent"),
	nativeSpec("Microsoft.Sql/servers/databases", "azure_microsoft_sql_servers_databases_dtu_used_average_count", "dtu_used", "Average", "Count"),
	nativeSpec("Microsoft.Sql/servers/databases", "azure_microsoft_sql_servers_databases_dtu_consumption_percent_average_percent", "dtu_consumption_percent", "Average", "Percent"),
	nativeSpec("Microsoft.Sql/servers/databases", "azure_microsoft_sql_servers_databases_dtu_limit_average_count", "dtu_limit", "Average", "Count"),

	// Microsoft.Sql/servers/elasticpools.
	nativeSpec("Microsoft.Sql/servers/elasticpools", "azure_microsoft_sql_servers_elasticpools_allocated_data_storage_average_bytes", "allocated_data_storage", "Average", "Bytes"),
	nativeSpec("Microsoft.Sql/servers/elasticpools", "azure_microsoft_sql_servers_elasticpools_storage_used_average_bytes", "storage_used", "Average", "Bytes"),
	nativeSpec("Microsoft.Sql/servers/elasticpools", "azure_microsoft_sql_servers_elasticpools_storage_limit_average_bytes", "storage_limit", "Average", "Bytes"),
	nativeSpec("Microsoft.Sql/servers/elasticpools", "azure_microsoft_sql_servers_elasticpools_cpu_percent_average_percent", "cpu_percent", "Average", "Percent"),
	nativeSpec("Microsoft.Sql/servers/elasticpools", "azure_microsoft_sql_servers_elasticpools_sql_instance_memory_percent_maximum_percent", "sql_instance_memory_percent", "Maximum", "Percent"),
	nativeSpec("Microsoft.Sql/servers/elasticpools", "azure_microsoft_sql_servers_elasticpools_edtu_used_average_count", "eDTU_used", "Average", "Count"),
	nativeSpec("Microsoft.Sql/servers/elasticpools", "azure_microsoft_sql_servers_elasticpools_sessions_count_average_count", "sessions_count", "Average", "Count"),
	nativeSpec("Microsoft.Sql/servers/elasticpools", "azure_microsoft_sql_servers_elasticpools_allocated_data_storage_percent_average_percent", "allocated_data_storage_percent", "Average", "Percent"),
	nativeSpec("Microsoft.Sql/servers/elasticpools", "azure_microsoft_sql_servers_elasticpools_storage_percent_average_percent", "storage_percent", "Average", "Percent"),

	// Microsoft.DBforPostgreSQL/flexibleServers.
	nativeSpec("Microsoft.DBforPostgreSQL/flexibleServers", "azure_microsoft_dbforpostgresql_flexibleservers_active_connections_average_count", "active_connections", "Average", "Count"),
	nativeSpec("Microsoft.DBforPostgreSQL/flexibleServers", "azure_microsoft_dbforpostgresql_flexibleservers_connections_succeeded_total_count", "connections_succeeded", "Total", "Count"),
	nativeSpec("Microsoft.DBforPostgreSQL/flexibleServers", "azure_microsoft_dbforpostgresql_flexibleservers_connections_connections_failed_total_count", "connections_failed", "Total", "Count"),
	nativeSpec("Microsoft.DBforPostgreSQL/flexibleServers", "azure_microsoft_dbforpostgresql_flexibleservers_cpu_percent_average_percent", "cpu_percent", "Average", "Percent"),
	nativeSpec("Microsoft.DBforPostgreSQL/flexibleServers", "azure_microsoft_dbforpostgresql_flexibleservers_storage_used_maximum_bytes", "storage_used", "Maximum", "Bytes"),
	nativeSpec("Microsoft.DBforPostgreSQL/flexibleServers", "azure_microsoft_dbforpostgresql_flexibleservers_storage_percent_maximum_percent", "storage_percent", "Maximum", "Percent"),
	nativeSpec("Microsoft.DBforPostgreSQL/flexibleServers", "azure_microsoft_dbforpostgresql_flexibleservers_read_iops_maximum_count", "read_iops", "Maximum", "Count"),
	nativeSpec("Microsoft.DBforPostgreSQL/flexibleServers", "azure_microsoft_dbforpostgresql_flexibleservers_write_iops_maximum_count", "write_iops", "Maximum", "Count"),
	nativeSpec("Microsoft.DBforPostgreSQL/flexibleServers", "azure_microsoft_dbforpostgresql_flexibleservers_database_size_bytes_average_bytes", "database_size_bytes", "Average", "Bytes"),
	nativeSpec("Microsoft.DBforPostgreSQL/flexibleServers", "azure_microsoft_dbforpostgresql_flexibleservers_storage_percent_average_percent", "storage_percent", "Average", "Percent"),
	nativeSpec("Microsoft.DBforPostgreSQL/flexibleServers", "azure_microsoft_dbforpostgresql_flexibleservers_memory_percent_average_percent", "memory_percent", "Average", "Percent"),
	nativeSpec("Microsoft.DBforPostgreSQL/flexibleServers", "azure_microsoft_dbforpostgresql_flexibleservers_read_iops_average_count", "read_iops", "Average", "Count"),
	nativeSpec("Microsoft.DBforPostgreSQL/flexibleServers", "azure_microsoft_dbforpostgresql_flexibleservers_write_iops_average_count", "write_iops", "Average", "Count"),
	nativeSpec("Microsoft.DBforPostgreSQL/flexibleServers", "azure_microsoft_dbforpostgresql_flexibleservers_network_bytes_ingress_total_bytes", "network_bytes_ingress", "Total", "Bytes"),
	nativeSpec("Microsoft.DBforPostgreSQL/flexibleServers", "azure_microsoft_dbforpostgresql_flexibleservers_network_bytes_egress_total_bytes", "network_bytes_egress", "Total", "Bytes"),
	nativeSpec("Microsoft.DBforPostgreSQL/flexibleServers", "azure_microsoft_dbforpostgresql_flexibleservers_read_throughput_average_count", "read_throughput", "Average", "Count"),
	nativeSpec("Microsoft.DBforPostgreSQL/flexibleServers", "azure_microsoft_dbforpostgresql_flexibleservers_write_throughput_average_count", "write_throughput", "Average", "Count"),

	// Microsoft.Storage/storageAccounts/blobServices.
	nativeSpec("Microsoft.Storage/storageAccounts/blobServices", "azure_microsoft_storage_storageaccounts_blobservices_containercount_average_count", "ContainerCount", "Average", "Count"),
	nativeSpec("Microsoft.Storage/storageAccounts/blobServices", "azure_microsoft_storage_storageaccounts_blobservices_blobcount_average_count", "BlobCount", "Average", "Count", "BlobType", "Tier"),
	nativeSpec("Microsoft.Storage/storageAccounts/blobServices", "azure_microsoft_storage_storageaccounts_blobservices_blobcapacity_average_bytes", "BlobCapacity", "Average", "Bytes", "BlobType", "Tier"),
	nativeSpec("Microsoft.Storage/storageAccounts/blobServices", "azure_microsoft_storage_storageaccounts_blobservices_indexcapacity_average_bytes", "IndexCapacity", "Average", "Bytes"),
	nativeSpec("Microsoft.Storage/storageAccounts/blobServices", "azure_microsoft_storage_storageaccounts_blobservices_ingress_total_bytes", "Ingress", "Total", "Bytes"),
	nativeSpec("Microsoft.Storage/storageAccounts/blobServices", "azure_microsoft_storage_storageaccounts_blobservices_egress_total_bytes", "Egress", "Total", "Bytes"),
	nativeSpec("Microsoft.Storage/storageAccounts/blobServices", "azure_microsoft_storage_storageaccounts_blobservices_availability_average_percent", "Availability", "Average", "Percent"),
	nativeSpec("Microsoft.Storage/storageAccounts/blobServices", "azure_microsoft_storage_storageaccounts_blobservices_transactions_total_count", "Transactions", "Total", "Count", "ApiName", "ResponseType"),

	// Microsoft.Storage/storageAccounts/queueServices.
	nativeSpec("Microsoft.Storage/storageAccounts/queueServices", "azure_microsoft_storage_storageaccounts_queueservices_queuecount_average_count", "QueueCount", "Average", "Count"),
	nativeSpec("Microsoft.Storage/storageAccounts/queueServices", "azure_microsoft_storage_storageaccounts_queueservices_queuemessagecount_average_count", "QueueMessageCount", "Average", "Count"),
	nativeSpec("Microsoft.Storage/storageAccounts/queueServices", "azure_microsoft_storage_storageaccounts_queueservices_queuecapacity_average_bytes", "QueueCapacity", "Average", "Bytes"),
	nativeSpec("Microsoft.Storage/storageAccounts/queueServices", "azure_microsoft_storage_storageaccounts_queueservices_ingress_total_bytes", "Ingress", "Total", "Bytes"),
	nativeSpec("Microsoft.Storage/storageAccounts/queueServices", "azure_microsoft_storage_storageaccounts_queueservices_egress_total_bytes", "Egress", "Total", "Bytes"),
	nativeSpec("Microsoft.Storage/storageAccounts/queueServices", "azure_microsoft_storage_storageaccounts_queueservices_availability_average_percent", "Availability", "Average", "Percent"),
	nativeSpec("Microsoft.Storage/storageAccounts/queueServices", "azure_microsoft_storage_storageaccounts_queueservices_transactions_total_count", "Transactions", "Total", "Count", "ApiName", "ResponseType"),

	// Microsoft.Network/loadBalancers.
	nativeSpec("Microsoft.Network/loadBalancers", "azure_microsoft_network_loadbalancers_syncount_total_count", "SYNCount", "Total", "Count"),
	nativeSpec("Microsoft.Network/loadBalancers", "azure_microsoft_network_loadbalancers_packetcount_total_count", "PacketCount", "Total", "Count"),
	nativeSpec("Microsoft.Network/loadBalancers", "azure_microsoft_network_loadbalancers_bytecount_total_bytes", "ByteCount", "Total", "Bytes"),
	nativeSpec("Microsoft.Network/loadBalancers", "azure_microsoft_network_loadbalancers_snatconnectioncount_total_count", "SnatConnectionCount", "Total", "Count"),
	nativeSpec("Microsoft.Network/loadBalancers", "azure_microsoft_network_loadbalancers_usedsnatports_average_count", "UsedSnatPorts", "Average", "Count"),
	nativeSpec("Microsoft.Network/loadBalancers", "azure_microsoft_network_loadbalancers_allocatedsnatports_average_count", "AllocatedSnatPorts", "Average", "Count"),

	// Microsoft.Network/applicationGateways.
	nativeSpec("Microsoft.Network/applicationGateways", "azure_microsoft_network_applicationgateways_totalrequests_total_count", "TotalRequests", "Total", "Count"),
	nativeSpec("Microsoft.Network/applicationGateways", "azure_microsoft_network_applicationgateways_failedrequests_total_count", "FailedRequests", "Total", "Count"),
	nativeSpec("Microsoft.Network/applicationGateways", "azure_microsoft_network_applicationgateways_responsestatus_total_count", "ResponseStatus", "Total", "Count"),
	nativeSpec("Microsoft.Network/applicationGateways", "azure_microsoft_network_applicationgateways_throughput_average_bytespersecond", "Throughput", "Average", "BytesPerSecond"),
	nativeSpec("Microsoft.Network/applicationGateways", "azure_microsoft_network_applicationgateways_applicationgatewaytotaltime_average_milliseconds", "ApplicationGatewayTotalTime", "Average", "MilliSeconds"),
	nativeSpec("Microsoft.Network/applicationGateways", "azure_microsoft_network_applicationgateways_currentconnections_total_count", "CurrentConnections", "Total", "Count"),

	// Microsoft.Cdn/profiles.
	nativeSpec("Microsoft.Cdn/profiles", "azure_microsoft_cdn_profiles_percentage4xx_average_percent", "Percentage4XX", "Average", "Percent"),
	nativeSpec("Microsoft.Cdn/profiles", "azure_microsoft_cdn_profiles_percentage5xx_average_percent", "Percentage5XX", "Average", "Percent"),
	nativeSpec("Microsoft.Cdn/profiles", "azure_microsoft_cdn_profiles_requestsize_total_bytes", "RequestSize", "Total", "Bytes"),
	nativeSpec("Microsoft.Cdn/profiles", "azure_microsoft_cdn_profiles_responsesize_total_bytes", "ResponseSize", "Total", "Bytes"),
	nativeSpec("Microsoft.Cdn/profiles", "azure_microsoft_cdn_profiles_totallatency_average_milliseconds", "TotalLatency", "Average", "MilliSeconds"),
	nativeSpec("Microsoft.Cdn/profiles", "azure_microsoft_cdn_profiles_originhealthpercentage_average_percent", "OriginHealthPercentage", "Average", "Percent"),
	nativeSpec("Microsoft.Cdn/profiles", "azure_microsoft_cdn_profiles_originlatency_average_milliseconds", "OriginLatency", "Average", "MilliSeconds"),
	nativeSpec("Microsoft.Cdn/profiles", "azure_microsoft_cdn_profiles_originrequestcount_total_count", "OriginRequestCount", "Total", "Count"),
	nativeSpec("Microsoft.Cdn/profiles", "azure_microsoft_cdn_profiles_requestcount_total_count", "RequestCount", "Total", "Count", "Endpoint", "ClientCountry", "HttpStatusGroup"),

	// Microsoft.EventHub/namespaces.
	nativeSpec("Microsoft.EventHub/namespaces", "azure_microsoft_eventhub_namespaces_activeconnections_maximum_count", "ActiveConnections", "Maximum", "Count"),
	nativeSpec("Microsoft.EventHub/namespaces", "azure_microsoft_eventhub_namespaces_connectionsopened_maximum_count", "ConnectionsOpened", "Maximum", "Count"),
	nativeSpec("Microsoft.EventHub/namespaces", "azure_microsoft_eventhub_namespaces_connectionsclosed_maximum_count", "ConnectionsClosed", "Maximum", "Count"),
	nativeSpec("Microsoft.EventHub/namespaces", "azure_microsoft_eventhub_namespaces_incomingrequests_total_count", "IncomingRequests", "Total", "Count"),
	nativeSpec("Microsoft.EventHub/namespaces", "azure_microsoft_eventhub_namespaces_successfulrequests_total_count", "SuccessfulRequests", "Total", "Count"),
	nativeSpec("Microsoft.EventHub/namespaces", "azure_microsoft_eventhub_namespaces_throttledrequests_total_count", "ThrottledRequests", "Total", "Count"),
	nativeSpec("Microsoft.EventHub/namespaces", "azure_microsoft_eventhub_namespaces_usererrors_total_count", "UserErrors", "Total", "Count"),
	nativeSpec("Microsoft.EventHub/namespaces", "azure_microsoft_eventhub_namespaces_servererrors_total_count", "ServerErrors", "Total", "Count"),
	nativeSpec("Microsoft.EventHub/namespaces", "azure_microsoft_eventhub_namespaces_incomingbytes_total_bytes", "IncomingBytes", "Total", "Bytes"),
	nativeSpec("Microsoft.EventHub/namespaces", "azure_microsoft_eventhub_namespaces_outgoingbytes_total_bytes", "OutgoingBytes", "Total", "Bytes"),
	nativeSpec("Microsoft.EventHub/namespaces", "azure_microsoft_eventhub_namespaces_incomingmessages_total_count", "IncomingMessages", "Total", "Count", "EntityName"),
	nativeSpec("Microsoft.EventHub/namespaces", "azure_microsoft_eventhub_namespaces_outgoingmessages_total_count", "OutgoingMessages", "Total", "Count", "EntityName"),
	nativeSpec("Microsoft.EventHub/namespaces", "azure_microsoft_eventhub_namespaces_capturedmessages_total_count", "CapturedMessages", "Total", "Count", "EntityName"),

	// Microsoft.ServiceBus/namespaces.
	nativeSpec("Microsoft.ServiceBus/namespaces", "azure_microsoft_servicebus_namespaces_incomingmessages_total_count", "IncomingMessages", "Total", "Count"),
	nativeSpec("Microsoft.ServiceBus/namespaces", "azure_microsoft_servicebus_namespaces_outgoingmessages_total_count", "OutgoingMessages", "Total", "Count"),
	nativeSpec("Microsoft.ServiceBus/namespaces", "azure_microsoft_servicebus_namespaces_incomingrequests_total_count", "IncomingRequests", "Total", "Count"),
	nativeSpec("Microsoft.ServiceBus/namespaces", "azure_microsoft_servicebus_namespaces_successfulrequests_total_count", "SuccessfulRequests", "Total", "Count"),
	nativeSpec("Microsoft.ServiceBus/namespaces", "azure_microsoft_servicebus_namespaces_activeconnections_total_count", "ActiveConnections", "Total", "Count"),
	nativeSpec("Microsoft.ServiceBus/namespaces", "azure_microsoft_servicebus_namespaces_usererrors_total_count", "UserErrors", "Total", "Count"),
	nativeSpec("Microsoft.ServiceBus/namespaces", "azure_microsoft_servicebus_namespaces_servererrors_total_count", "ServerErrors", "Total", "Count"),
	nativeSpec("Microsoft.ServiceBus/namespaces", "azure_microsoft_servicebus_namespaces_messages_average_count", "Messages", "Average", "Count"),
	nativeSpec("Microsoft.ServiceBus/namespaces", "azure_microsoft_servicebus_namespaces_activemessages_average_count", "ActiveMessages", "Average", "Count", "EntityName"),
	nativeSpec("Microsoft.ServiceBus/namespaces", "azure_microsoft_servicebus_namespaces_size_average_bytes", "Size", "Average", "Bytes", "EntityName"),

	// Microsoft.CognitiveServices/accounts (opt-in scrape family).
	nativeSpec("Microsoft.CognitiveServices/accounts", "azure_microsoft_cognitiveservices_accounts_total_calls_total_count", "TotalCalls", "Total", "Count"),
	nativeSpec("Microsoft.CognitiveServices/accounts", "azure_microsoft_cognitiveservices_accounts_successful_calls_total_count", "SuccessfulCalls", "Total", "Count"),
	nativeSpec("Microsoft.CognitiveServices/accounts", "azure_microsoft_cognitiveservices_accounts_blocked_calls_total_count", "BlockedCalls", "Total", "Count"),
	nativeSpec("Microsoft.CognitiveServices/accounts", "azure_microsoft_cognitiveservices_accounts_total_errors_total_count", "TotalErrors", "Total", "Count"),
	nativeSpec("Microsoft.CognitiveServices/accounts", "azure_microsoft_cognitiveservices_accounts_client_errors_total_count", "ClientErrors", "Total", "Count"),
	nativeSpec("Microsoft.CognitiveServices/accounts", "azure_microsoft_cognitiveservices_accounts_server_errors_total_count", "ServerErrors", "Total", "Count"),
	nativeSpec("Microsoft.CognitiveServices/accounts", "azure_microsoft_cognitiveservices_accounts_total_token_calls_total_count", "TotalTokenCalls", "Total", "Count"),
	nativeSpec("Microsoft.CognitiveServices/accounts", "azure_microsoft_cognitiveservices_accounts_processed_prompt_tokens_total_count", "ProcessedPromptTokens", "Total", "Count", "ModelDeploymentName", "ModelName"),
	nativeSpec("Microsoft.CognitiveServices/accounts", "azure_microsoft_cognitiveservices_accounts_generated_completion_tokens_total_count", "GeneratedTokens", "Total", "Count", "ModelDeploymentName", "ModelName"),
	nativeSpec("Microsoft.CognitiveServices/accounts", "azure_microsoft_cognitiveservices_accounts_tokens_per_second_average_count", "TokensPerSecond", "Average", "Count", "ModelDeploymentName", "ModelName"),
}

var nativeMetricSpecByLegacy = func() map[string]nativeMetricSpec {
	result := make(map[string]nativeMetricSpec, len(nativeMetricSpecs))
	for _, spec := range nativeMetricSpecs {
		result[spec.LegacyName] = spec
	}
	return result
}()

// receiverMetricName reproduces azuremonitorreceiver/internal/metadata's exact
// getLogicalMetricID law. It is the only name transformation in this lane.
func receiverMetricName(metricName, aggregation string) string {
	return strings.ToLower("azure_" + strings.ReplaceAll(metricName, " ", "_") + "_" + aggregation)
}

func (s nativeMetricSpec) receiverName() string {
	return receiverMetricName(s.MetricName, s.Aggregation)
}

// nativeMetricContext identifies a metric without using either of the emitted
// names. It is the key for the small set of Azure exporter compatibility
// exceptions below.
type nativeMetricContext struct {
	ResourceType string
	MetricName   string
	Aggregation  string
	Unit         string
}

// legacyMetricTokenExceptions records the metric token used by the existing
// scrape family when it is not the lowercased Azure metric-definition name.
// The keys deliberately use native context, never LegacyName, so a legacy
// spelling cannot become an input to native naming.
var legacyMetricTokenExceptions = map[nativeMetricContext]string{
	{
		ResourceType: "Microsoft.DBforPostgreSQL/flexibleServers",
		MetricName:   "connections_failed",
		Aggregation:  "Total",
		Unit:         "Count",
	}: "connections_connections_failed",
	{
		ResourceType: "Microsoft.CognitiveServices/accounts",
		MetricName:   "TotalCalls",
		Aggregation:  "Total",
		Unit:         "Count",
	}: "total_calls",
	{
		ResourceType: "Microsoft.CognitiveServices/accounts",
		MetricName:   "SuccessfulCalls",
		Aggregation:  "Total",
		Unit:         "Count",
	}: "successful_calls",
	{
		ResourceType: "Microsoft.CognitiveServices/accounts",
		MetricName:   "BlockedCalls",
		Aggregation:  "Total",
		Unit:         "Count",
	}: "blocked_calls",
	{
		ResourceType: "Microsoft.CognitiveServices/accounts",
		MetricName:   "TotalErrors",
		Aggregation:  "Total",
		Unit:         "Count",
	}: "total_errors",
	{
		ResourceType: "Microsoft.CognitiveServices/accounts",
		MetricName:   "ClientErrors",
		Aggregation:  "Total",
		Unit:         "Count",
	}: "client_errors",
	{
		ResourceType: "Microsoft.CognitiveServices/accounts",
		MetricName:   "ServerErrors",
		Aggregation:  "Total",
		Unit:         "Count",
	}: "server_errors",
	{
		ResourceType: "Microsoft.CognitiveServices/accounts",
		MetricName:   "TotalTokenCalls",
		Aggregation:  "Total",
		Unit:         "Count",
	}: "total_token_calls",
	{
		ResourceType: "Microsoft.CognitiveServices/accounts",
		MetricName:   "ProcessedPromptTokens",
		Aggregation:  "Total",
		Unit:         "Count",
	}: "processed_prompt_tokens",
	{
		ResourceType: "Microsoft.CognitiveServices/accounts",
		MetricName:   "GeneratedTokens",
		Aggregation:  "Total",
		Unit:         "Count",
	}: "generated_completion_tokens",
	{
		ResourceType: "Microsoft.CognitiveServices/accounts",
		MetricName:   "TokensPerSecond",
		Aggregation:  "Average",
		Unit:         "Count",
	}: "tokens_per_second",
}

// legacyMetricName is the forward form of the existing azure_exporter scrape
// family. It derives the scrape name from the explicit Azure resource type,
// vendor metric name, aggregation, and unit. Native receiver names use
// receiverMetricName directly from the vendor metric name and aggregation.
func legacyMetricName(spec nativeMetricSpec) string {
	metric := strings.ToLower(strings.NewReplacer(" ", "_", "/", "_").Replace(spec.MetricName))
	if replacement, ok := legacyMetricTokenExceptions[nativeMetricContext{
		ResourceType: spec.ResourceType,
		MetricName:   spec.MetricName,
		Aggregation:  spec.Aggregation,
		Unit:         spec.Unit,
	}]; ok {
		metric = replacement
	}
	provider := strings.ToLower(strings.NewReplacer(".", "_", "/", "_").Replace(spec.ResourceType))
	return "azure_" + provider + "_" + metric + "_" + strings.ToLower(spec.Aggregation) + "_" + strings.ToLower(spec.Unit)
}

type nativeResourceMetrics struct {
	attrs   map[string]any
	metrics map[string]*otlp.Metric
}

func (c *construct) writeNativeMetrics(ctx context.Context, now time.Time, series []promrw.Series, w *core.World) error {
	if w == nil || w.OTLPMetrics == nil {
		return nil
	}

	byResource := map[string]*nativeResourceMetrics{}
	for _, s := range series {
		spec, ok := nativeMetricSpecByLegacy[s.Name]
		if !ok {
			continue
		}
		subID := s.Labels["subscriptionID"]
		subName := s.Labels["subscriptionName"]
		resourceKey := subID + "\x00" + subName
		resource := byResource[resourceKey]
		if resource == nil {
			resource = &nativeResourceMetrics{
				attrs: map[string]any{
					"azuremonitor.subscription":    subName,
					"azuremonitor.subscription_id": subID,
					"azuremonitor.tenant_id":       c.cfg.TenantID,
				},
				metrics: map[string]*otlp.Metric{},
			}
			if env := s.Labels["env"]; env != "" {
				resource.attrs["env"] = env
			}
			byResource[resourceKey] = resource
		}

		name := spec.receiverName()
		metric := resource.metrics[name]
		if metric == nil {
			metric = &otlp.Metric{
				Name: name,
				Unit: spec.Unit,
				Kind: otlp.MetricGauge,
			}
			resource.metrics[name] = metric
		}
		attrs := map[string]any{}
		// The receiver carries resource_id as a datapoint attribute because one
		// subscription resource block contains multiple Azure resources.
		if resourceID := nativeResourceID(spec, s.Labels); resourceID != "" {
			attrs["azuremonitor.resource_id"] = resourceID
		}
		for _, dimension := range spec.Dimensions {
			if value := nativeDimensionValue(s.Labels, dimension, len(spec.Dimensions)); value != "" {
				attrs[dimension] = value
			}
		}
		metric.Numbers = append(metric.Numbers, otlp.NumberPoint{
			Attrs: attrs,
			Time:  pointTime(s.T, now),
			Value: s.Value,
		})
	}

	keys := make([]string, 0, len(byResource))
	for key := range byResource {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	resources := make([]otlp.MetricResource, 0, len(keys))
	for _, key := range keys {
		resource := byResource[key]
		metricNames := make([]string, 0, len(resource.metrics))
		for name := range resource.metrics {
			metricNames = append(metricNames, name)
		}
		sort.Strings(metricNames)
		metrics := make([]otlp.Metric, 0, len(metricNames))
		for _, name := range metricNames {
			metric := resource.metrics[name]
			sort.SliceStable(metric.Numbers, func(i, j int) bool {
				return nativePointKey(metric.Numbers[i]) < nativePointKey(metric.Numbers[j])
			})
			metrics = append(metrics, *metric)
		}
		resources = append(resources, otlp.MetricResource{
			Attrs:   resource.attrs,
			Scope:   otlp.Scope{Name: azureMonitorReceiverScope},
			Metrics: metrics,
		})
	}
	if len(resources) == 0 {
		return nil
	}
	return w.OTLPMetrics.Write(ctx, resources)
}

// nativeResourceID retains the full Azure Monitor resource identity for native
// receiver output. The established serverless scrape contract intentionally
// collapses Storage blob/queue sub-services to the account resource ID, but the
// native receiver has no namespace attribute to distinguish equal metric names
// from those two sub-resources. Restoring the documented ARM suffix keeps their
// datapoint identities distinct without changing the legacy scrape labels.
func nativeResourceID(spec nativeMetricSpec, labels map[string]string) string {
	resourceID := labels["resourceID"]
	if resourceID == "" {
		return ""
	}
	switch spec.ResourceType {
	case "Microsoft.Storage/storageAccounts/blobServices":
		if !strings.HasSuffix(strings.ToLower(resourceID), "/blobservices/default") {
			return resourceID + "/blobServices/default"
		}
	case "Microsoft.Storage/storageAccounts/queueServices":
		if !strings.HasSuffix(strings.ToLower(resourceID), "/queueservices/default") {
			return resourceID + "/queueServices/default"
		}
	}
	return resourceID
}

func nativeDimensionValue(labels map[string]string, dimension string, dimensionCount int) string {
	if value := labels["dimension_"+dimension]; value != "" {
		return value
	}
	if value := labels["dimension"+dimension]; value != "" {
		return value
	}
	if dimensionCount == 1 {
		return labels["dimension"]
	}
	return ""
}

func pointTime(value, fallback time.Time) time.Time {
	if value.IsZero() {
		return fallback
	}
	return value
}

func nativePointKey(point otlp.NumberPoint) string {
	keys := make([]string, 0, len(point.Attrs))
	for key, value := range point.Attrs {
		keys = append(keys, key+"="+valueString(value))
	}
	sort.Strings(keys)
	return strings.Join(keys, "\x00")
}

func valueString(value any) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(toString(value), "\x00", ""), "\n", " "))
}

func toString(value any) string {
	if stringValue, ok := value.(string); ok {
		return stringValue
	}
	return "<non-string>"
}
