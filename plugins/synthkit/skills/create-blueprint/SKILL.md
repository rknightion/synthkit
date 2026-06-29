---
name: create-blueprint
description: Use when authoring or editing a synthkit blueprint (blueprints/*.yaml) — declaring infrastructure or applications, wiring workloads to clusters, choosing emission switches, or modelling a new scenario.
---

# Author a synthkit blueprint

Blueprints are the only place blueprint-specific config and wiring live. This skill orients you; the
authoritative contracts live in the repo.

## Before editing
- Read `ARCHITECTURE.md` (frozen seams + invariants) and `SIGNALS.md` → `signals/` (the per-construct
  data contract). NEVER invent a metric/label/field name — source it from `signals/<area>.md`.
- Read `BLUEPRINT-SCHEMA.md` (generated from the Go types) for valid fields per construct/workload.
- Copy an existing blueprint as a starting point: `blueprints/acme-ai-platform.yaml` (multi-service
  golden thread) or `blueprints/k8s-minimal.yaml` (minimal).

## Authoring loop
1. Declare resources; gate which constructs each builds via its emission switch
   (e.g. a `databases:` entry's `observability: { cloudwatch:…, dbo11y:… }`).
2. Wire workloads → clusters and shared identity in the blueprint (the explicit wiring layer).
3. Validate offline: `make dump` (= `DRY_RUN=true go run ./cmd/synthkit -once -dump`; needs a local
   Go toolchain) or the dockerized form
   `docker compose build synthkit && DRY_RUN=true docker compose run --rm synthkit -once -dump`;
   diff the series inventory against `signals/`.
4. Keep the gate green: `make gate` (build + vet + test + race; includes schema + env drift guards).

> TODO (deep procedure): per-construct field walkthroughs, identity/collision rules, and worked
> multi-construct examples. Until then, mirror `blueprints/acme-ai-platform.yaml` and lean on `-dump` + `signals/`.
