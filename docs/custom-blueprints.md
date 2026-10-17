---
title: Custom Blueprints
description: Adding blueprints beyond the bundled set — control-plane upload, git sources, the staging layout, and restart-to-apply.
---

# Custom Blueprints

Synthkit loads blueprints from two sources at startup:

1. **Bundled blueprints** — the `blueprints/` directory baked into the binary (always present).
2. **Custom blueprints** — staged to the `BLUEPRINT_DATA_DIR` volume by upload or git fetch, then
   loaded on restart only when their exact names are included in `BLUEPRINT_NAMES` (or `*` is set).

Custom blueprints use the same YAML format as the bundled set. They are managed via the control-plane API and optionally via git sources that poll for updates.

## Control-plane upload

The simplest way to add a blueprint is `POST /control/blueprints/custom` with a JSON body carrying the namespace, name, and blueprint YAML. The form `name` may be the YAML's bare top-level `name:` or its exact effective runtime identity. The latter makes a copied blueprint's destination explicit: `{sanitised namespace}/{bare-name}` (or `custom/{bare-name}` when the namespace is empty).

Save is the authoritative preflight. Before it writes, synthkit loads the submitted YAML with that effective identity and validates the prospective built-in + staged custom + staged git set, including substrate identity collisions. A rejected save leaves the existing staged file unchanged and returns `400`.

```json
POST /control/blueprints/custom
Content-Type: application/json

{
  "namespace": "mine",
  "name": "mine/my-blueprint",
  "yaml": "name: my-blueprint\n..."
}
```

On success the response includes the effective identity, for example `{"status":"staged","name":"mine/my-blueprint"}`. A form/YAML name mismatch, invalid current YAML, or prospective-set collision returns `400`.

To check a blueprint **without** staging it, `POST /control/blueprints/validate` with body `{"yaml": "..."}`. This JSON automation-compatible endpoint remains an isolated parse and dry-run cardinality check. It is useful feedback while editing, but it does not replace Save's prospective-set preflight.

The blueprint is staged immediately but **does not take effect until restart**. Add its effective
namespaced identity to `BLUEPRINT_NAMES` before restarting; otherwise it remains staged and pending.
The control-plane UI shows a "restart to apply" banner when staged blueprints differ from what is running. See [Control Plane](control-plane.md) for the full `/control/blueprints` API.

To remove a staged upload: `DELETE /control/blueprints/custom?name=<namespace>/<name>`.

## Git sources

A git source moves through four explicit states: **configured → fetched → staged → loaded**. Configure one through the control-plane UI, which performs **Add source → Fetch now** as one guided operation, or use `POST /control/blueprints/sources` followed by `POST /control/blueprints/sources/fetch?id=<id>` yourself:

```json
{
  "id": "my-bp-repo",
  "name": "My blueprint repo",
  "namespace": "mine",
  "url": "https://github.com/example/synthkit-blueprints",
  "ref": "refs/heads/main",
  "subpath": "blueprints",
  "token_env_var": "GIT_TOKEN_REPO"
}
```

| Field | Description |
|---|---|
| `id` | Required stable lowercase slug (letters, numbers, `_`, `-`), also the on-disk directory name under `git/`. |
| `name` | Human-readable label shown in the UI. |
| `namespace` | Required lowercase slug, applied to every blueprint name from this source (`namespace/blueprint-name`). It is validated rather than silently rewritten. |
| `url` | Required absolute HTTPS repository URL with a non-empty host. SSH, embedded credentials, and fragments are rejected. |
| `ref` | Required Git ref, e.g. `refs/heads/main` or `refs/tags/v1.0`. |
| `subpath` | Directory within the repo holding `*.yaml` files (`""` = repo root). |
| `token_env_var` | `GIT_TOKEN` or an environment-variable name starting with `GIT_TOKEN_` and a non-empty suffix, holding a read-only HTTPS PAT. Empty uses the `GIT_TOKEN` default; if that is also empty, no authentication is sent. Only the variable name is persisted; the token is sent to the configured git host as HTTPS Basic auth. |

`Fetch now` contacts the remote, reports authentication and network failures immediately, and stages the fetched YAML. Each source reports its fetched SHA, fetched file count, and effective names. A fetch never changes the running process: restart to load that staged snapshot. The source row separately reports the loaded SHA, pending-restart state, and the names loaded or skipped by the most recent restart.

### Polling for "update available"

Set `GIT_POLL_INTERVAL` (in seconds) to enable background polling. The poller resolves each configured source's configured ref to its current commit SHA and marks an unseen remote SHA as **update available**. Polling is change detection, not automatic apply: it does not fetch blobs, alter the staged snapshot, or change the running blueprints. Choose **Fetch now** to stage the observed revision, then restart to apply it.

A default fallback PAT for all sources can be set via `GIT_TOKEN`; individual sources override with a `GIT_TOKEN_<suffix>` variable in `token_env_var`. Existing explicit `GIT_TOKEN` sources remain supported. Other legacy variable names must be renamed before fetching again.

Set `GIT_SOURCE_HOST_ALLOWLIST` to restrict source hosts, for example `git.example,mirror.example`. It accepts exact case-insensitive hostnames or IP addresses, not URLs, ports, wildcards or empty list entries. Subdomains are not implicitly allowed; any HTTPS port on an allowed host is permitted. Empty leaves host restrictions disabled. The policy applies both when saving sources and before fetching or polling persisted sources. Redirects are refused, so use the final HTTPS repository URL. See [Configuration](configuration.md).

## Staging layout

All custom blueprints are staged under `BLUEPRINT_DATA_DIR` (default `./data/blueprints`):

```text
data/blueprints/
├── custom/                  # uploaded blueprints
│   └── mine__my-blueprint.yaml
├── git/
│   └── my-bp-repo/          # fetched from the git source with id="my-bp-repo"
│       └── production.yaml
└── .boot-manifest.json      # records what was loaded at last startup
```

- `custom/` — uploads, named `<namespace>__<name>.yaml`.
- `git/<id>/` — one directory per configured source, containing the fetched `*.yaml` blobs.
- `.boot-manifest.json` — written by the runner at startup; records which blueprints (and fetched git source SHAs) were loaded. The control plane diffs this against the current staged state to drive the "restart to apply" banner.

## Namespacing

Every custom blueprint is prefixed with its effective namespace: `{namespace}/{name}`. For uploads, the
submitted namespace is sanitised; if sanitisation produces an empty value, it falls back to `custom`, so
the runtime identity is `custom/{name}`. The form name may be either the YAML's bare `name:` or that exact
effective identity; any other value is rejected. This is not a second rename. For git sources, the
configured namespace must already be a non-empty valid lowercase slug and is rejected if it is not—it is
never rewritten. This prevents collision between blueprints from different sources. The namespace is
applied at load time, not inside the YAML file — the file's `name:` field is the bare blueprint name; the
namespace wraps it.

Blueprint identity (the determinism seed root) includes the namespaced name, so blueprints from different namespaces with the same bare name produce distinct identities and series.

## Collision handling

Save catches collisions involving a custom upload against the prospective staged set before it writes. At restart, the loader repeats the set check for every source; this still protects git content fetched after a prior upload and logs a diagnostic for anything it skips. Fix the collision by renaming the conflicting resource in one of the blueprints, then re-stage and restart.

## Restart to apply

Custom and git blueprints take effect only on restart and only when selected by exact runtime identity
or the explicit `*` selector. The runner loads the selected staged snapshot at startup, writes the
boot manifest, and runs with that boot set for its lifetime. Restarting does not fetch a newer remote
revision. Staged uploads and git snapshots are never hot-reloaded; the one live lever is
[applying a single blueprint live](#apply-live-without-a-restart), which adds to the boot set.

The control-plane endpoint `GET /control/blueprints/pending` returns the diff between the boot manifest and the current staged state:

```json
{
  "added":   ["mine/new-service"],
  "removed": [],
  "changed": ["my-bp-repo"],
  "restart": true
}
```

`changed` lists git sources whose fetched SHA differs from the loaded SHA. `restart: true` means a restart is needed to apply pending changes. Use the command matching your deployment:

```bash
# Docker Compose (the shipped deployment)
docker compose restart synthkit

# A system service deployment
systemctl restart synthkit

# Kubernetes
kubectl rollout restart deployment/synthkit
```

After the process starts, inspect the source row or `GET /control/blueprints/sources`: it records the loaded SHA plus names that loaded and files skipped during that startup. A skipped file is not silently applied; correct it, Fetch now again, and restart.

## Apply live without a restart

`POST /control/blueprints/live` applies one namespaced blueprint to the running process. It takes the
same `namespace`, `name` and `yaml` as an upload, plus an optional opaque `owner` label (at most 128
bytes, no control characters) for the caller's own bookkeeping:

```json
POST /control/blueprints/live
Content-Type: application/json

{
  "namespace": "mine",
  "name": "my-blueprint",
  "yaml": "name: my-blueprint\n...",
  "owner": "pipeline-42"
}
```

The identity follows the [namespacing](#namespacing) rules (`mine/my-blueprint`). Before anything
changes, synthkit loads the YAML under that identity and repeats the restart collision check against
every running blueprint. An invalid document, name or owner returns `400`; a running blueprint with
the same identity (including a second apply of a live one) or a colliding substrate claim returns
`409` naming the claim. A rejected apply leaves the running set and the persisted state unchanged.
On success the response is `{"status":"applied","blueprint":"mine/my-blueprint","owner":"pipeline-42"}`
and each of the blueprint's lanes emits within one construct interval. `GET /control/inventory`
lists it with `"live": true`, its owner and its construct and workload lanes.

A live blueprint can carry Synthetic Monitoring. Its checks go through the same fail-closed handoff
as the boot set ([Synthetic Monitoring](synthetic-monitoring.md)), computed over every running
blueprint plus this one:

- The apply publishes that snapshot (an HA standby only validates; it never writes). The write is a
  compare-and-swap: it is skipped when the published snapshot already has that hash, and it is
  refused with `503` `sm_state_busy`, changing nothing, while an `sm-provision` mutation is pending.
  On the file state backend `sm-provision` holds the state lock for its whole run, so a live SM
  apply during that run returns `503` `sm_state_unavailable` instead.
  Do not live-apply SM while `sm-provision` runs; retry once it completes. The blueprint is
  admitted, but its SM construct is stripped unless the stored registration matches that exact
  snapshot. A matching registration is injected and the checks emit.
- To activate checks the registration does not yet cover, run the usual provisioner preview and
  apply, then restart. The boot snapshot covers the persisted live set too, so the restart restores
  the blueprint with its registration.
- A probe declared with another region or coordinates than a running blueprint's returns `409`
  naming the probe. An SM construct with no checks, an invalid check, or SM with no SM state
  configured returns `400`, before any snapshot write. Unreadable SM state returns `503`
  `sm_state_unavailable`. If a later step fails after the snapshot write (reading the registration,
  or persisting the live record), the published snapshot covers the rejected blueprint until the next
  publish.
- HA lease mode without the `kubernetes` state backend strips SM from live blueprints as it does
  from boot ones, with no snapshot write.
- When `STARTUP_DISABLED_KINDS` lists `synthetic_monitoring`, a live or restored blueprint's SM is
  stripped without a snapshot write.

DELETE does not republish the snapshot: the removed checks stay in it until the next Synthetic
Monitoring apply or restart publishes the running set again. The registration must match the whole
set, so after a live SM apply the registration does not cover, or a DELETE of a live SM blueprint,
the next restart (a pod reschedule or an HA leader failover included) publishes a snapshot the stored registration no longer
matches, and every blueprint's SM, boot blueprints included, stays stripped until the provisioner
applies again. Run the provisioner before restarting. At most 64
blueprints can be live at once (counting persisted records the restore skipped); the next apply
returns `409` `live blueprint limit reached (64)`. Each record carries its YAML inline in the control
snapshot, and the Kubernetes state backend stores that snapshot in one ConfigMap document bounded at
about 1 MiB, so keep live blueprints small there.

`DELETE /control/blueprints/live?name=mine/my-blueprint` removes it: its constructs, workloads,
substrate lanes and cumulative counters stop and leave the inventory, and other blueprints are
unaffected. The same write forgets the control state keyed by that name (its disabled flag, span
metrics opt-in, active scenarios, construct disables, scaling and runtime incidents), so applying it
again later starts enabled and from fresh state. Fleet Management collectors it declared leave the
registered roster and are unregistered on the controller's next reconcile. Rows already recorded in
`GET /control/status` per-blueprint emission and `GET /control/health` tick health remain until
restart. DELETE removes any name in the persisted live set, running or not. For a skipped record
whose name is now a running boot blueprint, DELETE drops only the live record: the boot blueprint
keeps running and keeps its settings. Boot blueprints themselves are not
removable this way (`404`). There is no expiry and no history backfill: a live blueprint runs until
it is removed.

The live set is stored in the control snapshot (`CONFIG_SNAPSHOT_PATH`) and survives a restart. At
startup the boot set selected by `BLUEPRINT_NAMES` loads first and takes precedence; each persisted
live blueprint is then re-validated against it. One that no longer loads, or now collides (for
example because `BLUEPRINT_NAMES` selects a staged upload with the same identity, or because its
Synthetic Monitoring probe now conflicts with the boot set), is skipped and logged as an error rather
than failing startup. A restored blueprint's Synthetic Monitoring goes through the same handoff as a
live apply, except that the restore never writes the snapshot: the startup (or, in HA, the leader
acquisition) already published one covering the persisted live set, and the restore only validates
against it. An HA leader failover is a restart for this purpose. Its record stays in the snapshot until
`DELETE /control/blueprints/live` removes it. `POST /control/reset` does not remove live blueprints.

## skcapture and skforge

For blueprints derived from a real Kubernetes environment, the `skcapture` + `skforge` tools capture live cluster state and generate a blueprint skeleton from it. See [Tools](tools.md) for the full capture→forge workflow.

In brief: `skcapture` runs as a kubectl ephemeral container and exports an age-encrypted inventory snapshot; `skforge` decrypts it, maps the captured workloads to synthkit construct declarations, and emits an LLM prompt that produces a draft blueprint. The output is a starting point — review and adjust the generated YAML before staging it.
