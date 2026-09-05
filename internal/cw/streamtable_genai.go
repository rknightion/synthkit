// SPDX-License-Identifier: AGPL-3.0-only

package cw

// AWS reference pages used for the entries below (Context7 library ID
// /websites/aws_amazon_bedrock, checked on 2026-09-05; table rows were also opened at the
// cited official sources):
//   - Amazon Bedrock runtime metrics:
//     https://docs.aws.amazon.com/bedrock/latest/userguide/monitoring-runtime-metrics.html
//   - Amazon Bedrock Agents metrics:
//     https://docs.aws.amazon.com/bedrock/latest/userguide/monitoring-agents-cw-metrics.html
//   - Amazon Bedrock Guardrails metrics:
//     https://docs.aws.amazon.com/bedrock/latest/userguide/monitoring-guardrails-cw-metrics.html
//   - Amazon Bedrock AgentCore service-provided observability:
//     https://docs.aws.amazon.com/bedrock-agentcore/latest/devguide/observability-runtime-metrics.html
//     https://docs.aws.amazon.com/bedrock-agentcore/latest/devguide/observability-service-provided.html
//     https://docs.aws.amazon.com/bedrock-agentcore/latest/devguide/observability-tool-metrics.html
//   - CloudWatch Metric Streams OpenTelemetry unit translation:
//     https://docs.aws.amazon.com/AmazonCloudWatch/latest/monitoring/CloudWatch-metric-streams-formats-opentelemetry-translation-100.html
//
// The six Bedrock model-invocation logging names are intentionally absent: the runtime page lists
// the names and the "Across all model IDs" wording but gives neither a unit nor a dimension name.
// The twelve AgentCore bases are intentionally absent as well: the runtime page does not give
// units/dimensions for its invocation-class list, and its resource-usage units (vCPU-Hours and
// GB-Hours) have no mapping in the permitted CloudWatch Metric Streams OTLP unit table.
//
// Omitted Bedrock logging-delivery bases:
//
//	aws_bedrock_model_invocation_logs_cloud_watch_delivery_failure
//	aws_bedrock_model_invocation_logs_cloud_watch_delivery_success
//	aws_bedrock_model_invocation_logs_s3_delivery_failure
//	aws_bedrock_model_invocation_logs_s3_delivery_success
//	aws_bedrock_model_invocation_large_data_s3_delivery_failure
//	aws_bedrock_model_invocation_large_data_s3_delivery_success
//
// Omitted AgentCore bases:
//
//	aws_bedrock_agentcore_active_streaming_connections
//	aws_bedrock_agentcore_cpu_used_v_cpu_hours
//	aws_bedrock_agentcore_inbound_streaming_bytes_processed
//	aws_bedrock_agentcore_invocations
//	aws_bedrock_agentcore_latency
//	aws_bedrock_agentcore_memory_used_gb_hours
//	aws_bedrock_agentcore_outbound_streaming_bytes_processed
//	aws_bedrock_agentcore_session_count
//	aws_bedrock_agentcore_system_errors
//	aws_bedrock_agentcore_throttles
//	aws_bedrock_agentcore_total_errors
//	aws_bedrock_agentcore_user_errors
func streamTableGenAI() streamTable {
	return streamTable{
		entries: map[string]StreamEntry{
			// AWS/Bedrock runtime metrics. The source page documents ModelId for all
			// runtime metrics retained here; OutputImageCount is not a construct base.
			// The runtime page spells this metric OutputTokenCount; the Agents page
			// separately spells its corresponding metric outputTokenCount.
			"aws_bedrock_cache_read_input_tokens":  {Namespace: "AWS/Bedrock", MetricName: "CacheReadInputTokens", Unit: "{Count}"},
			"aws_bedrock_cache_write_input_tokens": {Namespace: "AWS/Bedrock", MetricName: "CacheWriteInputTokens", Unit: "{Count}"},
			"aws_bedrock_estimated_tpmquota_usage": {Namespace: "AWS/Bedrock", MetricName: "EstimatedTPMQuotaUsage", Unit: "{Count}"},
			"aws_bedrock_input_token_count":        {Namespace: "AWS/Bedrock", MetricName: "InputTokenCount", Unit: "{Count}"},
			"aws_bedrock_invocation_client_errors": {Namespace: "AWS/Bedrock", MetricName: "InvocationClientErrors", Unit: "{Count}"},
			"aws_bedrock_invocation_latency":       {Namespace: "AWS/Bedrock", MetricName: "InvocationLatency", Unit: "ms"},
			"aws_bedrock_invocation_server_errors": {Namespace: "AWS/Bedrock", MetricName: "InvocationServerErrors", Unit: "{Count}"},
			"aws_bedrock_invocation_throttles":     {Namespace: "AWS/Bedrock", MetricName: "InvocationThrottles", Unit: "{Count}"},
			"aws_bedrock_invocations":              {Namespace: "AWS/Bedrock", MetricName: "Invocations", Unit: "{Count}"},
			"aws_bedrock_legacy_model_invocations": {Namespace: "AWS/Bedrock", MetricName: "LegacyModelInvocations", Unit: "{Count}"},
			"aws_bedrock_output_token_count":       {Namespace: "AWS/Bedrock", MetricName: "OutputTokenCount", Unit: "{Count}"},
			"aws_bedrock_time_to_first_token":      {Namespace: "AWS/Bedrock", MetricName: "TimeToFirstToken", Unit: "ms"},

			// AWS/Bedrock/Agents. The three-dimension form is documented for every
			// metric in this retained family.
			"aws_bedrock_agents_input_token_count":              {Namespace: "AWS/Bedrock/Agents", MetricName: "InputTokenCount", Unit: "{Count}"},
			"aws_bedrock_agents_invocation_client_errors":       {Namespace: "AWS/Bedrock/Agents", MetricName: "InvocationClientErrors", Unit: "{Count}"},
			"aws_bedrock_agents_invocation_count":               {Namespace: "AWS/Bedrock/Agents", MetricName: "InvocationCount", Unit: "{Count}"},
			"aws_bedrock_agents_invocation_server_errors":       {Namespace: "AWS/Bedrock/Agents", MetricName: "InvocationServerErrors", Unit: "{Count}"},
			"aws_bedrock_agents_invocation_throttles":           {Namespace: "AWS/Bedrock/Agents", MetricName: "InvocationThrottles", Unit: "{Count}"},
			"aws_bedrock_agents_model_invocation_client_errors": {Namespace: "AWS/Bedrock/Agents", MetricName: "ModelInvocationClientErrors", Unit: "{Count}"},
			"aws_bedrock_agents_model_invocation_count":         {Namespace: "AWS/Bedrock/Agents", MetricName: "ModelInvocationCount", Unit: "{Count}"},
			"aws_bedrock_agents_model_invocation_server_errors": {Namespace: "AWS/Bedrock/Agents", MetricName: "ModelInvocationServerErrors", Unit: "{Count}"},
			"aws_bedrock_agents_model_invocation_throttles":     {Namespace: "AWS/Bedrock/Agents", MetricName: "ModelInvocationThrottles", Unit: "{Count}"},
			"aws_bedrock_agents_model_latency":                  {Namespace: "AWS/Bedrock/Agents", MetricName: "ModelLatency", Unit: "ms"},
			"aws_bedrock_agents_output_token_count":             {Namespace: "AWS/Bedrock/Agents", MetricName: "outputTokenCount", Unit: "{Count}"},
			"aws_bedrock_agents_total_time":                     {Namespace: "AWS/Bedrock/Agents", MetricName: "TotalTime", Unit: "ms"},
			"aws_bedrock_agents_ttft":                           {Namespace: "AWS/Bedrock/Agents", MetricName: "TTFT", Unit: "ms"},

			// AWS/Bedrock/Guardrails. The policy-type dimension is documented only
			// for InvocationsIntervened and TextUnitCount below.
			"aws_bedrock_guardrails_invocation_client_errors": {Namespace: "AWS/Bedrock/Guardrails", MetricName: "InvocationClientErrors", Unit: "{Count}"},
			"aws_bedrock_guardrails_invocation_latency":       {Namespace: "AWS/Bedrock/Guardrails", MetricName: "InvocationLatency", Unit: "ms"},
			"aws_bedrock_guardrails_invocation_server_errors": {Namespace: "AWS/Bedrock/Guardrails", MetricName: "InvocationServerErrors", Unit: "{Count}"},
			"aws_bedrock_guardrails_invocation_throttles":     {Namespace: "AWS/Bedrock/Guardrails", MetricName: "InvocationThrottles", Unit: "{Count}"},
			"aws_bedrock_guardrails_invocations":              {Namespace: "AWS/Bedrock/Guardrails", MetricName: "Invocations", Unit: "{Count}"},
			"aws_bedrock_guardrails_invocations_intervened":   {Namespace: "AWS/Bedrock/Guardrails", MetricName: "InvocationsIntervened", Unit: "{Count}"},
			"aws_bedrock_guardrails_text_unit_count":          {Namespace: "AWS/Bedrock/Guardrails", MetricName: "TextUnitCount", Unit: "{Count}"},
		},
		dimensions: map[string]map[string]string{
			// AWS/Bedrock runtime metrics: ModelId.
			"aws_bedrock_cache_read_input_tokens": {
				"dimension_ModelId": "ModelId",
			},
			"aws_bedrock_cache_write_input_tokens": {
				"dimension_ModelId": "ModelId",
			},
			"aws_bedrock_estimated_tpmquota_usage": {
				"dimension_ModelId": "ModelId",
			},
			"aws_bedrock_input_token_count": {
				"dimension_ModelId": "ModelId",
			},
			"aws_bedrock_invocation_client_errors": {
				"dimension_ModelId": "ModelId",
			},
			"aws_bedrock_invocation_latency": {
				"dimension_ModelId": "ModelId",
			},
			"aws_bedrock_invocation_server_errors": {
				"dimension_ModelId": "ModelId",
			},
			"aws_bedrock_invocation_throttles": {
				"dimension_ModelId": "ModelId",
			},
			"aws_bedrock_invocations": {
				"dimension_ModelId": "ModelId",
			},
			"aws_bedrock_legacy_model_invocations": {
				"dimension_ModelId": "ModelId",
			},
			"aws_bedrock_output_token_count": {
				"dimension_ModelId": "ModelId",
			},
			"aws_bedrock_time_to_first_token": {
				"dimension_ModelId": "ModelId",
			},

			// AWS/Bedrock/Agents: Operation, ModelId, AgentAliasArn.
			"aws_bedrock_agents_input_token_count": {
				"dimension_Operation":     "Operation",
				"dimension_ModelId":       "ModelId",
				"dimension_AgentAliasArn": "AgentAliasArn",
			},
			"aws_bedrock_agents_invocation_client_errors": {
				"dimension_Operation":     "Operation",
				"dimension_ModelId":       "ModelId",
				"dimension_AgentAliasArn": "AgentAliasArn",
			},
			"aws_bedrock_agents_invocation_count": {
				"dimension_Operation":     "Operation",
				"dimension_ModelId":       "ModelId",
				"dimension_AgentAliasArn": "AgentAliasArn",
			},
			"aws_bedrock_agents_invocation_server_errors": {
				"dimension_Operation":     "Operation",
				"dimension_ModelId":       "ModelId",
				"dimension_AgentAliasArn": "AgentAliasArn",
			},
			"aws_bedrock_agents_invocation_throttles": {
				"dimension_Operation":     "Operation",
				"dimension_ModelId":       "ModelId",
				"dimension_AgentAliasArn": "AgentAliasArn",
			},
			"aws_bedrock_agents_model_invocation_client_errors": {
				"dimension_Operation":     "Operation",
				"dimension_ModelId":       "ModelId",
				"dimension_AgentAliasArn": "AgentAliasArn",
			},
			"aws_bedrock_agents_model_invocation_count": {
				"dimension_Operation":     "Operation",
				"dimension_ModelId":       "ModelId",
				"dimension_AgentAliasArn": "AgentAliasArn",
			},
			"aws_bedrock_agents_model_invocation_server_errors": {
				"dimension_Operation":     "Operation",
				"dimension_ModelId":       "ModelId",
				"dimension_AgentAliasArn": "AgentAliasArn",
			},
			"aws_bedrock_agents_model_invocation_throttles": {
				"dimension_Operation":     "Operation",
				"dimension_ModelId":       "ModelId",
				"dimension_AgentAliasArn": "AgentAliasArn",
			},
			"aws_bedrock_agents_model_latency": {
				"dimension_Operation":     "Operation",
				"dimension_ModelId":       "ModelId",
				"dimension_AgentAliasArn": "AgentAliasArn",
			},
			"aws_bedrock_agents_output_token_count": {
				"dimension_Operation":     "Operation",
				"dimension_ModelId":       "ModelId",
				"dimension_AgentAliasArn": "AgentAliasArn",
			},
			"aws_bedrock_agents_total_time": {
				"dimension_Operation":     "Operation",
				"dimension_ModelId":       "ModelId",
				"dimension_AgentAliasArn": "AgentAliasArn",
			},
			"aws_bedrock_agents_ttft": {
				"dimension_Operation":     "Operation",
				"dimension_ModelId":       "ModelId",
				"dimension_AgentAliasArn": "AgentAliasArn",
			},

			// AWS/Bedrock/Guardrails: Operation, GuardrailContentSource,
			// GuardrailArn, GuardrailVersion for the base invocation family.
			"aws_bedrock_guardrails_invocation_client_errors": {
				"dimension_Operation":              "Operation",
				"dimension_GuardrailContentSource": "GuardrailContentSource",
				"dimension_GuardrailArn":           "GuardrailArn",
				"dimension_GuardrailVersion":       "GuardrailVersion",
			},
			"aws_bedrock_guardrails_invocation_latency": {
				"dimension_Operation":              "Operation",
				"dimension_GuardrailContentSource": "GuardrailContentSource",
				"dimension_GuardrailArn":           "GuardrailArn",
				"dimension_GuardrailVersion":       "GuardrailVersion",
			},
			"aws_bedrock_guardrails_invocation_server_errors": {
				"dimension_Operation":              "Operation",
				"dimension_GuardrailContentSource": "GuardrailContentSource",
				"dimension_GuardrailArn":           "GuardrailArn",
				"dimension_GuardrailVersion":       "GuardrailVersion",
			},
			"aws_bedrock_guardrails_invocation_throttles": {
				"dimension_Operation":              "Operation",
				"dimension_GuardrailContentSource": "GuardrailContentSource",
				"dimension_GuardrailArn":           "GuardrailArn",
				"dimension_GuardrailVersion":       "GuardrailVersion",
			},
			"aws_bedrock_guardrails_invocations": {
				"dimension_Operation":              "Operation",
				"dimension_GuardrailContentSource": "GuardrailContentSource",
				"dimension_GuardrailArn":           "GuardrailArn",
				"dimension_GuardrailVersion":       "GuardrailVersion",
			},
			// GuardrailPolicyType is additionally documented for these two metrics.
			"aws_bedrock_guardrails_invocations_intervened": {
				"dimension_Operation":              "Operation",
				"dimension_GuardrailContentSource": "GuardrailContentSource",
				"dimension_GuardrailPolicyType":    "GuardrailPolicyType",
				"dimension_GuardrailArn":           "GuardrailArn",
				"dimension_GuardrailVersion":       "GuardrailVersion",
			},
			"aws_bedrock_guardrails_text_unit_count": {
				"dimension_Operation":              "Operation",
				"dimension_GuardrailContentSource": "GuardrailContentSource",
				"dimension_GuardrailPolicyType":    "GuardrailPolicyType",
				"dimension_GuardrailArn":           "GuardrailArn",
				"dimension_GuardrailVersion":       "GuardrailVersion",
			},
		},
	}
}
