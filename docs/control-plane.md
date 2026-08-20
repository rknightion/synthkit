---
title: Control Plane
description: Reference for the synthkit operator UI and HTTP control plane — endpoints, auth, and live runtime operations.
---

# Control Plane

synthkit embeds an operator control plane served on port **8088** (configured via `JSON_HTTP_ADDR`). It has two surfaces:

- **Operator UI** at `/control/ui` — a browser dashboard for live runtime management.
- **HTTP API** — a JSON API used by the UI, the Grafana Infinity datasource, and curl/automation.

With `CONTROL_TOKEN` set, every control route except sanitized `GET /control/readiness` requires
HTTP Basic auth. The sibling Infinity JSON host keeps only `/healthz` public. Loopback may remain
token-free; non-loopback exposure has an additional fail-closed acknowledgement gate.

## State vocabulary

These terms describe different points in the blueprint lifecycle:

- **Loaded** means the blueprint passed startup loading and is part of the running process.
- **Enabled** means a loaded blueprint is not in the control state's disabled list. It can be
  disabled or enabled live; this is not a YAML `enabled:` field.
- **Emitting** means the running blueprint has produced telemetry observed by the sink-status
  report. It is a runtime outcome, not a promise made by loading.
- **Staged** means a custom upload or fetched git blueprint has been written to the staging area
  for a future restart.
- **Pending** means staged custom/git content differs from the startup manifest, so a restart will
  add, remove, or update loaded content.
- **Active** describes a runtime incident scenario currently applied to its qualified
  `blueprint/scenario` ID.

Use `GET /control/status` for sink readiness and per-blueprint emission, `GET /control/inventory`
for the live emission/cardinality inventory, `GET /control/health` for per-construct tick and
process health, `GET /control/diagnostics` for startup/load problems, and
`GET /control/blueprints/pending` for staged-versus-running changes. `GET /control/blueprints/staged`
lists staged custom/git blueprints.

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

- All control reads except sanitized `/control/readiness`, all Infinity data routes except
  `/healthz`, and every mutation require **HTTP Basic auth**: username `control`, password =
  `CONTROL_TOKEN`.
- A browser hitting a guarded route for the first time triggers Chrome/Firefox's native credential dialog.
- The Grafana Infinity datasource stores the password in `secureJsonData` and authenticates its
  server-side reads. Browser-direct dashboard action buttons do not inherit datasource credentials;
  they use the browser's separate Basic challenge and therefore require a trusted HTTPS endpoint.

For the mutation examples below, run this once in Bash or Zsh. When `CONTROL_TOKEN` is set it uses a
mode-0600 temporary netrc file, keeping the token out of process arguments. When the token is unset,
requests remain unauthenticated:

```bash
control_auth=()
control_netrc=
if [ -n "${CONTROL_TOKEN:-}" ]; then
  control_netrc="$(mktemp)"
  chmod 600 "$control_netrc"
  printf 'machine 127.0.0.1 login control password %s\n' "$CONTROL_TOKEN" > "$control_netrc"
  control_auth=(--netrc-file "$control_netrc")
  trap 'test -z "$control_netrc" || rm -f "$control_netrc"' EXIT
fi
```

!!! warning "Non-loopback startup is explicit"
    Direct runs evaluate `JSON_HTTP_ADDR`; Compose evaluates the exact interpolated
    `SYNTHKIT_BIND`. A non-loopback bind fails startup unless `CONTROL_TOKEN` is non-empty and
    `CONTROL_EXPOSURE_ACK` is exactly `trusted-network` or `tls-proxy`. Never send Basic
    credentials over untrusted plaintext HTTP.

```bash
# Example: uses the temporary netrc when CONTROL_TOKEN is set
curl -fsS "${control_auth[@]}" -X POST http://127.0.0.1:8088/control/load \
  -H "Content-Type: application/json" \
  -d '{"volume_multiplier": 2.0}'
```

---

## Endpoint reference

### Read-only (GET; authenticated when `CONTROL_TOKEN` is set, except readiness)

| Endpoint | Description |
|---|---|
| `GET /control/ui/` | Embedded operator UI (SPA). Redirect from `/control/ui`. |
| `GET /control/readiness` | Public sanitized readiness probe: process, HTTP, blueprint counts, and state-writable boolean only. |
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

The root path `/` serves the Infinity JSON host. Every Infinity data route requires the same Basic
auth when `CONTROL_TOKEN` is set; only `/healthz` remains public.

### Mutations (POST / DELETE — guarded by CONTROL_TOKEN when set)

| Endpoint | Body | Description |
|---|---|---|
| `POST /control/load` | `{"volume_multiplier": 1.5}` | Set the master volume multiplier. Scales all synthetic volume coherently. |
| `POST /control/scenarios` | `{"active_scenarios": ["bp/name", ...]}` | Full replacement of the active scenario list. Kept for backward compatibility; use the item operations for ordinary UI/client toggles. Each id must match a `blueprint/scenario-name` pair in the derived schema. |
| `POST /control/scenarios/activate` | `{"scenario": "bp/name"}` | Add one active scenario. Idempotent; validates the scenario against the derived schema. |
| `POST /control/scenarios/deactivate` | `{"scenario": "bp/name"}` | Remove one active scenario. Idempotent; validates the scenario against the derived schema. |
| `POST /control/scaling` | `{"blueprint/workload": 4, ...}` | Set live workload pod counts (merge into existing scaling map). Each target must be live-scalable within its blueprint-declared bounds. Node count cascades automatically via `fixture.DeriveNodes`. |
| `POST /control/failures` | `{"mode": {"enabled": true, "intensity": 0.8, "scope": "target"}, ...}` | Ad-hoc failure injection (merge). Unknown modes are warned but accepted — an intentional escape hatch for exercising modes not yet in the schema. |
| `POST /control/blueprints` | `{"disabled_blueprints": ["name", ...]}` | Full replacement of the disabled blueprint list. Kept for backward compatibility; use the item operations for ordinary UI/client toggles. |
| `POST /control/blueprints/disable` | `{"blueprint": "name"}` | Add one blueprint to the disabled list. Idempotent. |
| `POST /control/blueprints/enable` | `{"blueprint": "name"}` | Remove one blueprint from the disabled list. Idempotent. |
| `POST /control/constructs` | `{"disabled_constructs": ["bp/kind:name", ...]}` | Replace the disabled construct instance list. IDs validated against the derived schema. |
| `POST /control/kinds` | `{"disabled_kinds": ["cloudflare", ...]}` | Replace the disabled construct-kind list. All instances of these kinds go dark. |
| `POST /control/spanmetrics` | `{"span_metrics_blueprints": ["name", ...]}` | Full replacement of the opt-in list for synthkit's own span-metrics emission. Default OFF (defer to Grafana Cloud metrics-generator or Beyla). |
| `POST /control/incidents` | `{"blueprint": "...", "mode": "...", "target": "...", "at": "...", "for": "...", "intensity": 0.8}` | Create a runtime incident (server mints the ID). |
| `DELETE /control/incidents/{id}` | — | Remove a runtime incident by ID. |
| `POST /control/blueprints/custom` | `{"namespace": "...", "name": "...", "yaml": "..."}` | Validate the exact upload against the prospective bundled/custom/git set, reject identity collisions, and stage it for the next restart. The response names its effective namespaced runtime identity. See [custom-blueprints.md](custom-blueprints.md). |
| `DELETE /control/blueprints/custom?name=ns/name` | — | Remove a staged custom blueprint. |
| `POST /control/blueprints/sources` | SourceView JSON | Upsert a git blueprint source. Token value never echoed in the response. |
| `DELETE /control/blueprints/sources?id=<id>` | — | Remove a git blueprint source. |
| `POST /control/blueprints/sources/fetch?id=<id>` | — | Trigger an immediate git fetch for a source. |
| `POST /control/blueprints/validate` | `{"yaml": "..."}` | Validate blueprint YAML in isolation for automation compatibility. Cross-blueprint collisions are checked by `POST /control/blueprints/custom` when Save stages the exact content. |
| `POST /control/reset` | — | Reset all control state to defaults. |

Array-bearing mutation routes are replacements, not additive patches: submitting an array omits every
existing item not included in that request. For routine one-at-a-time scenario and blueprint
changes, use `/control/scenarios/{activate,deactivate}` and
`/control/blueprints/{disable,enable}` instead. The item routes are idempotent and avoid replacing
another operator's change.

---

## Common operations

### Activate a scenario

```bash
curl -fsS "${control_auth[@]}" -X POST http://127.0.0.1:8088/control/scenarios/activate \
  -H "Content-Type: application/json" \
  -d '{"scenario": "mine/db-pressure"}'
```

Scenarios are identified as `blueprint-name/scenario-name`. Item activation/deactivation is safe when
another client may be changing a different scenario at the same time. To intentionally replace the
whole list (including deactivating all scenarios), use `POST /control/scenarios` with
`{"active_scenarios": []}`.

### Scale a workload live

```bash
curl -fsS "${control_auth[@]}" -X POST http://127.0.0.1:8088/control/scaling \
  -H "Content-Type: application/json" \
  -d '{"mine/mine-api": 8}'
```

Node count cascades: the k8s cluster and EC2 construct both re-derive their node counts via `fixture.DeriveNodes` so the substrate stays consistent. Scale-down retires old pod and node series automatically (`state.DropWhere`).

### Inject a failure ad-hoc

```bash
curl -fsS "${control_auth[@]}" -X POST http://127.0.0.1:8088/control/failures \
  -H "Content-Type: application/json" \
  -d '{"connection_saturation": {"enabled": true, "intensity": 0.7, "scope": "mine-db"}}'
```

### Check sink readiness

```bash
curl -fsS "${control_auth[@]}" http://127.0.0.1:8088/control/status | jq '.sinks[] | {name, last_success_ms, failures}'
```

`dry_run: true` in the response means no live push is happening regardless of sink health — re-check `DRY_RUN` in your `.env`.

### Boost volume temporarily

```bash
curl -fsS "${control_auth[@]}" -X POST http://127.0.0.1:8088/control/load \
  -H "Content-Type: application/json" \
  -d '{"volume_multiplier": 3.0}'
```

---

## State persistence

Control state (volume multiplier, active scenarios, scaling, disabled blueprints/constructs/kinds) persists across restarts in the snapshot file at `CONFIG_SNAPSHOT_PATH`. The file is written lazily on the first mutation — it is normal for it not to exist on a fresh deploy.

State is **not** written on a clean shutdown (it is already in the file from the last mutation). It is written only when a mutation succeeds.

!!! note "Security note for shared-use deployments"
    The owner-only control-state snapshot persists operational state and a private git source's
    `token_env_var` name only. The resolved PAT is read from the process environment at fetch time
    and never serialized. Exclude the snapshot from untrusted backups because its operational
    metadata can still be sensitive.

---

## See also

- [incidents.md](incidents.md) — declaring and triggering incident scenarios in blueprints
- [custom-blueprints.md](custom-blueprints.md) — uploading and managing custom blueprints
- [configuration.md](configuration.md) — `CONTROL_TOKEN`, `CONTROL_EXPOSURE_ACK`, binds, and state paths
