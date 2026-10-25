---
title: Incidents & Scenarios
description: Declaring and activating failure modes — named scenario bundles, scheduled incidents, and live control-plane activation.
---

# Incidents & Scenarios

The incident model separates **what can happen** (the failure-mode vocabulary declared by each construct/workload) from **when it happens** (activation via a scheduled `incidents:` block or a live control-plane call).

A failure mode is not a synthetic error injection in a naive sense — it shifts the shape engine's multiplier for specific constructs so that the affected signal families brown out realistically: latency histograms right-tail, error counters climb, pod restarts increase, connection gauges approach max. The rest of the estate stays healthy.

## Scenarios: named failure bundles

A `scenarios:` block defines **reusable named bundles** of effects. Each effect declares a mode, a target instance, and an intensity in `[0, 1]`:

```yaml
scenarios:
  - name: database_overload
    title: Database overload storm
    summary: >
      Aurora connections saturate and query latency spikes;
      the API backend browns out.
    effects:
      - { mode: connection_saturation, target: mine-app-db,  intensity: 0.9 }
      - { mode: slow_query_storm,      target: mine-app-db,  intensity: 0.7 }
      - { mode: latency_spike,         target: mine-api,     intensity: 0.5 }

  - name: cluster_instability
    title: Cluster pod crash-loop
    effects:
      - { mode: pod_crashloop, target: mine-prod-use1, intensity: 0.4 }
      - { mode: oom_kill,      target: mine-prod-use1, intensity: 0.3 }
```

### Effect target addressing

The `target` field on an effect resolves to a specific declared instance:

| Target form | Meaning |
|---|---|
| `mine-app-db` | Exact instance name — the named database, cluster, workload, or service node |
| `database:*` | All database instances on the `database` axis |
| `cluster:*` | All k8s cluster instances |
| `workload:*` | All workload instances |
| `service:*` | All app service nodes across all app workloads |
| `cloud:*` | All cloud-scoped constructs (Bedrock, AgentCore, Portkey, etc.) |
| `network:*` | All network-topology instances |
| `orders_daily.transform` | One declared pipeline stage, named `<pipeline>.<stage>` (see [pipeline_stage axis](#pipeline_stage-axis)) |
| `pipeline_stage:*` | Every declared pipeline stage |
| `orders-publisher` | One function declared under an environment's `cloud.lambda.functions` (see [function axis](#function-axis-lambda)) |
| `function:*` | Every declared Lambda function |
| `orders-out` | One queue declared under `integrations.solace` (see [queue axis](#queue-axis-solace)) |
| `queue:*` | Every declared Solace queue |
| `orders` | One table declared under an environment's `cloud.dynamodb.tables` (see [table axis](#table-axis-dynamodb)) |
| `table:*` | Every declared DynamoDB table |
| `checkout-pool` | One causal resource declared under an environment's `resources` (see [resource axis](#resource-axis-causal-resources)); a resource named `<base>-<lower(env)>` is also reached by `<base>` |
| `resource:*` | Every declared causal resource |
| omitted | Valid only for single-axis modes — the mode's axis is inferred and all instances of that axis are targeted |

The resolver validates every effect target against the blueprint's actual instance inventory at load time. An unknown name or an axis mismatch is a loud load error.

A mode that appears on more than one axis (e.g. `lock_contention` exists on both `database` and `service`) **requires** an explicit target — the empty-target shorthand is rejected at load to avoid ambiguous targeting.

## Incidents: scheduled activations

An `incidents:` block schedules when a scenario or single-mode effect fires:

=== "Scenario reference"

    ```yaml
    incidents:
      - scenario: database_overload
        at: "2026-07-01T14:00"   # ISO-8601 wall time (local to the blueprint's timezone)
        for: 30m
    ```

=== "Single-mode (inline)"

    ```yaml
    incidents:
      - kind: pod_crashloop
        target: mine-prod-use1
        at: "2026-07-01T11:00"
        for: 15m
        intensity: 0.6
    ```

=== "Recurring (every)"

    ```yaml
    incidents:
      # Non-prod ambient churn: fires every 30 minutes, lasts 8 minutes.
      - kind: oom_kill
        target: mine-dev-use1
        every: 30m
        for: 8m
        intensity: 0.15
    ```

`scenario:` and `kind:` are mutually exclusive in one entry, and an entry needs exactly one of `at:` or `every:`. `at:` accepts either a full wall-clock timestamp (fires once) or a bare `HH:MM` (fires daily at that time); `every:` fires on the given interval continuously for the lifetime of the process.

`intensity` is in `[0, 1]`: explicit `intensity: 0` is preserved as zero, while omission
defaults to 1 (the maximum effect the construct physics implement). Fractional values are
preserved. This presence rule applies to inline single-mode incidents and to each effect in a
scheduled scenario reference; set the intensity on the scenario's effect, not on its referencing
incident. Zero-intensity effects still undergo normal mode and target validation.

For intensity-scaled effects, zero leaves the baseline unchanged and fractional intensities
scale the effect. A zero-intensity window still exists: consumers that check only whether a
window is active ignore intensity (for example, `node_not_ready` and `stage_stall`). End or omit
the window to disable those effects. Scheduled presence handling does not change live
resolved-zero values or live activation semantics.

### Rate-only warning

A scenario, or a single-mode incident row, that scales a request-path rate, error share, latency
or throughput with no declared cause is reported as a validation warning. It is non-fatal: the
blueprint still loads, and the warning appears in the loader log, the `warnings` of
`synthkit validate`, and the control-plane diagnostics beside the other resolve-time warnings.

The warned modes are `error_spike`, `latency_storm`, `throughput_drop`, `retry_storm` and
`fallback_storm` on the service axis; `error_burst` and `latency_spike` on the workload axis;
`function_error_spike`, `function_duration_storm` and `function_throttle_storm` on the function
axis; `queue_consumer_stall` and `queue_publish_drop` on the queue axis; and `table_throttle_storm`
on the table axis. A scenario is warned when at least one effect is in that set and none is a resource-axis effect. A resource-axis
effect (for example `dependency_slow` or `concurrency_reduced` on a declared
`environments[].resources` target) is the declared cause, so it clears the warning for the whole
scenario. Cluster, database, cloud, network, GPU, pipeline-stage, quality and runtime-pressure
modes (`cpu_hotspot`, `memory_leak`, `goroutine_leak`, `lock_contention`) are never warned. A
scenario that an incident references is warned once, on the scenario, not again on the schedule row.

## Available failure modes

### cluster axis

| Mode | Effect |
|---|---|
| `oom_kill` | Containers OOM-killed; restart count climbs, status reason OOMKilled. `intensity` selects the fraction of pods affected. |
| `pod_crashloop` | Pods crash-looping; restarts climb, phase Pending not Running. `intensity` selects fraction of pods. |
| `node_not_ready` | A node flips NotReady; its pods go Pending. |

### database axis

| Mode | Effect |
|---|---|
| `connection_saturation` | Active connections climb toward max. |
| `replication_lag` | Replica falls behind primary. |
| `lock_contention` | Lock waits climb. (Also fires the `query_data_locks` dbo11y op when active.) |
| `slow_query_storm` | Query latency right-tail spikes. |

A database with `resource:` (bound to a `dependency` or `pool` under `environments[].resources`) is not
a database-axis target: an effect naming it with a database-axis mode is an unknown-target load error.
Its `aws_rds_*` latency and connection families follow the shared resource model instead, so the
resource-axis modes reach it: `dependency_slow` on the dependency (or on the dependency the pool is
held across) multiplies `aws_rds_read_latency` and `aws_rds_write_latency` by the dependency's latency
factor, and on a pool-bound database `aws_rds_database_connections` follows the pool's busy
connections (they rise as the hold stretches, and `pool_exhausted` caps them at the reduced size).
Every other family keeps its baseline, and each family returns to the baseline once the incident ends.
`dependency_unavailable` and `credential_expired` move nothing on a bound database, because no sourced
CloudWatch metric records them. A bound DocumentDB cluster (`engine: docdb`) behaves the same way:
`aws_docdb_read_latency` and `aws_docdb_write_latency` follow the latency factor, and
`aws_docdb_database_connections` follows a pool's busy connections.

### workload axis (web_service)

| Mode | Effect |
|---|---|
| `latency_spike` | Elevated request latency (up to 4× at full intensity). |
| `error_burst` | Elevated 5xx error rate. |
| `cpu_hotspot` | CPU concentrated in a hot frame (profiling flamegraph). |
| `memory_leak` | Growing heap (profile sample values rise). |
| `lock_contention` | Elevated mutex/block contention (profile values). |
| `goroutine_leak` | Goroutine accumulation (profile values). |

### service axis (app workload — per-node)

| Mode | Effect |
|---|---|
| `latency_storm` | Elevated latency on the targeted service node. |
| `error_spike` | Elevated 5xx rate on the targeted service node. |
| `throughput_drop` | Reduced throughput on the targeted service node. |
| `fallback_storm` | Elevated gateway fallback rate on the targeted service node. |
| `retry_storm` | Elevated gateway retry rate on the targeted service node. |
| `cpu_hotspot` | Hot frame amplification on the targeted node (profiling). |
| `memory_leak` | Growing heap on the targeted node (profiling). |
| `lock_contention` | Mutex/block contention on the targeted node (profiling). |
| `goroutine_leak` | Goroutine accumulation on the targeted node (profiling). |
| `web_vitals_degraded` | Browser web-vitals degrade on the targeted frontend node — LCP/INP/TTFB/FCP/CLS spike. |

`error_spike` and `latency_storm` act the same way whether or not the workload declares causal
resources, but what a failed hop does next differs. In a blueprint with no causal declarations
(`uses`, `call_policies`, `worker` or `traffic.from_resource`) an `error_spike` or declared
`error_rate` hop failure is independent: each hop draws its failure once, nothing resends it, and
the request fails if the entry or any hop failed. In a causal workload the same failure is a request
fact like a resource fault: it reaches the caller as the mapped status (a callee 5xx becomes 502, a
429 becomes 503, or stays 429 with `propagate_throttle`) and a declared `call_policies` retry can
absorb it, each resend drawing its own failure so a retry can recover a spike, including one at the
entry. See [App workload](workloads.md#app) for the declarations and
[`signals/traces.md`](https://github.com/rknightion/synthkit/blob/main/signals/traces.md)
(`traces-app-causal`) for what the spans carry.

### cloud axis

| Mode | Effect |
|---|---|
| `bedrock_throttle` | Bedrock invocation throttling climbs. |
| `agentcore_throttle` | AgentCore request throttles + system_errors spike (region-scoped capacity constraint). |
| `portkey_scrape_degraded` | Portkey analytics scrape degrades — API error_rate and 4xx/5xx share climb, latency rises, poller falls behind. |
| `eval_quality_degraded` | LangSmith eval quality regresses — faithfulness/completeness/relevance and retrieval scores drop while retry/fallback/HITL rates and error/pending run-outcomes climb. |

An AgentCore runtime listed under `cloud.agentcore.runtimes` with a `resource:` (a `concurrency`
resource under `environments[].resources`) emits its own `aws_bedrock_agentcore_*` invocation-class
series, named by the runtime ARN, from the shared resource model, and ignores `agentcore_throttle`:
that mode is scoped to the environment, not to a runtime, so it cannot be a load error for one bound
runtime, and it keeps amplifying the deployment-wide `.../runtime/global` series. The resource-axis
modes reach the bound runtime instead: `concurrency_reduced` on the resource (`throttles` rise,
`invocations` fall), `dependency_slow` on its held-across dependency (`latency` rises, then
`throttles` once the limit is reached) and `dependency_unavailable` or `credential_expired`
(`system_errors` and `total_errors`). Each family drains back to the baseline when the incident ends.

### network axis

| Mode | Effect |
|---|---|
| `nettopo_devices_unreachable` | SNMP polling fails for a fraction of devices (walk errors spike, device discovery drops). |
| `nettopo_discovery_slow` | Discovery cycle duration inflates (cycle_duration_seconds and module walk times rise). |
| `nettopo_walker_degraded` | Walker outcome errors climb; edge count under-reports (partial topology visibility). |
| `nettopo_auth_failures` | SNMP credential trials fail (credential_trials_total error rate rises). |
| `nettopo_spoke_down` | A federation spoke goes offline (`network_topology_federation_spoke_up` drops to 0, hub/spoke session metrics degrade). |

### pipeline_stage axis

Every stage of a pipeline declared under an environment's `cloud.pipelines:` is a target named
`<pipeline>.<stage>`. The modes act on the shared run timeline (`internal/pipelinerun`) that the
`glue` and `mwaa` constructs both read, so one incident shows up consistently in both.
Pipeline names must be unique across **all environments in the blueprint**: repeated names are
rejected at load, even with disjoint stages. Pipeline and stage names match `[a-z0-9_-]+`;
there is no environment-qualified target alias. Use distinct pipeline names (for example,
`orders_prod.transform` and `orders_dev.transform`) for environment-local incidents;
`pipeline_stage:*` deliberately spans every environment's stages.

| Mode | Effect |
|---|---|
| `stage_stall` | The stage starts nothing while active. Work already executing finishes; runs arriving from upstream wait and age from their arrival. Upstream stages keep running, later stages complete nothing. MWAA `aws_mwaa_approximate_age_of_oldest_task` and `queued_tasks` climb; the stage's and later Glue jobs report zero completed tasks. When it clears, the backlog starts at once, oldest first. `intensity` is ignored. |
| `run_failure` | Runs completing the stage fail, with `intensity` as the fraction (default 1.0, every run). A failed run ends with a FAILED terminal outcome and never reaches later stages. A Glue stage reports `aws_glue_driver_aggregate_num_failed_tasks` of 4 per failed run (Spark's default `spark.task.maxFailures`). |

Name a stage or use `pipeline_stage:*`. The empty-target shorthand is rejected for these modes,
because both `glue` and `mwaa` register them.

Incidents are evaluated once per 60 s tick. A stall at the first stage makes each triggered run
wait; a stall at a later stage only builds a backlog once a run reaches it, so on an hourly or daily
pipeline a short stall may never meet a run. MWAA run-outcome metrics (DAG run duration success and
failure, task-instance failures) are not yet sourced, so a failed run is visible through Glue only.

### function axis (Lambda)

Every function declared under an environment's `cloud.lambda.functions` is a target named by
its function name. One function name declared in several environments is one target: an effect
naming it reaches that function in every environment. The modes change only the targeted
function's `aws_lambda_*` CloudWatch
statistics (`signals/cw.md`); account-region concurrency follows from the changed durations.
Magnitudes are synthetic operator assumptions, not AWS behaviour curves.

| Mode | Effect |
|---|---|
| `function_error_spike` | Executions fail: `intensity` is the fraction of otherwise-successful executions that fail, so `aws_lambda_errors` over `aws_lambda_invocations` rises from the declared `error_fraction` to 1 at full intensity. Errors never exceed invocations. |
| `function_throttle_storm` | Demand above concurrency is throttled while admitted executions continue: `aws_lambda_throttles` gains `intensity` × executions per window. Invocations are unchanged. |
| `function_duration_storm` | Every execution's duration (mean and declared bounds) is multiplied by up to 4× at full intensity, raising `aws_lambda_duration` and the modelled concurrency. |

A function with `resource:` (bound to a `concurrency` resource under `environments[].resources`) is not
a function-axis target: an effect naming it with a function-axis mode is an unknown-target load error.
Its `aws_lambda_*` families follow the shared resource model instead, so the resource-axis modes reach
it: `concurrency_reduced` on the resource (throttles, fewer invocations, concurrency capped at the
reduced limit), `dependency_slow` on its held-across dependency (longer `aws_lambda_duration`, more
concurrency, throttles once the limit is reached) and `dependency_unavailable` or `credential_expired`
(`aws_lambda_errors` over `aws_lambda_invocations`). A bound function omits every arrival-rate and
duration field; `reserved_concurrency` and `zero_valued_samples` still apply. Each family drains back
to the baseline once the incident ends.

### queue axis (Solace)

Every queue declared under `integrations.solace` is a target named by its queue name. Queue
names are unique only within a VPN, so a name repeated in several VPNs or brokers addresses every
queue carrying it. The queue's changes roll up into its VPN and broker totals.

| Mode | Effect |
|---|---|
| `queue_publish_drop` | Publishes into the queue stop: the publish rate is scaled by 1 - `intensity`. `solace_queue_msg_spooled` and `solace_queue_byte_spooled` flatten, the backlog drains, and spool usage stops growing. |
| `queue_consumer_stall` | The queue's consumers stop draining: the drain rate is scaled by 1 - `intensity`. `solace_queue_msg_total_deleted` flattens while `solace_queue_spool_usage_msgs` and spool bytes grow; once the queue's spool quota is full, arrivals are discarded into `solace_queue_msg_spool_usage_exceeded`. |

There is no separate discard mode: the sourced discard counter records spool-full discards, which
`queue_consumer_stall` reaches when the quota fills.

A queue that declares a `drain_schedule` (a batch consumer that runs only inside periodic windows,
so its spool is a sawtooth) is stalled the same way: the stall scales the drain rate inside each
window, and the window schedule is unchanged. A queue bound to a causal resource cannot declare
`drain_schedule`; its drain follows the resource's model. More generally a queue with `resource:`
(a `queue` resource under `environments[].resources`) is not a queue-axis target: an effect naming it
with `queue_publish_drop` or `queue_consumer_stall` is an unknown-target load error, and
`publish_msgs_per_sec`, `consume_msgs_per_sec`, `rate_scale`, `shape_profile` and `drain_schedule`
are rejected on it. The resource-axis modes reach it instead (see the next section).

### table axis (DynamoDB)

Every table declared under an environment's `cloud.dynamodb.tables` is a target named by its
`name` (the TableName, not `collector_name`). One table name declared in several environments is
one target.

| Mode | Effect |
|---|---|
| `table_throttle_storm` | GetItem and PutItem requests are rejected for exceeding provisioned throughput: each window gains `intensity` × the operation's admitted requests as throttled requests. `aws_dynamodb_throttled_requests` (TableName+Operation) and `aws_dynamodb_read_throttle_events` (GetItem) or `aws_dynamodb_write_throttle_events` (PutItem) on the table, otherwise absent, become positive and add to any declared `throttled_requests_per_second`. |

Admitted requests, consumed capacity and latency are unchanged, as for `function_throttle_storm`.
Query, Scan and ExecuteStatement were not captured and are unaffected. The magnitude is a synthetic
operator assumption, not an AWS behaviour curve. With no incident active the output is unchanged.

A function or queue name must be usable as a schedule scope: a name containing `@`, `/`, `#` or
whitespace is not addressable, and naming it is an unknown-target error. The loader logs a warning
for every such name, and again for each `function:*` or `queue:*` effect, listing the declared
names that wildcard does not reach.

A function or queue may share its name with a target on another axis, for example an app service
node and the Lambda function that publishes for it. The name is then a target on both axes, and an
effect naming it resolves on the axis that declares the effect's mode: `function_error_spike` on
the function, `latency_storm` on the service node. It is an error when none of the name's axes
declares the mode, or when more than one does. With no incident
active on these axes the output is unchanged.

One scenario can couple both with other axes, for example a publisher failing while its output
queue receives nothing:

```yaml
scenarios:
  - name: responses_unpublished
    title: Responses decided but never published
    effects:
      - { mode: function_error_spike, target: orders-publisher, intensity: 1 }
      - { mode: queue_publish_drop,   target: orders-out,       intensity: 1 }
```

### resource axis (causal resources)

Every resource declared under an environment's `resources:` (a `pool`, `concurrency`, `queue`,
`quota`, `dependency`, `credential` or `rule`) is a target named by its resource name. A resource is
a shared cause: one declared state drives every signal that reads it, so an incident on the resource
moves the app's request spans, logs and span metrics, any custom metric with a `resource:` or
`outcomes:` value, and every bound construct below, in the same window. The authoring guide's
["Declaring causes" section](blueprints.md#declaring-causes) and the [fault catalogue](fault-catalogue.md) give worked
declarations and one story per mode.

| Mode | Resource kind | Effect at intensity `i` |
|---|---|---|
| `pool_exhausted` | `pool` | Effective size shrinks to `size` × (1 − `i`). In-use connections cap there, acquisitions wait up to `acquire_timeout_ms` and then fail with a 503. |
| `concurrency_reduced` | `concurrency` | Effective limit shrinks to `size` × (1 − `i`). Arrivals above it are rejected with a 429. |
| `quota_reduced` | `quota` | Admitted rate shrinks to `limit_rps` × (1 − `i`). Arrivals above it are rejected with a 429. |
| `consumer_slowdown` | `queue` | The consumers' service rate scales by (1 − `i`). Backlog and the oldest message's age grow, and arrivals beyond `max_depth` are rejected with a 503. |
| `dependency_slow` | `dependency` | Latency factor 1 + (`slow_factor` − 1) × `i`. Every pool, concurrency limit and queue held across the dependency holds longer by the extra latency, so it saturates sooner. |
| `dependency_unavailable` | `dependency` | A share `i` of calls fail to connect. The caller sees a transport failure with no HTTP status, which reaches its own caller as a 502. |
| `credential_expired` | `credential` | A share `i` of calls to the credential's dependency fail: the handshake for a certificate, a 401 for a token. The caller that meets it does not resend it. |
| `rule_regression` | `rule` | A share `i` × `reject_share` of arrivals fail validation with a 422. |

Intensity follows the rules above: omitted is 1, an explicit `0` is kept, and every effect is
validated at load. The modes are the vocabulary of `causal.FailureModes` in `internal/causal`, which
every causal consumer registers. None shares a name with a mode on another axis, so an effect with an
omitted target resolves to this axis. A dependency under both `dependency_unavailable` and
`dependency_slow` follows the stronger, a tie going to unavailable.

Targets:

- A resource name is unique across the blueprint. An exact name targets one resource.
- A resource named `<base>-<lower(env)>` is also reached by `<base>`, so one effect can hit that
  resource in every environment that declares one. A base name equal to a declared resource name is a
  load error.
- A scheduled `resource:*` expands to the declared resource names only. A live `resource:*` also
  reaches the base aliases, which is idempotent.
- A resource may share its name with a service node, a Lambda function or a queue. The effect then
  resolves on the axis that declares its mode, as for `function_error_spike` above.

A resource also saturates without an incident. Demand above a pool's, concurrency's or quota's
capacity rejects the excess by itself, and a queue whose service rate falls below its arrivals builds
a backlog that drains afterwards, so an incident often only moves a resource across a threshold it
was already near. Faults act through the shared 5 s model grid, so the same incident gives the same
state to the app and to every bound construct however their ticks are spaced.

#### Bound constructs

A construct that declares a `resource:` binding is driven by the model, and its own rate or duration
fields (and the legacy mode vocabulary built on them) no longer apply. A bound construct either
rejects the legacy mode or ignores it; the resource-axis modes reach it instead. Unbound
declarations are unchanged.

| Construct | Binding | Legacy modes on the bound element | What a resource fault moves |
|---|---|---|---|
| `app` workload | `uses` on service nodes, `call_policies`, `traffic.from_resource` with `worker` | Service-axis modes still apply to the nodes; see the note under the service axis | Request outcome, status, duration, retries and the log, span, span-metric and custom-metric projections of each request |
| `lambda` | `functions[].resource` (`concurrency`) | Not a function-axis target: an effect naming it with a function-axis mode is an unknown-target load error | `aws_lambda_*` (see the function axis above) |
| `sqs` | `queues[].resource` (`queue` without `max_depth`) | None: SQS registers no legacy mode. `arrivals_per_second`, `receives_per_second`, `processing_seconds`, `delay_seconds`, `load` and `burst` must be omitted | `aws_sqs_number_of_messages_sent` (arrivals), `aws_sqs_number_of_messages_received` and `aws_sqs_number_of_messages_deleted` (served), `aws_sqs_approximate_number_of_messages_visible` (backlog) and `aws_sqs_approximate_age_of_oldest_message`. `consumer_slowdown`, or `dependency_slow` on the held-across dependency, grows the backlog and age, which drain after the incident |
| `solace` | `queues[].resource` (`queue`) | Not a queue-axis target (unknown-target load error); `publish_msgs_per_sec`, `consume_msgs_per_sec`, `rate_scale`, `shape_profile`, `drain_schedule` and a `max_depth` on the resource are rejected | Publish and consume rates, queue depth, spool usage and spool-full discards (`solace_queue_msg_spool_usage_exceeded`) |
| `rds` and `docdb` databases | `databases[].resource` (`dependency` or `pool`) | Not a database-axis target (unknown-target load error); cannot combine with `observability.dbo11y` | Read and write latency, and a pool's connections (see the database axis above) |
| `agentcore` | `runtimes[].resource` (`concurrency`) | `agentcore_throttle` is scoped to the environment, so a bound runtime ignores it rather than rejecting it, and it keeps driving the deployment-wide `runtime/global` series | The runtime's own `invocations`, `throttles`, `system_errors`, `total_errors` and `latency` series, named by the runtime ARN |

Counts agree across consumers, not only shares: a bound Lambda function's invocations plus throttles
equal the attempts the app makes through it in expectation.

#### Mode and resource kind validation

Targets are the names under `environments[].resources`, plus the base name of a resource called
`<base>-<lower(env)>`, which reaches that resource in every environment that declares one. Each mode
acts on exactly one resource kind, and the loader rejects a mode aimed at any other kind, for a
scenario effect and an incident row alike, because the fault would silently move nothing.

| Mode | Acts on kind |
|---|---|
| `pool_exhausted` | `pool` |
| `concurrency_reduced` | `concurrency` |
| `quota_reduced` | `quota` |
| `consumer_slowdown` | `queue` |
| `dependency_slow`, `dependency_unavailable` | `dependency` |
| `credential_expired` | `credential` |
| `rule_regression` | `rule` |

The error names the mode, the target, the resource of the wrong kind and the accepted kind. A base
name is checked against every resource it reaches, so one resource of another kind in any environment
fails the load. `resource:*` selects only the declared resources of the mode's kind (so
`dependency_slow` on `resource:*` hits the dependencies and skips the pools), and it is a load error
when the blueprint declares none of that kind. Environment base aliases are not repeated in the expansion.

## Optional gateway pool failures

The `alloy_health` addon registers six cluster-scoped modes:
`gateway_instance_loss`, `gateway_wan_outage`, `gateway_queue_overflow`,
`gateway_config_reload_failure`, `gateway_cloud_credential_expired` and
`gateway_source_gap`. Activate them against the declared cluster name. Member-specific
loss, reload and source-gap modes use `gateway.source_victim`; send faults affect
pool egress.

Queues retain modeled requests for the emitter lifetime, not an actual disk WAL.
Within capacity, outage input drains after recovery without loss; overflow increments
enqueue-rejected item counters, not just failed attempts. Generic failed sends do not
uniquely diagnose credential expiry. Source gap removes the selected member's three
same-process `remotecfg` data families before the public writer, while target `up`,
independent healthy controller observations and the other member remain present.

See [gateway configuration and limits](ai-factory/alloyhealth.md) and
[`signals/fm.md`, `fm-gateway`](https://github.com/rknightion/synthkit/blob/main/signals/fm.md) for cadence, scope, absence queries
and the source catalogue. Local absence proof does not establish backend alert timing.

## Definition vs activation

The `scenarios:` and `incidents:` blocks are the **definition** layer: they describe what modes exist in this blueprint and when they are scheduled to fire. The runner validates these at load time against the actual construct vocabulary and target inventory.

**Activation** is separate:

- The scheduled `incidents:` entries fire automatically according to their `at:` or `every:` schedule while the process runs.
- **Live activation** via the control plane is additive: it unions on top of any currently scheduled windows. A scenario activated live runs until explicitly deactivated, regardless of the `incidents:` schedule.

For live activation, see [Control Plane](control-plane.md). The control plane also exposes `GET /control/schema`, which returns the complete derived vocabulary — modes, addressable targets with current scaling state, and all named scenarios — for the loaded blueprints.

## Emitted-data acceptance evidence

An HTTP success or the `active_scenarios` control field is not scenario evidence. The
schema-derived acceptance harness selects a sourced observation for every declared scenario,
records a baseline, activates the scenario, and verifies the observation moves in the documented
direction. It then deactivates the scenario and verifies that the observation returns near its
baseline. Counter and histogram totals are never expected to decrease: their assertions use a
windowed rate or a reversible gauge instead.

Run the root-owned `just e2e-scenarios` recipe against a deployment with both query endpoints
configured. The harness discovers IDs from `GET /control/schema`, continues after failures, and
prints one disposition per ID. For `eval_quality_degraded` and `portkey_scrape_degraded`, pass a
sibling environment: their observations prove the target moves while that sibling remains at its
baseline. The current query catalogue is sourced from `signals/agentcore.md`, `signals/cw.md`,
`signals/apm.md`, `signals/k8s.md`, `signals/langsmith.md`, `signals/portkey.md`, and
`signals/logs.md`; adding a scenario whose effects have no catalogue entry is a failed disposition,
not a control-plane pass.

## Complete example

```yaml
name: mine
scenarios:
  - name: db_storm
    title: Database connection storm
    effects:
      - { mode: connection_saturation, target: mine-app-db, intensity: 0.8 }
      - { mode: slow_query_storm,      target: mine-app-db, intensity: 0.6 }
      - { mode: latency_spike,         target: mine-api,    intensity: 0.4 }

  - name: ai_brownout
    title: AI gateway brownout
    effects:
      - { mode: agentcore_throttle,                    intensity: 0.7 }
      - { mode: retry_storm, target: mine-api-backend, intensity: 0.5 }

incidents:
  # One-shot scheduled event
  - scenario: db_storm
    at: "2026-07-15T14:00"
    for: 25m

  # Ambient non-prod churn
  - kind: oom_kill
    target: mine-dev-eks
    every: 20m
    for: 5m
    intensity: 0.2

  # Single-mode with explicit target
  - kind: pod_crashloop
    target: mine-dev-eks
    at: "2026-07-15T10:05"
    for: 12m
    intensity: 0.5
```

## H100 HGX DCGM failures

The [DCGM integration](ai-factory/dcgm.md) uses the shared GPU topology and constraint
inputs. These are synthetic scenario controls, not claims of live fault induction.

| Mode | Target | Observable |
|---|---|---|
| `gpu_cooling_fault` | GPU rack / supported cooling device | Shared temperature; declared software thermal cap activates only above 89 C, with elapsed ns and separate clock window/edge counters. |
| `gpu_power_constraint` | GPU or GPU node | Shared uncapped demand increases; a reducing power cap activates only above configured budget. |
| `gpu_xid_burst` | Selected shared GPU targets | Sourced configured last-Xid gauge, window count and cumulative total; no generic thermal/power Xid is invented. |
| `dcgm_ecc_error_growth` | GPU | Declared DBE and uncorrectable-row-remap events accumulate; their ratio is a scenario input. |
| `dcgm_health_failure` | GPU | Source-supported memory health failure with code/severity/category metadata. |
| `gpu_nvlink_error_burst` | Selected GPU or sourced NVLink target | Shared physical edge-error rates accumulate as the declared CRC scenario, with NVLINK health metadata. |
| `gpu_fallen_off_bus` | GPU, GPU node or supported tray | All affected GPU series disappear; no zero-valued unavailable clocks. |

Window expiration does not reset totals. First observations prime event edges and elapsed
durations. The model holds the previously observed state between observations; it does not
claim to reconstruct unobserved physical transitions. Default healthy PCIe, retired-page,
and unsupported platform behavior must not be fabricated into additional incidents.
