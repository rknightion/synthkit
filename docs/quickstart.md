---
title: Quick Start
description: Run one synthetic telemetry demo with Git and Docker, then verify fresh metrics, logs and traces in Grafana.
---

# Quick Start

Run the bundled `otlp-native` example without building synthkit. You need Git, a running Docker
Engine with Docker Compose, a shell and a browser. The supported baseline is Docker Engine 24+
and Compose 2.24.4+. Go, Node.js, `just`, `gcx` and an AI assistant are optional.
The commands below use a POSIX-style shell on Linux or macOS.

Start with an offline preview, then connect your Grafana Cloud stack. This walkthrough is the
complete first-run path; [Installation](installation.md) covers optional source builds and
[Deployment](deployment.md) covers standing deployments.

## 1. Clone and create local configuration

```bash
git clone https://github.com/rknightion/synthkit.git
cd synthkit
docker --version
docker compose version
docker info
```

Docker must be running and reachable from this shell. Create the ignored configuration file
without overwriting an existing file or following a symlink:

```bash
if [ -L .env ] || { [ -e .env ] && [ ! -f .env ]; }; then
  echo "refusing symlink or non-file .env" >&2
  exit 1
fi
if [ ! -e .env ]; then
  if ! (umask 077; set -C; cat .env.example > .env); then
    echo "could not create .env without overwriting another file" >&2
    exit 1
  fi
else
  echo "Keeping existing .env; review it before continuing."
fi
chmod 600 .env
```

Open `.env` in a local text editor and set these existing entries. Keep the published image
reference from `.env.example` for this walkthrough:

```dotenv
BLUEPRINT_NAMES=otlp-native
DRY_RUN=true
SELFOBS_ENABLED=false
SYNTHKIT_BIND=127.0.0.1
```

`BLUEPRINT_NAMES` selects exact runtime names. Empty means setup mode with no synthetic
blueprints loaded; comma-separated names select several; `*` explicitly selects the available
catalogue. Use the exact example name above for this first run.

`DRY_RUN=true` prevents synthetic delivery. `SELFOBS_ENABLED=false` separately disables telemetry
about the synthkit process itself; that path has its own credentials and otherwise operates
independently of dry-run. Leave the optional Synthetic Monitoring, Fleet Management, Git-source,
Faro and profiling credentials empty for this example. Put comments on their own lines in `.env`.

The Compose deployment binds port 8088 to loopback. Keep that default for this walkthrough.
When `CONTROL_TOKEN` is set, the browser asks for username `control` and that token as its password.

## 2. Prepare private storage and preview offline

The container runs as uid 65532. Its mounted state directory must be writable by that uid and
private. This pinned helper prepares the directory using Docker; no host-side Node installation
or `sudo` is needed:

```bash
state_dir="$PWD/control-state-data"
if [ -L "$state_dir" ] || { [ -e "$state_dir" ] && [ ! -d "$state_dir" ]; }; then
  echo "refusing non-directory or symlink state path: $state_dir" >&2
  exit 1
fi
(umask 077; mkdir -p "$state_dir")
docker run --rm --volume "$state_dir:/data" --entrypoint /bin/sh \
  node:24-alpine@sha256:d32cdf619f63fe0471182d08996dd516c6275bb5fd31ae06e55a570bd9e1ad43 \
  -ceu 'chown 65532:65532 /data; chmod 0700 /data'

docker compose config --quiet
docker compose pull synthkit
BLUEPRINT_NAMES=otlp-native docker compose run --rm --no-deps \
  -e DRY_RUN=true -e SELFOBS_ENABLED=false synthkit -once -dump
```

Pulling the images needs network access. The final command derives the selected blueprint's
inventory offline and sends no synthetic or process telemetry. `-once` performs one cycle and
exits; `-dump` prints emitted metric names and labels alongside dry-run summaries. Expect
`selected blueprints: 1 [otlp-native]` and both `otlp-api-enriched` and `otlp-api-naked` in the
output. An unknown blueprint name stops startup and lists the available names.

The published image contains its own bundled blueprints and operator UI. Editing a YAML file in
the checkout does not change the image's `/app/blueprints`. For custom YAML, use the operator UI's
upload or Git-source flow described in [Custom Blueprints](custom-blueprints.md), including its
effective namespaced identity and apply/restart rules. Compose does not automatically mount the
checkout's blueprint directory.

The starter includes the two HTTP services below, Kubernetes telemetry and an agent example.
Optional agent/Sigil status may show missing credentials while the configured HTTP telemetry
lanes are ready. Those optional credentials are not required for this first run. The starter
does not include named incident scenarios; choose a scenario-bearing example later when rehearsing
failure injection.

You can stop here if you only want to inspect the generated signals.

## 3. Connect your Grafana Cloud stack

In your Grafana Cloud account, find the stack's metrics, logs and OpenTelemetry connection
settings. Create a Cloud Access Policy token with `metrics:write`, `logs:write` and `traces:write`
for that stack. The [credential guide](credentials.md#finding-the-three-required-numeric-identifiers)
explains where the three usernames come from; they are different identifiers.

Edit the existing entries in `.env`, keeping `DRY_RUN=true` until preflight succeeds:

```dotenv
GC_TOKEN=<your-stack-scoped-write-token>
GC_PROM_RW=<Prometheus-remote-write-URL>
GC_PROM_USER=<metrics-instance-id>
GC_OTLP_ENDPOINT=<OTLP-gateway-base-URL-ending-in-/otlp>
GC_OTLP_USER=<stack-id>
GC_LOKI=<Loki-push-URL>
GC_LOKI_USER=<logs-instance-id>
BLUEPRINT_NAMES=otlp-native
DRY_RUN=true
SELFOBS_ENABLED=false
```

Replace the placeholders with the values for your destination. Keep the token in `.env`, not in
a shell command, blueprint or shared screenshot. Keep the three endpoint/user pairs together.

Run the image's connection preflight:

```bash
BLUEPRINT_NAMES=otlp-native docker compose run --rm --no-deps \
  -e SELFOBS_ENABLED=false synthkit -preflight
```

Preflight makes authenticated network calls, including empty protocol requests. It does not
start the synthetic emit loop. Continue only when every required destination reports ready.
If it fails, check the endpoint, its matching username and the token's scope before starting live.

## 4. Start live delivery

In the same `.env`, change `DRY_RUN=true` to `DRY_RUN=false`. Keep
`BLUEPRINT_NAMES=otlp-native` and `SELFOBS_ENABLED=false`, then run:

```bash
BLUEPRINT_NAMES=otlp-native docker compose up -d --wait --wait-timeout 180 synthkit
docker compose ps
```

Open [the operator UI](http://127.0.0.1:8088/control/ui) in your browser. It should show the
selected blueprint and live delivery rather than dry-run. The sink strip reports local push
results; inspect any error detail before checking Grafana.

Open [readiness](http://127.0.0.1:8088/control/readiness) in another browser tab. For this selected
live run, check `ready: true`, `setup_required: false`, `live_ready: true` and
`persisted_state.writable: true`. Healthy setup mode with zero loaded blueprints is a different
result and does not establish live delivery.

The master clock defaults to five seconds, but the selected construct/workload metrics collect
at one-minute intervals. Allow a full collection interval plus queue delivery time before judging
initial readiness, and leave the demo running for about five minutes before assessing five-minute
rate or quantile graphs. Trace search can lag ingestion too. Do not raise blueprint rates to make
data appear sooner.

If startup does not become healthy, inspect its recent diagnostic output:

```bash
docker compose logs --tail 100 synthkit
```

## 5. Read back fresh data in Grafana

Open Grafana Explore in your destination stack and select a recent time range covering this run.
Local readiness proves accepted pushes, not backend readback. Confirm fresh sample timestamps,
log timestamps and trace start times after this run started.

In the stack's Prometheus datasource, query:

```promql
http_server_request_duration_seconds_count{service_name="otlp-api-enriched"}
```

Expect non-empty cumulative counts. The native OTLP duration histogram is projected into classic
`_bucket`, `_sum` and `_count` series by the documented receiver. Its active-request instrument
is a gauge:

```promql
http_server_active_requests{service_name="otlp-api-enriched"}
```

The enriched resource's identity is visible separately:

```promql
target_info{service_name="otlp-api-enriched",k8s_cluster_name="otlp-native-prod-euw1"}
```

In the stack's Loki datasource, query:

```logql
{source="app",service_name="otlp-api-enriched",cluster="otlp-native-prod-euw1"}
```

In its Tempo datasource, use TraceQL:

```traceql
{ resource.service.name = "otlp-api-enriched" }
```

Open a returned trace and check its HTTP root span. Repeat the service-name checks for
`otlp-api-naked` to compare its SDK-only resource shape. The naked service has no Kubernetes
resource attributes, so do not require the enriched `target_info` selector to match it.

Missing data remains unverified. Keep emission active while checking the selected blueprint,
datasource, query time range, destination credentials and delivery diagnostics. A successful
preflight, green sink strip or empty successful query alone is not proof that every signal arrived.

To pause the demo while keeping its local configuration and staged blueprints:

```bash
docker compose stop synthkit
```

<a id="step-1-build"></a>

## Optional source builds

To change the generator or its bundled blueprints, follow the source-build path in
[Installation](installation.md). It builds the frontend before the Go binary so the operator UI
is embedded. The published-image walkthrough above needs neither build toolchain.

## Next steps

- [Dashboard tools](tools.md#synthkit-dash-grafana-dashboard-generator) explain generated views and
  their image-version requirements. Grafana Explore is enough to verify this first run.
- [Custom Blueprints](custom-blueprints.md) covers UI uploads, Git sources and runtime selection.
- [Control Plane](control-plane.md) covers the operator UI, scaling and incident controls.
- [Deployment](deployment.md) covers persistent hosts, image identity and network exposure.
- [Verification runbook](RUNBOOK.md) adds deeper optional CLI checks.
- [Contributing](contributing.md) covers source builds and repository changes.
