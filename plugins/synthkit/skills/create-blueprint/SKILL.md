---
name: create-blueprint
description: "Create or change a synthkit blueprint in a verified checkout; not generic YAML or catalog code."
---

# Author a synthkit blueprint

Blueprints own scenario-specific configuration and explicit wiring. They do not add constructs,
workloads or invent catalog signal names: those contracts already live in the catalog. The narrow
exception is sourced, blueprint-owned `app` service-node `custom_metrics`, governed by the
[canonical workload guide](https://github.com/rknightion/synthkit/blob/main/docs/workloads.md#blueprint-owned-custom-metrics).

## Locate the synthkit checkout

Plugin installation provides guidance only; it does not install synthkit or create a checkout.

```bash
SYNTHKIT_CHECKOUT="/absolute/path/to/synthkit"
SYNTHKIT_CHECKOUT="$(git -C "$SYNTHKIT_CHECKOUT" rev-parse --show-toplevel)" || exit 1
test -f "$SYNTHKIT_CHECKOUT/AGENTS.md" && \
  test -f "$SYNTHKIT_CHECKOUT/BLUEPRINT-SCHEMA.md" && \
  test -d "$SYNTHKIT_CHECKOUT/blueprints" || exit 1
cd "$SYNTHKIT_CHECKOUT"
```

## Read the contracts, then choose a starting point

Read `ARCHITECTURE.md`, `SIGNALS.md` and the relevant `signals/<area>.md`, then
`BLUEPRINT-SCHEMA.md`. Start from `blueprints/k8s-minimal.yaml` for a small Kubernetes estate or
`blueprints/acme-ai-platform.yaml` for a correlated multi-service example. Keep the copied file's
comments only when they still describe the new scenario.

Use fields only where the schema lists them. For a catalog field's signal effect, use its matching
signal catalogue; a plausible-looking name is not evidence. If the desired catalog signal is absent,
stop and return the missing contract rather than adding a guessed field or name.

For `app` service-node `custom_metrics`, cite the instrumentation contract for every name, unit and
label dimension in the required nonblank `source`. This is configuration-only provenance, never
wire data; the loader checks presence, not authenticity. Keep labels bounded and low-cardinality;
high-cardinality keys, high-cardinality refs and node-identity overrides are forbidden. Each custom
declaration must own its instrument name and complete Prometheus family per node, including a
histogram's `_bucket`, `_sum` and `_count`: collisions with profiles, legacy `metrics` or other
custom declarations reject regardless of labels, units or bounds. Follow the workload guide above
for cumulative value semantics, transport behavior and realistic volume assumptions; do not copy
its illustrative fixture values as production defaults.

For a sourced custom gauge that is truly empty in ordinary operation, keep `base: 0` and use
`value: {shape: {base: 0, mode: error_spike, incident_value: 12}}` only with a deployment-justified
endpoint (12 here is a fixture illustration). The metric-only opt-in interpolates from `base` to
`incident_value` with scoped intensity, then scales by business load and master volume; it does
not apply the legacy ×3 amplification. At unit load it returns to zero as soon as the matching
incident ends; volume 0 pauses the shape gauge at zero. It models no backlog persistence or drain.
For an outage level that must not shrink at night or at weekends (a duration such as a publish
lag), add `incident_absolute: true`: the incident share then reads `incident_value` at any load.
Both endpoints must be finite and `mode` nonblank; use an existing registered service failure mode
and an exact service-node target to constrain scope. Do not add incident-dependent label values.
Omit the option to preserve legacy multiplicative shape output. Follow the canonical workload
DSL section for metric value semantics and the
[canonical incident guide](https://github.com/rknightion/synthkit/blob/main/docs/incidents.md#incidents-scheduled-activations)
for scheduled intensity: explicit `intensity: 0` is preserved as zero, omission defaults to 1,
and fractional values are preserved. This applies to inline incidents and each effect of a
scheduled scenario; put scenario intensity on the effect, not the referencing incident. Zero
does not bypass target validation. It leaves intensity-scaled values at baseline, but consumers
that check only window activity ignore intensity; end or omit their window to disable the effect.
Live resolved-zero semantics are unchanged. Verify baseline → scoped onset → recovery through
a loaded fixture before using the value in a composition.

Declare batch pipelines once in an environment's `cloud.pipelines`; follow
[the canonical pipeline guide](https://github.com/rknightion/synthkit/blob/main/docs/constructs.md#declared-pipelines)
and [stage incident contract](https://github.com/rknightion/synthkit/blob/main/docs/incidents.md#pipeline_stage-axis).
Pipeline names must be unique blueprint-wide, including disjoint stages in different environments.
Use canonical `<pipeline>.<stage>` targets for local scope; `pipeline_stage:*` intentionally spans
all environments. Glue and MWAA are the emitting consumers of the shared timeline; accepted
Lambda/SQS/Redshift stages do not install emitting adapters or a log contract. For MWAA terminal
outcomes, follow the [canonical CloudWatch outcome contract](https://github.com/rknightion/synthkit/blob/main/signals/cw.md):
observations are sparse, so an empty completion window is not a zero-duration sample. Do not claim
full pipeline/demo coverage or raise shipped rates to force a run; use a selected test fixture.

## Declare causes, not rates

When the blueprint models a failure, declare the cause and let the symptoms follow. A rate-only
scenario (`error_spike`, `latency_storm` and similar) scales a number and leaves every other
signal as it was; avoid it as the default way to model an incident. Use it only for an effect with
no resource to name, and say so in a comment.

1. Name what a real incident would break: a connection pool too small for its hold time, a
   function's reserved concurrency, consumers that cannot keep up with a queue, a quota, a slow
   dependency, an expired certificate or token, a validation rule or a bad release.
2. Declare it under the environment's `resources:` and attach it where requests meet it: an `app`
   service node's `uses`, or a `resource:` field on a Lambda function, SQS queue, Solace queue or
   database. Read the `environments[].resources[]` rows of `BLUEPRINT-SCHEMA.md` for the exact
   fields per kind.
3. Write the scenario against the resource that is at fault, using one of the eight `resource`-axis
   modes (`pool_exhausted`, `concurrency_reduced`, `quota_reduced`, `consumer_slowdown`,
   `dependency_slow`, `dependency_unavailable`, `credential_expired`, `rule_regression`). Aim a
   mode at the kind it acts on. Aim `dependency_slow` at the dependency, not at the pool that
   holds connections across it: the exhaustion is emergent.
4. Size the resource with arithmetic and put the sum in a comment: offered load is
   `rps x hold seconds`, a pool is comfortable below about 80% of its size and exhausts above
   100%, and a slow dependency stretches the hold. Every size, hold time, latency and rate carries
   its real-world basis. Never raise a rate in `blueprints/` to make a signal appear; one-tick
   forcing belongs in `e2e/fixtures/`.
5. Verify through the loader and the dump (Procedure step 7), then confirm the incident moves the
   state you expect: healthy before, the cause during, recovered after.

For AgentCore runtimes bound to a concurrency resource, use resource-axis faults instead of legacy
`agentcore_throttle`: loading rejects that legacy effect if its cloud scope reaches any bound
runtime, including mixed deployments, `cloud:*` and omitted targets. Expanded cloud names must also
survive scheduler parsing unchanged; malformed or whitespace-altered names are rejected even on
unbound deployments. See the [cloud-axis contract](https://github.com/rknightion/synthkit/blob/main/docs/incidents.md#cloud-axis)
for scope rules and the raw runtime escape-hatch boundary.

Follow the [Declaring causes guide](https://github.com/rknightion/synthkit/blob/main/docs/blueprints.md#declaring-causes)
for the worked example (a connection pool held across a slow database, loaded from
`e2e/fixtures/causal-example.yaml`) and the
[fault catalogue](https://github.com/rknightion/synthkit/blob/main/docs/fault-catalogue.md) for a
loadable blueprint per mode. Copy their structure, not their rates. An error class and message
are values you choose in a resource's `error` block; the catalogue ships none. Do not name an
attribute or log field for the cause: the signal contracts fix those names once sourced.

## Procedure

1. Give the blueprint a unique, stable `name` and `label`. `name` is the deterministic seed;
   `label` is the sink selector. Do not derive either from a position in a file.
2. Declare one or more `environments`. Each environment owns its cloud account/region/VPC and,
   when applications run there, its cluster. Use the schema's required topology fields rather than
   copying an unrelated provider or engine variant.
3. Select emission switches only for lanes the scenario needs. For example,
   `databases[].observability.cloudwatch` and `.dbo11y` independently fan the same declared
   database into its supported construct lanes. A switch gates emission; it does not change the
   declaration's identity. PostgreSQL/MySQL may combine dbo11y with a database `resource:` binding
   to a dependency/pool, but the CloudWatch lane remains required. Follow
   [database bindings](https://github.com/rknightion/synthkit/blob/main/docs/blueprints.md#the-emission-switch):
   compare same-minute pool connections and duration/counter increments across both lanes, including
   recovery and an unbound sibling. Do not infer server Lock/IO causes or SQL errors from acquisition
   pressure, or schedule database-axis modes on a bound DB. DocumentDB/Neptune dbo11y is unsupported.
4. Add workloads under `workloads:`. Set `type`, unique workload `name`, and `runs_on` to the
   exact declared cluster name. Add `calls[].db` / `calls[].cache` only with exact names from the
   same resolved environment. The loader rejects unknown references; do not work around that by
   duplicating an identity.
5. Add `features:` for Grafana Cloud products and `integrations:` for externally observed
   systems. These are distinct schema sections; a kind in the wrong section is a load error.
   Use `for_each_env: true` only when the schema supports it and every selected environment has
   the required resolved resource.
6. For a failure scenario, declare the cause under `resources:` and aim the scenario at it, as
   [Declare causes, not rates](#declare-causes-not-rates) describes. Do not model an incident with
   a rate-only mode when a resource can be named.
7. For multi-construct output, validate both the wiring and the intended inventory before
   declaring it done:

   ```bash
   BLUEPRINT_RUNTIME_NAME="<exact runtime identity>"
   SELFOBS_ENABLED=false DRY_RUN=true BLUEPRINT_NAMES="$BLUEPRINT_RUNTIME_NAME" go run ./cmd/synthkit -env /dev/null -once -dump
   # Or, without a local Go toolchain:
   docker compose run --rm --no-deps -e SELFOBS_ENABLED=false -e DRY_RUN=true \
     -e BLUEPRINT_NAMES="$BLUEPRINT_RUNTIME_NAME" synthkit -env /dev/null -once -dump
   ```

   Compare the dumped series inventory with the selected `signals/` contracts; do not make the
   catalogue match a new dump.

For captured Lambda/SQS/DynamoDB/Redshift/Logs CloudWatch output, follow
[the public consumer guide](https://github.com/rknightion/synthkit/blob/main/docs/blueprints.md#captured-cloudwatch-consumers)
and the explicit emitted sections in `signals/cw.md`, not the larger positive-source tables.
Use optional `cloud.lambda.functions`, `cloud.sqs.queues`, `cloud.dynamodb.tables`,
`cloud.redshift.clusters` and `cloud.cloudwatch_logs.log_groups`; at most one
block per blueprint/account/region/kind, including across environments with disjoint resources.
These consumers reject native OTLP export and have no pipeline adapters. Preserve explicit
non-global SQS/DynamoDB collector names, exact alternative dimensions and once-only account
snapshots. Redshift additionally requires non-global collector names and all snapshot assumptions,
including explicit zero/false. Optional read/write I/O uses seconds and independent completed-I/O
rates; named queue stocks use the exact separate QueueName projection. Logs requires explicit
rate and whole uncompressed bytes per event; its event unit remains AWS None, not Count.
Optional group-owned `subscription_filters` require an unpadded unique name within the group,
`destination_type: Lambda` and all four whole finite nonnegative `period_observation` fields:
`delivery_errors`, `delivery_throttling`, `forwarded_log_events`, `forwarded_bytes`. Bytes are
already compressed. These independent authored values observe once per active minute with
four value statistics=x, sample_count=1, including explicit zero. Progress is independent of
intake, even zero intake; this is not a delivery simulator, compression/retry/loss or intake model.
Follow the guide's required-presence/bounds and exact carry/pause/first-resume rules. No captured
enrichment, NodeID/user-query/managed-storage exception, other Logs selectors/profiles/by-data-source,
compression calculation, info, pipeline or lifecycle attribution is admitted. ESM/fair/info remain withheld. Fixed rates/value assumptions need a real-world
basis beside them; do not copy deliberate all-family populations from `e2e/fixtures/` into a
shipped blueprint. Ordinary sparse rates may not emit every family in one CLI tick.

For native OTLP pod logs, select `k8s_monitoring.pod_logs_collector: otel_collector`
when modelling the captured OTel Collector shape. Inspect `collector_profile` in
`-inventory-json` and follow the collector comparison procedure in
`docs/reality-corpus.md#comparing-pod-log-collectors`. Attribution is inventory-only
and does not change wire keys.

For DCGM, use only the documented H100 SXM HGX profile and declare shared pool
`operating.constraints` plus sourced usable framebuffer. Follow `docs/ai-factory/dcgm.md`
for supported profiles, explicit workload assumptions and conditional Kubernetes labels.
The stress fixture is under `e2e/fixtures/`; do not copy its fault inputs into a shipped
blueprint to force emission. Other compute collection adapters remain separately gated.

For per-agent Bedrock application inference profiles, follow the profile subsection in
`docs/blueprints.md` and the capture boundary in `signals/bedrock.md`. Use fictional opaque IDs,
explicit traffic/value assumptions and a separate foundation model identity for generation pricing.
Keep (account_id, region, profile ID) unique across environments within a blueprint; environment,
agent and team names do not distinguish profile series. Duplicate declarations are rejected.
Select `sub_signals: [application_profiles]` for profile-only emission. Profile OTLP declarations
still fail closed pending complete resource/enrichment verification. Native Summary source
payloads from a failed-delivery backup are confirmed, not successful collector/backend parity,
invocation-log evidence or tag persistence.

For `ai_agent`, follow the current Agent Observability producer and tag/token
contracts in `signals/sigil.md`. Real backend rules and legacy synthetic scoring
must not score the same agents; backend-derived families are never blueprint
metric declarations. Use the generated schema for `execution_mode` and
`filterable_tag_keys`, not an inferred evaluator-role label.

For the bounded `app.agent_pipeline` component, read the
[workload guide](https://github.com/rknightion/synthkit/blob/main/docs/workloads.md#bound-agent-observability-executor-component-boundary)
and [app-specific signal subset](https://github.com/rknightion/synthkit/blob/main/signals/sigil.md#bound-app-executor-subset-slug-sigil-app-pipeline).
This is not yet an ordinary CLI-authorable runtime feature: boot/live/persisted restore
cannot inject the unconditional `GenerationStages` construction dependency and fail closed,
even with hooks disabled/ratings zero. A no-op object is not admission proof. Do not install
one to force a dump. Actual selected-set/destination/current/historical custody and runtime
adapters remain separate requirements. Use generated fields only for controlled component
fixtures until those requirements are met; forced rates belong only in `e2e/fixtures/`.

Model `content_facets` are an exact unordered set, omitted means facetless, and both textual
user/assistant candidates must exist without fallback. Retrieval requires explicit tool name,
`kind: tool_result` and exact facets shared by caller/source; planner owns preflight policy,
not retrieval-only hooks or a fabricated backend tool-name predicate. Preserve the documented
16-stage/3-revision/32-attempt/1-MiB limits and native duration/token-only metric subset.
Every pipeline graph service must be bound and used in its stage plan. Do not declare
inline logs, log-producing catalog profiles, pages or rum_faro: Build rejects these
unsupported outputs rather than silently dropping them. Ordinary app graphs are unchanged.
`metadata_only` capture keeps a thinking part as an empty thinking payload through the
explicit part kind, with no placeholder content. The corpus path produces no media and
reduced Python media is refused. Default/no_tool_content/full_with_metadata_spans keep native
generation content but omit shared tool-span content; only full includes accepted tool content
in spans. Authored metadata/tags are not scrubbed. Follow the canonical SDK-specific exceptions,
not a blanket privacy claim. Do not generalize this subset to the separate `ai_agent` workload.

## Identity and collision rules

- A cluster name, cloud account ID, database name, and cache name must not collide across enabled
  blueprints where the architecture defines that identity as substrate-scoped. Rename the declared
  resource, not a sink label.
- Blueprint-scoped output receives the blueprint selector from the scoped writer. Never add that
  selector manually, and never add it to substrate-scoped constructs.
- A missing dimension is omitted, not set to `""` or `"NA"`. Request IDs and other high-cardinality
  facts belong only in the ledger-derived trace/log positions allowed by the contract, never in
  metric or Loki stream labels.
- Workload names are unique in their blueprint. `app` service-node names are unique in that app;
  node identity is automatically stamped. Share a declared database/cache identity through the
  resolver instead of creating parallel resources with similar names.

## Worked composition: API, database, cache, and Fleet Management

This is a field-shaped skeleton, not a signal contract. Fill versions, routes, and switches from
the schema and relevant signal pages.

```yaml
name: shop-demo
label: shop-demo
shape: business_hours_plateau
environments:
  - name: prod
    weight: 1
    cloud:
      provider: aws
      account_id: "111122223333"
      region: us-east-1
      vpc_id: vpc-shop-prod
    cluster:
      type: eks
      name: shop-prod-use1
      node_groups: [{name: general, instance_type: m6i.xlarge, desired: 3}]
      k8s_monitoring: {enabled: true, alloy: true}
    databases:
      - {engine: postgres, version: "16.2", name: shop-orders,
         observability: {cloudwatch: true, dbo11y: true, digests: 20}}
    caches:
      - {engine: redis, version: "7.1", name: shop-sessions}
workloads:
  - type: web_service
    name: shop-api
    runs_on: shop-prod-use1
    calls: [{db: shop-orders}, {cache: shop-sessions}]
    endpoints: [{route: "GET /v1/orders", error_rate: 0.01, p95_ms: 180}]
features:
  fleet_management:
    enabled: true
    collectors_per_os: {linux: 3}
```

The `shop-orders` declaration fans into CloudWatch and database-observability lanes while sharing
one database fixture; `shop-api` resolves its cluster/database/cache names against that same
environment. Fleet collector count is the sum of positive `collectors_per_os` entries in enabled
blueprints, not a workload count. Before live use, run the dump command above and inspect the
loader error rather than editing generated schema or catalog code to silence it.
