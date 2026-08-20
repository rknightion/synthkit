---
name: create-blueprint
description: Use when authoring or editing a synthkit blueprint in a verified checkout — declaring infrastructure or applications, wiring workloads to clusters, choosing emission switches, or modelling a new scenario.
---

# Author a synthkit blueprint

Blueprints are the only place blueprint-specific config and wiring live. This skill orients you; the
authoritative contracts live in the repo.

## Locate the synthkit checkout

Plugin installation provides guidance only; it does not install synthkit or create a checkout.
Before any repository command, establish and verify the checkout root:

```bash
SYNTHKIT_CHECKOUT="/absolute/path/to/synthkit"
SYNTHKIT_CHECKOUT="$(git -C "$SYNTHKIT_CHECKOUT" rev-parse --show-toplevel)" || exit 1
test -f "$SYNTHKIT_CHECKOUT/AGENTS.md" && \
  test -f "$SYNTHKIT_CHECKOUT/BLUEPRINT-SCHEMA.md" && \
  test -d "$SYNTHKIT_CHECKOUT/blueprints" || exit 1
cd "$SYNTHKIT_CHECKOUT"
```

All repository paths below are rooted at `$SYNTHKIT_CHECKOUT`. This skill has no plugin-owned
helper to invoke.

## Before editing
- Read `$SYNTHKIT_CHECKOUT/ARCHITECTURE.md` (frozen seams + invariants) and
  `$SYNTHKIT_CHECKOUT/SIGNALS.md` → `$SYNTHKIT_CHECKOUT/signals/` (the per-construct
  data contract). NEVER invent a metric/label/field name — source it from `signals/<area>.md`.
- Read `$SYNTHKIT_CHECKOUT/BLUEPRINT-SCHEMA.md` (generated from the Go types) for valid fields per
  construct/workload.
- Copy an existing blueprint as a starting point:
  `$SYNTHKIT_CHECKOUT/blueprints/acme-ai-platform.yaml` (multi-service request correlation) or
  `$SYNTHKIT_CHECKOUT/blueprints/k8s-minimal.yaml` (minimal).

## Authoring loop
1. Declare resources; gate which constructs each builds via its emission switch
   (e.g. a `databases:` entry's `observability: { cloudwatch:…, dbo11y:… }`).
2. Wire workloads → clusters and shared identity in the blueprint (the explicit wiring layer).
3. Validate offline: `make dump` (= `DRY_RUN=true go run ./cmd/synthkit -once -dump`; needs a local
   Go toolchain) or the dockerized form
   `DRY_RUN=true docker compose run --rm synthkit -once -dump`;
   diff the series inventory against `signals/`.
4. Keep the gate green: `make gate` (build + vet + test + race; includes schema + env drift guards).

> TODO (deep procedure): per-construct field walkthroughs, identity/collision rules, and worked
> multi-construct examples. Until then, mirror
> `$SYNTHKIT_CHECKOUT/blueprints/acme-ai-platform.yaml` and lean on `-dump` + `signals/`.
