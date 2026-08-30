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
  "token_env_var": "MY_REPO_TOKEN"
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
| `token_env_var` | Name of an environment variable holding the HTTPS PAT for private repos. Empty = public repo. The token itself never leaves the server; only the variable name is persisted. |

`Fetch now` contacts the remote, reports authentication and network failures immediately, and stages the fetched YAML. Each source reports its fetched SHA, fetched file count, and effective names. A fetch never changes the running process: restart to load that staged snapshot. The source row separately reports the loaded SHA, pending-restart state, and the names loaded or skipped by the most recent restart.

### Polling for "update available"

Set `GIT_POLL_INTERVAL` (in seconds) to enable background polling. The poller resolves each configured source's configured ref to its current commit SHA and marks an unseen remote SHA as **update available**. Polling is change detection, not automatic apply: it does not fetch blobs, alter the staged snapshot, or change the running blueprints. Choose **Fetch now** to stage the observed revision, then restart to apply it.

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
boot manifest, and runs with that fixed set for its lifetime. Restarting does not fetch a newer remote revision. There is no hot-reload.

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

## skcapture and skforge

For blueprints derived from a real Kubernetes environment, the `skcapture` + `skforge` tools capture live cluster state and generate a blueprint skeleton from it. See [Tools](tools.md) for the full capture→forge workflow.

In brief: `skcapture` runs as a kubectl ephemeral container and exports an age-encrypted inventory snapshot; `skforge` decrypts it, maps the captured workloads to synthkit construct declarations, and emits an LLM prompt that produces a draft blueprint. The output is a starting point — review and adjust the generated YAML before staging it.
