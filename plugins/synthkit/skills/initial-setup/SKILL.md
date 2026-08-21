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
- Empty `BLUEPRINT_NAMES` is the safe setup-mode default and emits nothing. Never substitute `*`
  without the operator explicitly choosing the complete catalog.
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
3. Optional lanes to enable now: RUM, Fleet metrics, Fleet API registration, Synthetic Monitoring,
   self-observability, process profiling, Sigil, private Git, and synthetic profiles.
4. Deploy target: this machine (local) now, or a remote host? (remote = handoff, see Step 7).
5. Network exposure: loopback `127.0.0.1` (default, safest) or `0.0.0.0`?
6. Initial blueprint selection: no selection (setup mode), one or more exact runtime names
   (`otlp-native` is the focused first-workload recommendation), or the complete catalog (`*`)?
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
- If absent: `install -m 600 "$SYNTHKIT_CHECKOUT/.env.example" "$SYNTHKIT_CHECKOUT/.env"`.
  If it already exists, tighten it before inspection or mutation:
  `chmod 600 "$SYNTHKIT_CHECKOUT/.env"`.
- Write the **non-secret** config with `set-env.sh` (it upserts). This includes the six mandatory
  customer-sink endpoints/users — `GC_PROM_RW`, `GC_PROM_USER`, `GC_OTLP_ENDPOINT`, `GC_OTLP_USER`,
  `GC_LOKI`, `GC_LOKI_USER` — plus `DRY_RUN true`, `SYNTHKIT_BIND <choice>`, and the `*_ENABLED`
  flags for chosen lanes (e.g. `SELFOBS_ENABLED true` + `GC_SELF_OTLP_ENDPOINT`,
  `GC_SELF_OTLP_USER` for the staff stack). Profiling has no separate enable flag: it requires
  `SELFOBS_ENABLED=true` and the `GC_PYROSCOPE_*` triplet, independently of synthetic `DRY_RUN`.
- Also write the operator's exact `BLUEPRINT_NAMES` decision: for example `otlp-native`; leave it
  empty only for intentional setup mode; use `*` only after explicit complete-catalog confirmation.
- For non-loopback `SYNTHKIT_BIND`, also set `CONTROL_EXPOSURE_ACK` to exactly `trusted-network`
  for an isolated plaintext path or `tls-proxy` for a trusted HTTPS proxy. Startup fails closed
  unless both that acknowledgement and `CONTROL_TOKEN` are present.
- Generate the control token idempotently (value never printed; strips any prior line first):
  `set -o pipefail; openssl rand -hex 24 | bash "${CLAUDE_PLUGIN_ROOT}/skills/initial-setup/scripts/add-secret.sh" CONTROL_TOKEN "$SYNTHKIT_CHECKOUT/.env"`
- Collect the **secret** vars via the secure path. Confirm `.env` is gitignored:
  `git -C "$SYNTHKIT_CHECKOUT" check-ignore .env` → prints `.env`.

## Step 5 — Host prep (once per host)

Reject path surprises before invoking `sudo`, then create or tighten only the dedicated directory:

```bash
state_dir="$SYNTHKIT_CHECKOUT/control-state-data"
if [ -L "$state_dir" ] || { [ -e "$state_dir" ] && [ ! -d "$state_dir" ]; }; then
  echo "refusing non-directory or symlink state path: $state_dir" >&2
  exit 1
fi
sudo install -d -o 65532 -g 65532 -m 700 "$state_dir"
```

The container runs as uid 65532 and must own the persisted control-state volume. Do not use a
recursive ownership change here; runtime manages the files beneath this dedicated directory.

## Step 6 — Dry-run gate (before any live push)
First run `make compose-check`. It requires Compose 2.24.4 or later and renders the default and
`sm-provision` profile with `.env.example` fake inputs; never substitute the real credential file or
run raw `docker compose config` against it.

`DRY_RUN=true docker compose run --rm synthkit -once -dump`
(`pull_policy: always` pulls the selected image automatically; the emitter uses `.env` by default or
the service env file selected by `SYNTHKIT_ENV_FILE`, and appends `-once -dump` to the entrypoint).
When an override is configured, retain the same `SYNTHKIT_ENV_FILE` value for this command and every
later Compose command. Confirm the config parses and the series inventory looks
right. `make dump` is an equivalent **only if Go is installed locally** — the docker form is the
no-toolchain path. With exact names, require a non-empty selected inventory. With intentional setup
mode, require the actionable `no blueprints selected` warning and an empty inventory; do not claim
signal verification. Only then set `DRY_RUN false` via `set-env.sh` when live delivery was requested.

Also run `docker compose run --rm synthkit -preflight` and inspect every `optional <lane>` line.
Resolve requested `partial` dispositions before deployment; `unsupported` is a hard stop. Disabled
Fleet registration is valid when the operator chose metrics-only mode.

## Step 7 — Deploy
The image is pulled from `ghcr.io/rknightion/synthkit`. Keep the eligible published
`SYNTHKIT_IMAGE_REF` copied from `.env.example`, or replace it only with a release that has already
passed `scripts/synthkit-deploy.py verify-image`. Prefer the exact index digest.
`SYNTHKIT_IMAGE_TAG` is a legacy bare-tag fallback used only when the preferred selector is empty;
a malformed or unavailable preferred value never falls back. `main` and `latest` are mutable edge
tags and require deliberate `set-image --allow-mutable` plus an explicit pull. To build from local
source instead, use
`docker compose -f docker-compose.yml -f docker-compose.build.yml up -d --build`.

- **Local:** `docker compose up -d --wait`.
- **Remote (aware/handoff):** on the standing host, first repeat **Locate the synthkit checkout**
  against its existing clone. A first deployment repeats Step 5 and uses `docker compose up -d
  --wait`. An existing deployment must follow `docs/deployment.md`'s reproducible lifecycle:
  verify the candidate first; inspect and record the current identity; stop and snapshot `/data`
  into an external private directory; compare-and-swap only `SYNTHKIT_IMAGE_REF`; recreate with
  `--wait`; then retain and verify the rollback target. Never copy or replace the whole host `.env`
  during an upgrade. Set `SYNTHKIT_BIND` deliberately. Loopback + SSH tunnel needs no
  acknowledgement; any non-loopback bind needs `CONTROL_TOKEN` plus
  `CONTROL_EXPOSURE_ACK=trusted-network|tls-proxy`.

When Synthetic Monitoring was selected, the first start writes a private snapshot and intentionally
suppresses SM. Preview, explicitly apply, then restart the same image:

```bash
docker compose --profile sm-provision run --rm sm-provision
SM_PROVISION_APPLY=true docker compose --profile sm-provision run --rm sm-provision
docker compose restart synthkit
```

`DRY_RUN=false` does not authorise the provisioner. Do not enable legacy adoption unless preview
shows exactly one complete match and the operator explicitly confirms ownership.

If an existing deployment rotates `GC_SM_TOKEN` or `GC_SM_URL`, the target fingerprint changes.
Recreate synthkit to write the new suppressed snapshot, preview with
`SM_PROVISION_MIGRATE_TARGET=true`, then within 15 minutes apply with both that flag and
`SM_PROVISION_APPLY=true`. Do not combine target migration with legacy adoption. The migration must
fail if any recorded remote resource no longer matches the ledger's authoritative evidence. If an
interrupted apply reports an incomplete migration, retain the marker and rerun the same migration-
plus-apply command; do not bypass it with a normal reconciliation. Target migration accepts only an
unchanged, fully registered resource set and makes no remote API writes; reconcile configuration
changes in a separate normal apply.

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
- Leaving `BLUEPRINT_NAMES` empty while expecting telemetry (empty is intentional setup mode).
- Setting `BLUEPRINT_NAMES=*` without explicitly choosing the complete catalog.
- Going live with `DRY_RUN=true` still set, or skipping the dry-run gate.
- Expecting SM credentials alone to activate emission, or provisioning without restarting synthkit.
- Requiring a host Go toolchain for SM instead of using the shipped Compose profile.
