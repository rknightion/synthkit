# AWS Bedrock CloudWatch (→ Mimir) — ScopeBlueprint

`AWS/Bedrock` (+ `/Agents`, `/Guardrails`) CloudWatch metric-stream families (metric stream →
Firehose → Mimir). Blueprint-scoped via `cloud.bedrock:` (shares the cloud account identity, like
RDS/ElastiCache). CW naming + five-stat expansion delegate to [`internal/cw`](../internal/cw); the
construct supplies only the `signals/`-sourced bases + value policy. Global rules + the cw-naming
LAW: [`00-canon.md`](00-canon.md), [`cw.md`](cw.md) `[slug: cw-naming]`.

*Provenance: legacy generic model/Agents/Guardrails families are AWS-docs-sourced and remain
`v: assumed`, SK-36; Mar-2026 metrics + ServiceTier/ContextWindow dims especially unverified.
The application-profile subset below has a separate, bounded 2026-10-21 CloudWatch API capture.
That capture does not verify raw metric-stream OTLP or collector enrichment. CW mangling/five-stat
mechanics retain their existing independent contract in `internal/cw`.*

**Per-model magnitude weighting:** Bedrock construct emission is weighted per model by
`genai.VolumeWeight(modelId)` (request-volume multiplier) and cost metrics by
`genai.BlendedCostPerToken(modelId, inputFrac)`. The full catalogue of Bedrock model IDs,
families, costs, and weights lives in [`signals/genai-models.md`](genai-models.md)
`[slug: genai-models-bedrock]`. Default models emitted (current-generation):
`anthropic.claude-sonnet-4-6`, `anthropic.claude-haiku-4-5-20251001-v1:0`,
`amazon.nova-micro-v1:0`, `amazon.nova-pro-v1:0`,
`meta.llama3-1-8b-instruct-v1:0`, `amazon.titan-embed-text-v2:0`.

⚠ **Mangling traps (cw-law):** `EstimatedTPMQuotaUsage`→`estimated_tpmquota_usage` (**`tpmquota`**,
NOT `tpm_quota` — consecutive-caps collapse), core `OutputTokenCount`→`output_token_count` while
Agents `outputTokenCount`→`output_token_count` (Prom form unaffected), `CloudWatch`→`cloud_watch`,
`S3`→`s3`. Every base emits all
five stats (`_sum`/`_average`/`_maximum`/`_minimum`/`_sample_count`); `_sum` is a per-period GAUGE
(never `rate()`). ⚠ **No per-use-case dimension exists on Bedrock metrics** (predecessor §5.3.4) — never add one.

---

## AWS/Bedrock — `aws_bedrock_*` [slug: bedrock-core]

Dims `dimension_ModelId` (unless noted). Log-delivery six use dim "Across all model IDs".

```yaml signals
family: aws_bedrock
scope: blueprint
sink: promrw
stats: [_sum, _average, _maximum, _minimum, _sample_count]
labels:
  account_id: <account>
  region: <aws-region>
  namespace: AWS/Bedrock
  job: cloud/aws/bedrock
  name: <resource-arn|global>
  dimension_ModelId: <model-id>
  dimension_ServiceTier: <tier>          # optional, TTFT/TPMQuota only
  dimension_ResolvedServiceTier: <tier>  # optional, TTFT/TPMQuota only
  dimension_ContextWindow: <window>      # optional, TPMQuota >200K-token calls
metrics:
  - {root: invocations, type: gauge, unit: count, v: assumed}
  - {root: invocation_latency, type: gauge, unit: milliseconds, v: assumed}
  - {root: time_to_first_token, type: gauge, unit: milliseconds, v: assumed, note: "Mar-2026; streaming APIs only"}
  - {root: estimated_tpmquota_usage, type: gauge, unit: count, v: assumed, note: "Mar-2026 approximation; tpmquota NOT tpm_quota"}
  - {root: input_token_count, type: gauge, unit: count, v: assumed}
  - {root: output_token_count, type: gauge, unit: count, v: assumed, note: "CW source OutputTokenCount; Agents separately uses lowercase o"}
  - {root: cache_read_input_tokens, type: gauge, unit: count, v: assumed}
  - {root: cache_write_input_tokens, type: gauge, unit: count, v: assumed}
  - {root: invocation_throttles, type: gauge, unit: count, v: assumed}
  - {root: invocation_client_errors, type: gauge, unit: count, v: assumed}
  - {root: invocation_server_errors, type: gauge, unit: count, v: assumed}
  - {root: legacy_model_invocations, type: gauge, unit: count, v: assumed, note: "≈0; OutputImageCount omitted (no image-gen models)"}
  # log-delivery (dim "Across all model IDs") — Tier-1 record-path health
  - {root: model_invocation_logs_cloud_watch_delivery_success, type: gauge, unit: count, v: assumed}
  - {root: model_invocation_logs_cloud_watch_delivery_failure, type: gauge, unit: count, v: assumed}
  - {root: model_invocation_logs_s3_delivery_success, type: gauge, unit: count, v: assumed}
  - {root: model_invocation_logs_s3_delivery_failure, type: gauge, unit: count, v: assumed}
  - {root: model_invocation_large_data_s3_delivery_success, type: gauge, unit: count, v: assumed}
  - {root: model_invocation_large_data_s3_delivery_failure, type: gauge, unit: count, v: assumed}
```

## Application inference profiles — restricted `aws_bedrock_*` subset [slug: bedrock-application-profiles]

Optional `cloud.bedrock.application_profiles` associates each profile with an agent and a separate
`provider: bedrock` plus catalogued foundation `model`. **Those associations are configuration,
not additional CloudWatch dimensions or resource-tag readback.** Generation workloads must declare
the same provider/foundation model independently for Agent Observability pricing; never substitute
the profile ID as the generation model. No generation, backend-derived cost or team metric is emitted
by this construct. No profile declarations preserves the legacy output above unchanged. Select
`sub_signals: [application_profiles]` for profile-only output; omitting `sub_signals` still includes
the legacy families, on their own model/guardrail identities, never on profile IDs. Duplicate
`(account_id, region, profile ID)` declarations across environments within one blueprint are rejected,
even when profile emission is excluded; environment names do not disambiguate the wire identity.

**Observed source (2026-10-21, eu-west-1):** two temporary APPLICATION inference profiles copied
from EU Haiku 4.5 and EU Sonnet 4.6, two successful bounded Converse calls each, then deletion
verified by ResourceNotFound. ListMetrics showed namespace `AWS/Bedrock` and exactly one dimension:
`ModelId=<opaque application profile ID>` — not `InferenceProfileId`, a model name or an ARN.
GetMetricStatistics with period 60 returned the five bases below. `Invocations` had Sum 2,
Average/Minimum/Maximum 1 and SampleCount 2. Input tokens had Sum 44 and mean/min/max 22;
output tokens had sums 8/10 and means 4/5. Latency (milliseconds) had sums 1156/1350,
means 578/675, minima 555/672 and maxima 601/678. EstimatedTPMQuotaUsage had sums 84/94 and
means/minima/maxima 42/47. Every base had SampleCount 2. These tiny observations establish
shape, **not production rates, a quota formula, a latency distribution or model/region support**.

Resource API provenance: CreateInferenceProfile returned ARN/status ACTIVE; GetInferenceProfile
returned name, description, timestamps, ARN, model ARN list, opaque ID, status and type APPLICATION.
Agent/team tags were submitted at creation but persistence was not read back. The renderer does not
emit that resource inventory or infer any tag label. Public API references:
[inference profiles](https://docs.aws.amazon.com/bedrock/latest/userguide/inference-profiles.md) and
[creation/model source](https://docs.aws.amazon.com/bedrock/latest/userguide/inference-profiles-create.md).
No captured identifiers are retained in public examples.

**Synthetic value policy:** each profile requires an explicit positive `requests_per_min` with a
real-world basis beside the blueprint value, plus constant per-request input/output token counts,
latency milliseconds and estimated TPM quota usage. No generic model volume weight, traffic floor,
TTFT, cache, error/throttle, Agents, guardrail or logging family is inherited. UTC minute windows
schedule whole observations at fractional rates; idle windows emit no profile point, including after
an active window. For N observations of configured value V: Sum=N×V, Average=Minimum=Maximum=V,
SampleCount=N. For invocations, V=1. The constant-request model intentionally does not reproduce the
capture's latency spread. All five stats are independent per-period **gauges**, never cumulative
counters, and no histogram is emitted.

**Native stream source evidence (2026-10-21):** read-only inspection of an existing delivery-error
backup recovered four length-delimited OTLP `ExportMetricsServiceRequest` frames containing both
approved profiles. Their ten Summary datapoints cover the five bases above for each profile;
count, sum and minimum/maximum quantiles exactly match the captured CloudWatch API statistics.
Metric names are `amazonaws.com/AWS/Bedrock/<MetricName>`; units are `{Count}` or `ms` for latency.
Datapoint attributes are exactly `Namespace`, `MetricName` and `Dimensions`, with `ModelId` the sole
dimension. Scope name/version are empty. Resource keys include `cloud.provider`, `cloud.account.id`,
`cloud.region` and `aws.exporter.arn`. Captured values and identifiers are not public fixtures.
This is source-payload evidence from a failed delivery backup, not successful collector/backend
acceptance, resource-tag persistence or invocation-log evidence.

**Evidence boundary / PENDING:** the remote-write shape below uses the existing CW naming convention
and `name=global` (no inferred profile ARN or tag enrichment); it remains an offline projection,
not live collector parity. Any profile declaration with `cloudwatch_export: otlp` still fails closed,
even when its sub-signal is excluded. Native profile emission remains disabled until its complete
resource/enrichment contract is verified; profile logs, unconfirmed tags and unsupported families
remain gated. Generic Bedrock OTLP behavior without profiles is unchanged.

```yaml signals
family: aws_bedrock
scope: blueprint
sink: promrw
stats: [_sum, _average, _maximum, _minimum, _sample_count]
labels:
  account_id: <account>
  region: <aws-region>
  namespace: AWS/Bedrock
  job: cloud/aws/bedrock
  name: global
  dimension_ModelId: <fictional-application-profile-id>
metrics:
  - {root: invocations, type: gauge, unit: count, v: assumed, note: "API verified; profile collector projection not live-proven"}
  - {root: input_token_count, type: gauge, unit: count, v: assumed, note: "API verified; profile collector projection not live-proven"}
  - {root: output_token_count, type: gauge, unit: count, v: assumed, note: "API verified; profile collector projection not live-proven"}
  - {root: invocation_latency, type: gauge, unit: milliseconds, v: assumed, note: "API verified; profile collector projection not live-proven"}
  - {root: estimated_tpmquota_usage, type: gauge, unit: count, v: assumed, note: "API verified; profile collector projection not live-proven"}
```

## AWS/Bedrock/Agents — `aws_bedrock_agents_*` [slug: bedrock-agents]

Dim sets: `Operation` · `Operation+ModelId` · `Operation+AgentAliasArn+ModelId` (the three-dim set
emitted together). 13-metric set. (LangGraph-style workloads use the workload-AI lane not Bedrock Agents — low
volume / optional; kept for completeness.)

```yaml signals
family: aws_bedrock_agents
scope: blueprint
sink: promrw
stats: [_sum, _average, _maximum, _minimum, _sample_count]
labels:
  account_id: <account>
  region: <aws-region>
  namespace: AWS/Bedrock/Agents
  job: cloud/aws/bedrock
  name: <resource-arn|global>
  dimension_Operation: <operation>
  dimension_ModelId: <model-id>
  dimension_AgentAliasArn: <agent-alias-arn>
metrics:
  - {root: invocation_count, type: gauge, unit: count, v: assumed}
  - {root: total_time, type: gauge, unit: milliseconds, v: assumed}
  - {root: ttft, type: gauge, unit: milliseconds, v: assumed, note: "CW name really is TTFT; streaming only"}
  - {root: invocation_throttles, type: gauge, unit: count, v: assumed}
  - {root: invocation_server_errors, type: gauge, unit: count, v: assumed}
  - {root: invocation_client_errors, type: gauge, unit: count, v: assumed}
  - {root: model_latency, type: gauge, unit: milliseconds, v: assumed}
  - {root: model_invocation_count, type: gauge, unit: count, v: assumed}
  - {root: model_invocation_throttles, type: gauge, unit: count, v: assumed}
  - {root: model_invocation_client_errors, type: gauge, unit: count, v: assumed}
  - {root: model_invocation_server_errors, type: gauge, unit: count, v: assumed}
  - {root: input_token_count, type: gauge, unit: count, v: assumed}
  - {root: output_token_count, type: gauge, unit: count, v: assumed}
```

## AWS/Bedrock/Guardrails — `aws_bedrock_guardrails_*` [slug: bedrock-guardrails]

Dims `GuardrailArn`, `GuardrailVersion`, `GuardrailPolicyType`, `GuardrailContentSource`,
`Operation`=`ApplyGuardrail`. ⚠ Arn×Version = cardinality watch. (Automated-reasoning extras
`FindingCounts`/`TotalFindings` exist but are niche — not emitted.)

```yaml signals
family: aws_bedrock_guardrails
scope: blueprint
sink: promrw
stats: [_sum, _average, _maximum, _minimum, _sample_count]
labels:
  account_id: <account>
  region: <aws-region>
  namespace: AWS/Bedrock/Guardrails
  job: cloud/aws/bedrock
  name: <resource-arn|global>
  dimension_GuardrailArn: <guardrail-arn>
  dimension_GuardrailVersion: <version>
  dimension_GuardrailPolicyType: <policy-type>
  dimension_GuardrailContentSource: <content-source>
  dimension_Operation: ApplyGuardrail
metrics:
  - {root: invocations, type: gauge, unit: count, v: assumed}
  - {root: invocations_intervened, type: gauge, unit: count, v: assumed, note: "key signal"}
  - {root: invocation_latency, type: gauge, unit: milliseconds, v: assumed}
  - {root: invocation_client_errors, type: gauge, unit: count, v: assumed}
  - {root: invocation_server_errors, type: gauge, unit: count, v: assumed}
  - {root: invocation_throttles, type: gauge, unit: count, v: assumed}
  - {root: text_unit_count, type: gauge, unit: count, v: assumed}
```

## Bedrock invocation log — `source=bedrock_invocation` [slug: bedrock-logs]

Loki stream labels low-card; high-card (`requestId`,`correlation_id`) → structured metadata. Body
fields: `timestamp`, `requestId`, `operation`, `modelId`, `requestMetadata_use_case`,
`requestMetadata_correlation_id`, `input_inputTokenCount`, `output_outputTokenCount` — **bodies
stripped** (`inputBodyJson`/`outputBodyJson` never emitted). Per-use-case attribution lives
ONLY here (logs-only — no Bedrock metric carries a use-case dimension).
