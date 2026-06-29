---
name: initial-setup
description: Use when deploying synthkit for the first time, setting up its .env or docker-compose, configuring Grafana Cloud credentials, or onboarding a new synthkit host. Triggers include "deploy synthkit", "set up synthkit", "first run", "configure credentials", "get synthkit running".
---

# synthkit initial setup

Guide a Grafana staff user from a fresh checkout to a running, verified synthkit deployment.
Ask the right questions, collect every required credential **safely**, write `.env`, deploy with
docker-compose, and validate. Full variable reference: [references/credentials.md](references/credentials.md).

**Core rules**
- NEVER invent an env-var name. Use only the vars in `references/credentials.md` (they are gate-enforced).
- The customer/synthetic stack and the staff stack NEVER share a token.
- Default to the **secure** secret path (below). `.env` is gitignored — keep it that way.
- `DRY_RUN` stays `true` until the dry-run gate passes.
- NEVER run a command that prints secret values into context: no `cat .env`, no
  `docker compose config` (it interpolates and echoes env values), no `echo "$GC_TOKEN"`. Inspect
  `.env` only with presence/shape checks like `grep -q '^KEY=.\+' .env`.

## Step 1 — Preflight
Run (report failures, don't proceed past them):
- `docker --version && docker compose version` — both must exist.
- `git rev-parse --show-toplevel` — confirm we're in a synthkit checkout; `ls docker-compose.yml .env.example`.
- `test -f .env && echo "EXISTS — do NOT clobber; offer to review/extend" || echo "no .env yet"`.

## Step 2 — Scope questions (ask before collecting creds)
Ask the user (use AskUserQuestion):
1. Customer/synthetic-data stack details ready? (mandatory)
2. Also ship synthkit's own telemetry to a **staff** stack? → self-observability and/or profiling.
3. Optional lanes to enable now: Fleet Management, Synthetic Monitoring, RUM (Faro), PDC.
4. Deploy target: this machine (local) now, or a remote host? (remote = handoff, see Step 7).
5. Network exposure: loopback `127.0.0.1` (default, safest) or `0.0.0.0`?
Their answers select which credential groups Step 3 collects.

## Step 3 — Collect credentials (per chosen lane)
For each selected lane, look up its exact vars + where to generate them in
[references/credentials.md](references/credentials.md). Tell the user the precise scopes for each token.

### Secret handling — two paths (default = secure)
**Why it matters:** the agent's Bash tool runs in a *separate, non-interactive* shell. A secret you
`export` in your own terminal is invisible to the agent; and any command the agent writes that
contains the secret value puts that value in model context. The only way that is BOTH out-of-context
AND readable by docker-compose is **you writing the secret into `.env` from your own shell**.

- **Secure (default):** for each secret var, tell the user to run, in THEIR terminal:
  `bash plugins/synthkit/skills/initial-setup/scripts/add-secret.sh GC_TOKEN`
  (or the inline form: `read -rsp "GC_TOKEN: " V && printf 'GC_TOKEN=%s\n' "$V" >> .env && unset V; echo`).
  Then the agent verifies **presence only**: `grep -q '^GC_TOKEN=.\+' .env && echo ok`. Never print the value.
- **Easy (opt-in):** the user gives the agent the value and the agent writes it with
  `scripts/set-env.sh`. Warn explicitly: "this value will enter the model context."

**The secret vars (always secure path, never `set-env.sh`):** `GC_TOKEN`, `GC_SELF_OTLP_PASSWORD`,
`GC_PYROSCOPE_PASSWORD`, `GC_FM_TOKEN`, `GC_SM_TOKEN`, `CONTROL_TOKEN`.
Everything else is non-secret config, written by the agent with
`bash plugins/synthkit/skills/initial-setup/scripts/set-env.sh KEY VALUE` (it *upserts* — replaces any
existing line, so re-runs don't duplicate).

## Step 4 — Assemble .env
- If absent: `cp .env.example .env`.
- Write the **non-secret** config with `set-env.sh` (it upserts). This includes the six mandatory
  customer-sink endpoints/users — `GC_PROM_RW`, `GC_PROM_USER`, `GC_OTLP_ENDPOINT`, `GC_OTLP_USER`,
  `GC_LOKI`, `GC_LOKI_USER` — plus `DRY_RUN true`, `SYNTHKIT_BIND <choice>`, and the `*_ENABLED`
  flags for chosen lanes (e.g. `SELFOBS_ENABLED true` + `GC_SELF_OTLP_ENDPOINT`,
  `GC_SELF_OTLP_USER` for the staff stack).
- Generate the control token idempotently (value never printed; strips any prior line first):
  `grep -v '^CONTROL_TOKEN=' .env > .env.tmp 2>/dev/null; mv -f .env.tmp .env; printf 'CONTROL_TOKEN=%s\n' "$(openssl rand -hex 24)" >> .env`
- Collect the **secret** vars via the secure path. Confirm `.env` is gitignored: `git check-ignore .env` → prints `.env`.

## Step 5 — Host prep (once per host)
`mkdir -p control-state-data && sudo chown -R 65532:65532 control-state-data`
(the container runs as uid 65532 and must own the persisted control-state volume).

## Step 6 — Dry-run gate (before any live push)
`docker compose build synthkit && DRY_RUN=true docker compose run --rm synthkit -once -dump`
(the explicit `build` avoids validating a stale image; `run` honours `env_file: .env` and appends
`-once -dump` to the entrypoint). Confirm the config parses and the series inventory looks right.
`make dump` is an equivalent **only if Go is installed locally** — the docker form is the no-toolchain
path. Only then set `DRY_RUN false` via `set-env.sh`.

## Step 7 — Deploy
- **Local:** `docker compose up -d --build`.
  (Once `ghcr.io/rknightion/synthkit` is published this becomes an `image:` pull — no build, no Go
  toolchain. It is NOT published yet, so `--build` is required today.)
- **Remote (aware/handoff):** synthkit deploys to a standing host via:
  `ssh <host>` → `cd <repo> && git pull --ff-only` →
  `mkdir -p control-state-data && sudo chown -R 65532:65532 control-state-data` →
  scp the host's `.env` across → `docker compose up -d --build`.
  Set `SYNTHKIT_BIND` deliberately (loopback + SSH tunnel, or PDC, rather than `0.0.0.0` on an
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
