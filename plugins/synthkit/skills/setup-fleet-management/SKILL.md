---
name: setup-fleet-management
description: "Configure synthkit's Fleet Management fake collectors and verify registration; not arbitrary collectors."
---

# Set up Fleet Management for synthkit

synthkit can register its deterministic fake Alloy collector roster with Grafana Cloud Fleet
Management (FM). Registration is optional: the collector metrics lane remains available without it.

## Locate the synthkit checkout

```bash
SYNTHKIT_CHECKOUT="/absolute/path/to/synthkit"
SYNTHKIT_CHECKOUT="$(git -C "$SYNTHKIT_CHECKOUT" rev-parse --show-toplevel)" || exit 1
test -f "$SYNTHKIT_CHECKOUT/AGENTS.md" && \
  test -f "$SYNTHKIT_CHECKOUT/.env.example" && \
  test -f "$SYNTHKIT_CHECKOUT/BLUEPRINT-SCHEMA.md" || exit 1
cd "$SYNTHKIT_CHECKOUT"
```

## Declare the synthetic roster

In an enabled blueprint, add the public schema shape below. `collectors_per_os` accepts only
`linux`, `windows`, and `darwin`; omitted OSes emit no collectors. Zero is an explicit zero, not a
request for one collector.

```yaml
features:
  fleet_management:
    enabled: true
    collectors_per_os: {linux: 3, windows: 1}
```

Use `blueprints/k8s-full-stack.yaml` as a complete repository example. A cluster's
`k8s_monitoring.fleet_management: true` models its Alloy context, while the top-level feature
declares the synthetic FM roster. Validate the copied declaration offline with:

```bash
DRY_RUN=true BLUEPRINT_NAMES=k8s-full-stack go run ./cmd/synthkit -once -dump
```

## Configure credentials safely

Set the non-secret `GC_FM_URL` and `GC_FM_STACK_ID` (the Grafana Cloud stack ID, not
`GC_PROM_USER`) with the initial-setup helper. Add `GC_FM_TOKEN` only through the secure prompt
path in `initial-setup`; it needs the `fleet-management:write` scope and must not reuse `GC_TOKEN`.
All three values are required for registration. An empty `GC_FM_URL` deliberately produces metrics
without an FM API write.

## Public API model and read-only verification

Grafana Fleet Management's public Connect API exposes collector registration and listing. The
relevant public shapes are:

```json
{"collector":{"id":"stable-id","name":"display name","collectorType":1,"enabled":true,
              "localAttributes":{"collector.os":"linux","collector.version":"v1"}}}
```

and a read-only list request:

```json
{"matchers":[]}
```

synthkit itself uses its supported registration endpoint and stable generated IDs. Do not manually
POST the create/register shape: it would be a live write and can duplicate or alter the synthetic
roster. After a live deployment, list only, using credentials from the operator's terminal without
printing them:

```bash
curl --fail-with-body --silent --show-error \
  --user "$GC_FM_STACK_ID:$GC_FM_TOKEN" \
  -H 'Content-Type: application/json' \
  --data '{"matchers":[]}' \
  "$GC_FM_URL/collector.v1.CollectorService/ListCollectors"
```

Count only the collectors attributable to this blueprint's stable IDs/attributes and compare with
the sum of its positive `collectors_per_os` values. Check `collector.os`, a non-empty
`collector.version`, and the declared cluster attribute where applicable. A 401/403 means the
stack ID, token, or `fleet-management:write` scope is wrong; do not retry with `GC_TOKEN`.

Public schema reference: Grafana Fleet Management API, `CollectorService/CreateCollector` and
`CollectorService/ListCollectors` (queried 2026-08-20). The list operation above is read-only.
