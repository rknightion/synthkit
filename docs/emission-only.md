---
title: Emission-Only Local Run
description: Run synthkit on a macOS or Linux workstation to push synthetic metrics, logs and traces to a Grafana Cloud stack with no Synthetic Monitoring or Fleet Management side effects, then confirm emission from the control plane.
---

# Emission-Only Local Run

Use this page when you want synthetic data in an existing Grafana Cloud stack, for example to give
infrastructure dashboards something to render, and you want nothing else to happen on that stack.
The run delivers metrics, logs and traces for a chosen set of bundled blueprints. It does not
provision Synthetic Monitoring checks, and it does not register or heartbeat Fleet Management
collectors.

It builds on the [Quick Start](quickstart.md): that page previews one blueprint offline and walks
the full first run, and this page changes only what an emission-only operator needs different.

---

## What emission-only means

| Setting | Value | Effect |
|---|---|---|
| `DRY_RUN` | `false` | Delivers to the configured sinks. The shipped default is `true`, which pushes nothing. |
| `STARTUP_DISABLED_KINDS` | `synthetic_monitoring,fleet_management` | Disables both construct kinds from the first tick. |
| `BLUEPRINT_NAMES` | exact names, comma-separated | Loads only the named blueprints. |

With `synthetic_monitoring` in `STARTUP_DISABLED_KINDS`, the Synthetic Monitoring lane is removed
for the lifetime of the process and no provisioning happens. With `fleet_management` listed, Fleet
registration and heartbeats are suspended and no Fleet `alloy_*` series are emitted. The full
semantics, the precedence against the persisted snapshot and the effect of `POST /control/kinds` are
documented once, in [Disabling kinds at startup](control-plane.md#disabling-kinds-at-startup) and
[Disabling Fleet Management from the first tick](fleet-management.md#disabling-fleet-management-from-the-first-tick).
The variable itself is listed in [Configuration](configuration.md).

!!! warning "Do not run Grafana write recipes"
    Three `just` recipes mutate a live Grafana stack: `just provision`, `just selfobs-dashboard`
    and `just corpus-gcx`. None of them is part of an emission-only run. Do not run them.

---

## Example blueprint set

This set suits infrastructure dashboards: Kubernetes, a host fleet, native OTLP, Linux hosts in
Docker, and AWS over native OTLP.

```dotenv
BLUEPRINT_NAMES=k8s-full-stack,hostfleet,otlp-native,hosts-linux-docker,aws-otlp-native
```

These are bundled blueprint names under `blueprints/`. Any other combination of bundled names works
the same way. An unknown name stops startup and lists the names that are available. Keep the set
small: every selected blueprint adds series, and each one is pushed to your stack.

---

## Step 1: Build

Follow [Step 1 of the Quick Start](quickstart.md#step-1-build). The commands are identical on
macOS and Linux and need Bash 5+, which on macOS means a Homebrew Bash ahead of `/bin/bash` on
`PATH` (see [Installation](installation.md#platform-choices)).

```bash
git clone https://github.com/rknightion/synthkit.git
cd synthkit
just ui </dev/null
go build ./cmd/synthkit
```

---

## Step 2: Create the token and `.env`

Create a Grafana Cloud Access Policy token with these scopes, and no others for this run:

- `metrics:write`
- `logs:write`
- `traces:write`

These are the scope names used in [Credentials](credentials.md); that page also lists where to find
the three numeric identifiers the endpoints below need. `profiles:write` is only needed if you
configure a Pyroscope destination, which this run does not.

Create `.env` without replacing an existing file, then add the secret through the hidden-prompt
helper so it never appears in shell history or a process listing:

```bash
if [ -L .env ] || { [ -e .env ] && [ ! -f .env ]; }; then
  echo "refusing symlink or non-file .env" >&2
  exit 1
fi
if [ ! -e .env ]; then
  install -m 600 .env.example .env
else
  echo "Keeping existing .env; review its settings before continuing."
fi
chmod 600 .env
./plugins/synthkit/skills/initial-setup/scripts/add-secret.sh GC_TOKEN .env
./plugins/synthkit/skills/initial-setup/scripts/add-secret.sh CONTROL_TOKEN .env
```

Then set the endpoint and user values with the same placeholders the Quick Start uses. Edit `.env`
so these keys read:

```dotenv
GC_PROM_RW=https://prometheus-prod-XX-<region>.grafana.net/api/prom/push
GC_PROM_USER=<mimir-instance-id>
GC_OTLP_ENDPOINT=https://otlp-gateway-<region>.grafana.net/otlp
GC_OTLP_USER=<stack-id>
GC_LOKI=https://logs-prod-XXX.grafana.net/loki/api/v1/push
GC_LOKI_USER=<loki-instance-id>
BLUEPRINT_NAMES=k8s-full-stack,hostfleet,otlp-native,hosts-linux-docker,aws-otlp-native
STARTUP_DISABLED_KINDS=synthetic_monitoring,fleet_management
DRY_RUN=false
```

Leave every `GC_SM_*` and `GC_FM_*` key empty. Leaving them empty is not enough on its own to stop
Fleet series, which is why `STARTUP_DISABLED_KINDS` is set. Synthkit's own telemetry
(`SELFOBS_ENABLED`) uses a separate credential triplet and stack and is not part of this run; never
reuse `GC_TOKEN` for it.

`CONTROL_TOKEN` is the Basic-auth password (username `control`) for the control plane used in the
verification step. Do not paste either token into a command line or a note.

---

## Step 3: Preview, preflight, then run

Prove the selection loads before anything is delivered. This run is offline, ignores `.env`, and
`-once` exits after one tick:

```bash
DRY_RUN=true SELFOBS_ENABLED=false \
  BLUEPRINT_NAMES=k8s-full-stack,hostfleet,otlp-native,hosts-linux-docker,aws-otlp-native \
  STARTUP_DISABLED_KINDS=synthetic_monitoring,fleet_management \
  ./synthkit -env /dev/null -once
```

The output names the five selected blueprints before any sink can push, as
`selected blueprints: 5 [aws-otlp-native hostfleet hosts-linux-docker k8s-full-stack otlp-native]`,
followed by `control: disabled kinds from first tick: [synthetic_monitoring fleet_management]` and
`[dry-run ...]` summaries. Then check the live endpoints and start the loop:

```bash
./synthkit -preflight
./synthkit
```

`./synthkit` reads `.env` and runs continuously; exported process variables override file values.
Preflight sends empty authenticated requests and must report `ready` for the Prometheus, Loki and
OTLP lanes first. To use Docker Compose instead of the binary, set the same keys in `.env` and
follow the [Compose path](installation.md#docker-compose-recommended-for-standing-deployments); the
variables above are passed through the selected `env_file`.

Stop the foreground process with Ctrl-C.

---

## No backfill, and warm-up

Synthkit emits only from the moment it starts. There is **no backfill**: it never writes samples
with earlier timestamps, so a panel's time range before the process started stays empty, and the
generator is not a way to fill history. A restart does not recover or replay what was missed
(see [checkpoint foundations](checkpoint-foundations.md) for what the optional checkpoint package
does and does not provide).

Panels therefore fill as cumulative series accumulate:

- The master clock ticks every `TICK_DEFAULT`, `5s` by default (see [Configuration](configuration.md)).
  Request traffic is minted on that cadence.
- Metric lanes sample at 60 seconds by default, unless a blueprint explicitly opts into a faster
  `high_dpm.metric_interval`, bounded by `MAX_DPM_PER_SERIES` ([Architecture](architecture.md)).
- Counters and histograms are cumulative across ticks and held in process memory
  (`internal/state`), and the sinks push running totals, never deltas. A restart starts them again
  from zero.

`rate()`, `increase()` and `histogram_quantile()` over a counter or histogram need at least two
samples inside the panel's range. At the default 60 second cadence that is at least two minutes
after start before any such panel shows a value, and a panel with a five minute range keeps filling
until that window holds a full set of samples. Gauge panels show a value after the first push.
Allow roughly five to ten minutes before judging a rate or histogram panel, and expect a panel with
a long range, such as one hour, to look partial until that much time has elapsed since the start.

---

## Confirm emission

Everything below is read-only against the local control plane. None of it touches Grafana.

**Operator UI.** Open [http://127.0.0.1:8088/control/ui](http://127.0.0.1:8088/control/ui) and sign
in with username `control` and your `CONTROL_TOKEN`. The sink-readiness strip shows the last push
result for `promrw`, `loki` and `otlp`; each should show a recent success and no failures.

**Status API.** `curl -u control` prompts for `CONTROL_TOKEN` without echoing it:

```bash
curl -fsS -u control http://127.0.0.1:8088/control/status | jq -e '.dry_run == false'
```

This must print `true`. If `jq` is not installed, run the same request without the pipe and confirm
the response contains `"dry_run":false`.

**Inventory and disabled kinds.** `GET /control/inventory` lists what is declared for each blueprint
and construct, with exact metric names and a safe query identity. `GET /control/state` reports the
effective disabled kinds:

```bash
curl -fsS -u control http://127.0.0.1:8088/control/inventory | jq '.totals, [.blueprints[].blueprint]'
curl -fsS -u control http://127.0.0.1:8088/control/state | jq '.disabled_kinds'
```

Expect `totals.blueprints` to be `5` with the five blueprint names listed, and `disabled_kinds` to be
`["synthetic_monitoring","fleet_management"]`. The inventory is a structural view, so a
`fleet_management` entry can still appear in it; the disabled kind is what stops its ticks, and it
pushes no series. Use the inventory's metric names and identity when you query the stack, rather
than a hand-kept list. Per [Control Plane](control-plane.md#verify-a-selected-blueprint-without-container-shell-access),
wait two declared emission intervals plus the delivery deadline before treating a missing signal as
evidence.

If a lane fails, `GET /control/status` and `GET /control/diagnostics` name it; see
[Troubleshooting](troubleshooting.md).
