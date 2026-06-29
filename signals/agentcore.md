# AWS Bedrock AgentCore CloudWatch (→ Mimir) — ScopeBlueprint

`AWS/Bedrock-AgentCore` vended CloudWatch metrics (metric stream → Firehose → Mimir; the GC managed
scraper does NOT list this namespace, so the stream is the only metric path). Blueprint-scoped via
`cloud.agentcore:`. CW naming + five-stat expansion delegate to [`internal/cw`](../internal/cw).
Account coverage (which envs emit AgentCore) is Config-driven — not every account runs AgentCore.

*Provenance: predecessor synthetics/SIGNALS.md §2.4 (AWS docs 2026-06-10). `v: assumed`, SK-37 — two
distinct uncertainty regimes below.*

⚠ **Namespace mangling:** `AWS/Bedrock-AgentCore` → prefix `aws_bedrock_agentcore_`; `namespace`
label = the original CW string. Each base emits all five stats; `_sum` is a per-period gauge.

---

## Invocation-class — `aws_bedrock_agentcore_*` [slug: agentcore-invocation]

⚠ **Names are PROSE-DERIVED — AWS publishes no CW name table for these (SK-37).** ⚠ **Dimensions on
invocation-class metrics are UNDOCUMENTED — synthkit OMITS `dimension_Service/Resource/Name` here**
(do not invent dims; only base CW labels). This is the I2-reviewed position (the predecessor stamped
Service/Resource/Name flagged Ⓐ; we instead omit undocumented dims per names-are-law).

```yaml signals
family: aws_bedrock_agentcore
scope: blueprint
sink: promrw
stats: [_sum, _average, _maximum, _minimum, _sample_count]
labels:
  account_id: <account>
  region: <aws-region>
  namespace: AWS/Bedrock-AgentCore
  job: cloud/aws/bedrock-agentcore
  name: <resource-arn|global>
metrics:   # prose-derived names (no CW table); dims undocumented → omitted
  - {root: invocations, type: gauge, unit: count, v: assumed}
  - {root: latency, type: gauge, unit: milliseconds, v: assumed}
  - {root: throttles, type: gauge, unit: count, v: assumed}
  - {root: system_errors, type: gauge, unit: count, v: assumed}
  - {root: user_errors, type: gauge, unit: count, v: assumed}
  - {root: total_errors, type: gauge, unit: count, v: assumed}
  - {root: session_count, type: gauge, unit: count, v: assumed}
  - {root: active_streaming_connections, type: gauge, unit: count, v: assumed}
  - {root: inbound_streaming_bytes_processed, type: gauge, unit: bytes, v: assumed}
  - {root: outbound_streaming_bytes_processed, type: gauge, unit: bytes, v: assumed}
```

## Resource-usage — `aws_bedrock_agentcore_*` (hyphen+unit names) [slug: agentcore-resource-usage]

⚠ Resource-usage CW names are ✅ **exact with hyphens + embedded units** (`CPUUsed-vCPUHours`,
`MemoryUsed-GBHours`); the hyphen→underscore mangled form is `v: assumed` (SK-37). These DO carry
the documented dims `Service`=`AgentCore.Runtime`, `Resource`=agent ARN, `Name`=`AgentName::EndpointName`.

```yaml signals
family: aws_bedrock_agentcore   # same namespace as invocation-class; distinct dim regime (resource-usage)
scope: blueprint
sink: promrw
stats: [_sum, _average, _maximum, _minimum, _sample_count]
labels:
  account_id: <account>
  region: <aws-region>
  namespace: AWS/Bedrock-AgentCore
  job: cloud/aws/bedrock-agentcore
  name: <resource-arn|global>
  dimension_Service: AgentCore.Runtime
  dimension_Resource: <agent-arn>
  dimension_Name: <AgentName::EndpointName>
metrics:
  - {root: cpu_used_v_cpu_hours, type: gauge, unit: vcpu_hours, v: assumed, note: "CW CPUUsed-vCPUHours; hyphen mangling unverified (SK-37)"}
  - {root: memory_used_gb_hours, type: gauge, unit: gb_hours, v: assumed, note: "CW MemoryUsed-GBHours; hyphen mangling unverified (SK-37)"}
```

Gateway (MCP `tools/call`: `Duration`, `TargetExecutionTime`; dims `Operation,Protocol,Method,Resource,Name`),
Memory, Identity, Policy sub-resource metrics exist ✅ — emit the **Gateway** ones only under an
MCP-tooling storyline (Config sub-signal); others omitted.

## AgentCore logs [slug: agentcore-logs]

*Provenance: synthkit construct implementation 2026-06-16 (`v: assumed` — AWS publishes no
normative log schema; shapes derived from AWS documentation + CW metric realism, NEVER validated
against a real AgentCore workload. Tracked as **cantfind SK-71** (app + usage shapes + deferred
spans) — resolve via live-capture of a real Bedrock AgentCore runtime.)*

### `source=agentcore_app` — APPLICATION_LOGS (always emitted)

Content-stripped runtime diagnostics. One line per declared agent per tick; no
prompt/response content.

```yaml signals
family: agentcore_app
scope: blueprint
sink: loki
stream_labels:
  account_id: <account>
  region: <aws-region>
  job: cloud/aws/bedrock-agentcore
  source: agentcore_app
  level: info
  env: <env-name>           # omitted if fixture.Env is nil
structured_metadata:        # Line.Meta — high-card, never stream labels
  session_id: sess-<agent>-<unix-ts>
  trace_id: tid-<agent>-<hex-ns>
body_json:
  msg: "agent step completed"
  agent: <agent-name>       # e.g. "planner", "retriever"
  event: <phase>            # agent_start | tool_invoke | tool_result | agent_step | agent_end
```

### `source=agentcore_usage` — USAGE_LOGS (vended; Firehose→Loki; opt-in via `usage_logs` sub-signal)

Per-second CPU + memory snapshot, consistent with CW `cpu_used_v_cpu_hours` /
`memory_used_gb_hours` resource-usage metrics (values scale with business factor).
One line per declared agent per tick.

```yaml signals
family: agentcore_usage
scope: blueprint
sink: loki
stream_labels:
  account_id: <account>
  region: <aws-region>
  job: cloud/aws/bedrock-agentcore
  source: agentcore_usage
  env: <env-name>           # omitted if fixture.Env is nil
structured_metadata:        # Line.Meta — high-card, never stream labels
  session_id: sess-<agent>-<unix-ts>
body_json:
  cpu: "<float4>"           # vCPU fraction per second ≈ 0.05–0.25
  memory: "<float4>"        # GB per second ≈ 0.10–0.50
```

### `source=agentcore_spans` — DEFERRED

**OPTIONAL, DEFERRED.** The `aws/spans` service-vended span records
(CW Logs subscription → Firehose → Loki as queryable JSON; X-Ray format: `aws.operation.name`,
`aws.resource.arn`, `aws.request_id`, `aws.agent.id`, `session.id`, `latency_ms`, `error_type` +
trace/span IDs in structured metadata). These spans are CloudWatch-only and **never reach Tempo**
(real constraint preserved). Record-envelope JSON unverified. Deferred to a scenario-driven pass —
NOT part of the v1 request-correlation set.
