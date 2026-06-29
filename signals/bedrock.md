# AWS Bedrock CloudWatch (→ Mimir) — ScopeBlueprint

`AWS/Bedrock` (+ `/Agents`, `/Guardrails`) CloudWatch metric-stream families (metric stream →
Firehose → Mimir). Blueprint-scoped via `cloud.bedrock:` (shares the cloud account identity, like
RDS/ElastiCache). CW naming + five-stat expansion delegate to [`internal/cw`](../internal/cw); the
construct supplies only the `signals/`-sourced bases + value policy. Global rules + the cw-naming
LAW: [`00-canon.md`](00-canon.md), [`cw.md`](cw.md) `[slug: cw-naming]`.

*Provenance: AWS docs + the CW mangling convention confirmed empirically via `internal/cw`; the
AWS/Bedrock metric set itself is AWS-docs-sourced with no synthkit live capture → `v: assumed`,
SK-36. Mar-2026 metrics + ServiceTier/ContextWindow dims especially unverified.*

**Per-model magnitude weighting:** Bedrock construct emission is weighted per model by
`genai.VolumeWeight(modelId)` (request-volume multiplier) and cost metrics by
`genai.BlendedCostPerToken(modelId, inputFrac)`. The full catalogue of Bedrock model IDs,
families, costs, and weights lives in [`signals/genai-models.md`](genai-models.md)
`[slug: genai-models-bedrock]`. Default models emitted (current-generation):
`anthropic.claude-sonnet-4-6`, `anthropic.claude-haiku-4-5-20251001-v1:0`,
`amazon.nova-micro-v1:0`, `amazon.nova-pro-v1:0`,
`meta.llama3-1-8b-instruct-v1:0`, `amazon.titan-embed-text-v2:0`.

⚠ **Mangling traps (cw-law):** `EstimatedTPMQuotaUsage`→`estimated_tpmquota_usage` (**`tpmquota`**,
NOT `tpm_quota` — consecutive-caps collapse), `outputTokenCount`→`output_token_count` (CW source has
lowercase `o`; Prom form unaffected), `CloudWatch`→`cloud_watch`, `S3`→`s3`. Every base emits all
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
  - {root: output_token_count, type: gauge, unit: count, v: assumed, note: "CW source outputTokenCount (lowercase o)"}
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
