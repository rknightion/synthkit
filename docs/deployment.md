---
title: Deployment
description: How to deploy synthkit with Docker Compose or the Helm chart, including volume setup, networking, and the published container image.
---

# Deployment

There are two supported deployments, both running the same published image with the same
environment surface:

- **Docker Compose** on a persistent host — the canonical deployment, and the subject of this page.
  The committed `docker-compose.yml` is secret-free; all credentials and configuration come from a
  gitignored `.env` file you provision on the host.
- **Kubernetes**, via the Helm chart in `charts/synthkit/` — see [Kubernetes](kubernetes.md). The
  chart supplies credentials from existing Secrets grouped by destination stack, keeps the control
  plane closed by default, and holds `/data` on a PersistentVolumeClaim.

Everything on this page about the `/data` contract, queue memory, image verification and counter
resets applies to both. The two pages differ only in how values are supplied and what is reachable
from outside the process.

---

## Quick deploy

=== "Docker Compose (canonical)"

    ```bash
    # 1. Clone the repo
    git clone https://github.com/rknightion/synthkit.git
    cd synthkit

    # 2. Create the state bind-mount directory, owned by the container's uid
    #    (distroless nonroot = uid 65532). This is a one-time step per host.
    if [ -L control-state-data ] || { [ -e control-state-data ] && [ ! -d control-state-data ]; }; then echo 'refusing unsafe state path' >&2; exit 1; fi
    sudo install -d -o 65532 -g 65532 -m 700 control-state-data

    # 3. Configure credentials
    install -m 600 .env.example .env
    # Edit .env: set BLUEPRINT_NAMES=otlp-native (or other exact names), set
    # DRY_RUN=false, and fill GC_TOKEN, GC_PROM_RW/USER, GC_OTLP_ENDPOINT/USER,
    # GC_LOKI/USER at minimum. Empty BLUEPRINT_NAMES emits nothing.

    # 4. Validate the committed pin and start it, waiting for delivery readiness
    just compose-check
    docker compose up -d --wait

    # 5. Verify
    open http://127.0.0.1:8088/control/ui
    curl -s -u control http://127.0.0.1:8088/control/status | jq
    ```

=== "Kubernetes (Helm)"

    ```bash
    git clone https://github.com/rknightion/synthkit.git
    cd synthkit

    # Dry run first — renders every estate, pushes nothing, needs no credentials.
    helm install synthkit ./charts/synthkit \
      --namespace synthkit --create-namespace \
      --set config.blueprintNames=otlp-native

    kubectl -n synthkit rollout status deploy/synthkit

    # The control plane is closed by default; reach it without exposing anything.
    kubectl -n synthkit port-forward deploy/synthkit 8088:8088
    open http://127.0.0.1:8088/control/ui
    ```

    Credentials, the exposure acknowledgement, the state claim and resource sizing are all
    covered in [Kubernetes](kubernetes.md).

=== "Local binary"

    ```bash
    go build ./cmd/synthkit

    # Dry run (offline, no push):
    DRY_RUN=true BLUEPRINT_NAMES=otlp-native ./synthkit -once -dump

    # Live run:
    install -m 600 .env.example .env   # fill credentials
    ./synthkit
    ```

---

## The `/data` volume contract

!!! warning "Must be a DIRECTORY — not a single-file bind mount"
    The control plane saves state atomically (write → rename). A single-file bind mount breaks the rename step and silently wipes state on every tick. Mount a **directory** and let synthkit manage the files inside it.

The Helm chart mounts the same `/data` on a PersistentVolumeClaim and sets `fsGroup: 65532` so
the same ownership requirement is met without a manual `install -d`; see [Kubernetes](kubernetes.md).

The `/data` directory holds:

- `control-state.json` — live control-plane state (volume multiplier, active scenarios, scaling overrides). Written lazily on the first mutation; absent at startup is normal.
- `blueprints/` — staged custom and git-sourced blueprints (subdirectories `custom/`, `git/<id>/`, `.boot-manifest.json`).
- `runtime/` — owner-only Synthetic Monitoring snapshot, activation registration, durable ownership
  ledger, adoption-preview marker, lock, and pending journal used by the version-matched one-shot
  provisioner.

The container image runs as **uid 65532** (distroless nonroot). The bind-mount directory must be owned by this uid or state saves fail:

```bash
if [ -L control-state-data ] || { [ -e control-state-data ] && [ ! -d control-state-data ]; }; then echo 'refusing unsafe state path' >&2; exit 1; fi
sudo install -d -o 65532 -g 65532 -m 700 control-state-data
```

If a control-plane change made in the operator UI doesn't survive a restart, check `persist.last_error` in `/control/status` — a `permission denied` there confirms the ownership problem.

Resetting state is destructive maintenance, not an upgrade or rollback technique. Stop synthkit and
take a retained integrity snapshot before removing anything. Resetting only control-plane choices
removes `control-state-data/control-state.json`; a full `/data` reset also removes
`control-state-data/runtime/`, including Synthetic Monitoring ownership, registration,
adoption-preview, and crash-recovery state. Existing remote resources then become foreign until
explicitly previewed and adopted. Never remove runtime state from a running container.

---

## Networking and exposure

By default `SYNTHKIT_BIND=127.0.0.1` — the control plane is loopback-only and may remain token-free.

!!! danger "Non-loopback exposure requires an explicit policy"
    Startup fails unless a non-loopback deployment has `CONTROL_TOKEN` and
    `CONTROL_EXPOSURE_ACK=trusted-network` or `tls-proxy`. Basic auth protects sensitive reads and
    mutations but does not encrypt them. Never send it over untrusted plaintext HTTP.

| Scenario | What to do |
|---|---|
| Grafana Cloud Infinity datasource on a **different host** | Set a non-loopback `SYNTHKIT_BIND`, `CONTROL_TOKEN`, and the appropriate `CONTROL_EXPOSURE_ACK`; configure the datasource with Basic auth. |
| Grafana Cloud reaching it **privately** | Use a PDC Tailscale connection — Grafana Cloud reaches the Tailscale IP directly; no public exposure needed. |
| Browser-trusted HTTPS endpoint | Run `tailscale serve https:443 / http://127.0.0.1:8088` alongside synthkit. |
| Secure remote access | SSH-tunnel: `ssh -L 8088:localhost:8088 <host>` and access `http://localhost:8088/control/ui` locally. |

The compose file uses the same interpolated `${SYNTHKIT_BIND:-127.0.0.1}` for port publication and
inside the container's exposure check. The binary itself binds `0.0.0.0:8088` inside the container.

On Kubernetes the equivalent gate is `controlPlane.exposure.ack`, which takes the same two exact
values and additionally requires a control-token Secret before any Service is rendered. The closed
default binds loopback inside the pod and is reached with `kubectl port-forward`, which enters the
pod's network namespace. See [Kubernetes](kubernetes.md#the-control-plane-is-closed-by-default).

---

## Docker-only Synthetic Monitoring provisioning

`docker compose up -d` never provisions Synthetic Monitoring. With an SM blueprint selected and
`GC_SM_URL`/`GC_SM_TOKEN` configured, the emitter first writes the exact private snapshot and keeps
SM emission suppressed. Preview and apply the opt-in one-shot job from the same image:

    docker compose --profile sm-provision run --rm sm-provision
    SM_PROVISION_APPLY=true docker compose --profile sm-provision run --rm sm-provision
    docker compose restart synthkit

The job receives only `GC_SM_URL`, `GC_SM_TOKEN`, its three explicit control flags, and `/data`; it
does not inherit the emitter's other credentials. It has no port, restart policy, host Go toolchain,
or long-running role. The restart validates the matching registration and activates the lane. If a
collision, stale binding, or pending/ambiguous journal is reported, leave the emitter suppressed
and inspect ownership before retrying. The provisioner never deletes remote resources.

Credential or endpoint rotation requires a new emitter snapshot plus an explicit
`SM_PROVISION_MIGRATE_TARGET=true` preview. Apply within 15 minutes with both the migration and apply
flags; the preview-bound migration revalidates every owned resource before atomically rebinding the
ledger and makes no remote API writes. Reconcile resource/configuration changes separately. An
interrupted post-rebind migration retains its marker and requires the same migration-plus-
apply command to resume. See [Synthetic Monitoring](synthetic-monitoring.md) for the exact sequence.

---

## Capacity, queue memory, and container limits

Each configured sink has its own in-memory queue. `SEND_QUEUE_CAPACITY` is the maximum queued item
count **per sink**, divided across `SEND_SHARDS`; it is not a byte limit. Items differ substantially:
a metric series, Loki stream, OTLP resource, Faro beacon, profile, and Sigil export retain different
maps, slices, strings, and encoded payloads. Do not estimate memory as capacity times one universal
struct size.

Use a controlled fake-sink pressure test for the selected blueprint set and calculate:

```text
retained bytes/item for sink ~= (heap_at_depth - steady_heap) / (depth_at_sample - steady_depth)
queue memory budget          ~= sum(capacity_per_sink * measured bytes/item for that sink)
container memory             >= steady_heap + queue budget + encoding/retry headroom
```

`/control/health` exposes `process.heap_bytes`; `/control/status` exposes each queue's `depth`,
`blocked_enqueues`, cumulative `dropped_items`, and current/recovered loss state. Sample after several
ticks at steady state, then during an isolated fake-sink stall. Never stall a real Grafana endpoint
to size the queue.

On 2026-08-21 the single-blueprint standing deployment measured about 300.5 MiB resident
memory and 0.53% of one CPU in a one-shot `docker stats` sample. That is a baseline, not a full-queue
capacity guarantee. A practical initial allocation for a small explicit blueprint set is 1 CPU and
1 GiB, with at least 2x the measured steady heap free; increase memory or reduce
`SEND_QUEUE_CAPACITY` when the pressure calculation exceeds that headroom. For a larger catalog,
measure first rather than copying the small-deployment limit.

Example operator override:

```yaml
services:
  synthkit:
    cpus: "1.0"
    mem_limit: 1g
    logging:
      driver: json-file
      options:
        max-size: "10m"
        max-file: "3"
```

Backpressure is intentional: when one shard buffer fills, the producer tick blocks instead of
growing memory without bound. A slow or unavailable sink can therefore lengthen a cycle and cause
later scheduled ticks to be dropped. OTLP has the longest retry window (up to five minutes), so an
OTLP outage can hold a shard and apply backpressure much longer than the other HTTP sinks. After
retry exhaustion, failed sub-batches are discarded and counted; there is no disk replay. Keep Docker
log rotation bounded because repeated outage warnings otherwise compete with queue memory and state
on the host filesystem.

---

## Container image

The published multi-arch image (amd64 + arm64) is at:

```text
ghcr.io/rknightion/synthkit:<X.Y.Z>
ghcr.io/rknightion/synthkit:latest
ghcr.io/rknightion/synthkit:main
```

Built by CI on each push to `main` and each tagged release. Release Git tags have the form
`vX.Y.Z`; GHCR strips the leading `v` from the image tag. The image is signed with cosign and ships
with SBOM and provenance attestations.

`SYNTHKIT_IMAGE_REF` is the preferred Compose selector and may contain a release tag or complete
index-digest reference. The committed `.env.example` selects a published, healthcheck-capable image;
copying it preserves that known-good pin. `SYNTHKIT_IMAGE_TAG` is legacy compatibility for a bare
tag and is consulted only when `SYNTHKIT_IMAGE_REF` is absent or empty. A malformed or unavailable
preferred reference fails: Compose and the deployment helper never fall back silently.

| Selector | Image | Notes |
|---|---|---|
| `SYNTHKIT_IMAGE_REF=ghcr.io/rknightion/synthkit@sha256:<index>` | immutable release index | Preferred for standing deployments. |
| `SYNTHKIT_IMAGE_REF=ghcr.io/rknightion/synthkit:X.Y.Z` | released version tag | Reproducible while the registry tag remains intact; record its resolved digest. |
| `SYNTHKIT_IMAGE_REF=...:main` or `...:latest` | mutable edge | Deliberate testing only. Pass `--allow-mutable` to `set-image` and pull explicitly before recreate. |
| `SYNTHKIT_IMAGE_TAG=X.Y.Z` | legacy fallback | Bare tag only; ignored when the preferred selector is non-empty. |

An image has several related identities. The **registry index digest** names the multi-platform
artifact; the **platform manifest digest** selects one OS/architecture from that index; the **OCI
config digest** identifies the selected image configuration; and Docker's **running image ID** is a
distinct runtime observation. An accepted synthkit Compose deployment requires the OCI config
digest and the running image ID to be byte-equal. Neither is a substitute for the binary's reported
release version and complete source revision.

Verify a release before changing a deployment:

```bash
python3 scripts/synthkit-deploy.py verify-image \
  --reference ghcr.io/rknightion/synthkit@sha256:<index> \
  --expected-version X.Y.Z \
  --expected-oci-version vX.Y.Z \
  --expected-revision <40-hex-source-sha> \
  --source-ref refs/tags/vX.Y.Z \
  --platform linux/amd64
```

This checks the exact index and selected manifest/config, `synthkit -version`, the keyless cosign
signature, and GitHub provenance bound to the repository, tag ref, source SHA, reusable signer
workflow, and pinned signer revision. Its output contains closed statuses and non-secret identities.

**Building from source (opt-in).** If you need to test local changes, override the compose file:

```bash
docker compose -f docker-compose.yml -f docker-compose.build.yml up -d --build
```

The `VERSION` build-arg is stamped as `service.version` in self-observability and profiling data;
`REVISION` supplies the complete source commit reported by `synthkit -version`. Published images have
both stamped by CI.

---

## Reproducible upgrade

!!! warning "Selection default changed to emit nothing"
    Empty or unset `BLUEPRINT_NAMES` now starts setup mode. Before upgrading an existing deployment
    that relied on the former implicit full catalog, set `BLUEPRINT_NAMES=*` to preserve that
    behavior, or preferably list the exact blueprint identities you intend to emit.

Use Docker Compose 2.24.4 or later. Never run raw `docker compose config` against the real `.env`;
render with `.env.example` or generated fake inputs through `just compose-check`.

Set the candidate identity from the verified release, and the current expected identity from its
previous deployment record. Records and snapshots belong outside the checkout:

```bash
CHECKOUT="$(git rev-parse --show-toplevel)"
STATE_DIR="$(cd control-state-data && pwd -P)"
DEPLOYMENT_ROOT="$(dirname "$STATE_DIR")"
RECORDS_DIR="/absolute/private/path/synthkit-deployment-records"
CONTAINER_ID="$(docker compose ps -q synthkit)"
CANDIDATE_REF="ghcr.io/rknightion/synthkit@sha256:<candidate-index>"
CANDIDATE_VERSION="X.Y.Z"
CANDIDATE_REVISION="<40-hex-source-sha>"
PREVIOUS_RECORD="$RECORDS_DIR/current.json"

test -f "$PREVIOUS_RECORD" && test ! -L "$PREVIOUS_RECORD" || exit 1
CURRENT_REF="$(jq -er '.identity.configured_ref | strings | select(length > 0)' "$PREVIOUS_RECORD")"
CURRENT_VERSION="$(jq -er '.identity.version | strings | select(length > 0)' "$PREVIOUS_RECORD")"
CURRENT_REVISION="$(jq -er '.identity.revision | strings | select(test("^[0-9a-f]{40}$"))' "$PREVIOUS_RECORD")"
case "$CURRENT_REF" in
  ghcr.io/rknightion/synthkit@sha256:*) ;;
  *) echo "previous deployment record is not digest-bound" >&2; exit 1 ;;
esac

just compose-check
python3 scripts/synthkit-deploy.py verify-image \
  --reference "$CANDIDATE_REF" --expected-version "$CANDIDATE_VERSION" \
  --expected-oci-version "v$CANDIDATE_VERSION" --expected-revision "$CANDIDATE_REVISION" \
  --source-ref "refs/tags/v$CANDIDATE_VERSION" --platform linux/amd64
```

If the previous record contains a tag rather than an index digest, stop here. Resolve that tag to
its exact index digest, verify the artifact and source identity, and write a replacement
digest-bound record before continuing; `inspect-running` deliberately does not accept a tag.

Inspect the current deployment with its recorded expected values, stop it, and snapshot `/data`
before any selector mutation:

```bash
CURRENT_JSON="$(python3 scripts/synthkit-deploy.py inspect-running \
  --container "$CONTAINER_ID" --expected-reference "$CURRENT_REF" \
  --expected-version "$CURRENT_VERSION" --expected-revision "$CURRENT_REVISION")"
docker compose stop synthkit
SNAPSHOT_JSON="$(python3 scripts/synthkit-deploy.py snapshot-state \
  --state-dir "$STATE_DIR" --records-dir "$RECORDS_DIR" --checkout-root "$CHECKOUT" \
  --name before-upgrade --container "$CONTAINER_ID")"
SNAPSHOT_MANIFEST_SHA="$(jq -r .manifest_sha256 <<<"$SNAPSHOT_JSON")"
```

Write the private deployment record from the closed identity report and snapshot manifest. The
directory is created mode `0700`; records, manifests, and stored files are mode `0600`. The helper
rejects links, devices, sockets, FIFOs, and records inside the checkout.

```bash
python3 scripts/synthkit-deploy.py write-record \
  --records-dir "$RECORDS_DIR" --checkout-root "$CHECKOUT" --name known-good \
  --field "configured_ref=$(jq -r .configured_ref <<<"$CURRENT_JSON")" \
  --field "index_digest=$(jq -r .index_digest <<<"$CURRENT_JSON")" \
  --field "platform_manifest_digest=$(jq -r .platform_manifest_digest <<<"$CURRENT_JSON")" \
  --field "oci_config_digest=$(jq -r .oci_config_digest <<<"$CURRENT_JSON")" \
  --field "running_image_id=$(jq -r .running_image_id <<<"$CURRENT_JSON")" \
  --field "version=$(jq -r .version <<<"$CURRENT_JSON")" \
  --field "revision=$(jq -r .revision <<<"$CURRENT_JSON")" \
  --field "state_manifest_sha256=$(jq -r .manifest_sha256 <<<"$SNAPSHOT_JSON")"
```

Change only `SYNTHKIT_IMAGE_REF` with compare-and-swap, retaining the returned new `.env` hash for
rollback. A concurrent byte, inode, ownership, or mode change aborts rather than restoring unrelated
content:

```bash
ENV_SHA="$(python3 -c 'import hashlib, pathlib; print(hashlib.sha256(pathlib.Path(".env").read_bytes()).hexdigest())')"
SELECTOR_JSON="$(python3 scripts/synthkit-deploy.py set-image \
  --env-file .env --expected-sha256 "$ENV_SHA" --reference "$CANDIDATE_REF")"
ROLLBACK_ENV_SHA="$(jq -r .sha256 <<<"$SELECTOR_JSON")"
docker compose up -d --wait --force-recreate synthkit
CANDIDATE_JSON="$(python3 scripts/synthkit-deploy.py inspect-running \
  --container "$(docker compose ps -q synthkit)" --expected-reference "$CANDIDATE_REF" \
  --expected-version "$CANDIDATE_VERSION" --expected-revision "$CANDIDATE_REVISION")"
```

Finally require public readiness, writable persisted state, authenticated status, and every signal
declared by the selected blueprints after its emission interval plus delivery deadline. Preserve
`ROLLBACK_ENV_SHA`, the exact record hash, and snapshot manifest hash outside shell history or a
public issue. A legacy image without `-version` cannot produce a fully bound record; document that
gap and first upgrade to a versioned known-good target before treating rollback as proven.

## Rollback

Stop the candidate, snapshot its quiesced state, and write its exact identity record before any
restore. This preserves both sides of the tested transition rather than retaining only the old
known-good target. If backward state compatibility is not explicitly proven, then restore the
retained pre-upgrade snapshot before selecting the old exact reference:

```bash
ROLLBACK_CONTAINER_ID="$(docker compose ps -q synthkit)"
docker compose stop synthkit
CANDIDATE_SNAPSHOT_JSON="$(python3 scripts/synthkit-deploy.py snapshot-state \
  --state-dir "$STATE_DIR" --records-dir "$RECORDS_DIR" --checkout-root "$CHECKOUT" \
  --name candidate-after-upgrade --container "$ROLLBACK_CONTAINER_ID")"
python3 scripts/synthkit-deploy.py write-record \
  --records-dir "$RECORDS_DIR" --checkout-root "$CHECKOUT" --name candidate \
  --field "configured_ref=$(jq -r .configured_ref <<<"$CANDIDATE_JSON")" \
  --field "index_digest=$(jq -r .index_digest <<<"$CANDIDATE_JSON")" \
  --field "platform_manifest_digest=$(jq -r .platform_manifest_digest <<<"$CANDIDATE_JSON")" \
  --field "oci_config_digest=$(jq -r .oci_config_digest <<<"$CANDIDATE_JSON")" \
  --field "running_image_id=$(jq -r .running_image_id <<<"$CANDIDATE_JSON")" \
  --field "version=$(jq -r .version <<<"$CANDIDATE_JSON")" \
  --field "revision=$(jq -r .revision <<<"$CANDIDATE_JSON")" \
  --field "state_manifest_sha256=$(jq -r .manifest_sha256 <<<"$CANDIDATE_SNAPSHOT_JSON")"
python3 scripts/synthkit-deploy.py restore-state \
  --state-dir "$STATE_DIR" --expected-root "$DEPLOYMENT_ROOT" \
  --records-dir "$RECORDS_DIR" --name before-upgrade \
  --expected-manifest-sha256 "$SNAPSHOT_MANIFEST_SHA" --container "$ROLLBACK_CONTAINER_ID"
python3 scripts/synthkit-deploy.py set-image \
  --env-file .env --expected-sha256 "$ROLLBACK_ENV_SHA" --reference "$CURRENT_REF"
docker compose up -d --wait --force-recreate synthkit
python3 scripts/synthkit-deploy.py inspect-running \
  --container "$(docker compose ps -q synthkit)" --expected-reference "$CURRENT_REF" \
  --expected-version "$CURRENT_VERSION" --expected-revision "$CURRENT_REVISION"
```

The restore retains the candidate state beside the live tree as an exact
`.control-state-data.displaced-*` directory. Re-run the same readiness, writability, authenticated
status, and declared-signal checks after rollback. Do not automatically remove snapshots, records,
or displaced state.

Restoring uid/gid metadata can require elevated host privileges. If the helper returns
`restore_requires_elevated_privileges`, revalidate every exact path and rerun only the
`restore-state` command with `sudo`; do not run the entire upgrade workflow as root.

After the rollback window, review each exact artifact path and account for backups or encrypted
storage before removal. Arm only one validated target, pause for operator confirmation, then remove
that exact path; never use a wildcard or a broad recursive target:

```bash
REMOVE_TARGET="/absolute/private/path/synthkit-deployment-records/before-upgrade"
test -d "$REMOVE_TARGET" && test ! -L "$REMOVE_TARGET" || exit 1
find "$REMOVE_TARGET" -xdev -print
read -r -p "Remove exactly this retained artifact? [y/N] " answer
[ "$answer" = y ] && rm -r -- "$REMOVE_TARGET"
```

Repeat separately for the exact displaced-state path only after validating it remains beneath the
expected deployment root. Secure erase is storage-dependent; deletion on copy-on-write, SSD, or
backed-up filesystems may not erase every historical block.

---

## Counter resets and rate windows

Container restart = counter reset = a clean `rate()` window in Grafana. This is intentional. No counter-state volume exists or should — synthetic counters restart from zero on each run, which produces a brief stale window in `rate()` queries after a restart but no stale-series accumulation. Plan maintenance windows accordingly or use `increase()` with a long lookback.

---

## See also

- [kubernetes.md](kubernetes.md) — the Helm chart: credential groups, exposure gate, state claim
- [configuration.md](configuration.md) — all environment variables
- [RUNBOOK.md](RUNBOOK.md) — credentials → telemetry end-to-end walkthrough
- [control-plane.md](control-plane.md) — operator UI and HTTP API
