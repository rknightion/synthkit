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

## Procedure

1. Give the blueprint a unique, stable `name` and `label`. `name` is the deterministic seed;
   `label` is the sink selector. Do not derive either from a position in a file.
2. Declare one or more `environments`. Each environment owns its cloud account/region/VPC and,
   when applications run there, its cluster. Use the schema's required topology fields rather than
   copying an unrelated provider or engine variant.
3. Select emission switches only for lanes the scenario needs. For example,
   `databases[].observability.cloudwatch` and `.dbo11y` independently fan the same declared
   database into its supported construct lanes. A switch gates emission; it does not change the
   declaration's identity.
4. Add workloads under `workloads:`. Set `type`, unique workload `name`, and `runs_on` to the
   exact declared cluster name. Add `calls[].db` / `calls[].cache` only with exact names from the
   same resolved environment. The loader rejects unknown references; do not work around that by
   duplicating an identity.
5. Add `features:` for Grafana Cloud products and `integrations:` for externally observed
   systems. These are distinct schema sections; a kind in the wrong section is a load error.
   Use `for_each_env: true` only when the schema supports it and every selected environment has
   the required resolved resource.
6. For multi-construct output, validate both the wiring and the intended inventory before
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
fail closed pending raw metric-stream/enrichment evidence; API statistics do not prove that lane.

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
