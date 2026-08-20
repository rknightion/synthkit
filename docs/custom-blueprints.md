---
title: Custom Blueprints
description: Adding blueprints beyond the bundled set — control-plane upload, git sources, the staging layout, and restart-to-apply.
---

# Custom Blueprints

Synthkit loads blueprints from two sources at startup:

1. **Bundled blueprints** — the `blueprints/` directory baked into the binary (always present).
2. **Custom blueprints** — staged to the `BLUEPRINT_DATA_DIR` volume by upload or git fetch, then applied on restart.

Custom blueprints use the same YAML format as the bundled set. They are managed via the control-plane API and optionally via git sources that poll for updates.

## Control-plane upload

The simplest way to add a blueprint is `POST /control/blueprints/custom` with a JSON body carrying the namespace, name, and blueprint YAML. The form `name` is required to exactly match the YAML's top-level `name:`. This deliberately removes the former ambiguity: the effective runtime identity is always `{sanitized namespace}/{name}` (or `custom/{name}` when the namespace is empty).

Save is the authoritative preflight. Before it writes, synthkit loads the submitted YAML with that effective identity and validates the prospective built-in + staged custom + staged git set, including substrate identity collisions. A rejected save leaves the existing staged file unchanged and returns `400`.

```json
POST /control/blueprints/custom
Content-Type: application/json

{
  "namespace": "mine",
  "name": "my-blueprint",
  "yaml": "name: my-blueprint\n..."
}
```

On success the response includes the effective identity, for example `{"status":"staged","name":"mine/my-blueprint"}`. A form/YAML name mismatch, invalid current YAML, or prospective-set collision returns `400`.

To check a blueprint **without** staging it, `POST /control/blueprints/validate` with body `{"yaml": "..."}`. This JSON automation-compatible endpoint remains an isolated parse and dry-run cardinality check. It is useful feedback while editing, but it does not replace Save's prospective-set preflight.

The blueprint is staged immediately but **does not take effect until restart**. The control-plane UI shows a "restart to apply" banner when staged blueprints differ from what is running. See [Control Plane](control-plane.md) for the full `/control/blueprints` API.

To remove a staged upload: `DELETE /control/blueprints/custom?name=<namespace>/<name>`.

## Git sources

A git source is a repository (HTTPS only) that synthkit fetches YAML blueprints from on demand. Configure one via `POST /control/blueprints/sources` or through the control-plane UI:

```json
{
  "id": "my-bp-repo",
  "name": "My blueprint repo",
  "namespace": "mine",
  "url": "https://github.com/example/synthkit-blueprints",
  "ref": "refs/heads/main",
  "subpath": "blueprints",
  "token_env_var": "MY_REPO_TOKEN"
}
```

| Field | Description |
|---|---|
| `id` | Stable slug, also the on-disk directory name under `git/`. |
| `name` | Human-readable label shown in the UI. |
| `namespace` | Prefix applied to every blueprint name from this source (`namespace/blueprint-name`). |
| `url` | Repository URL (HTTPS). SSH is not supported. |
| `ref` | Git ref to fetch from, e.g. `refs/heads/main` or `refs/tags/v1.0`. |
| `subpath` | Directory within the repo holding `*.yaml` files (`""` = repo root). |
| `token_env_var` | Name of an environment variable holding the HTTPS PAT for private repos. Empty = public repo. The token itself never leaves the server; only the variable name is persisted. |

Trigger a fetch: `POST /control/blueprints/sources/fetch?id=<id>`. Synthkit fetches the remote HEAD SHA, compares it to the last applied SHA, and downloads updated YAML blobs only when the SHA has changed.

### Polling for "update available"

Set `GIT_POLL_INTERVAL` (in seconds) to enable background polling. The poller checks `HEAD` for each configured source on the interval and marks sources as "update available" when a new SHA is found. Polling updates only the cached SHA — it does not re-fetch blobs automatically. Use `POST /control/blueprints/sources/fetch?id=<id>` to apply the update, then restart.

A default fallback PAT for all sources can be set via `GIT_TOKEN`; individual sources override with `token_env_var`. See [Configuration](configuration.md).

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
- `.boot-manifest.json` — written by the runner at startup; records which blueprints (and git source SHAs) were applied. The control plane diffs this against the current staged state to drive the "restart to apply" banner.

## Namespacing

Every custom blueprint is prefixed with its effective namespace: `{namespace}/{name}`. The effective
namespace is the submitted namespace after sanitization; an empty submitted namespace falls back to
`custom`, so the runtime identity is `custom/{name}`. This prevents collision between blueprints from
different sources. The form name must exactly match YAML `name:`; it is never a second rename. The namespace:

- Is sanitised to a URL-safe slug (lowercase alphanumeric characters, `_`, and `-`).
- Is applied at load time, not inside the YAML file — the file's `name:` field is the bare blueprint name; the namespace wraps it.

Blueprint identity (the determinism seed root) includes the namespaced name, so blueprints from different namespaces with the same bare name produce distinct identities and series.

## Collision handling

Save catches collisions involving a custom upload against the prospective staged set before it writes. At restart, the loader repeats the set check for every source; this still protects git content fetched after a prior upload and logs a diagnostic for anything it skips. Fix the collision by renaming the conflicting resource in one of the blueprints, then re-stage and restart.

## Restart to apply

Custom and git blueprints take effect only on restart. The runner loads all staged blueprints at startup, writes the boot manifest, and runs with that fixed set for its lifetime. There is no hot-reload.

The control-plane endpoint `GET /control/blueprints/pending` returns the diff between the boot manifest and the current staged state:

```json
{
  "added":   ["mine/new-service"],
  "removed": [],
  "changed": ["my-bp-repo"],
  "restart": true
}
```

`changed` lists git sources whose latest fetched SHA differs from the applied SHA. `restart: true` means a restart is needed to apply pending changes.

## skcapture and skforge

For blueprints derived from a real Kubernetes environment, the `skcapture` + `skforge` tools capture live cluster state and generate a blueprint skeleton from it. See [Tools](tools.md) for the full capture→forge workflow.

In brief: `skcapture` runs as a kubectl ephemeral container and exports an age-encrypted inventory snapshot; `skforge` decrypts it, maps the captured workloads to synthkit construct declarations, and emits an LLM prompt that produces a draft blueprint. The output is a starting point — review and adjust the generated YAML before staging it.
