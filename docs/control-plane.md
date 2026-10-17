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
  report. It is a runtime outcome, not a promise made by loading. The opt-in public probe field
  `blueprint_emitting` (see [Per-blueprint flags on the public probe](#per-blueprint-flags-on-the-public-probe))
  is a different, state-derived signal: enabled and scheduled to tick, not observed delivery.
- **Staged** means a custom upload or fetched git blueprint has been written to the staging area
  for a future restart.
- **Pending** means staged custom/git content differs from the startup manifest, so a restart will
  add, remove, or update loaded content.
- **Active** describes a runtime incident scenario currently applied to its qualified
  `blueprint/scenario` ID.

Use `GET /control/status` for sink readiness and per-blueprint emission, `GET /control/inventory`
for the live emission/cardinality inventory and safe query identity, `GET /control/health` for per-construct tick and
process health, `GET /control/diagnostics` for startup/load problems, and
`GET /control/blueprints/pending` for staged-versus-running changes. `GET /control/blueprints/staged`
lists staged custom/git blueprints.

---

## Operator UI

Open `http://127.0.0.1:8088/control/ui` (or the host's address if exposed).

### Managed console features

`CONTROL_MANAGED_FEATURES` optionally contains a JSON object such as
`{"blueprint_sources":"Managed by operator","custom_uploads":"Use the approved blueprint process","reset":"Contact the operator to reset"}`.
The only admitted keys are `blueprint_sources`, `custom_uploads`, and `reset`;
each value must be a nonempty reason. Unknown keys and malformed objects fail startup.
An empty variable or `{}` preserves the existing index response bytes.

The console disables remote-source add/fetch/delete controls, custom-upload
validate/save/delete controls, and reset respectively, and displays the configured
reason as text. Runtime metadata beside the base-path tags is HTML-escaped before
injection. Built assets are shared unchanged across configurations. This is
**presentation only**, not authorization: direct API reads and mutations remain
available under the existing authentication/exposure policy. Do not use these locks
as a security boundary. Compose passes the variable through its selected env file.

### Reverse-proxy prefix

Set `CONTROL_BASE_PATH=/x/y` for the external console URL `/x/y/control/ui/`.
The same UI build works at any configured prefix: the server inserts a trusted
`<base href="/x/y/control/ui/">` and API prefix `/x/y/control/` into the page.
Navigation, assets, deep links and the trailing-slash redirect use that prefix.
Empty (the default) keeps the existing `/control/ui/` and `/control/` URLs.

The prefix is a canonical absolute path, without a trailing slash, encoding,
empty segments, `.`/`..` segments, query or fragment. Only ASCII letters, digits,
`.`, `_`, `~` and `-` within segments are accepted. Invalid configuration fails
loading. `X-Forwarded-Prefix` and other request headers are never trusted; the
operator must configure the actual public prefix. This setting does not replace
the token/exposure requirements above.

A proxy can strip `/x/y` before forwarding `/control/` to synthkit, or preserve
the prefix when the host router mounts the control handler at `/x/y/control/`.
The handler strips a preserved prefix before dispatch, while injecting external
URLs for either proxy mode. Both ordinary and HA modes configure the base path
before serving and mount the handler at `CONTROL_BASE_PATH + "/control/"` as well
as `/control/` for stripping proxies. With an empty prefix, `/control/` is mounted
once. Do not apply `http.StripPrefix` a second time. The prefix is not a new
API wrapper: Infinity GET payloads and existing root-host routes are unchanged.

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

With no `BLUEPRINT_NAMES` selection, the UI remains available in a distinct **Setup required**
state and states that no synthetic telemetry is emitted. The public readiness probe remains healthy
so the container does not restart-loop, while `live_ready` stays `false` until a selected blueprint
has completed its intended delivery lanes.

---

## Authentication

When `CONTROL_TOKEN` is set:

- All control reads except sanitized `/control/readiness`, all Infinity data routes except
  `/healthz`, and every mutation require **HTTP Basic auth**: username `control`, password =
  `CONTROL_TOKEN`.
- A browser hitting a guarded route for the first time triggers Chrome/Firefox's native credential dialog.
- The Grafana Infinity datasource stores the password in `secureJsonData` and authenticates its
  server-side reads and `infinity` actions. Browser-direct (`fetch`) dashboard action buttons do not
  inherit datasource credentials; they use the browser's separate Basic challenge and therefore
  require a trusted HTTPS endpoint.

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

## Customer control dashboard

`synthkit-control-dash` generates the customer dashboard with `-action-mode fetch|infinity` and
`-ds-uid`. `fetch` is the default and preserves browser-direct POST actions. `infinity` sends each
POST server-side through the named Infinity datasource and requires both `-ds-uid` and
`-write-base-url`. Grafana's `vizActionsAuth` feature toggle must be enabled for Infinity actions;
see [Grafana's feature note](https://grafana.com/whats-new/2025-09-03-actions-authentication-with-the-infinity-data-source/).

In Infinity mode the control-plane credential is stored in the Infinity datasource, and
`-write-base-url` must be an absolute HTTP or HTTPS URL without credentials, a query or a fragment
that the datasource reaches. It must not end in `/control`; generated actions append `/control/...`.
Without `-write-base-url` in fetch mode, buttons POST to relative `/control/...` paths on Grafana's
own origin and work only when a reverse proxy on that origin routes `/control/` to the synthkit
control plane. Otherwise pass a browser-reachable `-write-base-url`. In `infinity` mode, plain HTTP
is acceptable only for the hop from Grafana through Private Data Source Connect (PDC), or from inside
the cluster, to a `ClusterIP` control-plane Service. Never expose the control plane over plain HTTP
across the public internet; use HTTPS elsewhere. Anyone who
can query the Infinity datasource can issue authenticated control POSTs and change load or scenarios, even without
viewing this dashboard or using its action buttons. Restrict datasource query access and dashboard
visibility to operators. Grafana's Viewer restriction on dashboard actions does not protect the
datasource from direct queries. The upstream contributor reported a Grafana 13.3 click-test through
PDC; synthkit's offline generation checks do not verify live action execution.

For an instructor console, pass `-layout control-plane -action-mode infinity` with `-ds-uid`,
`-prom-uid` and `-loki-uid`. The generated dashboard has status tiles, one Start/Stop card per
scenario, emitted-family impact charts for eligible target services, collapsed fleet controls and
a three-hour default range. `-folder` annotates its destination folder. Impact charts are omitted
when the target node has no matching emitted metric or log family.

## Endpoint reference

### Read-only (GET; authenticated when `CONTROL_TOKEN` is set, except readiness)

| Endpoint | Description |
|---|---|
| `GET /control/ui/` | Embedded operator UI (SPA). Redirect from `/control/ui`. |
| `GET /control/readiness` | Public sanitized readiness probe: process, HTTP, blueprint counts, and state-writable boolean only. With `READINESS_BLUEPRINT_FLAGS=true` it also carries `blueprint_emitting`, one boolean per loaded blueprint name; see [Per-blueprint flags on the public probe](#per-blueprint-flags-on-the-public-probe). |
| `GET /control/schema` | Blueprint-derived schema: all modes, addressable targets, scenarios, scalable workloads, construct instances, and startup cost projections for explicit high-DPM blueprints. Add `?audience=customer` for a reduced view without operator-internal fields. |
| `GET /control/state` | Current control snapshot (volume multiplier, active scenarios, failures, scaling, disabled blueprints/constructs/kinds). |
| `GET /control/status` | Sink readiness strip: `last_success_ms`, failure counts, dry-run flag, per-blueprint emission, Fleet Management health, persist health. |
| `GET /control/health` | Per-construct tick health and process metrics. |
| `GET /control/config` | Redacted runtime configuration (secrets replaced with `[redacted]`). |
| `GET /control/inventory` | Live emission and cardinality inventory per blueprint and construct. Each construct may expose a low-cardinality query identity: its explicit scope plus only the declared selectors needed to query it. Blueprint-scoped output has its `blueprint` selector; substrate output instead names its declared substrate identity and never gains a blueprint selector. A blueprint applied with `POST /control/blueprints/live` also carries `"live": true` and its `owner` label when one was given; other entries omit both fields. |
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
| `POST /control/blueprints/live` | `{"namespace": "...", "name": "...", "yaml": "...", "owner": "..."}` | Apply one namespaced blueprint to the running process without a restart. `owner` is optional and opaque (at most 128 bytes, no control characters). The blueprint is validated against the running set and only then persisted and started; `200 {"status":"applied","blueprint":"ns/name","owner":"..."}`, `400` for invalid YAML, name or owner or an invalid Synthetic Monitoring check (or an SM construct with no checks), `409` naming the colliding claim (a running `ns/name`, including a re-apply, a substrate identity, or a Synthetic Monitoring probe declared with another region or coordinates) or `live blueprint limit reached (64)`, `404` when blueprint admin is not configured. Lanes emit within one construct interval. Synthetic Monitoring goes through the boot handoff: the apply publishes the running set's snapshot by compare-and-swap (`503` `sm_state_busy` while an `sm-provision` mutation is pending, `503` `sm_state_unavailable` when SM state cannot be read), and SM emits only when the stored registration matches it, otherwise its construct is stripped until a provisioner apply and restart. See [custom-blueprints.md](custom-blueprints.md#apply-live-without-a-restart). |
| `DELETE /control/blueprints/live?name=ns/name` | — | Remove a live-applied blueprint and every lane and cumulative counter it owned, and forget its per-name control state (disable, scaling, runtime incidents and similar) in the same write; its Fleet Management collectors are unregistered on the next reconcile, while status and health rows remain until restart. Any name in the persisted live set is removable. For a skipped record whose name is now a running boot blueprint, DELETE drops only the live record and keeps that boot blueprint running with its settings. Removing a blueprint that declares Synthetic Monitoring does not republish the SM snapshot; the next restart (or HA leader failover) publishes one without its checks, which the stored registration no longer matches, so every blueprint's SM stays stripped until the provisioner applies again. `200 {"status":"removed","blueprint":"ns/name"}`, or `404` when the name was not live-applied (boot blueprints are not removable live). |
| `POST /control/blueprints/sources` | SourceView JSON | Upsert a git blueprint source. Token value never echoed in the response. |
| `DELETE /control/blueprints/sources?id=<id>` | — | Remove a git blueprint source. |
| `POST /control/blueprints/sources/fetch?id=<id>` | — | Trigger an immediate git fetch for a source. |
| `POST /control/blueprints/validate` | `{"yaml": "..."}` | Validate blueprint YAML in isolation for automation compatibility. Cross-blueprint collisions are checked by `POST /control/blueprints/custom` when Save stages the exact content. |
| `POST /control/reset` | — | Reset all control state to defaults. Live-applied blueprints keep running and stay persisted; remove them with `DELETE /control/blueprints/live`. |

Array-bearing mutation routes are replacements, not additive patches: submitting an array omits every
existing item not included in that request. For routine one-at-a-time scenario and blueprint
changes, use `/control/scenarios/{activate,deactivate}` and
`/control/blueprints/{disable,enable}` instead. The item routes are idempotent and avoid replacing
another operator's change.

---

### Per-blueprint flags on the public probe

`GET /control/readiness` is public so Docker can call it without credentials, and by default it
names no blueprint: it carries aggregate `blueprints.loaded`, `skipped` and `active` counts only.
When `CONTROL_TOKEN` is configured, an operator that does not hold it therefore cannot see per-blueprint
scheduling state.
`READINESS_BLUEPRINT_FLAGS=true` (default `false`) adds it to the public probe itself: whether
each loaded blueprint is enabled and scheduled to tick, not whether telemetry was delivered.

- **Off (default).** The response is byte-identical to the probe without this setting; a golden test
  pins the exact bytes. The setting is read once at startup.
- **On.** One field is appended after the existing ones:
  `"blueprint_emitting":{"alpha":true,"beta":false}`. The key is the blueprint's plain name and the
  value is a bare boolean, so nothing else can ride along per blueprint: no reason, error, path,
  lane, source or secret. The object is `{}` when nothing is loaded and keys are sorted. The HTTP
  status, `ready` and `reason_codes` are unchanged by the flags. `GET /control/status` is unchanged.
- **What `true` means.** The blueprint is loaded (skipped blueprints are not loaded, so they are
  absent, not `false`) and is not in the control state's disabled list; at least one of its schema
  constructs is enabled, meaning its kind is not in the disabled kinds and its `blueprint/kind:name`
  key is not in the disabled constructs; and the process is in a state that runs ticks: running, serving HTTP, not in setup mode, leading when HA is enabled (a
  standby reports all `false`), and with no HA bootstrap or transition outstanding. It is a
  statement about control state, not an observation of delivery. Sink health stays in `ready`,
  `live_ready` and `reason_codes`, and a blueprint in `DRY_RUN` reads `true` while `live_ready` is
  `false`. For observed push history use authenticated `GET /control/status`.
- **Naming decision: plain name.** Options were the plain name, an opaque or hashed id, or not
  exposing blueprints. A blueprint name is topology and can carry operator meaning, so listing it
  weakens the sanitized-probe contract on purpose, and only when the operator opts in. An unsalted
  hash of a short, guessable name hides nothing, while a keyed id needs a new secret and gives the
  consumer a mapping to maintain, which defeats the purpose of reading the probe without
  credentials. The operator chose the names and is the one enabling the setting, so the plain name is
  the honest option: enabling it publishes the names of loaded blueprints to anyone who can reach the
  port. Expose the port accordingly, for example only on a trusted network or behind a proxy that
  limits `/control/readiness`. A test pins that keys are plain loaded names and that names in the
  disabled list but not loaded never appear.
- **Browser reach.** The control plane's CORS wrapper echoes any request `Origin` on every route,
  including `/control/readiness`. With the setting on, a web page opened in a browser that can
  reach the port can therefore read the blueprint names cross-origin, not only a process on that
  network. A trusted-network exposure must account for that, for example by keeping the port off
  networks that run untrusted browsers or by limiting `/control/readiness` at a proxy. CORS
  behaviour is unchanged by this setting.
- **Limits of the construct test.** It reads the control schema's construct list, which has no
  workloads: workloads are not enabled or disabled per instance and keep ticking while the
  blueprint is enabled. A blueprint that declares workloads and whose constructs are all disabled
  therefore reads `false` although its workloads still run. A loaded blueprint with no schema
  constructs at all keeps its blueprint-level state, since the construct test cannot judge it. With
  no schema source configured the construct test is skipped for every blueprint and only the
  blueprint-level conditions apply. Use authenticated `GET /control/inventory` for what a
  blueprint actually produces.

## Common operations

### Verify a selected blueprint without container shell access

Use the control surfaces in this order: `/control/readiness`, authenticated `/control/status`,
`/control/diagnostics`, `/control/inventory`, and `/control/health`. The status payload names a
blocked delivery lane; diagnostics names a rejected blueprint or load problem; health names a
failing construct tick. The X-ray view renders the same inventory, including each construct's
scope and safe query identity, so a blueprint-scoped family can be queried with its declared
`blueprint` selector while a substrate family is queried by declared substrate identity.

After readiness, wait two declared emission intervals plus the delivery deadline before treating a
missing declared signal class as evidence. Use the inventory's exact metric names and query identity
instead of inventing a hand-maintained mapping. Logs and traces are checked by their listed source or
service identity in the same bounded window. A `200` from a mutation only proves that control state
changed; the before/after emitted-data observation remains a separate acceptance check.

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
curl -fsS "${control_auth[@]}" http://127.0.0.1:8088/control/status | jq '.sinks[] | {sink, last_success_ms, failures, last_acknowledgement}'
```

`dry_run: true` in the response means no live push is happening regardless of sink health — re-check `DRY_RUN` in your `.env`.

`last_acknowledgement` distinguishes protocol acceptance, partial rejection and unverified or
deferred outcomes. HTTP transport success alone is not a durable checkpoint receipt or backend
readback. See [checkpoint foundations](checkpoint-foundations.md) for units and current limits.

### Boost volume temporarily

```bash
curl -fsS "${control_auth[@]}" -X POST http://127.0.0.1:8088/control/load \
  -H "Content-Type: application/json" \
  -d '{"volume_multiplier": 3.0}'
```

---

## Disabling kinds at startup

`POST /control/kinds` needs a running process, so the first tick has already gone out by the time it
is called. To keep a kind dark from the first tick, set `STARTUP_DISABLED_KINDS` to a comma-separated
list of bare construct kinds (see [configuration.md](configuration.md)):

```bash
STARTUP_DISABLED_KINDS=synthetic_monitoring,fleet_management
```

The setting is generic: any construct kind the catalog registers is valid, whether or not a selected
blueprint declares it, so setup mode and a partial boot set accept the same value. A kind the catalog
does not register (a typo) fails startup with an error naming it, using the same kind check as
`POST /control/kinds`.

**Precedence.** The effective `disabled_kinds` at boot is the list persisted in the snapshot at
`CONFIG_SNAPSHOT_PATH` (or the Kubernetes control document) **unioned** with the startup list. The
union is written back through the ordinary state mutation path and applied before the first tick, so
`GET /control/state` reports the effective list from the first request. With HA enabled, only the
leader writes it, during activation.

- `POST /control/kinds` still **replaces** the whole list and persists the replacement. A replacement
  that omits a startup kind re-enables it for the rest of the run, except `synthetic_monitoring`: its
  lane was removed from the process at startup, so omitting it does not restore the lane (see below).
- On the next restart the startup kinds are added again on top of whatever was persisted, so removing
  a kind from the live list never outlives a restart while the variable still names it.
- Removing a kind from `STARTUP_DISABLED_KINDS` does not re-enable it while the snapshot still
  lists it: replace the list with `POST /control/kinds` first.
- `POST /control/reset` clears the list along with the rest of the state for the rest of the run; the
  next restart adds the startup kinds again.

Disabling a kind stops its construct ticks, so no series of that kind is pushed. Two kinds also have
a side effect outside the tick:

- `fleet_management`: while the kind is disabled, at startup or through `POST /control/kinds`, the
  Fleet Management controller is suspended. It makes no request of any kind: no registration, no
  heartbeat and no unregistration. Collectors an earlier run or earlier heartbeats registered stay
  registered (they simply stop heartbeating), and enabling the kind again resumes heartbeats.
  Disabling the kind never unregisters anything (ordinary roster shrinkage while the kind is enabled
  still does; teardown is the explicit `-fleet-unregister` path, and `-fleet-unregister-owned`
  removes everything the ownership ledger records). See
  [fleet-management.md](fleet-management.md#disabling-fleet-management-from-the-first-tick).
- `synthetic_monitoring`: the lane is removed before the provisioning handoff, as an unregistered lane
  is, so the provisioner's snapshot is removed (an empty lane set invalidates any prior one) and
  `sm-provision` refuses to run with "private snapshot is unavailable or invalid"; nothing is registered
  with the Synthetic Monitoring API. The kind stays valid in `POST /control/kinds` and listed in `/control/schema` kinds,
  so re-posting the list `GET /control/state` returned works. Because the lane no longer exists in the
  process, a replacement that omits `synthetic_monitoring` does **not** bring it back: it stays absent
  until the variable is removed and the process restarts.

---

## State persistence

Control state (volume multiplier, active scenarios, scaling, disabled blueprints/constructs/kinds, and the live-applied blueprint set with its owner labels) persists across restarts in the snapshot file at `CONFIG_SNAPSHOT_PATH`. The file is written lazily on the first mutation — it is normal for it not to exist on a fresh deploy.

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
