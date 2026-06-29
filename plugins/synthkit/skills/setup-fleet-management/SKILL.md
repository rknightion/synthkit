---
name: setup-fleet-management
description: Use when enabling synthkit's Fleet Management (FM) lane — registering synthetic collectors with the Grafana Cloud Fleet Management API, or configuring the GC_FM_* credentials.
---

# Set up Fleet Management for synthkit

synthkit can register synthetic Alloy-style collectors with the Grafana Cloud Fleet Management API.

## Credentials (secret via the secure path — see initial-setup)
- `GC_FM_URL` — FM API base URL (e.g. `https://fleet-management-prod-0NN.grafana.net`).
- `GC_FM_STACK_ID` — Grafana Cloud **stack ID**, used as the Basic-auth user (NOT `GC_PROM_USER`).
- `GC_FM_TOKEN` — token scoped `fleet-management:write` (NOT `GC_TOKEN`).

## Blueprint requirement
FM registration only happens when the active blueprint declares a `fleet_management` construct
(see `setup-fleet-management` ⇄ `create-blueprint`). If `GC_FM_URL` is empty, the collectors still
emit metrics but skip FM API registration (the runner logs this).

## Verify
After deploy, confirm registration via the FM API for the expected collector count
(`collectors_per_os` × blueprints). Use `verify-deployment` for the data-landing check.

> TODO (deep procedure): collector pipeline/config payload shape and per-OS counts. Until then mirror
> the `fleet_management` block in an existing blueprint and check `internal/fleet`.
