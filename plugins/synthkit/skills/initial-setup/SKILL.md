---
name: initial-setup
description: "Deploy synthkit for first use: safely configure its .env and Docker Compose checkout; not generic Docker setup."
---

# synthkit initial setup

Guide a Grafana staff user from a fresh checkout to a running, verified synthkit deployment.
Ask the right questions, collect every required credential **safely**, write `.env`, deploy with
docker-compose, and validate. Full variable reference: [references/credentials.md](references/credentials.md).

## Locate the synthkit checkout

Plugin installation provides guidance and helpers only; it does not install synthkit or create a
checkout. Before any repository command, establish and verify the checkout root:

```bash
SYNTHKIT_CHECKOUT="/absolute/path/to/synthkit"
SYNTHKIT_CHECKOUT="$(git -C "$SYNTHKIT_CHECKOUT" rev-parse --show-toplevel)" || exit 1
test -f "$SYNTHKIT_CHECKOUT/AGENTS.md" && \
  test -f "$SYNTHKIT_CHECKOUT/docker-compose.yml" && \
  test -f "$SYNTHKIT_CHECKOUT/.env.example" || exit 1
cd "$SYNTHKIT_CHECKOUT"
```

Use `$SYNTHKIT_CHECKOUT` for every repository file and command. Invoke plugin-owned helpers through
`${CLAUDE_PLUGIN_ROOT}` and pass them absolute targets under `$SYNTHKIT_CHECKOUT`.

**Core rules**
- NEVER invent an env-var name. Use only the vars in `references/credentials.md` (they are gate-enforced).
- The customer/synthetic stack and the staff stack NEVER share a token.
- Default to the **secure** secret path (below). `.env` is gitignored — keep it that way.
- `DRY_RUN` stays `true` until the dry-run gate passes.
- NEVER run a command that prints secret values into context: no `cat .env`, no
  `docker compose config` (it interpolates and echoes env values), and no `echo` of a secret. Inspect
  `.env` only with presence/shape checks like
  `grep -q '^KEY=.\+' "$SYNTHKIT_CHECKOUT/.env"`.

## Step 1 — Preflight
Run (report failures, don't proceed past them):
- `docker --version && docker compose version` — both must exist.
- `test -f "$SYNTHKIT_CHECKOUT/.env" && echo "EXISTS — do NOT clobber; offer to review/extend" || echo "no .env yet"`.

## Step 2 — Scope questions (ask before collecting creds)
Ask the user (use AskUserQuestion):
1. Customer/synthetic-data stack details ready? (mandatory)
2. Also ship synthkit's own telemetry to a **staff** stack? → self-observability and/or profiling.
3. Optional lanes to enable now: Fleet Management, Synthetic Monitoring, RUM (Faro).
4. Deploy target: this machine (local) now, or a remote host? (remote = handoff, see Step 7).
5. Network exposure: loopback `127.0.0.1` (default, safest) or `0.0.0.0`?
Their answers select which credential groups Step 3 collects.

## Step 3 — Collect credentials (per chosen lane)
For each selected lane, look up its exact vars + where to generate them in
[references/credentials.md](references/credentials.md). Tell the user the precise scopes for each token.

### Secret handling
**Why it matters:** the agent's Bash tool runs in a *separate, non-interactive* shell. A secret you
`export` in your own terminal is invisible to the agent; and any command the agent writes that
contains the secret value puts that value in model context. The only way that is BOTH out-of-context
AND readable by docker-compose is **you writing the secret into `.env` from your own shell**.

- **Secure (required):** after Step 4 has created `.env`, have the operator run this in their own
  terminal and type the value at its hidden prompt:
  `bash "$SYNTHKIT_CHECKOUT/plugins/synthkit/skills/initial-setup/scripts/add-secret.sh" GC_TOKEN "$SYNTHKIT_CHECKOUT/.env"`
  The helper atomically replaces every existing `GC_TOKEN` entry, publishes a mode-0600 file, and
  never prints the value. Substitute another secret key when adding that key.
- When an agent is authorised to write, invoke the same helper; it must not receive the secret in
  an argument or chat: `bash "${CLAUDE_PLUGIN_ROOT}/skills/initial-setup/scripts/add-secret.sh" GC_TOKEN "$SYNTHKIT_CHECKOUT/.env"`.
  Verify **presence only** with
  `grep -q '^GC_TOKEN=.\+' "$SYNTHKIT_CHECKOUT/.env" && echo ok`.

**The secret vars (always secure path, never `set-env.sh`):** `GC_TOKEN`, `GC_FARO_COLLECTOR`,
`GC_FARO_APP_KEY`,
`GC_SELF_OTLP_PASSWORD`, `GC_PYROSCOPE_PASSWORD`, `GC_FM_TOKEN`, `GC_SM_TOKEN`, `GC_SIGIL_TOKEN`,
`GIT_TOKEN`, and `CONTROL_TOKEN`. A custom blueprint source's operator-defined `token_env_var` is
also a secret private-git token; do not add its value to a blueprint or print it.
Everything else is non-secret config, written by the agent with
`bash "${CLAUDE_PLUGIN_ROOT}/skills/initial-setup/scripts/set-env.sh" KEY VALUE "$SYNTHKIT_CHECKOUT/.env"`
(it *upserts* — replaces any existing line, so re-runs don't duplicate).

## Step 4 — Assemble .env
- If absent: `cp "$SYNTHKIT_CHECKOUT/.env.example" "$SYNTHKIT_CHECKOUT/.env"`.
- Write the **non-secret** config with `set-env.sh` (it upserts). This includes the six mandatory
  customer-sink endpoints/users — `GC_PROM_RW`, `GC_PROM_USER`, `GC_OTLP_ENDPOINT`, `GC_OTLP_USER`,
  `GC_LOKI`, `GC_LOKI_USER` — plus `DRY_RUN true`, `SYNTHKIT_BIND <choice>`, and the `*_ENABLED`
  flags for chosen lanes (e.g. `SELFOBS_ENABLED true` + `GC_SELF_OTLP_ENDPOINT`,
  `GC_SELF_OTLP_USER` for the staff stack). Profiling has no separate enable flag: it requires
  `SELFOBS_ENABLED=true`, `DRY_RUN=false`, and the `GC_PYROSCOPE_*` triplet.
- Generate the control token idempotently (value never printed; strips any prior line first):
  `set -o pipefail; openssl rand -hex 24 | bash "${CLAUDE_PLUGIN_ROOT}/skills/initial-setup/scripts/add-secret.sh" CONTROL_TOKEN "$SYNTHKIT_CHECKOUT/.env"`
- Collect the **secret** vars via the secure path. Confirm `.env` is gitignored:
  `git -C "$SYNTHKIT_CHECKOUT" check-ignore .env` → prints `.env`.

## Step 5 — Host prep (once per host)
`mkdir -p "$SYNTHKIT_CHECKOUT/control-state-data" && sudo chown -R 65532:65532 "$SYNTHKIT_CHECKOUT/control-state-data"`
(the container runs as uid 65532 and must own the persisted control-state volume).

## Step 6 — Dry-run gate (before any live push)
`DRY_RUN=true docker compose run --rm synthkit -once -dump`
(`pull_policy: always` pulls the latest image automatically; `run` honours `env_file: .env` and
appends `-once -dump` to the entrypoint). Confirm the config parses and the series inventory looks
right. `make dump` is an equivalent **only if Go is installed locally** — the docker form is the
no-toolchain path. Only then set `DRY_RUN false` via `set-env.sh`.

## Step 7 — Deploy
The image is pulled from `ghcr.io/rknightion/synthkit`. Set `SYNTHKIT_IMAGE_TAG` in `.env` to
control which tag is used: `latest` (default, last tagged release), `main` (bleeding-edge
default-branch build), or `vX.Y.Z` to pin a release. To build from local source instead, use
`docker compose -f docker-compose.yml -f docker-compose.build.yml up -d --build`.

- **Local:** `docker compose up -d`.
- **Remote (aware/handoff):** on the standing host, first repeat **Locate the synthkit checkout**
  against its existing clone. Then, from that verified root: `git pull --ff-only` →
  `mkdir -p "$SYNTHKIT_CHECKOUT/control-state-data" && sudo chown -R 65532:65532 "$SYNTHKIT_CHECKOUT/control-state-data"` →
  copy the host's `.env` across → `docker compose up -d`.
  Set `SYNTHKIT_BIND` deliberately (loopback + SSH tunnel rather than `0.0.0.0` on an
  untrusted network). Full SSH automation is out of scope for v1 — guide the user through these steps.

## Step 8 — Verify
**REQUIRED SUB-SKILL:** Use verify-deployment to confirm the control plane is healthy and data is
landing in the right stack(s). Do not hand-roll verification here.

## Common mistakes
- Pasting a secret into the chat (use the secure path).
- Reusing `GC_TOKEN` for the staff self-obs/profiling stacks (separate tokens).
- Using `GC_PROM_USER` as the FM user (FM uses `GC_FM_STACK_ID`).
- Transposing the customer and staff OTLP creds (`GC_OTLP_ENDPOINT`/`GC_OTLP_USER` = customer;
  `GC_SELF_OTLP_ENDPOINT`/`GC_SELF_OTLP_USER` = staff) — self-obs metrics then land in the wrong stack.
- Forgetting the `control-state-data` chown (container can't write its snapshot).
- Going live with `DRY_RUN=true` still set, or skipping the dry-run gate.
