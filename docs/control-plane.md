---
title: Control Plane
description: Reference for the synthkit operator UI and HTTP control plane — endpoints, auth, and live runtime operations.
---

# Control Plane

synthkit embeds an operator control plane served on port **8088** (configured via `JSON_HTTP_ADDR`). It has two surfaces:

- **Operator UI** at `/control/ui` — a browser dashboard for live runtime management.
- **HTTP API** — a JSON API used by the UI, the Grafana Infinity datasource, and curl/automation.

All GET endpoints are open (no auth). POST mutation endpoints require HTTP Basic auth when `CONTROL_TOKEN` is set.

---

## Operator UI

Open `http://127.0.0.1:8088/control/ui` (or the host's address if exposed).

The UI provides:

- **Overview** — per-blueprint emission status, sink readiness strip, dry-run indicator.
- **Scenarios** — activate and deactivate named incident scenarios defined in blueprints.
- **Scaling** — set live workload pod counts within blueprint-declared bounds.
- **Failures** — ad-hoc failure injection (any mode, any target, any intensity).
- **Load** — master volume multiplier (scales all synthetic volume coherently across blueprints).
- **Blueprints** — enable/disable blueprints; manage custom and git-sourced blueprints.
- **Constructs / Kinds** — toggle individual construct instances or all constructs of a kind.
- **Diagnostics** — load-time problems (skipped blueprints, dropped config entries).
- **Config** — redacted runtime config view.

The schema driving the UI (available modes, targets, scenarios, scalable workloads) is derived from the currently loaded blueprints at startup — it is never hardcoded.

---

## Authentication

When `CONTROL_TOKEN` is set:

- All GET routes remain **open** (unauthenticated). The Grafana Infinity datasource reads GET routes and must not be blocked.
- All POST, DELETE, and other mutation routes require **HTTP Basic auth**: username `control`, password = `CONTROL_TOKEN`.
- A browser hitting a guarded route for the first time triggers Chrome/Firefox's native credential dialog.
- The Grafana Infinity datasource authenticates via `basicAuthUser: control` + `basicAuthPassword: <CONTROL_TOKEN>`.

!!! warning "Set CONTROL_TOKEN when the bind address is non-loopback"
    The startup log warns if `CONTROL_TOKEN` is not set and `JSON_HTTP_ADDR` is not `127.0.0.1`. POST mutations with no auth on a reachable host allow anyone to inject failures, scale workloads down to zero, or disable blueprints.

```bash
# Example: curl with auth
curl -s -u control:${CONTROL_TOKEN} -X POST http://127.0.0.1:8088/control/load \
  -H "Content-Type: application/json" \
  -d '{"volume_multiplier": 2.0}'
```

---

## Endpoint reference

### Read-only (GET)

| Endpoint | Description |
|---|---|
| `GET /control/ui/` | Embedded operator UI (SPA). Redirect from `/control/ui`. |
| `GET /control/schema` | Blueprint-derived schema: all modes, addressable targets, scenarios, scalable workloads, construct instances. Add `?audience=customer` for a reduced view without operator-internal fields. |
| `GET /control/state` | Current control snapshot (volume multiplier, active scenarios, failures, scaling, disabled blueprints/constructs/kinds). |
| `GET /control/status` | Sink readiness strip: `last_success_ms`, failure counts, dry-run flag, per-blueprint emission, Fleet Management health, persist health. |
| `GET /control/health` | Per-construct tick health and process metrics. |
| `GET /control/config` | Redacted runtime configuration (secrets replaced with `[redacted]`). |
| `GET /control/inventory` | Live emission and cardinality inventory per blueprint and construct. |
| `GET /control/diagnostics` | Load-time problems: skipped blueprints, dropped config entries, warnings. Errors first. |
| `GET /control/incidents` | Declared and runtime incidents with authoritative `active_now` flags. |
| `GET /control/blueprint?blueprint=NAME` | Raw YAML of a named blueprint (text/plain). |
| `GET /control/blueprint-schema` | Complete blueprint authoring schema derived from live Go types. |
| `GET /control/blueprints/staged` | Blueprints staged for the next restart. |
| `GET /control/blueprints/sources` | Configured git blueprint sources (token values are never included). |
| `GET /control/blueprints/pending` | Staged-vs-manifest diff driving the "restart to apply" banner. |

The root path `/` serves the Infinity JSON host — the full synthetic data schema queryable by Grafana's Infinity datasource.

### Mutations (POST / DELETE — guarded by CONTROL_TOKEN when set)

| Endpoint | Body | Description |
|---|---|---|
| `POST /control/load` | `{"volume_multiplier": 1.5}` | Set the master volume multiplier. Scales all synthetic volume coherently. |
| `POST /control/scenarios` | `{"active_scenarios": ["bp/name", ...]}` | Replace the active scenario list. Each id must match a `blueprint/scenario-name` pair in the derived schema. |
| `POST /control/scaling` | `{"workload-name": 4, ...}` | Set live workload pod counts (merge into existing scaling map). Each target must be live-scalable within its blueprint-declared bounds. Node count cascades automatically via `fixture.DeriveNodes`. |
| `POST /control/failures` | `{"mode": {"enabled": true, "intensity": 0.8, "scope": "target"}, ...}` | Ad-hoc failure injection (merge). Unknown modes are warned but accepted — an intentional escape hatch for exercising modes not yet in the schema. |
| `POST /control/blueprints` | `{"disabled_blueprints": ["name", ...]}` | Replace the disabled blueprint list. |
| `POST /control/constructs` | `{"disabled_constructs": ["bp/kind:name", ...]}` | Replace the disabled construct instance list. IDs validated against the derived schema. |
| `POST /control/kinds` | `{"disabled_kinds": ["cloudflare", ...]}` | Replace the disabled construct-kind list. All instances of these kinds go dark. |
| `POST /control/spanmetrics` | `{"span_metrics_blueprints": ["name", ...]}` | Opt-IN list for synthkit's own span-metrics emission. Default OFF (defer to Grafana Cloud metrics-generator or Beyla). |
| `POST /control/incidents` | `{"blueprint": "...", "mode": "...", "target": "...", "at": "...", "for": "...", "intensity": 0.8}` | Create a runtime incident (server mints the ID). |
| `DELETE /control/incidents/{id}` | — | Remove a runtime incident by ID. |
| `POST /control/blueprints/custom` | `{"namespace": "...", "name": "...", "yaml": "..."}` | Stage a custom blueprint upload. Takes effect on next restart. See [custom-blueprints.md](custom-blueprints.md). |
| `DELETE /control/blueprints/custom?name=ns/name` | — | Remove a staged custom blueprint. |
| `POST /control/blueprints/sources` | SourceView JSON | Upsert a git blueprint source. Token value never echoed in the response. |
| `DELETE /control/blueprints/sources?id=<id>` | — | Remove a git blueprint source. |
| `POST /control/blueprints/sources/fetch?id=<id>` | — | Trigger an immediate git fetch for a source. |
| `POST /control/blueprints/validate` | `{"yaml": "..."}` | Validate a blueprint YAML in isolation. Note: cross-blueprint substrate-identity collisions are NOT detected here; see `GET /control/diagnostics` after a restart. |
| `POST /control/reset` | — | Reset all control state to defaults. |

---

## Common operations

### Activate a scenario

```bash
curl -s -X POST http://127.0.0.1:8088/control/scenarios \
  -H "Content-Type: application/json" \
  -d '{"active_scenarios": ["mine/db-pressure"]}'
```

Scenarios are identified as `blueprint-name/scenario-name`. To deactivate all scenarios, pass an empty list.

### Scale a workload live

```bash
curl -s -X POST http://127.0.0.1:8088/control/scaling \
  -H "Content-Type: application/json" \
  -d '{"mine-api": 8}'
```

Node count cascades: the k8s cluster and EC2 construct both re-derive their node counts via `fixture.DeriveNodes` so the substrate stays consistent. Scale-down retires old pod and node series automatically (`state.DropWhere`).

### Inject a failure ad-hoc

```bash
curl -s -X POST http://127.0.0.1:8088/control/failures \
  -H "Content-Type: application/json" \
  -d '{"connection_saturation": {"enabled": true, "intensity": 0.7, "scope": "mine-db"}}'
```

### Check sink readiness

```bash
curl -s http://127.0.0.1:8088/control/status | jq '.sinks[] | {name, last_success_ms, failures}'
```

`dry_run: true` in the response means no live push is happening regardless of sink health — re-check `DRY_RUN` in your `.env`.

### Boost volume temporarily

```bash
curl -s -X POST http://127.0.0.1:8088/control/load \
  -H "Content-Type: application/json" \
  -d '{"volume_multiplier": 3.0}'
```

---

## State persistence

Control state (volume multiplier, active scenarios, scaling, disabled blueprints/constructs/kinds) persists across restarts in the snapshot file at `CONFIG_SNAPSHOT_PATH`. The file is written lazily on the first mutation — it is normal for it not to exist on a fresh deploy.

State is **not** written on a clean shutdown (it is already in the file from the last mutation). It is written only when a mutation succeeds.

!!! note "Security note for shared-use deployments"
    The control-state snapshot at `CONFIG_SNAPSHOT_PATH` may contain resolved git blueprint source token values. Restrict filesystem permissions on the host and exclude it from untrusted backups. The UI never transmits token values over the wire — it stores and displays only the env-var name (e.g. `MY_GIT_TOKEN`).

---

## See also

- [incidents.md](incidents.md) — declaring and triggering incident scenarios in blueprints
- [custom-blueprints.md](custom-blueprints.md) — uploading and managing custom blueprints
- [configuration.md](configuration.md) — `CONTROL_TOKEN`, `JSON_HTTP_ADDR`, `CONFIG_SNAPSHOT_PATH`
