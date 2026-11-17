# Stock demo quick start

Run the shipped HTTP and MySQL examples, then use Grafana's native application, Kubernetes and
database experiences. After the private setup below, the repeat-run command is:

```bash
cd quickstart
docker compose up -d
```

Start from the repository root. You need Docker Engine 24+, Compose 2.24.4+, a shell, an editor
and a browser signed into your chosen Grafana Cloud sandbox. No host Go, Node.js, `just`, `gcx`
or documentation-query plugin is needed. The [presenter walkthrough](demo.md) targets 7-10 minutes.

## Prepare once, before sharing your screen

Use a new configuration and state directory. Preserve an existing deployment's configuration and
state; follow the [upgrade procedure](../docs/deployment.md#reproducible-upgrade) when changing its
image. This folder reuses the canonical Compose deployment with its own local paths.

From the repository root:

```bash
cd quickstart
if [ -L .env ] || { [ -e .env ] && [ ! -f .env ]; }; then
  echo "Refusing symlink or non-file .env" >&2
  exit 1
fi
if [ ! -e .env ]; then
  (umask 077; set -C; cat .env_example > .env) || exit 1
else
  echo "Keeping existing .env; review it privately."
fi
chmod 600 .env
```

The tracked `.env_example` contains no working credentials. The ignored `.env` is the local runtime
configuration. Compose loads its literal values automatically; a token export is unnecessary.
Keep the example's verified image selection for an unused first deployment, keep `DRY_RUN=true`
during preparation, and leave process self-observability off.

Prepare this folder's private state directory through Docker. The pinned helper changes only the
mount root's owner and mode; it needs no host Node installation or recursive ownership change:

```bash
state_dir="$PWD/control-state-data"
if [ -L "$state_dir" ] || { [ -e "$state_dir" ] && [ ! -d "$state_dir" ]; }; then
  echo "Refusing symlink or non-directory state path" >&2
  exit 1
fi
(umask 077; mkdir -p "$state_dir")
docker run --rm --volume "$state_dir:/data" --entrypoint /bin/sh \
  node:24-alpine@sha256:d32cdf619f63fe0471182d08996dd516c6275bb5fd31ae06e55a570bd9e1ad43 \
  -ceu 'chown 65532:65532 /data; chmod 0700 /data'

docker compose config --quiet
docker compose pull synthkit
docker compose run --rm --no-deps -e DRY_RUN=true -e SELFOBS_ENABLED=false \
  synthkit -env /dev/null -once -dump
```

The preview derives signal inventory without delivering telemetry. Run it before the continuous
emitter: it shares the state mount and may write boot and probe metadata. With an emitter already
running, use its console inventory instead of another shared-state one-shot.

## Connect your sandbox

Privately edit the seven ingest entries in `.env`:

| Setting | Value to obtain from your stack |
| --- | --- |
| `GC_PROM_RW` | Prometheus remote-write push URL. |
| `GC_PROM_USER` | Metrics tenant ID. |
| `GC_OTLP_ENDPOINT` | OTLP gateway base URL, normally ending in `/otlp`. |
| `GC_OTLP_USER` | Grafana Cloud stack ID. |
| `GC_LOKI` | Loki push URL, ending in `/loki/api/v1/push`. |
| `GC_LOKI_USER` | Logs tenant ID. |
| `GC_TOKEN` | Cloud Access Policy token with metrics, logs and traces write scopes. |

The three usernames are different numeric identifiers. The bundled [credential guide](../docs/credentials.md#finding-the-three-required-numeric-identifiers)
shows where to find them in Grafana Cloud and how to obtain the token. Put the literal token only
in your private `.env`, with comments on separate lines. Do not commit or distribute that file.
Clear stale shell overrides for image, selection or configuration variables before a repeat run.

```bash
docker compose run --rm --no-deps synthkit -env /dev/null -preflight
```

Require Prometheus, Loki and OTLP to report Ready. Optional integrations should be intentionally
disabled. After preview and preflight succeed, privately change `DRY_RUN=false` in `.env`.
The file already selects the unchanged `otlp-native,dbo11y-mysql` blueprints.

In Grafana, confirm that your Prometheus and Loki datasources point to this sandbox. Kubernetes
Monitoring needs its standard alert and recording-rule backend installed. Database Observability
may require activation; its UI explains metered database usage and any free tier. The stack owner
must approve that activation before you send database telemetry. No actual database, cluster or
Alloy deployment is needed for these synthetic examples.

For a sandbox without approved Database Observability usage, select only `otlp-native` until that
approval and setup are complete. The full presenter route assumes both examples are selected.

## Start, verify and stop

```bash
docker compose up -d
docker compose ps
```

Open [SynthKit](http://127.0.0.1:8088/control/ui/). Wait for Ready, two active built-in blueprints,
writable state and successful intended sinks. This loopback-only example needs no local UI login
unless you set `CONTROL_TOKEN`; then use username `control` and that token. Compose returning does not prove that Grafana has
received data. Allow several one-minute collection intervals, refresh the native apps' relative
time windows, and follow the [presenter walkthrough](demo.md). If Kubernetes Home still shows
onboarding after fresh data is confirmed, follow the
[cold-open guidance](../docs/troubleshooting.md#native-app-view-stays-empty-after-data-arrives).
Changing the time picker alone may not refresh that discovery state.

In Application Observability's **Logs** tab, select **New Format** for the default image's service
identity. The [log-format guide](../docs/troubleshooting.md#application-logs-new-format-is-empty)
covers fresh readback and the Explore fallback for an older deployment.

The [deployment verification skill](../plugins/synthkit/skills/verify-deployment/SKILL.md) and
[deployment guide](../docs/deployment.md#container-image) cover full immutable image identity and
fresh backend verification. Those stronger checks have additional prerequisites; ordinary Compose
startup is not a substitute for them.

```bash
docker compose stop
```

Keep the local state and configuration for repeat runs. The stock examples are independent:
selecting both does not create an HTTP-to-MySQL dependency. Their fictional identities, traffic
assumptions and supported signals come from the image's bundled blueprints. Editing checkout YAML
does not change the published image; see [custom blueprints](../docs/custom-blueprints.md) when you
are ready to tailor a model.

The `.env` and `control-state-data/` paths are ignored by Git. The entire quick-start folder is
excluded from Docker build contexts, so a local runtime credential does not enter image builds.
