---
name: initial-setup
description: "Deploy synthkit for first use: safely configure its .env and Docker Compose checkout; not generic Docker setup."
---

# synthkit initial setup

Guide an operator from a public checkout to a running synthkit deployment with Docker Compose,
then verify its declared signals in Grafana. Use the bundled skills and the verified checkout's
local docs; ordinary startup and browser readback require no specialised documentation or query
tool. Credential reference: [references/credentials.md](references/credentials.md).

## Locate the synthkit checkout

Plugin installation provides guidance and helpers only; it does not install synthkit or create a
checkout. If none exists, clone `https://github.com/rknightion/synthkit.git` into a new directory.
Before any repository command, establish and verify the checkout root:

```bash
SYNTHKIT_CHECKOUT="/absolute/path/to/synthkit"
SYNTHKIT_CHECKOUT="$(git -C "$SYNTHKIT_CHECKOUT" rev-parse --show-toplevel)" || exit 1
test -f "$SYNTHKIT_CHECKOUT/AGENTS.md" && \
  test -f "$SYNTHKIT_CHECKOUT/docker-compose.yml" && \
  test -f "$SYNTHKIT_CHECKOUT/.env.example" || exit 1
cd "$SYNTHKIT_CHECKOUT"
```

Use `$SYNTHKIT_CHECKOUT` for every repository file and command, including bundled helper scripts.
Do not require a harness-specific plugin-root variable. The first-run docs are
`$SYNTHKIT_CHECKOUT/docs/quickstart.md`, `$SYNTHKIT_CHECKOUT/docs/installation.md` and
`$SYNTHKIT_CHECKOUT/docs/credentials.md`; resolve these paths in the checkout rather than a plugin
cache. Retain the checkout revision and selected image identity: current source documentation does
not establish which features an older published image contains.

## Host tools for Docker-first startup

The Docker-first path needs Git 2.39+, Docker Engine 24.0+, Docker Compose 2.24.4+, a normal shell
and text editor, and a browser signed in to the intended Grafana Cloud stack. Confirm the daemon
is running and these commands succeed:

```bash
git --version
docker --version
docker compose version
docker info >/dev/null
```

The published image includes the binary and operator UI. Host Go, Node.js, `just`, `gcx` and
third-party documentation tools are not prerequisites for ordinary startup or browser readback.
Bash 5+ helpers and source-build tools are optional paths. Full immutable deployment identity
verification additionally requires Python 3.11+ and the existing checkout helper; without that
prerequisite, startup/browser observations can proceed but the deployment is not fully verified.
Keep that verification requirement separate from the tools needed to start Compose.

**Core rules**
- NEVER invent an env-var name. Use the bundled credential reference and the verified checkout's
  `.env.example` and configuration docs for supported keys.
- The customer/synthetic stack and the staff stack NEVER share a token.
- Default to the **secure** secret path (below). `.env` is gitignored — keep it that way.
- `DRY_RUN` stays `true` until the dry-run gate passes.
- Empty `BLUEPRINT_NAMES` is the safe setup-mode default and emits nothing. Never substitute `*`
  without the operator explicitly choosing the complete catalog.
- NEVER run a command that prints secret values into context: no `cat .env`, no rendered
  `docker compose config` output, and no `echo` of a secret. Parser-only `docker compose config --quiet`
  validates without displaying values. Inspect
  `.env` only with presence/shape checks like
  `grep -q '^KEY=.\+' "$SYNTHKIT_CHECKOUT/.env"`.

## Step 1: Preflight
Run (report failures, don't proceed past them):
- `docker --version && docker compose version` — both must exist.
- `test -f "$SYNTHKIT_CHECKOUT/.env" && echo "EXISTS — do NOT clobber; offer to review/extend" || echo "no .env yet"`.

## Step 2: Resolve scope before collecting credentials
Use the user's existing answers and authorisation. Ask only for missing decisions, using the
harness's available question tool; do not assume a Claude-specific tool. Establish:
1. For live delivery, which synthetic-data stack and are its seven ingest fields available?
   Offline dry-run or intentional setup mode needs no ingest credentials.
2. Also ship synthkit's own telemetry to a **staff** stack? → self-observability and/or profiling.
3. Optional lanes to enable now: RUM, Fleet metrics, Fleet API registration, Synthetic Monitoring,
   self-observability, process profiling, Sigil, private Git, and synthetic profiles.
4. Deploy target: this machine (local) now, or a remote host? (remote = handoff, see Step 7).
5. Network exposure: loopback `127.0.0.1` (default, safest) or `0.0.0.0`?
6. Initial blueprint selection: no selection (setup mode), one or more exact runtime names
   (`otlp-native` is the focused first-workload recommendation), or the complete catalog (`*`)?
Their answers select which credential groups Step 3 collects. For an ordinary first run, recommend
one exact `otlp-native` selection, loopback exposure, and no optional provisioning. If the user has
already requested live deployment to a named destination, carry out its necessary configuration,
dry-run, preflight and startup without asking again at each step. Separate optional provisioning,
remote exposure and new credential creation still require their actual scope decisions.

## Step 3: Collect credentials for the chosen lanes
For each selected lane, look up its exact vars + where to generate them in
[references/credentials.md](references/credentials.md). Tell the user the precise scopes for each token.

### Secret handling
After Step 4 creates the private `.env`, have the operator enter secrets directly in their local
editor or terminal without sending them through chat or agent command arguments. An export in
the operator's terminal need not be visible to a separate agent process. Use an already authorised
private credential source through the harness's supported secret-safe mechanism when available;
do not ask the operator to re-enter a credential that is already supplied safely.

- **Optional hidden-prompt helper (Bash 5+):** the operator can run:
  `bash "$SYNTHKIT_CHECKOUT/plugins/synthkit/skills/initial-setup/scripts/add-secret.sh" GC_TOKEN "$SYNTHKIT_CHECKOUT/.env"`
  The helper atomically replaces every existing `GC_TOKEN` entry, publishes a mode-0600 file, and
  never prints the value. Substitute another secret key when adding that key.
- When an agent is authorised to use that helper, invoke it from the verified checkout; the secret
  must arrive through its input stream, never an argument or chat:
  `bash "$SYNTHKIT_CHECKOUT/plugins/synthkit/skills/initial-setup/scripts/add-secret.sh" GC_TOKEN "$SYNTHKIT_CHECKOUT/.env"`.
  Verify **presence only** with
  `grep -q '^GC_TOKEN=.\+' "$SYNTHKIT_CHECKOUT/.env" && echo ok`.

**The secret vars (always secure path, never `set-env.sh`):** `GC_TOKEN`, `GC_FARO_COLLECTOR`,
`GC_FARO_APP_KEY`,
`GC_SELF_OTLP_PASSWORD`, `GC_PYROSCOPE_PASSWORD`, `GC_FM_TOKEN`, `GC_SM_TOKEN`, `GC_SIGIL_TOKEN`,
`GIT_TOKEN`, and `CONTROL_TOKEN`. A custom blueprint source's operator-defined `token_env_var` is
also a secret private-git token; do not add its value to a blueprint or print it.
Other values are non-secret configuration. Edit them in `.env`, or use the optional Bash helper
`bash "$SYNTHKIT_CHECKOUT/plugins/synthkit/skills/initial-setup/scripts/set-env.sh" KEY VALUE "$SYNTHKIT_CHECKOUT/.env"`.
It replaces existing entries rather than adding duplicates. Put `.env` comments on separate lines;
do not append comments to credential or endpoint values.

## Step 4: Assemble the private .env
- Reject a symlink or non-file `.env` before reading or writing it. If absent:
  `install -m 600 "$SYNTHKIT_CHECKOUT/.env.example" "$SYNTHKIT_CHECKOUT/.env"`.
  If it already exists, tighten it before inspection or mutation:
  `chmod 600 "$SYNTHKIT_CHECKOUT/.env"`.
- Set the **non-secret** config in the editor or with the optional `set-env.sh`. This includes the
  six mandatory
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
- Loopback-only deployment may leave `CONTROL_TOKEN` empty. If a control token was selected, enter
  a strong random value through the same private secret path and preserve it on repeat runs. The
  optional Bash 5+/OpenSSL recipe pipes a generated value into the atomic helper without printing
  it; the subshell's `pipefail` reports a failed generator as a failed command:
  `(set -o pipefail; openssl rand -hex 24 | bash "$SYNTHKIT_CHECKOUT/plugins/synthkit/skills/initial-setup/scripts/add-secret.sh" CONTROL_TOKEN "$SYNTHKIT_CHECKOUT/.env")`
  Use this only when a new token or rotation was selected, require exit zero, and retain an
  already configured token on ordinary repeat setup.
- Collect the **secret** vars via the secure path. Confirm `.env` is gitignored:
  `git -C "$SYNTHKIT_CHECKOUT" check-ignore .env` → prints `.env`.

## Step 5: Prepare persistent state once per host

Reject path surprises, then create or tighten only the dedicated directory through the pinned
helper container:

```bash
state_dir="$SYNTHKIT_CHECKOUT/control-state-data"
if [ -L "$state_dir" ] || { [ -e "$state_dir" ] && [ ! -d "$state_dir" ]; }; then
  echo "refusing non-directory or symlink state path: $state_dir" >&2
  exit 1
fi
mkdir -p "$state_dir"
chmod 700 "$state_dir"
docker run --rm --volume "$state_dir:/data" --entrypoint /bin/sh \
  node:24-alpine@sha256:d32cdf619f63fe0471182d08996dd516c6275bb5fd31ae06e55a570bd9e1ad43 \
  -ceu 'chown 65532:65532 /data; chmod 0700 /data'
```

The container runs as uid 65532 and must own the persisted control-state volume. The pinned helper
performs ownership setup through Docker, so this works without `sudo` on a socket-mounted clean
container and a normal Docker host. Do not use a recursive ownership change here; runtime manages
the files beneath this dedicated directory.

## Step 6: Dry-run before any live push
Use the committed Compose file directly. Contributor validation includes `just compose-check`;
it is not a prerequisite for an operator with Docker only. It renders fake `.env.example` inputs;
never substitute real credentials or run raw `docker compose config` against them.

`docker compose run --rm --no-deps -e DRY_RUN=true -e SELFOBS_ENABLED=false synthkit -env /dev/null -once -dump`
(`pull_policy: always` pulls the selected image automatically; the emitter uses `.env` by default or
the service env file selected by `SYNTHKIT_ENV_FILE`, and appends `-once -dump` to the entrypoint).
When an override is configured, retain the same `SYNTHKIT_ENV_FILE` value for this command and every
later Compose command. Confirm the config parses and the series inventory looks
right. `just dump` is an equivalent **only if Go is installed locally** — the docker form is the
no-toolchain path. With exact names, require a non-empty selected inventory. With intentional setup
mode, require the actionable `no blueprints selected` warning and an empty inventory; do not claim
signal verification. Keep `DRY_RUN=true` until both this inventory check and the connection preflight
below succeed. Then set `DRY_RUN=false` in the private `.env` when live delivery was
requested, using the editor or optional helper. Leave `SELFOBS_ENABLED=false` for the ordinary
synthetic-data first run; process telemetry requires its separately selected destination and
credentials.

Also run `docker compose run --rm --no-deps synthkit -preflight` and inspect every `optional <lane>`
line supported by the selected image. For a bounded one-shot job on an image verified to support
it, pass `-one-shot-deadline <RFC3339-expiry>` to exactly one of `-validate` or
`-preflight`, and require a successful exit before proceeding. Expired entry is refused before
configuration loading; live probes inherit the absolute context budget. Output printed before a
later failure is not a successful check. This does not roll back receiver-side processing.
Resolve requested `partial` dispositions before deployment; `unsupported` is a hard stop. Disabled
Fleet registration is valid when the operator chose metrics-only mode.

## Step 7: Deploy the selected image
The image is pulled from `ghcr.io/rknightion/synthkit`. Keep the eligible published
`SYNTHKIT_IMAGE_REF` copied from `.env.example`, or replace it only with a release that has already
passed `scripts/synthkit-deploy.py verify-image`. Prefer the exact index digest.
Take its independent binary version, OCI label and source inputs from the publication record in
`$SYNTHKIT_CHECKOUT/docs/deployment.md`; stable and prerelease label spellings differ.
That verification trusts the reusable
`rknightion/.github/.github/workflows/container-publish.yml` path across signer revisions, making
Rob's continued control of that workflow repository part of the trust boundary. It still enforces
the GitHub Actions OIDC issuer, synthkit source repository/digest/ref, OCI version/revision labels,
selected platform, and binary identity.
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

The running checkout and the selected image can be different revisions. A failed main publication
does not make newer source features available in the pinned image. In particular, do not promise
the source checkout's new dashboard commands or startup-only probe in an older published binary.
Read `$SYNTHKIT_CHECKOUT/docs/deployment.md` for image capabilities and identity limits.

When Synthetic Monitoring was selected, the first start writes a private snapshot and intentionally
suppresses SM. Preview, explicitly apply, then restart the same image:

```bash
docker compose --profile sm-provision run --rm sm-provision
SM_PROVISION_APPLY=true docker compose --profile sm-provision run --rm sm-provision
docker compose restart synthkit
```

The first v2 activation-format upgrade requires explicit apply/restart, preserving all v1 ownership,
preview, pending and lock evidence. Later release-only bumps retain activation when specs, target and
schema are unchanged; the tool must still match the current snapshot version. The in-cluster command and HA chart support dedicated named SM state and separate provisioning
Lease authority without shared disk. Follow `docs/kubernetes.md` and `docs/synthetic-monitoring.md`
for the required matching image and named-state configuration. The default file-mode Job still
uses shared storage. There is no automatic file-state migration.

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

## Step 8: Verify startup and delivery separately
**REQUIRED SUB-SKILL:** Read
`$SYNTHKIT_CHECKOUT/plugins/synthkit/skills/verify-deployment/SKILL.md` to verify the immutable
running identity, local control plane and actual backend readback. Its browser-first route needs
no Grafana query plugin. If the Python 3.11+ identity prerequisite is unavailable, report basic
startup/readback observations separately and full verification as blocked; do not replace the
required helper proof with weaker Docker observations or call setup fully verified.

## Common mistakes
- Pasting a secret into the chat (use the secure path).
- Reusing `GC_TOKEN` for the staff self-obs/profiling stacks (separate tokens).
- Using `GC_PROM_USER` as the FM user (FM uses `GC_FM_STACK_ID`).
- Transposing the customer and staff OTLP creds (`GC_OTLP_ENDPOINT`/`GC_OTLP_USER` = customer;
  `GC_SELF_OTLP_ENDPOINT`/`GC_SELF_OTLP_USER` = staff) — self-obs metrics then land in the wrong stack.
- Skipping the `control-state-data` helper step (container can't write its snapshot).
- Leaving `BLUEPRINT_NAMES` empty while expecting telemetry (empty is intentional setup mode).
- Setting `BLUEPRINT_NAMES=*` without explicitly choosing the complete catalog.
- Going live with `DRY_RUN=true` still set, or skipping the dry-run gate.
- Expecting SM credentials alone to activate emission, or provisioning without restarting synthkit.
- Requiring a host Go toolchain for SM instead of using the shipped Compose profile.
